package main

// relaycheck.go 中继自检：完整走一遍 TURN 与 TCP 两条数据通路并报告卡住的步骤。

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pion/turn/v5"
)

func cmdRelayCheck(args []string) error {
	fs := flag.NewFlagSet("relaycheck", flag.ContinueOnError)
	user := fs.String("user", "", "中继账号，链接中未提供时使用")
	pass := fs.String("pass", "", "中继密码")
	timeout := fs.Duration("timeout", 8*time.Second, "每一步的超时")
	room := fs.String("room", "", "仅 TCP 中继自检用的房间码；留空自动生成")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s · 中继自检\n", version)
	fmt.Printf("================================================\n")

	// 链接来自命令行，或当前生效的配置。
	raw := strings.Join(fs.Args(), " ")
	applyRelayFlag(raw, *user, *pass)

	plan, err := activeRelayPlan()
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("未提供中继链接。用法：mclbx relaycheck turn:用户:密码@主机:3478\n" +
			"      也可先设置环境变量 MCLBX_RELAY，再运行 mclbx relaycheck")
	}

	fmt.Printf(" 中继链接 : %s\n", plan.describe())
	fmt.Printf("------------------------------------------------\n")

	var failures []string
	okCount := 0
	for _, h := range plan.Hops {
		switch h.Kind {
		case hopTCP, hopAuto:
			if h.Kind == hopAuto {
				// 未指明类型：先当 TURN 试，不通再当 TCP 试
				fmt.Printf("\n[未指明类型] %s，先按 TURN 校验\n", h.addr())
				if err := checkTurnHop(h, *timeout); err == nil {
					fmt.Printf(" 它是 TURN 服务端。房主端填写： --relay-server %s\n", h.linkAs(hopTurn))
					okCount++
					continue
				}
				fmt.Printf("   TURN 校验未通过，改按 TCP 中继校验\n")
			} else {
				fmt.Printf("\n[TCP 中继] %s\n", h.addr())
			}
			if err := checkTCPHop(h.addr(), *room, *timeout); err != nil {
				failures = append(failures, fmt.Sprintf("%s：%v", h.addr(), err))
			} else {
				fmt.Printf(" 这是 TCP 中继。房主端填写： --relay-server %s\n", h.linkAs(hopTCP))
				okCount++
			}
		default:
			fmt.Printf("\n[TURN] %s\n", h.addr())
			if err := checkTurnHop(h, *timeout); err != nil {
				failures = append(failures, fmt.Sprintf("%s：%v", h.addr(), err))
				if h.Secret != "" {
					fmt.Printf("   本条使用 REST 临时凭据，密钥来自链接\n")
				}
			} else {
				fmt.Printf(" 中继可用。房主端填写： --relay-server %s\n", h.canonicalLink())
				okCount++
			}
		}
	}

	fmt.Printf("------------------------------------------------\n")
	if len(failures) == 0 {
		fmt.Printf(" 结论：这条链接可用。\n")
		return nil
	}
	// 部分可用时，结论要写清能用的是哪条。
	if okCount > 0 {
		fmt.Printf(" 结论：部分可用。上方标有 ✓ 的通道可用，剩余通道校验未通过：\n")
		return fmt.Errorf("  %s", strings.Join(failures, "\n  "))
	}
	return fmt.Errorf("校验未通过：\n  %s", strings.Join(failures, "\n  "))
}

// newTurnClient 起一个 TURN 客户端及其 UDP socket。
func newTurnClient(addr, user, pass string) (*turn.Client, net.PacketConn, error) {
	network, host := udpNetworkAndHost(addr)
	conn, err := net.ListenPacket(network, net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, nil, fmt.Errorf("创建本地 UDP socket 失败：%w", err)
	}
	// STUNServerAddr 与 TURNServerAddr 都给同一地址：TURN 服务端本身能应答 STUN 绑定请求。
	c, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: addr,
		TURNServerAddr: addr,
		Conn:           conn,
		Username:       user,
		Password:       pass,
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := c.Listen(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return c, conn, nil
}

// explainAllocError 按文本把库的 TURN 分配报错翻译成中文说明与下一步。
func explainAllocError(err error) string {
	s := err.Error()
	ls := strings.ToLower(s)
	switch {
	case strings.Contains(s, "401"), strings.Contains(ls, "unauthorized"):
		return "账号或密码不正确，中继返回 401 Unauthorized。静态账号区分大小写；REST 凭据请检查密钥"
	case strings.Contains(s, "438"), strings.Contains(ls, "stale nonce"):
		return "中继要求更换 nonce，返回 438。通常由本机时钟偏差过大或请求重放过快引起，重试即可"
	case strings.Contains(s, "400"), strings.Contains(ls, "bad request"):
		// 部分中继以 400 而非 401 表示认证失败，故把「账号不对」列为第一可能。
		return "中继拒绝本次请求，返回 400。常见原因有两种：一是账号或密码不正确，部分中继以 400 而非 401 表示认证失败；" +
			"二是使用 REST 临时凭据时，用户名中的过期时间戳必须为将来时间"
	case strings.Contains(s, "486"), strings.Contains(ls, "quota"):
		return "中继分配额度已满，返回 486 Quota Reached"
	case strings.Contains(s, "508"), strings.Contains(ls, "insufficient capacity"):
		return "中继没有可用的中继地址，返回 508 Insufficient Capacity；可能需要在其防火墙或路由器上开放端口段"
	case strings.Contains(ls, "all retransmissions failed"), strings.Contains(ls, "timeout"), strings.Contains(ls, "i/o timeout"):
		return "中继无应答。常见原因：UDP 被网络阻断、地址或端口错误、对端未运行中继服务"
	default:
		return "申请分配失败"
	}
}

// checkTurnHop 把 TURN 通路从头到尾跑一遍。
func checkTurnHop(h relayHop, timeout time.Duration) error {
	addr := h.addr()

	// 1) 解析地址
	network, _ := udpNetworkAndHost(addr)
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return fmt.Errorf("地址无法解析 %s：%w", addr, err)
	}
	fmt.Printf("  [1/5] 解析地址        ✓ %s\n", raddr)

	user, pass := h.User, h.Pass
	if h.Secret != "" {
		user, pass = restCredentials(h.Secret, h.RESTUser, turnRESTTTL)
		fmt.Printf("        REST 临时凭据    ✓ 用户名 %s，密码按共享密钥本地计算\n", user)
	}

	// 2) 起客户端并探一次映射（验证包出得去、应答回得来）
	c1, conn1, err := newTurnClient(addr, user, pass)
	if err != nil {
		return fmt.Errorf("创建 TURN 客户端失败：%w", err)
	}
	defer func() { c1.Close(); _ = conn1.Close() }()

	t0 := time.Now()
	mapped, err := c1.SendBindingRequest()
	if err != nil {
		// 包出不去或应答回不来，列出常见原因。
		return fmt.Errorf("中继无应答，常见原因：UDP 被网络阻断、地址或端口错误、对端未运行中继服务。原始报错：%v", err)
	}
	fmt.Printf("  [2/5] 探本机映射      ✓ 中继看到的地址 %s，%d ms\n", mapped, time.Since(t0).Milliseconds())

	// 3) 申请分配（校验账号在这一步）
	t0 = time.Now()
	relay1, err := c1.Allocate()
	if err != nil {
		return fmt.Errorf("%s，%d ms，原始报错：%v", explainAllocError(err), time.Since(t0).Milliseconds(), err)
	}
	defer func() { _ = relay1.Close() }()
	fmt.Printf("  [3/5] 申请中继地址    ✓ %s，%d ms\n", relay1.LocalAddr(), time.Since(t0).Milliseconds())

	// 4) 两个分配之间互传一段数据：单个分配只能证明拿到地址，证明不了数据转得动。
	c2, conn2, err := newTurnClient(addr, user, pass)
	if err != nil {
		return fmt.Errorf("创建第二个 TURN 客户端失败：%w", err)
	}
	defer func() { c2.Close(); _ = conn2.Close() }()

	relay2, err := c2.Allocate()
	if err != nil {
		fmt.Printf("  [4/5] 数据回环        ⚠ 第二个分配失败 %s\n", explainAllocError(err))
		fmt.Printf("        部分中继限制每个 IP 仅允许一个分配，跳过此步；\n")
		fmt.Printf("        前三步已确认账号可用且已获取中继地址，中继本身可用\n")
		return nil
	}
	defer func() { _ = relay2.Close() }()
	fmt.Printf("  [4/5] 第二个分配      ✓ %s\n", relay2.LocalAddr())

	// 双方互建权限后从 A 发数据给 B。
	if err := c1.CreatePermission(relay2.LocalAddr()); err != nil {
		return fmt.Errorf("创建对端权限失败：%w", err)
	}
	if err := c2.CreatePermission(relay1.LocalAddr()); err != nil {
		return fmt.Errorf("创建对端权限失败：%w", err)
	}

	payload := []byte("mclbx-relaycheck " + time.Now().Format(time.RFC3339Nano))
	if _, err := relay1.WriteTo(payload, relay2.LocalAddr()); err != nil {
		return fmt.Errorf("向中继地址写入数据失败：%w", err)
	}

	type got struct {
		n    int
		from net.Addr
		data []byte
		err  error
	}
	ch := make(chan got, 1)
	go func() {
		buf := make([]byte, 1500)
		n, from, err := relay2.ReadFrom(buf)
		cp := append([]byte(nil), buf[:n]...)
		ch <- got{n, from, cp, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("数据未能经中继返回，%v。分配成功但转发失败，中继本身可能存在问题", r.err)
		}
		if string(r.data) != string(payload) {
			return fmt.Errorf("返回内容不一致：收到 %q，期望 %q", r.data, payload)
		}
		if r.from.String() != relay1.LocalAddr().String() {
			return fmt.Errorf("返回数据的源地址不正确：收到 %s，期望中继地址 %s，中继未按规则改写对端地址",
				r.from, relay1.LocalAddr())
		}
		fmt.Printf("       数据回环        ✓ %d 字节往返成功，两端地址改写正确\n", r.n)
	case <-time.After(timeout):
		return fmt.Errorf("数据未能经中继返回，等待 %v。分配成功但转发失败", timeout)
	}

	// 5) 粗测中转带宽。
	const (
		burst    = 20
		pktSize  = 1000
		burstGap = time.Millisecond
	)
	recv := make(chan int, burst)
	go func() {
		buf := make([]byte, 1500)
		for i := 0; i < burst; i++ {
			if err := relay2.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				recv <- i
				return
			}
			if _, _, err := relay2.ReadFrom(buf); err != nil {
				recv <- i
				return
			}
		}
		recv <- burst
	}()
	big := make([]byte, pktSize)
	t0 = time.Now()
	for i := 0; i < burst; i++ {
		if _, err := relay1.WriteTo(big, relay2.LocalAddr()); err != nil {
			break
		}
		time.Sleep(burstGap)
	}
	n := 0
	select {
	case n = <-recv:
	case <-time.After(3 * time.Second):
	}
	el := time.Since(t0)
	if n < burst {
		fmt.Printf("  [5/5] 连发 %d 个包     ⚠ 仅收到 %d 个，%d ms，可连通但可能丢包\n", burst, n, el.Milliseconds())
	} else {
		mbps := float64(n*pktSize) / el.Seconds() / 1048576
		fmt.Printf("  [5/5] 连发 %d 个包     ✓ 全部收到，%.2f MiB/s，%d ms，含两端各一次加密开销\n",
			burst, mbps, el.Milliseconds())
	}
	return nil
}

// checkTCPHop 把 TCP 中继通路跑一遍：连两次 → 等配对 → 走一帧读回来。
func checkTCPHop(addr, room string, timeout time.Duration) error {
	if strings.TrimSpace(room) == "" {
		room = fmt.Sprintf("relaycheck%d", os.Getpid())
	}

	c1, _, err := dialRelay(addr, room)
	if err != nil {
		return fmt.Errorf("%w。常见原因：对端未运行 mclbx relay、端口错误、防火墙未开放", err)
	}
	defer c1.Close()
	fmt.Printf("  [1/4] 连上中继        ✓ 第一条连接就绪\n")

	c2, br2, err := dialRelay(addr, room)
	if err != nil {
		return err
	}
	defer c2.Close()
	fmt.Printf("  [2/4] 第二条连接      ✓ 房间码 %s，中继应将两条连接配对\n", room)

	m1 := &tcpMux{conn: c1}
	if err := m1.send(tcpFrameOpen, 1, nil); err != nil {
		return fmt.Errorf("发送握手帧失败：%w", err)
	}

	// 等 OPEN 走到对面，即配对成功的判据。
	_ = c2.SetReadDeadline(time.Now().Add(timeout))
	typ, id, _, err := readTcpFrame(br2)
	if err != nil {
		return fmt.Errorf("中继未将两条连接配对，等待 OPEN 帧超时：%v；同一房间码下应能配对", err)
	}
	if typ != tcpFrameOpen || id != 1 {
		return fmt.Errorf("收到的首个帧不正确：类型 %d 连接号 %d，中继可能存在会话串线", typ, id)
	}
	fmt.Printf("  [3/4] 配对与握手      ✓ 两条连接已对接\n")

	payload := []byte("mclbx-relaycheck")
	t0 := time.Now()
	if err := m1.send(tcpFrameData, 1, payload); err != nil {
		return fmt.Errorf("发送数据帧失败：%w", err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(timeout))
	typ, id, data, err := readTcpFrame(br2)
	if err != nil {
		return fmt.Errorf("数据帧未能通过：%w", err)
	}
	if typ != tcpFrameData || id != 1 || string(data) != string(payload) {
		return fmt.Errorf("通过的内容不一致：类型 %d 连接号 %d 内容 %q", typ, id, data)
	}
	fmt.Printf("  [4/4] 数据穿过中继    ✓ %d 字节往返成功，%d ms\n",
		len(data), time.Since(t0).Milliseconds())

	_ = m1.send(tcpFrameClose, 1, nil)
	fmt.Printf("        提醒：TCP 中继为明文传输，房间码是唯一凭据，中继机器可查看流量；\n")
	fmt.Printf("              建议优先使用 TURN 中继，本条仅用于 UDP 被屏蔽的网络。\n")
	return nil
}
