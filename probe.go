package main

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type v6addr struct {
	IP        net.IP
	IfName    string
	Temporary bool // 启发式：/128 一般是隐私扩展出来的临时地址
}

// globalIPv6 只返回全球可路由的 IPv6（2000::/3），排除链路本地/回环/ULA。
func globalIPv6() []v6addr {
	var out []v6addr
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip.To4() != nil { // 只要 IPv6
				continue
			}
			if !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
				continue
			}
			b := ip.To16()
			if b == nil {
				continue
			}
			if b[0]&0xfe == 0xfc { // ULA fc00::/7
				continue
			}
			if b[0]&0xe0 != 0x20 { // 只要 2000::/3
				continue
			}
			s := ip.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			ones, _ := ipnet.Mask.Size()
			out = append(out, v6addr{IP: ip, IfName: ifi.Name, Temporary: ones == 128})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return !out[i].Temporary && out[j].Temporary
	})
	return out
}

// ipv6DNSLabel 转为 sslip.io 风格标签：':' -> '-'（故 '::' -> '--'）。
func ipv6DNSLabel(ip net.IP) string {
	return strings.ReplaceAll(ip.String(), ":", "-")
}

// stunProbeV6 用 IPv6 socket 询问 STUN，验证公网 IPv6 能否出站；只能证明出站可达，调用方应视为警告。
func stunProbeV6(servers []string, timeout time.Duration) (stunResult, bool) {
	conn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: 0})
	if err != nil {
		return stunResult{}, false // 系统根本没有 IPv6 栈
	}
	defer conn.Close()
	for _, s := range servers {
		dst, err := net.ResolveUDPAddr("udp6", s)
		if err != nil {
			continue // 这台服务器没有 AAAA 记录
		}
		r, err := stunExchange(conn, dst, timeout)
		if err != nil || r.IP.To4() != nil || r.IP.To16() == nil {
			continue
		}
		return r, true
	}
	return stunResult{}, false
}

// 只实现 Binding 请求 + XOR-MAPPED-ADDRESS 解析（RFC 5389 的一个子集）。

const stunMagic = 0x2112A442

type stunResult struct {
	Server string
	IP     net.IP
	Port   int
	RTT    time.Duration
}

// stunExchange 完成一次 STUN Binding 事务；conn 须为未 connect 的 UDP socket，以便跨服务器比对映射端口。
func stunExchange(conn *net.UDPConn, dst *net.UDPAddr, timeout time.Duration) (stunResult, error) {
	res := stunResult{Server: dst.String()}
	var txid [12]byte
	if _, err := crand.Read(txid[:]); err != nil {
		return res, err
	}

	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(req[2:], 0)      // 无属性
	binary.BigEndian.PutUint32(req[4:], stunMagic)
	copy(req[8:], txid[:])

	t0 := time.Now()
	if _, err := conn.WriteToUDP(req, dst); err != nil {
		return res, err
	}
	_ = conn.SetReadDeadline(t0.Add(timeout))

	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return res, err
		}
		// 只接受目标服务器且事务号匹配的响应；共享 socket 时其他服务器的迟到响应须跳过。
		if !from.IP.Equal(dst.IP) || from.Port != dst.Port {
			continue
		}
		if n < 20 || binary.BigEndian.Uint16(buf[0:]) != 0x0101 {
			continue
		}
		if binary.BigEndian.Uint32(buf[4:]) != stunMagic {
			continue
		}
		if !bytes.Equal(buf[8:20], txid[:]) {
			continue
		}
		res.RTT = time.Since(t0)
		ip, port, ok := parseXORMapped(buf[:n], txid)
		if !ok {
			return res, errors.New("响应里没有 XOR-MAPPED-ADDRESS")
		}
		res.IP, res.Port = ip, port
		return res, nil
	}
}

// stunQuery 新建 socket 查询单台 STUN 服务器；跨服务器比对端口请用 stunProbeShared。
func stunQuery(server string, timeout time.Duration) (stunResult, error) {
	dst, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return stunResult{Server: server}, err
	}
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return stunResult{Server: server}, err
	}
	defer conn.Close()
	return stunExchange(conn, dst, timeout)
}

// stunProbeShared 用同一个 socket 依次查询多台 STUN 服务器，返回各自报回的映射。
// 端口全一致为锥形，各不相同为对称型；仅绑 IPv4，避免与 IPv6 出口混比。
func stunProbeShared(servers []string, timeout time.Duration) []stunResult {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil
	}
	defer conn.Close()

	var out []stunResult
	for _, s := range servers {
		dst, err := net.ResolveUDPAddr("udp4", s)
		if err != nil {
			continue
		}
		r, err := stunExchange(conn, dst, timeout)
		if err != nil || r.IP.To4() == nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// parseXORMapped 从 STUN 响应里取出 XOR-MAPPED-ADDRESS 的地址与端口。
func parseXORMapped(buf []byte, txid [12]byte) (net.IP, int, bool) {
	n := len(buf)
	p := 20
	for p+4 <= n {
		atype := binary.BigEndian.Uint16(buf[p:])
		alen := int(binary.BigEndian.Uint16(buf[p+2:]))
		p += 4
		if p+alen > n {
			return nil, 0, false
		}
		if atype == 0x0020 && alen >= 8 { // XOR-MAPPED-ADDRESS
			fam := buf[p+1]
			port := int(binary.BigEndian.Uint16(buf[p+2:])) ^ (stunMagic >> 16)
			switch fam {
			case 0x01: // IPv4
				ip := make(net.IP, 4)
				binary.BigEndian.PutUint32(ip, binary.BigEndian.Uint32(buf[p+4:])^stunMagic)
				return ip, port, true
			case 0x02: // IPv6
				if alen < 20 {
					break
				}
				ip := make(net.IP, 16)
				mask := make([]byte, 16)
				binary.BigEndian.PutUint32(mask, stunMagic)
				copy(mask[4:], txid[:])
				for i := 0; i < 16; i++ {
					ip[i] = buf[p+4+i] ^ mask[i]
				}
				return ip, port, true
			}
		}
		p += (alen + 3) &^ 3 // 属性按 4 字节对齐
	}
	return nil, 0, false
}

func defaultStunServers() []string {
	return []string{
		"stun.miwifi.com:3478",
		"stun.chat.bilibili.com:3478",
		"stun.hitv.com:3478",
		"stun.l.google.com:19302",
	}
}

var reControlURL = regexp.MustCompile(`(?s)<service>\s*<serviceType>urn:schemas-upnp-org:service:WANIPConnection:\d</serviceType>.*?<controlURL>(.*?)</controlURL>`)

// upnpDiscover 通过 SSDP 查找 IGD 并返回描述文件地址。
func upnpDiscover(timeout time.Duration) (string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return "", err
	}
	defer conn.Close()

	dst := &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}
	msg := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 2\r\n" +
		"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n\r\n"
	if _, err := conn.WriteToUDP([]byte(msg), dst); err != nil {
		return "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return "", errNoSSDP
		}
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if len(line) > 9 && strings.EqualFold(line[:9], "LOCATION:") {
				return strings.TrimSpace(line[9:]), nil
			}
		}
	}
}

func upnpSOAP(controlURL, service, action, body string) (string, error) {
	env := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>` +
		body + `</s:Body></s:Envelope>`

	req, err := http.NewRequest("POST", controlURL, strings.NewReader(env))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", fmt.Sprintf("%q", service+"#"+action))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("SOAP 失败：HTTP %d %s", resp.StatusCode, firstLine(string(out)))
	}
	return string(out), nil
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i > 0 {
		return s[:i]
	}
	return s
}

// upnpExternalIP 获取控制点地址与路由器 WAN IP。
func upnpExternalIP(location string) (controlURL string, extIP string, err error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(location)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	xmlBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	m := reControlURL.FindStringSubmatch(string(xmlBytes))
	if m == nil {
		return "", "", errors.New("描述文件里没有 WANIPConnection 服务")
	}
	ctrl := m[1]
	if strings.HasPrefix(ctrl, "/") {
		base, perr := url.Parse(location)
		if perr != nil {
			return "", "", perr
		}
		ctrl = base.Scheme + "://" + base.Host + ctrl
	}
	body := `<u:GetExternalIPAddress xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/>`
	out, err := upnpSOAP(ctrl, "urn:schemas-upnp-org:service:WANIPConnection:1", "GetExternalIPAddress", body)
	if err != nil {
		return ctrl, "", err
	}
	re := regexp.MustCompile(`<NewExternalIPAddress>(.*?)</NewExternalIPAddress>`)
	if mm := re.FindStringSubmatch(out); mm != nil {
		extIP = mm[1]
	}
	return ctrl, extIP, nil
}

// upnpAddPortMapping 请求路由器建立一条端口映射。
func upnpAddPortMapping(controlURL, extIP string, extPort, intPort int, proto, desc string, ttl int) error {
	if proto == "" {
		proto = "TCP"
	}
	body := fmt.Sprintf(`<u:AddPortMapping xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">`+
		`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort>`+
		`<NewProtocol>%s</NewProtocol><NewInternalPort>%d</NewInternalPort>`+
		`<NewInternalClient>%s</NewInternalClient><NewEnabled>1</NewEnabled>`+
		`<NewPortMappingDescription>%s</NewPortMappingDescription>`+
		`<NewLeaseDuration>%d</NewLeaseDuration></u:AddPortMapping>`,
		extPort, proto, intPort, localIPv4(), desc, ttl)
	_, err := upnpSOAP(controlURL, "urn:schemas-upnp-org:service:WANIPConnection:1", "AddPortMapping", body)
	return err
}

// publicV4Direct 判断本机是否直接位于公网 IPv4：STUN 出口地址与 localIPv4 相同即无 NAT。
// 用「相等」而非「是否为公网地址」，可避免把 CGNAT 误判为直连。
func publicV4Direct(stunIP string) bool {
	if stunIP == "" {
		return false
	}
	local := localIPv4()
	if local == "" || local == "0.0.0.0" {
		return false
	}
	return stunIP == local
}

// localIPv4 返回本机默认出口的 IPv4。
func localIPv4() string {
	c, err := net.Dial("udp", "8.8.8.8:53") // 不会真的发包，只让系统走一遍路由选择
	if err != nil {
		return "0.0.0.0"
	}
	defer c.Close()
	if ua, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return ua.IP.String()
	}
	return "0.0.0.0"
}

// natVerdict 判断 NAT 出站映射行为：端口全一致为锥形、全不同为对称型、部分相同为不稳定、仅一台应答无法判定。
func natVerdict(rs []stunResult) string {
	if len(rs) == 0 {
		return "未知"
	}
	ports := map[int]struct{}{}
	for _, r := range rs {
		ports[r.Port] = struct{}{}
	}
	ip := rs[0].IP
	switch {
	case len(rs) < 2:
		return fmt.Sprintf("公网映射 %s:%d，仅 1 台服务器应答，无法判定映射行为，建议稍后重试", ip, rs[0].Port)
	case len(ports) == 1:
		return fmt.Sprintf("%d 台服务器看到一致映射 %s:%d，全锥形 NAT，映射与目标无关",
			len(rs), ip, rs[0].Port)
	case len(ports) == len(rs):
		return fmt.Sprintf("%d 台服务器看到 %d 个不同端口（例如 %s:%d），对称型 NAT，更换目标即更换端口，不利于直连",
			len(rs), len(ports), ip, rs[0].Port)
	default:
		return fmt.Sprintf("%d 台服务器仅看到 %d 个端口（例如 %s:%d），映射行为不稳定，介于全锥形与对称型之间",
			len(rs), len(ports), ip, rs[0].Port)
	}
}

func randomRoomCode(n int) string {
	const ab = "abcdefghjkmnpqrstuvwxyz23456789" // 去掉形近字
	b := make([]byte, n)
	for i := range b {
		b[i] = ab[mrand.IntN(len(ab))]
	}
	return string(b)
}

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	useStun := fs.Bool("stun", true, "执行 STUN 探测")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Printf("==== %s · 网络探测 ====\n", version)

	v6 := globalIPv6()
	if len(v6) == 0 {
		fmt.Println("公网 IPv6      : 无")
	} else {
		fmt.Printf("公网 IPv6      : %d 个\n", len(v6))
		for _, a := range v6 {
			tag := ""
			if a.Temporary {
				tag = " [临时]"
			}
			fmt.Printf("   %s%s  [%s]\n", a.IP, tag, a.IfName)
		}
	}

	if *useStun {
		// 展示与判定须用同一批数据，均取自同一个 socket 的那一轮探测。
		v4 := stunProbeShared(defaultStunServers(), 1500*time.Millisecond)
		for _, r := range v4 {
			fmt.Printf("STUN %-28s : %s:%d  %d ms\n", r.Server, r.IP, r.Port, r.RTT.Milliseconds())
		}
		fmt.Printf("NAT 类型       : %s\n", natVerdict(v4))
		// IPv6 出口须另起一轮并显式走 udp6：不同地址族不能共用 socket，network "udp" 会挑 IPv4。
		if r, ok := stunProbeV6(defaultStunServers(), 1500*time.Millisecond); ok {
			fmt.Printf("IPv6 出口      : %s，IPv6 无 NAT，即本机地址\n", r.IP)
		} else if len(globalIPv6()) > 0 {
			fmt.Printf("IPv6 出口      : 无应答 —— 网卡上有 IPv6 地址，但未必出得去\n")
		}
	}

	loc, err := upnpDiscover(1500 * time.Millisecond)
	if err != nil {
		fmt.Printf("路由器 UPnP    : 不可用，%v\n", err)
	} else {
		ctrl, ext, err2 := upnpExternalIP(loc)
		if err2 != nil {
			fmt.Printf("路由器 UPnP    : 可用，但读取 WAN 地址失败，%v\n", err2)
		} else {
			fmt.Printf("路由器 UPnP    : 可用，WAN IP = %s\n", ext)
			fmt.Printf("                 控制点 %s，可进行自动端口映射\n", ctrl)
		}
	}
	return nil
}
