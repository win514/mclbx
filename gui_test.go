package main

// gui_test.go 测试控制台的输出读取链路：读循环、日志上限与增量、落后补发。
// 管道缓冲有限，读方停止会使写方阻塞。

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRoleEnv = "MCLBX_TEST_ROLE"

// TestMain 使测试二进制可扮演疯狂输出的子进程（由 MCLBX_TEST_ROLE=spew 触发）。
func TestMain(m *testing.M) {
	if os.Getenv(testRoleEnv) == "spew" {
		spewToStdout()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func spewToStdout() {
	out := bufio.NewWriterSize(os.Stdout, 64*1024)
	// ① 一行超长，远大于 Scanner 的 token 上限
	_, _ = out.WriteString(strings.Repeat("L", 3<<20))
	_, _ = out.WriteString("\n")
	// ② 大量普通行
	for i := 0; i < 20000; i++ {
		_, _ = fmt.Fprintf(out, "line-%05d\n", i)
	}
	// ③ 结尾不带换行：末尾半行也须读入
	_, _ = out.WriteString("tail-without-newline")
	_ = out.Flush()
}

func collectPump(t *testing.T, in string) []string {
	t.Helper()
	var got []string
	pumpLines(strings.NewReader(in), func(s string) { got = append(got, s) })
	return got
}

func TestPumpLinesHandlesOrdinaryInput(t *testing.T) {
	got := collectPump(t, "a\r\nb\n\nend")
	want := []string{"a", "b", "", "end"}
	if len(got) != len(want) {
		t.Fatalf("行数不对：拿到 %d 行 %q，想要 %d 行 %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 行是 %q，想要 %q", i, got[i], want[i])
		}
	}
}

func TestPumpLinesTruncatesButKeepsReading(t *testing.T) {
	long := strings.Repeat("x", guiMaxLine+5000)
	got := collectPump(t, long+"\nafter-long\n")

	if len(got) != 2 {
		t.Fatalf("超长行之后的行必须照收：拿到 %d 行 %q", len(got), got)
	}
	if !strings.HasSuffix(got[0], guiLineCut) {
		t.Errorf("超长行应当被截断并标记，实际结尾是 %q", tail(got[0], 40))
	}
	if len(got[0]) != guiMaxLine+len(guiLineCut) {
		t.Errorf("截断长度不对：%d（期望 %d）", len(got[0]), guiMaxLine+len(guiLineCut))
	}
	if got[1] != "after-long" {
		t.Errorf("超长行后面那一行被弄坏了：%q", got[1])
	}
}

func TestPumpLinesKeepsPartialLastLine(t *testing.T) {
	got := collectPump(t, "no-newline-at-eof")
	if len(got) != 1 || got[0] != "no-newline-at-eof" {
		t.Fatalf("末尾没有换行的半行也要收下，实际 %q", got)
	}
}

// 子进程输出 3 MiB 单行 + 2 万行时，读循环须一直读到子进程结束。
func TestRunnerDrainsChildThatSpews(t *testing.T) {
	if testing.Short() {
		t.Skip("耗时用例，-short 时跳过")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("拿不到测试二进制路径：%v", err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), testRoleEnv+"=spew")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("起不来子进程：%v", err)
	}

	j := &guiJob{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); j.pump(stdout) }()
	go func() { defer wg.Done(); j.pump(stderr) }()

	pumpsDone := make(chan struct{})
	go func() { wg.Wait(); close(pumpsDone) }()

	select {
	case <-pumpsDone:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("读循环停了：子进程的输出没被收完，它会一直卡在 write 上（这正是「界面像是卡住」的成因）")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("子进程非正常结束：%v", err)
	}

	// 1（超长行）+ 20000 行 + 1（末尾半行）
	if want := int64(20002); j.total != want {
		t.Fatalf("总行数 %d，期望 %d（少的那部分说明有输出被丢掉了）", j.total, want)
	}
	last := j.snapshot(j.total - 1)
	if len(last) != 1 || last[0] != "tail-without-newline" {
		t.Fatalf("最后一行不对：%q", last)
	}

	kept := j.snapshot(0)
	if len(kept) != guiMaxLines {
		t.Fatalf("服务端该留 %d 行，实际 %d 行", guiMaxLines, len(kept))
	}
	if kept[0] != "line-17001" {
		t.Errorf("留下来的第一行不对：%q", kept[0])
	}
}

// 日志裁到上限后，snapshot 仍须返回新增行。
func TestJobSnapshotKeepsDeliveringAfterTrim(t *testing.T) {
	j := &guiJob{}
	for i := 0; i < guiMaxLines+500; i++ {
		j.append(fmt.Sprintf("line-%d", i))
	}
	since := j.total
	j.append("late-line")

	got := j.snapshot(since)
	if len(got) != 1 || got[0] != "late-line" {
		t.Fatalf("裁到上限之后仍要能拿到新行，实际 %q", got)
	}
	if next := j.snapshot(j.total); len(next) != 0 {
		t.Fatalf("已经追平了就不该再有内容，实际 %q", next)
	}
}

func TestJobSnapshotTellsClientWhatItMissed(t *testing.T) {
	j := &guiJob{}
	for i := 0; i < guiMaxLines+100; i++ {
		j.append("row")
	}
	got := j.snapshot(3) // 模拟界面只收到第 3 行，中间已被裁掉
	if len(got) != guiMaxLines+1 {
		t.Fatalf("落后太多时应当把手上留着的全发过去（外加一行说明），实际 %d 行", len(got))
	}
	if !strings.Contains(got[0], "没有保留") {
		t.Errorf("第一行应当说明中间丢了多少，实际 %q", got[0])
	}

	// since=0 时不需要丢行说明
	if first := j.snapshot(0); strings.Contains(first[0], "没有保留") {
		t.Errorf("since=0 不该带丢行说明，实际 %q", first[0])
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
