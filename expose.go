package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// 门卫 + 转发器的共享状态（也供本地管理页读取）
type gateState struct {
	mu        sync.Mutex
	room      string
	roomGiven bool // 房间码是用户自己给的，而不是程序随机生成的
	entryPort int
	gamePort  int
	address   string
	v6        []v6addr
	natInfo   string
	upnpInfo  string
	logs      []string
	accepted  atomic.Uint64
	rejected  atomic.Uint64
	started   time.Time

	// 上游：默认本机游戏端口，中转使用时换成隧道里的新流。
	openUpstream func() (io.ReadWriteCloser, error)

	// IPv4 直连：路由器开的洞（无则 nil）
	v4map    *natMapping
	v4info   string // 展示给用户的一句话
	stunV4   string // STUN 看到的公网 IPv4，映射无外网地址时兜底
	entryExt int    // 对外真正生效的端口（映射可能换端口）

	// IPv6 出口：网卡上有公网 IPv6 不等于能出去；v6Tried 表示探过，未通时给出提醒。
	v6Tried bool
	v6Reach bool
	v6Out   string

	// addrKind 记录地址按哪条路算出（v6raw/v6dns/v4map/v4direct）；dnsSuffix、shareV6 用于地址变化后重算。
	addrKind  string
	dnsSuffix string
	shareV6   string
}

func (g *gateState) setV4Map(m *natMapping, info string, extPort int) {
	g.mu.Lock()
	g.v4map, g.v4info, g.entryExt = m, info, extPort
	g.mu.Unlock()
}

func (g *gateState) getV4Map() *natMapping {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.v4map
}

// keepMappingAlive 在租期到达前续约映射，端口变化时更新地址卡。
func (g *gateState) keepMappingAlive() {
	for {
		m := g.getV4Map()
		if m == nil {
			return
		}
		wait := m.Lifetime / 2
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		if wait > 10*time.Minute {
			wait = 10 * time.Minute
		}
		time.Sleep(wait)

		old := g.getV4Map()
		if old == nil {
			return
		}
		nm, tried := openNatMapping(old.Proto, old.IntPort, old.ExtPort, natMapLifetime)
		if nm == nil {
			g.addLog("自动端口映射续约失败：%v", tried)
			g.setV4Map(nil, "续约失败，路由器可能已撤销映射", g.entryExt)
			return
		}
		if nm.ExtIP == "" {
			nm.ExtIP = old.ExtIP
		}
		g.setV4Map(nm, g.v4infoOf(nm), nm.ExtPort)
		if nm.ExtPort != old.ExtPort {
			g.addLog("自动端口映射端口变化：%d → %d，地址卡已更新", old.ExtPort, nm.ExtPort)
			newAddr := g.withAddr(func(a string) string { return replacePort(a, nm.ExtPort) })
			// 界面仅在收到 ##ADDR## 时刷新地址卡，故此处重新播一次标记。
			g.emitAddr(newAddr)
		}
		old.Release()
	}
}

// getAddress 加锁读取当前对外地址。
func (g *gateState) getAddress() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.address
}

// withAddr 按函数改写当前地址（续约导致端口变化时用），返回改写后的值
func (g *gateState) withAddr(f func(string) string) string {
	g.mu.Lock()
	g.address = f(g.address)
	out := g.address
	g.mu.Unlock()
	return out
}

// emitAddr 重新播一次地址标记，使界面刷新地址卡与邀请文本。
func (g *gateState) emitAddr(value string) {
	if line := guiMarker("ADDR", value); line != "" {
		fmt.Println(line)
	}
}

// ipv6WatchInterval 是公网 IPv6 快照间隔（60 秒；快照只读网卡不发包）。
const ipv6WatchInterval = 60 * time.Second

// pickShareIPv6 从公网 IPv6 中优先选非临时地址（临时地址会轮换）。
func pickShareIPv6(list []v6addr) string {
	if len(list) == 0 {
		return ""
	}
	for _, a := range list {
		if !a.Temporary {
			return a.IP.String()
		}
	}
	return list[0].IP.String()
}

// addrFollowsNIC 判断地址是否跟着网卡 IPv6 走（仅 v6raw / v6dns 需要监控）。
func (g *gateState) addrFollowsNIC() bool {
	return g.addrKind == "v6raw" || g.addrKind == "v6dns"
}

// shareAddrFor 按当前地址形式用给定主机名/地址重算对外地址（仅 IPv6 两条路有效）。
func (g *gateState) shareAddrFor(host string) string {
	switch g.addrKind {
	case "v6raw":
		if g.entryPort == 25565 {
			return host
		}
		return fmt.Sprintf("[%s]:%d", host, g.entryPort)
	case "v6dns":
		name := fmt.Sprintf("%s.%s.%s", g.room, ipv6DNSLabel(net.ParseIP(host)), g.dnsSuffix)
		if g.entryPort != 25565 {
			return fmt.Sprintf("%s:%d", name, g.entryPort)
		}
		return name
	}
	return ""
}

// watchIPv6Changes 低频比较公网 IPv6，地址变化时刷新地址卡并重播 ##ADDR##。
func (g *gateState) watchIPv6Changes() {
	for {
		time.Sleep(ipv6WatchInterval)

		cur := globalIPv6()
		pick := pickShareIPv6(cur)
		if pick == "" {
			continue // 网卡抖动时读不到地址，不清空地址卡
		}

		g.mu.Lock()
		if pick == g.shareV6 {
			g.mu.Unlock()
			continue
		}
		old := g.shareV6
		g.shareV6 = pick
		g.v6 = cur
		newAddr := g.shareAddrFor(pick)
		g.mu.Unlock()
		if newAddr == "" {
			continue
		}
		g.addLog("公网 IPv6 变化：%s → %s，地址卡已更新（旧邀请请重新复制）", old, pick)
		g.emitAddr(newAddr)
	}
}

// replacePort 把 "host:port" / "[v6]:port" 里的端口换掉
func replacePort(addr string, port int) string {
	if addr == "" {
		return addr
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		if strings.Contains(h, ":") {
			return fmt.Sprintf("[%s]:%d", h, port)
		}
		return fmt.Sprintf("%s:%d", h, port)
	}
	if port == 25565 {
		return addr
	}
	// 原为裸地址（默认端口），端口变化后需补上
	if strings.Contains(addr, ":") {
		return fmt.Sprintf("[%s]:%d", addr, port)
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

func (g *gateState) v4infoOf(m *natMapping) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("%s 自动端口映射成功：%s 出口 IP %s:%d", m.Method, m.Proto, m.ExtIP, m.ExtPort)
}

func (g *gateState) addLog(format string, a ...any) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
	g.mu.Lock()
	g.logs = append(g.logs, line)
	if len(g.logs) > 300 {
		g.logs = g.logs[len(g.logs)-300:]
	}
	g.mu.Unlock()
	fmt.Println(line)
}

// roomCodeAccepted 校验玩家输入的名字里是否含房间码（SRV 目标名先归一化再比）。
func roomCodeAccepted(addr, room string) bool {
	a := strings.Trim(strings.TrimSpace(addr), ".")
	return strings.Contains(strings.ToLower(a), strings.ToLower(room))
}

// handshakeReadTimeout 是读首个握手包的时限，防止 slowloris 式无成本占用。
const handshakeReadTimeout = 10 * time.Second

// handleConn 读取握手包并校验玩家名，通过后将已读字节补发给真实游戏端口。
func handleConn(c net.Conn, g *gateState, gate bool, allow []string) {
	defer c.Close()
	peer := c.RemoteAddr().String()
	// 读握手阶段先设时限，之后放宽
	_ = c.SetReadDeadline(time.Now().Add(handshakeReadTimeout))
	br := bufio.NewReader(c)

	first, err := readFrame(br, 1<<20)
	if err != nil {
		g.rejected.Add(1)
		g.addLog("[入口校验] 拒绝 %s：无法读取合法握手包，可能为扫描器", peer)
		return
	}
	hs, err := parseHandshake(first)
	if err != nil {
		g.rejected.Add(1)
		g.addLog("[入口校验] 拒绝 %s：首包不是我的世界 Java 版握手包", peer)
		return
	}

	// 登录连接再多读一包，取玩家名
	var second []byte
	user := ""
	if hs.NextState == 2 {
		if b2, err := readFrame(br, 1<<20); err == nil {
			second = b2
			if n, err := parseLoginStart(b2); err == nil {
				user = n
			}
		}
	}
	// 进门之前的读取共用前面时限，之后放开。
	_ = c.SetReadDeadline(time.Time{})
	kind := "服务器列表查询"
	if hs.NextState != 1 {
		kind = "登录"
	}

	// 校验一：玩家输入的名字里必须含房间码；SRV 目标名结尾多一个点，比对前先去掉。
	addr := strings.Trim(strings.TrimSpace(hs.Address), ".")
	if gate && !roomCodeAccepted(addr, g.room) {
		g.rejected.Add(1)
		g.addLog("[入口校验] 拒绝 %s：%s 输入为 %q，不含房间码 %q", peer, kind, hs.Address, g.room)
		return
	}
	// 校验二：可选的玩家名白名单
	if len(allow) > 0 && user != "" {
		ok := false
		for _, n := range allow {
			if strings.EqualFold(n, user) {
				ok = true
				break
			}
		}
		if !ok {
			g.rejected.Add(1)
			g.addLog("[入口校验] 拒绝 %s：玩家名 %q 不在白名单内", peer, user)
			return
		}
	}

	who := user
	if who == "" {
		who = "未知玩家"
	}
	g.accepted.Add(1)
	g.addLog("[入口校验] 接受 %s：%s，输入名称 %q，协议版本 %d，玩家名 %s",
		peer, kind, hs.Address, hs.Protocol, who)

	open := g.openUpstream
	if open == nil {
		open = func() (io.ReadWriteCloser, error) {
			return net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", g.gamePort), 5*time.Second)
		}
	}
	up, err := open()
	if err != nil {
		g.addLog("[入口校验] 打开上游 %d 失败：%v", g.gamePort, err)
		return
	}
	defer up.Close()

	if err := writeFrame(up, first); err != nil {
		return
	}
	if second != nil {
		if err := writeFrame(up, second); err != nil {
			return
		}
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); done <- struct{}{} }() // 玩家 → 上游（br 里可能还有已缓冲的数据）
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()  // 上游 → 玩家
	<-done
	g.addLog("[入口校验] %s 的连接结束", peer)
}

// 入口端口选择
func pickEntry(want, gamePort int) (net.Listener, int, error) {
	try := func(p int) (net.Listener, int, error) {
		if p == 0 || p == gamePort {
			return nil, 0, fmt.Errorf("跳过 %d", p)
		}
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err != nil {
			return nil, 0, err
		}
		return ln, p, nil
	}
	if want != 0 {
		return try(want)
	}
	var lastErr error
	for _, p := range []int{25565, 25566, 25567, 25570, 25580, 25600} {
		ln, port, err := try(p)
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func probeGamePort(given int) (int, error) {
	if given != 0 {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", given), 1500*time.Millisecond)
		if err != nil {
			return 0, fmt.Errorf("端口 %d 无法连接；请先在游戏内开启「对局域网开放」", given)
		}
		c.Close()
		return given, nil
	}
	for _, p := range []int{25565, 25566, 25567} {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 700*time.Millisecond)
		if err == nil {
			c.Close()
			logf(".. 自动识别到游戏端口 %d", p)
			return p, nil
		}
	}
	return 0, fmt.Errorf("未找到游戏端口；请使用 --port 指定")
}

// 主流程
func cmdExpose(args []string) error {
	fs := flag.NewFlagSet("expose", flag.ContinueOnError)
	gamePort := fs.Int("port", 0, "游戏端口")
	entryPort := fs.Int("entry", 0, "入口端口")
	room := fs.String("room", "", "房间码；仅 dns 模式作为口令")
	suffix := fs.String("dns", "sslip.io", "公共通配 DNS 后缀；仅 dns 模式使用")
	mode := fs.String("mode", "auto", "公网入口地址形式：raw / dns / auto")
	webAddr := fs.String("web", "127.0.0.1:8080", "本地管理页地址；off 关闭")
	noGate := fs.Bool("no-gate", false, "关闭房间码校验")
	noMap := fs.Bool("no-map", false, "不启用自动端口映射")
	useStun := fs.Bool("stun", true, "使用 STUN 服务探测 IPv4 出口 IP")
	allow := fs.String("allow", "", "允许的玩家名；逗号分隔")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 房间码校验统一走 normalizeRoomCode。空串会被换成随机码，所以先记下用户到底给没给。
	roomGiven := strings.TrimSpace(*room) != ""
	rc, rcErr := normalizeRoomCode(*room)
	if rcErr != nil {
		return rcErr
	}
	*room = rc

	ready, err := startZeroInstallGate(gateSetup{
		gamePort:  *gamePort,
		entryPort: *entryPort,
		room:      *room,
		roomGiven: roomGiven,
		suffix:    *suffix,
		mode:      *mode,
		webAddr:   *webAddr,
		noGate:    *noGate,
		noMap:     *noMap,
		useSTUN:   *useStun,
		allow:     *allow,
		headline:  fmt.Sprintf("%s · 公网入口", version),
	})
	if err != nil {
		return err
	}
	printZeroInstallCard(ready)
	waitForInterrupt(ready, "入口已关闭")
	return nil
}

// 公网入口：把「诊断 + 算地址 + 起门卫 + 起管理页」抽成一个共用函数。
type gateSetup struct {
	gamePort  int
	entryPort int
	room      string
	roomGiven bool // 房间码是用户自己给的（而不是随机生成的）
	suffix    string
	mode      string
	webAddr   string
	noGate    bool
	noMap     bool // 不用 UPnP/NAT-PMP/PCP 主动开洞
	useSTUN   bool
	allow     string
	headline  string
}

type gateReady struct {
	state     *gateState
	listener  net.Listener
	entryPort int
	address   string // 玩家要输的那一行
	host      string // 玩家该连的主机名/地址（不带端口），软件入场也用这个
	gateRoom  bool   // 生效的门槛：口令是房间码（true）还是地址本身（false）
	noGate    bool   // 用户是否显式用 --no-gate 关掉了校验
	webAddr   string
}

func (r *gateReady) close() {
	if r.listener != nil {
		_ = r.listener.Close()
	}
	// 退出时撤销路由器上的映射。
	if r.state != nil {
		if m := r.state.getV4Map(); m != nil {
			m.Release()
			r.state.setV4Map(nil, "", 0)
		}
	}
}

// addrFormFor 决定拿到 IPv6 之后实际使用的地址形式。纯函数，便于把"门槛与形式的关系"钉住。
//
// 它只回答"用哪种形式"，不回答"玩家连不连得上" —— 域名形式能否用，还要看解析结果，
// 那一步在 startZeroInstallGate 里按 auto / 显式 dns 分别处理。
func addrFormFor(mode string, wantGate bool) string {
	switch mode {
	case "raw":
		return "raw" // 用户点名要字面量，尊重它
	case "dns":
		return "dns" // 用户点名要域名，哪怕他不会要门槛
	}
	if !wantGate {
		return "raw" // auto 且没有门槛诉求：没必要为门槛去引第三方 DNS
	}
	return "dns" // auto 且要门槛：房间码得有地方写，只有域名形式承载得了
}

func startZeroInstallGate(o gateSetup) (*gateReady, error) {
	gp, err := probeGamePort(o.gamePort)
	if err != nil {
		return nil, err
	}

	g := &gateState{room: o.room, roomGiven: o.roomGiven, gamePort: gp, started: time.Now()}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s\n", o.headline)
	fmt.Printf("================================================\n")
	fmt.Printf(" 游戏端口         : %d，仅本机可达\n", gp)

	// 路况诊断
	g.v6 = globalIPv6()
	if len(g.v6) == 0 {
		fmt.Println(" 公网 IPv6        : 无")
		g.natInfo = "无公网 IPv6"
	} else {
		fmt.Printf(" 公网 IPv6        : %d 个\n", len(g.v6))
		for _, a := range g.v6 {
			tag := ""
			if a.Temporary {
				tag = " 临时"
			}
			fmt.Printf("   %s%s  [%s]\n", a.IP, tag, a.IfName)
		}
	}

	if o.useSTUN {
		// 判定与展示必须来自同一轮、同一 socket 的探测
		v4 := stunProbeShared(defaultStunServers(), 1500*time.Millisecond)
		g.natInfo = natVerdict(v4)
		if len(v4) > 0 {
			g.stunV4 = v4[0].IP.String()
		}
		// IPv6 出口需单独用 IPv6 socket 探测（stunProbeV6），否则结果常为空。
		g.v6Tried = true
		if r, ok := stunProbeV6(defaultStunServers(), 1500*time.Millisecond); ok {
			g.v6Reach, g.v6Out = true, r.IP.String()
			g.natInfo += fmt.Sprintf("；IPv6 出口 %s，无 NAT", r.IP)
		} else if len(g.v6) > 0 {
			g.natInfo += "；IPv6 出口无应答（地址在，但可能出不去）"
		}
		fmt.Printf(" 出站 NAT         : %s\n", g.natInfo)
	}

	if loc, err := upnpDiscover(1500 * time.Millisecond); err != nil {
		g.upnpInfo = "无响应"
	} else if ctrl, ext, err2 := upnpExternalIP(loc); err2 == nil {
		g.upnpInfo = fmt.Sprintf("可用，WAN 地址 %s", ext)
		_ = ctrl
	} else {
		g.upnpInfo = "可用，WAN 地址不可读"
	}
	fmt.Printf(" 端口映射         : %s\n", g.upnpInfo)
	fmt.Println("------------------------------------------------")

	// 起门卫
	ln, ep, err := pickEntry(o.entryPort, gp)
	if err != nil {
		return nil, fmt.Errorf("入口端口绑定失败：%w", err)
	}
	g.entryPort = ep

	// 算出给玩家的那一行
	// raw：直接给 IPv6 字面量，零第三方依赖；dns：编入公共通配域名，使用房间码作口令。
	modeVal := strings.ToLower(strings.TrimSpace(o.mode))
	if modeVal != "auto" && modeVal != "raw" && modeVal != "dns" {
		_ = ln.Close()
		return nil, fmt.Errorf("--mode 只能是 auto / raw / dns；实际为 %q", o.mode)
	}

	// 门卫策略：dns 模式靠名字含房间码；raw 模式靠地址本身（IPv6 无法被全网扫描）。
	// wantGate 是“这一轮要不要门槛”：默认要，只有 --no-gate 才不要。
	wantGate := !o.noGate
	gateRoom := true
	has6 := len(g.v6) > 0
	shareHost := ""

	// 字面量形式单独抽出来：它在两处用得上 —— 用户明确要 raw，以及 auto 下域名走不通时的退回。
	applyRaw := func() {
		// 临时地址会变，不用于分享
		shareHost = pickShareIPv6(g.v6)
		g.shareV6 = shareHost
		g.addrKind = "v6raw"
		if ep == 25565 {
			g.address = shareHost
		} else {
			g.address = fmt.Sprintf("[%s]:%d", shareHost, ep)
		}
		gateRoom = false
		fmt.Printf(" 地址形式         : IPv6 字面量，无第三方依赖\n")
	}

	// 地址形式先定下来再动手。这段把"要不要门槛"与"用哪种形式"的关系写在一处：
	//   raw —— 用户点名要字面量；或 auto 且没有门槛诉求（不必为此引第三方 DNS）
	//   dns —— 其余情况：auto 且要门槛（房间码得有地方写），或用户点名要域名
	form := addrFormFor(modeVal, wantGate)
	switch {
	case has6 && form == "raw":
		applyRaw()

	case has6:
		// auto 且要门槛时也落在这里：房间码得有地方写，而只有域名形式承载得了它。
		// 这是“门槛设为默认”的落点 —— 宁可多一项第三方依赖，也不给一个看着有门槛、其实没有的入口。
		pick := pickShareIPv6(g.v6)
		g.shareV6 = pick
		g.addrKind = "v6dns"
		g.dnsSuffix = o.suffix
		picked := net.ParseIP(pick)
		label := ipv6DNSLabel(picked)
		name := fmt.Sprintf("%s.%s.%s", g.room, label, o.suffix)
		shareHost = name
		if ep != 25565 {
			g.address = fmt.Sprintf("%s:%d", name, ep)
		} else {
			g.address = name
		}
		fmt.Printf(" 地址形式         : 公共通配域名，依赖第三方\n")
		fmt.Printf(" 域名解析         : ")
		ips, err := net.LookupIP(name)
		ok := false
		for _, ip := range ips {
			if ip.Equal(picked) {
				ok = true
			}
		}
		if err != nil || !ok {
			fmt.Printf("失败：%v\n", err)
			if modeVal != "dns" {
				// auto：宁可给一个连得上、但如实说明“没有门槛”的地址，也不要给一个解析不通的域名。
				fmt.Println("  退回 IPv6 字面量（--mode auto）：这一形式没有房间码门槛")
				applyRaw()
				break
			}
			// 用户点名要域名形式（档位 P2/P3/P4 也是这么写的）：这时不能给一个解析不通的地址 ——
			// 玩家连不上，而卡片还会宣称有门槛。直接失败，把选择权交回用户。
			_ = ln.Close()
			return nil, fmt.Errorf("域名 %s 解析不通，不能作为玩家要填的地址：房间码要编进名字里，"+
				"就必须有一条能用的通配域名。可改用 --mode raw（该形式没有房间码门槛），"+
				"或换一台能解析 %s 的网络、或换一个 --dns 后缀后重试", name, o.suffix)
		} else {
			fmt.Printf("通过，%s 解析到 %s\n", name, pick)
		}
		// 该解析成功只代表房主侧；玩家侧 DNS 受限时提示改用 --mode raw。
		fmt.Println("  若玩家那边提示「无法解析主机名」（受限 DNS / 不解析通配域名）：")
		fmt.Println("  房主改用 --mode raw 重开一次即可 —— raw 直接给 IPv6 字面量，不经过任何 DNS")

	case publicV4Direct(g.stunV4):
		// 本机直接挂在公网 IPv4 上：无需开洞或第三方，地址即本机地址。
		shareHost = g.stunV4
		if ep == 25565 {
			g.address = shareHost
		} else {
			g.address = fmt.Sprintf("%s:%d", shareHost, ep)
		}
		// 公网 IPv4 可被全网扫描，房间码挡不住，故不做房门校验，靠名字白名单。
		gateRoom = false
		g.addrKind = "v4direct"
		fmt.Printf(" 地址形式         : 公网 IPv4 直连，无第三方依赖\n")

	default:
		// 无全球 IPv6 时尝试自动端口映射（UPnP/NAT-PMP/PCP），全程无需第三方。
		mapped := false
		if !o.noMap {
			fmt.Println(" .. 无公网 IPv6，尝试自动端口映射")
			m, tried := openNatMapping("TCP", ep, 25565, natMapLifetime)
			for _, t := range tried {
				if m != nil && strings.HasPrefix(t, m.Method) {
					continue // 成功项在下面单独输出
				}
				fmt.Printf("   %s\n", t)
			}
			if m != nil {
				extIP := m.ExtIP
				if extIP == "" {
					extIP = g.stunV4 // NAT-PMP/PCP 不提供外网地址，用 STUN 结果兜底
					m.ExtIP = extIP
				}
				if extIP == "" {
					fmt.Println("   端口映射已建立但出口地址未知，撤销映射")
					m.Release()
				} else {
					mapped = true
					shareHost = extIP
					g.setV4Map(m, g.v4infoOf(m), m.ExtPort)
					g.entryExt = m.ExtPort
					if m.ExtPort == 25565 {
						g.address = extIP
					} else {
						g.address = fmt.Sprintf("%s:%d", extIP, m.ExtPort)
					}
					gateRoom = false
					g.addrKind = "v4map"
					fmt.Printf("   %s\n", g.v4infoOf(m))
					fmt.Printf(" 地址形式         : IPv4 端口映射，无第三方依赖\n")
					go g.keepMappingAlive()
				}
			}
		}

		if !mapped {
			fmt.Println(" 结论：公网入口不可用——无公网 IPv6，自动端口映射不可用")
			fmt.Println("  原因：玩家需主动连接本机，本机位于 NAT 之后且无稳定入站端口")
			fmt.Println("  处理（按省事程度排）：")
			fmt.Println("    1) 最省事：请一位有公网地址（或有 VPS）的朋友运行 mclbx relaybox，")
			fmt.Println("       把他屏幕上那一行 mclbx:// 发给你，填进「中转服务器」即可；")
			fmt.Println("       你也可以自己在一台能上网的常驻机器上跑它。")
			fmt.Println("       注意：中继这条通道是明文转发，能用直连时别用它。")
			fmt.Println("    2) 在光猫或路由器开启 IPv6（家用宽带多数免费，改完重启一次）")
			fmt.Println("    3) 在路由器管理页把入口端口映射到本机")
			fmt.Println("    4) 两端都装本工具（双方都在 NAT 后时仍需中继，只是多一层加密）")
		}
	}

	// IPv6 路线且出口探测未通时必须提示。
	if has6 && g.v6Tried && !g.v6Reach {
		fmt.Println(" 提醒             : IPv6 出口探测无应答 —— 地址在网卡上，未必出得去")
		fmt.Println("                    先用纯 IPv6 网站确认本机能出去；不行就改用软件入场或中继")
	}

	// IPv6 路线下启动低频快照监控地址变化。
	if g.addrFollowsNIC() {
		go g.watchIPv6Changes()
	}

	var allowList []string
	if o.allow != "" {
		for _, s := range strings.Split(o.allow, ",") {
			if s = strings.TrimSpace(s); s != "" {
				allowList = append(allowList, s)
			}
		}
	}
	if note := allowListNote(allowList); note != "" {
		fmt.Printf(" 提醒             : %s\n", note)
	}

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(c, g, !o.noGate && gateRoom, allowList)
		}
	}()

	// 本地管理页
	if !strings.EqualFold(o.webAddr, "off") {
		go func() {
			if err := serveWeb(o.webAddr, g); err != nil {
				logf("管理页启动失败：%v", err)
			}
		}()
	}

	// 软件入场的地址要能直接拨：域名形式时用回 IPv6 字面量，免得"给直连不好使的人
	// 准备的兜底"本身又依赖公网 DNS。
	softHost := shareHost
	if g.shareV6 != "" {
		softHost = g.shareV6
	}

	return &gateReady{
		state: g, listener: ln, entryPort: ep,
		address: g.address, host: softHost, webAddr: o.webAddr,
		// gateRoom 是**生效的**门槛，不是“这一形式本来支不支持”——
		// 分享卡片按它写字，写错就会出现“卡片说校验房间码、实际谁都能进”。
		gateRoom: !o.noGate && gateRoom,
		noGate:   o.noGate,
	}, nil
}

// nonASCIIAllowNames 找出白名单里 Java 版发不出来的非 ASCII 名字。
func nonASCIIAllowNames(list []string) []string {
	var out []string
	for _, n := range list {
		for _, r := range n {
			if r > 127 {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// allowListNote 白名单里含非 ASCII 名字时的一句提醒；没有就返回空串。
func allowListNote(list []string) string {
	bad := nonASCIIAllowNames(list)
	if len(bad) == 0 {
		return ""
	}
	return fmt.Sprintf("白名单里的 %s 含非英文/数字字符，而 Java 版玩家名发不出这种名字，会被判成名单外；"+
		"请改填玩家的英文 ID（只含英文、数字与下划线）", strings.Join(bad, "、"))
}

// gateModeValue 把「口令是房间码还是地址本身」转成界面能用的一个词。
func gateModeValue(byRoom bool) string {
	if byRoom {
		return "room"
	}
	return "addr"
}

// gateOffExplanation 返回“这一档没有门槛”时该如实说给用户的话。
//
// 分几种情形是因为后果完全不同：IPv6 字面量难以被全网扫到，公网 IPv4 却可以被全网扫描。
// 卡片此前对二者都说“无法被全网扫描”，对 v4 形式来说那是错的 —— 会让用户以为只有
// 被告知的人才连得上，而实际上扫到端口的人都能进。
func gateOffExplanation(addrKind, room string, noGate, roomGiven bool) []string {
	var out []string
	switch {
	case noGate:
		out = append(out, "  说明：你已用 --no-gate 关掉房间码校验，拿到这一行的人都能进")
	case addrKind == "v6raw":
		out = append(out, "  说明：IPv6 地址空间无法被全网扫描，仅告知受信任的玩家")
	case addrKind == "v4direct" || addrKind == "v4map":
		out = append(out,
			"  说明：公网 IPv4 可以被全网扫描，拿到这一行的人都能进",
			"        需要门槛请改用 --mode dns（房间码编进玩家要填的名字）")
	default:
		out = append(out, "  说明：本模式没有房间码门槛")
	}
	// 给了房间码却落不到实处，必须指出来：否则用户以为门槛开着，其实没有。
	// 但只在用户自己给了房间码时才提醒 —— 程序随机生成的那种用户根本没见过，
	// 拿它去说“不生效”只会让人困惑。
	if !noGate && roomGiven && addrKind != "v6dns" {
		out = append(out, fmt.Sprintf("  注意：你给了房间码 %s，但这种地址形式没有地方承载它 —— "+
			"房间码在本模式下不生效，需要它生效请用 --mode dns", room))
	}
	if len(out) > 0 {
		out[len(out)-1] += "\n" // 卡片里这几行后面留一个空行，与旧版排版一致
	}
	return out
}

// zeroInstallCardLines 返回分享内容（不含标题与页脚）。
func zeroInstallCardLines(r *gateReady) []string {
	var out []string
	if r.gateRoom {
		out = append(out, fmt.Sprintf(" 口令 : 房间码 %s\n", r.state.room))
	} else {
		out = append(out, " 口令 : 地址本身")
		out = append(out, gateOffExplanation(r.state.addrKind, r.state.room, r.noGate, r.state.roomGiven)...)
	}
	tail := "，端口已包含在地址中：\n"
	if r.entryPort == 25565 {
		tail = "，无需填写端口：\n"
	}
	out = append(out,
		" 将下面这一行原样发送给玩家，在游戏内",
		"   「多人游戏 → 直接连接」中粘贴"+tail,
		"     "+r.address+"\n",
	)
	if r.gateRoom {
		out = append(out, "  说明：名称中不含房间码的连接将被拒绝")
	} else {
		out = append(out,
			"  说明：可通过「玩家名称白名单」限定可连接的玩家",
			"        名单外的连接将被拒绝",
		)
	}
	// 仅控制台界面拉起的子进程输出机器可读标记。
	if line := guiMarker("ADDR", r.address); line != "" {
		out = append(out, line)
	}
	if line := guiMarker("ROOM", r.state.room); line != "" {
		out = append(out, line)
	}
	// 将「口令是房间码还是地址本身」也播给界面。
	if line := guiMarker("GATE", gateModeValue(r.gateRoom)); line != "" {
		out = append(out, line)
	}
	return out
}

func printZeroInstallCard(r *gateReady) {
	fmt.Printf("================================================\n")
	fmt.Printf(" 公网入口：玩家在游戏内「直接连接」填入该地址\n")
	fmt.Printf("================================================\n")
	for _, l := range zeroInstallCardLines(r) {
		fmt.Println(l)
	}
	fmt.Printf("================================================\n")
	if !strings.EqualFold(r.webAddr, "off") {
		fmt.Printf(" 管理页 : http://%s\n", r.webAddr)
	}
	fmt.Printf(" 按 Ctrl+C 结束\n")
}

func waitForInterrupt(r *gateReady, what string) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	r.close()
	if r.state != nil {
		fmt.Printf("\n%s：累计接受 %d 次，拒绝 %d 次。\n", what, r.state.accepted.Load(), r.state.rejected.Load())
	}
}
