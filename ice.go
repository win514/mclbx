package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

// ice.go 用 ICE（RFC 8445，pion/ice 实现）打通 NAT，并在其数据报连接上做极简长度分帧。

// 信令信箱：双方各登记一份候选清单，并读取对方的一份。
type candBlob struct {
	Ufrag       string   `json:"ufrag"`
	Pwd         string   `json:"pwd"`
	Cands       []string `json:"cands"`
	Fingerprint string   `json:"fp,omitempty"`   // 本端 DTLS 证书的 SHA-256 指纹
	Room        string   `json:"room,omitempty"` // 房间码，软件入场也要校验。

	// Gen 为尝试代次号，断线重连时递增。
	Gen int `json:"gen,omitempty"`
}

// mailboxEntry 是信箱里的一条：内容 + 最后一次写入的时刻。
type mailboxEntry struct {
	b    candBlob
	seen time.Time
}

type mailbox struct {
	mu  sync.Mutex
	m   map[string]mailboxEntry
	rev uint64 // 每次写入自增，供 /rev 判断是否有变化。

	// 信箱满的记账与速率限制。
	fullCount  int
	fullLogged time.Time
}

// noteFullLocked 记录一次信箱满并输出日志，调用方须已持锁。
func (mb *mailbox) noteFullLocked(now time.Time) {
	mb.fullCount++
	if now.Sub(mb.fullLogged) < 30*time.Second {
		return
	}
	n := mb.fullCount
	mb.fullCount = 0
	mb.fullLogged = now
	logf("[信令] 信箱已满（上限 %d 条），拒绝了新登记：本段时间内共 %d 次。"+
		"若非受邀玩家，说明房间地址已外传：结束房间并更换房间码重开。", mailboxMaxSides, n)
}

func newMailbox() *mailbox { return &mailbox{m: map[string]mailboxEntry{}} }

// 信箱的容量上限与键长上限。
const (
	mailboxMaxSides = 1024
	mailboxMaxKey   = 64 // <side> 键的长度上限

	// mailboxTTL 是条目存活时间：未再被写过的条目在下次请求时回收。
	mailboxTTL = 3 * time.Minute

	// mailboxKeepAlive 是等待中一方重新登记候选的间隔。
	mailboxKeepAlive = 30 * time.Second
)

// sweepLocked 惰性回收过期条目，返回回收条数，调用方须已持锁。
func (mb *mailbox) sweepLocked(now time.Time) int {
	n := 0
	for k, e := range mb.m {
		if now.Sub(e.seen) > mailboxTTL {
			delete(mb.m, k)
			n++
		}
	}
	return n
}

func (mb *mailbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /rev 返回自增计数，供房主判断是否有变化。
	if r.URL.Path == "/rev" && r.Method == http.MethodGet {
		mb.mu.Lock()
		mb.sweepLocked(time.Now())
		rev, n := mb.rev, len(mb.m)
		mb.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"rev": rev, "n": n})
		return
	}

	// /blobs 列出登记表，供房主发现新客机。
	if r.URL.Path == "/blobs" && r.Method == http.MethodGet {
		mb.mu.Lock()
		mb.sweepLocked(time.Now()) // 回收过期项，列出结果不含过期条目
		keys := make([]string, 0, len(mb.m))
		for k := range mb.m {
			keys = append(keys, k)
		}
		mb.mu.Unlock()
		sort.Strings(keys)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(keys)
		return
	}

	side := strings.TrimPrefix(r.URL.Path, "/blob/")
	if side == "" || side == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var b candBlob
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&b); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(side) > mailboxMaxKey {
			http.Error(w, "键太长", http.StatusBadRequest)
			return
		}
		mb.mu.Lock()
		// 先回收再判容量。
		mb.sweepLocked(time.Now())
		_, exists := mb.m[side]
		if !exists && len(mb.m) >= mailboxMaxSides {
			mb.noteFullLocked(time.Now())
			mb.mu.Unlock()
			http.Error(w, "信箱满了", http.StatusServiceUnavailable)
			return
		}
		mb.m[side] = mailboxEntry{b: b, seen: time.Now()}
		mb.rev++
		mb.mu.Unlock()
		logf("[信令] 收到 %s 的候选：%d 条", side, len(b.Cands))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		mb.mu.Lock()
		mb.sweepLocked(time.Now()) // 过期条目对取信方等同于不存在
		e, ok := mb.m[side]
		mb.mu.Unlock()
		if !ok {
			http.Error(w, "not yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(e.b)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func fetchBlob(base, side string, timeout time.Duration) (candBlob, error) {
	var b candBlob
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(base + "/blob/" + side)
	if err != nil {
		return b, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return b, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&b)
	return b, err
}

func postBlob(base, side string, b candBlob, timeout time.Duration) error {
	buf, err := json.Marshal(b)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(base+"/blob/"+side, "application/json", strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// blobKeeper 每隔 mailboxKeepAlive 重新登记一次自己的候选。
type blobKeeper struct {
	base string
	side string
	blob candBlob
	last time.Time
}

func newBlobKeeper(base, side string, b candBlob) *blobKeeper {
	return &blobKeeper{base: base, side: side, blob: b, last: time.Now()}
}

// tick 到期重发一次，返回是否重发。
func (k *blobKeeper) tick(now time.Time, timeout time.Duration) bool {
	if now.Sub(k.last) < mailboxKeepAlive {
		return false
	}
	k.last = now
	if err := postBlob(k.base, k.side, k.blob, timeout); err != nil {
		logf("[信令] 重新登记 %s 失败：%v", k.side, err)
		return false
	}
	return true
}

// listBlobs 列出信令信箱里的条目名。
func listBlobs(base string, timeout time.Duration) ([]string, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(base + "/blobs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var keys []string
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&keys)
	return keys, err
}

// mailboxRev 读信箱自增计数；ok=false 表示接口不可用，调用方应按有变化处理。
func mailboxRev(base string, timeout time.Duration) (uint64, bool) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(base + "/rev")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var v struct {
		Rev uint64 `json:"rev"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&v); err != nil {
		return 0, false
	}
	return v.Rev, true
}

// iceStateCallback 是注册给 ICE agent 的唯一状态回调。
func iceStateCallback(onFail func()) func(ice.ConnectionState) {
	return func(s ice.ConnectionState) {
		logf("  ICE 状态：%s", s.String())
		// 只在 Failed 触发：Disconnected 可能自愈，Failed 不可自愈。
		if s == ice.ConnectionStateFailed && onFail != nil {
			onFail()
		}
	}
}

func newIceAgent(withSTUN bool, onFail func()) (*ice.Agent, error) {
	cfg := &ice.AgentConfig{
		NetworkTypes:   []ice.NetworkType{ice.NetworkTypeUDP4},
		CandidateTypes: []ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive},
	}
	if withSTUN {
		for _, s := range stunURLs() {
			if u, err := stun.ParseURI(s); err == nil {
				cfg.Urls = append(cfg.Urls, u)
			}
		}
	}

	// 中继服务器：候选类型加上 relay 后，对称型 NAT 也能连上，走中继转发。
	plan, err := activeRelayPlan()
	if err != nil {
		return nil, err
	}
	if plan != nil {
		logRelayPlanOnce(plan)
		turns, errs := plan.TurnURLs("", "")
		for _, e := range errs {
			logf("中继配置无效，已忽略：%v", e)
		}
		if len(turns) > 0 {
			cfg.CandidateTypes = append(cfg.CandidateTypes, ice.CandidateTypeRelay)
			cfg.Urls = append(cfg.Urls, turns...)
		} else {
			// 只有 TCP 中继，对 ICE 打洞无帮助。
			logf("  注意：该中继仅提供 TCP 中继转发，ICE 直连不可用；直连失败时请使用中继转发")
		}
	}

	a, err := ice.NewAgent(cfg)
	if err != nil {
		return nil, err
	}
	// 建完 agent 立刻装状态回调。
	a.OnConnectionStateChange(iceStateCallback(onFail))
	return a, nil
}

// parseTurnEntry 把 TURN 配置拆成 (scheme, 主机端口, 账号, 密码, 错误)，账号在最后一个 @ 之前。
func parseTurnEntry(s string) (scheme, hostPort, user, pass string, err error) {
	raw := strings.TrimSpace(s)
	i := strings.Index(raw, ":")
	if i <= 0 {
		return "", "", "", "", fmt.Errorf("地址不是 turn:/turns: 开头")
	}
	scheme = strings.ToLower(raw[:i])
	if scheme != "turn" && scheme != "turns" {
		return "", "", "", "", fmt.Errorf("仅支持 turn:/turns: 开头的地址")
	}
	rest := strings.TrimPrefix(raw[i+1:], "//")
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		creds := rest[:at]
		rest = rest[at+1:]
		if c := strings.Index(creds, ":"); c >= 0 {
			user, pass = decodeMaybe(creds[:c]), decodeMaybe(creds[c+1:])
		} else {
			user = decodeMaybe(creds)
		}
	}
	if rest == "" {
		return "", "", "", "", fmt.Errorf("缺少主机:端口")
	}
	return scheme, rest, user, pass, nil
}

// decodeMaybe 尽量做 URL 反转义，解不开则按原样返回。
func decodeMaybe(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// 中继链接的解析见 relaylink.go。

// stunURLs 返回 STUN 服务器列表，可由 MCLBX_STUN 覆盖（逗号分隔）。
func stunURLs() []string {
	if v := strings.TrimSpace(os.Getenv("MCLBX_STUN")); v != "" {
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"stun:stun.miwifi.com:3478", "stun:stun.hitv.com:3478"}
}

// hasServerReflexive 判断候选里有没有 STUN 换回的外网映射地址（typ srflx）。
func hasServerReflexive(cands []string) bool {
	for _, c := range cands {
		if strings.Contains(c, " typ srflx") {
			return true
		}
	}
	return false
}

// stunTroubleHint 在无外网映射候选时说明本次用过的 STUN 与更换方式。
func stunTroubleHint() string {
	urls := stunURLs()
	if len(urls) == 0 {
		return "未配置 STUN 服务器（可用环境变量 MCLBX_STUN 指定，逗号分隔）"
	}
	return fmt.Sprintf("本次配置的 STUN（%s）都没有换回外网映射地址 —— 这类网络多半 UDP 受限，"+
		"请改用中继转发；想换 STUN 服务器可设环境变量 MCLBX_STUN（逗号分隔）",
		strings.Join(urls, "、"))
}

// gatherCandidates 收集本地候选，OnCandidate(nil) 表示收集完毕。
func gatherCandidates(a *ice.Agent, timeout time.Duration, label string) ([]string, error) {
	var (
		mu   sync.Mutex
		out  []string
		done = make(chan struct{})
		once sync.Once
	)
	a.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			once.Do(func() { close(done) })
			return
		}
		line := c.Marshal()
		mu.Lock()
		out = append(out, line)
		mu.Unlock()
		logf("  [%s] 本地候选 %-6s %s", label, c.Type().String(), line)
	})
	a.OnSelectedCandidatePairChange(func(l, r ice.Candidate) {
		logf("  选中的候选对：本地 %s(%s)  <->  对端 %s(%s)",
			l.Address(), l.Type().String(), r.Address(), r.Type().String())
	})
	// 状态回调由 newIceAgent 统一注册，此处不重复注册。

	if err := a.GatherCandidates(); err != nil {
		return nil, err
	}
	select {
	case <-done:
	case <-time.After(timeout):
		guiDetailf(".. 候选收集超时，使用已获取的 %d 条继续", len(out))
	}
	mu.Lock()
	res := append([]string(nil), out...)
	mu.Unlock()
	return res, nil
}

func dumpPair(a *ice.Agent) {
	p, err := a.GetSelectedCandidatePair()
	if err != nil || p == nil {
		logf("  未读取到选中的候选对：%v", err)
		return
	}
	logf("  最终候选对：本地 %s:%d (%s)  <->  对端 %s:%d (%s)",
		p.Local.Address(), p.Local.Port(), p.Local.Type().String(),
		p.Remote.Address(), p.Remote.Port(), p.Remote.Type().String())
}

// 数据报上的字节流：仅做长度分帧，不保证送达。
const (
	msgOpen  = 1
	msgData  = 2
	msgClose = 3
	maxChunk = 1100
)

func sendMsg(c net.Conn, typ byte, payload []byte) error {
	buf := make([]byte, 3+len(payload))
	buf[0] = typ
	buf[1] = byte(len(payload) >> 8)
	buf[2] = byte(len(payload))
	copy(buf[3:], payload)
	_, err := c.Write(buf)
	return err
}

// readLoop 是整条 ICE 通道的读循环；CLOSE 只结束当前会话，读循环不退出。
func readLoop(c net.Conn, onOpen func(), onData func([]byte), onClose func()) {
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if err != nil {
			onClose()
			return
		}
		if n < 3 {
			continue
		}
		typ := buf[0]
		l := int(buf[1])<<8 | int(buf[2])
		if 3+l > n {
			continue
		}
		switch typ {
		case msgOpen:
			onOpen()
		case msgData:
			onData(buf[3 : 3+l])
		case msgClose:
			onClose()
		}
	}
}

// defaultListenAddr 是对外服务的默认监听地址：只给端口不给主机，以走双栈绑定。
const defaultListenAddr = ":8090"

// listenSignal 同步绑定信令信箱监听口，失败时返回含处理建议的错误。
func listenSignal(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("信令信箱无法监听 %s：%w\n  原因：端口可能已被占用（同一台机器上开第二个房间时最常见）\n  处理：换一个端口，例如 --signal :8091", addr, err)
	}
	return ln, nil
}

func cmdIceHost(args []string) error {
	fs := flag.NewFlagSet("ice host", flag.ContinueOnError)
	signalAddr := fs.String("signal", defaultListenAddr, "信令信箱监听地址")
	signalURL := fs.String("signal-url", "", "使用外部信令信箱，例如 http://10.0.0.1:8090；填写后不再自建")
	to := fs.String("to", "127.0.0.1:25566", "转发目标地址")
	useStun := fs.Bool("stun", true, "使用 STUN 服务收集公网映射候选")
	wait := fs.Duration("wait", 90*time.Second, "等待对端候选的最长时间")
	transport := fs.String("transport", "sctp", "隧道传输层：sctp 或 raw；sctp 基于 ICE + DTLS + SCTP，raw 仅长度分帧")
	tamperFP := fs.Bool("tamper-fp", false, "仅测试：公布错误的证书指纹，用于验证绑定是否被拒绝")
	relayLink := fs.String("relay-server", "", "中继服务；直连失败时的兜底；turn:/turns:/tcp:/mclbx:// 或 主机:端口")
	relayUser := fs.String("relay-user", "", "中继账号；链接未提供账号时使用")
	relayPass := fs.String("relay-pass", "", "中继密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *transport != "sctp" && *transport != "raw" {
		return fmt.Errorf("--transport 只能是 sctp 或 raw")
	}
	applyRelayFlag(*relayLink, *relayUser, *relayPass)

	// 本端自签证书与指纹，指纹随信令公布。
	id, err := newIdentity()
	if err != nil {
		return fmt.Errorf("生成身份失败：%w", err)
	}
	announceFP := id.fingerprint()
	if *tamperFP && *transport == "sctp" {
		announceFP = "00" + announceFP[2:] // 只改一位，模拟信令被改写
		logf("! 已启用 --tamper-fp：公布错误指纹，模拟信令被中间人改写")
	}
	// 挑战用公布值，故这里存公布出去的指纹。
	selfFP, err := parseFingerprint(announceFP)
	if err != nil {
		return fmt.Errorf("解析本端指纹失败：%w", err)
	}

	fmt.Printf("==== mclbx ice host · 直连模式 · 房主侧 ====\n")
	fmt.Printf("转发目标 : %s\n", *to)

	// 信令信箱：默认自建一个，也可用 --signal-url 指定外部信箱。
	base := strings.TrimRight(strings.TrimSpace(*signalURL), "/")
	if base == "" {
		mb := newMailbox()
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
		base = "http://127.0.0.1" + portOf(*signalAddr)
		fmt.Printf("信令信箱 : %s（本机自建，监听 %s）\n", base, *signalAddr)
	} else {
		fmt.Printf("信令信箱 : %s（外部提供）\n", base)
	}

	a, err := newIceAgent(*useStun, nil)
	if err != nil {
		return fmt.Errorf("创建 ICE agent 失败：%w", err)
	}
	defer a.Close()

	guiDetailf(".. 收集本地候选")
	cands, err := gatherCandidates(a, 8*time.Second, "host")
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		return fmt.Errorf("未收到任何候选")
	}
	ufrag, pwd, err := a.GetLocalUserCredentials()
	if err != nil {
		return err
	}
	myBlob := candBlob{Ufrag: ufrag, Pwd: pwd, Cands: cands, Fingerprint: announceFP}
	if err := postBlob(base, "host", myBlob, 10*time.Second); err != nil {
		return fmt.Errorf("发布本机候选失败：%w\n  原因：信令信箱不可达\n  处理：检查信令信箱地址与网络", err)
	}
	guiDetailf(".. 已发布本机候选 %d 条与证书指纹 %s，等待对端", len(cands), announceFP)
	// 等待期间定期重新登记，信箱条目有存活时间。
	keep := newBlobKeeper(base, "host", myBlob)

	deadline := time.Now().Add(*wait)
	var guest candBlob
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("等待对端候选超时")
		}
		if keep.tick(time.Now(), 5*time.Second) {
			logf("##DBG## [信令] 重新登记 host 候选（信箱条目有存活时间）")
		}
		if b, err := fetchBlob(base, "guest", 2*time.Second); err == nil {
			guest = b
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	logf("已获取对端候选 %d 条：", len(guest.Cands))
	for _, c := range guest.Cands {
		logf("  [guest] %s", c)
	}
	for _, s := range guest.Cands {
		if c, err := ice.UnmarshalCandidate(s); err == nil {
			if err := a.AddRemoteCandidate(c); err != nil {
				logf("  加入远端候选失败：%v", err)
			}
		} else {
			logf("  解析远端候选失败：%v", err)
		}
	}

	// 走 SCTP 时对端必须公布证书指纹。
	var peerFP [32]byte
	if *transport == "sctp" {
		if guest.Fingerprint == "" {
			return fmt.Errorf("对端没有公布证书指纹，拒绝在无身份认证的情况下建立连接")
		}
		var ferr error
		if peerFP, ferr = parseFingerprint(guest.Fingerprint); ferr != nil {
			return fmt.Errorf("解析对端证书指纹失败：%w", ferr)
		}
		logf("对端公布的证书指纹：%s，握手时校验", colonHex(peerFP[:]))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	guiDetailf(".. 开始连通性检查，本方为控制方")
	conn, err := a.Dial(ctx, guest.Ufrag, guest.Pwd)
	if err != nil {
		return fmt.Errorf("直连失败：%w", err)
	}
	defer conn.Close()
	logf("结论：ICE 通道已建立")
	dumpPair(a)

	if *transport == "sctp" {
		return serveSCTPHost(conn, iceRemoteAddr(a), *to, id, selfFP, peerFP, nil)
	}
	logf("隧道传输层：raw，长度分帧，无流控与重传，仅用于连通验证")
	logf("! raw 路径无加密、无身份认证，仅用于对照实验")

	// 等对端发 OPEN，再把隧道与本地服务对接。
	var (
		upMu sync.Mutex
		up   net.Conn
	)
	readLoop(conn,
		func() {
			// 新会话来了，先收掉上一个（仅支持单会话）。
			upMu.Lock()
			if up != nil {
				_ = up.Close()
				up = nil
			}
			upMu.Unlock()

			logf("[通道] 对端请求打开会话，转发到 %s", *to)
			c, err := net.DialTimeout("tcp", *to, 5*time.Second)
			if err != nil {
				logf("[通道] 连接 %s 失败：%v", *to, err)
				return
			}
			upMu.Lock()
			up = c
			upMu.Unlock()
			// 本地服务 -> 隧道
			go func(c net.Conn) {
				buf := make([]byte, maxChunk)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if e := sendMsg(conn, msgData, buf[:n]); e != nil {
							return
						}
					}
					if err != nil {
						_ = sendMsg(conn, msgClose, nil)
						return
					}
				}
			}(c)
		},
		func(b []byte) { // 隧道 -> 本地服务
			upMu.Lock()
			c := up
			upMu.Unlock()
			if c != nil {
				_, _ = c.Write(b)
			}
		},
		func() {
			upMu.Lock()
			if up != nil {
				_ = up.Close()
				up = nil
			}
			upMu.Unlock()
			logf("[通道] 会话结束")
		},
	)

	select {}
}

func cmdIceGuest(args []string) error {
	fs := flag.NewFlagSet("ice guest", flag.ContinueOnError)
	hostSignal := fs.String("host", "10.0.2.2:8090", "房主侧信令信箱地址")
	local := fs.String("local", "127.0.0.1:25565", "本机监听地址")
	useStun := fs.Bool("stun", true, "使用 STUN 服务收集公网映射候选")
	wait := fs.Duration("wait", 90*time.Second, "等待房主候选的最长时间")
	transport := fs.String("transport", "sctp", "隧道传输层：sctp 或 raw；sctp 基于 ICE + DTLS + SCTP，raw 仅长度分帧")
	tamperFP := fs.Bool("tamper-fp", false, "仅测试：公布错误的证书指纹，用于验证绑定是否被拒绝")
	relayLink := fs.String("relay-server", "", "中继服务；直连失败时的兜底；turn:/turns:/tcp:/mclbx:// 或 主机:端口")
	relayUser := fs.String("relay-user", "", "中继账号；链接未提供账号时使用")
	relayPass := fs.String("relay-pass", "", "中继密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *transport != "sctp" && *transport != "raw" {
		return fmt.Errorf("--transport 只能是 sctp 或 raw")
	}
	applyRelayFlag(*relayLink, *relayUser, *relayPass)

	// 本端自签证书与指纹（双向绑定）。
	id, err := newIdentity()
	if err != nil {
		return fmt.Errorf("生成身份失败：%w", err)
	}
	announceFP := id.fingerprint()
	if *tamperFP && *transport == "sctp" {
		announceFP = "00" + announceFP[2:]
		logf("! 已启用 --tamper-fp：公布错误指纹，模拟信令被中间人改写")
	}
	selfFP, err := parseFingerprint(announceFP)
	if err != nil {
		return fmt.Errorf("解析本端指纹失败：%w", err)
	}

	base := "http://" + *hostSignal
	fmt.Printf("==== mclbx ice guest · 直连模式 · 玩家侧 ====\n")
	fmt.Printf("信令信箱 : %s\n", base)
	fmt.Printf("本机监听 : %s\n", *local)

	a, err := newIceAgent(*useStun, nil)
	if err != nil {
		return fmt.Errorf("创建 ICE agent 失败：%w", err)
	}
	defer a.Close()

	guiDetailf(".. 收集本地候选")
	cands, err := gatherCandidates(a, 8*time.Second, "guest")
	if err != nil {
		return err
	}
	ufrag, pwd, err := a.GetLocalUserCredentials()
	if err != nil {
		return err
	}
	myBlob := candBlob{Ufrag: ufrag, Pwd: pwd, Cands: cands, Fingerprint: announceFP}
	if err := postBlob(base, "guest", myBlob, 10*time.Second); err != nil {
		return fmt.Errorf("发布本机候选失败：%w", err)
	}
	guiDetailf(".. 已发布本机候选 %d 条与证书指纹 %s，等待房主候选", len(cands), announceFP)
	// 登记不能只做一次，等房主可能很久。
	keep := newBlobKeeper(base, "guest", myBlob)

	deadline := time.Now().Add(*wait)
	var host candBlob
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("等待房主候选超时")
		}
		if keep.tick(time.Now(), 5*time.Second) {
			logf("##DBG## [信令] 重新登记 guest 候选（信箱条目有存活时间）")
		}
		if b, err := fetchBlob(base, "host", 5*time.Second); err == nil {
			host = b
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	logf("已获取房主候选 %d 条", len(host.Cands))
	for _, s := range host.Cands {
		if c, err := ice.UnmarshalCandidate(s); err == nil {
			_ = a.AddRemoteCandidate(c)
		}
	}

	// 走 SCTP 时主机必须公布证书指纹。
	var peerFP [32]byte
	if *transport == "sctp" {
		if host.Fingerprint == "" {
			return fmt.Errorf("房主没有公布证书指纹，拒绝在无身份认证的情况下建立连接")
		}
		var ferr error
		if peerFP, ferr = parseFingerprint(host.Fingerprint); ferr != nil {
			return fmt.Errorf("解析房主证书指纹失败：%w", ferr)
		}
		logf("房主公布的证书指纹：%s，握手时校验", colonHex(peerFP[:]))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	guiDetailf(".. 等待房主发起连通性检查，本方为被控方")
	conn, err := a.Accept(ctx, host.Ufrag, host.Pwd)
	if err != nil {
		return fmt.Errorf("直连失败：%w", err)
	}
	defer conn.Close()
	logf("结论：ICE 通道已建立")
	dumpPair(a)

	if *transport == "sctp" {
		return serveSCTPGuest(conn, iceRemoteAddr(a), *local, id, selfFP, peerFP)
	}
	logf("隧道传输层：raw，长度分帧，无流控与重传")
	logf("! raw 路径无加密、无身份认证，仅用于对照实验")

	// 本地监听，每条 TCP 连接在隧道上开一个会话。
	ln, err := net.Listen("tcp", *local)
	if err != nil {
		return fmt.Errorf("本地监听失败：%w", err)
	}
	logf("结论：本地隧道已就绪，游戏内连接 %s", *local)

	// 整条隧道只跑一个读循环，cur 指向当前会话的本地连接。
	var (
		smu sync.Mutex
		cur net.Conn
	)
	closeCur := func() {
		smu.Lock()
		if cur != nil {
			_ = cur.Close()
			cur = nil
		}
		smu.Unlock()
	}
	go readLoop(conn,
		func() {},
		func(b []byte) {
			smu.Lock()
			c := cur
			smu.Unlock()
			if c != nil {
				_, _ = c.Write(b)
			}
		},
		closeCur,
	)

	for {
		tc, err := ln.Accept()
		if err != nil {
			return err
		}
		logf("[隧道] 收到本地连接 %s", tc.RemoteAddr())
		go func(tc net.Conn) {
			defer tc.Close()
			closeCur() // 单会话：新连接顶掉旧的
			smu.Lock()
			cur = tc
			smu.Unlock()
			if err := sendMsg(conn, msgOpen, nil); err != nil {
				return
			}
			buf := make([]byte, maxChunk)
			for {
				n, err := tc.Read(buf)
				if n > 0 {
					if e := sendMsg(conn, msgData, buf[:n]); e != nil {
						return
					}
				}
				if err != nil {
					_ = sendMsg(conn, msgClose, nil)
					smu.Lock()
					if cur == tc {
						cur = nil
					}
					smu.Unlock()
					return
				}
			}
		}(tc)
	}
}

// iceRemoteAddr 从选中的候选对取出对端地址，供 DTLS 使用。
func iceRemoteAddr(a *ice.Agent) net.Addr {
	if p, err := a.GetSelectedCandidatePair(); err == nil && p != nil {
		if ip := net.ParseIP(p.Remote.Address()); ip != nil {
			return &net.UDPAddr{IP: ip, Port: p.Remote.Port()}
		}
	}
	return &net.UDPAddr{}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":8090"
}

func cmdIce(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：mclbx ice host|guest [选项]")
	}
	switch args[0] {
	case "host":
		return cmdIceHost(args[1:])
	case "guest":
		return cmdIceGuest(args[1:])
	}
	return fmt.Errorf("ice 子命令仅支持 host 与 guest")
}
