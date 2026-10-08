package main

// identity_flow_test.go 在内存消息流上运行完整身份证明握手，覆盖诚实、双方假指纹与冒充四种情况。

import (
	"crypto/sha256"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStream 消息语义的内存流，一次 Write 对应一次 Read。
type memStream struct {
	in     chan []byte
	out    chan []byte
	cur    []byte
	once   sync.Once
	closed bool
	mu     sync.Mutex
}

func memPair() (*memStream, *memStream) {
	ab := make(chan []byte, 64)
	ba := make(chan []byte, 64)
	return &memStream{in: ba, out: ab}, &memStream{in: ab, out: ba}
}

func (m *memStream) Write(p []byte) (int, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	m.mu.Unlock()
	buf := make([]byte, len(p))
	copy(buf, p)
	m.out <- buf
	return len(p), nil
}

func (m *memStream) Read(p []byte) (int, error) {
	for {
		if len(m.cur) > 0 {
			n := copy(p, m.cur)
			m.cur = m.cur[n:]
			return n, nil
		}
		msg, ok := <-m.in
		if !ok || msg == nil { // 对端关闭
			return 0, io.EOF
		}
		m.cur = msg
	}
}

func (m *memStream) Close() error {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		close(m.out)
	})
	return nil
}

type flowResult struct {
	der []byte
	err error
}

// runHandshake 并发运行两端，返回（主机结果, 客户机结果）。
func runHandshake(t *testing.T, hostFP, guestFP [32]byte, hostID, guestID *identity) (flowResult, flowResult) {
	t.Helper()
	hs, gs := memPair()
	defer hs.Close()
	defer gs.Close()

	hch := make(chan flowResult, 1)
	gch := make(chan flowResult, 1)
	go func() {
		d, e := proveIdentityHost(hs, hostID, hostFP, guestFP)
		hch <- flowResult{d, e}
	}()
	go func() {
		d, e := proveIdentityGuest(gs, guestID, guestFP, hostFP)
		gch <- flowResult{d, e}
	}()

	waitOne := func(ch chan flowResult, who string) flowResult {
		select {
		case r := <-ch:
			return r
		case <-time.After(5 * time.Second):
			t.Fatalf("%s 侧卡住了（超时）", who)
			return flowResult{}
		}
	}
	return waitOne(hch, "主机"), waitOne(gch, "客户机")
}

func newTestIdentity(t *testing.T) *identity {
	t.Helper()
	id, err := newIdentity()
	if err != nil {
		t.Fatalf("生成身份失败：%v", err)
	}
	return id
}

// 双方都诚实时都通过，且安全码一致。
func TestFlowBothHonest(t *testing.T) {
	old := idRejectFlush
	idRejectFlush = 0
	defer func() { idRejectFlush = old }()

	hostID := newTestIdentity(t)
	guestID := newTestIdentity(t)

	h, g := runHandshake(t, hostID.fp, guestID.fp, hostID, guestID)
	if h.err != nil {
		t.Fatalf("主机侧应当通过，却失败：%v", h.err)
	}
	if g.err != nil {
		t.Fatalf("客户机侧应当通过，却失败：%v", g.err)
	}

	// 对面拿到的证书须为本端真实证书
	if got := sha256Sum(g.der); got != hostID.fp {
		t.Errorf("客户机拿到的主机证书不对")
	}
	if got := sha256Sum(h.der); got != guestID.fp {
		t.Errorf("主机拿到的客户机证书不对")
	}

	// 两端独立算出的安全码须一致
	hs := safetyCode(hostID.fp, guestID.fp)
	gs := safetyCode(guestID.fp, hostID.fp)
	if hs != gs {
		t.Errorf("两端安全码不一致：主机=%s 客户机=%s", hs, gs)
	}
}

// 主机公布假指纹时，双方都必须失败。
func TestFlowTamperedHostFailsBothSides(t *testing.T) {
	old := idRejectFlush
	idRejectFlush = 0
	defer func() { idRejectFlush = old }()

	hostID := newTestIdentity(t)
	guestID := newTestIdentity(t)

	lie := hostID.fp
	lie[0] ^= 0xFF

	h, g := runHandshake(t, lie, guestID.fp, hostID, guestID)
	if h.err == nil {
		t.Error("主机被篡改后仍然宣称绑定成功——这正是要避免的")
	} else if !strings.Contains(h.err.Error(), "不一致") {
		t.Errorf("主机侧报错没点明是指纹不一致：%v", h.err)
	}
	if g.err == nil {
		t.Error("客户机没有拦住被篡改的主机")
	} else if !strings.Contains(g.err.Error(), "不一致") {
		t.Errorf("客户机侧报错没点明是指纹不一致：%v", g.err)
	}
}

// 客户机公布假指纹时，双方都必须失败。
func TestFlowTamperedGuestFailsBothSides(t *testing.T) {
	old := idRejectFlush
	idRejectFlush = 0
	defer func() { idRejectFlush = old }()

	hostID := newTestIdentity(t)
	guestID := newTestIdentity(t)

	lie := guestID.fp
	lie[0] ^= 0xFF

	h, g := runHandshake(t, hostID.fp, lie, hostID, guestID)
	if h.err == nil {
		t.Error("主机没有拦住被篡改的客户机")
	} else if !strings.Contains(h.err.Error(), "不一致") {
		t.Errorf("主机侧报错没点明是指纹不一致：%v", h.err)
	}
	if g.err == nil {
		t.Error("客户机被篡改后仍然宣称绑定成功")
	} else if !strings.Contains(g.err.Error(), "不一致") {
		t.Errorf("客户机侧报错没点明是指纹不一致：%v", g.err)
	}
}

// 冒充者公布他人合法指纹但用自己私钥签名，指纹能过而签名必失败。
func TestFlowImpostorFailsOnSignature(t *testing.T) {
	old := idRejectFlush
	idRejectFlush = 0
	defer func() { idRejectFlush = old }()

	victim := newTestIdentity(t) // 被冒充者的身份（指纹会公布出去）
	impostor := newTestIdentity(t)
	guestID := newTestIdentity(t)

	hs, gs := memPair()
	defer hs.Close()
	defer gs.Close()

	herr := make(chan error, 1)
	gerr := make(chan error, 1)

	go func() {
		// 手写主机流程：证书换为受害者的，签名仍用自己的私钥
		gm, err := readIDMsg(hs)
		if err != nil {
			herr <- err
			return
		}
		hN := []byte("0123456789abcdef")
		if err := writeIDMsg(hs, &idMsg{Nonce: hN}); err != nil {
			herr <- err
			return
		}
		ch := idChallenge(guestID.fp[:], victim.fp[:], gm.Nonce, hN)
		if _, err := readIDMsg(hs); err != nil {
			herr <- err
			return
		}
		sig, err := impostor.sign(ch) // 用错误私钥签名
		if err != nil {
			herr <- err
			return
		}
		if err := writeIDMsg(hs, &idMsg{Nonce: hN, Cert: victim.cert.Certificate[0], Sig: sig}); err != nil {
			herr <- err
			return
		}
		_, err = readIDMsg(hs) // 等对方回执（预期为拒绝）
		herr <- err
	}()

	go func() {
		_, err := proveIdentityGuest(gs, guestID, guestID.fp, victim.fp)
		gerr <- err
	}()

	select {
	case e := <-gerr:
		if e == nil {
			t.Fatal("客户机应当识破冒充者")
		}
		if !strings.Contains(e.Error(), "签名") {
			t.Errorf("客户机应报签名验证失败，实际：%v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("客户机侧卡住了（超时）")
	}
	select {
	case <-herr:
	case <-time.After(5 * time.Second):
		t.Fatal("冒充者侧卡住了（超时）")
	}
}

func sha256Sum(b []byte) [32]byte {
	return sha256.Sum256(b)
}
