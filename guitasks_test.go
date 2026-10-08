package main

// guitasks_test.go —— 界面按钮的底线：每个任务都要能拼出可识别的子命令，必填项留空须报错。

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// 说明书第 6 节操作表须与 guiTasks 对齐：操作名与标题项数都要一致。
func TestManualOpListMatchesGuiTasks(t *testing.T) {
	raw, err := os.ReadFile("manual.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	for _, task := range guiTasks {
		// 表格单元格以操作名开头，可能后跟「长时」角标
		if !strings.Contains(page, "<td>"+task.Name) {
			t.Errorf("说明书第 6 节缺了操作「%s」", task.Name)
		}
	}

	want := fmt.Sprintf("操作一览（%d 项）", len(guiTasks))
	// 目录与正文标题各出现一次，两处都须正确
	if n := strings.Count(page, want); n < 2 {
		t.Errorf("说明书的目录与标题都应当写「%s」，实际出现 %d 次", want, n)
	}
	if !strings.Contains(page, fmt.Sprintf("共 %d 项", len(guiTasks))) {
		t.Errorf("说明书操作表末尾的小字应当写「共 %d 项」", len(guiTasks))
	}
}

// 反向校验：说明书中的操作与分组在界面里都必须存在。
func TestManualOpListHasNoExtraRows(t *testing.T) {
	raw, err := os.ReadFile("manual.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	const tableTag = `<table class="boxed ops">`
	start := strings.Index(page, tableTag)
	if start < 0 {
		t.Fatalf("manual.html 里找不到 %s", tableTag)
	}
	end := strings.Index(page[start:], "</table>")
	if end < 0 {
		t.Fatal("手册的操作表没有闭合的 </table>")
	}
	table := page[start : start+end]

	known := map[string]bool{}
	for _, task := range guiTasks {
		known[task.Name] = true
	}
	knownGroup := map[string]bool{}
	for _, task := range guiTasks {
		knownGroup[task.Group] = true
	}

	ops, groups := 0, 0
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "<tr><td") {
			continue
		}
		if strings.HasPrefix(line, `<tr><td class="grp"`) {
			// 取 <td 之后的第一个 '>' 作为单元格起点（首个 '>' 属于 <tr>）
			cell := line[strings.Index(line, "<td"):]
			if i := strings.Index(cell, ">"); i >= 0 {
				cell = cell[i+1:]
			}
			if i := strings.Index(cell, "</td>"); i >= 0 {
				cell = cell[:i]
			}
			groups++
			if g := strings.TrimSpace(cell); g != "" && !knownGroup[g] {
				t.Errorf("说明书第 6 节有分组「%s」，但界面里已经没有这一组了", g)
			}
			continue
		}
		// 操作行：第一个单元格是操作名，可能后跟「长时」角标
		cell := strings.TrimPrefix(line, "<tr><td>")
		if i := strings.Index(cell, "</td>"); i >= 0 {
			cell = cell[:i]
		}
		for {
			a := strings.Index(cell, "<span")
			if a < 0 {
				break
			}
			b := strings.Index(cell[a:], "</span>")
			if b < 0 {
				cell = cell[:a]
				break
			}
			cell = cell[:a] + cell[a+b+len("</span>"):]
		}
		name := strings.TrimSpace(cell)
		if name == "" {
			continue
		}
		ops++
		if !known[name] {
			t.Errorf("说明书第 6 节有操作「%s」，但界面里已经没有它了 —— 用户会照着点一个不存在的按钮", name)
		}
	}

	if ops != len(guiTasks) {
		t.Errorf("操作表里解析出 %d 个操作，界面里有 %d 个", ops, len(guiTasks))
	}
	if groups != len(knownGroup) {
		t.Errorf("操作表里解析出 %d 个分组，界面里有 %d 个", groups, len(knownGroup))
	}
}

// 说明书第 11 节内嵌的 CLI 帮助须与程序 usageText 逐行一致。
func TestManualCliMatchesUsage(t *testing.T) {
	raw, err := os.ReadFile("manual.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	const startTag = `<pre class="cli">`
	start := strings.Index(page, startTag)
	if start < 0 {
		t.Fatalf("manual.html 里找不到 %s", startTag)
	}
	start += len(startTag)
	end := strings.Index(page[start:], "</pre>")
	if end < 0 {
		t.Fatal("manual.html 里找不到对应的 </pre>")
	}
	block := page[start : start+end]

	// 首行带 <b> 标记；正文中的 <、>、& 为 HTML 实体
	block = strings.ReplaceAll(block, "<b>", "")
	block = strings.ReplaceAll(block, "</b>", "")
	block = strings.ReplaceAll(block, "\r\n", "\n")
	block = strings.ReplaceAll(block, "&lt;", "<")
	block = strings.ReplaceAll(block, "&gt;", ">")
	block = strings.ReplaceAll(block, "&amp;", "&")

	got := strings.Split(strings.TrimRight(block, "\n"), "\n")
	want := strings.Split(strings.TrimRight(usageText, "\n"), "\n")

	if len(got) != len(want) {
		t.Errorf("说明书里的帮助文本 %d 行，程序实际输出 %d 行 —— 有整行被漏掉或多出来", len(got), len(want))
	}
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			t.Errorf("说明书第 %d 行与程序帮助不一致，从这一行起后面全部错位：\n  说明书: %q\n  程序  : %q", i+1, got[i], want[i])
			break
		}
	}
}

// 同一分组在 guiTasks 中必须连续出现：原生界面（winnative.go 的 buildRows）按分组变化插入标题，
// 分组被隔开时会生成重复标题。
func TestGroupsAreContiguous(t *testing.T) {
	closed := map[string]bool{}
	last := ""
	for _, task := range guiTasks {
		if task.Group == last {
			continue
		}
		if closed[task.Group] {
			t.Errorf("分组 %q 被拆成了两段（中间隔了别的组）—— 原生界面会插出第二个同名标题", task.Group)
		}
		if last != "" {
			closed[last] = true
		}
		last = task.Group
	}
}

// guiSampleValues 为界面字段提供的示例值，用于验证拼出的命令可被识别。
var guiSampleValues = map[string]string{
	"room": "abc123", "port": "25565", "entry": "25565", "to": "127.0.0.1:25565",
	"host": "[2408::1]:8090", "relay": "1.2.3.4:8090", "local": "127.0.0.1:25565",
	"relayServer": "turn:u:p@1.2.3.4:3478", "link": "turn:u:p@1.2.3.4:3478",
	"publicIP": "1.2.3.4", "range": "49160-49200", "rest": "s3cret",
	"allow": "a,b", "user": "mclbx", "pass": "lab", "peer": "1.2.3.4",
	"ports": "30000-30099", "tag": "t", "zone": "mclbx.test", "name": "abc",
	"addr": "1.2.3.4", "token": "t", "provider": "dynv6", "suffix": "sslip.io",
	"mode": "raw", "dns": "sslip.io", "signal": ":8090", "web": "off",
	"interval": "300ms", "wait": "30s", "timeout": "5s", "proto": "tcp", "watch": "10m",
	"relay-entry": "25580", "relay-allow": "a,b", "srv": "1.2.3.4",
	"server-name": "mc.example", "upstream": "1.1.1.1:53", "listen": "127.0.0.1:53",
	"txt": "x", "ttl": "60", "target": "mc.example", "ext": "25565",
	"suffixName": "", "provider2": "",
}

func guiSampleFor(f guiField) string {
	if f.Kind == "check" {
		return "" // 勾选项留空就是"不勾"
	}
	if v, ok := guiSampleValues[f.ID]; ok && v != "" {
		return v
	}
	if f.Default != "" {
		return f.Default
	}
	return "1"
}

func TestGuiTasksRejectMissingRequired(t *testing.T) {
	for _, task := range guiTasks {
		hasRequired := false
		for _, f := range task.Fields {
			if f.Required {
				hasRequired = true
			}
		}
		if !hasRequired {
			continue
		}
		t.Run(task.Key, func(t *testing.T) {
			if _, err := task.build(map[string]string{}); err == nil {
				t.Errorf("必填项都空着还拼出了命令 —— 应该在界面上就拦住，而不是让子进程去失败")
			}
		})
	}
}

// 字段填齐后所有任务都须拼出已知子命令。
func TestGuiTasksBuildKnownCommand(t *testing.T) {
	// main.go 认识的子命令
	known := map[string]bool{
		"gui": true, "expose": true, "room": true, "join": true, "stun": true,
		"mailbox": true, "probe": true, "ice": true, "punch": true, "relay": true,
		"tcptunnel": true, "relaycheck": true, "relaybox": true, "natmap": true, "doctor": true,
		"firewall": true, "dns": true, "slpfake": true, "ping": true, "verify": true,
		"diag": true,
	}
	for _, task := range guiTasks {
		t.Run(task.Key, func(t *testing.T) {
			in := map[string]string{}
			for _, f := range task.Fields {
				in[f.ID] = guiSampleFor(f)
			}
			args, err := task.build(in)
			if err != nil {
				t.Fatalf("把字段都填上之后仍然报错：%v", err)
			}
			if len(args) == 0 {
				t.Fatal("拼出来是空的")
			}
			if !known[args[0]] {
				t.Errorf("拼出来的子命令是 %q，main.go 里不认识它", args[0])
			}
			// 参数中不得有空串
			for i, a := range args {
				if a == "" {
					t.Errorf("第 %d 个参数是空串：%v", i, args)
					break
				}
			}
		})
	}
}

// 中转字段在所有任务中须使用同一 ID（relayServer）与同一标签；识别字段以 ID 为准，
// 标签文案可随界面调整。
func TestRelayFieldNameIsConsistent(t *testing.T) {
	var withRelay []string
	for _, task := range guiTasks {
		for _, f := range task.Fields {
			if f.ID == "relayServer" {
				if !strings.Contains(f.Label, "中转") {
					t.Errorf("任务 %s 里的中转字段标签是 %q，应当带上「中转」两个字", task.Key, f.Label)
				}
				withRelay = append(withRelay, task.Key)
			}
		}
	}
	if len(withRelay) < 5 {
		t.Errorf("太少任务带中转字段了（只有 %v）—— 房主在每条路上都该能填它", withRelay)
	}
	// 命令行中须使用同一参数名
	for _, task := range guiTasks {
		in := map[string]string{}
		for _, f := range task.Fields {
			in[f.ID] = guiSampleFor(f)
		}
		args, err := task.build(in)
		if err != nil {
			continue
		}
		for i, a := range args {
			if a == "--relay-server" && i+1 < len(args) {
				if args[i+1] != guiSampleValues["relayServer"] {
					t.Errorf("任务 %s 的 --relay-server 值不对：%q", task.Key, args[i+1])
				}
			}
		}
	}
}
