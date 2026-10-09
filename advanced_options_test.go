package main

import (
	"os"
	"strings"
	"testing"
)

/* 高级显示选项（面板透明度、背景图明暗）的护栏。
   这一层对用户是三项硬承诺，所以每条都有用例：
     一、出厂值下不写任何覆盖 —— 没动过这两根滑杆的人，拿到的必须是与引入这两项之前
         逐值相同的取值；
     二、倍率在 CSS 里以"未设置即 1"的形态出现，于是"不写覆盖"与"倍率是 1"是同一件事，
         不存在"写了覆盖但倍率是 1"这种中间状态；
     三、越过出厂值当场提示，不硬卡 —— 可读性由用户自己决定，但必须有提示。 */

// TestAdvancedOptionNormalize 越界、非数字与空值一律回到出厂值，不留半个合法值。
func TestAdvancedOptionNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "50"}, {"   ", "50"}, {"50", "50"}, {"0", "0"}, {"100", "100"},
		{" 70 ", "70"}, {"-1", "50"}, {"101", "50"}, {"abc", "50"}, {"1e2", "50"},
		{"50.5", "50"}, {"+60", "60"},
	}
	for _, c := range cases {
		if got := clampPercent(c.in); got != c.want {
			t.Errorf("clampPercent(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 两项互相独立：一项越界不该把另一项也打回出厂。
	u := normalizeUI(guiUIState{Transparency: "80", WallBright: "999"})
	if u.Transparency != "80" || u.WallBright != "50" {
		t.Errorf("归一化后是 %q / %q，期望 \"80\" / \"50\"", u.Transparency, u.WallBright)
	}
	// 出厂常量本身：Go 与前端各有一份同值的定义，两边不一致就会出现"看到的是出厂值、
	// 服务端存的却不是"这种没人查得出来的错，所以这里把它钉住。
	if uiPctDefault != 50 || uiPctMin != 0 || uiPctMax != 100 {
		t.Errorf("百分比常量是 %d/%d/%d，期望 0/100/50", uiPctMin, uiPctMax, uiPctDefault)
	}
	if !strings.Contains(guiPageHTML, "var ADV_FACTORY = 50;") {
		t.Error("前端的出厂常量不是 50 —— 与 Go 那边对不上")
	}
}

// TestAdvancedOptionsExistInThePanel 两根滑杆确实在设置页上，且刻度是 0 到 100。
func TestAdvancedOptionsExistInThePanel(t *testing.T) {
	for _, want := range []string{
		"uiSub('高级选项'",
		"uiRange('transparency', u.transparency, 0, 100)",
		"uiRange('wallBright', u.wallBright, 0, 100)",
		"'面板透明度'",
		"'背景图明暗'",
	} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("设置页里找不到 %q", want)
		}
	}
	// 出厂值在正中间，所以两端都是有效方向；写成 0 到 100 的滑杆而不是两态开关，
	// 是因为这一层的目的正是让用户自己定，而不是替用户定。
	if strings.Contains(guiPageHTML, "uiSeg('transparency'") || strings.Contains(guiPageHTML, "uiSeg('wallBright'") {
		t.Error("高级选项被做成了分段开关，但这一层要的是可调区间")
	}
}

// TestAdvancedFactoryWritesNoOverride 出厂值必须走 removeProperty：不写覆盖。
// 这条是"出厂观感逐值不变"的唯一保证，不能靠"倍率等于 1 所以无所谓"。
func TestAdvancedFactoryWritesNoOverride(t *testing.T) {
	if !strings.Contains(guiPageHTML, "if(k === 0){ el.style.removeProperty(prop); return; }") {
		t.Error("出厂值没有走 removeProperty —— 会留下一个内容为 1 的覆盖属性")
	}
	if !strings.Contains(guiPageHTML, "put('--adv-glass', transparency, 0.60)") ||
		!strings.Contains(guiPageHTML, "put('--adv-scrim', wallBright, 0.75)") {
		t.Error("两个倍率的换算步长没找到")
	}
	// 回落到 1 的形态：没有它，"未设置"与"设成 1"就是两件事。
	if n := strings.Count(guiPageHTML, "var(--adv-glass,1)"); n != 3 {
		t.Errorf("玻璃三层里以 var(--adv-glass,1) 出现的倍率有 %d 处，期望 3 处", n)
	}
	if n := strings.Count(guiPageHTML, "var(--adv-scrim,1)"); n != 1 {
		t.Errorf("压暗层的倍率有 %d 处，期望 1 处", n)
	}
}

// TestAdvancedOptionIsRegistered 两项都要登记进字段表 ——
// 恢复默认、导出、导入三处都按这张表走，漏登记的症状是"改了但重置不动它"。
func TestAdvancedOptionIsRegistered(t *testing.T) {
	for _, k := range []string{"transparency", "wallBright"} {
		info, ok := uiFieldLabels[k]
		if !ok {
			t.Fatalf("字段表里没有 %s", k)
		}
		if info.Face != "主题外观" {
			t.Errorf("%s 归到了 %q，期望「主题外观」", k, info.Face)
		}
		if info.Name == "" {
			t.Errorf("%s 没有中文名", k)
		}
	}
	// 服务端要把两项交给界面：不发的话面板显示的是默认值，用户改了也看不到回显。
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatalf("读不到 gui.go：%v", err)
	}
	for _, want := range []string{`"transparency":`, `"wallBright":`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("界面状态里没有下发 %s", want)
		}
	}
}

// TestAdvancedOptionWarnsPastFactory 越过出厂值必须当场提示，且说明行要就地刷新。
//
// 说明行是浏览器端按滑杆的值算出来的，Go 这边没有 JS 引擎可以执行它，所以这里核对源码里
// 的判据形态：提示句存在，且只在值大于出厂值时出现 —— 而"拖动时会更新"靠的是说明行登记进
// uiRefreshHints，那一条也一并盯住，否则改完滑杆要等重开面板才看得见提示。
func TestAdvancedOptionWarnsPastFactory(t *testing.T) {
	for _, want := range []string{
		"if(v > ADV_FACTORY){ return '已越过出厂值：部分文字可能看不清。'; }",
		"if(v < ADV_FACTORY){ return '比出厂值更保守，文字更容易看清。'; }",
		`['[data-rng="transparency"]', advHintText('transparency', u)]`,
		`['[data-rng="wallBright"]', advHintText('wallBright', u)]`,
	} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("说明行少了这一处：%q", want)
		}
	}
}
