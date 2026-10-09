package main

// guiconfig_test.go —— 覆盖「记住上次填的」这一层的边界：文件不存在、损坏、字段清空、勾选默认值、记录条数上限。

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// withTempConfig 把配置文件指到临时目录，并在用例结束时恢复。
func withTempConfig(t *testing.T) string {
	t.Helper()
	old := guiConfigFile
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	guiConfigFile = func() string { return path }
	resetGuiConfigCache()
	t.Cleanup(func() {
		guiConfigFile = old
		resetGuiConfigCache()
	})
	return path
}

func TestConfigRemembersWhatWasFilledIn(t *testing.T) {
	path := withTempConfig(t)

	// 未存过时应读出空值而非报错
	if v, ok := savedField("room", "relayServer"); ok || v != "" {
		t.Fatalf("第一次用不该有存过的东西，得到 %q ok=%v", v, ok)
	}

	rememberFieldValues("room", map[string]string{"room": " abc123 ", "port": "25565"})

	// 重置内存缓存（模拟重启）后仍可读到
	resetGuiConfigCache()
	if v, ok := savedField("room", "room"); !ok || v != "abc123" {
		t.Errorf("应当记住 abc123，得到 %q ok=%v", v, ok)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("配置文件应当已经写下来了：%v", err)
	}
	// 临时文件不该留在磁盘上
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("原子写不该留下 .tmp")
	}
}

// 字段取值：存过的优先，空串则回落到任务默认值
func TestFieldDefaultPrefersSavedThenFallsBackToTaskDefault(t *testing.T) {
	withTempConfig(t)
	f := guiField{ID: "port", Default: "25565"}

	if got := fieldDefault("room", f); got != "25565" {
		t.Errorf("没存过时该用任务默认值，得到 %q", got)
	}
	rememberFieldValues("room", map[string]string{"port": "25599"})
	if got := fieldDefault("room", f); got != "25599" {
		t.Errorf("存过时该用存过的值，得到 %q", got)
	}
	rememberFieldValues("room", map[string]string{"port": ""})
	if got := fieldDefault("room", f); got != "25565" {
		t.Errorf("清空之后该回到默认值，得到 %q", got)
	}
}

// 勾选项：默认值 "1" 表示默认勾上，用户更改后记住其状态
func TestFieldCheckedHonoursSavedAndDefault(t *testing.T) {
	withTempConfig(t)
	on := guiField{ID: "auto", Kind: "check", Default: "1"}
	off := guiField{ID: "quick", Kind: "check"}

	if !fieldChecked("probe", on) {
		t.Error("默认值是 1 的勾选项，第一次打开就该是勾上的")
	}
	if fieldChecked("probe", off) {
		t.Error("没有默认值的勾选项，第一次打开不该勾上")
	}
	rememberFieldValues("probe", map[string]string{"auto": "", "quick": "1"})
	if fieldChecked("probe", on) {
		t.Error("用户把勾去掉了，就该记住没勾 —— 否则每次打开都又来一次自动体检")
	}
	if !fieldChecked("probe", off) {
		t.Error("用户勾上了，就该记住勾上")
	}
}

// 自动体检开关即该勾选项
func TestAutoProbeFollowsTheCheckbox(t *testing.T) {
	withTempConfig(t)
	if !autoProbeOn() {
		t.Error("默认应当自动体检一次")
	}
	rememberFieldValues("probe", map[string]string{"auto": ""})
	if autoProbeOn() {
		t.Error("勾掉之后就不该再自动跑")
	}
}

// 配置文件损坏时当作未记住处理，不影响启动
func TestConfigIgnoresBrokenFile(t *testing.T) {
	path := withTempConfig(t)
	if err := os.WriteFile(path, []byte("{ 这不是 json"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	if v, ok := savedField("room", "port"); ok || v != "" {
		t.Errorf("坏文件应当被当成空，得到 %q ok=%v", v, ok)
	}
	// 坏文件后仍能正常写入
	rememberFieldValues("room", map[string]string{"port": "25565"})
	resetGuiConfigCache()
	if v, _ := savedField("room", "port"); v != "25565" {
		t.Errorf("坏文件之后应当能重新写进去，得到 %q", v)
	}
}

func TestForgetRemovesConfig(t *testing.T) {
	path := withTempConfig(t)
	rememberFieldValues("room", map[string]string{"port": "25565"})
	resetGuiConfigCache()
	if err := forgetGuiConfig(); err != nil {
		t.Fatalf("forget 不该失败：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("forget 之后文件应当没了")
	}
	if v, ok := savedField("room", "port"); ok || v != "" {
		t.Errorf("forget 之后内存里也该清干净，得到 %q ok=%v", v, ok)
	}
	// 未写过时重复 forget 不应报错
	if err := forgetGuiConfig(); err != nil {
		t.Errorf("重复 forget 不该失败：%v", err)
	}
}

// 值未变时不重复写盘
func TestRememberSkipsUnchangedWrite(t *testing.T) {
	path := withTempConfig(t)
	rememberFieldValues("room", map[string]string{"port": "25565"})
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 将 mtime 设为过去再写同样值；若真写盘则 mtime 会变
	old := st1.ModTime().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	rememberFieldValues("room", map[string]string{"port": "25565"})
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.ModTime().Equal(old) {
		t.Error("值没变时不该重新写盘")
	}
}

// 任务记录数须有上限
func TestConfigPrunesOldTasks(t *testing.T) {
	withTempConfig(t)
	for i := 0; i < guiConfigMaxTasks+8; i++ {
		rememberFieldValues("task-"+strconv.Itoa(i), map[string]string{"port": "1"})
	}
	resetGuiConfigCache()
	if n := len(loadGuiConfig().Inputs); n > guiConfigMaxTasks {
		t.Errorf("最多留 %d 个任务的记录，现在有 %d 个", guiConfigMaxTasks, n)
	}
}

// 网页版任务表须带上已存值（只认 default 字段）
func TestWebTasksCarrySavedValues(t *testing.T) {
	withTempConfig(t)
	rememberFieldValues("probe", map[string]string{"auto": "", "port": "25599", "quick": "1"})

	var got *guiTask
	web := guiTasksForWeb()
	for i := range web {
		if web[i].Key == "probe" {
			got = &web[i]
		}
	}
	if got == nil {
		t.Fatal("任务表里应当还有 probe")
	}
	byID := map[string]guiField{}
	for _, f := range got.Fields {
		byID[f.ID] = f
	}
	if byID["port"].Default != "25599" {
		t.Errorf("文本字段该带上存过的值，得到 %q", byID["port"].Default)
	}
	if byID["auto"].Default != "" {
		t.Errorf("没勾上的勾选项在网页版里应当是空串，得到 %q", byID["auto"].Default)
	}
	if byID["quick"].Default != "1" {
		t.Errorf("勾上的勾选项在网页版里应当是 \"1\"，得到 %q", byID["quick"].Default)
	}
	// 原任务表不得被修改（网页版数据为副本）
	for _, f := range guiTasks[0].Fields {
		if f.ID == "port" && f.Default != "25565" {
			t.Errorf("原任务表被改了：%q", f.Default)
		}
	}
}

// 每个分组都须有配色与图标
func TestEveryGroupHasAStyle(t *testing.T) {
	for _, task := range guiTasks {
		if groupStyle(task.Group) == nil {
			t.Errorf("分组 %q 没有配颜色和图标（见 natGroupStyles）", task.Group)
		}
	}
	// 配色表中不得有未使用的分组
	inUse := map[string]bool{}
	for _, task := range guiTasks {
		inUse[task.Group] = true
	}
	for _, s := range natGroupStyles {
		if !inUse[s.Name] {
			t.Errorf("natGroupStyles 里的 %q 已经没有任何任务在用了", s.Name)
		}
	}
	// 分组颜色两两不同
	seen := map[uint32]string{}
	for _, s := range natGroupStyles {
		if prev, dup := seen[s.Color]; dup {
			t.Errorf("%q 和 %q 用了同一个颜色 %06x", s.Name, prev, s.Color)
		}
		seen[s.Color] = s.Name
	}
}

// 环境体检须运行 doctor（含结论）
func TestEnvCheckTaskRunsDoctor(t *testing.T) {
	for _, task := range guiTasks {
		if task.Key != "probe" {
			continue
		}
		args, err := task.build(map[string]string{"port": "25565"})
		if err != nil {
			t.Fatalf("拼不出来：%v", err)
		}
		if args[0] != "doctor" {
			t.Errorf("环境体检应当跑 doctor（会给出结论和推荐路线），实际跑的是 %q", args[0])
		}
		if !strings.Contains(strings.Join(args, " "), "--port 25565") {
			t.Errorf("应当把端口带上，实际：%v", args)
		}
		return
	}
	t.Fatal("任务表里没有 probe")
}
