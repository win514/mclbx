package main

// uiconfig_test.go 覆盖设置配置读写、重置与变更记录。
//
// 面板上的每项是否真正生效、只读区是否混入可编辑控件等「扫前端源码」的用例，
// 随前端实现一同移出仓库（见 frontend/说明.txt 与 D:\mclbx-frontend\tests）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempArchive 把存档目录指到临时目录：导出文件落在那里。
func withTempArchive(t *testing.T) string {
	t.Helper()
	old := archiveDirOverride
	dir := t.TempDir()
	archiveDirOverride = dir
	t.Cleanup(func() {
		archiveDirOverride = old
	})
	return dir
}

// ---- 取值不合法要回报 ----

func TestCorrectedValuesAreReported(t *testing.T) {
	// logKeep 超出 200~20000、musicVol 超出 0~100、rail 是不认识的值
	sub := guiUIState{LogKeep: "999999", MusicVol: "500", Rail: "超级紧凑", Theme: "dark"}
	got := normalizeUI(sub)
	fixed := uiCorrectedFields(sub, got)
	if len(fixed) != 3 {
		t.Fatalf("应当报出 3 项被回退，实际 %d 项：%v", len(fixed), fixed)
	}
	byField := map[string]map[string]string{}
	for _, f := range fixed {
		byField[f["field"]] = f
	}
	for _, want := range []string{"logKeep", "musicVol", "rail"} {
		f, ok := byField[want]
		if !ok {
			t.Errorf("%s 被回退了但没报出来", want)
			continue
		}
		if f["name"] == "" || f["name"] == want {
			t.Errorf("%s 报出来的是字段名而不是选项名（%q）—— 用户看不懂字段名", want, f["name"])
		}
		// sent 与 used 都须有内容：提示按「选项名（提交值 → 生效值）」拼出，
		// 任一处为空串则提示没有信息量。
		if f["sent"] == "" || f["used"] == "" {
			t.Errorf("%s 的提示缺了内容：%q → %q", want, f["sent"], f["used"])
		}
	}
	// 合法输入不该被报成"被纠正"
	if n := len(uiCorrectedFields(guiUIState{Theme: "dark"}, normalizeUI(guiUIState{Theme: "dark"}))); n != 0 {
		t.Errorf("合法取值被当成了被纠正：%d 项", n)
	}
	// 留空表示"回到默认"，不算被纠正
	if n := len(uiCorrectedFields(guiUIState{LogKeep: ""}, normalizeUI(guiUIState{LogKeep: ""}))); n != 0 {
		t.Errorf("留空被当成了被纠正：%d 项", n)
	}
}

// ---- 配置损坏：丢弃 + 回默认 + 记下原因 ----

func TestBrokenConfigIsDiscardedAndReported(t *testing.T) {
	path := withTempConfig(t)
	configFaultMu.Lock()
	configFault = ""
	configFaultMu.Unlock()

	if err := os.WriteFile(path, []byte(`{"ui":{"theme":"dark"},"inputs":{`), 0o600); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()

	// 半截 JSON 不得被采用：整份丢弃、回到默认
	if got := loadUI().Theme; got == "dark" {
		t.Error("损坏文件里的半截内容被采用了 —— 应当整份丢弃、回到默认")
	}
	if configFaultReason() == "" {
		t.Error("配置损坏没有记下原因 —— 界面就没法告诉用户发生了什么")
	}
}

// ---- 导出 / 导入 ----

func TestExportImportRoundTrip(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)

	if _, err := saveUI(guiUIState{Theme: "dark", Glass: "high", LogKeep: "1500"}); err != nil {
		t.Fatalf("保存设置失败：%v", err)
	}
	path, err := exportUIConfig()
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	if filepath.Dir(path) != dataDir() {
		t.Errorf("导出文件落在 %s，期望在存档目录 %s 下", path, dataDir())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不回导出的文件：%v", err)
	}
	if !strings.Contains(string(data), "\n  ") {
		t.Error("导出的配置没有缩进 —— 这份文件是给人看与给人改的")
	}
	var env map[string]any
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("导出的不是合法 JSON：%v", err)
	}
	if env["format"] != uiConfigFormat {
		t.Errorf("导出文件缺少 format 标记：%v", env["format"])
	}

	if _, err := resetUIScope("all"); err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if loadUI().Theme == "dark" {
		t.Fatal("重置没生效，下面这条比对就没有意义")
	}
	res, err := importUIConfig(data, "overwrite")
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("自己导出的文件里出现了不认识的条目：%v", res.Skipped)
	}
	got := loadUI()
	// 毛玻璃一并验证迁移：存入 "high"（旧写法）归一化后应为 on。
	if got.Theme != "dark" || got.Glass != "on" || got.LogKeep != "1500" {
		t.Errorf("导入后取值不对：theme=%q glass=%q logKeep=%q", got.Theme, got.Glass, got.LogKeep)
	}
}

func TestImportSkipsUnknownKeysAndSaysWhy(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)

	src := `{"format":"mclbx-config","version":1,"app":"mclbx x","ui":{
		"theme":"light","someFutureThing":"on","anotherFutureThing":42}}`
	res, err := importUIConfig([]byte(src), "overwrite")
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if got := loadUI().Theme; got != "light" {
		t.Errorf("认识的字段没被应用：theme=%q", got)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("应当跳过 2 条，实际 %d 条：%v", len(res.Skipped), res.Skipped)
	}
	for _, s := range res.Skipped {
		if s.Why == "" {
			t.Errorf("跳过了 %q 但没说原因 —— 用户只会以为导入失败", s.Key)
		}
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)

	for _, c := range []struct{ name, src, want string }{
		{"空文件", "", "空的"},
		{"不是 JSON", "这不是 json", "不是合法的 JSON"},
		{"别人的 JSON", `{"hello":"world"}`, "不是本程序导出的配置文件"},
		{"来自更新版本", `{"format":"mclbx-config","version":99,"ui":{}}`, "请先升级本程序"},
		{"类型不对", `{"format":"mclbx-config","version":1,"ui":{"theme":123}}`, "类型不对"},
	} {
		_, err := importUIConfig([]byte(c.src), "overwrite")
		if err == nil {
			t.Errorf("%s：本该拒绝，却接受了", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：错误信息里应当讲清原因（含 %q），实际是 %q", c.name, c.want, err.Error())
		}
	}
}

// ---- 重置：只动指定范围 ----

func TestResetOnlyTouchesItsOwnBoard(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)

	if _, err := saveUI(guiUIState{Theme: "dark", Glass: "high", LogKeep: "1500", MusicVol: "20"}); err != nil {
		t.Fatal(err)
	}
	n, err := resetUIScope("主题外观")
	if err != nil {
		t.Fatalf("板块重置失败：%v", err)
	}
	if n == 0 {
		t.Fatal("重置说没有改动 —— 前面明明改过主题与毛玻璃")
	}
	got := loadUI()
	if got.Theme != "auto" || got.Glass != uiGlassDefault {
		t.Errorf("主题外观板块没被重置：theme=%q glass=%q", got.Theme, got.Glass)
	}
	if got.LogKeep != "1500" || got.MusicVol != "20" {
		t.Errorf("重置主题外观时动到了辅助板块：logKeep=%q musicVol=%q", got.LogKeep, got.MusicVol)
	}
	if n, err := resetUIScope("主题外观"); err != nil || n != 0 {
		t.Errorf("第二次重置同样是默认值，应当返回 0 项改动，实际 %d（err=%v）", n, err)
	}
	// 另一个板块名也须通过校验：范围名与 uiFaces 不一致时，
	// 界面点「仅辅助工具」会得到"没有这个板块"，只在真机上才暴露。
	if _, err := resetUIScope("辅助工具"); err != nil {
		t.Errorf("辅助工具板块重置失败：%v", err)
	}
	if _, err := resetUIScope("并不存在的板块"); err == nil {
		t.Error("重置一个不存在的板块竟然成功了")
	}
}

// 存档目录取不到时导出不得退化成相对路径：filepath.Join("", name) 会变成相对当前目录的文件名，
// 等于往用户的任意工作目录里丢文件。
func TestExportRefusesToWriteWithoutDataDir(t *testing.T) {
	withTempConfig(t)
	clearDirCaches()
	oldExe, oldOverride := exePath, archiveDirOverride
	exePath = func() (string, error) { return "", os.ErrNotExist }
	archiveDirOverride = ""
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Cleanup(func() {
		exePath, archiveDirOverride = oldExe, oldOverride
		clearDirCaches()
	})

	if got := dataDir(); got != "" {
		t.Skipf("本机仍然有一个可写位置（%s），这条只能验证到「没有退化成相对路径」", got)
	}
	if p, err := exportUIConfig(); err == nil {
		_ = os.Remove(p)
		t.Errorf("没有存档目录时导出竟然成功了：%s", p)
	}
}
