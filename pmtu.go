package main

// pmtu.go —— 建链前探测路径 MTU，决定给 DTLS 的记录上限与能否承载 SCTP。
//
// 每一侧按一组尺寸发探测包（1400/1200/1000/800/600 字节 UDP 载荷），对端用同样大小回一个应答；
// 「发出的 N 字节收到应答」即「N 字节两向都能通过」，取最大值减去协议开销即 DTLS MTU。

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	pmtuMagic = "MCLBXPMT" // 8 字节前缀，避免和别的流量混淆
	pmtuQuery = 'Q'        // 我发的探测
	pmtuEcho  = 'E'        // 对探测的应答
)

// 探测尺寸（UDP 载荷）。上限 1400；1200~1400 间加密（1250/1300）以贴合 SCTP 约 1225 字节的包。
var pmtuSizes = []int{1400, 1300, 1250, 1200, 1000, 800, 600}

// 周期性重发，拿到最大尺寸回音即提前结束；两端探测起始时间可能不同，只发一次会双双失败。
const (
	pmtuWindow = 1200 * time.Millisecond // 总时长上限
	pmtuResend = 250 * time.Millisecond  // 重发间隔
	pmtuReadIt = 80 * time.Millisecond   // 单次读取等待，便于兼顾重发
)

// buildPmtuProbe 造一个探测/应答包：魔数 + 类型 + 长度 + 填充
func buildPmtuProbe(kind byte, size int) []byte {
	if size < len(pmtuMagic)+5 {
		size = len(pmtuMagic) + 5
	}
	b := make([]byte, size)
	copy(b, pmtuMagic)
	b[len(pmtuMagic)] = kind
	binary.BigEndian.PutUint32(b[len(pmtuMagic)+1:], uint32(size))
	return b
}

// parsePmtuProbe 解析探测包，返回类型与声明长度。
func parsePmtuProbe(b []byte) (byte, int, bool) {
	if len(b) < len(pmtuMagic)+5 {
		return 0, 0, false
	}
	if string(b[:len(pmtuMagic)]) != pmtuMagic {
		return 0, 0, false
	}
	kind := b[len(pmtuMagic)]
	declared := int(binary.BigEndian.Uint32(b[len(pmtuMagic)+1:]))
	if kind != pmtuQuery && kind != pmtuEcho {
		return 0, 0, false
	}
	return kind, declared, true
}

// pickMTU 从收到应答的尺寸中取最大值，无则返回 0。
func pickMTU(echoed map[int]bool) int {
	best := 0
	for _, s := range pmtuSizes {
		if echoed[s] && s > best {
			best = s
		}
	}
	return best
}

// dtlsMTUFor 将可通过的包大小换算为 DTLS MTU：扣除约 100 字节开销，限幅 [400,1400]。
func dtlsMTUFor(probed int) int {
	if probed <= 0 {
		return 0 // 探测失败，交调用方决定
	}
	mtu := probed - 100
	if mtu < 400 {
		mtu = 400 // 下限
	}
	if mtu > 1400 {
		mtu = 1400
	}
	return mtu
}

// pmtuConn 把探测期间收到的非探测包缓存并优先交给上层（如先到的 DTLS 握手包）。
type pmtuConn struct {
	net.PacketConn

	ra      net.Addr // 对端地址（DTLS 用于核对来源）
	mu      sync.Mutex
	pending [][]byte
}

func (c *pmtuConn) stash(b []byte) {
	cp := append([]byte(nil), b...)
	c.mu.Lock()
	c.pending = append(c.pending, cp)
	c.mu.Unlock()
}

func (c *pmtuConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	if len(c.pending) > 0 {
		b := c.pending[0]
		c.pending = c.pending[1:]
		c.mu.Unlock()
		n := copy(p, b)
		return n, c.ra, nil
	}
	c.mu.Unlock()
	return c.PacketConn.ReadFrom(p)
}

// probePathMTU 跑一次探测，返回最大可通过 UDP 载荷与供 DTLS 使用的连接；失败返回 0。
func probePathMTU(pc net.PacketConn) (int, net.PacketConn) {
	var ra net.Addr
	if rc, ok := pc.(interface{ RemoteAddr() net.Addr }); ok {
		ra = rc.RemoteAddr()
	}
	wrapped := &pmtuConn{PacketConn: pc, ra: ra}
	if pc == nil {
		return 0, wrapped
	}

	// 周期重发边发边收；收到最大尺寸回音立即收工。
	echoed := map[int]bool{}
	deadline := time.Now().Add(pmtuWindow)
	lastSend := time.Time{}
	buf := make([]byte, 2048)
	biggest := pmtuSizes[0]
	for time.Now().Before(deadline) {
		if time.Since(lastSend) >= pmtuResend {
			for _, s := range pmtuSizes {
				_, _ = pc.WriteTo(buildPmtuProbe(pmtuQuery, s), nil)
			}
			lastSend = time.Now()
		}
		_ = pc.SetReadDeadline(time.Now().Add(pmtuReadIt))
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			continue // 超时，继续重发/读取
		}
		kind, size, ok := parsePmtuProbe(buf[:n])
		if !ok {
			wrapped.stash(buf[:n]) // 非探测包，可能是已开始的 DTLS 握手
			continue
		}
		switch kind {
		case pmtuQuery:
			// 回同长度应答（按实际收到长度回，使对端确认该长度可通过）
			_, _ = pc.WriteTo(buildPmtuProbe(pmtuEcho, n), nil)
			_ = size
		case pmtuEcho:
			echoed[size] = true
			if size >= biggest {
				_ = pc.SetReadDeadline(time.Time{})
				return pickMTU(echoed), wrapped
			}
		}
	}
	_ = pc.SetReadDeadline(time.Time{})

	return pickMTU(echoed), wrapped
}

// 链路 MTU 不足时 SCTP 出站包（下限 1191 字节 + DTLS 记录头）会被静默丢弃；在 SCTP 外拆分写入无效。

// sctpNeedsPayload：SCTP 隧道的硬性下限 = 它的出站包 1191 字节 + DTLS 记录头开销约 40 字节。
const sctpNeedsPayload = 1231

// sctpFitsOnPath 报告这条链路能不能放下 SCTP 的包。
// probed <= 0 表示"没探到结果"，此时不做判断（交给协议栈按默认值试）。
func sctpFitsOnPath(probed int) bool {
	return probed <= 0 || probed >= sctpNeedsPayload
}

// mtuTooSmallHelp 返回 MTU 过小时的替代方案提示。
func mtuTooSmallHelp(probed int) string {
	return fmt.Sprintf(
		"这条链路的 MTU 太小：实测可通过的最大载荷为 %d 字节，SCTP 隧道最小分包为 1191 字节，加记录头约 %d 字节。\n"+
			"   该链路无法承载 SCTP 的数据包，常见于 PPPoE、部分移动网络及多层隧道链路。\n"+
			"   建议：\n"+
			"     1) 改用中继转发，走 TCP，分片由 TCP 处理，不受此限制：\n"+
			"        公网机器：mclbx relay --listen :8090\n"+
			"        房主：      mclbx tcptunnel host  --relay <公网>:8090 --room <房间码> --to 127.0.0.1:25565\n"+
			"        玩家：      mclbx tcptunnel guest --relay <公网>:8090 --room <房间码> --local 127.0.0.1:25565\n"+
			"     2) 检查链路是否还存在其他隧道，如 VPN、加速器、双拨；将 MTU 调整到 1400 以上。",
		probed, sctpNeedsPayload)
}
