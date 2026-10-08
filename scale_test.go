package main

// scale_test.go —— 「界面字号」独立开关的护栏：字号须全部抽成 token，界面选项、归一化白名单、
// CSS 块三者一致，且大字号档覆盖每个字号 token。

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// segOptions 从 `uiSeg('scale', u.scale, [['std','标准'],['big','大']])` 里取出 id 列表。
func segOptions(t *testing.T, key string) []string {
	t.Helper()
	i := strings.Index(guiPageHTML, "uiSeg('"+key+"'")
	if i < 0 {
		t.Fatalf("界面上找不到 uiSeg('%s') —— 那个开关被删了吗？", key)
	}
	body := guiPageHTML[i:]
	if j := strings.Index(body, "])"); j > 0 {
		body = body[:j]
	}
	var out []string
	for _, m := range regexp.MustCompile(`\[\s*'([a-z]+)'\s*,`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("uiSeg('%s') 里一个选项都没解析出来", key)
	}
	return out
}

// 界面选项、归一化白名单、CSS 块三者必须一致。
func TestScaleOptionsAreConsistent(t *testing.T) {
	fromUI := segOptions(t, "scale")
	if strings.Join(fromUI, ",") != strings.Join(scaleValues, ",") {
		t.Errorf("界面上的字号选项 %v 与 normalizeUI 的白名单 %v 对不上", fromUI, scaleValues)
	}
	if fromUI[0] != "std" {
		t.Errorf("字号选项的第一项应当是默认的 std，实际 %q", fromUI[0])
	}
	// 默认档即 :root 本身，不应再有自己的 CSS 块。
	if hasBlock(guiPageHTML, `:root[data-scale="std"]`) {
		t.Error("std 档不该有 CSS 块 —— 它就是 :root 本身")
	}
	for _, name := range scaleValues {
		if name == "std" {
			continue
		}
		sel := `:root[data-scale="` + name + `"]`
		if !hasBlock(guiPageHTML, sel) {
			t.Errorf("字号档 %s 在白名单里、界面上也能选，但 CSS 里没有 %s", name, sel)
		}
	}
}

// 所有字号都必须走 token，裸 px 不会随开关变化。
func TestEveryFontSizeGoesThroughToken(t *testing.T) {
	css := cssRegion(t)
	bad := []*regexp.Regexp{
		regexp.MustCompile(`font-size:\s*[\d.]+px`),
		regexp.MustCompile(`font:\s*(?:\d+\s+)?[\d.]+px`),
	}
	for _, re := range bad {
		for _, m := range re.FindAllString(css, -1) {
			t.Errorf("样式表里出现了裸字号 %q —— 它必须写成 var(--fs-*)，否则字号开关对它无效", m)
		}
	}
}

// 开关覆盖的字号须与 :root 声明完全一致，且每档都要真的变大。
func TestBigScaleCoversEveryFontToken(t *testing.T) {
	std := fontTokens(cssBlock(t, ":root"))
	if len(std) < 10 {
		t.Fatalf(":root 里只解析出 %d 个字号 token，抽取那一步是不是被回退了", len(std))
	}
	big := fontTokens(cssBlock(t, `:root[data-scale="big"]`))

	for name := range std {
		if _, ok := big[name]; !ok {
			t.Errorf("大字号档没有覆盖 %s —— 那一块的字不会跟着变大", name)
		}
	}
	for name, v := range big {
		s, ok := std[name]
		if !ok {
			t.Errorf("大字号档里出现了 :root 没有的 %s —— 拼错名字了？", name)
			continue
		}
		if v <= s {
			t.Errorf("%s 在大字号档里是 %.1fpx，不比标准档的 %.1fpx 大 —— 复制之后忘了改值？", name, v, s)
		}
	}
}

// 顶栏/底栏高度须用 token，且大字号档将其调高。
func TestRowHeightsFollowScale(t *testing.T) {
	std := cssVars(cssBlock(t, ":root"))
	big := cssVars(cssBlock(t, `:root[data-scale="big"]`))
	for _, name := range []string{"--row-top", "--row-bot"} {
		s, ok := std[name]
		if !ok {
			t.Fatalf(":root 里没有 %s", name)
		}
		b, ok := big[name]
		if !ok {
			t.Errorf("大字号档没有覆盖 %s", name)
			continue
		}
		if px(b) <= px(s) {
			t.Errorf("%s 在大字号档里是 %s，不比标准档的 %s 大", name, b, s)
		}
	}
	if !strings.Contains(guiPageHTML, "grid-template-rows:var(--row-top) 1fr var(--row-bot)") {
		t.Error("顶栏/底栏高度没有走 token —— 字号一大就会被切")
	}
}

// 归一化与首屏注入：非法档位回退 std，data-scale 须写在 <html> 上。
func TestScaleNormalizeAndFirstPaint(t *testing.T) {
	if got := normalizeUI(guiUIState{}).Scale; got != "std" {
		t.Errorf("空值的字号档应当是 std，实际 %q", got)
	}
	if got := normalizeUI(guiUIState{Scale: "big"}).Scale; got != "big" {
		t.Errorf("big 被归一化掉了：%q", got)
	}
	if got := normalizeUI(guiUIState{Scale: "huge"}).Scale; got != "std" {
		t.Errorf("没定义的档位应当回到 std，实际 %q", got)
	}
	attr := normalizeUI(guiUIState{Scale: "big"}).htmlAttr()
	if !strings.Contains(attr, `data-scale="big"`) {
		t.Errorf("htmlAttr 没有带上 data-scale：%q", attr)
	}
	if !strings.Contains(normalizeUI(guiUIState{}).htmlAttr(), `data-scale="std"`) {
		t.Error("默认也要显式写 data-scale=std，属性缺省等于把语义交给猜测")
	}
}

// cssRegion 取出 <style> 到 </style> 之间的那一整段。
func cssRegion(t *testing.T) string {
	t.Helper()
	i := strings.Index(guiPageHTML, "<style>")
	j := strings.Index(guiPageHTML, "</style>")
	if i < 0 || j < i {
		t.Fatal("找不到 <style> 区域")
	}
	return guiPageHTML[i:j]
}

// fontTokens 取出一段声明体里所有 --fs-* 的像素值。
func fontTokens(block string) map[string]float64 {
	out := map[string]float64{}
	for name, v := range cssVars(block) {
		if strings.HasPrefix(name, "--fs-") {
			out[name] = px(v)
		}
	}
	return out
}

func px(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "px")), 64)
	if err != nil {
		return 0
	}
	return f
}
