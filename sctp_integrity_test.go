package main

// sctp_integrity_test.go 在本地回环 UDP 上直接运行 DTLS / DTLS+SCTP，验证大批量数据逐字节正确。

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
)

// udpPeer 把连通的 UDP socket 包成 net.PacketConn（等价于 icePacketConn，不依赖 ICE）。
type udpPeer struct {
	c    *net.UDPConn
	peer *net.UDPAddr
}

func (u *udpPeer) ReadFrom(p []byte) (int, net.Addr, error)  { return u.c.ReadFromUDP(p) }
func (u *udpPeer) WriteTo(p []byte, _ net.Addr) (int, error) { return u.c.WriteToUDP(p, u.peer) }
func (u *udpPeer) Close() error                              { return u.c.Close() }
func (u *udpPeer) LocalAddr() net.Addr                       { return u.c.LocalAddr() }
func (u *udpPeer) SetDeadline(t time.Time) error             { return u.c.SetDeadline(t) }
func (u *udpPeer) SetReadDeadline(t time.Time) error         { return u.c.SetReadDeadline(t) }
func (u *udpPeer) SetWriteDeadline(t time.Time) error        { return u.c.SetWriteDeadline(t) }

// udpPair 造一对互相知道的 UDP socket；须用未连接 socket，否则 WriteTo 会报错。
func udpPair(t testing.TB) (*udpPeer, *udpPeer) {
	t.Helper()
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	sp := &udpPeer{c: srv, peer: cli.LocalAddr().(*net.UDPAddr)}
	cp := &udpPeer{c: cli, peer: srv.LocalAddr().(*net.UDPAddr)}
	return sp, cp
}

func testDTLSConfig(t testing.TB, cert tls.Certificate) *dtls.Config {
	t.Helper()
	return &dtls.Config{
		Certificates:         []tls.Certificate{cert},
		CipherSuites:         certCipherSuites,
		InsecureSkipVerify:   true,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
	}
}

// payload 生成一段随机数据。
func payload(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// firstZeroRun 返回第一段长度 ≥64 的全零区域的起点与长度，无则返回 (-1, 0)。
func firstZeroRun(b []byte) (int, int) {
	for i := 0; i < len(b); {
		if b[i] != 0 {
			i++
			continue
		}
		j := i
		for j < len(b) && b[j] == 0 {
			j++
		}
		if j-i >= 64 { // 只报"成片的零"，忽略数据里本来就有的零字节
			return i, j - i
		}
		i = j
	}
	return -1, 0
}

// TestSCTPBulkAfterAbortedStreamIsByteExact 验证一条流被中途关闭后，后续流的字节仍完整无误。
func TestSCTPBulkAfterAbortedStreamIsByteExact(t *testing.T) {
	if testing.Short() {
		t.Skip("搬运大文件，-short 跳过")
	}
	const size = 16 << 20
	data := payload(size)

	cert, _, err := selfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	sp, cp := udpPair(t)
	defer sp.Close()
	defer cp.Close()

	gotCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := dtls.Server(sp, sp.peer, testDTLSConfig(t, cert))
		if err != nil {
			errCh <- err
			return
		}
		assoc, err := sctp.Server(sctp.Config{NetConn: conn, MaxMessageSize: chunkSize})
		if err != nil {
			errCh <- err
			return
		}
		defer assoc.Close()
		// 第一条流：读一小段后关闭
		s1, err := assoc.AcceptStream()
		if err != nil {
			errCh <- err
			return
		}
		buf := make([]byte, chunkSize)
		_, _ = s1.Read(buf) // 收一小段
		_ = s1.Close()      // 中途关闭，在途数据由栈处理

		// 第二条流：须逐字节正确
		s2, err := assoc.AcceptStream()
		if err != nil {
			errCh <- err
			return
		}
		got := make([]byte, 0, size)
		for len(got) < size {
			n, err := s2.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			got = append(got, buf[:n]...)
		}
		gotCh <- got
	}()

	conn, err := dtls.Client(cp, cp.peer, testDTLSConfig(t, cert))
	if err != nil {
		t.Fatalf("DTLS 握手失败：%v", err)
	}
	defer conn.Close()
	assoc, err := sctp.Client(sctp.Config{NetConn: conn, MaxMessageSize: chunkSize})
	if err != nil {
		t.Fatalf("SCTP 关联失败：%v", err)
	}
	defer assoc.Close()

	// 第一条流：写 256 KiB 后直接关闭
	s1, err := assoc.OpenStream(2, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		t.Fatalf("开第一条流失败：%v", err)
	}
	_ = s1.SetWriteDeadline(time.Now().Add(30 * time.Second))
	for off := 0; off < 256<<10; off += chunkSize {
		if _, err := s1.Write(data[off : off+chunkSize]); err != nil {
			break
		}
	}
	_ = s1.Close()

	// 第二条流：完整传送并校验字节
	s2, err := assoc.OpenStream(4, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		t.Fatalf("开第二条流失败：%v", err)
	}
	_ = s2.SetWriteDeadline(time.Now().Add(120 * time.Second))
	for off := 0; off < size; off += chunkSize {
		if _, err := s2.Write(data[off : off+chunkSize]); err != nil {
			t.Fatalf("第二条流写到第 %d 字节失败：%v", off, err)
		}
	}
	_ = s2.Close()

	select {
	case got := <-gotCh:
		if len(got) != size {
			t.Fatalf("第二条流应收 %d 字节，实收 %d", size, len(got))
		}
		if off, run := firstZeroRun(got); off >= 0 {
			t.Logf("第二条流里第一段成片全零：偏移 %d，长度 %d", off, run)
		}
		if !bytes.Equal(data, got) {
			for i := range data {
				if data[i] != got[i] {
					t.Fatalf("掐断一条流之后，下一条流的数据被动过：第 %d 字节 源=0x%02x 收=0x%02x", i, data[i], got[i])
				}
			}
		}
	case err := <-errCh:
		t.Fatalf("第二条流出错：%v", err)
	case <-time.After(300 * time.Second):
		t.Fatal("第二条流超时")
	}
}

func TestSCTPBulkIsByteExact(t *testing.T) {
	if testing.Short() {
		t.Skip("搬运大文件，-short 跳过")
	}
	const size = 32 << 20
	data := payload(size)

	cert, _, err := selfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	sp, cp := udpPair(t)
	defer sp.Close()
	defer cp.Close()

	// 服务端：DTLS → SCTP，从第一条流收满 size
	recvDone := make(chan []byte, 1)
	recvErr := make(chan error, 1)
	go func() {
		conn, err := dtls.Server(sp, sp.peer, testDTLSConfig(t, cert))
		if err != nil {
			recvErr <- err
			return
		}
		assoc, err := sctp.Server(sctp.Config{NetConn: conn, MaxMessageSize: chunkSize})
		if err != nil {
			recvErr <- err
			return
		}
		defer assoc.Close()
		st, err := assoc.AcceptStream()
		if err != nil {
			recvErr <- err
			return
		}
		got := make([]byte, 0, size)
		buf := make([]byte, chunkSize)
		for len(got) < size {
			n, err := st.Read(buf)
			if err != nil {
				recvErr <- err
				return
			}
			got = append(got, buf[:n]...)
		}
		recvDone <- got
	}()

	conn, err := dtls.Client(cp, cp.peer, testDTLSConfig(t, cert))
	if err != nil {
		t.Fatalf("DTLS 握手失败：%v", err)
	}
	defer conn.Close()
	assoc, err := sctp.Client(sctp.Config{NetConn: conn, MaxMessageSize: chunkSize})
	if err != nil {
		t.Fatalf("SCTP 关联失败：%v", err)
	}
	defer assoc.Close()
	st, err := assoc.OpenStream(2, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		t.Fatalf("开流失败：%v", err)
	}

	deadline := time.Now().Add(180 * time.Second)
	for off := 0; off < size; {
		end := off + chunkSize
		if end > size {
			end = size
		}
		_ = st.SetWriteDeadline(deadline)
		if _, err := st.Write(data[off:end]); err != nil {
			t.Fatalf("写到第 %d 字节失败：%v", off, err)
		}
		off = end
	}
	_ = st.Close()

	var got []byte
	select {
	case got = <-recvDone:
	case err := <-recvErr:
		t.Fatalf("接收侧出错：%v", err)
	case <-time.After(180 * time.Second):
		t.Fatal("SCTP 接收超时")
	}

	if len(got) != size {
		t.Fatalf("应收 %d 字节，实收 %d", size, len(got))
	}
	if off, run := firstZeroRun(got); off >= 0 {
		t.Logf("收到数据里第一段成片全零：偏移 %d，长度 %d", off, run)
	}
	if !bytes.Equal(data, got) {
		for i := range data {
			if data[i] != got[i] {
				t.Fatalf("SCTP 这一层弄坏了数据：第 %d 字节 源=0x%02x 收=0x%02x", i, data[i], got[i])
			}
		}
	}
}
