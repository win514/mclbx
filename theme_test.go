package main

// theme_test.go 主题的机械护栏（变量接线与主题/强调色的对应关系）。

import (
	"regexp"
	"strings"
	"testing"
)

// cssBlockOf 取出某个选择器的声明体，选择器不存在时报错。
func cssBlockOf(t *testing.T, css, selector string) string {
	t.Helper()
	i := strings.Index(css, selector+"{")
	if i < 0 {
		t.Fatalf("CSS 里找不到 %s{ —— 它被改名或删掉了吗？", selector)
	}
	body, ok := matchBraces(css[i+len(selector):])
	if !ok {
		t.Fatalf("%s 的花括号不配对，没法检查", selector)
	}
	return body
}

func cssBlock(t *testing.T, selector string) string {
	t.Helper()
	return cssBlockOf(t, guiPageHTML, selector)
}

// hasBlock 判断某个选择器块存不存在。
func hasBlock(css, selector string) bool {
	return strings.Contains(css, selector+"{")
}

// cssVars 抽出声明体里的自定义属性（只认 --名字:），先去掉注释再按分号切。
func cssVars(block string) map[string]string {
	block = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(block, "")
	out := map[string]string{}
	for _, part := range strings.Split(block, ";") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "--") {
			continue
		}
		if i := strings.Index(part, ":"); i > 0 {
			out[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
		}
	}
	return out
}

// themeBlock 返回主题对应的选择器；深色返回空串（:root 就是深色，没有自己的块）。
func themeBlock(theme string) string {
	if theme == "dark" {
		return ""
	}
	return `:root[data-theme="` + theme + `"]`
}

func accentBlock(accent string) string {
	if accent == "mint" {
		return "" // mint 就是 :root 里的默认值
	}
	return `:root[data-accent="` + accent + `"]`
}

func lightAccentBlock(accent string) string {
	return `:root[data-theme="light"][data-accent="` + accent + `"]`
}

// resolveVars 按真实层叠顺序合并取值：:root → 强调色 → 主题 → 主题×强调色，顺序须与 CSS 一致。
func resolveVars(t *testing.T, theme, accent string) map[string]string {
	t.Helper()
	out := cssVars(cssBlock(t, ":root"))
	for _, sel := range []string{accentBlock(accent), themeBlock(theme)} {
		if sel == "" || !hasBlock(guiPageHTML, sel) {
			continue
		}
		for k, v := range cssVars(cssBlock(t, sel)) {
			out[k] = v
		}
	}
	// 复合块（浅色 × 强调色）：
	sel := ""
	if theme != "dark" && accent != "mint" {
		sel = `:root[data-theme="` + theme + `"][data-accent="` + accent + `"]`
	}
	if sel != "" && hasBlock(guiPageHTML, sel) {
		for k, v := range cssVars(cssBlock(t, sel)) {
			out[k] = v
		}
	}
	return out
}

// jsListIDs 从 JS 选项表里取出 id。
func jsListIDs(t *testing.T, fn string) []string {
	t.Helper()
	i := strings.Index(guiPageHTML, "function "+fn+"()")
	if i < 0 {
		t.Fatalf("找不到 %s() —— 选项表被改名了吗？", fn)
	}
	body := guiPageHTML[i:]
	if j := strings.Index(body, "}"); j > 0 {
		body = body[:j]
	}
	var out []string
	for _, m := range regexp.MustCompile(`\[\s*'([a-z]+)'\s*,`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s() 里一个选项都没解析出来", fn)
	}
	return out
}

// sharedVars 是不归任何主题私有的变量：尺寸与字体、强调色的半透明值，以及运行期注入的背景图片地址。
var sharedVars = map[string]bool{
	"--s1": true, "--s2": true, "--s3": true, "--s4": true, "--s5": true,
	"--r1": true, "--r2": true, "--r3": true, "--pill": true,
	"--disp": true, "--ui": true, "--mono": true,
	"--wall":     true,
	"--sig-soft": true, "--sig-edge": true, "--sig-ring": true,
	"--sig-focus": true, "--sig-live": true,
	// 动效令牌也不是主题私有：时长与缓动是全局的。
	"--d1": true, "--d2": true, "--d3": true, "--ease": true,
	// 毛玻璃那一族同理：颜色由各主题的 --glass-rgb 决定，不透明度与模糊由开态块给；
	// 唯一覆盖它们的是高对比主题，相关约束见 TestGlassOverPageKeepsTextReadable。
	"--glass-1": true, "--glass-2": true, "--glass-3": true,
	"--g1a": true, "--g2a": true, "--g3a": true,
	"--glass-blur": true, "--blur-3": true,
	"--w1": true, "--w2": true, "--w3": true,
}

// isSharedVar 在 sharedVars 之外，按前缀认领 --fs-*（字号）与 --row-*（栏高）。
func isSharedVar(name string) bool {
	return strings.HasPrefix(name, "--fs-") || strings.HasPrefix(name, "--row-") || sharedVars[name]
}

// accentOwnedVars 由强调色块给值，主题只准在为浅底重调实心色时覆盖。
var accentOwnedVars = map[string]bool{
	"--sig": true, "--sig-deep": true, "--sig-glow": true,
	"--sel": true, "--pre-bg": true, "--aurora-a": true,
}

// 每个主题都必须声明全部主题私有变量。
func TestEveryThemeCoversEveryThemeOwnedVariable(t *testing.T) {
	dark := cssVars(cssBlock(t, ":root"))
	if len(dark) < 40 {
		t.Fatalf(":root 只解析出 %d 个变量，选择器或写法变了吧", len(dark))
	}
	for _, theme := range themeValues {
		sel := themeBlock(theme)
		if sel == "" {
			continue // 深色就是 :root，下面单独查
		}
		blk := cssVars(cssBlock(t, sel))
		for name := range dark {
			if isSharedVar(name) || accentOwnedVars[name] {
				continue
			}
			if _, ok := blk[name]; !ok {
				t.Errorf("主题 %s 没有声明 %s —— 那一块会留在上一个主题的颜色上（不报错、不崩）", theme, name)
			}
		}
		// 反方向：块里出现 :root 没有的名字，多半是拼错。
		for name := range blk {
			if _, ok := dark[name]; !ok {
				t.Errorf("主题 %s 里出现了 :root 中不存在的变量 %s —— 拼错名字了？", theme, name)
			}
		}
	}
	// 确认例外表里的名字都存在于 :root。
	for name := range sharedVars {
		if _, ok := dark[name]; !ok {
			t.Errorf("共用清单里的 %s 在 :root 里根本不存在，该从清单里删掉", name)
		}
	}
	for name := range accentOwnedVars {
		if _, ok := dark[name]; !ok {
			t.Errorf("强调色私有清单里的 %s 在 :root 里根本不存在，该从清单里删掉", name)
		}
	}
}

// themeOverridableShared 是"共用、但允许某个主题覆盖"的一小撮，目前只有毛玻璃三档不透明度。
// 高对比主题须把它们钉回接近实心；除这三个名字外，共用变量不许被主题重新定义。
var themeOverridableShared = map[string]bool{"--g1a": true, "--g2a": true, "--g3a": true}

// 共用变量不许被任何主题块重新定义（上面那三个例外除外）。
func TestSharedVarsAreNotRedefinedByThemes(t *testing.T) {
	for _, theme := range themeValues {
		sel := themeBlock(theme)
		if sel == "" {
			continue
		}
		for name := range cssVars(cssBlock(t, sel)) {
			if isSharedVar(name) && !themeOverridableShared[name] {
				t.Errorf("共用变量 %s 被主题 %s 重新定义了 —— 它要么该归主题私有（补进共用清单的对面），要么这次覆盖是绕开检查的补丁", name, theme)
			}
		}
	}
}

// 复合块（主题 × 强调色）只准调实心色那一小撮变量。
func TestCompoundBlocksOnlyTouchTheAgreedVars(t *testing.T) {
	allowed := map[string]bool{
		"--sig": true, "--sig-deep": true, "--sig-glow": true,
		"--sel": true, "--pre-bg": true, "--aurora-a": true,
	}
	re := regexp.MustCompile(`:root\[data-theme="[a-z]+"\]\[data-accent="[a-z]+"\]\{`)
	locs := re.FindAllStringIndex(guiPageHTML, -1)
	if len(locs) == 0 {
		t.Fatal("一个复合块都没有 —— 浅色 × 强调色那两条规则去哪了？")
	}
	for _, loc := range locs {
		sel := guiPageHTML[loc[0] : loc[1]-1]
		for name := range cssVars(cssBlock(t, sel)) {
			if !allowed[name] {
				t.Errorf("%s 里调了 %s —— 复合块只准调实心色那一小撮，否则组合数会炸开", sel, name)
			}
		}
	}
}

// 界面上能选的主题/强调色、normalizeUI 白名单、CSS 里存在的块，三者必须一致。
func TestThemeAndAccentOptionsMatchTheCSS(t *testing.T) {
	// 主题：选项表 = auto + 白名单；除深色外每个都要有块。
	themeIDs := jsListIDs(t, "uiThemes")
	if themeIDs[0] != "auto" {
		t.Errorf("主题选项的第一项应当是 auto，实际 %q", themeIDs[0])
	}
	var fromUI []string
	for _, id := range themeIDs[1:] {
		fromUI = append(fromUI, id)
	}
	if strings.Join(fromUI, ",") != strings.Join(themeValues, ",") {
		t.Errorf("界面上的主题选项 %v 与 normalizeUI 的白名单 %v 对不上", fromUI, themeValues)
	}
	for _, theme := range themeValues {
		sel := themeBlock(theme)
		if sel == "" {
			if hasBlock(guiPageHTML, `:root[data-theme="dark"]`) {
				t.Error("深色不该有自己的块（:root 就是它）—— 多一份副本就多一个会漂的地方")
			}
			continue
		}
		if !hasBlock(guiPageHTML, sel) {
			t.Errorf("主题 %s 在白名单里、界面上也能选，但 CSS 里没有 %s", theme, sel)
		}
	}
	// 强调色：选项表 = mint + 白名单；非 mint 的都要有深色块与浅色复合块。
	accIDs := jsListIDs(t, "uiAccents")
	if accIDs[0] != "mint" {
		t.Errorf("强调色选项的第一项应当是默认的 mint，实际 %q", accIDs[0])
	}
	var accFromUI []string
	for _, id := range accIDs[1:] {
		accFromUI = append(accFromUI, id)
	}
	if strings.Join(accFromUI, ",") != strings.Join(accentValues, ",") {
		t.Errorf("界面上的强调色 %v 与 normalizeUI 的白名单 %v 对不上", accFromUI, accentValues)
	}
	for _, acc := range accentValues {
		if !hasBlock(guiPageHTML, accentBlock(acc)) {
			t.Errorf("强调色 %s 没有它自己的块（深色那一支）", acc)
		}
		// 浅色复合块：每个强调色都要单独给一个更深的实心色。
		if !hasBlock(guiPageHTML, lightAccentBlock(acc)) {
			t.Errorf("强调色 %s 缺少浅色复合块 %s —— 浅底上会用到深色那一支的亮色", acc, lightAccentBlock(acc))
		}
	}
}

// 强调色与语义色（警告/错误）不许撞。
func TestAccentNeverCollidesWithSemanticColors(t *testing.T) {
	for _, theme := range themeValues {
		for _, acc := range append([]string{"mint"}, accentValues...) {
			v := resolveVars(t, theme, acc)
			if v["--sig"] == v["--amber"] {
				t.Errorf("主题 %s + 强调色 %s：强调色与警告色是同一个值 %s", theme, acc, v["--sig"])
			}
			if v["--sig"] == v["--rose"] {
				t.Errorf("主题 %s + 强调色 %s：强调色与错误色是同一个值 %s", theme, acc, v["--sig"])
			}
		}
	}
}

// 每个「主题 × 强调色」组合，每个变量都要能取到值。
func TestEveryThemeAccentCombinationResolves(t *testing.T) {
	base := cssVars(cssBlock(t, ":root"))
	themes := append([]string{"dark"}, themeValues...) // dark 与 themeValues 里的 dark 重复无妨
	accents := append([]string{"mint"}, accentValues...)
	for _, theme := range themes {
		for _, acc := range accents {
			v := resolveVars(t, theme, acc)
			for name := range base {
				if _, ok := v[name]; !ok {
					t.Errorf("主题 %s + 强调色 %s：%s 取不到值", theme, acc, name)
				}
			}
		}
	}
	// 深色（默认）与「选了 dark」必须完全一致。
	if a, b := resolveVars(t, "dark", "mint"), cssVars(cssBlock(t, ":root")); len(a) != len(b) {
		t.Error("data-theme=dark 的结果与 :root 不一致 —— 深色这个取值必须是「什么都不覆盖」")
	}
}

// 用到的每个变量都必须有人定义。
func TestNoNonexistentVariablesInCSS(t *testing.T) {
	declared := map[string]bool{}
	for name := range cssVars(cssBlock(t, ":root")) {
		declared[name] = true
	}
	for _, theme := range themeValues {
		if sel := themeBlock(theme); sel != "" {
			for name := range cssVars(cssBlock(t, sel)) {
				declared[name] = true
			}
		}
	}
	for _, acc := range accentValues {
		for _, sel := range []string{accentBlock(acc), lightAccentBlock(acc)} {
			if hasBlock(guiPageHTML, sel) {
				for name := range cssVars(cssBlock(t, sel)) {
					declared[name] = true
				}
			}
		}
	}
	// 字号档也会声明变量，一并收集。
	for _, name := range scaleValues {
		if name == "std" {
			continue
		}
		if sel := `:root[data-scale="` + name + `"]`; hasBlock(guiPageHTML, sel) {
			for v := range cssVars(cssBlock(t, sel)) {
				declared[v] = true
			}
		}
	}
	// 毛玻璃同理：--glass-blur 与三档不透明度按开态声明；选择器须照抄 :not([data-theme="contrast"])。
	for _, name := range glassValues {
		if name == uiGlassOff {
			continue // off 是关闭，没有属于自己的块
		}
		sel := `:root[data-glass="` + name + `"]:not([data-theme="contrast"])`
		if hasBlock(guiPageHTML, sel) {
			for v := range cssVars(cssBlock(t, sel)) {
				declared[v] = true
			}
		}
	}
	// 任何一处 `:root{...}` 都算定义；视觉美化那段自带的 :root 块须留在段内，删段时不留孤儿。
	for _, m := range regexp.MustCompile(`:root\{([^}]*)\}`).FindAllStringSubmatch(guiPageHTML, -1) {
		for v := range cssVars(m[1]) {
			declared[v] = true
		}
	}
	for _, m := range regexp.MustCompile(`var\((--[a-z0-9-]+)\)`).FindAllStringSubmatch(guiPageHTML, -1) {
		if !declared[m[1]] {
			t.Errorf("用了 var(%s)，但整份样式表里没有定义它", m[1])
		}
	}
}

// 写死在界面各处的底色/字色不许再回来，应走变量。
func TestNoHardcodedColorsOutsideTheTokens(t *testing.T) {
	marker := `data-backdrop="off"`
	i := strings.Index(guiPageHTML, marker)
	if i < 0 {
		t.Fatal("找不到背景光效那条规则 —— 主题块的边界判断失效了")
	}
	rest := guiPageHTML[i:]
	banned := []string{
		"#090E14", "#0A1119", "#07231A", "#1E3040", "#2A4356", "#062A1E", "#06120C",
		"#E9F2FB", "#74889C", "#A9BCCF", "#DDFFF2", "#E4DDFF",
		"#7CF0C4", "#FFB0BE", "#FFD79A", "#A8CDFF",
		"#FFDFAE", "#3A2A0E", "#FFC9D2", "#4A2230", "#D9FFF1", "#FFD3DA",
	}
	for _, c := range banned {
		if strings.Contains(rest, c) {
			t.Errorf("%s 又写回到规则里了 —— 它应当走变量（见 :root 那一组）", c)
		}
	}
	if !strings.Contains(guiPageHTML, "--chrome:") {
		t.Error("顶栏/底栏的 --chrome 不见了")
	}
}

// 「跟随系统」须由服务端先写入属性以免首屏闪色，再由脚本解析系统偏好。
func TestThemeIsResolvedBeforeFirstPaint(t *testing.T) {
	headEnd := strings.Index(guiPageHTML, "<style>")
	if headEnd < 0 {
		t.Fatal("找不到 <style>")
	}
	head := guiPageHTML[:headEnd]
	for _, want := range []string{"@@UIATTRS@@", "prefers-color-scheme: light", "MCLBX_THEME"} {
		if !strings.Contains(head, want) {
			t.Errorf("首屏脚本里应当有 %q", want)
		}
	}
	if !strings.Contains(head, "@@UIJSON@@") {
		t.Error("解析主题的脚本跑在样式表之后了")
	}
}

// 说明书是第二个文档：有自己的变量与浅色底，须有暗色分支并覆盖其 token。
func TestManualHasDarkBranchAndCoversItsTokens(t *testing.T) {
	light := cssVars(cssBlockOf(t, manualHTML, ":root"))
	if len(light) < 10 {
		t.Fatalf("说明书的 :root 只解析出 %d 个变量", len(light))
	}
	dark := cssVars(cssBlockOf(t, manualHTML, `:root[data-theme="dark"]`))
	// 字体与主题无关，只写一份。
	shared := map[string]bool{"--mono": true, "--sans": true}
	for name := range light {
		if shared[name] {
			continue
		}
		if _, ok := dark[name]; !ok {
			t.Errorf("说明书的暗色分支没给 %s —— 那一块会留在浅色上", name)
		}
	}
	for name := range dark {
		if _, ok := light[name]; !ok {
			t.Errorf("说明书的暗色分支里出现了浅色没有的变量 %s", name)
		}
	}
	// 打印一律浅色。
	if !strings.Contains(manualHTML, "@media print") {
		t.Error("说明书少了打印样式")
	}
	if !regexp.MustCompile(`(?s)@media print\{.*?:root\[data-theme="dark"\]`).MatchString(manualHTML) {
		t.Error("打印时要强制回浅色 —— 否则深色下打出来是一整页黑底")
	}
}

// 说明书的主题由查询串带进来，认不出来按浅色。
func TestServeManualHonorsTheme(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"light":    "",
		"dark":     ` data-theme="dark"`,
		"contrast": ` data-theme="dark"`,
		"DARK":     ` data-theme="dark"`,
		"rainbow":  "",
	}
	for in, want := range cases {
		if got := manualThemeAttr(in); got != want {
			t.Errorf("manualThemeAttr(%q) = %q，期望 %q", in, got, want)
		}
	}
	// 页面上的占位符必须存在，否则替换是空操作。
	if !strings.Contains(manualHTML, "@@MANUAL_THEME@@") {
		t.Error("manual.html 里没有主题占位符")
	}
	// 打开说明书时要带上解析后的主题（non-auto）。
	if !strings.Contains(guiPageHTML, `'/manual?theme=' + encodeURIComponent(resolvedTheme())`) {
		t.Error("界面打开说明书时没有把解析后的主题带上 —— 深色下会是一整页白")
	}
}
