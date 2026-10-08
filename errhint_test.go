package main

// errhint_test.go —— 报错提示的漏报与误报测试。

import (
	"strings"
	"testing"
)

// 真机抓取的原文（Windows winsock 常见措辞）
var realRawErrors = []struct {
	line string
	want string // 期望提示里出现的关键词
}{
	{
		"listen tcp 0.0.0.0:25565: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.",
		"已被其他程序占用",
	},
	{
		"listen tcp :8090: bind: address already in use",
		"已被其他程序占用",
	},
	{
		"dial tcp 1.2.3.4:8090: connect: connection refused",
		"无服务响应",
	},
	{
		"dial udp [2001:db8::9]:3478: i/o timeout",
		"未响应",
	},
	{
		"read udp 192.168.1.5:51000->203.0.113.7:3478: i/o timeout",
		"未响应",
	},
	{
		"context deadline exceeded",
		"未响应",
	},
	{
		"dial tcp [2001:db8::9]:8090: connect: no route to host",
		"不可达",
	},
	{
		"dial tcp: lookup mc.example.com: no such host",
		"解析失败",
	},
	{
		"listen udp: no suitable address found",
		"不匹配",
	},
	{
		"read tcp 192.168.1.5:51000->1.2.3.4:8090: wsarecv: An existing connection was forcibly closed by the remote host.",
		"中断",
	},
	{
		"listen tcp 0.0.0.0:80: bind: An attempt was made to access a socket in a way forbidden by its access permissions.",
		"无法使用",
	},
	// Windows 平台的原文：同一毛病措辞与 Linux 不同，需单独覆盖。
	{
		"dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.",
		"无服务响应",
	},
	{
		"dial tcp 1.2.3.4:80: connectex: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond.",
		"未响应",
	},
	{
		"read tcp 192.168.1.5:51000->1.2.3.4:8090: wsarecv: An established connection was aborted by the software in your host machine.",
		"中断",
	},
	{
		"dial tcp 1.2.3.4:80: connectex: A socket operation was attempted to an unreachable network.",
		"不可达",
	},
}

func TestRealErrorsGetAChineseHint(t *testing.T) {
	for _, c := range realRawErrors {
		got := explainError(c.line)
		if len(got) == 0 {
			t.Errorf("这句该给出提示，结果什么都没有：%s", c.line)
			continue
		}
		all := ""
		for _, h := range got {
			all += h.text + "\n"
		}
		if !strings.Contains(all, c.want) {
			t.Errorf("提示里该说到 %q，实际：\n%s\n（原文：%s）", c.want, all, c.line)
		}
		// 提示需包含「处理：」
		if !strings.Contains(all, "处理：") {
			t.Errorf("提示里该带上「处理：」：\n%s\n（原文：%s）", all, c.line)
		}
	}
}

// 正常输出不得被当成报错
func TestNormalOutputGetsNoHint(t *testing.T) {
	normal := []string{
		"",
		"==== mclbx 1.15 · 路况诊断 ====",
		"全球 IPv6      : 2 个",
		" 2001:db8:1111:2222:3333:4444:5555:6669（临时）  [以太网]",
		"STUN 198.51.100.9:3478   -> 203.0.113.7:37586  (50 ms)",
		"IPv4 出口      : 公网映射 203.0.113.7:37586（4 台服务器看到的一致），锥形 NAT —— 映射与目标无关，好打洞",
		"路由器 UPnP    : 不可用（SSDP 没有任何响应（路由器多半没开 UPnP））",
		" 本机 25565   : 被占用（可能游戏正开着，正常）",
		" [1/5] 解析地址        ✓ 1.2.3.4:3478",
		" [5/5] 连发 64 个包     ✓ 全收到，3.21 MiB/s（920 ms，含两端各一次加密开销）",
		" 结论：这条链接可用。",
		" 结论：部分步骤可用 —— 标记 ✓ 的路径已连通，其余路径不可用：",
		"  原因：这个端口已被其他程序占用",
		"      处理：关闭占用该端口的程序后重试",
		"路由器放行失败：SOAP 失败：HTTP 500",
		"配置没写下去（不影响使用）：permission denied",
	}
	for _, line := range normal {
		if got := explainError(line); len(got) != 0 {
			var texts []string
			for _, h := range got {
				texts = append(texts, h.text)
			}
			t.Errorf("这行是正常输出，不该补提示：%q\n补成了：%s", line, strings.Join(texts, " / "))
		}
	}
}

// permission denied 仅在 socket 语境下才算端口问题
func TestPermissionDeniedOnlyCountsInSocketContext(t *testing.T) {
	if got := explainError("open C:\\Users\\me\\AppData\\Local\\mclbx\\gui.log: permission denied"); len(got) != 0 {
		t.Error("写文件被拒不该提示端口问题")
	}
	if got := explainError("listen tcp 0.0.0.0:25: bind: permission denied"); len(got) == 0 {
		t.Error("绑定端口被拒应当提示端口问题")
	}
}

// 一行报错只给一组提示
func TestOnlyOneHintPerLine(t *testing.T) {
	got := explainError("listen tcp :25565: bind: address already in use")
	if len(got) != 2 {
		t.Fatalf("应当正好是「一条说明 + 一条处理：」，得到 %d 行：%+v", len(got), got)
	}
	if got[0].key == got[1].key {
		t.Error("两行的去重 key 不该一样")
	}
}

// 同一任务中重复报错，提示只追加一次
func TestJobAppendsHintOnlyOnce(t *testing.T) {
	j := &guiJob{}
	raw := "dial tcp 1.2.3.4:8090: connect: connection refused"
	j.append(raw)
	j.append(raw)
	j.append(raw)

	count := 0
	for _, ln := range j.lines {
		if strings.Contains(ln, "原因：") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("提示只该出现一次，实际出现 %d 次：\n%s", count, strings.Join(j.lines, "\n"))
	}
	if j.total != int64(len(j.lines)) {
		t.Errorf("行序号应当和实际行数一致：total=%d len=%d", j.total, len(j.lines))
	}
	// 原始行一行都不能少
	rawCount := 0
	for _, ln := range j.lines {
		if ln == raw {
			rawCount++
		}
	}
	if rawCount != 3 {
		t.Errorf("原始输出不该被吞掉，期望 3 行，得到 %d 行", rawCount)
	}
}
