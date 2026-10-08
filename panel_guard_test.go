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
func TestEverySwitchInThePanelIsCollected(t *testing.T) {
	panel := bodyBetween(t, "function uiSettingsHTML", "function uiCollect")
	collect := bodyBetween(t, "function uiCollect", "function uiApply")

	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`uiSw\('([A-Za-z]+)'`).FindAllStringSubmatch(panel, -1) {
		key := m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		if !strings.Contains(collect, "'"+key+"'") {
			t.Errorf("设置面板里有开关 %q，但 uiCollect() 从来没读它 —— 拨了不会有任何反应，"+
				"而且下一次任意保存都会把它打回默认", key)
		}
	}
	if len(seen) == 0 {
		t.Fatal("一个开关都没解析出来 —— 面板生成函数改名了，这条检查等于没做")
	}
}

// 分段器（uiSeg）与输入框（uiField）同理：确认收集端扫了对应的 data 属性。
func TestSegmentAndFieldAreCollectedGenerically(t *testing.T) {
	panel := bodyBetween(t, "function uiSettingsHTML", "function uiCollect")
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

// 空闲时不许留无限动画：animation: 只准出现在「执行中」的状态点上或被写成 animation:none。
func TestNoIdleAnimation(t *testing.T) {
	for _, r := range cssRules(cssRegion(t)) {
		if !strings.Contains(r.body, "animation:") {
			continue
		}
		sel := effSel(r.sel)
		if strings.Contains(sel, ".dot.run") {
			continue // 唯一允许：只在任务运行时存在，且只动 transform/opacity
		}
		if regexp.MustCompile(`animation:\s*none`).MatchString(r.body) {
			continue // 精简 / 系统偏好两条覆盖规则把它关掉
		}
		t.Errorf("%s 上出现了 animation —— 空闲动画会推翻「空闲 GPU 0.01%%」那条实测红线；"+
			"要么去掉，要么做成只在运行期存在的形态", sel)
	}
}
