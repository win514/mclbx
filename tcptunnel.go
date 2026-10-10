package main

// tcptunnel.go TCP 兜底通道：UDP 不可用时联机；明文转发，房间码是唯一门槛。
// 帧为 [类型1][连接号4][长度4][数据]，类型 1=新连接 2=数据 3=关闭。

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	tcpFrameOpen  = 1
	tcpFrameData  = 2
	tcpFrameClose = 3

	tcpHandshake = "MCLBX-TCP1 "
	tcpMaxFrame  = 1 << 20

	// 帧头：类型(1) + 连接号(4) + 负载长度(4)
	tcpFrameHeader = 9

	// 单次写入时限，按次重设。
	tcpWriteTimeout = 30 * time.Second
)

// 多路复用
type tcpMux struct {
	conn net.Conn
	wmu  sync.Mutex // 帧不能交错，写要串行
	hdr  [tcpFrameHeader]byte
}

func (m *tcpMux) send(typ byte, id uint32, payload []byte) error {
	dbgFrame("→", typ, id, len(payload))

	m.wmu.Lock()
	defer m.wmu.Unlock()

	// 帧头复用同一数组（wmu 已保证写串行，避免每帧分配）
	m.hdr[0] = typ
	binary.BigEndian.PutUint32(m.hdr[1:], id)
	binary.BigEndian.PutUint32(m.hdr[5:], uint32(len(payload)))

	_ = m.conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))

	// 帧头与负载用 net.Buffers 一次写出（writev），不可用时自动退化为顺序写。
	bufs := net.Buffers{m.hdr[:]}
	if len(payload) > 0 {
		bufs = append(bufs, payload)
	}
	_, err := bufs.WriteTo(m.conn)
	return err
}

func readTcpFrame(br *bufio.Reader) (byte, uint32, []byte, error) {
	hdr := make([]byte, tcpFrameHeader)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return 0, 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[5:])
	if n > tcpMaxFrame {
		return 0, 0, nil, fmt.Errorf("帧长度异常：%d 字节", n)
	}
	var payload []byte
	if n > 0 {
		payload = make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return 0, 0, nil, err
		}
	}
	dbgFrame("←", hdr[0], binary.BigEndian.Uint32(hdr[1:]), int(n))
	return hdr[0], binary.BigEndian.Uint32(hdr[1:]), payload, nil
}

// 帧级调试开关：设 MCLBX_TCP_DEBUG=1 输出每帧的类型/连接号/长度。
var tcpDebug = os.Getenv("MCLBX_TCP_DEBUG") != ""

func frameName(t byte) string {
	switch t {
	case tcpFrameOpen:
		return "OPEN"
	case tcpFrameData:
		return "DATA"
	case tcpFrameClose:
		return "CLOSE"
	}
	return fmt.Sprintf("?%d", t)
}

func dbgFrame(dir string, typ byte, id uint32, n int) {
	if tcpDebug {
		fmt.Printf("   [帧] %s %s id=%d len=%d\n", dir, frameName(typ), id, n)
	}
}

// relayRoomName 归一化并校验中继房间名：禁止控制字符（会破坏按行协议），统一小写去首尾空白。
func relayRoomName(raw string) (string, error) {
	if len(raw) > 128 {
		return "", fmt.Errorf("房间名过长：%d 字节，上限 128", len(raw))
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("房间名不能包含控制字符 0x%02x，控制字符会破坏协议行", r)
		}
	}
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "", fmt.Errorf("房间名为空")
	}
	return name, nil
}

func dialRelay(relayAddr, room string) (net.Conn, *bufio.Reader, error) {
	// 先校验再连，不合法时不在发起侧占用连接
	name, err := relayRoomName(room)
	if err != nil {
		return nil, nil, fmt.Errorf("房间名不合法：%w", err)
	}
	c, err := net.DialTimeout("tcp", relayAddr, 8*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("无法连接中继 %s：%w", relayAddr, err)
	}
	if _, err := fmt.Fprintf(c, "%s%s\n", tcpHandshake, name); err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	return c, bufio.NewReader(c), nil
}

// 中继服务

func cmdRelay(args []string) error {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	listen := fs.String("listen", defaultListenAddr, "监听地址；这是对外服务，默认双栈（IPv4 与 IPv6 都收）")
	wait := fs.Duration("pair-wait", 3*time.Minute, "等待对端出现的最长时间")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败：%w", *listen, err)
	}
	fmt.Printf("================================================\n")
	fmt.Printf(" %s · TCP 中继\n", version)
	fmt.Printf("================================================\n")
	fmt.Printf(" 监听     : %s\n", ln.Addr())
	fmt.Printf(" 用途     : UDP 被封禁时的中继转发通道，双方均主动连接此处\n")
	fmt.Printf(" 说明     : 中继仅在同一房间的两条连接之间转发字节，不检查、不存储内容\n")
	fmt.Printf(" 提示     : 本条通道为明文传输，中继机器可查看流量；优先使用直连模式\n")
	fmt.Printf("------------------------------------------------\n")
	fmt.Println(" 房主：mclbx tcptunnel host  --relay <本机公网地址>:8090 --room <房间码> --to 127.0.0.1:25565")
	fmt.Println(" 玩家：mclbx tcptunnel guest --relay <同上>                  --room <同上>   --local 127.0.0.1:25565")
	// 直接拼出该填的内容
	if _, port, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		if guess := firstNonLoopbackIPv4(); guess != "" {
			fmt.Printf("------------------------------------------------\n")
			fmt.Printf(" 房主端填写此行：--relay-server tcp:%s:%s\n", guess, port)
			fmt.Printf("   注意：%s 为本机网卡地址；若本机位于 NAT 或云环境后，请改为公网 IP 或域名\n", guess)
		}
	}
	fmt.Println(" Ctrl+C 退出。")

	var (
		mu      sync.Mutex
		waiting = map[string]chan relayPeer{}
	)
	go serveRelay(ln, *wait, &mu, waiting)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	_ = ln.Close()
	fmt.Println("\n 中继已停止。")
	return nil
}

// relayPeer 必须连缓冲读取器一起交给配对方，否则其缓冲区中的已读字节会丢失。
type relayPeer struct {
	c  net.Conn
	br *bufio.Reader
}

// serveRelay 只在同一房间的两条连接之间转发字节。
func serveRelay(ln net.Listener, wait time.Duration, mu *sync.Mutex, waiting map[string]chan relayPeer) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go serveRelayConn(c, wait, mu, waiting)
	}
}

// serveRelayConn 处理一条中继连接：读取房间码后配对或等待。
func serveRelayConn(c net.Conn, wait time.Duration, mu *sync.Mutex, waiting map[string]chan relayPeer) {
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, tcpHandshake) {
		_ = c.Close()
		return
	}
	room := strings.TrimSpace(strings.TrimPrefix(line, tcpHandshake))
	if room == "" {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	_ = c.SetDeadline(time.Time{})

	mu.Lock()
	if ch, ok := waiting[room]; ok {
		delete(waiting, room)
		mu.Unlock()
		ch <- relayPeer{c: c, br: br} // 连缓冲读取器一起交出（不能关 c）
		return
	}
	ch := make(chan relayPeer, 1)
	waiting[room] = ch
	mu.Unlock()

	// 定期报告等待对端的状态；只报其他等待房间数量，不泄露其房间码。
	hintDone := make(chan struct{})
	go func() {
		t := time.NewTicker(relayWaitHintEvery)
		defer t.Stop()
		for {
			select {
			case <-hintDone:
				return
			case <-t.C:
				mu.Lock()
				others := 0
				for k := range waiting {
					if k != room {
						others++
					}
				}
				mu.Unlock()
				if others > 0 {
					fmt.Printf(" [中继] 房间 %s 仍在等待对端；当前有 %d 个其他房间也在等待。"+
						"若双方应为同一房间，请核对链接中的 room= 参数，大小写与首尾空白已自动统一\n",
						room, others)
				} else {
					fmt.Printf(" [中继] 房间 %s 仍在等待对端，当前没有其他房间在等待\n", room)
				}
			}
		}
	}()

	select {
	case peer := <-ch:
		close(hintDone)
		fmt.Printf(" [中继] 房间 %s 配对成功，开始转发\n", room)
		// 一侧收尾时关闭两侧，避免半开连接泄漏 fd
		done := make(chan struct{}, 2)
		go func() {
			_, _ = io.Copy(peer.c, br) // 我方 → 对方（用我方的缓冲读取器）
			_ = peer.c.Close()
			done <- struct{}{}
		}()
		go func() {
			_, _ = io.Copy(c, peer.br) // 对方 → 我方（用对方的缓冲读取器，其中可能已有数据）
			_ = c.Close()
			done <- struct{}{}
		}()
		<-done
		<-done
		fmt.Printf(" [中继] 房间 %s 结束\n", room)
	case <-time.After(wait):
		close(hintDone)
		mu.Lock()
		delete(waiting, room)
		mu.Unlock()
		_ = c.Close()
		fmt.Printf(" [中继] 房间 %s 等待 %s 无对端接入，断开连接\n", room, wait)
	}
}

// relayWaitHintEvery 是「还在等对端」提示的间隔，设为变量以便测试缩短。
var relayWaitHintEvery = 5 * time.Second

// 两端桥接

func cmdTcpTunnel(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：mclbx tcptunnel host|guest ...，详细选项见 mclbx help")
	}
	switch args[0] {
	case "host":
		return tcpTunnelHost(args[1:])
	case "guest":
		return tcpTunnelGuest(args[1:])
	}
	return fmt.Errorf("tcptunnel 仅支持 host / guest，收到 %q", args[0])
}

func tcpTunnelHost(args []string) error {
	fs := flag.NewFlagSet("tcptunnel host", flag.ContinueOnError)
	relay := fs.String("relay", "", "中继地址，形如 1.2.3.4:8090")
	relayServer := fs.String("relay-server", "", "中继服务器链接，也可直接写 tcp:主机:端口，为 --relay 的替代写法")
	room := fs.String("room", "", "房间码；两端必须一致。链接里带着房间码时可以不填")
	to := fs.String("to", "127.0.0.1:25565", "连通后流量的本地转发目标")
	idle := fs.Duration("idle", tcpIdleTimeout, "有玩家连接时，链路无数据多久判定掉线；0 表示不检测")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	tcpIdleTimeout = *idle
	addr, err := resolveTCPRelayAddr(*relayServer, *relay)
	if err != nil {
		return err
	}
	roomCode := tcpTunnelRoom(*relayServer, *room)
	if roomCode == "" {
		return fmt.Errorf("缺少房间码，两端须填写相同值。" +
			"若已获得中继链接，其中通常包含房间码，用 --relay-server 传入即可；" +
			"否则请两端用 --room 填写相同的房间码")
	}
	return tcpTunnelHostRun(addr, roomCode, *to)
}

// tcpTunnelRoom 决定房间码：命令行优先，其次从中继链接取。
func tcpTunnelRoom(relayServer, given string) string {
	if r := strings.TrimSpace(given); r != "" {
		return r
	}
	if s := strings.TrimSpace(relayServer); s != "" {
		if plan, err := resolveRelayLink(s); err == nil && plan != nil && plan.Room != "" {
			return plan.Room
		}
	}
	if plan, err := activeRelayPlan(); err == nil && plan != nil {
		return plan.Room
	}
	return ""
}

// tcpIdleTimeout 是「有玩家连着时中继链路多久无帧即判死」的阈值（0 = 不检测）。
// 无玩家连接时不设超时；设为变量以便测试缩短，命令行用 --idle 覆盖。
var tcpIdleTimeout = 2 * time.Minute

// armIdleDeadline 按通道是否被使用决定是否设读超时，返回本次是否设置。
func armIdleDeadline(c net.Conn, active bool) bool {
	if tcpIdleTimeout <= 0 {
		return false
	}
	if active {
		_ = c.SetReadDeadline(time.Now().Add(tcpIdleTimeout))
		return true
	}
	_ = c.SetReadDeadline(time.Time{})
	return false
}

// isTimeoutErr 判断是否为读超时。
func isTimeoutErr(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// tcpTunnelHostRun 房主侧：把中继上进来的连接转给本地目标
func tcpTunnelHostRun(relay, room, to string) error {
	c, br, err := dialRelay(relay, room)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Printf(" 已连接中继 %s，房间 %s，等待玩家接入… 转发至 %s\n", relay, room, to)

	m := &tcpMux{conn: c}
	var (
		mu    sync.Mutex
		conns = map[uint32]net.Conn{}
	)
	get := func(id uint32) net.Conn {
		mu.Lock()
		defer mu.Unlock()
		return conns[id]
	}
	countConns := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(conns)
	}
	for {
		armed := armIdleDeadline(c, countConns() > 0)
		typ, id, payload, err := readTcpFrame(br)
		if err != nil {
			if armed && isTimeoutErr(err) {
				fmt.Printf(" 中继链路连续 %s 无数据，但仍有玩家连接，判定对端已掉线，主动断开\n", tcpIdleTimeout)
				return nil
			}
			fmt.Printf(" 中继连接结束：%v\n", err)
			return nil
		}
		switch typ {
		case tcpFrameOpen:
			up, err := net.DialTimeout("tcp", to, 6*time.Second)
			if err != nil {
				fmt.Printf(" [%d] 无法连接本地目标 %s：%v\n", id, to, err)
				_ = m.send(tcpFrameClose, id, nil)
				continue
			}
			mu.Lock()
			conns[id] = up
			mu.Unlock()
			fmt.Printf(" [%d] 玩家接入，已连到 %s\n", id, to)
			go func(id uint32, up net.Conn) {
				defer func() {
					_ = m.send(tcpFrameClose, id, nil)
					_ = up.Close()
					mu.Lock()
					delete(conns, id)
					mu.Unlock()
				}()
				buf := make([]byte, 32*1024)
				for {
					n, err := up.Read(buf)
					if n > 0 {
						if m.send(tcpFrameData, id, buf[:n]) != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(id, up)
		case tcpFrameData:
			if up := get(id); up != nil {
				_ = up.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))
				if _, err := up.Write(payload); err != nil {
					_ = m.send(tcpFrameClose, id, nil)
				}
			}
		case tcpFrameClose:
			if up := get(id); up != nil {
				_ = up.Close()
				mu.Lock()
				delete(conns, id)
				mu.Unlock()
			}
		}
	}
}

func tcpTunnelGuest(args []string) error {
	fs := flag.NewFlagSet("tcptunnel guest", flag.ContinueOnError)
	relay := fs.String("relay", "", "中继地址，形如 1.2.3.4:8090")
	relayServer := fs.String("relay-server", "", "中继服务器链接，也可直接写 tcp:主机:端口，为 --relay 的替代写法")
	room := fs.String("room", "", "房间码；两端必须一致。链接里带着房间码时可以不填")
	local := fs.String("local", "127.0.0.1:25565", "把通道暴露在本机哪个地址")
	idle := fs.Duration("idle", tcpIdleTimeout, "有连接使用本条通道时，链路无数据多久判定掉线；0 表示不检测")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	tcpIdleTimeout = *idle
	addr, err := resolveTCPRelayAddr(*relayServer, *relay)
	if err != nil {
		return err
	}
	roomCode := tcpTunnelRoom(*relayServer, *room)
	if roomCode == "" {
		return fmt.Errorf("缺少房间码，两端须填写相同值。" +
			"若已获得中继链接，其中通常包含房间码，用 --relay-server 传入即可；" +
			"否则请两端用 --room 填写相同的房间码")
	}

	ln, err := net.Listen("tcp", *local)
	if err != nil {
		return fmt.Errorf("本机 %s 监听失败，可能已被占用：%w", *local, err)
	}
	defer ln.Close()
	return tcpTunnelGuestOn(addr, roomCode, ln)
}

// tcpTunnelGuestOn 玩家侧：把本机监听到的连接送进中继（监听器由调用方提供以便测试）。
func tcpTunnelGuestOn(relay, room string, ln net.Listener) error {
	c, br, err := dialRelay(relay, room)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Printf(" 已连接中继 %s，房间 %s\n", relay, room)
	fmt.Printf(" 通道已就绪：在游戏中连接 %s 即可，等待房主侧连接同一房间\n", ln.Addr())

	m := &tcpMux{conn: c}
	var (
		mu     sync.Mutex
		locals = map[uint32]net.Conn{}
		idSeq  uint32
	)
	nextID := func() uint32 {
		mu.Lock()
		defer mu.Unlock()
		idSeq++
		for locals[idSeq] != nil { // 端口号冲突则递增
			idSeq++
		}
		return idSeq
	}

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			id := nextID()
			mu.Lock()
			locals[id] = c
			mu.Unlock()
			if err := m.send(tcpFrameOpen, id, nil); err != nil {
				_ = c.Close()
				return
			}
			fmt.Printf(" [%d] 本机有连接进来，已通过中继发出\n", id)
			go func(id uint32, c net.Conn) {
				buf := make([]byte, 32*1024)
				defer func() {
					_ = m.send(tcpFrameClose, id, nil)
					_ = c.Close()
					mu.Lock()
					delete(locals, id)
					mu.Unlock()
				}()
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if m.send(tcpFrameData, id, buf[:n]) != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(id, c)
		}
	}()

	countLocals := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(locals)
	}

	for {
		armed := armIdleDeadline(c, countLocals() > 0)
		typ, id, payload, err := readTcpFrame(br)
		if err != nil {
			if armed && isTimeoutErr(err) {
				fmt.Printf(" 中继链路连续 %s 无数据，但本机仍有连接在使用，判定对端已掉线，主动断开\n", tcpIdleTimeout)
				return nil
			}
			fmt.Printf(" 中继连接结束：%v\n", err)
			return nil
		}
		mu.Lock()
		c := locals[id]
		mu.Unlock()
		switch typ {
		case tcpFrameData:
			if c != nil {
				_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
				_, _ = c.Write(payload)
			}
		case tcpFrameClose:
			if c != nil {
				_ = c.Close()
			}
		}
	}
}

// newRoomCodeTCP 生成房间码，字母表去掉易混字符。
func newRoomCodeTCP() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprint(time.Now().UnixNano() % 1000000)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
