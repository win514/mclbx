package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
)

// sctp.go 在 ICE + DTLS 之上建 SCTP 关联（可靠、有序、流控、多流），并做应用层身份绑定。

const (
	chunkSize      = 8192 // SCTP 为消息语义，读写都用这个大小
	idStreamID     = 0    // 专门跑身份证明的流
	idProofTag     = "mclbx-id-v1"
	idRejectPrefix = "REJECT "
	idAckOK        = "ID-OK" // 客户机验完主机证明后的回执
	idMsgMaxLen    = 64 * 1024
)

type identity struct {
	cert tls.Certificate
	key  *ecdsa.PrivateKey
	fp   [32]byte // 证书 DER 的 SHA-256
}

func newIdentity() (*identity, error) {
	cert, key, err := selfSignedCert()
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("自签证书为空")
	}
	return &identity{cert: cert, key: key, fp: sha256.Sum256(cert.Certificate[0])}, nil
}

// fingerprint 返回 WebRTC 风格的展示格式 AA:BB:CC:...。
func (id *identity) fingerprint() string { return colonHex(id.fp[:]) }

func (id *identity) sign(msg []byte) ([]byte, error) {
	h := sha256.Sum256(msg)
	return ecdsa.SignASN1(crand.Reader, id.key, h[:])
}

func verifySignature(pub *ecdsa.PublicKey, msg, sig []byte) bool {
	h := sha256.Sum256(msg)
	return ecdsa.VerifyASN1(pub, h[:], sig)
}

// parseFingerprint 把信令里的指纹字符串解析回 32 字节（容忍冒号、空格、短横）。
func parseFingerprint(s string) ([32]byte, error) {
	var out [32]byte
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ':', ' ', '-', '\t':
			return -1
		}
		return r
	}, s)
	b, err := hex.DecodeString(clean)
	if err != nil {
		return out, fmt.Errorf("指纹不是合法的十六进制：%w", err)
	}
	if len(b) != 32 {
		return out, fmt.Errorf("指纹应为 32 字节的 SHA-256，实际 %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

func colonHex(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	return strings.Join(parts, ":")
}

// safetyCode 由双方指纹算出的安全码，两端应一致（32 bit，类 ZRTP 的 SAS）。
func safetyCode(a, b [32]byte) string {
	lo, hi := a, b
	if bytes.Compare(lo[:], hi[:]) > 0 {
		lo, hi = hi, lo
	}
	h := sha256.New()
	h.Write(lo[:])
	h.Write(hi[:])
	sum := h.Sum(nil)
	return fmt.Sprintf("%02X%02X-%02X%02X", sum[0], sum[1], sum[2], sum[3])
}

type idMsg struct {
	Nonce []byte `json:"nonce,omitempty"`
	Cert  []byte `json:"cert,omitempty"` // DER
	Sig   []byte `json:"sig,omitempty"`
}

// idChallenge = H(tag | 客户机指纹 | 主机指纹 | 客户机随机数 | 主机随机数)，两端结果必然一致。
func idChallenge(fpGuest, fpHost, nonceGuest, nonceHost []byte) []byte {
	h := sha256.New()
	h.Write([]byte(idProofTag))
	h.Write(fpGuest)
	h.Write(fpHost)
	h.Write(nonceGuest)
	h.Write(nonceHost)
	return h.Sum(nil)
}

// verifyProof 校验对方证明：指纹须等于信令公布值，签名须有效。
func verifyProof(proof *idMsg, ch []byte, expectFP [32]byte, who string) error {
	if len(proof.Cert) == 0 || len(proof.Sig) == 0 {
		return fmt.Errorf("%s没有提供身份证明", who)
	}
	got := sha256.Sum256(proof.Cert)
	if subtle.ConstantTimeCompare(got[:], expectFP[:]) != 1 {
		return fmt.Errorf("%s的证书指纹与信令公布值不一致\n    期望 %s\n    实际 %s\n  原因：可能存在中间人\n  处理：中止连接",
			who, colonHex(expectFP[:]), colonHex(got[:]))
	}
	cert, err := x509.ParseCertificate(proof.Cert)
	if err != nil {
		return fmt.Errorf("解析%s证书失败：%w", who, err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("%s的证书不是 ECDSA 公钥", who)
	}
	if !verifySignature(pub, ch, proof.Sig) {
		return fmt.Errorf("%s的挑战签名验证失败，原因：无对应私钥或签名被篡改", who)
	}
	return nil
}

// idStream 把身份证明需要的读写抽成接口，便于用内存流做单元测试。
type idStream interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}

func writeIDMsg(s idStream, m *idMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.Write(b) // 一条 SCTP 消息
	return err
}

// rejectIDMsg 校验失败时先把原因告诉对方再断开，延时后再关关联以确保消息发出。
func rejectIDMsg(s idStream, err error) {
	_ = writeIDMsg(s, &idMsg{Nonce: []byte(idRejectPrefix + err.Error())})
	time.Sleep(idRejectFlush)
}

// 拒绝消息发出去之后等多久再断关联（测试里会调成 0）
var idRejectFlush = 400 * time.Millisecond

// idVerdictWait 是关联断开后等待身份结论的最长时间，须大于 idRejectFlush。
var idVerdictWait = 900 * time.Millisecond

func readIDMsg(s idStream) (*idMsg, error) {
	buf := make([]byte, idMsgMaxLen)
	n, err := s.Read(buf)
	if err != nil {
		return nil, err
	}
	var m idMsg
	if err := json.Unmarshal(buf[:n], &m); err != nil {
		return nil, err
	}
	// 对端明确拒绝了：把它给的原因转成错误抛出去
	if strings.HasPrefix(string(m.Nonce), idRejectPrefix) {
		return nil, fmt.Errorf("对端判定身份不合法：%s", strings.TrimPrefix(string(m.Nonce), idRejectPrefix))
	}
	return &m, nil
}

// readIDConfirm 主机侧最后一步：等客户机确认，以区分双方各自判定成功。
func readIDConfirm(s idStream) error {
	m, err := readIDMsg(s)
	if err != nil {
		// 对面已经把原因说清楚了，原样带上去，别再叠一层猜测
		if strings.HasPrefix(err.Error(), "对端判定身份不合法") {
			return err
		}
		return fmt.Errorf("对端没有确认身份绑定：%w\n  原因：对端校验本端证明未通过或已断开", err)
	}
	if string(m.Nonce) != idAckOK {
		return fmt.Errorf("对端的确认内容无法识别：%q", string(m.Nonce))
	}
	return nil
}

// proveIdentityGuest 客户机侧：先发随机数，收对端随机数，再互发证明；挑战用双方公布的指纹。
func proveIdentityGuest(s idStream, id *identity, fpSelfAnnounced, fpHostAnnounced [32]byte) ([]byte, error) {
	gN := make([]byte, 16)
	if _, err := crand.Read(gN); err != nil {
		return nil, err
	}
	if err := writeIDMsg(s, &idMsg{Nonce: gN}); err != nil {
		return nil, fmt.Errorf("发送随机数失败：%w", err)
	}
	hm, err := readIDMsg(s)
	if err != nil {
		return nil, fmt.Errorf("读取房主随机数失败：%w", err)
	}
	ch := idChallenge(fpSelfAnnounced[:], fpHostAnnounced[:], gN, hm.Nonce)

	sig, err := id.sign(ch)
	if err != nil {
		return nil, err
	}
	if err := writeIDMsg(s, &idMsg{Nonce: gN, Cert: id.cert.Certificate[0], Sig: sig}); err != nil {
		return nil, fmt.Errorf("发送身份证明失败：%w", err)
	}
	hp, err := readIDMsg(s)
	if err != nil {
		return nil, fmt.Errorf("读取房主身份证明失败：%w", err)
	}
	if !bytes.Equal(hp.Nonce, hm.Nonce) {
		return nil, fmt.Errorf("房主随机数前后不一致")
	}
	if err := verifyProof(hp, ch, fpHostAnnounced, "房主"); err != nil {
		rejectIDMsg(s, err)
		return nil, err
	}
	// 我认了对面，现在告诉它"我也认了"。主机收到这个才会宣布绑定成功。
	if err := writeIDMsg(s, &idMsg{Nonce: []byte(idAckOK)}); err != nil {
		return nil, fmt.Errorf("发送确认失败：%w", err)
	}
	return hp.Cert, nil
}

// proveIdentityHost 主机侧：收客户机随机数，回自己的，互发证明，最后等客户机确认。
func proveIdentityHost(s idStream, id *identity, fpSelfAnnounced, fpGuestAnnounced [32]byte) ([]byte, error) {
	gm, err := readIDMsg(s)
	if err != nil {
		return nil, fmt.Errorf("读取玩家随机数失败：%w", err)
	}
	hN := make([]byte, 16)
	if _, err := crand.Read(hN); err != nil {
		return nil, err
	}
	if err := writeIDMsg(s, &idMsg{Nonce: hN}); err != nil {
		return nil, fmt.Errorf("发送随机数失败：%w", err)
	}

	ch := idChallenge(fpGuestAnnounced[:], fpSelfAnnounced[:], gm.Nonce, hN)

	gp, err := readIDMsg(s)
	if err != nil {
		return nil, fmt.Errorf("读取玩家身份证明失败：%w", err)
	}
	if !bytes.Equal(gp.Nonce, gm.Nonce) {
		return nil, fmt.Errorf("玩家随机数前后不一致")
	}
	if err := verifyProof(gp, ch, fpGuestAnnounced, "玩家"); err != nil {
		rejectIDMsg(s, err)
		return nil, err
	}

	sig, err := id.sign(ch)
	if err != nil {
		return nil, err
	}
	if err := writeIDMsg(s, &idMsg{Nonce: hN, Cert: id.cert.Certificate[0], Sig: sig}); err != nil {
		return nil, fmt.Errorf("发送身份证明失败：%w", err)
	}
	if err := readIDConfirm(s); err != nil {
		return nil, err
	}
	return gp.Cert, nil
}

// reportIdentity 输出身份绑定结果。
func reportIdentity(id *identity, peerDER []byte) error {
	if len(peerDER) == 0 {
		return fmt.Errorf("身份证明通过，但未收到对端证书")
	}
	peerFP := sha256.Sum256(peerDER)
	logf("结论：双向身份绑定完成，本端指纹 %s", id.fingerprint())
	logf("                    对端指纹 %s", colonHex(peerFP[:]))
	logf("                    说明：双方指纹与信令公布值一致，签名互验通过，对端已回执")
	logf("结论：安全码 %s；双方核对一致即可确认无中间人", safetyCode(id.fp, peerFP))
	return nil
}

type icePacketConn struct {
	net.Conn
	laddr net.Addr
	raddr net.Addr
}

func (c *icePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Conn.Read(p)
	return n, c.raddr, err
}

func (c *icePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) { return c.Conn.Write(p) }

func (c *icePacketConn) LocalAddr() net.Addr  { return c.laddr }
func (c *icePacketConn) RemoteAddr() net.Addr { return c.raddr }

func wrapICEConn(c net.Conn, raddr net.Addr) *icePacketConn {
	return &icePacketConn{Conn: c, laddr: &net.UDPAddr{}, raddr: raddr}
}

func selfSignedCert() (tls.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "mclbx"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(crand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, key, nil
}

// certCipherSuites 只允许带证书的套件，不留匿名套件。
var certCipherSuites = []dtls.CipherSuiteID{
	dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	dtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
}

// copyChunks 逐块搬运并校验写入长度；不用 io.CopyBuffer，因其会静默忽略传入的缓冲区。
func copyChunks(dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, chunkSize)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
			if w != n {
				return total, io.ErrShortWrite
			}
		}
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
	}
}

func pipe2(a, b io.ReadWriteCloser, label string) {
	var up, down int64
	var upErr, downErr error
	done := make(chan int, 2)
	go func() {
		up, upErr = copyChunks(a, b)
		done <- 1
	}()
	go func() {
		down, downErr = copyChunks(b, a)
		done <- 2
	}()
	first := <-done
	_ = a.Close()
	_ = b.Close()
	<-done
	logf("[%s] 会话结束：a<-b %.1f KiB / b<-a %.1f KiB；先结束的方向=%d；错误：a<-b=%v，b<-a=%v",
		label, float64(up)/1024, float64(down)/1024, first, upErr, downErr)
}

// sctpWindow 是 SCTP 接收窗口，单条隧道吞吐上限约为窗口 / RTT，取 4 MiB。
const sctpWindow = 4 << 20

// sctpAssociation 建 SCTP 关联，先判这条链路放不放得下 SCTP 的包。
func sctpAssociation(asServer bool, conn net.Conn, probed int) (*sctp.Association, error) {
	if !sctpFitsOnPath(probed) {
		return nil, fmt.Errorf("%s", mtuTooSmallHelp(probed))
	}
	cfg := sctp.Config{
		NetConn:              conn,
		MaxMessageSize:       chunkSize,
		MaxReceiveBufferSize: sctpWindow,
	}
	if asServer {
		return sctp.Server(cfg)
	}
	return sctp.Client(cfg)
}

func serveSCTPHost(iceConn net.Conn, raddr net.Addr, to string, id *identity, selfFP, peerFP [32]byte, onVerified func()) error {
	pc := wrapICEConn(iceConn, raddr)

	// 先探这条路能过多大的包，避免 MTU 黑洞（详见 pmtu.go）。
	probed, pcWrapped := probePathMTU(pc)
	t0 := time.Now()
	dtlsConn, err := dtls.Server(pcWrapped, pc.RemoteAddr(), &dtls.Config{
		Certificates:         []tls.Certificate{id.cert},
		CipherSuites:         certCipherSuites,
		ClientAuth:           dtls.RequireAnyClientCert,
		InsecureSkipVerify:   true,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
		MTU:                  dtlsMTUFor(probed),
	})
	if probed > 0 {
		guiDetailf(".. 路径探测：%d 字节可通过，DTLS 记录据此收窄", probed)
	} else {
		logf("路径探测无响应，使用默认 MTU")
	}
	if err != nil {
		return fmt.Errorf("DTLS 握手失败：%w", err)
	}
	logf("结论：DTLS 握手完成，耗时 %d ms，加密已就绪；身份校验使用应用层证明", time.Since(t0).Milliseconds())

	assoc, err := sctpAssociation(true, dtlsConn, probed)
	if err != nil {
		return fmt.Errorf("SCTP 关联建立失败：%w", err)
	}
	defer assoc.Close()
	logf("结论：SCTP 关联已建立，可靠、有序、带流控与重传、多流")

	// 用一个带缓冲通道接身份证明结论，确保退出时报真正原因而非 EOF。
	idFailCh := make(chan error, 1)

	for {
		s, err := assoc.AcceptStream()
		if err != nil {
			select {
			case e := <-idFailCh:
				return fmt.Errorf("身份绑定未通过，连接已断开：%w", e)
			case <-time.After(idVerdictWait):
				return fmt.Errorf("接受 SCTP 流失败：%w", err)
			}
		}

		// stream 0 是身份证明专用流，先办完再收数据流。
		if s.StreamIdentifier() == idStreamID {
			go func(s *sctp.Stream) {
				defer s.Close()
				der, err := proveIdentityHost(s, id, selfFP, peerFP)
				if err != nil {
					logf("✗ 身份证明失败，断开：%v", err)
					idFailCh <- err
					_ = assoc.Close()
					return
				}
				if err := reportIdentity(id, der); err != nil {
					logf("✗ %v", err)
					return
				}
				// 校验通过即视为在场，名单只认这一步。
				if onVerified != nil {
					onVerified()
				}
			}(s)
			continue
		}

		logf("[SCTP] 对端开了一条新流：stream=%d，转发到 %s", s.StreamIdentifier(), to)
		go func(s *sctp.Stream) {
			defer s.Close()
			up, err := net.DialTimeout("tcp", to, 5*time.Second)
			if err != nil {
				logf("[SCTP] 连接 %s 失败：%v", to, err)
				return
			}
			defer up.Close()
			pipe2(up, s, fmt.Sprintf("SCTP stream=%d", s.StreamIdentifier()))
		}(s)
	}
}

// guestAssoc 先办完身份证明并拿到 SCTP 关联，接客方式交给调用方。
func guestAssoc(iceConn net.Conn, raddr net.Addr, id *identity, selfFP, peerFP [32]byte) (*sctp.Association, error) {
	pc := wrapICEConn(iceConn, raddr)
	// 先探这条路上多大的包能过（详见 pmtu.go）。
	probed, pcWrapped := probePathMTU(pc)

	t0 := time.Now()
	dtlsConn, err := dtls.Client(pcWrapped, pc.RemoteAddr(), &dtls.Config{
		Certificates:         []tls.Certificate{id.cert},
		CipherSuites:         certCipherSuites,
		InsecureSkipVerify:   true,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
		MTU:                  dtlsMTUFor(probed),
	})
	if err != nil {
		return nil, fmt.Errorf("DTLS 握手失败：%w", err)
	}
	if probed > 0 {
		guiDetailf(".. 路径探测：%d 字节可通过，DTLS 记录据此收窄", probed)
	}
	logf("结论：DTLS 握手完成，耗时 %d ms，加密已就绪；身份校验使用应用层证明", time.Since(t0).Milliseconds())

	assoc, err := sctpAssociation(false, dtlsConn, probed)
	if err != nil {
		return nil, fmt.Errorf("SCTP 关联建立失败：%w", err)
	}
	logf("结论：SCTP 关联已建立，可靠、有序、带流控与重传、多流")

	// 先开专用流办身份证明，办不过不放行数据。
	ctrl, err := assoc.OpenStream(idStreamID, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		_ = assoc.Close()
		return nil, fmt.Errorf("开身份证明流失败：%w", err)
	}
	der, err := proveIdentityGuest(ctrl, id, selfFP, peerFP)
	_ = ctrl.Close()
	if err != nil {
		_ = assoc.Close()
		return nil, fmt.Errorf("身份证明失败：%w", err)
	}
	if err := reportIdentity(id, der); err != nil {
		_ = assoc.Close()
		return nil, err
	}
	return assoc, nil
}

// streamRWC 把隧道里的一条流包成 io.ReadWriteCloser。
type streamRWC struct{ s *sctp.Stream }

func (w streamRWC) Read(p []byte) (int, error)  { return w.s.Read(p) }
func (w streamRWC) Write(p []byte) (int, error) { return w.s.Write(p) }
func (w streamRWC) Close() error                { return w.s.Close() }

// guestTunnel 每来一条本地连接就开一条新流。
type guestTunnel struct {
	assoc *sctp.Association
	next  uint32
}

func newGuestTunnel(assoc *sctp.Association) *guestTunnel {
	// WebRTC 约定：DTLS 客户端用偶数流 ID；0 已占用，数据从 2 开始。
	return &guestTunnel{assoc: assoc, next: 2}
}

func (t *guestTunnel) open() (io.ReadWriteCloser, error) {
	sid := uint16(atomic.AddUint32(&t.next, 2) - 2)
	s, err := t.assoc.OpenStream(sid, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		return nil, err
	}
	return streamRWC{s: s}, nil
}

// serveGuestListener 把隧道暴露在本地或公网地址上；gate 非空时每条连接先过门卫。
func serveGuestListener(t *guestTunnel, local string, gate func(net.Conn)) error {
	ln, err := net.Listen("tcp", local)
	if err != nil {
		return fmt.Errorf("监听 %s 失败：%w", local, err)
	}
	logf("结论：本地隧道已就绪，游戏内连接 %s", ln.Addr())
	ref := &tunnelRef{}
	ref.set(t)
	return serveGuestListenerOn(ln, ref, gate)
}

// serveGuestListenerOn 收 tunnelRef 而非隧道本身：重连会换隧道，监听口不变，每个连接取当前隧道。
func serveGuestListenerOn(ln net.Listener, ref *tunnelRef, gate func(net.Conn)) error {
	guiDetailf(".. 本地监听已启动：%s", ln.Addr())
	for {
		tc, err := ln.Accept()
		if err != nil {
			return err
		}
		if gate != nil {
			go gate(tc)
			continue
		}
		t := ref.get()
		if t == nil {
			logf("[隧道] 拒绝本地连接 %s：正在重连，隧道暂不可用", tc.RemoteAddr())
			_ = tc.Close()
			continue
		}
		guiDetailf(".. [隧道] 收到本地连接 %s，开启 SCTP 流", tc.RemoteAddr())
		go func(tc net.Conn, t *guestTunnel) {
			defer tc.Close()
			up, err := t.open()
			if err != nil {
				logf("[隧道] 开流失败：%v", err)
				return
			}
			defer up.Close()
			pipe2(tc, up, "SCTP 流")
		}(tc, t)
	}
}

func serveSCTPGuest(iceConn net.Conn, raddr net.Addr, local string, id *identity, selfFP, peerFP [32]byte) error {
	assoc, err := guestAssoc(iceConn, raddr, id, selfFP, peerFP)
	if err != nil {
		return err
	}
	defer assoc.Close()
	return serveGuestListener(newGuestTunnel(assoc), local, nil)
}
