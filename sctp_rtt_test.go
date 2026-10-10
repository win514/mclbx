package main

// sctp_rtt_test.go 测量隧道吞吐随 RTT 的变化，定位高延迟下的瓶颈。

import (
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
)

// delayConn 给 PacketConn 加上固定的单向延迟
type delayConn struct {
	net.PacketConn
	d    time.Duration
	peer *net.UDPAddr
}

func (c *delayConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	cp := append([]byte(nil), b...) // 必须拷贝：调用方会复用该缓冲
	go func() {
		time.Sleep(c.d)
		_, _ = c.PacketConn.WriteTo(cp, addr)
	}()
	return len(b), nil
}

func (c *delayConn) ReadFrom(b []byte) (int, net.Addr, error) { return c.PacketConn.ReadFrom(b) }
func (c *delayConn) PeerAddr() net.Addr                       { return c.peer }

func delayedPair(t *testing.T, oneWay time.Duration) (*delayConn, *delayConn) {
	t.Helper()
	a, b := udpPair(t)
	return &delayConn{PacketConn: a, d: oneWay, peer: a.peer},
		&delayConn{PacketConn: b, d: oneWay, peer: b.peer}
}

// measureTunnelSized 通过 DTLS+SCTP 传输 size 字节并返回耗时；msg 为消息体大小（0 用默认）。
func measureTunnelSized(t *testing.T, oneWay time.Duration, size int, recvBuf uint32, msg int) (time.Duration, error) {
	t.Helper()
	if msg == 0 {
		msg = chunkSize
	}
	cert, _, err := selfSignedCert()
	if err != nil {
		return 0, err
	}
	sp, cp := delayedPair(t, oneWay)
	defer sp.Close()
	defer cp.Close()

	cfg := &dtls.Config{
		Certificates:         []tls.Certificate{cert},
		CipherSuites:         certCipherSuites,
		InsecureSkipVerify:   true,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
	}

	recvDone := make(chan int, 1)
	recvErr := make(chan error, 1)
	go func() {
		conn, err := dtls.Server(sp, sp.PeerAddr(), cfg)
		if err != nil {
			recvErr <- err
			return
		}
		assoc, err := sctp.Server(sctp.Config{NetConn: conn, MaxMessageSize: uint32(msg), MaxReceiveBufferSize: recvBuf})
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
		buf := make([]byte, msg)
		got := 0
		for got < size {
			n, err := st.Read(buf)
			if err != nil {
				recvErr <- err
				return
			}
			got += n
		}
		recvDone <- got
	}()

	conn, err := dtls.Client(cp, cp.PeerAddr(), cfg)
	if err != nil {
		return 0, fmt.Errorf("握手失败：%w", err)
	}
	assoc, err := sctp.Client(sctp.Config{NetConn: conn, MaxMessageSize: uint32(msg), MaxReceiveBufferSize: recvBuf})
	if err != nil {
		return 0, err
	}
	defer assoc.Close()
	st, err := assoc.OpenStream(2, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		return 0, err
	}

	block := make([]byte, msg)
	start := time.Now()
	for sent := 0; sent < size; sent += msg {
		if _, err := st.Write(block); err != nil {
			return 0, fmt.Errorf("写到第 %d 字节：%w", sent, err)
		}
	}
	select {
	case <-recvDone:
		return time.Since(start), nil
	case err := <-recvErr:
		return 0, err
	case <-time.After(180 * time.Second):
		return 0, fmt.Errorf("接收超时（%s 还没传完）", time.Since(start))
	}
}

// measureTunnel 默认 8 KiB 消息体
func measureTunnel(t *testing.T, oneWay time.Duration, size int, recvBuf uint32) (time.Duration, error) {
	return measureTunnelSized(t, oneWay, size, recvBuf, chunkSize)
}

func mbps(size int, d time.Duration) float64 {
	return float64(size) / 1048576 / d.Seconds()
}

// TestTunnelThroughputVsRTT 测量吞吐随 RTT 变化的曲线。
func TestTunnelThroughputVsRTT(t *testing.T) {
	if testing.Short() {
		t.Skip("要跑几轮传输，-short 跳过")
	}
	const size = 2 << 20
	t.Log("RTT | 耗时 | 吞吐")
	for _, oneWay := range []time.Duration{0, 5 * time.Millisecond, 25 * time.Millisecond, 100 * time.Millisecond} {
		d, err := measureTunnel(t, oneWay, size, 0)
		if err != nil {
			t.Errorf("单向延迟 %v：%v", oneWay, err)
			continue
		}
		t.Logf("%5dms | %6.2fs | %7.2f MiB/s", oneWay.Milliseconds()*2, d.Seconds(), mbps(size, d))
	}
}

// TestTunnelReceiveWindowIsNotTheBottleneck 记录：放大 SCTP 接收窗口对高 RTT 吞吐无影响。
func TestTunnelReceiveWindowIsNotTheBottleneck(t *testing.T) {
	if testing.Short() {
		t.Skip("要跑几轮传输，-short 跳过")
	}
	const size = 2 << 20
	const oneWay = 50 * time.Millisecond // 100ms RTT

	small, err := measureTunnel(t, oneWay, size, 0)
	if err != nil {
		t.Fatalf("默认窗口：%v", err)
	}
	big, err := measureTunnel(t, oneWay, size, 4<<20)
	if err != nil {
		t.Fatalf("大窗口：%v", err)
	}
	sp, bp := mbps(size, small), mbps(size, big)
	if sp <= 0 {
		t.Fatalf("默认窗口那次没测出有效吞吐：%.4f MiB/s", sp)
	}
	ratio := bp / sp
	t.Logf("100ms RTT：默认窗口 %.2f MiB/s → 4 MiB 窗口 %.2f MiB/s（%.1f 倍）", sp, bp, ratio)

	// 只要求量级相同；出现数量级差异说明前提变了，需重新定位瓶颈。
	if ratio < 0.5 || ratio > 2.0 {
		t.Errorf("接收窗口对吞吐的影响超出了预期范围（%.1f 倍）：原来的结论是「这个杠杆没用」，"+
			"现在前后差这么多，说明前提变了，需要重新定位瓶颈", ratio)
		return
	}
	if ratio < 1.2 {
		t.Logf("→ 加大接收窗口没有带来提升，确认瓶颈不在接收窗口（这正是本用例要记录的事实）")
	}
}

// TestTunnelMessageSizeAtHighRTT 记录：放大 SCTP 消息体对高 RTT 吞吐无影响，仅记录数字不断言。
func TestTunnelMessageSizeAtHighRTT(t *testing.T) {
	if testing.Short() {
		t.Skip("要跑几轮传输，-short 跳过")
	}
	const size = 4 << 20
	const oneWay = 50 * time.Millisecond
	for _, msg := range []int{8 << 10, 32 << 10, 64 << 10, 128 << 10} {
		d, err := measureTunnelSized(t, oneWay, size, 4<<20, msg)
		if err != nil {
			t.Errorf("消息体 %d：%v", msg, err)
			continue
		}
		t.Logf("消息体 %7d B | %6.2fs | %7.2f MiB/s", msg, d.Seconds(), mbps(size, d))
	}
}

// 结论：DTLS 无重传与流控，绕开 SCTP 直接打大流量必然丢数据，高 RTT 的慢只能在 SCTP 层解决。
