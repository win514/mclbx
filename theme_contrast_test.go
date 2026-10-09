package main

// theme_contrast_test.go 对主题配色做对比度数值断言；取值一律从 CSS 读取，不抄颜色常量。

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type rgba struct{ r, g, b, a float64 }

// parseColor 认 #RGB / #RRGGBB / rgb() / rgba()。
func parseColor(s string) (rgba, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch {
	case strings.HasPrefix(s, "#"):
		h := s[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 {
			return rgba{}, false
		}
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return rgba{}, false
		}
		return rgba{float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff), 1}, true
	case strings.HasPrefix(s, "rgb"):
		i := strings.Index(s, "(")
		j := strings.LastIndex(s, ")")
		if i < 0 || j < i {
			return rgba{}, false
		}
		var parts []float64
		for _, p := range strings.Split(s[i+1:j], ",") {
			f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return rgba{}, false
			}
			parts = append(parts, f)
		}
		if len(parts) < 3 {
			return rgba{}, false
		}
		a := 1.0
		if len(parts) >= 4 {
			a = parts[3]
		}
		return rgba{parts[0], parts[1], parts[2], a}, true
	}
	return rgba{}, false
}

// over 把半透明前景合成到底色上。
func over(fg, bg rgba) rgba {
	return rgba{
		fg.r*fg.a + bg.r*(1-fg.a),
		fg.g*fg.a + bg.g*(1-fg.a),
		fg.b*fg.a + bg.b*(1-fg.a),
		1,
	}
}

func relLum(c rgba) float64 {
	f := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.r) + 0.7152*f(c.g) + 0.0722*f(c.b)
}

func contrast(a, b rgba) float64 {
	la, lb := relLum(a), relLum(b)
	hi, lo := la, lb
	if lb > la {
		hi, lo = lb, la
	}
	return (hi + 0.05) / (lo + 0.05)
}

// pair 一条对比度检查：fg 压在 base（再压 under）上，比值不低于 min。
type pair struct {
	name  string
	fg    string // 变量名
	base  string // 背景变量名；为空表示"直接压在 --bg 上"
	under string // base 若是半透明，先压在这一层上
	min   float64
}

// 阈值统一为 4.5，适用于所有需要阅读的文字（含行号、底栏、计数等结构性文字）。
// 有意不纳入：1px 描边、装饰渐变与选中底色、悬浮态、页面氛围 —— 它们不承载文字信息。
var contrastTargets = []pair{
	{"正文", "--ink", "", "", 4.5},
	{"次级", "--ink2", "", "", 4.5},
	{"三级（提示/分组标题）", "--muted", "", "", 4.5},
	{"四级（行号/底栏/计数）", "--muted2", "", "", 4.5},
	{"结论-正常", "--ok-ink", "", "", 4.5},
	{"结论-失败", "--bad-ink", "", "", 4.5},
	{"结论-注意", "--warn-ink", "", "", 4.5},
	{"结论-说明", "--info-ink", "", "", 4.5},
	{"实心块上的字", "--on-sig", "--sig", "", 4.5},
	{"主按钮上的字", "--bar-ink", "--sig", "", 4.5},
	{"地址卡上的地址", "--addr-ink", "--plate", "--surf", 4.5},
	{"加入命令那一行", "--violet-ink", "--violet-soft", "--surf", 4.5},
	{"提示条上的话", "--amber-ink", "--amber-soft", "--bg", 4.5},
	{"危险按钮上的字", "--rose-ink", "--rose-soft", "--surf2", 4.5},
	{"浮层-成功", "--toast-ok-ink", "--toast-ok-bg", "", 4.5},
	{"浮层-失败", "--toast-bad-ink", "--toast-bad-bg", "", 4.5},
}

// 文字须压在每个界面上都满足对比度。
var textSurfaces = []string{"--bg", "--bg2", "--surf", "--surf2", "--surf3", "--chrome"}

// 遍历每个「主题 × 强调色」组合逐一测量对比度，新增主题或强调色会被自动覆盖。
func TestThemeContrastMeetsWCAG(t *testing.T) {
	themes := uniq(append([]string{"dark"}, themeValues...))
	accents := uniq(append([]string{"mint"}, accentValues...))

	type row struct {
		what  string
		ratio float64
		min   float64
		ok    bool
	}
	var failures []string
	worst := map[string]float64{} // 每个组合里最紧的一项

	for _, theme := range themes {
		for _, acc := range accents {
			v := resolveVars(t, theme, acc)
			label := theme + "+" + acc
			check := func(what string, fgVar, baseVar, underVar string, min float64) {
				if baseVar == "" {
					baseVar = "--bg" // 表里留空 = 压在页底上
				}
				fg, ok1 := parseColor(v[fgVar])
				bg, ok2 := parseColor(v[baseVar])
				if !ok1 || !ok2 {
					failures = append(failures, fmt.Sprintf("%s %s：%s 或 %s 取不到颜色（%q / %q）",
						label, what, fgVar, baseVar, v[fgVar], v[baseVar]))
					return
				}
				if bg.a < 1 && underVar != "" {
					if u, ok := parseColor(v[underVar]); ok {
						bg = over(bg, u)
					}
				}
				// 前景若为半透明也需合成（当前无此情况）
				if fg.a < 1 {
					fg = over(fg, bg)
				}
				r := contrast(fg, bg)
				if w, ok := worst[label]; !ok || r < w {
					worst[label] = r
				}
				if r+1e-9 < min {
					failures = append(failures, fmt.Sprintf("%s %s（%s 压在 %s 上）= %.2f，要求 ≥ %.1f",
						label, what, fgVar, baseVar, r, min))
				}
				_ = row{}
			}
			for _, p := range contrastTargets {
				check(p.name, p.fg, p.base, p.under, p.min)
			}
			// 各级文字 × 每个面
			for _, tok := range []string{"--ink", "--ink2", "--muted", "--muted2"} {
				for _, surf := range textSurfaces {
					check("文字压在 "+surf, tok, surf, "", 4.5)
				}
			}
		}
	}

	// 输出每个组合最紧的一项作为测量留档
	keys := make([]string, 0, len(worst))
	for k := range worst {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-18s 最紧的一条 = %.2f", k, worst[k])
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		for _, f := range failures {
			t.Error(f)
		}
		t.Fatalf("有 %d 条不达标的组合 —— 加主题/强调色之前先把它们修掉", len(failures))
	}
}

// 验证半透明层先合成再比较，确保上表的合成正确。
func TestAlphaCompositingIsApplied(t *testing.T) {
	// rgba(0,0,0,.5) 压在白色上 = 50% 灰
	got := over(rgba{0, 0, 0, 0.5}, rgba{255, 255, 255, 1})
	if got.r != 127.5 || got.a != 1 {
		t.Errorf("合成结果不对：%+v", got)
	}
	for _, s := range []string{"#fff", "#FFFFFF", "rgb(255,255,255)", "rgba(255,255,255,1)"} {
		c, ok := parseColor(s)
		if !ok || c.r != 255 || c.a != 1 {
			t.Errorf("parseColor(%q) = %+v ok=%v", s, c, ok)
		}
	}
	if _, ok := parseColor("var(--ink)"); ok {
		t.Error("变量引用不该被当成颜色 —— 那说明取值表里有没解析开的 var()")
	}
	if r := contrast(rgba{0, 0, 0, 1}, rgba{255, 255, 255, 1}); r < 20.9 || r > 21.1 {
		t.Errorf("黑白对比度应当是 21，得到 %.2f", r)
	}
}

// 对比度表引用的变量名必须真实存在，否则检查无效。
func TestContrastTableReferencesRealVars(t *testing.T) {
	v := resolveVars(t, "dark", "mint")
	for _, p := range contrastTargets {
		for _, name := range []string{p.fg, p.base, p.under} {
			if name == "" {
				continue
			}
			if _, ok := v[name]; !ok {
				t.Errorf("对比度表引用了不存在的变量 %s —— 这条检查等于没做", name)
			}
		}
	}
	for _, s := range textSurfaces {
		if _, ok := v[s]; !ok {
			t.Errorf("对比度表引用了不存在的面 %s", s)
		}
	}
	// 表里不得出现颜色字面量
	re := regexp.MustCompile(`#[0-9A-Fa-f]{3,8}`)
	for _, p := range contrastTargets {
		for _, name := range []string{p.fg, p.base, p.under} {
			if re.MatchString(name) {
				t.Errorf("对比度表里出现了颜色字面量 %s —— 值必须从 CSS 里读", name)
			}
		}
	}
}

// 控件边界检查：填充与描边中较强的一条不得低于 boundaryFloor，防止两者同时消失。
// 不变式：控件须「靠填充与面对比」或「靠描边与面对比」，两者至少一条成立。
const boundaryFloor = 1.10

// uniq 去重，避免 dark 同时出现在字面与 themeValues 中导致重复。
func uniq(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

type boundaryTarget struct {
	name   string
	fill   string // 控件填充色
	border string // 控件描边色
	surf   string // 它压在哪个面上（填充与描边都压在这上面）
}

var boundaryTargets = []boundaryTarget{
	{"输入框（设置面板）", "--field", "--hair2", "--surf2"},
	{"搜索框", "--search", "--line", "--chrome"},
	{"面板与页底", "--surf", "--line", "--bg"},
	{"控件底与卡片", "--surf2", "--line2", "--surf"},
	{"地址卡", "--plate", "--line2", "--surf"},
}

func TestControlBoundaryIsVisible(t *testing.T) {
	themes := uniq(append([]string{"dark"}, themeValues...))
	accents := uniq(append([]string{"mint"}, accentValues...))

	var failures []string
	worst := map[string]float64{}

	for _, theme := range themes {
		for _, acc := range accents {
			v := resolveVars(t, theme, acc)
			label := theme + "+" + acc
			for _, b := range boundaryTargets {
				surf, ok := parseColor(v[b.surf])
				if !ok {
					failures = append(failures, fmt.Sprintf("%s %s：面 %s 取不到颜色（%q）", label, b.name, b.surf, v[b.surf]))
					continue
				}
				best, which := 0.0, ""
				for _, cand := range []struct{ varName, kind string }{{b.fill, "填充"}, {b.border, "描边"}} {
					col, ok := parseColor(v[cand.varName])
					if !ok {
						continue
					}
					if col.a < 1 {
						col = over(col, surf) // 半透明面先合成再比较
					}
					if r := contrast(col, surf); r > best {
						best, which = r, cand.kind
					}
				}
				key := label + " " + b.name
				if w, ok := worst[key]; !ok || best < w {
					worst[key] = best
				}
				if best+1e-9 < boundaryFloor {
					failures = append(failures, fmt.Sprintf(
						"%s %s：填充(%s)与描边(%s)里更强的一条只有 %.2f（靠%s），低于塌陷下限 %.2f —— 控件边界会看不见",
						label, b.name, b.fill, b.border, best, which, boundaryFloor))
				}
			}
		}
	}

	keys := make([]string, 0, len(worst))
	for k := range worst {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-34s 最强边界 = %.2f", k, worst[k])
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		for _, f := range failures {
			t.Error(f)
		}
		t.Fatalf("有 %d 处控件边界看不见 —— 加主题之前先把它们修掉", len(failures))
	}
}

// 边界表引用的变量必须真实存在，否则检查无效。
func TestBoundaryTableReferencesRealVars(t *testing.T) {
	v := resolveVars(t, "dark", "mint")
	for _, b := range boundaryTargets {
		for _, name := range []string{b.fill, b.border, b.surf} {
			if _, ok := v[name]; !ok {
				t.Errorf("边界表引用了不存在的变量 %s —— 这条检查等于没做", name)
			}
		}
	}
}
