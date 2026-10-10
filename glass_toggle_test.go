package main

// glass_toggle_test.go —— 毛玻璃收成「开 / 关」两态。
//
// 契约：只有 off / on 两个取值，默认 on；on 即此前最透明的那一档，无中间态。
// 旧配置的数字（"0"…"10"）与更早的 low/mid/high 一律迁移成两态，不打回默认。

import (
	"strconv"
	"strings"
	"testing"
)

// cssVarFloat 取一条 CSS 块里某个变量的数值（允许带 px 单位）。
func cssVarFloat(t *testing.T, block, name string) float64 {
	t.Helper()
	vars := cssVars(block)
	raw, ok := vars[name]
	if !ok {
		t.Fatalf("CSS 块里取不到 %s：%q", name, block)
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(raw), "px"), 64)
	if err != nil {
		t.Fatalf("%s 的值 %q 解析不了", name, raw)
	}
	return f
}

// 只有两态，默认开。
func TestGlassIsATwoStateToggle(t *testing.T) {
	if len(glassValues) != 2 || glassValues[0] != "off" || glassValues[1] != "on" {
		t.Fatalf("毛玻璃的合法取值应当是 [off on]，实际 %v", glassValues)
	}
	if uiGlassDefault != "on" {
		t.Errorf("毛玻璃默认值应当是一开就是最透明那档（on），实际 %q", uiGlassDefault)
	}

	cases := []struct{ in, want string }{
		{"", "on"},   // 空值 → 默认
		{"on", "on"}, // 开
		{"off", "off"},
		{" off ", "off"},                       // 去空格后再判定
		{"ON", "on"},                           // 大小写不敏感
		{"1", "on"}, {"5", "on"}, {"10", "on"}, // 旧的数字档：任何非零档都迁到 on
		{"0", "off"},                                 // 旧的 0 档是"关闭"
		{"low", "on"}, {"mid", "on"}, {"high", "on"}, // 更早的四档
		{"yes", "on"}, // 认不出来的值 → 默认（开），不是关
	}
	for _, c := range cases {
		if got := normalizeUI(guiUIState{Glass: c.in}).Glass; got != c.want {
			t.Errorf("normalizeUI(Glass=%q).Glass = %q，期望 %q —— 迁移要保留用户意图，不能一律打回默认",
				c.in, got, c.want)
		}
	}
}

// 开态取最透明的那一档，且不许越过上限。
func TestGlassOnUsesTheMostTransparentState(t *testing.T) {
	body := cssRule(t, `:root[data-glass="on"]:not([data-theme="contrast"])`)
	for _, k := range []string{"--g1a", "--g2a", "--g3a", "--glass-blur", "--blur-3"} {
		if !strings.Contains(body, k+":") {
			t.Errorf("开态块里缺少 %s —— 档位表收成一块之后，这一块要给出全部取值：%q", k, body)
		}
	}
	// 开态模糊半径跟性能预算走（空闲 GPU 接近 0，峰值不超过 5%）；写成等值断言，
	// 使"再往上加"必须是一次有意识的改动。
	const (
		wantBlur  = 24
		wantBlur3 = 32
	)
	blur := cssVarFloat(t, body, "--glass-blur")
	blur3 := cssVarFloat(t, body, "--blur-3")
	if blur != wantBlur {
		t.Errorf("开态面板模糊是 %.0fpx，期望 %dpx —— 这个值跟性能预算走，改它要有依据", blur, wantBlur)
	}
	if blur3 != wantBlur3 {
		t.Errorf("开态浮层模糊是 %.0fpx，期望 %dpx —— 浮层要比主容器强，同样跟预算走", blur3, wantBlur3)
	}
	if blur3 <= blur {
		t.Errorf("浮层模糊 %.0fpx 不大于面板模糊 %.0fpx —— 浮层该更糊", blur3, blur)
	}
	// “最透明”是这套取值的方向：三档不透明度都必须小于 1，否则等于实心。
	for _, k := range []string{"--g1a", "--g2a", "--g3a"} {
		if v := cssVarFloat(t, body, k); v >= 1 {
			t.Errorf("开态应当是最透明那档，但 %s = %.3f 不小于 1", k, v)
		}
	}
}

// 档位收成一块后 CSS 里不该再有数字档位块，留着就是死规则。
func TestNoNumericGlassLevelsRemain(t *testing.T) {
	region := cssRegion(t)
	if strings.Contains(region, `:root[data-glass="0"]`) {
		t.Error(`还有 :root[data-glass="0"] —— 0 已经不是合法取值，这块规则永远匹配不到`)
	}
	for _, lv := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"} {
		if strings.Contains(region, `:root[data-glass="`+lv+`"]`) {
			t.Errorf(`还留着 :root[data-glass="%s"] —— 档位已经收成开/关，这块是死规则`, lv)
		}
	}
}

// 玻璃规则挂在开态上：关闭时完全不参与，而不是靠"不等于 0"这种反向条件。
func TestGlassRulesKeyOnTheOnState(t *testing.T) {
	region := cssRegion(t)
	if !strings.Contains(region, `:root[data-glass="on"]`) {
		t.Error(`样式表里没有 :root[data-glass="on"] —— 玻璃规则没有挂在开态上`)
	}
	if strings.Contains(region, `:not([data-glass="0"])`) {
		t.Error(`样式表里还有 :not([data-glass="0"]) —— 那是数字档位时代的写法，` +
			`现在应当直接写 [data-glass="on"]`)
	}
}

// 设置页给的是一个开关，不是一个滑杆。
func TestSettingsOfferAToggleNotASlider(t *testing.T) {
	if !strings.Contains(guiPageHTML, "uiSeg('glass'") {
		t.Error("设置页的毛玻璃控件不是 uiSeg('glass', …) —— 两态应当用分段开关，与「材质」那一项一致")
	}
	if strings.Contains(guiPageHTML, "uiRange('glass'") {
		t.Error("设置页还在用 uiRange('glass', …) —— 滑杆已经取消，档位收成了开/关")
	}
	if strings.Contains(guiPageHTML, "GLASS_MAX") {
		t.Error("前端还留着 GLASS_MAX —— 档位上限这个概念已经不存在了")
	}
	if !strings.Contains(guiPageHTML, "GLASS_DEF = 'on'") {
		t.Error("前端的 GLASS_DEF 不是 'on' —— 首屏默认值要与 guiconfig.go 的 uiGlassDefault 一致")
	}
}

// 设置项的说明里不该再讲“越大越透明”这种档位话术。
func TestGlassHintNoLongerTalksAboutLevels(t *testing.T) {
	// glassHintText 在前端脚本里，这里扫页面源码。
	i := strings.Index(guiPageHTML, "function glassHintText(u){")
	if i < 0 {
		t.Fatal("前端的 glassHintText 不见了 —— 这条检查等于没做")
	}
	end := strings.Index(guiPageHTML[i:], "\n}")
	if end < 0 {
		t.Fatal("glassHintText 的花括号不配对")
	}
	hint := guiPageHTML[i : i+end]
	if strings.Contains(hint, "0 到 10") || strings.Contains(hint, "越大越透明") {
		t.Errorf("毛玻璃的说明还在讲档位：%q", hint)
	}
	if !strings.Contains(hint, "开") {
		t.Errorf("毛玻璃的说明应当讲清开与关的区别：%q", hint)
	}
}
