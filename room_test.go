package main

// room_test.go —— 房间码归一、IPv6 方括号、监听地址端口提取的回归测试。

import (
	"strings"
	"testing"
)

func TestNormalizeRoomCode(t *testing.T) {
	if got, err := normalizeRoomCode(""); err != nil || len(got) != 6 {
		t.Errorf("空房间码应该随机生成 6 位，实际 %q err=%v", got, err)
	}
	if got, err := normalizeRoomCode("  MiX123  "); err != nil || got != "mix123" {
		t.Errorf("应归一成小写去空格，实际 %q err=%v", got, err)
	}
	for _, bad := range []string{"房间码", "abc 123", "abc_123", "abc.123"} {
		if _, err := normalizeRoomCode(bad); err == nil {
			t.Errorf("%q 不该被接受", bad)
		}
	}
	// 只填空白必须按「什么都没填」处理并生成随机码，不能返回空串（空串会让门卫校验失效）。
	for _, blank := range []string{" ", "   ", "\t", "\n  "} {
		got, err := normalizeRoomCode(blank)
		if err != nil {
			t.Errorf("%q 该按「什么都没填」处理（生成随机码），实际报错 %v", blank, err)
			continue
		}
		if len(got) != 6 {
			t.Errorf("%q 该生成 6 位随机房间码，实际 %q（空串会让门卫失效）", blank, got)
		}
	}
}

// IPv6 必须加方括号，否则会解析成「地址:端口」歧义
func TestDialable(t *testing.T) {
	if got := dialable("2001:db8:1111:2222:3333:4444:5555:6670", 8090); got != "[2001:db8:1111:2222:3333:4444:5555:6670]:8090" {
		t.Errorf("IPv6 必须加方括号，实际 %q", got)
	}
	if got := dialable("example.com", 8090); got != "example.com:8090" {
		t.Errorf("域名不该加方括号，实际 %q", got)
	}
}

func TestPortOfAddr(t *testing.T) {
	if got, err := portOfAddr("0.0.0.0:8090"); err != nil || got != 8090 {
		t.Errorf("取端口失败：%d err=%v", got, err)
	}
	if got, err := portOfAddr("[::]:8090"); err != nil || got != 8090 {
		t.Errorf("IPv6 监听地址取端口失败：%d err=%v", got, err)
	}
	if _, err := portOfAddr("0.0.0.0"); err == nil {
		t.Error("没有端口的地址本该报错")
	}
}

func TestRelayListenAddr(t *testing.T) {
	ra, err := relayListenAddr(25580)
	if err != nil {
		t.Fatalf("算中转入口地址失败：%v", err)
	}
	if ra.port != 25580 || !strings.HasSuffix(ra.listen, ":25580") {
		t.Errorf("监听地址不对：%+v", ra)
	}
	// share 可能为空；非空时格式必须是 [IPv6]:端口
	if ra.share != "" && (!strings.HasPrefix(ra.share, "[") || !strings.HasSuffix(ra.share, "]:25580")) {
		t.Errorf("分享行格式不对：%q", ra.share)
	}
}
