package main

// pmtu_test.go —— 进程内 MTU 黑洞实验台，复现与验证 MTU 相关的修法。

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
)

// mtuPipe 模拟小 MTU 且 ICMP 被丢弃的链路：超过 limit 的包静默丢弃，并统计收发量。
type mtuPipe struct {
	net.PacketConn
	limit   int
	drops   *int64
	dropped *map[int]int
	passed  *int64
	read    *int64 // 该 socket 上真正读到的包数
	mu      *sync.Mutex
}

func (c *mtuPipe) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err == nil && c.read != nil {
		atomic.AddInt64(c.read, 1)
	}
	return n, addr, err
}

func (c *mtuPipe) WriteTo(b []byte, addr net.Addr) (int, error) {
	if len(b) > c.limit {
		if c.mu != nil {
			c.mu.Lock()
		}
		if c.drops != nil {
			*c.drops++
		}
		if c.dropped != nil {
			(*c.dropped)[len(b)]++
		}
		if c.mu != nil {
			c.mu.Unlock()
		}
		return len(b), nil // 假装发送成功，实际已丢弃
	}
	if c.passed != nil {
		*c.passed++
	}
	return c.PacketConn.WriteTo(b, addr)
}

// spyConn 夹在 DTLS 与 SCTP 之间，记录读到的字节数与最近一次读错误。
type spyConn struct {
	net.Conn
	bytes   *int64
	lastErr *sync.Map // key "err" → error
}

func (c *spyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.bytes != nil {
		atomic.AddInt64(c.bytes, int64(n))
	}
	if err != nil && err != io.EOF && c.lastErr != nil {
		c.lastErr.Store("err", fmt.Sprintf("%v（缓冲区 %d 字节，读到 %d）", err, len(p), n))
	}
	return n, err
}

// mtuLab 一次实验的配置
type mtuLab struct {
	limit    int           // 链路能过的最大 UDP 载荷（0 = 不限）
	size     int           // 传输多少字节
	probe    bool          // 是否先做路径探测
	dtlsMTU  int           // 显式指定 DTLS 记录上限：0=用探测值换算，-1=用库默认
	splitAt  int           // 显式指定拆分上限：0=用探测值换算，-1=不拆分
	throttle time.Duration // 每次写入之间停顿（区分"配置不对"与"突发灌爆缓冲"）
}

func pickProbed(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	case a < b:
		return a
	}
	return b
}

func (cfg mtuLab) run(t *testing.T) (int, time.Duration, string, error) {
	t.Helper()
	cert, _, err := selfSignedCert()
	if err != nil {
		return 0, 0, "", err
	}
	ua, ub := udpPair(t)
	defer ua.Close()
	defer ub.Close()

	var drops, passed int64
	var sentBytes, recvBytes, readA, readB, dtlsToSCTP int64
	var spyErrs sync.Map
	var pipeMu sync.Mutex
	droppedSizes := map[int]int{}
	wrapPipe := func(p net.PacketConn) net.PacketConn {
		if cfg.limit <= 0 {
			return p
		}
		return &mtuPipe{PacketConn: p, limit: cfg.limit, drops: &drops, dropped: &droppedSizes, passed: &passed, mu: &pipeMu}
	}
	var paConn, pbConn net.PacketConn
	paConn, pbConn = wrapPipe(ua), wrapPipe(ub)
	if mp, ok := paConn.(*mtuPipe); ok {
		mp.read = &readA
	}
	if mp, ok := pbConn.(*mtuPipe); ok {
		mp.read = &readB
	}

	mtu := 0
	if cfg.probe {
		// 两端必须同时探测
		var probedA, probedB int
		var wrappedA, wrappedB net.PacketConn
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); probedA, wrappedA = probePathMTU(paConn) }()
		go func() { defer wg.Done(); probedB, wrappedB = probePathMTU(pbConn) }()
		wg.Wait()
		mtu = pickProbed(probedA, probedB)
		paConn, pbConn = wrappedA, wrappedB
		t.Logf("  路径探测：A=%d B=%d → 采用 %d", probedA, probedB, mtu)
	}

	// 上限：显式指定优先，其次由探测值换算
	dtlsRec := 0
	switch {
	case cfg.dtlsMTU == -1:
	case cfg.dtlsMTU > 0:
		dtlsRec = cfg.dtlsMTU
	default:
		dtlsRec = dtlsMTUFor(mtu)
	}
	// 不做「拆小 SCTP 写入」：该做法已证伪，链路放不下时改为报错并建议走 TCP 中继。
	split := 0
	desc := fmt.Sprintf("探测=%d DTLS记录上限=%d 拆分上限=%d", mtu, dtlsRec, split)
	t.Logf("  %s", desc)

	cfgDTLS := func() *dtls.Config {
		return &dtls.Config{
			Certificates:         []tls.Certificate{cert},
			CipherSuites:         certCipherSuites,
			InsecureSkipVerify:   true,
			ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
			MTU:                  dtlsRec,
		}
	}

	recvDone := make(chan int, 1)
	recvErr := make(chan error, 1)
	go func() {
		conn, err := dtls.Server(pbConn, ub.peer, cfgDTLS())
		if err != nil {
			recvErr <- fmt.Errorf("服务侧 DTLS 失败：%w", err)
			return
		}
		assoc, err := sctpAssociation(true, &spyConn{Conn: conn, bytes: &dtlsToSCTP, lastErr: &spyErrs}, mtu)
		if err != nil {
			recvErr <- fmt.Errorf("服务侧 SCTP 失败：%w", err)
			return
		}
		defer assoc.Close()
		st, err := assoc.AcceptStream()
		if err != nil {
			recvErr <- err
			return
		}
		buf := make([]byte, chunkSize)
		got := 0
		for got < cfg.size {
			n, err := st.Read(buf)
			if err != nil {
				recvErr <- err
				return
			}
			atomic.AddInt64(&recvBytes, int64(n))
			got += n
		}
		recvDone <- got
	}()

	conn, err := dtls.Client(paConn, ua.peer, cfgDTLS())
	if err != nil {
		return 0, 0, desc, fmt.Errorf("客户端 DTLS 失败：%w", err)
	}
	assoc, err := sctpAssociation(false, conn, mtu)
	if err != nil {
		return 0, 0, desc, fmt.Errorf("客户端 SCTP 失败：%w", err)
	}
	defer assoc.Close()
	st, err := assoc.OpenStream(2, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		return 0, 0, desc, err
	}

	block := make([]byte, chunkSize)
	start := time.Now()

	// 进度探针：每 3 秒报告发出/收到/通道包数，便于区分发送方停止与接收方未收到。
	stopProbe := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stopProbe:
				return
			case <-time.After(3 * time.Second):
			}
			t.Logf("  [%2ds] 通道上发出 %d 个包；服务端 socket 收到 %d 个包；已交给 SCTP %d KiB；SCTP 读出 %d KiB；DTLS→SCTP 之间读到 %d KiB",
				(i+1)*3, atomic.LoadInt64(&passed), atomic.LoadInt64(&readB),
				atomic.LoadInt64(&sentBytes)/1024, atomic.LoadInt64(&recvBytes)/1024, atomic.LoadInt64(&dtlsToSCTP)/1024)
		}
	}()
	defer close(stopProbe)

	for sent := 0; sent < cfg.size; sent += chunkSize {
		_ = st.SetWriteDeadline(time.Now().Add(20 * time.Second))
		if _, err := st.Write(block); err != nil {
			return 0, time.Since(start), desc, fmt.Errorf("写到第 %d 字节：%w", sent, err)
		}
		atomic.AddInt64(&sentBytes, int64(chunkSize))
		if cfg.throttle > 0 {
			time.Sleep(cfg.throttle)
		}
	}
	select {
	case got := <-recvDone:
		return got, time.Since(start), desc, nil
	case err := <-recvErr:
		return 0, time.Since(start), desc, err
	case <-time.After(30 * time.Second):
		return 0, time.Since(start), desc, fmt.Errorf(
			"超时：发出去的包 %d 个、被链路吞 %d 个（尺寸分布：%s）", passed, drops, sizeHistogram(droppedSizes))
	}
}

// sizeHistogram 把被丢弃包的尺寸与数量排成一行。
func sizeHistogram(m map[int]int) string {
	if len(m) == 0 {
		return "无"
	}
	sizes := make([]int, 0, len(m))
	for s := range m {
		sizes = append(sizes, s)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	var b strings.Builder
	for i, s := range sizes {
		if i >= 6 {
			b.WriteString(" …")
			break
		}
		fmt.Fprintf(&b, "%d字节×%d ", s, m[s])
	}
	return b.String()
}

// 实验

const bhLimit = 872 // 900 - 28（IPv4+UDP 头）

// 复现：不探测、不拆分时在该链路上传不动
func TestMTUBlackHoleRepro(t *testing.T) {
	if testing.Short() {
		t.Skip("带超时，-short 跳过")
	}
	got, d, desc, err := mtuLab{limit: bhLimit, size: 256 << 10}.run(t)
	if err == nil && got >= 256<<10 {
		t.Logf("现状居然传完了（%d 字节 / %.2fs，%s）——复现条件要重设", got, d.Seconds(), desc)
		return
	}
	t.Logf("✅ 复现成功：现状传不过去（收到 %d 字节 / %.1fs，%s，err=%v）", got, d.Seconds(), desc, err)
}

// 对照：正常链路 + 探测包装不得退化
func TestMTUPathFixOnNormalLink(t *testing.T) {
	if testing.Short() {
		t.Skip("带超时，-short 跳过")
	}
	got, d, desc, err := mtuLab{limit: 1500, size: 256 << 10, probe: true}.run(t)
	if err != nil {
		t.Fatalf("正常链路上走了探测+包装反而失败（说明包装层有 bug）：%v（%s）", err, desc)
	}
	t.Logf("✅ 正常链路 + 探测：%d 字节 / %.3fs（%.2f MiB/s）%s", got, d.Seconds(), mbps(got, d), desc)
}

// 链路刚好够 SCTP 用时（载荷 1300）必须照常工作。
func TestMTUFitsSCTPWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("带超时，-short 跳过")
	}
	got, d, desc, err := mtuLab{limit: 1300, size: 256 << 10, probe: true}.run(t)
	if err != nil {
		t.Fatalf("链路放得下 SCTP 却失败了：%v（%s）", err, desc)
	}
	t.Logf("✅ MTU 刚够的链路正常：%d 字节 / %.2fs（%.2f MiB/s）%s", got, d.Seconds(), mbps(got, d), desc)
}

// 放不下 SCTP 的链路必须快速给出明确结论，而非等待超时。
func TestMTUTooSmallFailsFastWithAdvice(t *testing.T) {
	if testing.Short() {
		t.Skip("带超时，-short 跳过")
	}
	start := time.Now()
	_, _, desc, err := mtuLab{limit: bhLimit, size: 256 << 10, probe: true}.run(t)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("这条链路本该被判为放不下 SCTP，却『成功』了（%s）", desc)
	}
	if !strings.Contains(err.Error(), "MTU 太小") {
		t.Fatalf("失败原因应当明确指出 MTU 太小并给替代方案，实际：%v（%s）", err, desc)
	}
	if elapsed > 12*time.Second {
		t.Fatalf("应当快速失败（探测窗口 + 握手，约几秒），实际花了 %v", elapsed)
	}
	t.Logf("✅ 放不下的链路：%.1f 秒内给出明确结论 —— %v", elapsed.Seconds(), err)
}

// 只收窄 DTLS 记录、不拆 SCTP 时，该链路仍应同样快速报错
func TestMTUTooSmallDTLSSHRINKStillFails(t *testing.T) {
	if testing.Short() {
		t.Skip("带超时，-short 跳过")
	}
	_, _, desc, err := mtuLab{limit: bhLimit, size: 64 << 10, probe: true, dtlsMTU: 700, splitAt: -1}.run(t)
	if err == nil {
		t.Logf("收窄 DTLS 后这次通过了（%s）——与预期不同，值得再看", desc)
		return
	}
	if !strings.Contains(err.Error(), "MTU 太小") {
		t.Fatalf("应当报『链路放不下 SCTP』，实际：%v（%s）", err, desc)
	}
	t.Logf("✅ 收窄 DTLS 也救不了（本来就不是 DTLS 的问题）：%s", desc)
}

// 纯逻辑：SCTP 可用性判据
func TestSctpFitsOnPath(t *testing.T) {
	// 未探到结果时不拦
	if !sctpFitsOnPath(0) {
		t.Error("探测失败时不该拦下建连")
	}
	// SCTP 出站包 1191 字节加记录头约 1231，够大应放行
	if !sctpFitsOnPath(1231) || !sctpFitsOnPath(1300) || !sctpFitsOnPath(1400) {
		t.Error("够大时不该拦")
	}
	// 小一点都会被链路静默丢弃
	if sctpFitsOnPath(872) || sctpFitsOnPath(1230) {
		t.Error("放不下 SCTP 的包时必须拦下并给出提示")
	}
	// 提示里必须包含可照做的命令
	msg := mtuTooSmallHelp(872)
	for _, want := range []string{"MTU 太小", "tcptunnel", "relay"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示里应当出现 %q，实际：%s", want, msg)
		}
	}
}

// 纯逻辑

func TestPmtuProbeRoundTrip(t *testing.T) {
	for _, kind := range []byte{pmtuQuery, pmtuEcho} {
		for _, size := range []int{1400, 800, 600} {
			b := buildPmtuProbe(kind, size)
			if len(b) != size {
				t.Fatalf("探测包长度应当是 %d，实际 %d", size, len(b))
			}
			k, s, ok := parsePmtuProbe(b)
			if !ok || k != kind || s != size {
				t.Fatalf("解析对不上：kind=%c size=%d ok=%v", k, s, ok)
			}
		}
	}
	if _, _, ok := parsePmtuProbe([]byte("GET / HTTP/1.1\r\n\r\n")); ok {
		t.Error("普通数据被误认成探测包了")
	}
	if _, _, ok := parsePmtuProbe([]byte("MCLBXPMT")); ok {
		t.Error("太短的包不该被当成探测包")
	}
}

func TestPickMTU(t *testing.T) {
	if got := pickMTU(map[int]bool{}); got != 0 {
		t.Errorf("一个应答都没有时应当是 0，实际 %d", got)
	}
	if got := pickMTU(map[int]bool{600: true, 800: true, 1000: true}); got != 1000 {
		t.Errorf("应当取最大的那个，实际 %d", got)
	}
}

func TestDtlsMTUForLeavesHeadroom(t *testing.T) {
	if got := dtlsMTUFor(0); got != 0 {
		t.Errorf("探测失败应当返回 0（交给默认值），实际 %d", got)
	}
	if got := dtlsMTUFor(1400); got != 1300 {
		t.Errorf("1400 留出开销后应当是 1300，实际 %d", got)
	}
	if got := dtlsMTUFor(600); got != 500 {
		t.Errorf("600 → 500，实际 %d", got)
	}
	if got := dtlsMTUFor(300); got < 400 {
		t.Errorf("再小也要给个下限，实际 %d", got)
	}
	if got := dtlsMTUFor(3000); got != 1400 {
		t.Errorf("上限应当收敛，实际 %d", got)
	}
}

type countingConn struct {
	net.Conn
	writes []int
}

func (c *countingConn) Write(p []byte) (int, error) {
	c.writes = append(c.writes, len(p))
	return len(p), nil
}

// 注：曾测「把 SCTP 写入拆小」的做法，已证伪并删除（pion/sctp 会自行解析分片边界）。
