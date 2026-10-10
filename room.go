package main

// room.go 房主一条命令同时开启公网入口与软件入口，共用同一游戏端口。

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/pion/ice/v4"
)

// softwareGuestBurstWarn 为一分钟内新登记数的提醒阈值，仅记录日志、不拒绝。
const softwareGuestBurstWarn = 10

// normalizeRoomCode 统一校验房间码（dns 模式下还会成为域名的一段）。
func normalizeRoomCode(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		// 对去空白后的值判空；空房间码在门卫侧等价于不校验
		return randomRoomCode(6), nil
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return "", fmt.Errorf("房间码只能包含 a-z 0-9 和短横；实际为 %q", s)
		}
	}
	return s, nil
}

// prepareIceAgent 创建 ICE agent 并收集候选（房主/客机/中转侧共用）。
// onFail 非空时在进入 Failed 时通知一次。
func prepareIceAgent(withSTUN bool, label string, gather time.Duration, onFail func()) (*ice.Agent, []string, string, string, error) {
	a, err := newIceAgent(withSTUN, onFail)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("创建 ICE agent 失败：%w", err)
	}
	cands, err := gatherCandidates(a, gather, label)
	if err != nil {
		_ = a.Close()
		return nil, nil, "", "", err
	}
	if len(cands) == 0 {
		_ = a.Close()
		return nil, nil, "", "", fmt.Errorf("未收到任何候选：%s", stunTroubleHint())
	}
	// 有候选但无 srflx，说明 STUN 无应答或本机 UDP 出不去
	if withSTUN && !hasServerReflexive(cands) {
		logf("  %s", stunTroubleHint())
	}
	ufrag, pwd, err := a.GetLocalUserCredentials()
	if err != nil {
		_ = a.Close()
		return nil, nil, "", "", err
	}
	return a, cands, ufrag, pwd, nil
}

// dialable 把对外地址与端口拼成玩家填写形式（IPv6 加方括号）。
// 使用 gate.host（本机对外地址），而非 --signal 的监听地址。
func dialable(host string, port int) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func cmdRoom(args []string) error {
	fs := flag.NewFlagSet("room", flag.ContinueOnError)
	gamePort := fs.Int("port", 0, "游戏端口；留空自动探测")
	entryPort := fs.Int("entry", 0, "入口端口")
	to := fs.String("to", "", "转发目标；默认本机游戏端口")
	signalAddr := fs.String("signal", defaultListenAddr, "信令信箱监听地址")
	room := fs.String("room", "", "房间码；留空随机生成")
	mode := fs.String("mode", "auto", "公网入口地址形式：raw / dns / auto")
	suffix := fs.String("dns", "sslip.io", "dns 模式后缀")
	webAddr := fs.String("web", "127.0.0.1:8080", "本地管理页；off 关闭")
	allow := fs.String("allow", "", "公网入口允许的玩家名；逗号分隔")
	noGate := fs.Bool("no-gate", false, "关闭公网入口的房间码校验")
	useStun := fs.Bool("stun", true, "使用 STUN 服务收集公网候选")
	noSoftware := fs.Bool("no-software", false, "不开启软件入场")
	relayLink := fs.String("relay-server", "", "中继服务；直连失败时的兜底；turn:/turns:/tcp:/mclbx:// 或 主机:端口")
	relayUser := fs.String("relay-user", "", "中继账号；链接未提供账号时使用")
	relayPass := fs.String("relay-pass", "", "中继密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	applyRelayFlag(*relayLink, *relayUser, *relayPass)

	// 空串会被换成随机码，先记录用户是否显式指定（卡片据此提示房间码是否生效）。
	roomGiven := strings.TrimSpace(*room) != ""
	roomCode, err := normalizeRoomCode(*room)
	if err != nil {
		return err
	}

	// 路一：公网入口
	gate, err := startZeroInstallGate(gateSetup{
		gamePort:  *gamePort,
		entryPort: *entryPort,
		room:      roomCode,
		roomGiven: roomGiven,
		suffix:    *suffix,
		mode:      *mode,
		webAddr:   *webAddr,
		noGate:    *noGate,
		useSTUN:   *useStun,
		allow:     *allow,
		headline:  fmt.Sprintf("%s · 混合模式房间", version),
	})
	if err != nil {
		return err
	}
	defer gate.close()

	target := *to
	if target == "" {
		target = fmt.Sprintf("127.0.0.1:%d", gate.state.gamePort)
	}

	// 路二：软件入场（信令 + 多客户机并行）
	signalPort := 8090
	if p, err := portOfAddr(*signalAddr); err == nil {
		signalPort = p
	}
	softwareReady := !*noSoftware && gate.host != ""
	if softwareReady {
		mb := newMailbox()
		// 同步绑定后再交给 http.Serve，绑定失败须当场报错
		ln, lerr := listenSignal(*signalAddr)
		if lerr != nil {
			return lerr
		}
		srv := &http.Server{Handler: mb, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				logf("信令信箱结束：%v", err)
			}
		}()
		base := "http://127.0.0.1" + portOf(*signalAddr)
		if portOf(*signalAddr) == "" {
			base = fmt.Sprintf("http://127.0.0.1:%d", signalPort)
		}
		go serveSoftwareGuests(base, roomCode, target)
	}

	// 卡片：标题按实际状态分档输出
	entryOK := gate.state.getAddress() != ""
	fmt.Println()
	fmt.Printf("================================================\n")
	switch {
	case entryOK && softwareReady:
		fmt.Printf(" 结论：房间已就绪，两条入口同时开放\n")
	case entryOK:
		fmt.Printf(" 结论：公网入口已就绪；软件入场本次未开启\n")
	case softwareReady:
		fmt.Printf(" 结论：软件入场已就绪；公网入口本次不可用\n")
	default:
		fmt.Printf(" 结论：两条入口都没能开启，玩家现在连不进来\n")
		fmt.Printf(" 下一步：按下面「公网入口不可用」列的 1)~4) 逐条处理\n")
	}
	fmt.Printf(" 房间码 : %s（两端需一致）\n", roomCode)
	fmt.Printf("================================================\n")
	fmt.Printf(" 入口一：公网入口，玩家在游戏内「直接连接」填该地址\n")
	fmt.Printf("================================================\n")
	for _, l := range zeroInstallCardLines(gate) {
		fmt.Println(l)
	}
	fmt.Println()
	if softwareReady {
		fmt.Printf("================================================\n")
		fmt.Printf(" 入口二：软件入场，加密通道\n")
		fmt.Printf("================================================\n")
		fmt.Printf(" 将下面这一行发送给已安装本工具的玩家执行：\n\n")
		fmt.Printf("     mclbx join --host %s --room %s\n\n", dialable(gate.host, signalPort), roomCode)
		if line := guiMarker("JOIN", fmt.Sprintf("mclbx join --host %s --room %s", dialable(gate.host, signalPort), roomCode)); line != "" {
			fmt.Println(line)
		}
		fmt.Printf("  说明：每位玩家获得独立通道，互不影响\n")
		// IPv6 出口探测未通过时提前说明
		if gate.state.v6Tried && !gate.state.v6Reach {
			fmt.Printf("================================================\n")
			fmt.Printf(" 注意：本机 IPv6 出口探测无应答，上面这个地址可能连不上\n")
			fmt.Printf(" 先用纯 IPv6 网站确认本机能出去；不行就改用中继转发（见说明书第 2 节）\n")
		}
		fmt.Printf("================================================\n")
	} else {
		fmt.Printf("================================================\n")
		fmt.Printf(" 入口二：本次未开启\n")
		fmt.Printf("================================================\n")
		if *noSoftware {
			fmt.Printf(" 已由 --no-software 关闭\n")
		} else {
			fmt.Printf(" 原因：本机无公网地址，玩家无法访问信令信箱，无法建立直连\n")
			fmt.Printf(" 处理：需要一台可被外部连接的常驻出口\n")
		}
		fmt.Printf("================================================\n")
	}
	if !strings.EqualFold(*webAddr, "off") {
		fmt.Printf(" 管理页 : http://%s\n", *webAddr)
	}
	// 入站端口一览：两条入口使用不同端口，防火墙与端口映射都要覆盖。
	var inPorts []string
	if gate.state.entryPort > 0 {
		inPorts = append(inPorts, fmt.Sprint(gate.state.entryPort))
	}
	if softwareReady {
		inPorts = append(inPorts, fmt.Sprint(signalPort))
	}
	if len(inPorts) > 0 {
		fmt.Printf(" 入站端口: %s（防火墙与端口映射都要覆盖，否则会出现「没装工具的能进、装了工具的进不来」）\n", strings.Join(inPorts, " 与 "))
		if runtime.GOOS == "windows" {
			fmt.Printf("          一键放行：mclbx firewall --port %s\n", strings.Join(inPorts, ","))
		}
	}
	fmt.Printf(" 按 Ctrl+C 结束\n")

	waitForInterrupt(gate, "房间已关闭")
	return nil
}

// serveSoftwareGuests 轮询信令登记表接纳客机，互不影响。
// 先用 /rev 判断是否有变化，取不到 /rev 时退回逐个列键取信。
func serveSoftwareGuests(base, room, to string) {
	logf(".. [软件入场] 已就绪，等待玩家")

	// 房主身份整场只生成一次，所有客机与代次共用，以保证安全码稳定。
	hostID, err := newIdentity()
	if err != nil {
		logf("[软件入场] 生成身份失败：%v", err)
		return
	}
	hostFP, err := parseFingerprint(hostID.fingerprint())
	if err != nil {
		return
	}

	reg := newGuestRegistry()
	lastRev := ^uint64(0) // 第一轮必定会去列表
	sweepTick := 0
	retrySoon := false // 上一轮有客机被推迟

	// 短时间大量新登记的记账；只提示不拦截
	burst, burstStart := 0, time.Time{}

	for {
		rev, ok := mailboxRev(base, 3*time.Second)
		// 计数变化或被推迟过时重新列表
		if !ok || rev != lastRev || retrySoon {
			lastRev = rev
			retrySoon = false
			claimed := 0
			if keys, err := listBlobs(base, 3*time.Second); err == nil {
				for _, k := range keys {
					if !strings.HasPrefix(k, "guest-") {
						continue
					}
					b, err := fetchBlob(base, k, 3*time.Second)
					if err != nil {
						continue
					}
					switch reg.claim(k, b.Gen) {
					case claimAccepted:
						claimed++
						go func(key string, blob candBlob) {
							defer reg.release(key)
							// 收尾顺序：先摘名单再让位；迟到的 markConnected 会被 gone 挡住
							defer func() { reg.markGone(key); emitRoster(reg) }()
							acceptOneGuestBlob(base, key, blob, room, to, hostID, hostFP, reg)
						}(k, b)
					case claimBusy:
						retrySoon = true
					case claimStale:
						// 同一份候选，忽略
					}
				}
			}
			if claimed > 0 {
				now := time.Now()
				if now.Sub(burstStart) > time.Minute {
					burst, burstStart = 0, now
				}
				burst += claimed
				if burst >= softwareGuestBurstWarn {
					logf("提醒：一分钟内有 %d 位新玩家在登记（朋友同时进入也会如此）。"+
						"若非受邀玩家，说明房间地址已外传：结束房间并更换房间码重开"+
						"（正在游戏的人会断开一次）；限定名字可用 --allow", burst)
					burst, burstStart = 0, now
				}
			}
		}
		if sweepTick++; sweepTick >= 60 {
			sweepTick = 0
			reg.sweep(10 * time.Minute)
		}
		time.Sleep(time.Second)
	}
}

// acceptOneGuestBlob 接纳一个客机的一代通道；返回即该代结束。
// 不做重试：客机断开后带更高代次重新登记。
func acceptOneGuestBlob(base, key string, blob candBlob, room, to string, id *identity, selfFP [32]byte, reg *guestRegistry) {
	short := strings.TrimPrefix(key, "guest-")
	if blob.Room != room {
		logf("[软件入场] 拒绝 %s：房间码不一致；对端房间码 %q", short, blob.Room)
		return
	}
	if blob.Fingerprint == "" {
		logf("[软件入场] 拒绝 %s：没有公布证书指纹", short)
		return
	}
	peerFP, err := parseFingerprint(blob.Fingerprint)
	if err != nil {
		logf("[软件入场] 拒绝 %s：指纹无法解析：%v", short, err)
		return
	}

	// 本代的取消信号：ICE Failed 时触发并关闭 conn，避免 AcceptStream 长期阻塞。
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	guiDetailf(".. [软件入场] %s 已登记（第 %d 代），开始收集候选", short, blob.Gen)
	a, cands, ufrag, pwd, err := prepareIceAgent(true, "room-"+short, 8*time.Second, cancelAttempt)
	if err != nil {
		logf("[软件入场] %s 收集候选失败：%v", short, err)
		return
	}
	defer a.Close()

	// 回送的候选须带代次，供客机确认是对本次尝试的回应
	if err := postBlob(base, "host-"+short, candBlob{
		Ufrag: ufrag, Pwd: pwd, Cands: cands,
		Fingerprint: id.fingerprint(), Room: room, Gen: blob.Gen,
	}, 10*time.Second); err != nil {
		logf("[软件入场] %s 回送候选失败：%v", short, err)
		return
	}
	for _, s := range blob.Cands {
		if c, err := ice.UnmarshalCandidate(s); err == nil {
			_ = a.AddRemoteCandidate(c)
		}
	}

	ctx, cancel := context.WithTimeout(attemptCtx, 60*time.Second)
	defer cancel()
	conn, err := a.Dial(ctx, blob.Ufrag, blob.Pwd)
	if err != nil {
		logf("[软件入场] %s 直连失败：%v", short, err)
		return
	}
	defer conn.Close()
	logf("结论：[软件入场] %s 的通道已建立", short)
	dumpPair(a)

	// 名单须等身份校验通过后才记录
	mode := connMode(a)
	verified := func() {
		reg.markConnected(key, safetyCode(id.fp, peerFP), mode)
		emitRoster(reg)
	}

	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-attemptCtx.Done():
			_ = conn.Close()
		case <-watchDone:
		}
	}()

	if err := serveSCTPHost(conn, iceRemoteAddr(a), to, id, selfFP, peerFP, verified); err != nil {
		logf("✗ [软件入场] %s 断开：%v", short, err)
	}
}

// connMode 依据选中候选对是否为 relay 返回「直连」或「中继」。
func connMode(a *ice.Agent) string {
	p, err := a.GetSelectedCandidatePair()
	if err != nil || p == nil {
		return "直连"
	}
	if p.Local.Type() == ice.CandidateTypeRelay || p.Remote.Type() == ice.CandidateTypeRelay {
		return "中继"
	}
	return "直连"
}

func cmdJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	hostSignal := fs.String("host", "", "房主侧信令信箱地址，例如 [2408:...]:8090")
	room := fs.String("room", "", "房间码")
	local := fs.String("local", "127.0.0.1:25565", "本机监听地址")
	useStun := fs.Bool("stun", true, "使用 STUN 服务收集公网候选")
	wait := fs.Duration("wait", 60*time.Second, "等待房主候选的最长时间")
	relay := fs.Bool("relay", false, "同时开放公网入口，为玩家中继转发")
	relayEntry := fs.Int("relay-entry", 25580, "中继转发入口端口")
	relayAllow := fs.String("relay-allow", "", "中继转发入口允许的玩家名；逗号分隔")
	relayLink := fs.String("relay-server", "", "中继服务；直连失败时的兜底；turn:/turns:/tcp:/mclbx:// 或 主机:端口")
	relayUser := fs.String("relay-user", "", "中继账号；链接未提供账号时使用")
	relayPass := fs.String("relay-pass", "", "中继密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	applyRelayFlag(*relayLink, *relayUser, *relayPass)

	// mclbx:// 链接中若含信令地址与房间码，取出作为缺省值
	if plan, perr := activeRelayPlan(); perr == nil && plan != nil {
		if strings.TrimSpace(*hostSignal) == "" && plan.SignalURL != "" {
			*hostSignal = strings.TrimPrefix(plan.SignalURL, "http://")
			logf("从链接读取信令信箱地址：%s", *hostSignal)
		}
		if strings.TrimSpace(*room) == "" && plan.Room != "" {
			*room = plan.Room
			logf("从链接读取房间码：%s", *room)
		}
	}

	if *hostSignal == "" {
		return fmt.Errorf("必须用 --host 指定房主侧信令信箱地址")
	}
	// 玩家侧房间码不能留空：随机生成会与房主不一致。
	if strings.TrimSpace(*room) == "" {
		return fmt.Errorf("没有房间码，无法加入\n" +
			"  原因：本机没读到房间码；软件入口按房间码区分房间，空着必然连不上房主\n" +
			"  处理：把房主发来的整段邀请原样粘贴到「房主地址」框里（其中含「房间码：xxxxxx」或 --room xxxxxx）\n" +
			"        也可以手动补上房主屏幕上的那个 6 位房间码")
	}
	roomCode, err := normalizeRoomCode(*room)
	if err != nil {
		return err
	}

	id, err := newIdentity()
	if err != nil {
		return fmt.Errorf("生成身份失败：%w", err)
	}
	selfFP, err := parseFingerprint(id.fingerprint())
	if err != nil {
		return err
	}

	// 先绑定本地端口，避免隧道建好后才发现端口被占
	ln, err := net.Listen("tcp", *local)
	if err != nil {
		return fmt.Errorf("本机监听 %s 失败：%w\n  原因：端口可能已被占用\n  处理：更换 --local 端口", *local, err)
	}
	defer ln.Close()

	base := "http://" + *hostSignal
	myKey := randomRoomCode(6) // 本场在信箱中的登记名；重连保持不变，房主据此认人

	fmt.Printf("==== mclbx join · 软件入场 ====\n")
	fmt.Printf("信令信箱 : %s\n", base)
	fmt.Printf("房间码   : %s\n", roomCode)

	var ref tunnelRef

	// 中继转发入口的上游同样走 ref，避免持有已失效的隧道
	if *relay {
		go serveRelayEntry(&ref, roomCode, *relayEntry, *relayAllow)
	}

	// 监听口整场只绑定一次；隧道在 ref 后更换而端口不变，重连前后地址始终有效。
	go func() {
		if err := serveGuestListenerOn(ln, &ref, nil); err != nil {
			logf("本地监听结束：%v", err)
		}
	}()

	sess := &guestSession{
		base: base, key: myKey, room: roomCode,
		id: id, selfFP: selfFP, useSTUN: *useStun, wait: *wait,
	}
	return runGuest(sess, &ref, func(gen int, first bool) {
		fmt.Printf("================================================\n")
		if first {
			fmt.Printf(" 结论：隧道已就绪，游戏内连接 %s\n", *local)
		} else {
			fmt.Printf(" 结论：已自动重连（第 %d 次尝试），游戏内仍连接 %s\n", gen, *local)
		}
		fmt.Printf("================================================\n")
	})
}

// serveRelayEntry 为未安装工具的玩家开启入口，数据经本机隧道转发到房主。
// 上游经 tunnelRef 动态取当前隧道，重连后无需重开。
func serveRelayEntry(ref *tunnelRef, room string, entryPort int, allow string) {
	addr, err := relayListenAddr(entryPort)
	if err != nil {
		logf("中继转发入口绑定失败：%v", err)
		return
	}
	var allowList []string
	if allow != "" {
		for _, s := range strings.Split(allow, ",") {
			if s = strings.TrimSpace(s); s != "" {
				allowList = append(allowList, s)
			}
		}
	}
	g := &gateState{room: room, entryPort: addr.port, gamePort: 0}
	g.openUpstream = ref.open // 上游不是本机端口，而是隧道里的新流
	logf("[中继转发] 入口端口已监听：%s", addr.listen)
	if addr.share == "" {
		fmt.Printf(" ! 未找到公网 IPv6，该入口端口仅本机与局域网可达\n")
	} else {
		fmt.Printf("================================================\n")
		fmt.Printf(" 中继转发入口：玩家在游戏内填入该地址即可连接\n")
		fmt.Printf("================================================\n")
		fmt.Printf(" 将下面这一行发给玩家：\n")
		fmt.Printf("   「多人游戏 → 直接连接」粘贴：\n\n")
		fmt.Printf("     %s\n\n", addr.share)
		// 转发同时占用本机上行与下行，按流量计费的用户需注意
		fmt.Printf("  说明：流量经隧道转发至房主：朋友的数据先到本机，再由本机发出，\n")
		fmt.Printf("        因此同时占用本机的上行与下行；按流量计费时请注意。\n")
		fmt.Printf("        本端退出后该入口关闭。\n")
		if len(allowList) == 0 {
			fmt.Printf("        该入口目前不校验玩家名：拿到上面这一行地址的人都能进入，\n")
			fmt.Printf("        限定名单可加 --relay-allow Steve,Alex\n")
		} else {
			fmt.Printf("        仅允许这些玩家名进入：%s\n", strings.Join(allowList, "、"))
			if note := allowListNote(allowList); note != "" {
				fmt.Printf("        注意：%s\n", note)
			}
		}
		fmt.Printf("================================================\n")
	}
	ln, err := net.Listen("tcp", addr.listen)
	if err != nil {
		logf("中继转发入口监听失败：%v", err)
		return
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go handleConn(c, g, false, allowList)
	}
}

// relayAddr 描述中转入口的监听地址与对外分享地址。
type relayAddr struct {
	listen string // net.Listen 用
	share  string // 发给朋友的
	port   int
}

func relayListenAddr(port int) (relayAddr, error) {
	out := relayAddr{listen: fmt.Sprintf(":%d", port), port: port}
	for _, a := range globalIPv6() {
		if a.Temporary {
			continue
		}
		out.share = fmt.Sprintf("[%s]:%d", a.IP, port)
		break
	}
	if out.share == "" && len(globalIPv6()) > 0 {
		out.share = fmt.Sprintf("[%s]:%d", globalIPv6()[0].IP, port)
	}
	return out, nil
}

// portOfAddr 从 host:port 中解析端口号。
func portOfAddr(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range p {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("端口不是数字：%s", p)
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return 0, fmt.Errorf("端口是 0")
	}
	return n, nil
}
