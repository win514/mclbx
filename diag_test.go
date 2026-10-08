package main

// diag_test.go 测试会话日志与诊断包中可单独测的部分。

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

// 会话日志落盘。
func TestSessionLogWritesPerJob(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	var s sessionLog
	s.write("job-1", "第一行")
	s.write("job-1", "第二行")

	data, err := os.ReadFile(sessionLogPath())
	if err != nil {
		t.Fatalf("会话日志没有落盘：%v", err)
	}
	got := string(data)
	if !strings.Contains(got, "第一行") || !strings.Contains(got, "第二行") {
		t.Fatalf("内容不对：%q", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Errorf("应当按 CRLF 分行（记事本打开才正常）：%q", got)
	}
}

// 换任务时截断重写。
func TestSessionLogTruncatesOnNewJob(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	var s sessionLog
	s.write("job-1", "上一次的内容")
	s.write("job-2", "这一次的内容")

	data, err := os.ReadFile(sessionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "上一次的内容") {
		t.Errorf("开始新任务时应当截断上一次，实际：%q", string(data))
	}
	if !strings.Contains(string(data), "这一次的内容") {
		t.Errorf("新内容没写进去：%q", string(data))
	}
}

func TestSessionLogTailKeepsLastLines(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	var s sessionLog
	for i := 1; i <= 10; i++ {
		s.write("job", fmt.Sprintf("行%d", i))
	}
	out := sessionLogTail(3)
	if !strings.Contains(out, "共 10 行") {
		t.Errorf("应当报出总行数，让人知道中间有没有被截掉：%q", out)
	}
	for _, want := range []string{"行8", "行9", "行10"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺 %s：%q", want, out)
		}
	}
	if strings.Contains(out, "行7") {
		t.Errorf("只该保留最后 3 行：%q", out)
	}
}

// 取不到日志时给出原因。
func TestSessionLogTailExplainsMissing(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	if out := sessionLogTail(100); !strings.Contains(out, "暂无会话日志") {
		t.Errorf("没有日志文件时应当说明原因，实际：%q", out)
	}
	if out := sessionLogTail(0); !strings.Contains(out, "未附带日志") {
		t.Errorf("--log 0 时应当明说不带日志，实际：%q", out)
	}
}

// 地址标注：判断地址是公网还是内网。
func TestAddrTagMarksPublicAndPrivate(t *testing.T) {
	cases := []struct{ ip, want string }{
		{"192.168.1.5", "内网"},
		{"203.0.113.7", "公网"},
		{"fe80::1", "链路本地"},
		{"fd00::1", "内网"},
		{"2001:db8:1111::1", "公网"},
	}
	for _, c := range cases {
		got := addrTag(net.ParseIP(c.ip))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s 应当标为%s，实际 %q", c.ip, c.want, got)
		}
	}
}

// 诊断包的命令与字段接线。
func TestDiagTaskIsWired(t *testing.T) {
	for _, task := range guiTasks {
		if task.Key != "diag" {
			continue
		}
		if task.Group != "实用工具" {
			t.Errorf("导出诊断包应当归在实用工具里，实际 %q", task.Group)
		}
		if task.Long {
			t.Error("导出诊断包是一次性任务，不该标成长时任务")
		}
		args, err := task.build(map[string]string{"port": "25566"})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(args, " "); got != "diag --port 25566" {
			t.Errorf("命令拼装不对：%q", got)
		}
		// 端口留空时不传空参数。
		if args, err := task.build(map[string]string{}); err != nil {
			t.Fatal(err)
		} else if strings.Join(args, " ") != "diag" {
			t.Errorf("端口留空时命令应当只有 diag，实际 %q", strings.Join(args, " "))
		}
		return
	}
	t.Fatal("guiTasks 里没有 diag，界面上的入口也就不会出现")
}

// 诊断包首句必须是隐私提醒。
func TestDiagTextWarnsAboutAddresses(t *testing.T) {
	// 只测文件头，不跑体检。
	head := diagHeader()
	if !strings.Contains(head, "诊断包") {
		t.Errorf("文件头应当标明这是诊断包：%q", head)
	}
	if !strings.Contains(head, "网络地址") {
		t.Errorf("文件头必须提醒含本机网络地址：%q", head)
	}
}
