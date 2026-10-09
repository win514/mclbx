package main

// signal_listen_test.go 验证对外服务的监听口为双栈（IPv4 与 IPv6 均可连）。
// IPv4 专用监听会静默丢弃 IPv6 连接；用例全部在回环上完成。

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 默认监听地址须为 ":8090"；写成 0.0.0.0 会静默挡掉 IPv6 玩家。
func TestDefaultListenAddrIsWildcard(t *testing.T) {
	if defaultListenAddr != ":8090" {
		t.Fatalf("对外服务的默认监听地址应当是 :8090，实际是 %q", defaultListenAddr)
	}
}

// 源码中不得再出现字符串字面量 "0.0.0.0:8090"（仅扫描带引号的字符串）。
func TestNoHardcodedIPv4OnlyListen(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"0.0.0.0:8090"`) {
			t.Errorf("%s 里写死了 \"0.0.0.0:8090\" —— 那是 IPv4 专用监听，IPv6 的玩家连不上；请用 defaultListenAddr", name)
		}
	}
}

// 用默认写法监听时，IPv4 与 IPv6 都应能连上。
func TestListenSignalAcceptsBothFamilies(t *testing.T) {
	ln, err := listenSignal(":0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	dialOK := func(network, addr string) error {
		c, err := net.DialTimeout(network, addr, 2*time.Second)
		if err == nil {
			_ = c.Close()
		}
		return err
	}

	if err := dialOK("tcp4", net.JoinHostPort("127.0.0.1", port)); err != nil {
		t.Errorf("IPv4 连不上默认监听口：%v", err)
	}
	if err := dialOK("tcp6", net.JoinHostPort("::1", port)); err != nil {
		// 无 IPv6 时跳过，不算失败
		if isNoIPv6Err(err) {
			t.Skipf("本机没有可用的 IPv6 回环，跳过：%v", err)
		}
		t.Errorf("IPv6 连不上默认监听口：%v —— 房主有公网 IPv6 时邀请里就是 IPv6 地址，这条必须通", err)
	}
}

// 验证平台行为：IPv4 专用监听收不到 IPv6 连接。
// 若某平台上 0.0.0.0 也能收 IPv6，则上一条用例不再必需。
func TestIPv4OnlyListenRejectsIPv6(t *testing.T) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Skipf("无法绑定 IPv4 监听：%v", err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	c, err := net.DialTimeout("tcp6", net.JoinHostPort("::1", port), 2*time.Second)
	if err == nil {
		_ = c.Close()
		t.Skip("这个平台上 0.0.0.0 也能收到 IPv6 连接 —— 与仓库既有判断不同，值得记一笔")
	}
	if isNoIPv6Err(err) {
		t.Skipf("本机没有 IPv6 回环：%v", err)
	}
	if !isRefused(err) {
		t.Logf("IPv6 连 IPv4 专用口得到的是 %v（既不是成功也不是拒绝，留意一下）", err)
	}
}

// 端口被占时须当场报错并说明如何处理。
func TestListenSignalReportsPortInUse(t *testing.T) {
	first, err := listenSignal(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, port, _ := net.SplitHostPort(first.Addr().String())

	_, err = listenSignal(":" + port)
	if err == nil {
		t.Fatal("同一个端口居然能绑两次")
	}
	for _, want := range []string{"无法监听", "--signal", "占用"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误里应当提到 %q，实际：%v", want, err)
		}
	}
}

// publicV4Direct 判断本机是否直接位于公网 IPv4。
// 判据：STUN 报回地址等于本机默认出口地址（字符串比较，不误判 CGNAT）。
func TestPublicV4Direct(t *testing.T) {
	if publicV4Direct("") {
		t.Error("空地址不该判成直连")
	}
	if publicV4Direct("0.0.0.0") {
		t.Error("0.0.0.0 不该判成直连")
	}
	if publicV4Direct("203.0.113.9") {
		t.Error("与默认出口不同的地址不该判成直连（CGNAT 下就是这个形状）")
	}
	local := localIPv4()
	if local == "" || local == "0.0.0.0" {
		t.Skip("本机没有默认出口地址，跳过正例")
	}
	if !publicV4Direct(local) {
		t.Errorf("STUN 报回的地址与本机出口一致（%s）时应当判成直连", local)
	}
}

// pickShareIPv6 优先返回非临时的 IPv6 地址（隐私扩展的临时地址会轮换）。
func TestPickShareIPv6PrefersStableAddress(t *testing.T) {
	temp := v6addr{IP: net.ParseIP("2001:db8::ffff"), Temporary: true, IfName: "以太网"}
	stable := v6addr{IP: net.ParseIP("2001:db8::1"), IfName: "以太网"}
	if got := pickShareIPv6([]v6addr{temp, stable}); got != "2001:db8::1" {
		t.Errorf("应当优先非临时地址，实际挑了 %q", got)
	}
	if got := pickShareIPv6([]v6addr{stable, temp}); got != "2001:db8::1" {
		t.Errorf("顺序反过来也应当优先非临时地址，实际 %q", got)
	}
	if got := pickShareIPv6([]v6addr{temp}); got != "2001:db8::ffff" {
		t.Errorf("只有临时地址时也得给出一个（总比没有好），实际 %q", got)
	}
	if got := pickShareIPv6(nil); got != "" {
		t.Errorf("没有地址时应当返回空，实际 %q", got)
	}
}

// shareAddrFor 重算地址卡须与启动时拼接形状一致。
func TestShareAddrForKeepsTheSameShape(t *testing.T) {
	g := &gateState{room: "abc123", entryPort: 25565, addrKind: "v6raw"}
	if got := g.shareAddrFor("2001:db8::1"); got != "2001:db8::1" {
		t.Errorf("默认端口下应当是裸地址，实际 %q", got)
	}
	g.entryPort = 25599
	if got := g.shareAddrFor("2001:db8::1"); got != "[2001:db8::1]:25599" {
		t.Errorf("非默认端口下应当带端口，实际 %q", got)
	}

	g.addrKind, g.dnsSuffix = "v6dns", "sslip.io"
	if got := g.shareAddrFor("2001:db8::1"); got != "abc123.2001-db8--1.sslip.io:25599" {
		t.Errorf("dns 模式应当拼出域名，实际 %q", got)
	}

	// v4 地址来自路由器/STUN，不由网卡决定，此处不重算
	g.addrKind = "v4map"
	if got := g.shareAddrFor("2001:db8::1"); got != "" {
		t.Errorf("v4 地址形式下不该重算，实际 %q", got)
	}
}

// addrFollowsNIC 判断是否需要跟踪网卡 IPv6 变化：仅 v6raw / v6dns 两种形式需要。
func TestAddrFollowsNIC(t *testing.T) {
	cases := map[string]bool{
		"v6raw":    true,
		"v6dns":    true,
		"v4map":    false,
		"v4direct": false,
		"":         false,
	}
	for kind, want := range cases {
		g := &gateState{addrKind: kind}
		if got := g.addrFollowsNIC(); got != want {
			t.Errorf("addrKind=%q 时 addrFollowsNIC() = %v，期望 %v", kind, got, want)
		}
	}
}

func isNoIPv6Err(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no suitable address") ||
		strings.Contains(s, "address family not supported") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "cannot assign requested address")
}

func isRefused(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "refused")
}
