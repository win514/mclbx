package main

// extreme_test.go 极端场景测试：慢速连接、资源上限、协议注入、并发规模。
//
// 每条用例描述无防护时的表现。

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 握手读取必须有超时，否则可被 slowloris 占用一个 goroutine 与最多 1 MiB 内存。
func TestExposeSlowHandshakeIsBounded(t *testing.T) {
	g := &gateState{room: "abc123"}
	cli, srv := net.Pipe()
	defer cli.Close()

	done := make(chan struct{})
	go func() {
		handleConn(srv, g, true, nil)
		close(done)
	}()

	// varint(1 MiB) = 0x80 0x80 0x40：声称 1048576 字节包体，只发长度头。
	go func() { _, _ = cli.Write([]byte{0x80, 0x80, 0x40}) }()

	select {
	case <-done:
		// 超时兜住，连接已释放
	case <-time.After(handshakeReadTimeout + 5*time.Second):
		t.Fatalf("慢速握手连接被一直挂着（%v 内 handleConn 没返回）："+
			"每个这样的连接占 1 个 goroutine + 最多 1 MiB 内存，而房间码校验要等整包读完才生效 —— "+
			"任何人都能零成本地把这道门堵死", handshakeReadTimeout)
	}
}

// DNS over TCP 有 10 秒绝对时限，此用例将其固定住。
func TestDNSTCPHalfOpenIsBounded(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	cli, srv := net.Pipe()
	defer cli.Close()

	done := make(chan struct{})
	go func() {
		handleDNSTCP(srv, z)
		close(done)
	}()

	// 声称 32 字节查询，只发 1 字节后挂起
	go func() { _, _ = cli.Write([]byte{0x00, 0x20, 0x01}) }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("DNS over TCP 的半开连接没有被超时清掉 —— 每条这样的连接都会一直占着一个 goroutine")
	}
}

// 长度 0 或超过 4096 的帧须立即关闭连接，且不影响后续正常查询。
func TestDNSTCPRejectsBadLengthImmediately(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	recs, err := recordsForRoom("room1.mclbx.test", net.ParseIP("127.0.0.1"), 25566, "", 30, "")
	if err != nil {
		t.Fatal(err)
	}
	z.setRecords(recs)

	cases := []struct {
		name string
		pre  []byte
	}{
		{"长度 0", []byte{0x00, 0x00}},
		{"长度 5000（>4096 上限）", []byte{0x13, 0x88}},
	}
	for _, c := range cases {
		cli, srv := net.Pipe()
		done := make(chan struct{})
		go func() {
			handleDNSTCP(srv, z)
			close(done)
		}()
		go func() { _, _ = cli.Write(c.pre) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s：没有立刻关闭连接", c.name)
		}
		cli.Close()
	}

	// 换一条正常连接，验证仍能正确应答
	cli, srv := net.Pipe()
	defer cli.Close()
	go handleDNSTCP(srv, z)
	q := buildTestQuery(0x4321, "room1.mclbx.test", dnsTypeSRV)
	frame := append([]byte{byte(len(q) >> 8), byte(len(q))}, q...)
	go func() { _, _ = cli.Write(frame) }()

	_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(cli)
	hdr, err := br.Peek(2)
	if err != nil {
		t.Fatalf("正常查询没有拿到应答：%v", err)
	}
	n := int(hdr[0])<<8 | int(hdr[1])
	if n < 12 {
		t.Fatalf("应答长度前缀不合理：%d", n)
	}
}

// mailboxSpecSides 为信箱上限的规格值，独立于生产常量以免测试随实现放宽。
const mailboxSpecSides = 1024

// /blob/<side> 的键由请求方决定，必须有条数上限以防耗尽。
func TestMailboxHostilePostsStayBounded(t *testing.T) {
	mb := newMailbox()
	srv := httptest.NewServer(mb)
	defer srv.Close()

	const body = `{"ufrag":"u","pwd":"p","cands":["1.2.3.4:1"],"room":"r1"}`

	// 一、先顺序填满信箱，保证"满"确定发生
	posted := 0
	for i := 0; i < mailboxSpecSides*2; i++ {
		err := postBlob(srv.URL, fmt.Sprintf("fill-%05d", i), candBlob{Room: "r1"}, 5*time.Second)
		if err != nil {
			if !strings.Contains(err.Error(), "503") {
				t.Fatalf("灌第 %d 个键时出的不是「满了」，而是别的错：%v", i, err)
			}
			break
		}
		posted++
	}
	// 校验生产常量未超过规格上限
	if posted != mailboxSpecSides {
		t.Fatalf("第 %d 个新键才被挡住，期望正好是规格上限 %d（信箱上限没生效，或者被改动过）",
			posted+1, mailboxSpecSides)
	}
	if mailboxMaxSides > mailboxSpecSides {
		t.Fatalf("mailboxMaxSides=%d 超出了规格上限 %d：改大它就等于把这条耗尽路径放回来了",
			mailboxMaxSides, mailboxSpecSides)
	}

	// 二、满了以后新键必须被拒（503）
	resp, err := http.Post(srv.URL+"/blob/atk-brand-new", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("信箱满了之后新键应当被拒（503），实际 %d", resp.StatusCode)
	}

	// 三、满了以后已有键仍可覆盖
	if err := postBlob(srv.URL, "fill-00000", candBlob{Ufrag: "u2", Pwd: "p", Room: "r1"}, 3*time.Second); err != nil {
		t.Errorf("已有键的覆盖写不该被容量限制挡住：%v", err)
	}

	// 四、并发写入不同键，上限仍须守住
	const hits = 2500
	var wg sync.WaitGroup
	for i := 0; i < hits; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := http.Post(srv.URL+"/blob/atk-"+strconv.Itoa(i), "application/json", strings.NewReader(body))
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()

	if got := len(mb.m); got > mailboxSpecSides {
		t.Fatalf("%d 次不同的 POST 之后信箱里堆了 %d 条（规格上限 %d）："+
			"键不设上限 = 一条零成本的耗尽路径，而且每个请求还会刷一行日志",
			hits, got, mailboxSpecSides)
	}

	// 五、超长键必须被拒（400）
	longKey := strings.Repeat("k", mailboxMaxKey+1)
	resp2, err := http.Post(srv.URL+"/blob/"+longKey, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("超长键（%d 字节）应当被拒（400），实际 %d", len(longKey), resp2.StatusCode)
	}

	// 六、超大 body 要被拒（1 MiB 上限）
	big := bytes.Repeat([]byte{'x'}, 2<<20)
	resp3, err := http.Post(srv.URL+"/blob/somebody", "application/json", bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp3.Body)
	_ = resp3.Body.Close()
	if resp3.StatusCode < 400 {
		t.Errorf("2 MiB 的 body 应当被拒，实际 %d", resp3.StatusCode)
	}
}

// 房间名会拼进 "MCLBX1 <房间名>\n" 供中继按行读取，必须拒绝控制字符。
func TestRelayRoomNameRejectsControlChars(t *testing.T) {
	relayAddr, stopRelay := startTestRelay(t)
	defer stopRelay()

	bad := []struct {
		why  string
		name string
	}{
		{"换行会撕开行协议", "room1\nX"},
		{"回车同上", "room1\rX"},
		{"NUL 也会进到写出去的那行里", "room\x001"},
		{"超长", strings.Repeat("r", 200)},
		{"空白", "   "},
	}
	for _, c := range bad {
		conn, _, err := dialRelay(relayAddr, c.name)
		if err == nil {
			_ = conn.Close()
			t.Errorf("房间名 %q 被接受了（%s）—— 它会被原样拼进握手行", c.name, c.why)
		}
	}
}

// 房间名须对大小写和首尾空白归一，两种写法应匹配到同一房间。
func TestRelayRoomNameNormalizesBothSides(t *testing.T) {
	relayAddr, stopRelay := startTestRelay(t)
	defer stopRelay()

	c1, _, err := dialRelay(relayAddr, "ROOM1")
	if err != nil {
		t.Fatalf("ROOM1 应当被接受并归一化：%v", err)
	}
	defer c1.Close()
	c2, _, err := dialRelay(relayAddr, "  room1  ")
	if err != nil {
		t.Fatalf("\"  room1  \" 应当被接受并归一化：%v", err)
	}
	defer c2.Close()

	// 配对后中继原样转发字节
	if _, err := c1.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c2, buf); err != nil {
		t.Fatalf("两个写法没能配到同一个房间（大小写/空白没归一化）：%v", err)
	}
	if string(buf) != "ping\n" {
		t.Fatalf("转发内容不对：%q", buf)
	}
}

// 60 条并发连接验证多路复用不串流，并记录 goroutine 数（不断言）。
func TestConcurrentTunnelAtScale(t *testing.T) {
	const conns = 60
	const chunk = 32 << 10

	payloads := make([][]byte, conns)
	for i := range payloads {
		// 每份内容带序号，串流即可发现
		payloads[i] = bytes.Repeat([]byte{byte('A' + i%26)}, chunk)
	}

	// 假服务端：按接入顺序发第 N 份内容
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	n := 0
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			idx := n
			n++
			mu.Unlock()
			go func(c net.Conn, idx int) {
				defer c.Close()
				if idx < len(payloads) {
					_, _ = c.Write(payloads[idx])
				}
				_, _ = io.Copy(io.Discard, bufio.NewReader(c))
			}(c, idx)
		}
	}()

	relayAddr, stopRelay := startTestRelay(t)
	defer stopRelay()
	go func() { _ = tcpTunnelHostRun(relayAddr, "scale", ln.Addr().String()) }()
	time.Sleep(300 * time.Millisecond)

	gln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gln.Close()
	go func() { _ = tcpTunnelGuestOn(relayAddr, "scale", gln) }()
	time.Sleep(300 * time.Millisecond)

	before := runtime.NumGoroutine()

	var wg sync.WaitGroup
	got := make([][]byte, conns)
	errs := make([]error, conns)
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = fetchN(t, gln.Addr().String(), chunk)
		}(i)
	}
	wg.Wait()

	bad := 0
	for i, g := range got {
		if errs[i] != nil {
			t.Fatalf("第 %d 条连接出错：%v", i, errs[i])
		}
		if len(g) < chunk {
			t.Fatalf("第 %d 条只收到 %d 字节", i, len(g))
		}
		matched := false
		for _, p := range payloads {
			if bytes.Equal(g[:chunk], p) {
				matched = true
				break
			}
		}
		if !matched {
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d/%d 条连接收到的内容不是任何一整份（多路复用串流了）", bad, conns)
	}

	time.Sleep(2 * time.Second)
	t.Logf("并发 %d 条：全部逐字节正确；goroutine %d → %d（只作参考，不断言）",
		conns, before, runtime.NumGoroutine())
}

// readFrame 须在分配前拒绝不合理长度，并快速失败。
func TestReadFrameHugeLengthRejectedWithoutAllocating(t *testing.T) {
	for _, c := range []struct {
		name string
		hdr  []byte
	}{
		{"声称 2^31 附近", []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{"负数（-1 的 varint）", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{"6 字节 continuation", []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01}},
		{"零长度", []byte{0x00}},
	} {
		start := time.Now()
		_, err := readFrame(bufio.NewReader(bytes.NewReader(c.hdr)), 1<<20)
		if err == nil {
			t.Errorf("%s：应当报错", c.name)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s：失败得太慢（%v）—— 说明它先去读/分配了", c.name, d)
		}
	}

	// 合法小包仍须能读出
	body, err := readFrame(bufio.NewReader(bytes.NewReader([]byte{0x05, 'h', 'e', 'l', 'l', 'o'})), 1<<20)
	if err != nil || string(body) != "hello" {
		t.Fatalf("正常的小帧应当能读出来：body=%q err=%v", body, err)
	}
}
