package main

// relaylink.go —— 把各种中继部署形态统一解析成 relayPlan：turn:/turns:/tcp:/网址/主机:端口/mclbx:// 链接。

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TURN REST 凭据规定使用 HMAC-SHA1
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

type relayHopKind string

const (
	// hopAuto 表示未指明类型：ICE 侧按 TURN 试，兜底侧按 TCP 中继试。
	hopAuto  relayHopKind = "auto"
	hopTurn  relayHopKind = "turn"
	hopTurns relayHopKind = "turns"
	hopTCP   relayHopKind = "tcp"
)

// relayHop 是中继的一个具体落点
type relayHop struct {
	Kind relayHopKind
	Host string
	Port int

	User string // 静态账号
	Pass string

	Secret   string // TURN REST 共享密钥（即 coturn 的 static-auth-secret）
	RESTUser string // REST 时间戳后跟的用户名，可空

	Raw string // 用户原文，报错回显时用
}

func (h relayHop) addr() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }

// escCred 转义账号/密码中的分隔符；TURN REST 凭据（过期时间:用户名）自带冒号，必须转。
func escCred(s string) string {
	s = strings.ReplaceAll(s, "%", "%25") // 先处理百分号，避免二次转义
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, "@", "%40")
	return s
}

// canonicalLink 生成可直接粘贴的单行链接（不直接回显用户原文）。
func (h relayHop) canonicalLink() string {
	scheme := "turn"
	if h.Kind == hopTurns {
		scheme = "turns"
	}
	switch h.Kind {
	case hopTCP:
		return "tcp:" + h.addr()
	case hopTurn, hopTurns:
		switch {
		case h.Secret != "":
			return fmt.Sprintf("%s:%s?secret=%s", scheme, h.addr(), escCred(h.Secret))
		case h.User != "":
			return fmt.Sprintf("%s:%s:%s@%s", scheme, escCred(h.User), escCred(h.Pass), h.addr())
		}
		return scheme + ":" + h.addr()
	default:
		return h.addr()
	}
}

// linkAs 按指定类型生成单行链接，用于自动识别出类型后的回显。
func (h relayHop) linkAs(kind relayHopKind) string {
	c := h
	c.Kind = kind
	return c.canonicalLink()
}

func (h relayHop) String() string {
	switch h.Kind {
	case hopTCP:
		return "tcp:" + h.addr()
	case hopTurns:
		return "turns:" + h.addr()
	default:
		return h.addr()
	}
}

// relayPlan 描述如何使用这台中继。
type relayPlan struct {
	Raw  string
	Hops []relayHop

	SignalURL string // 链接里可能捎带信令会合点
	Room      string // 也可能捎带房间码

	Notes []string // 需要提醒用户的事（例如"这是自动判断的"）
}

// TurnURLs 将方案转换为 ICE 需要的中继 URL；每次调用重新计算 REST 凭据。
func (p *relayPlan) TurnURLs(user, pass string) ([]*ice.URL, []error) {
	if p == nil {
		return nil, nil
	}
	var out []*ice.URL
	var errs []error
	for _, h := range p.Hops {
		if h.Kind == hopTCP {
			continue // TCP 中继不是 TURN，走另一条路
		}
		scheme := "turn"
		if h.Kind == hopTurns {
			scheme = "turns"
		}
		u, err := stun.ParseURI(scheme + ":" + h.addr())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", h.Raw, err))
			continue
		}
		switch {
		case h.Secret != "":
			u.Username, u.Password = restCredentials(h.Secret, h.RESTUser, turnRESTTTL)
		case h.User != "":
			u.Username, u.Password = h.User, h.Pass
		default:
			u.Username, u.Password = user, pass
		}
		if u.Username == "" {
			errs = append(errs, fmt.Errorf("%s：缺少账号。可写 turn:用户:密码@主机:端口，或加 --relay-user/--relay-pass；REST 方式写 ?secret=密钥", h.Raw))
			continue
		}
		u.Proto = ice.ProtoTypeUDP
		out = append(out, u)
	}
	return out, errs
}

// TCPAddr 取 TCP 中继的地址（第一个 tcp:/auto 的落点）；没有就返回空
func (p *relayPlan) TCPAddr() string {
	if p == nil {
		return ""
	}
	for _, h := range p.Hops {
		if h.Kind == hopTCP || h.Kind == hopAuto {
			return h.addr()
		}
	}
	return ""
}

func (p *relayPlan) hasTurn() bool {
	if p == nil {
		return false
	}
	for _, h := range p.Hops {
		if h.Kind != hopTCP {
			return true
		}
	}
	return false
}

// describe 返回日志用的一句话说明。
func (p *relayPlan) describe() string {
	if p == nil || len(p.Hops) == 0 {
		return "未配置"
	}
	var parts []string
	for _, h := range p.Hops {
		tag := ""
		switch h.Kind {
		case hopAuto:
			tag = "未指明类型"
		case hopTurn:
			tag = "TURN"
			if h.Secret != "" {
				tag = "TURN·REST临时凭据"
			} else if h.User != "" {
				tag = "TURN·账号已提供"
			} else {
				tag = "TURN·账号待提供"
			}
		case hopTurns:
			tag = "TURN over TLS"
		case hopTCP:
			tag = "TCP 中继"
		}
		parts = append(parts, fmt.Sprintf("%s，%s", h.String(), tag))
	}
	s := strings.Join(parts, " + ")
	if p.SignalURL != "" {
		s += "，信令 " + p.SignalURL
	}
	if p.Room != "" {
		s += "，房间码 " + p.Room
	}
	return s
}

// turnRESTTTL 为本地生成的 REST 凭据有效期。
var turnRESTTTL = time.Hour

// restCredentials 按 draft-uberti-behave-turn-rest-00 计算临时凭据：username = "<时间戳>:<用户名>"，password = base64(HMAC-SHA1(secret, username))；username 须为将来时刻。
func restCredentials(secret, user string, ttl time.Duration) (string, string) {
	if strings.TrimSpace(user) == "" {
		user = "mclbx"
	}
	name := fmt.Sprintf("%d:%s", time.Now().Add(ttl).Unix(), user)
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(name))
	return name, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// defaultPortFor 给省略了端口的写法兜一个默认值
func defaultPortFor(kind relayHopKind) int {
	switch kind {
	case hopTurns:
		return 5349
	case hopTCP:
		return 8090
	default:
		return 3478
	}
}

// splitQuery 把 "主机:端口?k=v&k2=v2" 拆成两半（没有查询串时 query 为空）
func splitQuery(s string) (string, url.Values, error) {
	base, raw, found := strings.Cut(s, "?")
	if !found {
		return base, url.Values{}, nil
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", nil, fmt.Errorf("查询串格式错误：%w", err)
	}
	return base, q, nil
}

// hostPort 解析主机与端口，缺端口时补默认值；IPv6 字面量须带方括号。
func hostPort(s string, def int) (string, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("未填写主机名")
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		// 没有端口，或者 IPv6 忘了写方括号
		if strings.Count(s, ":") > 1 && !strings.HasPrefix(s, "[") {
			return "", 0, fmt.Errorf("IPv6 地址须写成 [地址]:端口，%q 缺少方括号", s)
		}
		host, portStr = s, ""
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return "", 0, fmt.Errorf("未填写主机名")
	}
	if portStr == "" {
		return host, def, nil
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("端口不合法：%q", portStr)
	}
	return host, p, nil
}

// parseMclbxLink 解析 mclbx://[账号:密码@]主机[:端口]?turn=&turns=&tcp=&sig=&room=&user=&pass=&secret=（各部分可选，sig= 为信令端口）。
func parseMclbxLink(raw string) (*relayPlan, error) {
	const prefix = "mclbx://"
	// 长度检查不可省（否则短串切片 panic）；前缀大小写不敏感。
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return nil, fmt.Errorf("%q：这不是 mclbx:// 链接", raw)
	}
	rest := raw[len(prefix):]
	body, q, err := splitQuery(rest)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", raw, err)
	}
	p := &relayPlan{Raw: raw}

	user, pass := "", ""
	if at := strings.LastIndex(body, "@"); at >= 0 {
		creds := body[:at]
		body = body[at+1:]
		if c := strings.Index(creds, ":"); c >= 0 {
			user, pass = decodeMaybe(creds[:c]), decodeMaybe(creds[c+1:])
		} else {
			user = decodeMaybe(creds)
		}
	}
	host, hostPortNum, err := hostPort(body, 0)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", raw, err)
	}
	if u := strings.TrimSpace(q.Get("user")); u != "" {
		user = u
	}
	if v := strings.TrimSpace(q.Get("pass")); v != "" {
		pass = v
	}
	secret := strings.TrimSpace(q.Get("secret"))
	restUser := strings.TrimSpace(q.Get("name"))

	numeric := func(key string, def int) (int, bool, error) {
		v := strings.TrimSpace(q.Get(key))
		if v == "" {
			return def, false, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return 0, false, fmt.Errorf("%s= 后面的端口不合法：%q", key, v)
		}
		return n, true, nil
	}

	turnPort, hasTurn, err := numeric("turn", 0)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", raw, err)
	}
	turnsPort, hasTurns, err := numeric("turns", 0)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", raw, err)
	}
	tcpPort, hasTCP, err := numeric("tcp", 0)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", raw, err)
	}

	add := func(kind relayHopKind, port int) {
		p.Hops = append(p.Hops, relayHop{
			Kind: kind, Host: host, Port: port,
			User: user, Pass: pass, Secret: secret, RESTUser: restUser, Raw: raw,
		})
	}
	// TURN 落点先于 TCP：写了 turn=/turns= 则忽略主机端口，否则主机端口用作 TURN 端口（未带补默认）。
	authorityTurnPort := 0
	switch {
	case hasTurn || hasTurns:
	case hostPortNum != 0:
		authorityTurnPort = hostPortNum
	case !hasTCP:
		authorityTurnPort = defaultPortFor(hopTurn)
	}
	if hasTurns {
		add(hopTurns, turnsPort)
	}
	if hasTurn {
		add(hopTurn, turnPort)
	}
	if authorityTurnPort != 0 {
		add(hopTurn, authorityTurnPort)
	}
	if hasTCP {
		add(hopTCP, tcpPort)
	}

	if sig := strings.TrimSpace(q.Get("sig")); sig != "" {
		port, err := strconv.Atoi(sig)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%s：sig= 后面的端口不合法：%q", raw, sig)
		}
		p.SignalURL = "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	}
	p.Room = strings.TrimSpace(q.Get("room"))
	return p, nil
}

// parseRelayEntry 解析输入中的一段，可能返回多个落点（仅 mclbx://）。
func parseRelayEntry(raw string) (*relayPlan, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	lower := strings.ToLower(s)

	switch {
	case strings.HasPrefix(lower, "mclbx://"):
		return parseMclbxLink(s)

	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
		// 网址形式：仅取主机名，端口按 TCP 中继默认值。
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return nil, fmt.Errorf("%s：不是可用的网址", s)
		}
		port := 8090
		if ps := u.Port(); ps != "" {
			p, err := strconv.Atoi(ps)
			if err != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("%s：端口不合法", s)
			}
			port = p
		}
		return &relayPlan{
			Raw:  raw,
			Hops: []relayHop{{Kind: hopAuto, Host: u.Hostname(), Port: port, Raw: raw}},
			Notes: []string{
				"输入为网址，已仅提取主机名和端口；若为 TURN，请按 turn:主机:端口 填写并提供账号",
			},
		}, nil

	case strings.HasPrefix(lower, "tcp:"):
		host, port, err := hostPort(s[len("tcp:"):], defaultPortFor(hopTCP))
		if err != nil {
			return nil, fmt.Errorf("%s：%w", s, err)
		}
		return &relayPlan{Raw: raw, Hops: []relayHop{{Kind: hopTCP, Host: host, Port: port, Raw: raw}}}, nil

	case strings.HasPrefix(lower, "turn:"), strings.HasPrefix(lower, "turns:"):
		scheme, hostPortStr, user, pass, err := parseTurnEntry(s)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", s, err)
		}
		kind := hopTurn
		if scheme == "turns" {
			kind = hopTurns
		}
		base, q, err := splitQuery(hostPortStr)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", s, err)
		}
		host, port, err := hostPort(base, defaultPortFor(kind))
		if err != nil {
			return nil, fmt.Errorf("%s：%w", s, err)
		}
		hop := relayHop{Kind: kind, Host: host, Port: port, User: user, Pass: pass, Raw: raw}
		if q != nil {
			hop.Secret = strings.TrimSpace(q.Get("secret"))
			hop.RESTUser = strings.TrimSpace(q.Get("name"))
			if u := strings.TrimSpace(q.Get("user")); u != "" && hop.User == "" {
				hop.User = u
			}
			if v := strings.TrimSpace(q.Get("pass")); v != "" && hop.Pass == "" {
				hop.Pass = v
			}
		}
		return &relayPlan{Raw: raw, Hops: []relayHop{hop}}, nil

	default:
		// 裸 主机[:端口]：类型未知，两端都保留；须带端口或本身为 IP，避免把任意词当成主机名。
		if !strings.Contains(s, ":") && net.ParseIP(s) == nil {
			return nil, fmt.Errorf("%s：无法识别的写法。请写 主机:端口，或用 turn: / turns: / tcp: / mclbx:// 开头", s)
		}
		host, port, err := hostPort(s, 0)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", s, err)
		}
		if port == 0 {
			port = defaultPortFor(hopTurn)
		}
		return &relayPlan{
			Raw:  raw,
			Hops: []relayHop{{Kind: hopAuto, Host: host, Port: port, Raw: raw}},
			Notes: []string{
				"此行未指明类型，先按 TURN 使用；若非 TURN，可用 mclbx relaycheck 校验后按提示改为 turn: 或 tcp: 开头",
			},
		}, nil
	}
}

// parseRelayLink 解析整串配置（逗号/空格/换行分隔）：正确行保留，错误行单独返回；无任何行成功时由调用方硬报错。
func parseRelayLink(raw string) (*relayPlan, []error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	plan := &relayPlan{Raw: raw}
	var errs []error
	seen := map[string]bool{}
	for _, f := range fields {
		if strings.TrimSpace(f) == "" {
			continue
		}
		sub, err := parseRelayEntry(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if sub == nil {
			continue
		}
		for _, h := range sub.Hops {
			key := string(h.Kind) + "|" + h.addr()
			if seen[key] {
				continue
			}
			seen[key] = true
			plan.Hops = append(plan.Hops, h)
		}
		if plan.SignalURL == "" {
			plan.SignalURL = sub.SignalURL
		}
		if plan.Room == "" {
			plan.Room = sub.Room
		}
		plan.Notes = append(plan.Notes, sub.Notes...)
	}
	return plan, errs
}

// relayLinkFlag 由各命令的 --relay-server 参数写入
var relayLinkFlag string

// relayUserFlag / relayPassFlag 是账号外置时的来源（--relay-user / --relay-pass）
var relayUserFlag, relayPassFlag string

// activeRelayPlan 汇总本次使用的中继：--relay-server > MCLBX_RELAY > MCLBX_TURN。
func activeRelayPlan() (*relayPlan, error) {
	raw := strings.TrimSpace(relayLinkFlag)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("MCLBX_RELAY"))
	}
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("MCLBX_TURN"))
	}
	if raw == "" {
		return nil, nil
	}
	return resolveRelayLink(raw)
}

// resolveRelayLink 为 activeRelayPlan 的纯函数部分（输入字符串，输出方案）。
func resolveRelayLink(raw string) (*relayPlan, error) {
	plan, errs := parseRelayLink(raw)

	// 账号来源：命令行 > 环境变量；链接里已经写了的优先
	user := strings.TrimSpace(relayUserFlag)
	if user == "" {
		user = strings.TrimSpace(os.Getenv("MCLBX_TURN_USER"))
	}
	pass := strings.TrimSpace(relayPassFlag)
	if pass == "" {
		pass = strings.TrimSpace(os.Getenv("MCLBX_TURN_PASS"))
	}
	for i := range plan.Hops {
		h := &plan.Hops[i]
		if h.User == "" && h.Secret == "" {
			h.User, h.Pass = user, pass
		}
	}

	if len(plan.Hops) == 0 {
		var reason strings.Builder
		for i, e := range errs {
			if i > 0 {
				reason.WriteString("；")
			}
			reason.WriteString(e.Error())
		}
		if reason.Len() == 0 {
			return nil, fmt.Errorf("中继服务器配置有误：%q 中未解析出任何地址", raw)
		}
		return nil, fmt.Errorf("中继服务器配置有误，无可用条目：%s", reason.String())
	}

	// 有可用行也有错误行：保留可用行，错误作为提示输出
	for _, e := range errs {
		plan.Notes = append(plan.Notes, fmt.Sprintf("无法识别的条目，已跳过：%v", e))
	}
	return plan, nil
}

// applyRelayFlag 把命令行的 --relay-server / --relay-user / --relay-pass 记下来
func applyRelayFlag(link, user, pass string) {
	relayLinkFlag = strings.TrimSpace(link)
	relayUserFlag = strings.TrimSpace(user)
	relayPassFlag = strings.TrimSpace(pass)
}

// udpNetworkAndHost 按目标地址选择 UDP 协议族与绑定地址（IPv6 用 udp6/::，否则 udp4/0.0.0.0）。
func udpNetworkAndHost(addr string) (network, host string) {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		h = addr
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil && ip.To4() == nil {
		return "udp6", "::"
	}
	return "udp4", "0.0.0.0"
}

// resolveTCPRelayAddr 决定 TCP 兜底通道的地址：--relay > --relay-server 链接 > 环境变量。
func resolveTCPRelayAddr(relayServer, relayAddr string) (string, error) {
	if s := strings.TrimSpace(relayAddr); s != "" {
		return s, nil // 老写法优先，行为不变
	}
	if s := strings.TrimSpace(relayServer); s != "" {
		plan, err := resolveRelayLink(s)
		if err != nil {
			return "", err
		}
		if a := plan.TCPAddr(); a != "" {
			return a, nil
		}
		return "", fmt.Errorf("这条中继链接中没有 TCP 中继地址 %s —— 中继转发无法使用它，"+
			"请在链接中加 tcp:主机:端口，或单独运行一台 mclbx relay", plan.describe())
	}
	// 都未给：读取环境变量配置
	plan, err := activeRelayPlan()
	if err != nil {
		return "", err
	}
	if plan != nil {
		if a := plan.TCPAddr(); a != "" {
			return a, nil
		}
	}
	return "", fmt.Errorf("需要指定 TCP 中继：使用 --relay 主机:端口，或 --relay-server tcp:主机:端口")
}

// logRelayPlan 把生效的中继配置打出来。
func logRelayPlan(plan *relayPlan) {
	if plan == nil {
		return
	}
	logf("中继服务器：%s", plan.describe())
	for _, n := range plan.Notes {
		logf("  提示：%s", n)
	}
}

// relayLogOnce 保证中继配置只打印一次。
var relayLogOnce sync.Once

func logRelayPlanOnce(plan *relayPlan) {
	relayLogOnce.Do(func() { logRelayPlan(plan) })
}
