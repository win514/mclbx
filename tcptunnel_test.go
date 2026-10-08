package main

// tcptunnel_test.go 对 TCP 兜底通道做端到端验证（大数据逐字节正确、多连接互不串流）。

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeGameServer 是游戏服务端替身：先回一段握手字节，再全量吐出 payload。
func fakeGameServer(t *testing.T, payload []byte) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				_, _ = c.Write([]byte("HELLO\n"))
				_, _ = c.Write(payload)
				// 等对端读完再关，避免半路截断
				_, _ = io.Copy(io.Discard, bufio.NewReader(c))
			}(c)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close(); wg.Wait() }
}

// startTestRelay 起一个中继并返回地址。
func startTestRelay(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu      sync.Mutex
		waiting = map[string]chan relayPeer{}
	)
	go serveRelay(ln, 20*time.Second, &mu, waiting)
	return ln.Addr().String(), func() { _ = ln.Close() }
}

// fetchN 通过通道取回正好 n 个字节（不能用 io.ReadAll，服务端不会关连接）。
func fetchN(t *testing.T, addr string, n int) ([]byte, error) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		return buf, err
	}
	return buf, nil
}

func TestTCPTunnelCarriesDataByteExact(t *testing.T) {
	payload := payload(8 << 20) // 8 MiB，够触发多帧
	srvAddr, stopSrv := fakeGameServer(t, payload)
	defer stopSrv()
	relayAddr, stopRelay := startTestRelay(t)
	defer stopRelay()

	// 房主侧
	go func() { _ = tcpTunnelHostRun(relayAddr, "roomA", srvAddr) }()
	time.Sleep(200 * time.Millisecond)

	// 玩家侧：自己给监听器以拿到实际端口。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = tcpTunnelGuestOn(relayAddr, "roomA", ln) }()
	time.Sleep(200 * time.Millisecond)

	got, err := fetchN(t, ln.Addr().String(), len("HELLO\n")+len(payload))
	if err != nil {
		t.Fatalf("通过 TCP 通道取数据失败：%v", err)
	}
	body := got[len("HELLO\n"):]
	if !bytes.Equal(body[:len(payload)], payload) {
		srcSum := sha256.Sum256(payload)
		gotSum := sha256.Sum256(body[:len(payload)])
		t.Fatalf("TCP 通道把数据弄坏了：收到 %d 字节，前 %d 字节与源不一致（sha 源=%x 收=%x）",
			len(body), len(payload), srcSum[:8], gotSum[:8])
	}
}

// 并发多连接时连接号绝不能串。
func TestTCPTunnelKeepsConnectionsSeparate(t *testing.T) {
	const conns = 6
	// 每个玩家连接拿到不同内容。
	payloads := make([][]byte, conns)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('A' + i)}, 256*1024)
	}

	// 服务端按接入顺序发第 N 份内容。
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
	go func() { _ = tcpTunnelHostRun(relayAddr, "roomB", ln.Addr().String()) }()
	time.Sleep(200 * time.Millisecond)

	gln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gln.Close()
	go func() { _ = tcpTunnelGuestOn(relayAddr, "roomB", gln) }()
	time.Sleep(200 * time.Millisecond)

	// 同时开 6 条本地连接各自收一份内容。
	var wg sync.WaitGroup
	errs := make([]error, conns)
	got := make([][]byte, conns)
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = fetchN(t, gln.Addr().String(), 256*1024)
		}(i)
	}
	wg.Wait()

	// 每条连接应收完整的一份内容，绝不能混合。
	for i, g := range got {
		if errs[i] != nil {
			t.Fatalf("第 %d 条连接出错：%v", i, errs[i])
		}
		if len(g) < 256*1024 {
			t.Fatalf("第 %d 条只收到 %d 字节", i, len(g))
		}
		head := g[:256*1024]
		matched := false
		for _, p := range payloads {
			if bytes.Equal(head, p) {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("第 %d 条收到的内容不对（前 16 字节 %q）——多路复用串流了", i, head[:16])
		}
	}
}

// 房间码不同时不应配对。
func TestTCPRelayOnlyPairsSameRoom(t *testing.T) {
	relayAddr, stopRelay := startTestRelay(t)
	defer stopRelay()

	c1, _, err := dialRelay(relayAddr, "roomX")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	// 另一房间的连接不该和 roomX 配对。
	c2, _, err := dialRelay(relayAddr, "roomY")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	_ = c1.SetDeadline(time.Now().Add(700 * time.Millisecond))
	buf := make([]byte, 16)
	if _, err := c1.Read(buf); err == nil {
		t.Fatal("不同房间的连接被配到一起了")
	}
}
