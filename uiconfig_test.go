package main

// uiconfig_test.go 覆盖设置面板的两条规约、配置读写、以及重置与变更记录。
//
// 一、面板上每项都须是真正生效的设置，不得有改了无反应的开关。
// 二、只读区不得出现任何可编辑控件。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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

// ---- 规约一：面板上只有生效的设置 ----

// 面板上每个可编辑控件都须有服务端字段（登记在 uiFieldLabels 里）；
// 登记过的每一项也须出现在面板上，guiUIState 的每个字段也须登记过。
// 三向须对齐，任一侧多出或缺失都判为失败。
func TestPanelOnlyShowsRealSettings(t *testing.T) {
	panel := bodyBetween(t, "function uiThemeHTML(u){", "function uiAboutHTML(u){")
	keyRe := regexp.MustCompile(`ui(?:Seg|Sw|Field|Range)\('([A-Za-z]+)'`)
	seen := map[string]bool{}
	for _, m := range keyRe.FindAllStringSubmatch(panel, -1) {
		seen[m[1]] = true
	}
	// 强调色用圆点控件而非分段控件，单独识别
	if strings.Contains(panel, "uiAccentDots(u)") {
		seen["accent"] = true
	}
	// 背景图用自定义控件 uiWallPicker（下拉选图 + 导入）。
	// 既须确认面板调用了它，也须确认它绑在 bgImage 字段上，只认前者会漏掉控件换字段名的情况。
	if strings.Contains(panel, "uiWallPicker(") {
		picker := bodyBetween(t, "function uiWallPicker(u){", "function uiMusicList(u){")
		if !strings.Contains(picker, `data-in="bgImage"`) {
			t.Error("背景图控件没有绑在 bgImage 字段上 —— 在那里选图不会落到任何设置上")
		}
		seen["bgImage"] = true
	}
	// 面板里若有内联的输入控件，也一并算上
	for _, m := range regexp.MustCompile(`data-in="([A-Za-z]+)"`).FindAllStringSubmatch(panel, -1) {
		seen[m[1]] = true
	}
	// 自定义滑杆控件同理：识别它的 data- 钩子。
	for _, m := range regexp.MustCompile(`data-rng="([A-Za-z]+)"`).FindAllStringSubmatch(panel, -1) {
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("一个可编辑控件都没解析出来 —— 面板函数改名了，这条检查等于没做")
	}

	for k := range seen {
		if _, ok := uiFieldLabels[k]; !ok {
			t.Errorf("面板上有可编辑项 %q，但它不在 uiFieldLabels 里 —— "+
				"要么它没有服务端字段（那就是个改了没反应的假开关），要么忘了登记", k)
		}
	}
	for f, info := range uiFieldLabels {
		if !seen[f] {
			t.Errorf("清单里的 %s（%s / %s）没有出现在面板上 —— 有字段却没人找得到它", f, info.Face, info.Name)
		}
	}
	for _, f := range uiFieldNames() {
		if _, ok := uiFieldLabels[f]; !ok {
			t.Errorf("guiUIState 有字段 %q 却不在 uiFieldLabels 里 —— 它会生效，但清单上查不到", f)
		}
	}
}

// ---- 规约三：面板上的每个按钮都有处理者 ----

// 面板里带 data-act 的按钮都须落到真实处理者：客户端的 uiAction / uiBind，
// 或服务端 /api/settings 的 action 分支。三处都不认的按钮点了不会发生任何事。
func TestEveryPanelActionHasAHandler(t *testing.T) {
	client := bodyBetween(t, "async function uiAction(a, btn){", "\n/* 起手：先按地址栏的 hash")
	bind := bodyBetween(t, "function uiBind(){", "function uiCollect(){")

	// 服务端的 action 分支：只取 settingsSave 里的 switch，不纳入文件中其它 switch
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatalf("读不到 gui.go：%v", err)
	}
	server := ""
	if i := strings.Index(string(src), "switch req.Action {"); i >= 0 {
		if j := strings.Index(string(src)[i:], "\n\t\t}"); j >= 0 {
			server = string(src)[i : i+j]
		}
	}
	if server == "" {
		t.Fatal("没能从 gui.go 里抠出 /api/settings 的 action 分支 —— 这条检查会误报，先修它")
	}

	acts := map[string]bool{}
	for _, m := range regexp.MustCompile(`data-act="([A-Za-z]+)"`).FindAllStringSubmatch(guiPageHTML, -1) {
		acts[m[1]] = true
	}
	if len(acts) == 0 {
		t.Fatal("面板里一个 data-act 都没解析出来 —— 这条检查等于没做")
	}
	for act := range acts {
		handled := strings.Contains(client, "'"+act+"'") ||
			strings.Contains(bind, "'"+act+"'") ||
			strings.Contains(server, `case "`+act+`":`)
		if !handled {
			t.Errorf("面板上的按钮 data-act=%q 没有任何处理者 —— 点了不会有任何反应", act)
		}
	}
}

// 分段控件的取值须与服务端白名单一致。
//
// 界面多一个取值：选了它不生效（归一化打回默认）。
// 服务端多一个取值：该档位永远选不到。字号另见 scale_test.go。
func TestNewSegmentOptionsMatchTheWhitelist(t *testing.T) {
	for _, c := range []struct {
		key  string
		want []string
	}{
		{"radii", radiiValues},
		{"rail", railValues},
	} {
		got := segOptions(t, c.key)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("界面上的 %s 选项 %v 与 normalizeUI 的白名单 %v 对不上 —— "+
				"多一个就是死档位，少一个就是选不到", c.key, got, c.want)
		}
	}
}

// ---- 规约二：只读区不得有可编辑控件 ----

func TestReadonlyBlockHasNoEditableControls(t *testing.T) {
	ro := bodyBetween(t, "function uiAboutHTML(u){", "function uiChangesHTML(u){")
	for _, bad := range []string{"uiSeg(", "uiSw(", "uiField(", "uiItem(", "data-in=", "data-sw=", "data-seg=", "data-accent="} {
		if strings.Contains(ro, bad) {
			t.Errorf("「关于与状态」里出现了 %s —— 这一块是纯展示，不许留任何编辑入口", bad)
		}
	}
	// 反向确认这段确实在渲染内容，而非空函数
	if !strings.Contains(ro, "uiInfoRow(") || !strings.Contains(ro, "uiStat(") {
		t.Error("只读区没有渲染任何信息行 —— 这条检查等于没做")
	}
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

// ---- 变更记录 ----

func TestChangesRecordBoardAndItemName(t *testing.T) {
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
	if c.Module != "主题外观" || c.Item != "主题" {
		t.Errorf("记录没写清改的是哪一项：板块=%q 选项=%q", c.Module, c.Item)
	}
	if c.From == "" || c.To != "dark" || c.At == "" {
		t.Errorf("记录内容不完整：from=%q to=%q at=%q", c.From, c.To, c.At)
	}
	if _, err := saveUI(guiUIState{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if n := len(loadChanges()); n != 1 {
		t.Errorf("取值没变却又记了一条，现在共 %d 条", n)
	}
	if n := clearUIChanges(); n != 1 {
		t.Errorf("清空说清掉了 %d 条，实际记录里有 1 条", n)
	}
}

// 存档目录取不到时，变更记录与导出都不得退化成相对路径。
// filepath.Join("", "ui-changes.json") 会变成相对当前目录的文件名，
// 等于往用户的任意工作目录里丢文件。
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
