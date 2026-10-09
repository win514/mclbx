package main

// appearance_test.go —— 三项外观能力的护栏：扁平化、毛玻璃、背景图片。
//
// 这个文件里的断言都围绕同一件事：**界面在空闲时不能有持续的 GPU / CPU 开销**。
// 项目里那条实测红线是「空闲 13%、运行中 51% 来自一个占满视口又带模糊或动画的图层」，
// 下面每一条守卫都是把那条红线写成可执行的判据。

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// withTempGallery 把图库目录指到临时目录 —— 没有它，旧路径的自动采纳会往真实的存档里写文件。
func withTempGallery(t *testing.T) string {
	t.Helper()
	old := wallGalleryDir
	dir := t.TempDir()
	wallGalleryDir = func() string { return dir }
	t.Cleanup(func() { wallGalleryDir = old })
	return dir
}

// withTempWall 把背景图缓存目录与图库目录都指到临时目录，并清掉进程内的记忆。
func withTempWall(t *testing.T) string {
	t.Helper()
	oldDir, oldMemo := wallCacheDir, wallMemo
	dir := t.TempDir()
	wallCacheDir = func() string { return dir }
	withTempGallery(t)
	wallMemo = map[string]wallResult{}
	t.Cleanup(func() {
		wallCacheDir, wallMemo = oldDir, oldMemo
	})
	return dir
}

// 样式表不许使用比基线更新的 CSS 函数。
//
// 界面跑在系统自带的 WebView2 上，那台机器上的 Chromium 版本由微软的更新决定，不由这个 exe
// 决定。用了新函数时，不支持的引擎会在**解析期**把整条声明丢掉，而且丢得悄无声息：
// 毛玻璃那几条只是没效果，但暗化层一丢就是"照片上的字没有东西托着"——那一档是危险的。
// 所以这类值一律写成每个主题各一份的静态变量（见 :root 里 --glass / --wall-scrim 那段注释）。
func TestNoVersionDependentCSSFunctions(t *testing.T) {
	// 括号里这几个都是 Chromium 105 之后才有的。项目基线只用到 min() / max()（79），
	// 那两个不在此列，别顺手加进来。
	//
	// 先剥掉注释再扫：说明"不要用某个函数"的那几句注释本身会命中，那不是声明。
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(cssRegion(t), "")
	re := regexp.MustCompile(`(?i)\b(color-mix|oklch|oklab|lab|lch|light-dark|device-cmyk)\(|:has\(|@container|color\(from`)
	hits := re.FindAllString(css, -1)
	if len(hits) == 0 {
		return
	}
	seen := map[string]bool{}
	var uniq []string
	for _, h := range hits {
		if !seen[h] {
			seen[h] = true
			uniq = append(uniq, h)
		}
	}
	t.Errorf("样式表里用了版本相关的色彩函数或选择器 %v —— 改成每个主题各一份的静态变量；"+
		"老一点的 WebView2 上整条声明会被丢掉，暗化层丢失会让壁纸上的字没法读", uniq)
}

// ---- 毛玻璃（视觉美化层）----
//
// 这一段的口径变过一次，值得写清楚：原来这里是「全表禁 backdrop-filter」，
// 禁的其实是**占满视口又带模糊或动画**的图层 —— 那条实测红线（空闲 13%、运行中 51%）就是它。
// 现在允许真毛玻璃，理由不是"模糊变便宜了"，而是把那个乘积的两个乘数都压住了：
// 只加在局部面板上（面积），任务执行期间整体让位（变化频率）。
// 下面几条把那两条压法写成判据。放宽的只有"能不能用"，边界一条都没松。

// vfxCSS 取出视觉美化那一段 CSS。它必须是**可以整段删掉**的，所以先能把它圈出来。
// 起点取到注释的 `/*` 上，而不是注释里那行标题 —— 否则这一段自带的那段说明会被算成"段外"，
// 剥注释时剥不干净，判据就会拿说明文字当声明。
func vfxCSS(t *testing.T) string {
	t.Helper()
	i := strings.Index(guiPageHTML, "/* ============ 视觉美化（VFX）")
	if i < 0 {
		t.Fatal("找不到视觉美化那一段的起头标记 —— 它被改名或删掉了吗？这条检查等于没做")
	}
	j := strings.Index(guiPageHTML[i:], "\n*{box-sizing")
	if j < 0 {
		t.Fatal("找不到视觉美化那一段的结尾（全局 reset 那一行）")
	}
	return guiPageHTML[i : i+j]
}

// cssNoComments 剥掉注释。cssRules 会把选择器前面那段注释一起算进选择器，
// 而说明用的注释里正好也写着这些类名 —— 不剥掉就会拿说明文字当规则判。
func cssNoComments(t *testing.T) string {
	t.Helper()
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(cssRegion(t), "")
}

// stripNot 去掉 :not(...) 那几段。
// 判"这条规则碰没碰某个元素"时必须先去掉它：`.card:not(.logcard)` 里的 .logcard
// 是**排除**，恰恰说明它不碰日志卡。不剥掉就会把这条最要紧的排除条件当成违规。
func stripNot(sel string) string {
	return regexp.MustCompile(`:not\([^)]*\)`).ReplaceAllString(sel, "")
}

// blurRules 取出真正会加模糊的规则（backdrop-filter:none 那种覆盖写法不算）。
func blurRules(t *testing.T) []panelCSSRule {
	t.Helper()
	none := regexp.MustCompile(`backdrop-filter\s*:\s*none`)
	var out []panelCSSRule
	for _, r := range cssRules(cssNoComments(t)) {
		if strings.Contains(r.body, "backdrop-filter") && !none.MatchString(r.body) {
			out = append(out, r)
		}
	}
	return out
}

// 允许加模糊的地方。它们都是**局部**面板，且内容是间歇变化的。
// 往这份名单里加东西之前先回答两个问题：它背后是谁？那块内容多久变一次？
var blurAllowedSel = []string{".card:not(.logcard)", ".foot", ".mbox"}

// 局部：模糊只准落在上面那几个面板上，一个都不许漏到别处（逐条选择器核，不是整串包含）。
func TestBackdropFilterOnlyOnLocalSurfaces(t *testing.T) {
	rules := blurRules(t)
	if len(rules) == 0 {
		t.Fatal("样式表里一处 backdrop-filter 都没有 —— 这条检查等于没做")
	}
	for _, r := range rules {
		sel := strings.Join(strings.Fields(r.sel), " ")
		for _, part := range strings.Split(sel, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			ok := false
			for _, allow := range blurAllowedSel {
				if strings.Contains(part, allow) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("这条选择器加了 backdrop-filter，可它不在允许的局部面板里：\n  %s\n"+
					"允许的是 %v —— 换地方之前先想清楚它背后是谁、那块内容多久变一次", part, blurAllowedSel)
			}
		}
	}
}

// 三类地方**永远**不许加模糊。它们对应三条各自成立的代价来源，不是同一件事的三种说法。
func TestNoBackdropFilterOnChurningSurfaces(t *testing.T) {
	banned := []struct{ cls, why string }{
		{".logcard", "日志在任务执行期间持续追加，模糊要跟着一遍遍重算"},
		{".log", "同上，它是流式内容"},
		{".top", "整条顶栏，且进度链上那个点在运行期间一直在呼吸"},
		{".modal", "整屏遮罩层：占满视口再配模糊，就是原来那条红线本身"},
		{".bg", "整屏背景层"},
		{".aurora", "整屏背景层"},
		{".grid", "整屏背景层"},
		{".t", "悬停有 transform:translateX，位移意味着每帧重新取样"},
		{".gcard", "悬停有 transform:translateY，同上"},
	}
	token := regexp.MustCompile(`\.([A-Za-z][\w-]*)`)
	for _, r := range blurRules(t) {
		sel := stripNot(cssNoCommentsOne(r.sel))
		for _, m := range token.FindAllStringSubmatch(sel, -1) {
			got := "." + m[1]
			for _, b := range banned {
				if got == b.cls {
					t.Errorf("%s 上加模糊了（%s）：%s", b.cls, got, b.why)
				}
			}
		}
	}
}

// cssNoCommentsOne 只剥单段选择器里的注释（blurRules 那层已经剥过一遍，这里防按行拼接的写法）。
func cssNoCommentsOne(sel string) string {
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(sel, "")
}

// 每一条模糊规则都必须同时挂着三道闸：总开关、档位不为 off、"当前不在运算期间"。
// 少任何一道，界面就会在不该花钱的时候花钱。
func TestGlassYieldsWhileBusy(t *testing.T) {
	for _, r := range blurRules(t) {
		sel := strings.Join(strings.Fields(r.sel), " ")
		for _, want := range []string{`data-vfx="on"`, `:not([data-glass="off"])`, `:not([data-vfx-busy="on"])`} {
			if !strings.Contains(sel, want) {
				t.Errorf("这条模糊规则少了 %s，等于绕开了那道闸：\n  %s", want, sel)
			}
		}
	}
}

// 四档必须是**有界且递增**的离散值：低档压在 5px 以内、高档不超过 16px。
// 档位而不是任意像素值，是为了保证用户拿不到一个把界面拖垮的取值。
func TestGlassLevelsAreBoundedAndOrdered(t *testing.T) {
	val := map[string]float64{}
	for _, lv := range []string{"low", "mid", "high"} {
		body := cssRule(t, `:root[data-glass="`+lv+`"]`)
		m := regexp.MustCompile(`--glass-blur:\s*([\d.]+)px`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("档位 %s 没有给出 --glass-blur：%q", lv, body)
		}
		f, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatalf("档位 %s 的模糊值 %q 解析不了", lv, m[1])
		}
		val[lv] = f
	}
	if val["low"] > 5 {
		t.Errorf("低档是 %.0fpx，超过 5px —— 这一档的意义就是「最低性能消耗」，面板上也是这么写的", val["low"])
	}
	if val["high"] > 16 {
		t.Errorf("高档是 %.0fpx，超过 16px —— 面板上承诺的上限是 16px", val["high"])
	}
	if !(val["low"] < val["mid"] && val["mid"] < val["high"]) {
		t.Errorf("四档没有递增：低 %.0f / 中 %.0f / 高 %.0f", val["low"], val["mid"], val["high"])
	}
}

// 整段可删：模糊必须全部落在视觉美化那一段里，段外一处都不许有。
// 这条就是"美化模块独立、方便移除"的可执行版本 —— 段外有模糊，删那一段就会留下半拉样式。
func TestBackdropFilterIsConfinedToTheVFXBlock(t *testing.T) {
	inside := vfxCSS(t)
	if !strings.Contains(inside, "backdrop-filter") {
		t.Fatal("视觉美化那一段里没有 backdrop-filter —— 圈错地方了？这条检查等于没做")
	}
	outside := strings.Replace(cssRegion(t), inside, "", 1)
	if m := regexp.MustCompile(`(-webkit-)?backdrop-filter\s*:\s*([^;}]+)`).FindStringSubmatch(outside); m != nil {
		t.Errorf("视觉美化那一段之外还出现了 backdrop-filter（%s）：删掉美化层之后它会留下来，%s", m[1], m[2])
	}
}

// 不写 will-change 是个决定，不是遗漏：给玻璃容器挂它会为每一块永久分配一个合成层。
func TestNoWillChangeOnGlass(t *testing.T) {
	// 剥注释：那一段的说明里正好写着"这里一个 will-change 都没写"，不剥就会拿说明当声明判
	body := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(vfxCSS(t), "")
	if strings.Contains(body, "will-change") {
		t.Error("视觉美化那一段里出现了 will-change —— 它会让每一块玻璃常驻一个合成层，" +
			"而这些面板平时是静止的，即将变化这件事根本不成立；按需合成的代价更小")
	}
}

// 面板淡入只准动 opacity。一旦动 transform，背后那层模糊就要每帧重新采样 ——
// 那正是原来那条红线被触发的机制。
func TestFadeOnlyAnimatesOpacity(t *testing.T) {
	css := cssRegion(t)
	i := strings.Index(css, "@keyframes vfx-fade")
	if i < 0 {
		t.Fatal("找不到 vfx-fade 关键帧 —— 面板淡入被删了吗？这条检查等于没做")
	}
	j := strings.Index(css[i:], "{")
	body, ok := matchBraces(css[i+j:])
	if !ok {
		t.Fatal("vfx-fade 的花括号不配对")
	}
	if !strings.Contains(body, "opacity") {
		t.Error("vfx-fade 没动 opacity —— 那它就不是淡入")
	}
	for _, bad := range []string{"transform", "filter", "box-shadow", "margin", "width", "height"} {
		if strings.Contains(body, bad) {
			t.Errorf("vfx-fade 里动了 %s：玻璃面板一动，背后那块纹理就得每帧重采样", bad)
		}
	}
	// 时长必须走变量，精简动效与系统的"减少动态效果"才能把它归零
	if !strings.Contains(css, "--vfx-fade:0s") {
		t.Error("找不到把淡入时长归零的规则 —— 精简动效与系统「减少动态效果」就关不掉它")
	}
}

// 占满视口的图层不许带模糊、滤波或动画。原来这条只盯着 .aurora，
// 这里推广到所有整屏规则：以后有人往背景上再叠一层，同样会被拦住。
// （mask 不受此限 —— 它只光栅一次，之后就是一个带遮罩的四边形。）
func TestGuiNeverBlursAFullScreenLayer(t *testing.T) {
	reFull := regexp.MustCompile(`inset\s*:\s*0(px)?\s*;`)
	checked := 0
	for _, r := range cssRules(cssRegion(t)) {
		if !reFull.MatchString(r.body) {
			continue
		}
		checked++
		sel := effSel(r.sel)
		for _, bad := range []string{"backdrop-filter", "-webkit-filter", "filter:", "animation:"} {
			if strings.Contains(r.body, bad) {
				t.Errorf("整屏图层 %s 里出现了 %s —— 占满视口再配模糊或动画，就是那条"+
					"「空闲 13%%、运行中 51%%」的红线", sel, bad)
			}
		}
	}
	if checked < 3 {
		t.Fatalf("只找出 %d 个整屏图层，太少 —— 选择器写法变了，这条检查等于没做", checked)
	}
}

// ---- 扁平化 ----

// 扁平化的规则不许出现背景层的选择器：背景光效归 data-backdrop 管，背景图归 data-wall 管，
// 三个开关各管各的，否则会互相打架，也判不清是谁改的。
func TestFlatNeverTouchesTheBackdropLayer(t *testing.T) {
	found := 0
	for _, r := range cssRules(cssRegion(t)) {
		sel := effSel(r.sel)
		if !strings.Contains(sel, `data-flat="on"`) {
			continue
		}
		found++
		for _, banned := range []string{".bg", ".aurora", ".grid", ".wall", ".scrim"} {
			if strings.Contains(sel, banned) {
				t.Errorf("扁平化的规则碰了背景层 %s（%s）—— 背景归 data-backdrop 与 data-wall 管", banned, sel)
			}
		}
	}
	if found == 0 {
		t.Fatal("找不到 data-flat 的规则 —— 这个开关被删了吗？这条检查等于没做")
	}
}

// 扁平化去掉的是外阴影，不是焦点可见性：原来靠 box-shadow 画的聚焦环必须换成 outline，
// 否则键盘用户会完全看不到焦点落在哪。
func TestFlatKeepsFocusVisible(t *testing.T) {
	// 逐选择器核对，而不是"整段里出现过 outline 就算过" —— 后者把 outline 的值改成 none
	// 也照样通过，等于没测。
	type rule struct{ sel, body string }
	var flat []rule
	for _, r := range cssRules(cssRegion(t)) {
		// 用完整的（多行拼平的）选择器，不能用 effSel：它只留最后一行，
		// 分组规则里前面的选择器会被丢掉。
		sel := strings.Join(strings.Fields(r.sel), " ")
		if strings.Contains(sel, `data-flat="on"`) {
			flat = append(flat, rule{sel, r.body})
		}
	}
	if len(flat) == 0 {
		t.Fatal("找不到 data-flat 的规则 —— 这个开关被删了吗？这条检查等于没做")
	}
	// 这几处原本都靠 box-shadow 画环，扁平化必须给每一处补上看得见的描边
	need := []string{".search input:focus", ".fld input[type=text]:focus", ".mtxt:focus", ".mdot.on", ".qbtn.on"}
	reOutline := regexp.MustCompile(`outline\s*:\s*(\d+)px\s+solid\s+var\(--sig`)
	for _, want := range need {
		found := ""
		for _, r := range flat {
			if !strings.Contains(r.sel, want) {
				continue
			}
			m := reOutline.FindStringSubmatch(r.body)
			if m == nil {
				continue
			}
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 2 {
				found = m[0]
				break
			}
		}
		if found == "" {
			t.Errorf("扁平化没有给 %s 留下看得见的描边（要求 outline ≥ 2px 且不是 none）——"+
				"那一处会退回外阴影环，或者干脆看不见焦点", want)
		}
	}
	for _, r := range flat {
		if regexp.MustCompile(`box-shadow\s*:\s*0 0 0`).MatchString(r.body) {
			t.Errorf("%s 里还有 box-shadow 画的聚焦环 —— 那一处应当换成 outline", r.sel)
		}
	}
}

// 「扁平」必须真的把圆角归零，否则只是换了个说法。
func TestFlatZeroesRadiusTokens(t *testing.T) {
	blk := cssVars(cssBlock(t, `:root[data-flat="on"]`))
	for _, name := range []string{"--r1", "--r2", "--r3", "--pill"} {
		v, ok := blk[name]
		if !ok {
			t.Errorf("扁平化没有覆盖 %s —— 圆角不会归零", name)
			continue
		}
		if v != "0" && v != "0px" {
			t.Errorf("扁平化把 %s 设成了 %q —— 期望恰好是 0。"+
				"别用 px() 判：它在解析失败时返回 0，于是 calc()、var()、写错的值都会被当成 0，"+
				"这条断言就废了", name, v)
		}
	}
}

// ---- 两个新开关的接线 ----

// 界面选项、归一化、首屏注入、立即生效四处必须都接上。
func TestFlatAndGlassAreWiredEndToEnd(t *testing.T) {
	if got := segOptions(t, "flat"); strings.Join(got, ",") != "off,on" {
		t.Errorf("uiSeg('flat') 的取值是 %v，期望 off,on", got)
	}
	// 毛玻璃从勾选框改成了四档，取值必须与 guiconfig.go 的白名单一致
	if got := segOptions(t, "glass"); strings.Join(got, ",") != "off,low,mid,high" {
		t.Errorf("uiSeg('glass') 的取值是 %v，期望 off,low,mid,high；"+
			"它必须与 guiconfig.go 的 glassValues 与 CSS 的三个 --glass-blur 块一一对应", got)
	}
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"默认材质", normalizeUI(guiUIState{}).Flat, "off"},
		{"非法材质", normalizeUI(guiUIState{Flat: "yes"}).Flat, "off"},
		{"合法材质", normalizeUI(guiUIState{Flat: "on"}).Flat, "on"},
		{"默认毛玻璃", normalizeUI(guiUIState{}).Glass, uiGlassDefault},
		{"非法毛玻璃", normalizeUI(guiUIState{Glass: "1"}).Glass, uiGlassDefault},
		{"合法毛玻璃", normalizeUI(guiUIState{Glass: "high"}).Glass, "high"},
		// 老配置里存的是 "on"（那一版是"半透明、不模糊"）：当初勾它的人要的就是玻璃观感，
		// 所以按推荐档位迁移，而不是把功能悄悄关掉
		{"旧值迁移", normalizeUI(guiUIState{Glass: "on"}).Glass, "mid"},
		{"默认总开关", normalizeUI(guiUIState{}).VFX, "on"},
		{"总开关只认 off", normalizeUI(guiUIState{VFX: "yes"}).VFX, "on"},
		{"关掉总开关", normalizeUI(guiUIState{VFX: "off"}).VFX, "off"},
		// 总开关关掉时**不改**存着的档位：用户再打开时要回到他原来选的那一档
		{"关总开关不动档位", normalizeUI(guiUIState{VFX: "off", Glass: "high"}).Glass, "high"},
		{"默认微光", normalizeUI(guiUIState{}).Glow, "on"},
		{"默认淡入", normalizeUI(guiUIState{}).Fade, "on"},
		{"默认自动降级", normalizeUI(guiUIState{}).NoDegrade, ""},
		{"关掉自动降级", normalizeUI(guiUIState{NoDegrade: "1"}).NoDegrade, "1"},
	} {
		if c.got != c.want {
			t.Errorf("%s：得到 %q，期望 %q", c.name, c.got, c.want)
		}
	}
	// 首屏注入：每个开关都要显式写在 <html> 上，缺省等于把语义交给猜测
	a := normalizeUI(guiUIState{Flat: "on", Glass: "high", VFX: "on", Glow: "on", Fade: "on"}).htmlAttr()
	for _, want := range []string{`data-flat="on"`, `data-glass="high"`,
		`data-vfx="on"`, `data-glow="on"`, `data-fade="on"`} {
		if !strings.Contains(a, want) {
			t.Errorf("htmlAttr 没有带上 %s：%q", want, a)
		}
	}
	d := normalizeUI(guiUIState{}).htmlAttr()
	for _, want := range []string{`data-flat="off"`, `data-glass="` + uiGlassDefault + `"`,
		`data-vfx="on"`, `data-glow="on"`, `data-fade="on"`} {
		if !strings.Contains(d, want) {
			t.Errorf("默认也要显式写 %s，否则默认值就靠猜", want)
		}
	}
	// 改完要立刻生效，不能等重开界面。
	// 这四项属性由 VFX 统一写（它要先过一遍"浏览器认不认 backdrop-filter"），
	// 所以这里查的是"uiApply 有没有把它递进去"。
	apply := bodyBetween(t, "function uiApply", "async function uiSave")
	if !strings.Contains(apply, "VFX.apply(u)") {
		t.Error("uiApply 没有把设置递给 VFX —— 改完档位要重开界面才看得到")
	}
	if !strings.Contains(apply, "data-flat") {
		t.Error("uiApply 没有设置 data-flat")
	}
	// VFX 自己必须把这几项都写出来，少一项就等于那个开关失效
	vfx := bodyBetween(t, "var VFX = (function(){", "async function api")
	for _, want := range []string{"data-vfx", "data-glass", "data-glow", "data-fade"} {
		if !strings.Contains(vfx, want) {
			t.Errorf("VFX.apply 没有写 %s —— 那个开关点了没反应", want)
		}
	}
	// 支持性检测与降级必须在内：不认 backdrop-filter 的老内核上要退成"不做模糊"，而不是报错破版
	if !strings.Contains(vfx, "CSS.supports") || !strings.Contains(vfx, "'off'") {
		t.Error("VFX 里没有 backdrop-filter 的支持性检测与降级 —— 老内核上会直接少一层样式")
	}
}

// 四档特效下，工具本身必须完全一样。
//
// 这是需求里「核心功能不受任何特效开关状态影响」的可执行版本：把四档各渲染一份页面，
// 抹掉两处**本来就该变**的注入点（<html> 上的属性、首屏那份 JSON）之后逐字节比对。
// 也就是说美化只准通过属性与 CSS 起作用 —— 一旦它开始改结构、改脚本、改按钮文案，这条就红。
func TestFourLevelsChangeNothingButAttributes(t *testing.T) {
	blank := func(p string) string {
		p = regexp.MustCompile(`<html lang="zh-CN"[^>]*>`).ReplaceAllString(p, "<html>")
		p = regexp.MustCompile(`var u = \{[^\n]*\};`).ReplaceAllString(p, "var u = {};")
		return p
	}
	base := ""
	for _, lv := range glassValues {
		u := normalizeUI(guiUIState{Glass: lv})
		p := strings.Replace(guiPageHTML, "@@UIATTRS@@", u.htmlAttr(), 1)
		p = strings.Replace(p, "@@UIJSON@@", u.uiStartupJSON(), 1)
		// 注入点必须真的被替换掉了，否则下面比的是同一个没渲染的模板，等于没测
		if strings.Contains(p, "@@UI") {
			t.Fatalf("档位 %s：渲染后还剩着占位符", lv)
		}
		got := blank(p)
		if base == "" {
			base = got
			continue
		}
		if got != base {
			t.Fatalf("档位 %s 渲染出来的页面不止属性不同 —— "+
				"美化只准通过 <html> 属性与 CSS 起作用，不许改结构、脚本或文案", lv)
		}
	}
	// 反过来确认这条不是空转：属性那一处确实随档位变
	a := normalizeUI(guiUIState{Glass: "off"}).htmlAttr()
	b := normalizeUI(guiUIState{Glass: "high"}).htmlAttr()
	if a == b {
		t.Fatal("两档的 htmlAttr 完全一样 —— 档位根本没传到页面上")
	}
}

// 三项能力都要在设置面板里有入口，背景层的两个 div 也要真的在页面上。
func TestAppearanceControlsExistInThePanel(t *testing.T) {
	for _, want := range []string{"uiSeg('flat'", "uiSeg('glass'", "uiWallPicker(", "'/api/wall/import'",
		"uiSw('vfx'", "uiSw('glow'", "uiSw('fade'", "uiSw('noDegrade'", "vfxMeasure"} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("设置面板里缺少 %s —— 这个能力在界面上没有入口", want)
		}
	}
	for _, want := range []string{`<div class="wall"></div>`, `<div class="scrim"></div>`} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("背景层里缺少 %s", want)
		}
	}
}

// 背景光效的开关只管极光与网格：它不该把用户自选的背景图一起关掉。
func TestBackdropSwitchLeavesTheWallpaperAlone(t *testing.T) {
	if strings.Contains(cssRegion(t), `data-backdrop="off"] .bg{`) {
		t.Error("背景光效关掉时整层 .bg 都被隐藏了 —— 用户自选的背景图会跟着一起消失")
	}
	for _, want := range []string{`data-backdrop="off"] .aurora`, `data-backdrop="off"] .grid`} {
		if !strings.Contains(cssRegion(t), want) {
			t.Errorf("背景光效关掉时应当隐藏 %s", want)
		}
	}
}

// ---- 背景图片 ----

// 背景图必须是静态贴图：它一旦被动画、平移或缩放，就变成整屏逐帧重新光栅化。
func TestWallpaperLayerStaysStatic(t *testing.T) {
	// 壁纸那一叠有两层，都占满视口，都不能动
	for _, sel := range []string{".wall", ".scrim"} {
		body := cssRule(t, sel)
		for _, bad := range []string{"animation", "filter", "transform", "will-change", "transition"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s 里不该有 %s —— 背景那一叠一动就是整屏逐帧重算，等同那条 13%% 的红线", sel, bad)
			}
		}
	}
	if !strings.Contains(cssRule(t, ".wall"), "background-size:cover") {
		t.Error("背景图应当用 cover 铺满（按比例裁切），而不是拉伸变形")
	}
	if !strings.Contains(cssRegion(t), `:root[data-wall="on"] .scrim`) {
		t.Error("压暗层没有和 data-wall 绑定 —— 没有背景图时会平白压暗整个界面")
	}
}

// 文字压在背景图上的情形必须只剩一个：**承载它的面板是实心的**。
//
// 半透明面板压在一张任意照片上时，合成色由用户那张图决定 —— 一张纯白图就能把它推到最亮。
// 实测把全白图与全黑图各合成一遍：深色主题配全白图，第四级灰落在 3.97；浅色主题配全黑图，
// 落在 4.15，而门槛是 4.5。要把它压回 4.5，面板不透明度得提到 0.94 以上（那时照片已经透不出来，
// 等于没做玻璃）；反过来把第三、四级灰调到能承受照片，那两级会收敛成同一个颜色，四级灰阶塌成三级。
//
// 所以规则是「设了背景图，毛玻璃一律让位」。这条用例把规则钉在样式表上：
// 每一条玻璃规则都必须带 :not([data-wall="on"])。
func TestGlassYieldsToWallpaper(t *testing.T) {
	// 先剥注释：cssRules 会把选择器前面那段注释一起当成选择器，
	// 而说明"为什么让位"的注释里正好也写着 data-glass。
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(cssRegion(t), "")
	rules := 0
	for _, r := range cssRules(css) {
		// 判据是"这条规则把面板做成了半透明"，而不是"选择器里写了某个字面量" ——
		// 四档改造之后档位写成了 :not([data-glass="off"])，靠字面量找已经找不准了。
		if !strings.Contains(r.body, "var(--glass") {
			continue
		}
		rules++
		sel := strings.Join(strings.Fields(r.sel), " ")
		if !strings.Contains(sel, `:not([data-wall="on"])`) {
			t.Errorf("玻璃规则 %s 没有排除「已设背景图」的情形 —— 半透明面板压在一张纯白照片上时，"+
				"第三、四级灰会掉到 4.5 以下（实测最差 3.97）", sel)
		}
	}
	if rules == 0 {
		t.Fatal("找不到任何半透明的面板规则 —— 玻璃这一块被删了吗？这条检查等于没做")
	}
}

// 与上一条配套的数值断言：没有背景图时，玻璃面板压着的只有页面底色、极光与网格那三层，
// 全是我们自己定的，于是可以把极端情形穷举出来逐格核对。
func TestGlassOverPageKeepsTextReadable(t *testing.T) {
	type layer struct {
		name string
		col  rgba
	}
	// 三种承载文字的面，各对应 CSS 里一条玻璃规则
	surfaces := []struct{ name, glassVar string }{
		{"面板", "--glass"},
		{"条目悬停/选中", "--glass-2"},
		{"引导卡悬停", "--glass-3"},
	}
	texts := []string{"--ink", "--ink2", "--muted", "--muted2"}
	themes := uniq(append([]string{"dark"}, themeValues...))
	accents := uniq(append([]string{"mint"}, accentValues...))

	var failures []string
	for _, theme := range themes {
		for _, acc := range accents {
			v := resolveVars(t, theme, acc)
			bg, ok := parseColor(v["--bg"])
			if !ok {
				t.Fatalf("%s+%s：--bg 取不到颜色（%q）", theme, acc, v["--bg"])
			}
			// 背景那一叠：页面底色之上可能画了极光与网格，两层都是半透明，
			// 所以极端值是"完全没画"与"该色以它的 alpha 满铺"。
			stacks := []layer{{"只有底色", bg}}
			for _, av := range []string{"--aurora-a", "--aurora-b", "--grid-ink"} {
				if a, ok := parseColor(v[av]); ok {
					stacks = append(stacks, layer{av, over(a, bg)})
				}
			}
			for _, s := range surfaces {
				glass, ok := parseColor(v[s.glassVar])
				if !ok {
					t.Fatalf("%s+%s：%s 取不到颜色（%q）", theme, acc, s.glassVar, v[s.glassVar])
				}
				for _, st := range stacks {
					plate := over(glass, st.col)
					for _, tok := range texts {
						fg, ok := parseColor(v[tok])
						if !ok {
							continue
						}
						if r := contrast(fg, plate); r+1e-9 < 4.5 {
							failures = append(failures, fmt.Sprintf(
								"%s+%s：%s 压在「%s + %s」上只有 %.2f，要求 ≥ 4.5",
								theme, acc, tok, s.name, st.name, r))
						}
					}
				}
			}
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		for _, f := range failures {
			t.Error(f)
		}
		t.Fatalf("有 %d 处会在玻璃面板上读不清 —— --glass 那一组取值要先修掉", len(failures))
	}
}

// 换过图之后，旧的那份缓存会被清掉；此时再切回旧图必须重新生成，
// 而不是把记忆里的旧地址直接交出去 —— 那样界面会拿到 404：整屏被压暗层盖住却没有图，
// 而且要重启程序才恢复。
func TestPrepareWallRebuildsAfterPrune(t *testing.T) {
	dir := withTempWall(t)
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a.jpg")
	b := filepath.Join(tmp, "b.jpg")
	writeTestJPEG(t, a, 800, 600)
	writeTestJPEG(t, b, 640, 480)

	ra := prepareWall(a)
	if !ra.on() {
		t.Fatalf("第一张图准备失败：%s", ra.Reason)
	}
	// 换成第二张：第一张的缓存会被清掉
	rb := prepareWall(b)
	if !rb.on() || rb.URL == ra.URL {
		t.Fatalf("第二张图应当得到另一个地址，得到 %+v", rb)
	}
	if _, err := os.Stat(filepath.Join(dir, strings.TrimPrefix(ra.URL, "/bg/"))); !os.IsNotExist(err) {
		t.Errorf("换图之后第一张的缓存应当已被清掉，实际 err=%v", err)
	}
	// 再切回第一张：必须重新生成，且拿到的地址真有文件
	back := prepareWall(a)
	if !back.on() {
		t.Fatalf("切回第一张失败：%s", back.Reason)
	}
	if _, err := os.Stat(filepath.Join(dir, strings.TrimPrefix(back.URL, "/bg/"))); err != nil {
		t.Errorf("切回第一张后拿到的地址没有对应文件（%s）—— 界面会是一个 404 加一层压暗层", back.URL)
	}
}

// 手机拍的照片在文件里多半是"横着"存的，靠 EXIF 的方向标签告诉看图程序转多少度。
// 标准库不认这个标签，所以要自己转 —— 不做的话，用户挑一张竖拍的照片，壁纸是躺倒的。
func TestPrepareWallAppliesEXIFOrientation(t *testing.T) {
	withTempWall(t)
	dir := t.TempDir()

	// 4x2 的图：上面一行红、下面一行蓝，方便判断转没转
	base := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		base.SetRGBA(x, 0, color.RGBA{R: 255, A: 255})
		base.SetRGBA(x, 1, color.RGBA{B: 255, A: 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, base, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		orient uint16
		w, h   int
		what   string
	}{
		{1, 4, 2, "原样"},
		{3, 4, 2, "旋转 180 度"},
		{6, 2, 4, "顺时针 90 度"},
		{8, 2, 4, "逆时针 90 度"},
	} {
		src := filepath.Join(dir, fmt.Sprintf("o%d.jpg", c.orient))
		if err := os.WriteFile(src, jpegWithOrientation(t, buf.Bytes(), c.orient), 0o644); err != nil {
			t.Fatal(err)
		}
		r := prepareWall(src)
		if !r.on() {
			t.Fatalf("%s：准备失败：%s", c.what, r.Reason)
		}
		got := readTestJPEG(t, filepath.Join(wallCacheDir(), strings.TrimPrefix(r.URL, "/bg/")))
		if got.Bounds().Dx() != c.w || got.Bounds().Dy() != c.h {
			t.Errorf("%s：尺寸是 %dx%d，期望 %dx%d —— 方向标签没有被应用",
				c.what, got.Bounds().Dx(), got.Bounds().Dy(), c.w, c.h)
		}
	}

	// 再核一次像素方向：顺时针 90 度之后，原来在上面的那一行会跑到右边去
	src := filepath.Join(dir, "cw90.jpg")
	if err := os.WriteFile(src, jpegWithOrientation(t, buf.Bytes(), 6), 0o644); err != nil {
		t.Fatal(err)
	}
	r := prepareWall(src)
	got := readTestJPEG(t, filepath.Join(wallCacheDir(), strings.TrimPrefix(r.URL, "/bg/")))
	rr, gg, bb, _ := got.At(got.Bounds().Dx()-1, 0).RGBA()
	if !(rr > gg && rr > bb) {
		t.Errorf("顺时针 90 度之后右上角应当是原来那一行红（R 最大），实际 r=%d g=%d b=%d —— "+
			"方向标签读到了但转错了方向", rr>>8, gg>>8, bb>>8)
	}
}

// 读不到方向时要安静地返回 1，不能把一张正常照片弄坏。
func TestExifOrientationDefaultsToOne(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []byte
	}{
		{"空", nil},
		{"不是 JPEG", []byte("这不是图片")},
		{"只有 SOI", []byte{0xFF, 0xD8}},
		{"段长不合法", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01, 0x00}},
		{"带 EXIF 但 TIFF 头是坏的", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x0C, 'E', 'x', 'i', 'f', 0, 0, 'X', 'X', 0, 0}},
	} {
		if got := exifOrientation(bytes.NewReader(c.in)); got != 1 {
			t.Errorf("%s：应当返回 1（不变换），实际 %d", c.name, got)
		}
	}
	// 一张普通的无 EXIF JPEG 也必须返回 1
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	if got := exifOrientation(bytes.NewReader(buf.Bytes())); got != 1 {
		t.Errorf("没有 EXIF 的 JPEG 应当返回 1，实际 %d", got)
	}
}

// jpegWithOrientation 在 SOI 之后插一段只含方向标签的 APP1。
func jpegWithOrientation(t *testing.T, raw []byte, orient uint16) []byte {
	t.Helper()
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatal("拿到的不是 JPEG")
	}
	var tiff []byte
	tiff = append(tiff, 'I', 'I', 0x2A, 0x00)             // 小端 + 42
	tiff = binary.LittleEndian.AppendUint32(tiff, 8)      // IFD0 偏移
	tiff = binary.LittleEndian.AppendUint16(tiff, 1)      // 一条记录
	tiff = binary.LittleEndian.AppendUint16(tiff, 0x0112) // Orientation
	tiff = binary.LittleEndian.AppendUint16(tiff, 3)      // 类型 SHORT
	tiff = binary.LittleEndian.AppendUint32(tiff, 1)      // 个数 1
	tiff = binary.LittleEndian.AppendUint16(tiff, orient) // 值
	tiff = append(tiff, 0, 0)                             // 值字段补齐 4 字节
	tiff = binary.LittleEndian.AppendUint32(tiff, 0)      // 没有下一条 IFD
	body := append([]byte("Exif\x00\x00"), tiff...)
	n := len(body) + 2 // 段长含自己那两字节
	seg := []byte{0xFF, 0xE1, byte(n >> 8), byte(n & 0xFF)}
	out := append([]byte{}, raw[:2]...)
	out = append(out, seg...)
	out = append(out, body...)
	return append(out, raw[2:]...)
}

// 只有内容哈希形式的文件名可以被提供出去：名字来自 URL，白名单必须窄到没有第二种解释。
func TestServeWallAcceptsOnlyHashNames(t *testing.T) {
	dir := withTempWall(t)
	good := "bg-0123456789abcdef.jpg"
	if err := os.WriteFile(filepath.Join(dir, good), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want int
	}{
		{"/bg/" + good, http.StatusOK},
		{"/bg/../config.json", http.StatusNotFound},
		{"/bg/..%2Fconfig.json", http.StatusNotFound},
		{"/bg/bg-0123456789abcdeZ.jpg", http.StatusNotFound},
		{"/bg/bg-0123.jpg", http.StatusNotFound},
		{"/bg/", http.StatusNotFound},
		{"/bg/config.json", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		serveWall(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		if w.Code != c.want {
			t.Errorf("%s 返回 %d，期望 %d", c.path, w.Code, c.want)
		}
	}
	w := httptest.NewRecorder()
	serveWall(w, httptest.NewRequest(http.MethodGet, "/bg/"+good, nil))
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "max-age=31536000") || !strings.Contains(cc, "immutable") {
		t.Errorf("缓存头是 %q —— 名字里已经含内容哈希了，没有理由让浏览器每次重取", cc)
	}
}

// 大图必须缩到上限以内：4K 图解码后是 33 MB，而窗口再大也用不到那个分辨率。
func TestPrepareWallShrinksLargeImages(t *testing.T) {
	dir := withTempWall(t)
	src := filepath.Join(t.TempDir(), "big.jpg")
	writeTestJPEG(t, src, 3200, 1600)

	r := prepareWall(src)
	if !r.on() {
		t.Fatalf("准备背景图失败：%s", r.Reason)
	}
	if r.Reason != "" {
		t.Errorf("成功时不该带原因，得到 %q", r.Reason)
	}
	name := strings.TrimPrefix(r.URL, "/bg/")
	if !wallName.MatchString(name) {
		t.Fatalf("返回的地址 %q 不满足缓存文件名的白名单", r.URL)
	}
	img := readTestJPEG(t, filepath.Join(dir, name))
	if got := max(img.Bounds().Dx(), img.Bounds().Dy()); got != wallLongEdge {
		t.Errorf("长边是 %d，期望正好缩到 %d", got, wallLongEdge)
	}
	// 同一份源文件第二次准备必须直接命中，不再解码
	if r2 := prepareWall(src); r2 != r {
		t.Errorf("两次准备结果不一致：%+v / %+v", r, r2)
	}
}

// 本来就够小的图不该被放大（放大只会更糊），也不该被改动。
func TestPrepareWallKeepsSmallImagesAsIs(t *testing.T) {
	withTempWall(t)
	src := filepath.Join(t.TempDir(), "small.jpg")
	writeTestJPEG(t, src, 800, 600)
	r := prepareWall(src)
	if !r.on() {
		t.Fatalf("准备背景图失败：%s", r.Reason)
	}
	img := readTestJPEG(t, filepath.Join(wallCacheDir(), strings.TrimPrefix(r.URL, "/bg/")))
	if img.Bounds().Dx() != 800 || img.Bounds().Dy() != 600 {
		t.Errorf("小图被改动了：%dx%d，期望 800x600", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

// 不是图片 / 不存在 / 是个目录：一律给出可读的原因，而不是静默没有背景。
func TestPrepareWallReportsWhy(t *testing.T) {
	withTempWall(t)
	dir := t.TempDir()
	notImage := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(notImage, []byte("这不是图片"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ in, want string }{
		{filepath.Join(dir, "nope.jpg"), "找不到这个文件"},
		{dir, "目录"},
		{notImage, "格式"},
	} {
		got := prepareWall(c.in)
		if got.on() {
			t.Errorf("%q 不该给出背景图（%s）", c.in, got.URL)
		}
		if !strings.Contains(got.Reason, c.want) {
			t.Errorf("%q 的原因应当包含 %q，实际 %q", c.in, c.want, got.Reason)
		}
	}
	// 空路径是"不用背景图"，不是错误
	got := prepareWall("")
	if got.on() || got.Reason != "" {
		t.Errorf("空路径应当安静地什么都不做，得到 %+v", got)
	}
}

// 只有真的准备好了图片才往 <html> 上写属性：空地址会让浏览器去请求当前页面。
func TestWallAttrOnlyForAReadyImage(t *testing.T) {
	if got := wallAttr(wallResult{}); got != "" {
		t.Errorf("没有图片时不该注入任何属性，得到 %q", got)
	}
	if got := wallAttr(wallResult{Reason: "找不到这个文件"}); got != "" {
		t.Errorf("准备失败时不该注入属性，得到 %q", got)
	}
	got := wallAttr(wallResult{URL: "/bg/bg-0123456789abcdef.jpg"})
	for _, want := range []string{`data-wall="on"`, `--wall:url('/bg/bg-0123456789abcdef.jpg')`} {
		if !strings.Contains(got, want) {
			t.Errorf("注入的属性里应当有 %s，实际 %q", want, got)
		}
	}
}

// 配置里存的是「导入后的文件名」，不是路径。
// 这条把新语义钉住：存名字读回来还是名字；而旧配置里那种绝对路径会被复制进图库换成名字。
func TestBgImageRoundTrips(t *testing.T) {
	withTempConfig(t)
	withTempWall(t)
	gallery := withTempGallery(t)

	// 一、名字原样往返（去掉引号与空白）
	if _, err := saveUI(guiUIState{BgImage: "  \"我的壁纸.jpg\"  "}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	if got := loadUI().BgImage; got != "我的壁纸.jpg" {
		t.Errorf("名字应当去掉引号与空白，得到 %q", got)
	}

	// 二、旧配置里那种绝对路径要被复制进图库并换成名字
	src := filepath.Join(t.TempDir(), "老壁纸.jpg")
	writeTestJPEG(t, src, 320, 200)
	if _, err := saveUI(guiUIState{BgImage: src}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	if got := loadUI().BgImage; got != "老壁纸.jpg" {
		t.Errorf("旧配置里的路径应当被换成导入后的名字，得到 %q", got)
	}
	if _, err := os.Stat(filepath.Join(gallery, "老壁纸.jpg")); err != nil {
		t.Errorf("原图没有被复制进图库：%v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("迁移只该复制，原文件不该消失：%v", err)
	}
}

// 导入把图复制进存档。扩展名以文件头为准，不信上传时的文件名 —— 后者完全由客户端说了算。
func TestImportWallCopiesIntoGallery(t *testing.T) {
	withTempWall(t)
	gallery := withTempGallery(t)

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		img.SetRGBA(x, 0, color.RGBA{G: 255, A: 255})
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	got, err := importWallFile("风景.png", buf.Bytes())
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if got != "风景.png" {
		t.Errorf("存下的名字应当是 风景.png，得到 %q", got)
	}
	if _, err := os.Stat(filepath.Join(gallery, got)); err != nil {
		t.Errorf("图库里没有这个文件：%v", err)
	}

	// 扩展名说错了要被文件头纠正
	fixed, err := importWallFile("其实是张图.txt", buf.Bytes())
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if fixed != "其实是张图.png" {
		t.Errorf("扩展名应当以文件头为准，得到 %q", fixed)
	}

	// 非图片内容必须拒绝，否则图库里会混进打不开的文件
	if _, err := importWallFile("不是图.jpg", []byte("这不是图片内容")); err == nil {
		t.Error("非图片内容应当被拒绝")
	}
	// 名字里带路径的也要落成安全的文件名
	clean, err := importWallFile(`..\..\逃逸.jpg`, buf.Bytes())
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if strings.ContainsAny(clean, `/\`) {
		t.Errorf("存下的名字里不该有路径分隔符，得到 %q", clean)
	}
}

// 图库列表只算真正的图片：改了后缀的假图片不算（PCL2 为这类问题修过两次 bug）。
func TestWallListCountsOnlyRealImages(t *testing.T) {
	withTempWall(t)
	gallery := withTempGallery(t)

	writeTestJPEG(t, filepath.Join(gallery, "真图.jpg"), 40, 30)
	if err := os.WriteFile(filepath.Join(gallery, "假图.jpg"), []byte("这不是图片"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gallery, "说明.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gallery, "子目录.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := listWallImages()
	if len(got) != 1 || got[0] != "真图.jpg" {
		t.Errorf("图库应当只有「真图.jpg」一个，实际 %v", got)
	}
}

// 「配置里选的那张图不见了」必须是一等状态，不能静默变成「没有背景图」。
func TestMissingImportedImageSaysSo(t *testing.T) {
	withTempConfig(t)
	withTempWall(t)
	withTempGallery(t)

	if _, err := saveUI(guiUIState{BgImage: "已经删掉的图.jpg"}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	r := wallFromConfig(loadUI().BgImage)
	if r.on() {
		t.Fatal("文件不在时不该给出可用的地址")
	}
	if r.Reason == "" {
		t.Error("文件不在时必须给出一句原因 —— 否则用户看到的是背景图凭空消失")
	}
	if !strings.Contains(r.Reason, "存档") {
		t.Errorf("原因要能说清是存档里的文件不见了，实际 %q", r.Reason)
	}
}

// 名字里带路径分隔符的一律收拾掉：这个名字会被拼进存档目录。
func TestWallNameRejectsTraversal(t *testing.T) {
	for _, bad := range []string{`..\..\config.json`, "../../config.json", `子目录\x.jpg`, "/etc/passwd", "..", "."} {
		got := cleanWallName(bad)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("%q 收拾之后仍然带着路径分隔符：%q", bad, got)
		}
		if got == ".." || got == "." {
			t.Errorf("%q 收拾之后还是相对路径：%q", bad, got)
		}
	}
	if got := cleanWallName(`D:\图片\壁纸.jpg`); got != "壁纸.jpg" {
		t.Errorf("应当只取文件名部分，得到 %q", got)
	}
}

// 面板要能就地说明"填了没反应"的原因，否则用户没法自己排查。
func TestWallpaperReasonReachesThePanel(t *testing.T) {
	withTempConfig(t)
	withTempWall(t)
	p := settingsPayload(guiUIState{BgImage: filepath.Join(t.TempDir(), "nope.jpg")})
	if p["wallURL"] != "" {
		t.Errorf("读不到图时不该给出地址，得到 %v", p["wallURL"])
	}
	if s, _ := p["wallReason"].(string); s == "" {
		t.Error("读不到图时要给一句原因，面板上没有别的信息可用")
	}
	if !strings.Contains(guiPageHTML, "u.wallReason") {
		t.Error("面板脚本没有用 wallReason —— 那句话到不了界面上")
	}
}

// 首屏就要带上背景图，否则打开界面会先空一下再出图。
func TestRenderGuiPageInjectsWallpaper(t *testing.T) {
	withTempConfig(t)
	withTempWall(t)
	src := filepath.Join(t.TempDir(), "w.jpg")
	writeTestJPEG(t, src, 800, 600)
	if _, err := saveUI(guiUIState{BgImage: src}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()
	page := renderGuiPage()
	for _, want := range []string{`data-wall="on"`, `--wall:url('/bg/bg-`} {
		if !strings.Contains(page, want) {
			t.Errorf("页面上应当带上 %s —— 背景图在首屏就该定位好", want)
		}
	}
}

// writeTestJPEG 造一张指定尺寸的测试图。
func writeTestJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readTestJPEG 读回一张 JPEG。
func readTestJPEG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}
