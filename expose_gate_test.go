package main

// expose_gate_test.go 分享卡片的门槛文案必须与实际生效的门槛一致，
// 并覆盖地址形式与门槛诉求之间的关系。文案与真实行为不符会误导用户，
// 这里把卡片文案钉在地址形式上。

import (
	"strings"
	"testing"
)

const testRoom = "abc123"

// 没门槛时，不同地址形式的说明不同，公网 IPv4 不得声称“无法被全网扫描”。
func TestGateOffExplanationTellsTheTruthPerForm(t *testing.T) {
	cases := []struct {
		name       string
		addrKind   string
		room       string
		noGate     bool
		roomGiven  bool
		mustHave   []string
		mustNotHas []string
	}{
		{
			name: "用户给了房间码但形式承载不了：要提示不生效", addrKind: "v6raw", room: testRoom,
			roomGiven: true,
			mustHave:  []string{"IPv6 地址空间无法被全网扫描", testRoom, "不生效"},
		},
		{
			name: "公网 IPv4 直连：不许说无法被扫到", addrKind: "v4direct", room: testRoom,
			roomGiven:  true,
			mustHave:   []string{"可以被全网扫描", "任何获得该地址的玩家均可进入"},
			mustNotHas: []string{"无法被全网扫描"},
		},
		{
			name: "端口映射：同上", addrKind: "v4map", room: testRoom,
			roomGiven:  true,
			mustHave:   []string{"可以被全网扫描"},
			mustNotHas: []string{"无法被全网扫描"},
		},
		{
			name: "用户自己关了校验：要说清是他关的", addrKind: "v6dns", room: testRoom, noGate: true,
			roomGiven:  true,
			mustHave:   []string{"--no-gate", "任何获得该地址的玩家均可进入"},
			mustNotHas: []string{"IPv6 地址空间无法被全网扫描"},
		},
		{
			// 房间码由程序随机生成，用户并不知晓，不得据此提示“不生效”。
			name: "房间码是随机生成的：不要拿它来提醒", addrKind: "v6raw", room: "k3f9qz",
			roomGiven:  false,
			mustHave:   []string{"IPv6 地址空间无法被全网扫描"},
			mustNotHas: []string{"不生效", "k3f9qz"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(gateOffExplanation(c.addrKind, c.room, c.noGate, c.roomGiven), "\n")
			for _, w := range c.mustHave {
				if !strings.Contains(got, w) {
					t.Errorf("缺少必须出现的说法 %q；实际文案：\n%s", w, got)
				}
			}
			for _, w := range c.mustNotHas {
				if strings.Contains(got, w) {
					t.Errorf("出现了不该有的说法 %q；实际文案：\n%s", w, got)
				}
			}
		})
	}
}

// 用户给了房间码但地址形式承载不了时，必须明确说明房间码不生效。
func TestGateOffExplanationWarnsWhenRoomCodeCannotApply(t *testing.T) {
	for _, kind := range []string{"v6raw", "v4direct", "v4map"} {
		got := strings.Join(gateOffExplanation(kind, testRoom, false, true), "\n")
		if !strings.Contains(got, testRoom) || !strings.Contains(got, "不生效") {
			t.Errorf("addrKind=%s 且用户给了房间码时，应当明确说明房间码不生效；实际文案：\n%s", kind, got)
		}
	}
	// 反例：未给房间码时不出现“不生效”。
	got := strings.Join(gateOffExplanation("v6raw", "", false, false), "\n")
	if strings.Contains(got, "不生效") {
		t.Errorf("用户没给房间码时不该出现“不生效”这类提醒；实际文案：\n%s", got)
	}
	// 反例：用户以 --no-gate 关闭校验时不再提房间码。
	got = strings.Join(gateOffExplanation("v6raw", testRoom, true, true), "\n")
	if strings.Contains(got, "不生效") {
		t.Errorf("--no-gate 时房间码本就不该生效，不应再提醒；实际文案：\n%s", got)
	}
}

// 地址形式与门槛诉求的关系：auto 要门槛时只能选域名形式，否则房间码无处可写。
func TestAddrFormFor(t *testing.T) {
	cases := []struct {
		mode     string
		wantGate bool
		want     string
		why      string
	}{
		{"auto", true, "dns", "auto 且要门槛：房间码得有地方写，只能选域名形式"},
		{"auto", false, "raw", "auto 且不要门槛：没必要为门槛去引第三方 DNS"},
		{"raw", true, "raw", "用户点名要字面量：尊重它，代价是没有门槛"},
		{"raw", false, "raw", "同上"},
		{"dns", true, "dns", "用户点名要域名"},
		{"dns", false, "dns", "用户点名要域名，即使他不要门槛"},
	}
	for _, c := range cases {
		if got := addrFormFor(c.mode, c.wantGate); got != c.want {
			t.Errorf("addrFormFor(%q, wantGate=%v) = %q，期望 %q（%s）",
				c.mode, c.wantGate, got, c.want, c.why)
		}
	}
}

// 卡片若声称“口令是房间码”则该门槛必须实际生效；不生效时不得再列出房间码当口令。
func TestShareCardGateMatchesWhatActuallyApplies(t *testing.T) {
	on := &gateReady{
		state:     &gateState{room: testRoom, addrKind: "v6dns"},
		entryPort: 25565,
		address:   "abc123.2001-db8--1.sslip.io",
		gateRoom:  true,
	}
	card := strings.Join(zeroInstallCardLines(on), "\n")
	if !strings.Contains(card, "口令 : 房间码 "+testRoom) {
		t.Errorf("门槛生效时，卡片应当把房间码写成口令；实际：\n%s", card)
	}
	if !strings.Contains(card, "名称中不含房间码的连接将被拒绝") {
		t.Errorf("门槛生效时，卡片应当说明会拒绝不含房间码的连接；实际：\n%s", card)
	}

	// 以 --no-gate 关闭校验时，dns 形式同样没有门槛。
	off := &gateReady{
		state:     &gateState{room: testRoom, addrKind: "v6dns"},
		entryPort: 25565,
		address:   "abc123.2001-db8--1.sslip.io",
		gateRoom:  false,
		noGate:    true,
	}
	card = strings.Join(zeroInstallCardLines(off), "\n")
	if strings.Contains(card, "口令 : 房间码") {
		t.Errorf("--no-gate 时卡片仍在说口令是房间码 —— 这正是“说了有门槛、其实没有”；实际：\n%s", card)
	}
	if !strings.Contains(card, "--no-gate") {
		t.Errorf("--no-gate 时卡片应当点名是这个开关关掉了校验；实际：\n%s", card)
	}
}
