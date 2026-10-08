package main

// capacity_test.go —— 各条转发路径的软件侧吞吐上限基准。

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
)

// benchBlock 是每次迭代搬运的字节数，取 1 MiB。
const benchBlock = 1 << 20

// benchEcho 起一个回显服务端替身，返回地址与端口。
func benchEcho(b *testing.B) (addr string, port int) {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64<<10)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.String(), a.Port
}

// BenchmarkEntryForward 测量公网入口的转发（expose.go 的 handleConn 里的两个 io.Copy）。
//
// SetBytes 取 2×：客户端写 1 MiB、服务端回 1 MiB，入口两个方向都搬。
func BenchmarkEntryForward(b *testing.B) {
	echoAddr, echoPort := benchEcho(b)
	g := &gateState{room: "abc123", gamePort: echoPort, started: time.Now()}
	g.openUpstream = func() (io.ReadWriteCloser, error) { return net.Dial("tcp", echoAddr) }

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(c, g, true, nil)
		}
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))

	// 门卫要读一个带房间码的握手包；先把替身回吐的回声读干净再计时。
	hs := buildHandshake(-1, "abc123.example", 25565, 1)
	var hsFrame bytes.Buffer
	if err := writeFrame(&hsFrame, hs); err != nil {
		b.Fatal(err)
	}
	if _, err := c.Write(hsFrame.Bytes()); err != nil {
		b.Fatal(err)
	}
	echoBack := make([]byte, hsFrame.Len())
	if _, err := io.ReadFull(c, echoBack); err != nil {
		b.Fatalf("读回握手回声失败：%v", err)
	}

	block := payload(benchBlock)
	got := make([]byte, benchBlock)
	b.SetBytes(2 * benchBlock)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Write(block); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(c, got); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPRelayPair 测量 TCP 中继（tcptunnel.go 的 serveRelay）配对后的单向转发。
func BenchmarkTCPRelayPair(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	mu := &sync.Mutex{}
	waiting := map[string]chan relayPeer{}
	go serveRelay(ln, 30*time.Second, mu, waiting)

	addr := ln.Addr().String()
	a, _, err := dialRelay(addr, "bench-room")
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	z, zr, err := dialRelay(addr, "bench-room")
	if err != nil {
		b.Fatal(err)
	}
	defer z.Close()
	_ = a.SetDeadline(time.Now().Add(120 * time.Second))
	_ = z.SetDeadline(time.Now().Add(120 * time.Second))

	block := payload(benchBlock)
	got := make([]byte, benchBlock)
	b.SetBytes(benchBlock)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.Write(block); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(zr, got); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPMuxFrame 测量多路复用通道（tcptunnel.go 的 tcpMux）的按帧往返搬运。
func BenchmarkTCPMuxFrame(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()

	// 替身：收到帧就照样发回去
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		m := &tcpMux{conn: c}
		br := bufio.NewReader(c)
		for {
			typ, id, p, err := readTcpFrame(br)
			if err != nil {
				return
			}
			if err := m.send(typ, id, p); err != nil {
				return
			}
		}
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	m := &tcpMux{conn: c}
	br := bufio.NewReader(c)

	block := payload(32 << 10) // 与生产里读本地 socket 的缓冲区同量级
	b.SetBytes(2 * int64(len(block)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.send(tcpFrameData, 1, block); err != nil {
			b.Fatal(err)
		}
		if _, _, _, err := readTcpFrame(br); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTunnelDTLSSCTP 测量软件入口隧道（DTLS 之上的 SCTP，即 copyChunks 走的层）的吞吐。
func BenchmarkTunnelDTLSSCTP(b *testing.B) {
	const total = 16 << 20
	data := payload(total)

	cert, _, err := selfSignedCert()
	if err != nil {
		b.Fatal(err)
	}
	sp, cp := udpPair(b)
	defer sp.Close()
	defer cp.Close()

	// 服务端先在 4 号流写一个热身字节立起回执流，再等数据流；SCTP 流是隐式的，顺序不能反。
	srvErr := make(chan error, 1)
	go func() {
		conn, err := dtls.Server(sp, sp.peer, testDTLSConfig(b, cert))
		if err != nil {
			srvErr <- err
			return
		}
		assoc, err := sctpAssociation(true, conn, 0)
		if err != nil {
			srvErr <- err
			return
		}
		defer assoc.Close()
		ack, err := assoc.OpenStream(4, sctp.PayloadTypeWebRTCBinary)
		if err != nil {
			srvErr <- err
			return
		}
		if _, err := ack.Write([]byte{2}); err != nil {
			srvErr <- err
			return
		}
		stream, err := assoc.AcceptStream()
		if err != nil {
			srvErr <- err
			return
		}
		buf := make([]byte, chunkSize)
		for {
			got := 0
			for got < total {
				n, err := stream.Read(buf)
				if err != nil {
					srvErr <- err
					return
				}
				got += n
			}
			if _, err := ack.Write([]byte{1}); err != nil {
				srvErr <- err
				return
			}
		}
	}()

	conn, err := dtls.Client(cp, cp.peer, testDTLSConfig(b, cert))
	if err != nil {
		b.Fatal(err)
	}
	assoc, err := sctpAssociation(false, conn, 0)
	if err != nil {
		b.Fatal(err)
	}
	defer assoc.Close()
	stream, err := assoc.OpenStream(2, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		b.Fatal(err)
	}
	ack, err := assoc.AcceptStream()
	if err != nil {
		b.Fatal(err)
	}

	// 同时等待回执与服务端错误。
	one := make([]byte, 1)
	waitAck := func() error {
		done := make(chan error, 1)
		go func() {
			_, err := io.ReadFull(ack, one)
			done <- err
		}()
		select {
		case err := <-done:
			return err
		case err := <-srvErr:
			return fmt.Errorf("服务端：%w", err)
		case <-time.After(60 * time.Second):
			return fmt.Errorf("等回执超时")
		}
	}

	if _, err := io.ReadFull(ack, one); err != nil { // 吃掉热身字节
		b.Fatal(err)
	}

	b.SetBytes(total)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := copyChunks(stream, bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
		if err := waitAck(); err != nil {
			b.Fatal(err)
		}
	}
}
