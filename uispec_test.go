package main

// uispec_test.go —— 覆盖「设置全集 → 面板 → 配置读写」这一条链。
//
// 三块内容：
//   · 设计清单与实现对照表之间不能失联（条目标了可操作，界面却渲染不出来）；
//   · 面板一次只渲染一个模块，由此带来的那条最危险的后果（保存时把别的模块打回默认）；
//   · 导出 / 导入 / 重置 / 变更记录 的实际行为。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempArchive 把存档目录指到临时目录：导出文件与变更记录都落在那里。
func withTempArchive(t *testing.T) string {
	t.Helper()
	old := archiveDirOverride
	dir := t.TempDir()
	archiveDirOverride = dir
	oldChanges := uiChangeFile
	uiChangeFile = func() string { return filepath.Join(dir, "ui-changes.json") }
	t.Cleanup(func() {
		archiveDirOverride = old
		uiChangeFile = oldChanges
	})
	return dir
}

// 对照表里的每一条都必须能在设计清单里找到同名条目。
//
// 这是两处最容易失联的地方：改了文档里的条目名、或者把模块名写错一个字，
// 症状都是"那一行静默消失" —— 面板不报错，只是不再显示它。
func TestEveryImplItemExistsInTheSpec(t *testing.T) {
	known := map[string]map[string]bool{}
	for _, m := range uiSpecModules {
		set := map[string]bool{}
		for _, it := range m.Items {
			set[it.Name] = true
		}
		known[m.Name] = set
	}
	if len(known) == 0 {
		t.Fatal("设计清单是空的 —— 生成文件没进编译？这条检查等于没做")
	}

	n := 0
	for module, list := range uiImplByModule {
		set, ok := known[module]
		if !ok {
			t.Errorf("对照表里的模块名 %q 在生成清单里不存在 —— 这一整个模块的条目都不会显示", module)
			continue
		}
		for _, im := range list {
			n++
			if !set[im.Item] {
				t.Errorf("对照表里写了「%s / %s」，但生成清单里没有这一条 —— "+
					"两边名字对不上，界面上这一行会静默消失", module, im.Item)
			}
		}
	}
	for module, list := range uiExtraByModule {
		if _, ok := known[module]; !ok {
			t.Errorf("自有条目挂在了不存在的模块 %q 上", module)
		}
		n += len(list)
	}
	if n == 0 {
		t.Fatal("一条已实现的对照都没有 —— 这条检查等于没做")
	}
}

// 面板上「可用」的条目数必须与对照表算出来的一致：两边都靠同一份数据，
// 多算一条就会出现"显示可用、点下去是空白"。
func TestSpecLiveCountsMatchTheImplTable(t *testing.T) {
	groups := uiSpecForWeb()
	if len(groups) != len(uiSpecModules) {
		t.Fatalf("面板拿到 %d 个模块，清单里有 %d 个", len(groups), len(uiSpecModules))
	}

	totalLive, totalItems := 0, 0
	for _, g := range groups {
		totalItems += len(g.Items)
		totalLive += g.Live

		want := 0
		for _, im := range uiImplsFor(g.Name) {
			if im.Ctl != "chrome" { // chrome 由标题栏与底栏承担，不显示在条目列表里
				want++
			}
		}
		if g.Live != want {
			t.Errorf("%s：面板说 %d 项可操作，对照表算出 %d 项", g.Name, g.Live, want)
		}
		// 每条自有的条目都要带着 key 与 ctl，否则渲染成空白
		for _, it := range g.Items {
			if it.Extra && (it.Key == "" || it.Ctl == "") {
				t.Errorf("%s / %s：自有条目没有 key 或 ctl，会渲染成空白", g.Name, it.Name)
			}
		}
	}
	if totalLive == 0 || totalItems == 0 {
		t.Fatalf("清单统计异常：条目 %d、可操作 %d", totalItems, totalLive)
	}
	if totalItems < 287 {
		t.Errorf("清单只剩 %d 条，设计文档里是 287 条 —— 生成文件是不是被截断了", totalItems)
	}
}

// 面板一次只渲染一个模块，所以收集设置时**必须以服务端那份状态为起点**。
//
// 起点若是一串硬编码默认值，那么在任何一个模块里改动一项再保存，
// 其它模块的设置会被一起打回默认 —— 那等于把"改个字号"变成"重置全部"。
func TestUICollectStartsWithTheServerState(t *testing.T) {
	collect := bodyBetween(t, "function uiCollect", "function uiApply")
	if !strings.Contains(collect, "var u = ui || {}") {
		t.Fatal("uiCollect() 的起点不是服务端那份 ui —— 改一个模块会把其它模块打回默认")
	}
	// 反向确认：面板确实只渲染当前模块（这条前提成立，上面那条约束才有意义）
	mod := bodyBetween(t, "function uiModHTML(g, u){", "/* 搜索结果")
	if !strings.Contains(mod, "g.items") {
		t.Fatal("uiModHTML 没有按模块渲染条目 —— 面板结构与这条约束对不上了")
	}
}

// 导出→导入 应当原样回来。
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
	// 导出必须是给人看的：带缩进、带格式标记
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

	// 打回默认，再导入，取值应当回来
	if _, err := resetUIScope("all"); err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if got := loadUI().Theme; got == "dark" {
		t.Fatal("重置之后主题还是 dark —— 重置没生效，下面这条比对就没有意义")
	}
	res, err := importUIConfig(data, "overwrite")
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("自己导出的文件里出现了不认识的条目：%v", res.Skipped)
	}
	if res.Applied == 0 {
		t.Error("导入成功但一项都没应用")
	}
	got := loadUI()
	if got.Theme != "dark" || got.Glass != "high" || got.LogKeep != "1500" {
		t.Errorf("导入后取值不对：theme=%q glass=%q logKeep=%q", got.Theme, got.Glass, got.LogKeep)
	}
}

// 文件里有本程序不认识的条目：跳过它、应用其余、并逐条说明跳过了什么。
func TestImportSkipsUnknownKeysAndSaysWhy(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)

	src := `{"format":"mclbx-config","version":1,"app":"mclbx x","ui":{
		"theme":"light",
		"someFutureThing":"on",
		"anotherFutureThing":42
	}}`
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
			t.Errorf("跳过了 %q 但没说原因 —— 用户看到只会以为导入失败", s.Key)
		}
	}
}

// 三种不该被接受的文件：不是 JSON、不是本程序导出的、类型不对。
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

// 模块级重置只动这个模块的字段，别的模块一个都不许碰。
func TestResetOnlyTouchesItsOwnModule(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)

	if _, err := saveUI(guiUIState{Theme: "dark", Glass: "high", LogKeep: "1500", MusicVol: "20"}); err != nil {
		t.Fatal(err)
	}
	n, err := resetUIScope("界面与外观设置")
	if err != nil {
		t.Fatalf("模块重置失败：%v", err)
	}
	if n == 0 {
		t.Fatal("模块重置说没有改动 —— 前面明明改过主题与毛玻璃")
	}
	got := loadUI()
	if got.Theme != "auto" || got.Glass != uiGlassDefault {
		t.Errorf("外观模块没被重置：theme=%q glass=%q", got.Theme, got.Glass)
	}
	if got.LogKeep != "1500" {
		t.Errorf("重置外观把「基础行为」里的日志行数也改了：%q", got.LogKeep)
	}
	if got.MusicVol != "20" {
		t.Errorf("重置外观把音乐音量也改了：%q", got.MusicVol)
	}

	// 已经是默认值时不该报"改了几项"，也不该无意义地写盘
	if n, err := resetUIScope("界面与外观设置"); err != nil || n != 0 {
		t.Errorf("第二次重置同样是默认值，应当返回 0 项改动，实际 %d（err=%v）", n, err)
	}
	// 不存在的模块要明确报错，而不是悄悄什么都不做
	if _, err := resetUIScope("并不存在的模块"); err == nil {
		t.Error("重置一个不存在的模块竟然成功了")
	}
}

// 存档目录取不到时，变更记录与导出都不许退化成相对路径。
//
// filepath.Join("", "ui-changes.json") 会变成一个相对当前目录的文件名 ——
// 那等于往用户的任意工作目录里丢文件。配置那边有同一条约束，
// 而记账是挂在保存设置上的，所以这条更容易被顺手破坏。
func TestChangeLogAndExportRefuseToWriteWithoutDataDir(t *testing.T) {
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

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(cwd, "ui-changes.json")
	_ = os.Remove(stray)

	// 保存设置本身不该失败：记账是辅助行为，写不了就少一条
	if _, err := saveUI(guiUIState{Theme: "dark"}); err != nil {
		t.Fatalf("存档目录不可用时保存设置不该失败：%v", err)
	}
	if _, err := os.Stat(stray); err == nil {
		_ = os.Remove(stray)
		t.Error("变更记录被写进了当前工作目录 —— 存档目录取不到时应当干脆不写")
	}

	if p, err := exportUIConfig(); err == nil {
		_ = os.Remove(p)
		t.Errorf("没有存档目录时导出竟然成功了：%s", p)
	}
	if n := clearUIChanges(); n != 0 {
		t.Errorf("没有存档目录时清空记录应当返回 0，实际 %d", n)
	}
}

// 变更记录：改动要记得下模块与选项名，清空要能清掉。
func TestChangesRecordModuleAndItemName(t *testing.T) {
	withTempConfig(t)
	withTempArchive(t)
	clearUIChanges()

	if _, err := saveUI(guiUIState{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	list := loadChanges()
	if len(list) != 1 {
		t.Fatalf("改了一项，记录里应当有 1 条，实际 %d 条：%v", len(list), list)
	}
	c := list[0]
	if c.Module != "界面与外观设置" || c.Item != "主题" {
		t.Errorf("记录没写清改的是哪一项：模块=%q 选项=%q", c.Module, c.Item)
	}
	if c.From == "" || c.To != "dark" {
		t.Errorf("记录的前后取值不对：from=%q to=%q", c.From, c.To)
	}
	if c.At == "" {
		t.Error("记录没有时间")
	}

	// 没改动就不该多记一条
	if _, err := saveUI(guiUIState{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if n := len(loadChanges()); n != 1 {
		t.Errorf("取值没变却又记了一条，现在共 %d 条", n)
	}

	if n := clearUIChanges(); n != 1 {
		t.Errorf("清空说清掉了 %d 条，实际记录里有 1 条", n)
	}
	if n := len(loadChanges()); n != 0 {
		t.Errorf("清空之后还剩 %d 条", n)
	}
}
