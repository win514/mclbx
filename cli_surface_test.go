package main

// cli_surface_test.go 能力面“只增不减”：允许新增子命令与改动默认值，既有能力不得消失。
//
// cliSurfaceBaseline 记录重构起始的 22 个子命令，断言它们都能被 main.go 分发到。
// 删除或改名都会失败：改名对用户等同于删除。

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// cliSurfaceBaseline 是重构起始的子命令全集，按 main.go 的 `switch os.Args[1]` 统计，顺序同 switch。
var cliSurfaceBaseline = []string{
	"gui", "expose", "room", "join", "stun", "mailbox", "probe", "ice",
	"punch", "relay", "relaybox", "tcptunnel", "relaycheck", "natmap",
	"doctor", "diag", "firewall", "dns", "slpfake", "ping", "verify", "forget",
}

// dispatchCases 抽出 main.go 分发 switch 里形如 `case "expose":` 的单值标签。
// 取值范围是 `switch os.Args[1] {` 到帮助分支 `case "help"` 之间的那段。
func dispatchCases(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	i := strings.Index(src, "switch os.Args[1] {")
	if i < 0 {
		t.Fatal("main.go 里找不到 `switch os.Args[1]` —— 分发换形状了，这条用例要跟着改，" +
			"而不是把断言放宽")
	}
	j := strings.Index(src[i:], `case "help"`)
	if j < 0 {
		t.Fatal(`main.go 的分发里找不到 case "help" —— 帮助分支被挪走或改名了`)
	}

	re := regexp.MustCompile(`(?m)^\s*case\s+"([a-z0-9_-]+)":`)
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src[i:i+j], -1) {
		found[m[1]] = true
	}
	return found
}

// 基线清单自身不得重复，否则手误会掩盖某个子命令已消失。
func TestCommandSurfaceBaselineIsClean(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range cliSurfaceBaseline {
		if seen[name] {
			t.Errorf("基线清单里 %q 出现了两次 —— 清单有手误，子集断言会失真", name)
		}
		seen[name] = true
	}
	if len(cliSurfaceBaseline) != 22 {
		t.Errorf("基线记录的是 22 个子命令，清单里却是 %d 个 —— 改清单前先确认能力面确实变了",
			len(cliSurfaceBaseline))
	}
}

// 能力面只增不减：基线里的每一个都必须仍然能被 main.go 分发到。
func TestCommandSurfaceStaysAdditive(t *testing.T) {
	found := dispatchCases(t)
	if len(found) == 0 {
		t.Fatal("一个 case 都没抽到 —— 正则是空转的，这条断言等于没做")
	}

	var missing []string
	for _, name := range cliSurfaceBaseline {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("这些子命令在 main.go 的分发里不见了：%v\n"+
			"后端重构只允许新增能力，不允许删或改名 —— 若确实要退掉一条路，"+
			"先改 cliSurfaceBaseline 并说明理由", missing)
	}

	// 一并报出新增情况，便于发版时核对“只增”。
	var added []string
	for name := range found {
		dup := false
		for _, b := range cliSurfaceBaseline {
			if b == name {
				dup = true
				break
			}
		}
		if !dup {
			added = append(added, name)
		}
	}
	if len(added) > 0 {
		t.Logf("本次分发里多出这些子命令（新增是允许的，记得同步 usageText 与 manual.html）：%v", added)
	}
}
