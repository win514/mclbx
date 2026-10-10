package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// upnp_test.go 测试真机上没法验证的分支（例如用假 IGD 覆盖 AddPortMapping）。

// 假的 IGD：收下 SOAP 请求并回成功响应。
func TestUpnpAddPortMapping(t *testing.T) {
	var gotAction, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAction = r.Header.Get("SOAPAction")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope><s:Body><u:AddPortMappingResponse/></s:Body></s:Envelope>`))
	}))
	defer srv.Close()

	// 用固定内网地址，使断言稳定。
	err := upnpAddPortMapping(srv.URL+"/ctl", "1.2.3.4", 25565, 25566, "TCP", "mclbx", 3600)
	if err != nil {
		t.Fatalf("AddPortMapping 出错: %v", err)
	}
	if !strings.Contains(gotAction, "AddPortMapping") {
		t.Errorf("SOAPAction 不对: %q", gotAction)
	}
	for _, want := range []string{
		"<NewExternalPort>25565</NewExternalPort>",
		"<NewInternalPort>25566</NewInternalPort>",
		"<NewProtocol>TCP</NewProtocol>",
		"mclbx",
	} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("报文里缺少 %s\n报文：%s", want, gotBody)
		}
	}
}

// 全球 IPv6 筛选：排除回环 / 链路本地 / ULA。
func TestIPv6Filtering(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"2001:db8:1111:2222:3333:4444:5555:6670", true},
		{"::1", false},
		{"fe80::1", false},
		{"fd00::1", false},
		{"192.168.1.1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip).To16()
		got := ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() &&
			ip[0]&0xfe != 0xfc && ip[0]&0xe0 == 0x20
		if got != c.want {
			t.Errorf("%s 期望 %v，得到 %v", c.ip, c.want, got)
		}
	}
}

// sslip.io 的地址编码规则：':' -> '-'，'::' -> '--'
func TestIPv6DNSLabel(t *testing.T) {
	cases := map[string]string{
		"2001:4860:4860::8888":                   "2001-4860-4860--8888",
		"2001:db8:1111:2222:3333:4444:5555:6670": "2001-db8-1111-2222-3333-4444-5555-6670",
	}
	for in, want := range cases {
		if got := ipv6DNSLabel(net.ParseIP(in)); got != want {
			t.Errorf("ipv6DNSLabel(%s) = %q，期望 %q", in, got, want)
		}
	}
}
