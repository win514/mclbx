package main

// natmap.go —— 主动请求路由器开洞（UPnP IGD / NAT-PMP / PCP）。
//
// 全部失败则如实报错，不伪造成功。

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UPnP 回读/删除时用到的字段
var reNewExternalPort = regexp.MustCompile(`<NewExternalPort>(\d+)</NewExternalPort>`)

// 找不到 IGD 时的统一说法
var errNoSSDP = errors.New("SSDP 无响应，路由器可能未启用 UPnP")

// SSDP 发现做成变量，便于测试指向本机假 IGD。
var upnpDiscoverFunc = upnpDiscover

const (
	natPMPPort = 5351
	// 映射有效期：过短需频繁续约，过长难撤销
	natMapLifetime = 15 * time.Minute
)

// 调试/测试用：把「网关」指到别处（如本机假服务器）；运行时留空走系统路由表。
var (
	natGWOverrideIP   net.IP
	natGWOverridePort int
)

func natGateway() (net.IP, int, error) {
	if natGWOverrideIP != nil {
		p := natGWOverridePort
		if p == 0 {
			p = natPMPPort
		}
		return natGWOverrideIP, p, nil
	}
	ip, err := defaultGateway()
	return ip, natPMPPort, err
}

// natMapping 一次成功的端口映射；release 用于撤销。
type natMapping struct {
	Proto    string // "TCP" / "UDP"
	ExtPort  int
	IntPort  int
	ExtIP    string // 路由器提供的公网地址（可能为空，则用 STUN 结果兜底）
	Method   string // UPnP / NAT-PMP / PCP
	Lifetime time.Duration

	release func() error
}

func (m *natMapping) Release() {
	if m == nil || m.release == nil {
		return
	}
	_ = m.release()
}

// Addr 返回玩家该连的地址（IPv6 加括号以便拼接端口）。
func (m *natMapping) Addr() string {
	if m == nil || m.ExtIP == "" {
		return ""
	}
	if strings.Contains(m.ExtIP, ":") {
		return "[" + m.ExtIP + "]"
	}
	return m.ExtIP
}

// 统一入口

// natMapPlan 一次开洞尝试的完整结果（可能多个协议各开一条）
type natMapPlan struct {
	Mappings []*natMapping
	Tried    []string // 每种方式的尝试结论，给用户看
}

func (p *natMapPlan) Release() {
	for _, m := range p.Mappings {
		m.Release()
	}
	p.Mappings = nil
}

// ExtIP 返回这一批映射的公网地址（取第一个拿到的）
func (p *natMapPlan) ExtIP() string {
	for _, m := range p.Mappings {
		if m.ExtIP != "" {
			return m.ExtIP
		}
	}
	return ""
}

// openNatMapping 按 UPnP → NAT-PMP → PCP 顺序尝试，返回第一个成功的。
func openNatMapping(proto string, intPort, wantExtPort int, lifetime time.Duration) (*natMapping, []string) {
	var tried []string
	if lifetime <= 0 {
		lifetime = natMapLifetime
	}

	if m, err := upnpMap(proto, intPort, wantExtPort, lifetime); err == nil {
		tried = append(tried, fmt.Sprintf("UPnP：映射成功，外网端口 %d", m.ExtPort))
		return m, tried
	} else {
		tried = append(tried, "UPnP："+err.Error())
	}

	if m, err := natpmpMap(proto, intPort, wantExtPort, lifetime); err == nil {
		tried = append(tried, fmt.Sprintf("NAT-PMP：映射成功，外网端口 %d", m.ExtPort))
		return m, tried
	} else {
		tried = append(tried, "NAT-PMP："+err.Error())
	}

	if m, err := pcpMap(proto, intPort, wantExtPort, lifetime); err == nil {
		tried = append(tried, fmt.Sprintf("PCP：映射成功，外网端口 %d", m.ExtPort))
		return m, tried
	} else {
		tried = append(tried, "PCP："+err.Error())
	}

	return nil, tried
}

// UPnP

func upnpMap(proto string, intPort, wantExtPort int, lifetime time.Duration) (*natMapping, error) {
	location, err := upnpDiscoverFunc(1200 * time.Millisecond)
	if err != nil {
		return nil, err
	}
	ctrl, extIP, err := upnpExternalIP(location)
	if err != nil {
		return nil, err
	}
	extPort := wantExtPort
	if extPort <= 0 {
		extPort = intPort
	}
	// 端口被占用时小幅退让（25565 → +1 → +2）。
	tryPorts := []int{extPort, extPort + 1, extPort + 2}
	var lastErr error
	for _, p := range tryPorts {
		if err := upnpAddPortMapping(ctrl, extIP, p, intPort, proto, "mclbx", int(lifetime.Seconds())); err != nil {
			lastErr = err
			continue
		}
		// 回读规则，确认生效并取得实际端口
		got, err := upnpPortMappingEntry(ctrl, p, proto)
		if err != nil || got == 0 {
			got = p // 部分路由器不支持回读，按被接受的请求取值
		}
		m := &natMapping{
			Proto: proto, ExtPort: got, IntPort: intPort, ExtIP: extIP,
			Method: "UPnP", Lifetime: lifetime,
		}
		m.release = func() error {
			return upnpDeletePortMapping(ctrl, got, proto)
		}
		return m, nil
	}
	if lastErr == nil {
		lastErr = errors.New("路由器拒绝了映射请求")
	}
	return nil, lastErr
}

// 回读一条端口映射，取得实际分配的外网端口并确认规则生效
func upnpPortMappingEntry(controlURL string, extPort int, proto string) (int, error) {
	body := fmt.Sprintf(`<u:GetSpecificPortMappingEntry xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">`+
		`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort>`+
		`<NewProtocol>%s</NewProtocol></u:GetSpecificPortMappingEntry>`, extPort, proto)
	out, err := upnpSOAP(controlURL, "urn:schemas-upnp-org:service:WANIPConnection:1", "GetSpecificPortMappingEntry", body)
	if err != nil {
		return 0, err
	}
	if m := reNewExternalPort.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, nil
	}
	return 0, errors.New("回读结果里没有端口")
}

func upnpDeletePortMapping(controlURL string, extPort int, proto string) error {
	body := fmt.Sprintf(`<u:DeletePortMapping xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">`+
		`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort>`+
		`<NewProtocol>%s</NewProtocol></u:DeletePortMapping>`, extPort, proto)
	_, err := upnpSOAP(controlURL, "urn:schemas-upnp-org:service:WANIPConnection:1", "DeletePortMapping", body)
	return err
}

// NAT-PMP

// natpmpMap 按 RFC 6886 请求网关开通端口（请求 12 字节 / 应答 16 字节）。
func natpmpMap(proto string, intPort, wantExtPort int, lifetime time.Duration) (*natMapping, error) {
	gw, gwPort, err := natGateway()
	if err != nil {
		return nil, err
	}
	op, err := natpmpOpcode(proto)
	if err != nil {
		return nil, err
	}
	if wantExtPort <= 0 {
		wantExtPort = intPort
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: gw, Port: gwPort})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := make([]byte, 12)
	req[1] = op
	binary.BigEndian.PutUint16(req[4:], uint16(intPort))
	binary.BigEndian.PutUint16(req[6:], uint16(wantExtPort))
	binary.BigEndian.PutUint32(req[8:], uint32(lifetime.Seconds()))

	buf := make([]byte, 32)
	n := 0
	// UDP 会丢包，NAT-PMP 要求重传，给三次机会
	for attempt := 0; attempt < 3; attempt++ {
		_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}
		n, err = conn.Read(buf)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, errors.New("网关无响应，可能不支持 NAT-PMP")
	}
	if n < 16 {
		return nil, fmt.Errorf("应答长度不足 %d 字节", n)
	}
	if buf[1]&0x7F != op {
		return nil, fmt.Errorf("应答操作码不匹配 %d", buf[1])
	}
	if res := binary.BigEndian.Uint16(buf[2:]); res != 0 {
		return nil, fmt.Errorf("网关拒绝：%s", natpmpResultText(res))
	}
	gotPort := int(binary.BigEndian.Uint16(buf[10:]))
	life := time.Duration(binary.BigEndian.Uint32(buf[12:])) * time.Second
	if gotPort == 0 {
		return nil, errors.New("网关返回 0 端口")
	}

	m := &natMapping{
		Proto: proto, ExtPort: gotPort, IntPort: intPort,
		Method: "NAT-PMP", Lifetime: life,
	}
	m.release = func() error {
		conn2, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: gw, Port: gwPort})
		if err != nil {
			return err
		}
		defer conn2.Close()
		_ = conn2.SetDeadline(time.Now().Add(2 * time.Second))
		r := make([]byte, 12)
		r[1] = op
		binary.BigEndian.PutUint16(r[4:], uint16(intPort)) // 内网端口
		binary.BigEndian.PutUint16(r[6:], uint16(gotPort)) // 要撤掉的外网端口
		binary.BigEndian.PutUint32(r[8:], 0)               // 生命周期 0 = 撤销映射
		_, err = conn2.Write(r)
		return err
	}
	return m, nil
}

func natpmpOpcode(proto string) (byte, error) {
	switch strings.ToUpper(proto) {
	case "UDP":
		return 1, nil
	case "TCP":
		return 2, nil
	}
	return 0, fmt.Errorf("NAT-PMP 不支持 %s", proto)
}

func natpmpResultText(code uint16) string {
	switch code {
	case 1:
		return "设备不支持该操作"
	case 2:
		return "路由器策略拒绝"
	case 3:
		return "网络故障"
	case 4:
		return "资源不足"
	case 5:
		return "端口不被允许"
	}
	return fmt.Sprintf("错误码 %d", code)
}

// PCP

// pcpMap 按 RFC 6887 请求 MAP（version=2；请求头 24 字节 + MAP 数据 36 字节）。
func pcpMap(proto string, intPort, wantExtPort int, lifetime time.Duration) (*natMapping, error) {
	gw, gwPort, err := natGateway()
	if err != nil {
		return nil, err
	}
	protoNum, err := pcpProtocol(proto)
	if err != nil {
		return nil, err
	}
	if wantExtPort <= 0 {
		wantExtPort = intPort
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	req := make([]byte, 24+36)
	req[0] = 2 // version
	req[1] = 1 // R=0(请求) + opcode=1(MAP)
	binary.BigEndian.PutUint32(req[4:], uint32(lifetime.Seconds()))
	copy(req[8:24], net.IPv4zero.To16())
	copy(req[24:36], nonce)
	req[36] = protoNum
	binary.BigEndian.PutUint16(req[40:], uint16(intPort))
	binary.BigEndian.PutUint16(req[42:], uint16(wantExtPort))
	copy(req[44:60], net.IPv4zero.To16())

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: gw, Port: gwPort})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}

	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, errors.New("网关无响应，可能不支持 PCP")
	}
	if n < 60 {
		return nil, fmt.Errorf("应答长度不足 %d 字节", n)
	}
	if buf[0] != 2 {
		return nil, fmt.Errorf("版本不匹配 %d", buf[0])
	}
	if result := buf[3]; result != 0 {
		return nil, fmt.Errorf("网关拒绝：%s", pcpResultText(result))
	}
	// 回执必须带本次随机数，否则可能是别的软件的应答
	if !bytes.Equal(buf[24:36], nonce) {
		return nil, errors.New("回执随机数与本次请求不匹配")
	}
	gotPort := int(binary.BigEndian.Uint16(buf[42:]))
	life := time.Duration(binary.BigEndian.Uint32(buf[4:])) * time.Second
	if gotPort == 0 {
		return nil, errors.New("网关返回 0 端口")
	}
	extIP := net.IP(buf[44:60])
	ipStr := ""
	if !extIP.Equal(net.IPv4zero) && extIP.To4() != nil {
		ipStr = extIP.String()
	}

	m := &natMapping{
		Proto: proto, ExtPort: gotPort, IntPort: intPort, ExtIP: ipStr,
		Method: "PCP", Lifetime: life,
	}
	m.release = func() error {
		conn2, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: gw, Port: gwPort})
		if err != nil {
			return err
		}
		defer conn2.Close()
		_ = conn2.SetDeadline(time.Now().Add(2 * time.Second))
		r := make([]byte, len(req))
		copy(r, req)
		binary.BigEndian.PutUint32(r[4:], 0) // 生命周期 0 = 撤销
		_, err = conn2.Write(r)
		return err
	}
	return m, nil
}

func pcpProtocol(proto string) (byte, error) {
	switch strings.ToUpper(proto) {
	case "UDP":
		return 17, nil
	case "TCP":
		return 6, nil
	}
	return 0, fmt.Errorf("PCP 不支持 %s", proto)
}

func pcpResultText(code byte) string {
	switch code {
	case 1:
		return "版本不支持"
	case 2:
		return "操作不支持"
	case 3:
		return "参数不被接受"
	case 4:
		return "资源不足"
	case 5:
		return "协议不被支持"
	case 6:
		return "用户超额"
	case 7:
		return "不允许建立映射"
	case 8:
		return "地址族不支持"
	}
	return fmt.Sprintf("错误码 %d", code)
}

// 网关探测

var gwCache struct {
	sync.Mutex
	ip  net.IP
	err error
	at  time.Time
}

// defaultGateway 查默认网关；Go 标准库无此 API，按平台读取路由表。
func defaultGateway() (net.IP, error) {
	gwCache.Lock()
	if time.Since(gwCache.at) < 30*time.Second && (gwCache.err != nil || gwCache.ip != nil) {
		ip, err := gwCache.ip, gwCache.err
		gwCache.Unlock()
		return ip, err
	}
	gwCache.Unlock()

	ip, err := readDefaultGateway()

	gwCache.Lock()
	gwCache.ip, gwCache.err, gwCache.at = ip, err, time.Now()
	gwCache.Unlock()
	return ip, err
}

func readDefaultGateway() (net.IP, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("route", "print", "-4").Output()
		if err != nil {
			return nil, errors.New("读不到 IPv4 路由表")
		}
		return firstGatewayFromRoutePrint(string(out))
	case "linux":
		if b, err := os.ReadFile("/proc/net/route"); err == nil {
			return firstGatewayFromProcRoute(string(b))
		}
	}
	// 兜底：netstat（Linux 没 /proc 时、以及 macOS）
	out, err := exec.Command("netstat", "-rn").Output()
	if err != nil {
		return nil, errors.New("读不到路由表")
	}
	return firstGatewayFromNetstat(string(out))
}

// firstGatewayFromRoutePrint 从 Windows route print -4 输出取默认网关。
func firstGatewayFromRoutePrint(out string) (net.IP, error) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 3 && f[0] == "0.0.0.0" && f[1] == "0.0.0.0" {
			if ip := net.ParseIP(f[2]); ip != nil && ip.To4() != nil {
				return ip, nil
			}
		}
	}
	return nil, errors.New("路由表里没有默认网关")
}

// firstGatewayFromProcRoute 从 /proc/net/route 取默认网关（网关为十六进制小端）。
func firstGatewayFromProcRoute(out string) (net.IP, error) {
	for i, line := range strings.Split(out, "\n") {
		if i == 0 {
			continue // 表头
		}
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == "00000000" {
			v, err := strconv.ParseUint(f[2], 16, 32)
			if err != nil {
				continue
			}
			ip := net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
			if !ip.Equal(net.IPv4zero) {
				return ip, nil
			}
		}
	}
	return nil, errors.New("路由表里没有默认网关")
}

func firstGatewayFromNetstat(out string) (net.IP, error) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i, tok := range f {
			if tok == "default" && i+1 < len(f) {
				if ip := net.ParseIP(strings.Trim(f[i+1], "[]")); ip != nil && ip.To4() != nil {
					return ip, nil
				}
			}
		}
	}
	return nil, errors.New("路由表里没有默认网关")
}
