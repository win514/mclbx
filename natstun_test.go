package main

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStun 是最小 STUN 服务端：回送请求方的源地址并记录观察到的源端口；portShift 用于模拟对称型 NAT。
type fakeStun struct {
	conn      *net.UDPConn
	portShift int

	mu   sync.Mutex
	seen []int
}

func newFakeStun(t *testing.T, portShift int) *fakeStun {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("起假 STUN 服务端失败：%v", err)
	}
	f := &fakeStun{conn: c, portShift: portShift}
	go f.serve()
	t.Cleanup(func() { _ = c.Close() })
	return f
}

func (f *fakeStun) addr() string { return f.conn.LocalAddr().String() }

// ports 返回观察到的所有源端口（按到达顺序）。
func (f *fakeStun) ports() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.seen...)
}

func (f *fakeStun) serve() {
	buf := make([]byte, 2048)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 20 {
			continue
		}
		f.mu.Lock()
		f.seen = append(f.seen, from.Port)
		f.mu.Unlock()

		ip4 := from.IP.To4()
		if ip4 == nil {
			continue
		}

		resp := make([]byte, 20, 32)
		binary.BigEndian.PutUint16(resp[0:], 0x0101) // Binding Success Response
		binary.BigEndian.PutUint32(resp[4:], stunMagic)
		copy(resp[8:], buf[8:20]) // 事务号原样带回

		attr := make([]byte, 12)
		binary.BigEndian.PutUint16(attr[0:], 0x0020) // XOR-MAPPED-ADDRESS
		binary.BigEndian.PutUint16(attr[2:], 8)
		attr[5] = 0x01 // 地址族 IPv4
		binary.BigEndian.PutUint16(attr[6:], uint16(from.Port+f.portShift)^uint16(stunMagic>>16))
		binary.BigEndian.PutUint32(attr[8:], binary.BigEndian.Uint32(ip4)^stunMagic)

		resp = append(resp, attr...)
		binary.BigEndian.PutUint16(resp[2:], uint16(len(attr)))
		_, _ = f.conn.WriteToUDP(resp, from)
	}
}

// 用同一个 socket 问多台服务器时，正常的无 NAT 路径必须判成锥形。
func TestStunProbeSharedJudgesCone(t *testing.T) {
	a := newFakeStun(t, 0)
	b := newFakeStun(t, 0)

	got := stunProbeShared([]string{a.addr(), b.addr()}, time.Second)
	if len(got) != 2 {
		t.Fatalf("期望拿到 2 台服务器的结果，实际 %d：%+v", len(got), got)
	}
	if got[0].Port != got[1].Port {
		t.Errorf("同一个 socket 打两台服务器，映射端口应当一致，实际 %d / %d", got[0].Port, got[1].Port)
	}

	// 两台服务器必须看到同一个源端口，才证明只开了一个 socket。
	pa, pb := a.ports(), b.ports()
	if len(pa) == 0 || len(pb) == 0 {
		t.Fatalf("服务器没收到请求：a=%v b=%v", pa, pb)
	}
	if pa[0] != pb[0] {
		t.Errorf("两台服务器看到的源端口不同（%d / %d），说明探测并没有共用一个 socket", pa[0], pb[0])
	}

	if v := natVerdict(got); !strings.Contains(v, "锥形") {
		t.Errorf("无 NAT 的正常路径应当判成锥形，实际：%s", v)
	}
}

// 多台服务器看到不同端口时必须判成对称型。
func TestStunProbeSharedJudgesSymmetric(t *testing.T) {
	a := newFakeStun(t, 100)
	b := newFakeStun(t, 200)

	got := stunProbeShared([]string{a.addr(), b.addr()}, time.Second)
	if len(got) != 2 {
		t.Fatalf("期望两台服务器的结果，实际 %d：%+v", len(got), got)
	}
	if v := natVerdict(got); !strings.Contains(v, "对称型") {
		t.Errorf("端口不一致应当判成对称型，实际：%s", v)
	}
}

// 只有一台服务器有应答时不能下结论。
func TestNatVerdictOneServerCannotDecide(t *testing.T) {
	v := natVerdict([]stunResult{{IP: net.IPv4(1, 2, 3, 4), Port: 1234}})
	if !strings.Contains(v, "无法判定") {
		t.Errorf("只有一台服务器有应答时不该下结论，实际：%s", v)
	}
}

// 部分服务器端口一致时判为不稳定。
func TestNatVerdictMixed(t *testing.T) {
	ip := net.IPv4(1, 2, 3, 4)
	rs := []stunResult{{IP: ip, Port: 5000}, {IP: ip, Port: 5000}, {IP: ip, Port: 5001}}
	if v := natVerdict(rs); !strings.Contains(v, "不稳定") {
		t.Errorf("部分一致时应报不稳定，实际：%s", v)
	}
}

// 无任何应答时返回「未知」，不崩。
func TestNatVerdictEmpty(t *testing.T) {
	if v := natVerdict(nil); v != "未知" {
		t.Errorf("无应答时应返回「未知」，实际：%s", v)
	}
}

// stunQuery（自建 socket 的单次查询）应能正常工作。
func TestStunQueryRoundTrip(t *testing.T) {
	a := newFakeStun(t, 0)
	r, err := stunQuery(a.addr(), time.Second)
	if err != nil {
		t.Fatalf("stunQuery 失败：%v", err)
	}
	if r.Port == 0 || r.IP.To4() == nil {
		t.Errorf("没解析出映射地址：%+v", r)
	}
}
