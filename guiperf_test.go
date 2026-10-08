package main

// guiperf_test.go —— 约束界面 CSS/JS 不引入持续 GPU 开销。

import (
	"regexp"
	"strings"
	"testing"
)

// cssRule 取出 ".名字{...}" 的声明体（不含花括号，规则内不嵌套）。
func cssRule(t *testing.T, selector string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(selector) + `\{([^}]*)\}`)
	m := re.FindStringSubmatch(guiPageHTML)
	if m == nil {
		t.Fatalf("界面 CSS 里找不到规则 %s —— 它被改名或删掉了吗？", selector)
	}
	return m[1]
}

// 背景光斑必须保持静态：占满视口的图层带模糊或动画会持续占用 GPU。
func TestGuiBackgroundGlowIsStatic(t *testing.T) {
	body := cssRule(t, ".aurora")
	for _, bad := range []string{"animation", "filter", "backdrop-filter"} {
		if strings.Contains(body, bad) {
			t.Errorf(".aurora 里不该有 %s：它是占满视口的图层，配上模糊或动画就会让界面闲着也烧 GPU"+
				"（实测空闲 13%%→0.01%%、运行中 51%%→0.6%% 就是去掉它换来的）", bad)
		}
	}
	// 无动画时不应外扩：放大会让光栅化面积翻倍
	if strings.Contains(body, "inset:-") {
		t.Error(".aurora 不该比视口还大（inset 为负）：没有动画位移就不需要外扩，那只会让光栅化面积翻倍")
	}
}

// @keyframes 只可动画 transform / opacity，避免每帧重绘。
func TestGuiKeyframesStayCompositorFriendly(t *testing.T) {
	open := regexp.MustCompile(`@keyframes\s+[\w-]+\s*\{`)
	locs := open.FindAllStringIndex(guiPageHTML, -1)
	if len(locs) == 0 {
		t.Skip("界面里没有 @keyframes —— 那就没什么可查的")
	}
	for _, loc := range locs {
		brace := loc[1] - 1
		body, ok := matchBraces(guiPageHTML[brace:])
		if !ok {
			t.Fatalf("第 %d 字节处的 @keyframes 花括号不配对，没法检查", loc[0])
		}
		for _, bad := range []string{"box-shadow", "filter", "mask"} {
			if strings.Contains(body, bad) {
				t.Errorf("@keyframes 里不该动画 %s：那是每帧重新绘制，纯浪费；"+
					"改用 transform / opacity 让合成器去做（呼吸圈就是这么改的）", bad)
			}
		}
	}
}

// matchBraces 从以 '{' 开头的字符串里取出配对内容，返回 ... 与是否配对成功
func matchBraces(s string) (string, bool) {
	if s == "" || s[0] != '{' {
		return "", false
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[1:i], true
			}
		}
	}
	return "", false
}

// 轮询间隔需要下限：过密会让页面持续重排重绘。
func TestGuiPollingIntervalStaysReasonable(t *testing.T) {
	re := regexp.MustCompile(`setInterval\(\s*tick\s*,\s*(\d+)\s*\)`)
	m := re.FindStringSubmatch(guiPageHTML)
	if m == nil {
		t.Fatal("找不到 tick 的轮询间隔 —— 轮询逻辑被重写了吗？请同步更新这条用例")
	}
	ms := 0
	for _, c := range m[1] {
		ms = ms*10 + int(c-'0')
	}
	if ms < 300 {
		t.Errorf("轮询间隔是 %dms，太密了：每次 tick 都可能改 DOM，进而让整页重排重绘（当前 700ms 足够跟手）", ms)
	}
}
