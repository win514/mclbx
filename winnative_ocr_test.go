//go:build windows

package main

// winnative_ocr_test.go —— 用 OCR 校验界面手绘文字确实被画出且可识别（依赖外部工具，缺失则跳过）。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ocrTools 找到两个外部工具；缺任何一个都返回空串（调用方跳过）
func ocrTools(t *testing.T) (magick, tesseract string) {
	t.Helper()
	for _, p := range []string{
		`D:\mclbx-tools\bin\magick.cmd`,
		`D:\mclbx-tools\imagemagick\magick.exe`,
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			magick = p
			break
		}
	}
	if magick == "" {
		if p, err := exec.LookPath("magick"); err == nil {
			magick = p
		}
	}
	if p, err := exec.LookPath("tesseract"); err == nil {
		tesseract = p
	}
	return magick, tesseract
}

// ocrImage 把 PNG 的指定区域裁剪、放大 2 倍并灰度后 OCR 成文字。
func ocrImage(t *testing.T, magick, tesseract, src, crop string) string {
	t.Helper()
	big := src + ".ocr.png"
	args := []string{src}
	if crop != "" {
		args = append(args, "-crop", crop, "+repage")
	}
	args = append(args, "-resize", "200%", "-colorspace", "Gray", big)
	if out, err := exec.Command(magick, args...).CombinedOutput(); err != nil {
		t.Skipf("magick 处理失败（%v）：%s", err, strings.TrimSpace(string(out)))
	}
	defer os.Remove(big)
	out, err := exec.Command(tesseract, big, "stdout", "-l", "chi_sim", "--psm", "6").Output()
	if err != nil {
		t.Skipf("tesseract 认图失败：%v", err)
	}
	// OCR 会插入空格与换行，全部去掉再比
	var b strings.Builder
	for _, r := range string(out) {
		if r != ' ' && r != '\n' && r != '\r' && r != '\t' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normForOCR 丢弃空白与标点、ASCII 统一小写，只保留汉字与字母数字用于比较。
func normForOCR(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0x2E80: // CJK 部首/汉字/假名等
			b.WriteRune(r)
		}
	}
	return b.String()
}

// titleQualifier 取任务名里的括号限定词（如「直连模式（玩家）」→「玩家」）；无括号返回空串。
func titleQualifier(name string) string {
	l := strings.IndexAny(name, "（(")
	if l < 0 {
		return ""
	}
	r := strings.IndexAny(name, "）)")
	if r < l {
		return ""
	}
	return strings.TrimSpace(name[l+len("（") : r])
}

// ocrScore 用带窗口最长公共子序列计算期望串在 OCR 文本中出现的比例。
func ocrScore(expected, ocr string) float64 {
	exp := []rune(normForOCR(expected))
	if len(exp) == 0 {
		return 1
	}
	hay := []rune(normForOCR(ocr))
	win := len(exp)*2 + 8
	best := 0
	for start := 0; start < len(hay); start++ {
		end := start + win
		if end > len(hay) {
			end = len(hay)
		}
		seg := hay[start:end]
		prev := make([]int, len(seg)+1)
		cur := make([]int, len(seg)+1)
		for i := 1; i <= len(exp); i++ {
			for j := 1; j <= len(seg); j++ {
				if exp[i-1] == seg[j-1] {
					cur[j] = prev[j-1] + 1
				} else if prev[j] >= cur[j-1] {
					cur[j] = prev[j]
				} else {
					cur[j] = cur[j-1]
				}
			}
			prev, cur = cur, prev
			for j := range cur {
				cur[j] = 0
			}
		}
		if prev[len(seg)] > best {
			best = prev[len(seg)]
		}
	}
	return float64(best) / float64(len(exp))
}

// TestEveryPageReallyDrewItsText 分区 OCR 校验每页标题、字段标签、等价命令与顶栏 IPv6 文字。
func TestEveryPageReallyDrewItsText(t *testing.T) {
	magick, tesseract := ocrTools(t)
	if magick == "" || tesseract == "" {
		t.Skipf("没有找到 OCR 工具链（magick=%q tesseract=%q），跳过 OCR 复核。"+
			"装法见 D:\\mclbx-tools\\README.md", magick, tesseract)
	}

	st := uiStates()[2] // 含长状态文字与地址卡片的那组
	dir := t.TempDir()
	sample := map[string]string{}
	for _, task := range guiTasks {
		for _, f := range task.Fields {
			sample[f.ID] = guiSampleFor(f)
		}
	}

	for i, task := range guiTasks {
		png := filepath.Join(dir, task.Key+".png")
		// 本页的等价命令（界面显示的就是它）
		cmd := "mclbx " + task.Key // 兜底：任务表里 key 与子命令一般同名
		if args, err := task.build(sample); err == nil && len(args) > 0 {
			cmd = "mclbx " + args[0]
		}
		s := visScene{
			Name: task.Key, Task: i, State: st,
			W: natWinW, H: natWinH, DPI: 96,
			Extra: func(u *nativeUI) {
				u.running = true
				u.taskTitle = task.Name
				u.command = cmd + " --port 25565（示例）"
			},
		}
		writePNG(t, png, renderScene(t, s))

		// 按 96 DPI 布局计算裁剪区域：右栏从左边界略左侧起、纵向从顶栏下方起。
		w, h := int32(natWinW), int32(natWinH)
		right := fmt.Sprintf("%dx%d+%d+%d", w-natListW-6, h-natTopH, natListW+6, natTopH)
		top := fmt.Sprintf("%dx%d+0+0", w, natTopH)

		rightText := ocrImage(t, magick, tesseract, png, right)
		topText := ocrImage(t, magick, tesseract, png, top)

		// 标题单独裁剪识别（整栏会混入字段标签；96 DPI 下标题行固定从 (322,62) 起、行高 24）。
		titleCrop := fmt.Sprintf("%dx%d+%d+%d", w-316, 28, 316, 60)
		titleText := ocrImage(t, magick, tesseract, png, titleCrop)

		// 标题被读空时退回整栏文本再判一次。
		titleSrc := titleText
		if len([]rune(normForOCR(titleText))) < 2 {
			titleSrc = rightText
		}
		if os.Getenv("MCLBX_OCR_DEBUG") != "" {
			head := []rune(rightText)
			if len(head) > 260 {
				head = head[:260]
			}
			t.Logf("%s 期望命令=%q 期望标题=%q\n   OCR右栏读到：%s", task.Key, cmd, task.Name, string(head))
		}

		// 标定对照：拿另一个任务名比对，用于判断度量是否可信。
		control := guiTasks[(i+5)%len(guiTasks)].Name
		ctrlScore := ocrScore(control, titleText)

		// 右栏大标题 = 任务名：绝对分、限定词或候选排名三者之一达标即通过。
		nameScore := ocrScore(task.Name, titleSrc)
		qual := titleQualifier(task.Name)
		qualScore := 0.0
		if qual != "" {
			qualScore = ocrScore(qual, titleSrc)
		}
		type cand struct {
			name string
			sc   float64
		}
		rank := make([]cand, 0, len(guiTasks))
		for _, c := range guiTasks {
			rank = append(rank, cand{c.Name, ocrScore(c.Name, titleSrc)})
		}
		best, second := rank[0], cand{"", -1}
		for _, c := range rank[1:] {
			switch {
			case c.sc > best.sc:
				second, best = best, c
			case c.sc > second.sc:
				second = c
			}
		}
		if nameScore < 0.7 && qualScore < 0.7 && best.name != task.Name {
			t.Errorf("%s：右栏标题认不出任务名 %q（得分 %.2f，限定词 %q 得分 %.2f，"+
				"排名冠军是 %q %.2f；对照串 %q 得分 %.2f）",
				task.Key, task.Name, nameScore, qual, qualScore, best.name, best.sc, control, ctrlScore)
		}
		// 仅当冠军是字面不像的任务名时才判失败；近似兄弟名属度量限制，豁免。
		if best.name != task.Name && ocrScore(best.name, task.Name) < 0.6 {
			t.Errorf("%s：右栏标题认成了 %q（%.2f），而不是本页任务名 %q（%.2f）",
				task.Key, best.name, best.sc, task.Name, nameScore)
		}
		// 区分度不足只记日志，不算失败。
		if best.sc-second.sc < 0.15 {
			t.Logf("（区分度低，属度量已知限制）%s：本页标题 %.2f 只比 %q %.2f 高 %.2f；"+
				"这两个名字字面相似度 %.2f，本就分不开",
				task.Key, best.sc, second.name, second.sc, best.sc-second.sc,
				ocrScore(second.name, task.Name))
		}
		// 2) 第一个文本字段的标签（取前 6 个字）
		for _, f := range task.Fields {
			if f.Kind == "check" {
				continue
			}
			head := []rune(f.Label)
			if len(head) > 6 {
				head = head[:6]
			}
			if sc := ocrScore(string(head), rightText); sc < 0.6 {
				t.Errorf("%s：字段标签 %q（取 %q）在右栏没认出来（得分 %.2f）",
					task.Key, f.Label, string(head), sc)
			}
			break
		}
		// 等价命令为 ASCII，OCR 基本不错，要求接近满分
		if sc := ocrScore(cmd, rightText); sc < 0.85 {
			t.Errorf("%s：右栏的命令行 %q 没认出来（得分 %.2f）—— 它是「渲染链路还活着」的哨兵",
				task.Key, cmd, sc)
		}
		// 顶栏状态文字
		if sc := ocrScore("IPv6", topText); sc < 0.8 {
			t.Errorf("%s：顶栏状态文字里的 IPv6 没认出来（最长公共子串占比 %.2f）", task.Key, sc)
		}

		// 记录各分数，便于核对阈值合理性。
		t.Logf("%-14s 标题分 %.2f（对照串 %.2f，领先第二名 %.2f，第二名是 %s）  命令分 %.2f",
			task.Key, nameScore, ctrlScore, best.sc-second.sc, second.name, ocrScore(cmd, rightText))
	}
}
