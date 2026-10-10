package main

// uisettings_test.go 测试设置面板、主题与默认值整合：主题变量完整性、坏值容错、字段默认值优先级。

import (
	"os"
	"strings"
	"testing"
)

func TestRenderGuiPageInjectsSettings(t *testing.T) {
	// 页面骨架不在本仓库（见 frontend/说明.txt）：未装入前端时 / 走占位页，
	// 注入逻辑与界面节拍由前端专用用例在带前端的一侧验证，此处跳过。
	if guiShellHTML == "" {
		t.Skip("未装入前端，跳过页面注入检查")
	}
	withTempConfig(t)
	if _, err := saveUI(guiUIState{Theme: "light", Accent: "blue", Motion: "lite", LogKeep: "500"}); err != nil {
		t.Fatal(err)
	}
	page := renderGuiPage()
	for _, gone := range []string{"@@UIATTRS@@", "@@UIJSON@@"} {
		if strings.Contains(page, gone) {
			t.Errorf("占位符 %s 没有被替换掉", gone)
		}
	}
	for _, want := range []string{
		`data-theme="light"`, `data-accent="blue"`, `data-motion="lite"`,
		`"logKeep":500`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("页面上应当带上 %s", want)
		}
	}
	// 默认状态下也须能正常渲染
	resetGuiConfigCache()
	if _, err := saveUI(guiUIState{}); err != nil {
		t.Fatal(err)
	}
	def := renderGuiPage()
	for _, want := range []string{`data-theme="auto"`, `data-accent="mint"`, `data-motion="full"`} {
		if !strings.Contains(def, want) {
			t.Errorf("默认设置下页面上应当带上 %s", want)
		}
	}
}

// 设置面板入口与相关脚本须存在于页面中。
func TestGuiPageHasSettingsEntry(t *testing.T) {
	if guiShellHTML == "" {
		t.Skip("未装入前端，跳过页面内容检查")
	}
	for _, want := range []string{
		`id="btnSettings"`, "$('btnSettings').onclick = openSettings;",
		"function openSettings", "function uiCollect", "function uiApply",
		"/api/settings", `class="mset"`, ".mdot",
	} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("界面里应当有 %q", want)
		}
	}
}

// 坏值一律回落到默认值，不得使界面崩溃。
func TestUINormalizeFallsBackToDefaults(t *testing.T) {
	got := normalizeUI(guiUIState{
		Theme: "rainbow", Accent: "neon", Motion: "zippy", Remember: "yes",
		LogKeep: "一百万", DefPort: "70000", DefRelay: "  turn:a:3478  ",
	})
	if got.Theme != "auto" || got.Accent != "mint" || got.Motion != "full" {
		t.Errorf("非法取值应当回到默认，得到 %+v", got)
	}
	if got.Remember != "" {
		t.Errorf("Remember 只有 \"0\" 才有意义，得到 %q", got.Remember)
	}
	if got.LogKeep != "" {
		t.Errorf("超出范围的行数应当被丢掉（回到默认），得到 %q", got.LogKeep)
	}
	if got.DefPort != "" {
		t.Errorf("端口超出 1~65535 应当被丢掉，得到 %q", got.DefPort)
	}
	if got.DefRelay != "turn:a:3478" {
		t.Errorf("中转地址应当只做去空白，得到 %q", got.DefRelay)
	}

	ok := normalizeUI(guiUIState{Theme: "dark", Accent: "violet", Motion: "lite",
		Remember: "0", LogKeep: "500", DefPort: "25566", DefRelay: "mclbx://h?tcp=8090"})
	if ok.Theme != "dark" || ok.Accent != "violet" || ok.Motion != "lite" ||
		ok.Remember != "0" || ok.LogKeep != "500" || ok.DefPort != "25566" {
		t.Errorf("合法取值不该被改动，得到 %+v", ok)
	}
}

func TestUILogKeepBounds(t *testing.T) {
	if got := (guiUIState{}).logKeep(); got != uiLogKeepDefault {
		t.Errorf("没配时应当是默认 %d，得到 %d", uiLogKeepDefault, got)
	}
	// 越界值在归一化阶段即被丢弃，故此处经 normalizeUI 读取
	for _, c := range []struct {
		in   string
		want int
	}{
		{"200", 200}, {"20000", 20000},
		{"199", uiLogKeepDefault}, {"20001", uiLogKeepDefault}, {"abc", uiLogKeepDefault},
	} {
		if got := normalizeUI(guiUIState{LogKeep: c.in}).logKeep(); got != c.want {
			t.Errorf("LogKeep=%q 归一化之后应当是 %d，得到 %d", c.in, c.want, got)
		}
	}
}

func TestUISettingsRoundTrip(t *testing.T) {
	path := withTempConfig(t)
	if _, err := saveUI(guiUIState{Theme: "light", Accent: "violet", DefPort: "25566"}); err != nil {
		t.Fatal(err)
	}
	// 丢弃内存缓存，模拟重新打开
	resetGuiConfigCache()
	got := loadUI()
	if got.Theme != "light" || got.Accent != "violet" || got.DefPort != "25566" {
		t.Errorf("设置应当能读回来，得到 %+v", got)
	}
	// 设置与填写值同存一个文件，互不影响
	rememberFieldValues("room", map[string]string{"room": "abc123"})
	resetGuiConfigCache()
	if v, _ := savedField("room", "room"); v != "abc123" {
		t.Errorf("写设置不该把记住的填写内容弄丢，得到 %q", v)
	}
	if loadUI().Theme != "light" {
		t.Error("写填写内容不该把设置弄丢")
	}
	// 删除文件（mclbx forget）后设置也回到默认
	if err := forgetGuiConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("forget 之后文件应当没了，实际 err=%v", err)
	}
	resetGuiConfigCache()
	if loadUI().Theme != "auto" {
		t.Error("forget 之后设置应当回到默认")
	}
}

// 清除仅清空填写内容，不改动外观设置。
func TestForgetInputsKeepsUI(t *testing.T) {
	withTempConfig(t)
	if _, err := saveUI(guiUIState{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	rememberFieldValues("room", map[string]string{"room": "abc123"})

	if err := forgetSavedInputs(); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	if v, ok := savedField("room", "room"); ok && v != "" {
		t.Errorf("填写内容应当被清掉，得到 %q", v)
	}
	if loadUI().Theme != "light" {
		t.Error("清除填写内容不该动主题")
	}
}

// 全局默认值注入各任务字段，但本任务填过的值优先。
func TestGlobalDefaultsFillFieldsButSavedValuesWin(t *testing.T) {
	withTempConfig(t)
	if _, err := saveUI(guiUIState{DefPort: "25566", DefRelay: "turn:a:3478"}); err != nil {
		t.Fatal(err)
	}
	relay := guiField{ID: "relayServer", Default: ""}
	port := guiField{ID: "port", Default: ""}
	if got := fieldDefault("room", relay); got != "turn:a:3478" {
		t.Errorf("中转服务器应当吃到全局默认值，得到 %q", got)
	}
	if got := fieldDefault("room", port); got != "25566" {
		t.Errorf("游戏端口应当吃到全局默认值，得到 %q", got)
	}
	// 任务里填过的优先
	rememberFieldValues("room", map[string]string{"port": "25599", "relayServer": "turn:b:3478"})
	if got := fieldDefault("room", port); got != "25599" {
		t.Errorf("填过的值应当优先于全局默认值，得到 %q", got)
	}
	if got := fieldDefault("room", relay); got != "turn:b:3478" {
		t.Errorf("填过的中转地址应当优先于全局默认值，得到 %q", got)
	}
	// 不相关的字段不受影响
	if got := fieldDefault("room", guiField{ID: "room", Default: "abc"}); got != "abc" {
		t.Errorf("别的字段不该被注入默认值，得到 %q", got)
	}
	// 清空全局默认值后回到任务自身默认
	if _, err := saveUI(guiUIState{}); err != nil {
		t.Fatal(err)
	}
	if got := fieldDefault("punch", guiField{ID: "port", Default: "25565"}); got != "25565" {
		t.Errorf("没有全局默认值时应回到任务默认值，得到 %q", got)
	}
}

// 关闭「记住上次填写的值」后不再写盘。
func TestRememberOffStopsWriting(t *testing.T) {
	withTempConfig(t)
	if _, err := saveUI(guiUIState{Remember: "0"}); err != nil {
		t.Fatal(err)
	}
	rememberFieldValues("room", map[string]string{"room": "abc123"})
	resetGuiConfigCache()
	if v, ok := savedField("room", "room"); ok && v != "" {
		t.Errorf("关掉记住之后不该再写，得到 %q", v)
	}
	// 关掉开关本身是用户的明确动作，不该被这个开关挡掉
	if loadUI().Remember != "0" {
		t.Error("关掉记住之后，这个开关自己得存下来")
	}
	// 打开之后又能写了
	if _, err := saveUI(guiUIState{}); err != nil {
		t.Fatal(err)
	}
	rememberFieldValues("room", map[string]string{"room": "abc123"})
	resetGuiConfigCache()
	if v, _ := savedField("room", "room"); v != "abc123" {
		t.Errorf("打开之后应当又能记住，得到 %q", v)
	}
}

// 「打开界面时自动体检」与 probe 任务的勾为同一存储值，两处界面共用。
func TestAutoProbeIsOneSwitchForBothUIs(t *testing.T) {
	withTempConfig(t)
	if !autoProbeOn() {
		t.Fatal("默认应当是开着的")
	}
	setAutoProbe(false)
	resetGuiConfigCache()
	if autoProbeOn() {
		t.Error("设置里关掉之后，probe 那个勾也必须是关的")
	}
	// 只动 auto，不得连带清掉其它字段
	writeTaskInputs("probe", map[string]string{"port": "25566", "auto": ""})
	setAutoProbe(true)
	resetGuiConfigCache()
	if !autoProbeOn() {
		t.Error("设置里打开之后应当生效")
	}
	if v, _ := savedField("probe", "port"); v != "25566" {
		t.Errorf("改自动体检不该动同一个任务的其他字段，得到 %q", v)
	}
	// 关闭「记住填写内容」后该开关仍可修改
	if _, err := saveUI(guiUIState{Remember: "0"}); err != nil {
		t.Fatal(err)
	}
	setAutoProbe(false)
	resetGuiConfigCache()
	if autoProbeOn() {
		t.Error("「记住填写内容」关掉时，自动体检这个开关仍然要被记住")
	}
}

func TestSettingsPayloadShape(t *testing.T) {
	withTempConfig(t)
	setAutoProbe(false)
	p := settingsPayload(guiUIState{Theme: "light", Accent: "blue", LogKeep: "500"})
	for _, k := range []string{
		"ok", "theme", "accent", "motion", "scale", "remember", "logKeep", "musicPlay",
		"defPort", "defRelay", "autoProbe", "dataDir", "version",
	} {
		if _, ok := p[k]; !ok {
			t.Errorf("设置面板要用的字段 %q 不见了", k)
		}
	}
	if p["autoProbe"] != false {
		t.Errorf("autoProbe 应当跟着 probe 的勾走，得到 %v", p["autoProbe"])
	}
	if p["theme"] != "light" {
		t.Errorf("theme 应当原样带出去，得到 %v", p["theme"])
	}
}

// 设置载荷必须覆盖 guiUIState 的每个可编辑字段，故按字段清单逐项比对而非手写清单。
// 旧配置遗留字段（musicMode / musicLoop）已合并进 musicPlay，不算可编辑设置，故不在其列。
func TestSettingsPayloadCoversEveryUIStateField(t *testing.T) {
	withTempConfig(t)
	p := settingsPayload(guiUIState{})
	for _, name := range uiFieldNames() {
		if _, ok := p[name]; !ok {
			t.Errorf("guiUIState 有可编辑字段 %s，但设置载荷里没有它 —— 面板拿不到它的当前值", name)
		}
	}
}

// 新增设置项的存取往返测试。
func TestScalePersistsAcrossReload(t *testing.T) {
	withTempConfig(t)
	if _, err := saveUI(guiUIState{Scale: "big"}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	if got := loadUI().Scale; got != "big" {
		t.Errorf("界面字号应当能读回来，得到 %q", got)
	}
	// 未定义档位须归一化为 std
	if _, err := saveUI(guiUIState{Scale: "huge"}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	if got := loadUI().Scale; got != "std" {
		t.Errorf("非法档位应当被归一化成 std，得到 %q", got)
	}
}
