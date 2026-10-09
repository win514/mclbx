package main

// panel_guard_test.go 设置面板与样式约定的三条护栏。

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// bodyBetween 取 guiPageHTML 里 from 与 to 之间的那一段。
func bodyBetween(t *testing.T, from, to string) string {
	t.Helper()
	i := strings.Index(guiPageHTML, from)
	if i < 0 {
		t.Fatalf("找不到 %q —— 它被改名或删掉了吗？", from)
	}
	j := strings.Index(guiPageHTML[i:], to)
	if j < 0 {
		t.Fatalf("找不到 %q（在 %q 之后）", to, from)
	}
	return guiPageHTML[i : i+j]
}

// TestEverySwitchInThePanelIsCollected 检查面板上每个开关都被 uiCollect() 读到。
//
// 分段与输入框走的是通用扫描（见下一条），开关不是：每个开关值怎么落到配置上都不同
// （例如「记住」存的是关掉、「帧率自动降级」也存的是关掉），所以必须逐个显式处理。
// 漏一个的症状是：拨了没反应，而且下一次任意保存都会把它打回默认。
func TestEverySwitchInThePanelIsCollected(t *testing.T) {
	panel := bodyBetween(t, "function uiSettingsHTML(u){", "async function openSettings(){")
	collect := bodyBetween(t, "function uiCollect", "function uiApply")

	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`uiSw\('([A-Za-z]+)'`).FindAllStringSubmatch(panel, -1) {
		key := m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		if !strings.Contains(collect, "'"+key+"'") {
			t.Errorf("面板上的开关 %q 没有出现在 uiCollect() 里 —— 拨了不会有任何反应，"+
				"而且下一次任意保存都会把它打回默认", key)
		}
	}
	if len(seen) == 0 {
		t.Fatal("一个开关都没解析出来 —— 面板函数改名了，这条检查等于没做")
	}
}

// 分段器（uiSeg）与输入框（uiField）同理：确认收集端扫了对应的 data 属性。
func TestSegmentAndFieldAreCollectedGenerically(t *testing.T) {
	panel := bodyBetween(t, "function uiSettingsHTML(u){", "async function openSettings(){")
	collect := bodyBetween(t, "function uiCollect", "function uiApply")

	if strings.Contains(panel, "uiSeg('") && !strings.Contains(collect, "[data-seg]") {
		t.Error("面板里有分段控件，但 uiCollect() 没有扫 [data-seg] —— 所有分段都会白点")
	}
	if strings.Contains(panel, "uiField('") && !strings.Contains(collect, "[data-in]") {
		t.Error("面板里有输入框，但 uiCollect() 没有扫 [data-in] —— 填了不会保存")
	}
}

type panelCSSRule struct{ sel, body string }

// cssRules 把样式表里所有 `选择器{声明}` 抠出来；媒体查询内的规则 sel 会带 @media 前缀。
func cssRules(css string) []panelCSSRule {
	var out []panelCSSRule
	for _, m := range regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		out = append(out, panelCSSRule{sel: m[1], body: m[2]})
	}
	return out
}

// effSel 取选择器里真正的那一段（去掉媒体查询前缀与换行）。
func effSel(sel string) string {
	if i := strings.LastIndex(sel, "\n"); i >= 0 {
		sel = sel[i+1:]
	}
	return strings.TrimSpace(sel)
}

// declSet 把一段声明拆成排序后的集合。
func declSet(body string) []string {
	var out []string
	for _, d := range strings.Split(body, ";") {
		d = strings.TrimSpace(d)
		if d != "" {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// 「精简」动效与系统的 prefers-reduced-motion 必须逐条相同，且「精简」不许碰背景光效。
func TestLiteMotionMatchesSystemPreference(t *testing.T) {
	css := cssRegion(t)
	var lite, sys []string
	for _, r := range cssRules(css) {
		if sel := effSel(r.sel); strings.Contains(sel, `:root[data-motion="lite"]`) {
			if strings.Contains(sel, ".bg") || strings.Contains(sel, ".aurora") || strings.Contains(sel, ".grid") {
				t.Errorf("「精简」动了背景光效那一层（%s）—— 背景光效只归 data-backdrop 管", sel)
			}
			lite = append(lite, declSet(r.body)...)
		}
	}
	// 先定位媒体查询块再在块内解析，否则 @media 的花括号会把前缀截断。
	i := strings.Index(css, "@media (prefers-reduced-motion")
	if i < 0 {
		t.Fatal("找不到 prefers-reduced-motion 的规则 —— 这条检查等于没做")
	}
	j := strings.Index(css[i:], "{")
	if j < 0 {
		t.Fatal("prefers-reduced-motion 后面的花括号不见了")
	}
	mediaBody, ok := matchBraces(css[i+j:])
	if !ok {
		t.Fatal("prefers-reduced-motion 的花括号不配对")
	}
	for _, r := range cssRules(mediaBody) {
		sys = append(sys, declSet(r.body)...)
	}
	sort.Strings(lite)
	sort.Strings(sys)

	if len(lite) == 0 {
		t.Fatal("找不到 data-motion=\"lite\" 的规则 —— 这条检查等于没做")
	}
	if len(sys) == 0 {
		t.Fatal("prefers-reduced-motion 块里一条声明都没有 —— 这条检查等于没做")
	}
	if strings.Join(lite, "|") != strings.Join(sys, "|") {
		t.Errorf("「精简」与系统偏好已经不是同一套规则了：\n  lite   = %v\n  system = %v\n"+
			"两份必须逐条相同，否则同一件事会有两种表现", lite, sys)
	}
}

// 空闲时不许留**无限**动画。
//
// 口径原来更严：除「执行中」那个状态点外，全表不许出现 animation。加面板淡入时放宽成现在这样 ——
// 真正烧 GPU 的是"永远在跑"的那种，而一次性动画（淡入 0.16 秒就结束）不是常驻开销。
// 放宽带三条约束，缺一条它就变回常驻开销：不能是 infinite；必须有明确时长；
// 不能挂在整屏图层上（整屏 + 动画正是那条「空闲 13%」红线的机制）。
func TestNoIdleAnimation(t *testing.T) {
	css := cssRegion(t)
	infinite := regexp.MustCompile(`animation[^;}]*\binfinite\b`)
	hasDur := regexp.MustCompile(`animation:[^;}]*\d+(\.\d+)?m?s`)
	varDur := regexp.MustCompile(`animation:[^;}]*var\((--[\w-]+)\)`)
	fullScreen := regexp.MustCompile(`inset\s*:\s*0(px)?\s*;`)
	// 时长可以写字面量，也可以走变量（面板淡入走变量，好让精简动效把它归零）。
	// 走变量时必须在样式表里查得到那个变量确实是个时间值 —— 否则"有明确时长"这句话没被验证到。
	varIsTime := func(name string) bool {
		return regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*[\d.]+m?s`).MatchString(css)
	}
	for _, r := range cssRules(css) {
		if !strings.Contains(r.body, "animation:") {
			continue
		}
		sel := effSel(r.sel)
		if strings.Contains(sel, ".dot.run") {
			continue // 有意保留：只在任务运行期间存在，是"正在干活"的唯一反馈
		}
		if regexp.MustCompile(`animation:\s*none`).MatchString(r.body) {
			continue // 精简 / 系统偏好两条覆盖规则把它关掉
		}
		if infinite.MatchString(r.body) {
			t.Errorf("%s 上是无限动画 —— 空闲时它会一直重绘，推翻「空闲 GPU 0.01%%」那条实测红线；"+
				"要么去掉，要么做成按需触发的一次性动效", sel)
			continue
		}
		finite := hasDur.MatchString(r.body)
		if !finite {
			if m := varDur.FindStringSubmatch(r.body); m != nil {
				finite = varIsTime(m[1])
			}
		}
		if !finite {
			t.Errorf("%s 上的 animation 查不到明确时长：%q —— 看不出它一共跑多久，"+
				"也就无从判断它会不会一直占着合成器", sel, strings.TrimSpace(r.body))
			continue
		}
		if fullScreen.MatchString(r.body) {
			t.Errorf("整屏图层 %s 上有动画 —— 占满视口的一次性动画同样是整屏逐帧重算", sel)
		}
	}
}
