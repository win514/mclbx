package main

// relaybox.go —— 本机中继：UDP 中继（直连失败时转发）、TCP 通道（对端屏蔽 UDP 时使用）、
// 信令信箱（双方交换地址）。输出的 mclbx:// 链接内含房间码与密钥；无公网地址时可用 UPnP 放行 TCP 端口。

import (
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pion/turn/v5"
)

// relayRealm 为 TURN 域标识，两端一致即可。
const relayRealm = "mclbx"

// relayAddrInfo 描述对外可连接的地址。
type relayAddrInfo struct {
	Addr string // 别人要连的地址
	Kind string // 地址来源说明
	// Public：本机任意端口都可被外部直连（自有公网地址）
	Public bool
	// Mapped：地址由路由器映射而来；TCP 端口可逐个放行，UDP 中继所需的大量端口不可
	Mapped bool
	LAN    string // 同一局域网内适用的地址
}

// relaybox 记录本次启动的服务
type relaybox struct {
	turnPort   int
	tcpPort    int
	signalPort int
	room       string
	secret     string
	addr       relayAddrInfo

	turnUp bool
	tcpUp  bool
	sigUp  bool

	turnSrv *turn.Server
	tcpLn   net.Listener
	httpSrv *http.Server

	portPlan *natMapPlan // 路由器放行记录，退出时撤销
	openNote string      // 端口放行结论

	// randomSecret 表示密钥为本次随机生成（重启后旧链接失效）。
	randomSecret bool
}

func cmdRelayBox(args []string) error {
	fs := flag.NewFlagSet("relaybox", flag.ContinueOnError)
	turnPort := fs.Int("turn-port", 3478, "UDP 中继端口，直连失败时使用")
	tcpPort := fs.Int("tcp-port", 8090, "TCP 通道端口，对端网络屏蔽 UDP 时使用")
	signalPort := fs.Int("signal-port", 8091, "信令信箱端口；房主无法被直接连接时，双方在此交换地址")
	public := fs.String("public", "", "本机对外地址；不填则自动探测，优先公网 IPv6")
	room := fs.String("room", "", "房间码；不填随机生成一个")
	secret := fs.String("secret", "", "中继密钥；不填则每次随机，密钥包含在链接中")
	noTurn := fs.Bool("no-turn", false, "不开 UDP 中继")
	noSignal := fs.Bool("no-signal", false, "不开信令信箱")
	noOpen := fs.Bool("no-open-ports", false, "不修改路由器；不加此参数时将尝试自动端口映射")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	b := &relaybox{
		turnPort:   *turnPort,
		tcpPort:    *tcpPort,
		signalPort: *signalPort,
		room:       strings.TrimSpace(*room),
		secret:     strings.TrimSpace(*secret),
	}
	if b.room == "" {
		b.room = randomRoomCode(6)
	}
	if b.secret == "" {
		b.secret = randomKey(10)
		b.randomSecret = true
	}
	b.addr = detectRelayAddr(*public)
	if b.addr.Addr == "" {
		return fmt.Errorf("未找到可对外发布的地址。可用 --public 指定本机对外地址后重试")
	}

	// 仅有公网地址且非映射的机器开启 UDP 中继
	wantTurn := !*noTurn && b.addr.Public && !b.addr.Mapped
	turnMakesSense := !*noTurn && net.ParseIP(b.addr.Addr) != nil

	var errs []string
	if turnMakesSense && wantTurn {
		if err := b.startTurn(); err != nil {
			errs = append(errs, fmt.Sprintf("UDP 中继启动失败：%v", err))
		}
	}
	if err := b.startTCP(); err != nil {
		errs = append(errs, fmt.Sprintf("TCP 通道启动失败：%v", err))
	}
	if !*noSignal {
		if err := b.startSignal(); err != nil {
			errs = append(errs, fmt.Sprintf("信令信箱启动失败：%v", err))
		}
	}
	if !b.turnUp && !b.tcpUp && !b.sigUp {
		return fmt.Errorf("全部服务启动失败：%s", strings.Join(errs, "；"))
	}

	b.openPorts(*noOpen)
	b.printCard(errs, *noTurn, turnMakesSense)

	// 放行有租期，须按时续约
	go b.renewLoop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\n 收到结束信号，正在停止…")
	b.close()
	fmt.Println(" 已停止，路由器上的自动端口映射已撤销。")
	return nil
}

// detectRelayAddr 选择对外地址，优先级：手填 > 公网 IPv6 > 路由器公网 IPv4 > 内网地址。
func detectRelayAddr(explicit string) relayAddrInfo {
	if s := strings.TrimSpace(explicit); s != "" {
		ip := strings.Trim(s, "[]")
		return relayAddrInfo{Addr: ip, Kind: "手动指定地址：" + ip, Public: true}
	}

	if v6 := globalIPv6(); len(v6) > 0 {
		lan := firstNonLoopbackIPv4()
		if lan == v6[0].IP.String() {
			lan = ""
		}
		return relayAddrInfo{
			Addr:   v6[0].IP.String(),
			Kind:   "本机公网 IPv6，无需修改路由器",
			Public: true,
			LAN:    lan,
		}
	}

	if loc, err := upnpDiscover(2 * time.Second); err == nil {
		if _, ext, err := upnpExternalIP(loc); err == nil && ext != "" && net.ParseIP(ext) != nil {
			lan := firstNonLoopbackIPv4()
			return relayAddrInfo{
				Addr:   ext,
				Kind:   "路由器的公网地址，本机位于路由器后方",
				Public: true,
				Mapped: true,
				LAN:    lan,
			}
		}
	}

	lan := firstNonLoopbackIPv4()
	if lan == "" {
		return relayAddrInfo{}
	}
	return relayAddrInfo{
		Addr: lan,
		Kind: "本机内网地址，未找到公网地址",
		LAN:  lan,
	}
}

func (b *relaybox) startTurn() error {
	// 本机为 IPv6 时须用 IPv6 socket
	network, bindHost := udpNetworkAndHost(b.addr.Addr)
	pc, err := net.ListenPacket(network, net.JoinHostPort(bindHost, strconv.Itoa(b.turnPort)))
	if err != nil {
		return fmt.Errorf("UDP %d 已被占用，请改用 --turn-port", b.turnPort)
	}
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: relayRealm,
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: pc,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				// 中转出口地址即对外地址
				RelayAddress: net.ParseIP(b.addr.Addr),
				// 不绑定具体网卡，避免多网卡机器从其他口发出
				Address: bindHost,
			},
		}},
		// 用临时凭据，密钥现算一次性账号
		AuthHandler: restAuthHandler(b.secret, relayRealm),
	})
	if err != nil {
		_ = pc.Close()
		return err
	}
	b.turnSrv = srv
	b.turnUp = true
	return nil
}

func (b *relaybox) startTCP() error {
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(b.tcpPort)))
	if err != nil {
		return fmt.Errorf("TCP %d 已被占用，请改用 --tcp-port", b.tcpPort)
	}
	b.tcpLn = ln
	b.tcpUp = true
	var (
		mu      sync.Mutex
		waiting = map[string]chan relayPeer{}
	)
	go serveRelay(ln, 3*time.Minute, &mu, waiting)
	return nil
}

func (b *relaybox) startSignal() error {
	mb := newMailbox()
	srv := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(b.signalPort)),
		Handler:           mb,
		ReadHeaderTimeout: 5 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("TCP %d 已被占用，请改用 --signal-port", b.signalPort)
	}
	b.httpSrv = srv
	b.sigUp = true
	go func() { _ = srv.Serve(ln) }()
	return nil
}

// openPorts 在路由器后面（Mapped）的机器上请求放行所需端口。
func (b *relaybox) openPorts(disabled bool) {
	if disabled {
		b.openNote = "已按 no-open-ports 设置跳过路由器配置；若对端无法连接，请手动配置端口映射"
		return
	}
	if !b.addr.Mapped {
		b.openNote = "无需修改路由器，可直接连接"
		return
	}

	var (
		protos []string
		ports  []int
	)
	if b.tcpUp {
		protos, ports = append(protos, "TCP"), append(ports, b.tcpPort)
	}
	if b.sigUp {
		protos, ports = append(protos, "TCP"), append(ports, b.signalPort)
	}
	if b.turnUp {
		protos, ports = append(protos, "UDP"), append(ports, b.turnPort)
	}
	if len(ports) == 0 {
		return
	}

	var plan natMapPlan
	var okList []string
	for i, p := range protos {
		sub := mapPortsQuiet([]string{p}, ports[i], 0)
		if sub == nil {
			continue
		}
		plan.Mappings = append(plan.Mappings, sub.Mappings...)
		okList = append(okList, fmt.Sprintf("%s %d", p, ports[i]))
	}
	if len(plan.Mappings) == 0 {
		b.openNote = "自动端口映射失败；若对端无法连接，请手动配置端口映射"
		return
	}
	b.portPlan = &plan
	b.openNote = "自动端口映射已建立：" + strings.Join(okList, "、")
}

// renewLoop 每 6 分钟续约路由器放行（租期 15 分钟）。
func (b *relaybox) renewLoop() {
	if b.portPlan == nil {
		return
	}
	for range time.Tick(6 * time.Minute) {
		var protos []string
		var ports []int
		if b.tcpUp {
			protos, ports = append(protos, "TCP"), append(ports, b.tcpPort)
		}
		if b.sigUp {
			protos, ports = append(protos, "TCP"), append(ports, b.signalPort)
		}
		if b.turnUp {
			protos, ports = append(protos, "UDP"), append(ports, b.turnPort)
		}
		var fresh natMapPlan
		for i, p := range protos {
			if sub := mapPortsQuiet([]string{p}, ports[i], 0); sub != nil {
				fresh.Mappings = append(fresh.Mappings, sub.Mappings...)
			}
		}
		if len(fresh.Mappings) == 0 {
			continue
		}
		b.portPlan.Release()
		b.portPlan = &fresh
	}
}

func (b *relaybox) close() {
	if b.turnSrv != nil {
		_ = b.turnSrv.Close()
	}
	if b.tcpLn != nil {
		_ = b.tcpLn.Close()
	}
	if b.httpSrv != nil {
		_ = b.httpSrv.Close()
	}
	if b.portPlan != nil {
		b.portPlan.Release()
	}
}

// shareLink 拼出分享链接，内含房间码与密钥。
func (b *relaybox) shareLink() string {
	host := b.addr.Addr
	if strings.Contains(host, ":") { // IPv6 要带方括号，否则端口分不出来
		host = "[" + strings.Trim(host, "[]") + "]"
	}
	q := []string{}
	if b.turnUp {
		q = append(q, "turn="+strconv.Itoa(b.turnPort))
	}
	if b.tcpUp {
		q = append(q, "tcp="+strconv.Itoa(b.tcpPort))
	}
	if b.sigUp {
		q = append(q, "sig="+strconv.Itoa(b.signalPort))
	}
	q = append(q, "room="+escCred(b.room), "secret="+escCred(b.secret))
	return "mclbx://" + host + "?" + strings.Join(q, "&")
}

func (b *relaybox) printCard(errs []string, turnMuted, turnMakesSense bool) {
	link := b.shareLink()
	fmt.Printf("================================================\n")
	fmt.Printf(" %s · 本机中继服务\n", version)
	fmt.Printf("================================================\n")
	fmt.Printf(" 对外地址 : %s\n", b.addr.Kind)
	fmt.Printf(" 已启动\n")
	if b.tcpUp {
		fmt.Printf("   TCP 通道  : 端口 %d，对端屏蔽 UDP 或直连失败时使用\n", b.tcpPort)
	}
	if b.sigUp {
		fmt.Printf("   信令信箱 : 端口 %d，房主被直接连不上时双方在此交换地址\n", b.signalPort)
	}
	if b.turnUp {
		fmt.Printf("   UDP 中继  : 端口 %d，直连失败时流量经本机转发\n", b.turnPort)
	} else if b.addr.Mapped {
		fmt.Printf("   UDP 中继  : 未开启，本机位于路由器后方，中继需要大量随机端口，无法逐个映射\n")
	} else if turnMuted {
		fmt.Printf("   UDP 中继  : 已按 no-turn 设置关闭\n")
	} else if turnMakesSense {
		fmt.Printf("   UDP 中继  : 启动失败，原因见上\n")
	}
	fmt.Printf(" 端口映射 : %s\n", b.openNote)
	fmt.Printf("------------------------------------------------\n")
	fmt.Printf(" 将下面这一行发给其他玩家，填入「中继服务器」即可：\n")
	fmt.Printf("     %s\n", link)
	fmt.Printf("   账号与密钥已包含在此行中，无需另行发送密码，请勿公开分享\n")
	if b.addr.LAN != "" && b.addr.LAN != b.addr.Addr {
		fmt.Printf("   同一局域网内使用此链接速度更快：mclbx://%s?tcp=%d&sig=%d&room=%s&secret=%s\n",
			b.addr.LAN, b.tcpPort, b.signalPort, escCred(b.room), escCred(b.secret))
	}
	fmt.Printf(" 对端用法 : 填入「中继服务器」，再运行「加入房间」或「TCP 中继·玩家」\n")
	fmt.Printf(" 连接校验 : 可先运行 mclbx relaycheck <上面那一行>\n")
	if b.randomSecret {
		// 重启后密钥会变，旧链接失效。
		fmt.Printf(" 注意     : 本次密钥是随机生成的，重启后它会变，之前发出去的链接随之失效；\n")
		fmt.Printf("            想长期固定就用 --secret <一串自己定的字符>\n")
	}
	fmt.Printf(" 端口冲突 : 撞端口时可用 --turn-port / --tcp-port / --signal-port 换\n")
	for _, e := range errs {
		fmt.Printf(" 提醒     : %s\n", e)
	}
	fmt.Printf(" 按 Ctrl+C 结束，退出时自动撤销路由器上的端口映射\n")

	if line := guiMarker("ADDR", link); line != "" {
		fmt.Println(line)
	}
}

// randomKey 生成随机密钥串（去掉易混字符）。
func randomKey(n int) string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprint(time.Now().UnixNano() % 100000000)
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}
