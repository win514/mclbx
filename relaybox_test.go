package main

// relaybox_test.go —— 联机点分享链接与端到端转发验证。

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRelayboxShareLinkIsSelfContained(t *testing.T) {
	b := &relaybox{
		turnPort: 3478, tcpPort: 8090, signalPort: 8091,
		room: "abc123", secret: "s3cret",
		addr:   relayAddrInfo{Addr: "1.2.3.4", Public: true},
		turnUp: true, tcpUp: true, sigUp: true,
	}
	link := b.shareLink()
	for _, want := range []string{"turn=3478", "tcp=8090", "sig=8091", "room=abc123", "secret=s3cret"} {
		if !strings.Contains(link, want) {
			t.Errorf("链接里少了 %s：%s", want, link)
		}
	}

	plan, err := resolveRelayLink(link)
	if err != nil {
		t.Fatalf("自己生成的链接都解析不了：%v", err)
	}
	if plan.Room != "abc123" {
		t.Errorf("房间码没跟着链接走：%q", plan.Room)
	}

	// 不给账号密码也应能算出中继凭据
	urls, errs := plan.TurnURLs("", "")
	if len(errs) != 0 || len(urls) == 0 {
		t.Fatalf("链接里带着密钥，就不该再要用户填账号：errs=%v urls=%d", errs, len(urls))
	}
	if urls[0].Username == "" || urls[0].Password == "" {
		t.Errorf("临时凭据没算出来：%+v", urls[0])
	}
}

func TestRelayboxShareLinkBracketsIPv6(t *testing.T) {
	b := &relaybox{
		tcpPort: 8090, room: "r", secret: "k",
		addr:  relayAddrInfo{Addr: "2001:db8:1111:2223::1", Public: true},
		tcpUp: true,
	}
	link := b.shareLink()
	if !strings.Contains(link, "mclbx://[2001:db8:1111:2223::1]?") {
		t.Fatalf("IPv6 地址没加方括号，端口就分不出来了：%s", link)
	}
	plan, err := resolveRelayLink(link)
	if err != nil {
		t.Fatalf("解析不了：%v", err)
	}
	if got := plan.TCPAddr(); got != "[2001:db8:1111:2223::1]:8090" {
		t.Errorf("TCP 地址取出来不对：%q", got)
	}
}

func TestDetectRelayAddrTakesExplicit(t *testing.T) {
	got := detectRelayAddr("1.2.3.4")
	if got.Addr != "1.2.3.4" || !got.Public || got.Mapped {
		t.Errorf("手填的地址应当原样使用、且当成公网地址：%+v", got)
	}
	// 方括号是 IPv6 在链接里的写法，取出来时要脱掉
	if v6 := detectRelayAddr("[2408::1]"); v6.Addr != "2408::1" {
		t.Errorf("IPv6 方括号没脱掉：%+v", v6)
	}
}

func TestUDPNetworkPicksFamily(t *testing.T) {
	cases := []struct {
		addr          string
		network, host string
	}{
		{"1.2.3.4:3478", "udp4", "0.0.0.0"},
		{"[2408::1]:3478", "udp6", "::"},
		{"relay.example.com:3478", "udp4", "0.0.0.0"},
	}
	for _, c := range cases {
		n, h := udpNetworkAndHost(c.addr)
		if n != c.network || h != c.host {
			t.Errorf("%s：拿到 %s/%s，想要 %s/%s", c.addr, n, h, c.network, c.host)
		}
	}
}

func TestTCPTunnelRoomComesFromLink(t *testing.T) {
	link := "mclbx://1.2.3.4?tcp=8090&room=abc123&secret=k"
	if got := tcpTunnelRoom(link, ""); got != "abc123" {
		t.Errorf("房间码没从链接里取出来：%q", got)
	}
	if got := tcpTunnelRoom(link, "手填的"); got != "手填的" {
		t.Errorf("手填的房间码应当优先：%q", got)
	}
	if got := tcpTunnelRoom("tcp:1.2.3.4:8090", ""); got != "" {
		t.Errorf("链接里没有房间码时应当返回空（由调用方提示）：%q", got)
	}
}

func TestRandomKeyAvoidsConfusableChars(t *testing.T) {
	k := randomKey(40)
	if len(k) != 40 {
		t.Fatalf("长度不对：%q", k)
	}
	for _, bad := range []string{"0", "o", "1", "l", "i"} {
		if strings.Contains(k, bad) {
			t.Errorf("随机串里出现了容易看错的字符 %q：%s", bad, k)
		}
	}
}

// 从分享链接出发，房主与玩家各自连接，逐字节校验游戏流量。
func TestRelayboxTCPCarriesGameTraffic(t *testing.T) {
	payload := payload(1 << 20) // 1 MiB：触发多帧读写
	srvAddr, stopSrv := fakeGameServer(t, payload)
	defer stopSrv()

	// 先占端口取号，再交给中继监听
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	b := &relaybox{
		tcpPort: port, room: "relaycheckroom", secret: "k",
		addr: relayAddrInfo{Addr: "127.0.0.1", Kind: "本机内网地址", LAN: "127.0.0.1"},
	}
	if err := b.startTCP(); err != nil {
		t.Fatalf("中继没起来：%v", err)
	}
	defer b.close()

	link := b.shareLink()
	room := tcpTunnelRoom(link, "") // 房间码必须能从链接里取出来
	if room == "" {
		t.Fatalf("链接里没带房间码：%s", link)
	}
	relayAddr, err := resolveTCPRelayAddr(link, "")
	if err != nil {
		t.Fatalf("从链接里取中继地址失败：%v", err)
	}

	go func() { _ = tcpTunnelHostRun(relayAddr, room, srvAddr) }()
	time.Sleep(200 * time.Millisecond)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = tcpTunnelGuestOn(relayAddr, room, ln) }()
	time.Sleep(200 * time.Millisecond)

	got, err := fetchN(t, ln.Addr().String(), len("HELLO\n")+len(payload))
	if err != nil {
		t.Fatalf("通过自建中继取数据失败：%v", err)
	}
	if !bytes.Equal(got[len("HELLO\n"):], payload) {
		t.Fatal("自建中继把游戏数据弄坏了（逐字节比对不一致）")
	}
}
