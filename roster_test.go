package main

// roster_test.go 覆盖房主侧在场玩家名单：未校验不入榜、迟到 markConnected、重连清空三处时序。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRosterOnlyShowsVerifiedGuests(t *testing.T) {
	g := newGuestRegistry()
	const key = "guest-a"

	if got := g.claim(key, 1); got != claimAccepted {
		t.Fatalf("认领应当成功，实际 %v", got)
	}
	if n := len(g.roster()); n != 0 {
		t.Fatalf("未经身份校验的连接不该出现在名单里，实际 %d 条", n)
	}

	g.markConnected(key, "A1B2-C3D4", "直连")
	list := g.roster()
	if len(list) != 1 {
		t.Fatalf("校验通过后应当有 1 条，实际 %d 条", len(list))
	}
	if list[0].Code != "A1B2-C3D4" || list[0].Mode != "直连" {
		t.Fatalf("名单内容不对：%+v", list[0])
	}

	g.markGone(key)
	if n := len(g.roster()); n != 0 {
		t.Fatalf("下线后名单应当为空，实际 %d 条", n)
	}
}

// 守 markConnected 的 gone 判断：仅靠 busy 挡不住迟到的校验结果。
func TestLateMarkConnectedIsIgnored(t *testing.T) {
	g := newGuestRegistry()
	const key = "guest-late"

	g.claim(key, 1)
	g.markGone(key)                         // 会话先结束
	g.markConnected(key, "DEAD-BEEF", "直连") // 校验结果迟到

	if n := len(g.roster()); n != 0 {
		t.Fatalf("迟到的人不该出现在名单里，实际 %d 条：%s", n, rosterPayload(g))
	}
	g.release(key)
}

// 断线重连：新代次被接纳后名单先清空，待重新校验通过再出现。
func TestRosterClearedWhenNextGenerationAccepted(t *testing.T) {
	g := newGuestRegistry()
	const key = "guest-r"

	g.claim(key, 1)
	g.markConnected(key, "1111-2222", "直连")
	if n := len(g.roster()); n != 1 {
		t.Fatalf("应当有 1 条，实际 %d", n)
	}

	g.markGone(key) // 主流程收尾
	g.release(key)  // 让出位置

	if got := g.claim(key, 2); got != claimAccepted {
		t.Fatalf("带更高代次回来应当被接纳，实际 %v", got)
	}
	if n := len(g.roster()); n != 0 {
		t.Fatalf("新的一代还没通过校验，名单里不该有人，实际 %d 条", n)
	}

	g.markConnected(key, "3333-4444", "中继")
	list := g.roster()
	if len(list) != 1 || list[0].Code != "3333-4444" || list[0].Mode != "中继" {
		t.Fatalf("重连后的名单不对：%+v", list)
	}
}

// 界面按 JSON 解析，字段名即契约；空名单须给出 []，不能是 null。
func TestRosterPayloadIsJSONWithStableKeys(t *testing.T) {
	type who struct {
		C string `json:"c"`
		M string `json:"m"`
		S int64  `json:"s"`
	}
	type payload struct {
		N   int   `json:"n"`
		Who []who `json:"who"`
	}

	g := newGuestRegistry()
	var empty payload
	if err := json.Unmarshal([]byte(rosterPayload(g)), &empty); err != nil {
		t.Fatalf("空名单不是合法 JSON：%v", err)
	}
	if empty.N != 0 || empty.Who == nil {
		t.Fatalf("空名单应当是 n=0、who=[]，实际 n=%d who=%v", empty.N, empty.Who)
	}
	if !strings.Contains(rosterPayload(g), `"who":[]`) {
		t.Errorf("空名单的 who 应当是 []，实际：%s", rosterPayload(g))
	}

	g.claim("guest-j", 1)
	g.markConnected("guest-j", "A1B2-C3D4", "直连")
	var one payload
	if err := json.Unmarshal([]byte(rosterPayload(g)), &one); err != nil {
		t.Fatalf("名单不是合法 JSON：%v", err)
	}
	if one.N != 1 || len(one.Who) != 1 || one.Who[0].C != "A1B2-C3D4" || one.Who[0].M != "直连" {
		t.Fatalf("字段名与内容对不上：%s", rosterPayload(g))
	}
	// s 为上线时刻（unix 秒），非已连时长。
	if one.Who[0].S < 1600000000 {
		t.Errorf("s 应当是 unix 秒的上线时刻，实际 %d", one.Who[0].S)
	}
}
