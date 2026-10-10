package main

// natmap_test.go 覆盖端口映射（UPnP/NAT-PMP/PCP）测试，用本机假网关覆盖三条路径。

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// NAT-PMP

// fakeNatPMP 起一个假网关：按 RFC 6886 应答，行为可配
func fakeNatPMP(t *testing.T, result uint16, extPort uint16, capture *[]byte) (addr *net.UDPAddr, stop func()) {
	t.Helper()
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("起不了假网关：%v", err)
	}
	go func() {
		buf := make([]byte, 64)
		for {
			n, peer, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := append([]byte(nil), buf[:n]...)
			if capture != nil {
				*capture = req
			}
			if n < 12 {
				continue
			}
			resp := make([]byte, 16)
			resp[0] = 0
			resp[1] = req[1] | 0x80
			binary.BigEndian.PutUint16(resp[2:], result)
			binary.BigEndian.PutUint32(resp[4:], 12345) // 纪元
			binary.BigEndian.PutUint16(resp[8:], binary.BigEndian.Uint16(req[4:]))
			binary.BigEndian.PutUint16(resp[10:], extPort)
			binary.BigEndian.PutUint32(resp[12:], binary.BigEndian.Uint32(req[8:]))
			_, _ = pc.WriteToUDP(resp, peer)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr), func() { _ = pc.Close() }
}

func useFakeGateway(t *testing.T, addr *net.UDPAddr) {
	t.Helper()
	oldIP, oldPort := natGWOverrideIP, natGWOverridePort
	natGWOverrideIP, natGWOverridePort = addr.IP, addr.Port
	t.Cleanup(func() { natGWOverrideIP, natGWOverridePort = oldIP, oldPort })
}

func TestNatPMPMapSucceeds(t *testing.T) {
	var captured []byte
	addr, stop := fakeNatPMP(t, 0, 45000, &captured)
	defer stop()
	useFakeGateway(t, addr)

	m, err := natpmpMap("UDP", 25565, 25565, 10*time.Minute)
	if err != nil {
		t.Fatalf("映射应当成功：%v", err)
	}
	if m.Method != "NAT-PMP" || m.ExtPort != 45000 || m.IntPort != 25565 {
		t.Fatalf("映射内容不对：%+v", m)
	}
	if m.Lifetime != 10*time.Minute {
		t.Errorf("有效期应当是请求的那个：%v", m.Lifetime)
	}

	// 校验请求报文：版本 0、操作码 1（UDP）、内网端口、建议外网端口、有效期
	if len(captured) != 12 || captured[0] != 0 || captured[1] != 1 {
		t.Fatalf("请求报文头不对：% x", captured)
	}
	if got := binary.BigEndian.Uint16(captured[4:]); got != 25565 {
		t.Errorf("请求里的内网端口不对：%d", got)
	}
	if got := binary.BigEndian.Uint32(captured[8:]); got != 600 {
		t.Errorf("请求里的有效期不对：%d 秒", got)
	}

	m.Release()
}

func TestNatPMPMapTCPUsesOpcodeTwo(t *testing.T) {
	var captured []byte
	addr, stop := fakeNatPMP(t, 0, 25565, &captured)
	defer stop()
	useFakeGateway(t, addr)

	if _, err := natpmpMap("TCP", 25565, 25565, time.Minute); err != nil {
		t.Fatalf("TCP 映射应当成功：%v", err)
	}
	if captured[1] != 2 {
		t.Fatalf("TCP 映射的操作码应当是 2，实际 %d", captured[1])
	}
}

func TestNatPMPMapExplainsRefusal(t *testing.T) {
	addr, stop := fakeNatPMP(t, 2, 0, nil) // 2 = 路由器拒绝
	defer stop()
	useFakeGateway(t, addr)

	_, err := natpmpMap("UDP", 25565, 25565, time.Minute)
	if err == nil {
		t.Fatal("被拒绝时不该装作成功——玩家连不上比不给地址更糟")
	}
	if !strings.Contains(err.Error(), "拒绝") {
		t.Errorf("错误信息该说清原因，实际：%v", err)
	}
}

func TestNatPMPMapSilentlyUnsupportedWithoutGateway(t *testing.T) {
	// 指向无监听端口：必须快速失败
	useFakeGateway(t, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1})
	start := time.Now()
	_, err := natpmpMap("UDP", 25565, 25565, time.Minute)
	if err == nil {
		t.Fatal("没有网关时必须报错")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("失败要快，实际花了 %v", d)
	}
}

// PCP

func fakePCP(t *testing.T, result byte, extPort uint16, extIP net.IP, capture *[]byte) (addr *net.UDPAddr, stop func()) {
	t.Helper()
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("起不了假网关：%v", err)
	}
	go func() {
		buf := make([]byte, 256)
		for {
			n, peer, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := append([]byte(nil), buf[:n]...)
			if capture != nil {
				*capture = req
			}
			if n < 60 || req[0] != 2 {
				continue // 非 PCP 请求（NAT-PMP 会先到达）
			}
			resp := make([]byte, 60)
			resp[0] = 2
			resp[1] = req[1] | 0x80
			resp[3] = result
			binary.BigEndian.PutUint32(resp[4:], binary.BigEndian.Uint32(req[4:]))
			copy(resp[24:36], req[24:36]) // 原样回随机数
			resp[36] = req[36]
			binary.BigEndian.PutUint16(resp[42:], extPort)
			copy(resp[44:60], extIP.To16())
			_, _ = pc.WriteToUDP(resp, peer)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr), func() { _ = pc.Close() }
}

func TestPCPMapSucceeds(t *testing.T) {
	var captured []byte
	addr, stop := fakePCP(t, 0, 41000, net.IPv4(203, 0, 113, 9), &captured)
	defer stop()
	useFakeGateway(t, addr)

	m, err := pcpMap("UDP", 25565, 25565, 5*time.Minute)
	if err != nil {
		t.Fatalf("PCP 映射应当成功：%v", err)
	}
	if m.Method != "PCP" || m.ExtPort != 41000 {
		t.Fatalf("映射内容不对：%+v", m)
	}
	if m.ExtIP != "203.0.113.9" {
		t.Errorf("该把网关告诉我们的公网地址带上，实际 %q", m.ExtIP)
	}
	if len(captured) < 60 || captured[0] != 2 || captured[1] != 1 {
		t.Fatalf("PCP 请求头不对：% x", captured[:8])
	}
	if captured[36] != 17 {
		t.Errorf("UDP 的协议号应当是 17，实际 %d", captured[36])
	}
	if got := binary.BigEndian.Uint16(captured[40:]); got != 25565 {
		t.Errorf("请求里的内网端口不对：%d", got)
	}
	m.Release()
}

func TestPCPMapExplainsRefusal(t *testing.T) {
	addr, stop := fakePCP(t, 7, 0, net.IPv4zero, nil) // 7 = 不允许建立映射
	defer stop()
	useFakeGateway(t, addr)

	_, err := pcpMap("UDP", 25565, 25565, time.Minute)
	if err == nil {
		t.Fatal("被拒绝时不该装作成功")
	}
	if !strings.Contains(err.Error(), "不允许") {
		t.Errorf("错误信息该说清原因，实际：%v", err)
	}
}

func TestPCPRejectsMismatchedNonce(t *testing.T) {
	// 回一个别的请求的随机数：必须拒收，否则会拿到别人的端口
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 256)
		for {
			n, peer, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 60 || buf[0] != 2 {
				continue
			}
			resp := make([]byte, 60)
			resp[0] = 2
			resp[1] = buf[1] | 0x80
			for i := 24; i < 36; i++ {
				resp[i] = 0xAA // 随机数故意写错
			}
			binary.BigEndian.PutUint16(resp[42:], 1234)
			_, _ = pc.WriteToUDP(resp, peer)
		}
	}()
	useFakeGateway(t, pc.LocalAddr().(*net.UDPAddr))

	if _, err := pcpMap("UDP", 25565, 25565, time.Minute); err == nil {
		t.Fatal("随机数对不上时必须拒收")
	}
}

// UPnP

// TestUpnpMapEndToEnd 用假 IGD 走通 UPnP 全流程：发现 → 取外网地址 → 加映射 → 回读 → 删映射。
func TestUpnpMapEndToEnd(t *testing.T) {
	var actions []string
	var internalPort, extPort string
	igdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 描述文件，从中取出控制点地址
		if strings.HasSuffix(r.URL.Path, "desc.xml") {
			_, _ = w.Write([]byte(`<root><device><serviceList><service>` +
				`<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>` +
				`<controlURL>/ctl</controlURL></service></serviceList></device></root>`))
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		soap := r.Header.Get("SOAPAction")
		switch {
		case strings.Contains(soap, "GetExternalIPAddress"):
			actions = append(actions, "GetExternalIPAddress")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope><s:Body>` +
				`<u:GetExternalIPAddressResponse><NewExternalIPAddress>203.0.113.7</NewExternalIPAddress>` +
				`</u:GetExternalIPAddressResponse></s:Body></s:Envelope>`))
		case strings.Contains(soap, "AddPortMapping"):
			actions = append(actions, "AddPortMapping")
			s := string(body)
			extPort = between(s, "<NewExternalPort>", "</NewExternalPort>")
			internalPort = between(s, "<NewInternalPort>", "</NewInternalPort>")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope><s:Body><u:AddPortMappingResponse/></s:Body></s:Envelope>`))
		case strings.Contains(soap, "GetSpecificPortMappingEntry"):
			actions = append(actions, "GetSpecificPortMappingEntry")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope><s:Body>` +
				`<u:GetSpecificPortMappingEntryResponse><NewExternalPort>` + extPort +
				`</NewExternalPort></u:GetSpecificPortMappingEntryResponse></s:Body></s:Envelope>`))
		case strings.Contains(soap, "DeletePortMapping"):
			actions = append(actions, "DeletePortMapping")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope><s:Body><u:DeletePortMappingResponse/></s:Body></s:Envelope>`))
		default:
			http.Error(w, "unknown action", 500)
		}
	}))
	defer igdp.Close()

	// 把 SSDP 发现结果指向上面的 HTTP 服务
	oldDiscover := upnpDiscoverFunc
	upnpDiscoverFunc = func(time.Duration) (string, error) { return igdp.URL + "/desc.xml", nil }
	t.Cleanup(func() { upnpDiscoverFunc = oldDiscover })

	m, err := upnpMap("TCP", 25565, 25565, 15*time.Minute)
	if err != nil {
		t.Fatalf("UPnP 映射应当成功：%v", err)
	}
	if m.Method != "UPnP" || m.ExtPort != 25565 || m.ExtIP != "203.0.113.7" {
		t.Fatalf("映射内容不对：%+v", m)
	}
	if internalPort != "25565" {
		t.Errorf("内网端口不对：%s", internalPort)
	}
	if m.Addr() != "203.0.113.7" {
		t.Errorf("玩家该连的地址不对：%s", m.Addr())
	}

	m.Release()
	want := []string{"GetExternalIPAddress", "AddPortMapping", "GetSpecificPortMappingEntry", "DeletePortMapping"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Errorf("调用顺序不对：%v（期望 %v）", actions, want)
	}
}

func TestUpnpMapFallsBackWhenPortTaken(t *testing.T) {
	// 第一次请求端口被占，第二档端口应被用上
	var tries []string
	igdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "desc.xml") {
			_, _ = w.Write([]byte(`<root><device><serviceList><service>` +
				`<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>` +
				`<controlURL>/ctl</controlURL></service></serviceList></device></root>`))
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		soap := r.Header.Get("SOAPAction")
		switch {
		case strings.Contains(soap, "GetExternalIPAddress"):
			_, _ = w.Write([]byte(`<s:Envelope><s:Body><NewExternalIPAddress>203.0.113.7</NewExternalIPAddress></s:Body></s:Envelope>`))
		case strings.Contains(soap, "AddPortMapping"):
			s := string(raw)
			p := between(s, "<NewExternalPort>", "</NewExternalPort>")
			tries = append(tries, p)
			if p == "25565" {
				http.Error(w, "ConflictInMappingEntry", 500)
				return
			}
			_, _ = w.Write([]byte(`<s:Envelope><s:Body><u:AddPortMappingResponse/></s:Body></s:Envelope>`))
		case strings.Contains(soap, "GetSpecificPortMappingEntry"):
			_, _ = w.Write([]byte(`<s:Envelope><s:Body><NewExternalPort>25566</NewExternalPort></s:Body></s:Envelope>`))
		default:
			_, _ = w.Write([]byte(`<s:Envelope><s:Body/></s:Envelope>`))
		}
	}))
	defer igdp.Close()

	oldDiscover := upnpDiscoverFunc
	upnpDiscoverFunc = func(time.Duration) (string, error) { return igdp.URL + "/desc.xml", nil }
	t.Cleanup(func() { upnpDiscoverFunc = oldDiscover })

	m, err := upnpMap("TCP", 25565, 25565, time.Minute)
	if err != nil {
		t.Fatalf("退让到 25566 应当成功：%v", err)
	}
	if m.ExtPort != 25566 {
		t.Errorf("应当退到 25566，实际 %d", m.ExtPort)
	}
	if len(tries) < 2 || tries[0] != "25565" || tries[1] != "25566" {
		t.Errorf("尝试的端口顺序不对：%v", tries)
	}
}

// 汇总入口

func TestOpenNatMappingReportsEveryAttempt(t *testing.T) {
	// UPnP 不可用、NAT-PMP 可用：应落到 NAT-PMP，并报出 UPnP 的结论。
	addr, stop := fakeNatPMP(t, 0, 48000, nil)
	defer stop()
	useFakeGateway(t, addr)

	oldDiscover := upnpDiscoverFunc
	upnpDiscoverFunc = func(d time.Duration) (string, error) {
		return "", errNoSSDP
	}
	t.Cleanup(func() { upnpDiscoverFunc = oldDiscover })

	m, tried := openNatMapping("UDP", 25565, 25565, time.Minute)
	if m == nil || m.Method != "NAT-PMP" {
		t.Fatalf("应当落到 NAT-PMP：%+v", m)
	}
	if len(tried) < 2 {
		t.Fatalf("应当把每次尝试的结论都报出来：%v", tried)
	}
	if !strings.HasPrefix(tried[0], "UPnP：") {
		t.Errorf("第一条应当说明 UPnP 的结论：%v", tried)
	}
	m.Release()
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}
