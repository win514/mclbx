package main

// render_determinism_test.go 渲染确定性度量：默认跳过，设 MCLBX_DETERMINISM=<轮数> 运行；
// 同时统计 GDI 对象增长与 GDI+ 画布创建失败次数。

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// 读当前进程的 GDI 对象数（每进程默认配额一万个）。
var (
	pK32Diag   = syscall.NewLazyDLL("kernel32.dll")
	pCurProc   = pK32Diag.NewProc("GetCurrentProcess")
	pU32Diag   = syscall.NewLazyDLL("user32.dll")
	pGetGuiRes = pU32Diag.NewProc("GetGuiResources")
)

func gdiObjectCount() uintptr {
	h, _, _ := pCurProc.Call()
	n, _, _ := pGetGuiRes.Call(h, 0) // 0 = GR_GDIOBJECTS
	return n
}

func TestRenderDeterminismRate(t *testing.T) {
	n := 0
	if s := strings.TrimSpace(os.Getenv("MCLBX_DETERMINISM")); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 {
			t.Fatalf("MCLBX_DETERMINISM=%q 不是正整数", s)
		}
		n = v
	}
	if n == 0 {
		t.Skip("设 MCLBX_DETERMINISM=<轮数> 才跑：这条用例是用来量出现率的")
	}

	scenes := visScenes()
	startFails := gpBeginFails
	startGDI := gdiObjectCount()
	mismatch, worst, worstScene := 0, 0, ""
	for i := 0; i < n; i++ {
		for j, s := range scenes {
			first := renderScene(t, s)
			_ = renderScene(t, scenes[(j+1)%len(scenes)]) // 中间翻一页别的，和视觉基准的交替顺序一致
			again := renderScene(t, s)
			if d, _ := diffPixels(first, again); d != 0 {
				mismatch++
				if d > worst {
					worst, worstScene = d, s.Name
				}
			}
		}
		if i%25 == 0 || i == n-1 {
			t.Logf("  第 %3d 轮：GDI 对象 = %d（起始 %d，涨了 %d）", i, gdiObjectCount(), startGDI, int(gdiObjectCount())-int(startGDI))
		}
	}
	t.Logf("渲了 %d 轮 × %d 场景：不一致 %d 次；最坏 %d 像素（%s）",
		n, len(scenes), mismatch, worst, worstScene)
	t.Logf("GDI 对象：起始 %d → 结束 %d（涨 %d）；GDI+ 画布创建失败 %d 次（最近状态码 %d）",
		startGDI, gdiObjectCount(), int(gdiObjectCount())-int(startGDI),
		gpBeginFails-startFails, gpBeginFailCode)
	if mismatch > 0 {
		t.Errorf("渲染不确定：%d 次不一致（这条只是度量出现率，缺陷本身另算；对照上面的画布失败次数）", mismatch)
	}
}
