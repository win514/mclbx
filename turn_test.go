package main

// turn_test.go 测试 TURN 配置解析。

import (
	"testing"
)

func TestParseTurnEntryDocumentedForm(t *testing.T) {
	// 文档里的写法必须能解析。
	scheme, hostPort, user, pass, err := parseTurnEntry("turn:mclbx:mclbx-lab@10.99.0.1:3478")
	if err != nil {
		t.Fatalf("文档里的写法居然解析失败：%v", err)
	}
	if scheme != "turn" || hostPort != "10.99.0.1:3478" || user != "mclbx" || pass != "mclbx-lab" {
		t.Fatalf("解析结果不对：scheme=%q host=%q user=%q pass=%q", scheme, hostPort, user, pass)
	}
}

func TestParseTurnEntryVariants(t *testing.T) {
	cases := []struct {
		in                           string
		scheme, hostPort, user, pass string
	}{
		// 带 // 的写法
		{"turn://u:p@h.example.com:3478", "turn", "h.example.com:3478", "u", "p"},
		// TLS
		{"turns:u:p@h.example.com:5349", "turns", "h.example.com:5349", "u", "p"},
		// 长凭据：账号里带冒号，按 URL 转义写
		{"turn:1750000000%3Amclbx:b64=@h:3478", "turn", "h:3478", "1750000000:mclbx", "b64="},
		// 密码里的 = 和 / 不该被破坏
		{"turn:u:lBp/AqHogWA0TEy5NhRB3mCujps=@h:3478", "turn", "h:3478", "u", "lBp/AqHogWA0TEy5NhRB3mCujps="},
		// 密码里的 @：按最后一个 @ 拆分
		{"turn:u:p@ss@h:3478", "turn", "h:3478", "u", "p@ss"},
		// 不带账号，靠环境变量补
		{"turn:h:3478", "turn", "h:3478", "", ""},
		// 带查询串
		{"turn:h:3478?transport=udp", "turn", "h:3478?transport=udp", "", ""},
	}
	for _, c := range cases {
		scheme, hostPort, user, pass, err := parseTurnEntry(c.in)
		if err != nil {
			t.Errorf("%s：不该失败：%v", c.in, err)
			continue
		}
		if scheme != c.scheme || hostPort != c.hostPort || user != c.user || pass != c.pass {
			t.Errorf("%s：得到 scheme=%q host=%q user=%q pass=%q，期望 %q %q %q %q",
				c.in, scheme, hostPort, user, pass, c.scheme, c.hostPort, c.user, c.pass)
		}
	}
}

func TestParseTurnEntryRejects(t *testing.T) {
	for _, bad := range []string{"", "http:u:p@h:80", "turn", "stun:h:3478"} {
		if _, _, _, _, err := parseTurnEntry(bad); err == nil {
			t.Errorf("%q 应当被拒绝", bad)
		}
	}
}

// 整串中继配置的解析见 relaylink_test.go；这里只覆盖从 turn: URL 拆账号这一步。
