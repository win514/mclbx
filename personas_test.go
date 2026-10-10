package main

// personas_test.go 房间参与者行为约定（文案与纯函数）断言。

import (
	"strings"
	"testing"
	"time"
)

// 好-1：玩家侧留空房间码必须当场报错，不得退化成静默随机。
func TestJoinWithoutRoomCodeIsRejected(t *testing.T) {
	err := cmdJoin([]string{"--host", "[2001:db8::1]:8090"})
	if err == nil {
		t.Fatal("没有房间码时 cmdJoin 应当直接报错，而不是随机生成一个再连")
	}
	for _, want := range []string{"房间码", "--room"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误里应当提到 %q，实际：%v", want, err)
		}
	}

	// 反方向：房主侧留空仍应随机生成
	if _, err := normalizeRoomCode(""); err != nil {
		t.Errorf("房主侧留空应当仍然随机生成一个，实际报错：%v", err)
	}
}

// 穷-5：界面填的是「秒」，而 --wait 是 flag.Duration，只认带单位的写法。
func TestDurationArg(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"  ":    "",
		"120":   "120s",
		" 60 ":  "60s",
		"2m":    "2m",
		"1m30s": "1m30s",
	}
	for in, want := range cases {
		if got := durationArg(in); got != want {
			t.Errorf("durationArg(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 好-4：界面填的是端口，而 --local 要的是监听地址。
func TestListenArg(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"25566":               "127.0.0.1:25566",
		" 25566 ":             "127.0.0.1:25566",
		"127.0.0.1:25565":     "127.0.0.1:25565",
		"[2001:db8::1]:25565": "[2001:db8::1]:25565",
		"0.0.0.0:25565":       "0.0.0.0:25565",
	}
	for in, want := range cases {
		if got := listenArg(in); got != want {
			t.Errorf("listenArg(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 好-5：Java 版离线模式玩家名只允许 [A-Za-z0-9_]，中文名会被判为名单外。
func TestNonASCIIAllowNames(t *testing.T) {
	got := nonASCIIAllowNames([]string{"Steve", "小明", "Alex_99", "小红"})
	if strings.Join(got, ",") != "小明,小红" {
		t.Errorf("应当只挑出非 ASCII 的名字，实际 %v", got)
	}
	if len(nonASCIIAllowNames([]string{"Steve", "Alex"})) != 0 {
		t.Error("全是 ASCII 时不该有输出 —— 否则正常用户也会看到一句莫名其妙的提醒")
	}
	if nonASCIIAllowNames(nil) != nil {
		t.Error("空名单应当返回空")
	}
}

func TestAllowListNote(t *testing.T) {
	if note := allowListNote([]string{"Steve"}); note != "" {
		t.Errorf("正常名单不该有提醒，实际：%s", note)
	}
	note := allowListNote([]string{"Steve", "小明"})
	for _, want := range []string{"小明", "英文 ID"} {
		if !strings.Contains(note, want) {
			t.Errorf("提醒里应当提到 %q，实际：%s", want, note)
		}
	}
	if strings.Contains(note, "Steve") {
		t.Errorf("提醒里不该点名正常的名字，实际：%s", note)
	}
}

// 好-2：口令是房间码还是地址本身，决定邀请文本写法。
func TestGateModeValue(t *testing.T) {
	if got := gateModeValue(true); got != "room" {
		t.Errorf("房间码作口令时应当是 room，实际 %q", got)
	}
	if got := gateModeValue(false); got != "addr" {
		t.Errorf("地址作口令时应当是 addr，实际 %q", got)
	}
}

// 穷-4：只有本机候选时，需说明用过的 STUN 服务器。
func TestHasServerReflexive(t *testing.T) {
	host := "candidate:1 1 udp 2130706431 192.168.1.9 50000 typ host"
	srflx := "candidate:2 1 udp 1694498815 203.0.113.9 51234 typ srflx raddr 192.168.1.9 rport 50000"
	if hasServerReflexive([]string{host}) {
		t.Error("只有 host 候选时不该判成有外网映射")
	}
	if !hasServerReflexive([]string{host, srflx}) {
		t.Error("有 srflx 候选时应当判成有外网映射")
	}
	if hasServerReflexive(nil) {
		t.Error("没有候选时不该判成有外网映射")
	}
}

func TestStunTroubleHintNamesTheServers(t *testing.T) {
	hint := stunTroubleHint()
	if !strings.Contains(hint, "MCLBX_STUN") {
		t.Errorf("提示里要给出换服务器的办法，实际：%s", hint)
	}
	// 必须是建链实际使用的 stunURLs，而非体检用的那一份。
	for _, s := range stunURLs() {
		host := strings.TrimPrefix(s, "stun:")
		if !strings.Contains(hint, host) {
			t.Errorf("提示里应当列出建链实际配置的 %s，实际：%s", host, hint)
		}
	}
}

// 好-3：原生界面把 ##GUESTS## 压成一行日志，字段名 c/m/s 是界面与 reconnect.go 的契约。
func TestRosterLine(t *testing.T) {
	line := rosterLine(`{"n":2,"who":[{"c":"1DAF-F589","m":"直连","s":125},{"c":"AAAA-0001","m":"中继","s":7200}]}`)
	for _, want := range []string{"2 人", "1DAF-F589", "直连", "2 分", "AAAA-0001", "中继", "2 时 00 分"} {
		if !strings.Contains(line, want) {
			t.Errorf("名单那一行里应当含 %q，实际：%s", want, line)
		}
	}

	empty := rosterLine(`{"n":0,"who":[]}`)
	if !strings.Contains(empty, "0 人") {
		t.Errorf("空名单也要说一句，实际：%s", empty)
	}

	// 非法名单不应输出任何内容
	if got := rosterLine("not json"); got != "" {
		t.Errorf("格式不对时应当返回空串，实际 %q", got)
	}
}

func TestHumanSince(t *testing.T) {
	cases := map[int64]string{
		-5:   "0 秒",
		0:    "0 秒",
		59:   "59 秒",
		60:   "1 分",
		3599: "59 分",
		3600: "1 时 00 分",
		7260: "2 时 01 分",
	}
	for in, want := range cases {
		if got := humanSince(in); got != want {
			t.Errorf("humanSince(%d) = %q，期望 %q", in, got, want)
		}
	}
}

// 坏-2：信箱满必须提示，且需限速（30 秒内只记账）。
func TestMailboxFullNoteIsRateLimited(t *testing.T) {
	mb := newMailbox()
	now := time.Now()

	mb.noteFullLocked(now)
	if mb.fullCount != 0 {
		t.Errorf("说过一次之后应当把计数清零，实际 %d", mb.fullCount)
	}
	if mb.fullLogged.IsZero() {
		t.Error("应当记下说过话的时刻")
	}

	// 30 秒内：只记账
	mb.noteFullLocked(now.Add(time.Second))
	mb.noteFullLocked(now.Add(29 * time.Second))
	if mb.fullCount != 2 {
		t.Errorf("30 秒内不该重复说话，应当只记账，实际计数 %d", mb.fullCount)
	}

	// 超过 30 秒：再说一次并清零
	mb.noteFullLocked(now.Add(31 * time.Second))
	if mb.fullCount != 0 {
		t.Errorf("超过 30 秒后应当再说一次并清零，实际 %d", mb.fullCount)
	}
}
