package main

// relaylink_test.go 测试中继服务器链接的解析与翻译。

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // 按 TURN REST 的算法复算，属协议规定
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

// resetRelayFlags 清掉命令行的中继参数（包级变量）。
func resetRelayFlags(t *testing.T) {
	t.Helper()
	l, u, p := relayLinkFlag, relayUserFlag, relayPassFlag
	relayLinkFlag, relayUserFlag, relayPassFlag = "", "", ""
	t.Cleanup(func() { relayLinkFlag, relayUserFlag, relayPassFlag = l, u, p })
}

// clearRelayEnv 清掉环境里残留的中继配置。
func clearRelayEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MCLBX_RELAY", "")
	t.Setenv("MCLBX_TURN", "")
	t.Setenv("MCLBX_TURN_USER", "")
	t.Setenv("MCLBX_TURN_PASS", "")
}

func TestParseRelayLinkForms(t *testing.T) {
	cases := []struct {
		in   string
		kind relayHopKind
		host string
		port int
		user string
		pass string
	}{
		// 账号写在链接里的 TURN。
		{"turn:mclbx:mclbx-lab@1.2.3.4:3478", hopTurn, "1.2.3.4", 3478, "mclbx", "mclbx-lab"},
		// 标准 TURN：只给地址。
		{"turn:1.2.3.4:3478", hopTurn, "1.2.3.4", 3478, "", ""},
		// 省略端口按默认端口算。
		{"turn:turn.example.com", hopTurn, "turn.example.com", 3478, "", ""},
		// TURN over TLS
		{"turns:u:p@1.2.3.4:5349", hopTurns, "1.2.3.4", 5349, "u", "p"},
		// 带 transport 查询串。
		{"turn:1.2.3.4:3478?transport=udp", hopTurn, "1.2.3.4", 3478, "", ""},
		// coturn 的 REST 临时凭据
		{"turn:1.2.3.4:3478?secret=SHARED&name=alice", hopTurn, "1.2.3.4", 3478, "", ""},
		// 本工具的 TCP 中继
		{"tcp:1.2.3.4:8090", hopTCP, "1.2.3.4", 8090, "", ""},
		{"tcp:relay.example.com", hopTCP, "relay.example.com", 8090, "", ""},
		// IPv6 字面量
		{"turn:[2001:db8:1111::1]:3478", hopTurn, "2001:db8:1111::1", 3478, "", ""},
		{"tcp:[2001:db8:1111::1]:8090", hopTCP, "2001:db8:1111::1", 8090, "", ""},
		// 网址形式：取主机名，端口按 8090 算。
		{"https://relay.example.com", hopAuto, "relay.example.com", 8090, "", ""},
		{"http://1.2.3.4:9000", hopAuto, "1.2.3.4", 9000, "", ""},
		// 裸的主机:端口，类型标成 auto。
		{"1.2.3.4:3478", hopAuto, "1.2.3.4", 3478, "", ""},
		{"relay.example.com:8090", hopAuto, "relay.example.com", 8090, "", ""},
	}
	for _, c := range cases {
		plan, err := parseRelayEntry(c.in)
		if err != nil {
			t.Errorf("%s：不该失败：%v", c.in, err)
			continue
		}
		if len(plan.Hops) != 1 {
			t.Errorf("%s：期望 1 个落点，实际 %d 个", c.in, len(plan.Hops))
			continue
		}
		h := plan.Hops[0]
		if h.Kind != c.kind || h.Host != c.host || h.Port != c.port || h.User != c.user || h.Pass != c.pass {
			t.Errorf("%s：得到 %s|%s:%d|%s:%s，期望 %s|%s:%d|%s:%s",
				c.in, h.Kind, h.Host, h.Port, h.User, h.Pass,
				c.kind, c.host, c.port, c.user, c.pass)
		}
	}
}

// 一条 mclbx:// 链接携带中继、信令与房间码。
func TestParseMclbxLinkCarriesEverything(t *testing.T) {
	plan, err := parseRelayEntry(
		"mclbx://u:p@1.2.3.4:3478?tcp=8090&sig=8090&room=abc123&secret=S")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(plan.Hops) != 2 {
		t.Fatalf("期望 TURN + TCP 两个落点，实际 %d 个：%+v", len(plan.Hops), plan.Hops)
	}
	if plan.Hops[0].Kind != hopTurn || plan.Hops[0].Port != 3478 {
		t.Errorf("第一个落点应当是 TURN:3478，实际 %+v", plan.Hops[0])
	}
	if plan.Hops[0].Secret != "S" {
		t.Errorf("REST 密钥没带上：%+v", plan.Hops[0])
	}
	if plan.Hops[1].Kind != hopTCP || plan.Hops[1].Port != 8090 {
		t.Errorf("第二个落点应当是 TCP:8090，实际 %+v", plan.Hops[1])
	}
	if plan.SignalURL != "http://1.2.3.4:8090" {
		t.Errorf("信令地址不对：%q", plan.SignalURL)
	}
	if plan.Room != "abc123" {
		t.Errorf("房间码不对：%q", plan.Room)
	}
	// 账号要落到每个落点上。
	for _, h := range plan.Hops {
		if h.User != "u" || h.Pass != "p" {
			t.Errorf("账号没传到落点上：%+v", h)
		}
	}
}

// 测试 mclbx:// 里主机后面端口的计算方法。
func TestParseMclbxLinkPortRules(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		// 主机带端口 + tcp=，两个落点都要在。
		{"mclbx://1.2.3.4:3478?tcp=8090", []string{"turn:1.2.3.4:3478", "tcp:1.2.3.4:8090"}},
		{"mclbx://1.2.3.4:3478", []string{"turn:1.2.3.4:3478"}},
		// 不写端口也补默认 TURN 端口。
		{"mclbx://1.2.3.4", []string{"turn:1.2.3.4:3478"}},
		// 只声明 tcp= 时不发明 TURN 端口。
		{"mclbx://1.2.3.4?tcp=8090", []string{"tcp:1.2.3.4:8090"}},
		// 显式 turn= 以其为准。
		{"mclbx://1.2.3.4?turn=3480", []string{"turn:1.2.3.4:3480"}},
		{"mclbx://1.2.3.4:3478?turn=3480", []string{"turn:1.2.3.4:3480"}},
		{"mclbx://1.2.3.4?turns=5349&tcp=8090", []string{"turns:1.2.3.4:5349", "tcp:1.2.3.4:8090"}},
	}
	for _, c := range cases {
		plan, err := parseRelayEntry(c.in)
		if err != nil {
			t.Errorf("%s：不该失败：%v", c.in, err)
			continue
		}
		var got []string
		for _, h := range plan.Hops {
			got = append(got, h.canonicalLink())
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s：得到 %v，期望 %v", c.in, got, c.want)
		}
	}
}

func TestParseRelayLinkRejectsGarbage(t *testing.T) {
	// 形似地址但含错字的输入必须报错。
	for _, bad := range []string{
		"nonsense",                   // 单个词，没有端口也不是 IP
		"turn",                       // 只有前缀
		"stun:1.2.3.4:3478",          // STUN 不是中继
		"turn:1.2.3.4:99999",         // 端口越界
		"turn::3478",                 // 没主机名
		"tcp:",                       // 没地址
		"turn:2001:db8:1111::1:3478", // IPv6 忘了方括号
	} {
		if _, err := parseRelayEntry(bad); err == nil {
			t.Errorf("%q 应当被拒绝，实际被接受了", bad)
		}
	}
}

// 三个中继第二个打错字：前两个保留，错的单独报出。
func TestRelayPlanKeepsGoodEntriesWhenOneIsBad(t *testing.T) {
	plan, errs := parseRelayLink("turn:u:p@a.example:3478, nonsense, turn:u:p@b.example:3478")
	if len(plan.Hops) != 2 {
		t.Fatalf("两条合法配置都该保留，实际 %d 条", len(plan.Hops))
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "nonsense") {
		t.Fatalf("非法那条应当被单独报出来，实际 errs=%v", errs)
	}
}

// 全错必须硬报错。
func TestRelayPlanAllBadIsHardError(t *testing.T) {
	resetRelayFlags(t)
	clearRelayEnv(t)
	for _, bad := range []string{"nonsense", "nonsense, also-nonsense"} {
		if _, err := resolveRelayLink(bad); err == nil {
			t.Errorf("%q 应当直接报错", bad)
		}
	}
}

func TestRelayPlanUnset(t *testing.T) {
	resetRelayFlags(t)
	clearRelayEnv(t)
	plan, err := activeRelayPlan()
	if plan != nil || err != nil {
		t.Fatalf("什么都没配时应当返回空：plan=%v err=%v", plan, err)
	}
}

// 账号优先级：命令行 > 环境变量 > 链接里没有
func TestRelayPlanAccountPrecedence(t *testing.T) {
	clearRelayEnv(t)

	// 1) 账号走环境变量
	resetRelayFlags(t)
	t.Setenv("MCLBX_TURN_USER", "1750000000:mclbx")
	t.Setenv("MCLBX_TURN_PASS", "secret==")
	plan, err := resolveRelayLink("turn:1.2.3.4:3478")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if plan.Hops[0].User != "1750000000:mclbx" || plan.Hops[0].Pass != "secret==" {
		t.Fatalf("环境变量里的账号没生效：%+v", plan.Hops[0])
	}

	// 2) 链接里的账号更优先
	plan, err = resolveRelayLink("turn:inline:inlinepass@1.2.3.4:3478")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if plan.Hops[0].User != "inline" || plan.Hops[0].Pass != "inlinepass" {
		t.Fatalf("链接里的账号应当优先于环境变量：%+v", plan.Hops[0])
	}

	// 3) 命令行参数更优先于环境变量
	relayUserFlag, relayPassFlag = "flaguser", "flagpass"
	plan, err = resolveRelayLink("turn:1.2.3.4:3478")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if plan.Hops[0].User != "flaguser" || plan.Hops[0].Pass != "flagpass" {
		t.Fatalf("命令行参数应当优先于环境变量：%+v", plan.Hops[0])
	}

	// 4) REST 密钥存在时不被静态账号覆盖。
	t.Setenv("MCLBX_TURN_USER", "should-not-win")
	plan, err = resolveRelayLink("turn:1.2.3.4:3478?secret=SHARED")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if plan.Hops[0].Secret != "SHARED" || plan.Hops[0].User != "" {
		t.Fatalf("REST 模式下不该被静态账号顶掉：%+v", plan.Hops[0])
	}
}

// 翻译成 ICE 需要的 URL：协议为 UDP，turns: 保留。
func TestRelayPlanTurnURLs(t *testing.T) {
	clearRelayEnv(t)
	resetRelayFlags(t)
	plan, err := resolveRelayLink("turn:u:p@1.2.3.4:3478, turns:u:p@1.2.3.4:5349, tcp:1.2.3.4:8090")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	urls, errs := plan.TurnURLs("", "")
	if len(errs) != 0 {
		t.Fatalf("不该有错误：%v", errs)
	}
	// TCP 落点不出现在 TURN 列表里。
	if len(urls) != 2 {
		t.Fatalf("期望 2 个 TURN 地址（TCP 那条不算），实际 %d 个", len(urls))
	}
	for _, u := range urls {
		if u.Proto != 1 { // ice.ProtoTypeUDP
			t.Errorf("协议应当是 UDP，实际 %v", u.Proto)
		}
		if u.Username != "u" || u.Password != "p" {
			t.Errorf("账号没挂上去：%+v", u)
		}
	}
	if urls[0].Scheme.String() != "turn" || urls[1].Scheme.String() != "turns" {
		t.Errorf("scheme 不对：%v / %v", urls[0].Scheme, urls[1].Scheme)
	}
	if got := plan.TCPAddr(); got != "1.2.3.4:8090" {
		t.Errorf("TCP 中继地址不对：%q", got)
	}
}

// 没账号又没 REST 密钥时必须明确报出。
func TestRelayPlanMissingAccount(t *testing.T) {
	clearRelayEnv(t)
	resetRelayFlags(t)
	plan, err := resolveRelayLink("turn:1.2.3.4:3478")
	if err != nil {
		t.Fatalf("解析阶段不该失败（账号缺失在翻译成 URL 时才该报）：%v", err)
	}
	_, errs := plan.TurnURLs("", "")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "缺少账号") {
		t.Fatalf("应当报「缺少账号」，实际 errs=%v", errs)
	}
}

// TURN REST 临时凭据：格式须正确，时间戳须为将来。
func TestRestCredentials(t *testing.T) {
	const secret = "labsecret"
	name, pass := restCredentials(secret, "alice", time.Hour)

	tsPart, userPart, ok := strings.Cut(name, ":")
	if !ok {
		t.Fatalf("用户名应当形如 <时间戳>:<用户>，实际 %q", name)
	}
	ts, err := strconv.ParseInt(tsPart, 10, 64)
	if err != nil {
		t.Fatalf("时间戳部分不是整数：%q", tsPart)
	}
	if ts <= time.Now().Unix() {
		t.Errorf("时间戳必须是将来（中继会校验时间窗）：%d", ts)
	}
	if userPart != "alice" {
		t.Errorf("用户名部分不对：%q", userPart)
	}

	// 密码 = base64(HMAC-SHA1(密钥, 完整用户名))，取原始摘要的 base64。
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(name))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if pass != want {
		t.Errorf("密码算错了：得到 %q，期望 %q", pass, want)
	}
	if raw, err := base64.StdEncoding.DecodeString(pass); err != nil || len(raw) != sha1.Size {
		t.Errorf("密码应当是一段 %d 字节摘要的 base64：err=%v len=%d", sha1.Size, err, len(raw))
	}

	// 用户名留空时也能算。
	if n2, _ := restCredentials(secret, "", time.Hour); !strings.HasSuffix(n2, ":mclbx") {
		t.Errorf("用户名留空时应当兜一个默认值，实际 %q", n2)
	}
}

func TestRelayPlanDescribe(t *testing.T) {
	plan, err := parseRelayLink("turn:mclbx:mclbx-lab@1.2.3.4:3478, tcp:1.2.3.4:8090")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	s := plan.describe()
	for _, want := range []string{"1.2.3.4:3478", "TURN", "tcp:1.2.3.4:8090", "TCP 中继"} {
		if !strings.Contains(s, want) {
			t.Errorf("说明里应当包含 %q，实际 %q", want, s)
		}
	}
}

// TCP 兜底通道选哪台中继：--relay 优先，其次链接，最后环境变量。
func TestResolveTCPRelayAddr(t *testing.T) {
	resetRelayFlags(t)
	clearRelayEnv(t)

	// 直接给地址，原样返回。
	if a, err := resolveTCPRelayAddr("", "1.2.3.4:8090"); err != nil || a != "1.2.3.4:8090" {
		t.Errorf("直接给地址时应当原样返回：%q %v", a, err)
	}
	// 从链接里取
	if a, err := resolveTCPRelayAddr("tcp:5.6.7.8:9090", ""); err != nil || a != "5.6.7.8:9090" {
		t.Errorf("应当能从链接里取出 TCP 地址：%q %v", a, err)
	}
	// 裸地址（auto）也能当 TCP 中继用。
	if a, err := resolveTCPRelayAddr("5.6.7.8:9090", ""); err != nil || a != "5.6.7.8:9090" {
		t.Errorf("裸地址应当也能当 TCP 中继用：%q %v", a, err)
	}
	// 只给 TURN 链接时必须报错。
	if _, err := resolveTCPRelayAddr("turn:u:p@a.example:3478", ""); err == nil {
		t.Error("链接里没有 TCP 中继时应当报错")
	}
	// 环境变量兜底
	t.Setenv("MCLBX_RELAY", "tcp:9.9.9.9:8090")
	if a, err := resolveTCPRelayAddr("", ""); err != nil || a != "9.9.9.9:8090" {
		t.Errorf("应当能从环境变量取到：%q %v", a, err)
	}
}

// 测试 hop 生成链接（供 relaycheck 结论直接粘回）。
func TestRelayHopCanonicalLink(t *testing.T) {
	cases := []struct {
		hop  relayHop
		want string
	}{
		{relayHop{Kind: hopTurn, Host: "1.2.3.4", Port: 3478, User: "u", Pass: "p"}, "turn:u:p@1.2.3.4:3478"},
		{relayHop{Kind: hopTurns, Host: "1.2.3.4", Port: 5349, User: "u", Pass: "p"}, "turns:u:p@1.2.3.4:5349"},
		{relayHop{Kind: hopTurn, Host: "1.2.3.4", Port: 3478, Secret: "S"}, "turn:1.2.3.4:3478?secret=S"},
		{relayHop{Kind: hopTCP, Host: "1.2.3.4", Port: 8090}, "tcp:1.2.3.4:8090"},
		{relayHop{Kind: hopTurn, Host: "1.2.3.4", Port: 3478}, "turn:1.2.3.4:3478"},
	}
	for _, c := range cases {
		if got := c.hop.canonicalLink(); got != c.want {
			t.Errorf("得到 %q，期望 %q", got, c.want)
		}
	}

	// 账号里的冒号必须转义，否则会被当成账号:密码分界。
	h := relayHop{Kind: hopTurn, Host: "1.2.3.4", Port: 3478, User: "1750000000:mclbx", Pass: "a@b"}
	link := h.canonicalLink()
	if link != "turn:1750000000%3Amclbx:a%40b@1.2.3.4:3478" {
		t.Errorf("转义不对：%q", link)
	}
	// 生成的链接必须能被自己解析回原样。
	plan, err := parseRelayEntry(link)
	if err != nil {
		t.Fatalf("自己生成的链接应当能解析：%v", err)
	}
	if plan.Hops[0].User != "1750000000:mclbx" || plan.Hops[0].Pass != "a@b" {
		t.Errorf("往返一趟之后账号变了：%+v", plan.Hops[0])
	}

	// 识别出类型后返回带前缀的链接。
	auto := relayHop{Kind: hopAuto, Host: "1.2.3.4", Port: 8090}
	if got := auto.linkAs(hopTCP); got != "tcp:1.2.3.4:8090" {
		t.Errorf("linkAs 没带上前缀：%q", got)
	}
	if got := auto.linkAs(hopTurn); got != "turn:1.2.3.4:8090" {
		t.Errorf("linkAs 没带上前缀：%q", got)
	}
}
