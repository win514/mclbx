package main

// expose_gate_test.go —— “分享卡片上写的门槛，必须与实际生效的门槛一致”。
//
// 门槛这件事最容易出的错不是漏校验，而是**说错话**：卡片写着“名称中不含房间码的连接
// 将被拒绝”，实际谁都能进；或者卡片写着“地址无法被全网扫描”，而那个地址是公网 IPv4，
// 扫得到。用户按卡片行事，就会被误导。这里把卡片文案钉在实际地址形式上。

import (
	"strings"
	"testing"
)

const testRoom = "abc123"

// 没门槛时，对不同地址形式要说不同的话 —— 尤其不能对公网 IPv4 说“无法被全网扫描”。
func TestGateOffExplanationTellsTheTruthPerForm(t *testing.T) {
	cases := []struct {
		name       string
		addrKind   string
		room       string
		noGate     bool
		mustHave   []string
		mustNotHas []string
	}{
		{
			name: "IPv6 字面量：可以说难以被扫到", addrKind: "v6raw", room: testRoom,
			mustHave: []string{"IPv6 地址空间无法被全网扫描"},
			// 给了房间码却承载不了，必须点出来
			mustNotHas: []string{"房间里没有"},
		},
		{
			name: "公网 IPv4 直连：不许说无法被扫到", addrKind: "v4direct", room: testRoom,
			mustHave:   []string{"可以被全网扫描", "拿到这一行的人都能进"},
			mustNotHas: []string{"无法被全网扫描"},
		},
		{
			name: "端口映射：同上", addrKind: "v4map", room: testRoom,
			mustHave:   []string{"可以被全网扫描"},
			mustNotHas: []string{"无法被全网扫描"},
		},
		{
			name: "用户自己关了校验：要说清是他关的", addrKind: "v6dns", room: testRoom, noGate: true,
			mustHave:   []string{"--no-gate", "拿到这一行的人都能进"},
			mustNotHas: []string{"IPv6 地址空间无法被全网扫描"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(gateOffExplanation(c.addrKind, c.room, c.noGate), "\n")
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

// 给了房间码、但地址形式承载不了它时，必须明确告诉用户“房间码不生效”。
func TestGateOffExplanationWarnsWhenRoomCodeCannotApply(t *testing.T) {
	for _, kind := range []string{"v6raw", "v4direct", "v4map"} {
		got := strings.Join(gateOffExplanation(kind, testRoom, false), "\n")
		if !strings.Contains(got, testRoom) || !strings.Contains(got, "不生效") {
			t.Errorf("addrKind=%s 且给了房间码时，应当明确说明房间码不生效；实际文案：\n%s", kind, got)
		}
	}
	// 反例：没给房间码时不必吓唬人
	got := strings.Join(gateOffExplanation("v6raw", "", false), "\n")
	if strings.Contains(got, "不生效") {
		t.Errorf("没给房间码时不该出现“不生效”这类提醒；实际文案：\n%s", got)
	}
	// 反例：用户自己关了校验时，也不必再提房间码
	got = strings.Join(gateOffExplanation("v6raw", testRoom, true), "\n")
	if strings.Contains(got, "不生效") {
		t.Errorf("--no-gate 时房间码本就不该生效，不应再提醒；实际文案：\n%s", got)
	}
}

// 卡片说“口令是房间码”，就必须真的在生效；说不生效，就不能还列着房间码当口令。
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

	// 用户用 --no-gate 关掉校验（此时 dns 形式也照样没有门槛）。
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
