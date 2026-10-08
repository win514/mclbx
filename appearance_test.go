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

// ---- 毛玻璃 ----

// 全表禁真毛玻璃。backdrop-filter 的价值只是观感，代价却是「背后一变就要重新回读 + 重新模糊」，
// 而"背后变"由别人触发 —— 一个正在运行的呼吸点就够让它逐帧重算。
func TestGuiNoBackdropFilterAtAll(t *testing.T) {
	css := cssRegion(t)
	re := regexp.MustCompile(`(-webkit-)?backdrop-filter\s*:\s*([^;}]+)`)
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		if v := strings.TrimSpace(m[2]); v != "none" {
			t.Errorf("样式表里出现了真毛玻璃（%s:%s）：它会让面板背后每次变化都重新回读并模糊，"+
				"代价按玻璃总面积 × 变化频率增长，几块面板加起来就等价于整屏", m[1], v)
		}
	}
	for _, js := range []string{"backdropFilter", "webkitBackdropFilter"} {
		if strings.Contains(guiPageHTML, js) {
			t.Errorf("脚本里出现了 %s —— 动态加上去的毛玻璃同样绕不过这条守卫", js)
		}
	}
}

// 毛玻璃那一族规则只准用颜色与边框，不许出现任何滤波或模糊函数。
func TestGlassRulesUseNoFilter(t *testing.T) {
	found := 0
	for _, r := range cssRules(cssRegion(t)) {
		sel := effSel(r.sel)
		if !strings.Contains(sel, `data-glass="on"`) {
			continue
		}
		found++
		for _, bad := range []string{"backdrop-filter", "-webkit-filter", "filter:", "blur(", "animation"} {
			if strings.Contains(r.body, bad) {
				t.Errorf("%s 里出现了 %s —— 伪毛玻璃只能靠半透明色与 1px 边框，"+
					"任何模糊或滤波都会把它变成每帧重算的图层", sel, bad)
			}
		}
	}
	if found == 0 {
		t.Fatal("找不到 data-glass 的规则 —— 这个开关被删了吗？这条检查等于没做")
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
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"默认材质", normalizeUI(guiUIState{}).Flat, "off"},
		{"非法材质", normalizeUI(guiUIState{Flat: "yes"}).Flat, "off"},
		{"合法材质", normalizeUI(guiUIState{Flat: "on"}).Flat, "on"},
		{"默认毛玻璃", normalizeUI(guiUIState{}).Glass, "off"},
		{"非法毛玻璃", normalizeUI(guiUIState{Glass: "1"}).Glass, "off"},
		{"合法毛玻璃", normalizeUI(guiUIState{Glass: "on"}).Glass, "on"},
	} {
		if c.got != c.want {
			t.Errorf("%s：得到 %q，期望 %q", c.name, c.got, c.want)
		}
	}
	// 首屏注入：两个开关都要显式写在 <html> 上，缺省等于把语义交给猜测
	a := normalizeUI(guiUIState{Flat: "on", Glass: "on"}).htmlAttr()
	for _, want := range []string{`data-flat="on"`, `data-glass="on"`} {
		if !strings.Contains(a, want) {
			t.Errorf("htmlAttr 没有带上 %s：%q", want, a)
		}
	}
	d := normalizeUI(guiUIState{}).htmlAttr()
	for _, want := range []string{`data-flat="off"`, `data-glass="off"`} {
		if !strings.Contains(d, want) {
			t.Errorf("默认也要显式写 %s，否则默认值就靠猜", want)
		}
	}
	// 改完要立刻生效，不能等重开界面
	apply := bodyBetween(t, "function uiApply", "async function uiSave")
	for _, want := range []string{"data-flat", "data-glass"} {
		if !strings.Contains(apply, want) {
			t.Errorf("uiApply 没有设置 %s —— 改完要重开界面才看得到", want)
		}
	}
}

// 三项能力都要在设置面板里有入口，背景层的两个 div 也要真的在页面上。
func TestAppearanceControlsExistInThePanel(t *testing.T) {
	for _, want := range []string{"uiSeg('flat'", "uiSw('glass'", "uiWallPicker(", "'/api/wall/import'"} {
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
		sel := strings.Join(strings.Fields(r.sel), " ")
		if !strings.Contains(sel, `data-glass="on"`) {
			continue
		}
		rules++
		if !strings.Contains(sel, `:not([data-wall="on"])`) {
			t.Errorf("玻璃规则 %s 没有排除「已设背景图」的情形 —— 半透明面板压在一张纯白照片上时，"+
				"第三、四级灰会掉到 4.5 以下（实测最差 3.97）", sel)
		}
	}
	if rules == 0 {
		t.Fatal("找不到 data-glass 的规则 —— 这个开关被删了吗？这条检查等于没做")
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
