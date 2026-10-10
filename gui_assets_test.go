package main

// gui_assets_test.go 盯住"样式与脚本是独立文件、由程序自己托管"这条接线。
// 易断的是接线而非样式：路径与处理器对不上、缓存策略漏路径、样式被内联回页面。

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

// 骨架必须引用这两份资源，而且不许再把样式内联在页面里。
func TestGuiShellReferencesExternalAssets(t *testing.T) {
	page := renderGuiPage()
	for _, want := range []string{
		`<link rel="stylesheet" href="` + guiCSSPath + `">`,
		`<script src="` + guiJSPath + `"></script>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("页面骨架里没有 %s —— 那份资源不会被加载", want)
		}
	}
	if strings.Contains(guiShellHTML, "<style>") {
		t.Error("骨架里又出现了 <style> —— 样式只应当放在 assets/gui.css 里")
	}
	// 首屏小脚本必须仍内联：它要在样式生效前把"跟随系统"解析成具体主题，改外链会先闪默认主题。
	// 骨架里应当恰好只有这一个内联 <script>。
	if n := strings.Count(guiShellHTML, "<script>"); n != 1 {
		t.Errorf("骨架里应当只有首屏那一个内联 <script>，实际 %d 个", n)
	}
}

// 两份资源真的嵌进来了，而且不是空文件。
func TestGuiAssetsAreEmbedded(t *testing.T) {
	for _, name := range []string{"gui.css", "gui.js"} {
		if s := guiAssetString(name); len(s) < 1000 {
			t.Errorf("assets/%s 只有 %d 字节，八成没打进来", name, len(s))
		}
	}
}

// 摊平出来的源码必须与两份文件对得上：否则一大批"在源码里找规则"的用例会测一个不存在的东西。
func TestGuiPageFlatMatchesAssets(t *testing.T) {
	if !strings.Contains(guiPageHTML, "<style>\n"+guiAssetString("gui.css")+"</style>") {
		t.Error("摊平后的源码里没有 assets/gui.css 的内容")
	}
	if !strings.Contains(guiPageHTML, "<script>\n"+guiAssetString("gui.js")+"</script>") {
		t.Error("摊平后的源码里没有 assets/gui.js 的内容")
	}
	if strings.Contains(guiPageHTML, guiCSSPath) || strings.Contains(guiPageHTML, guiJSPath) {
		t.Error("摊平后的源码里还留着资源引用 —— 摊平没做干净")
	}
}

// 服务端把这两份资源发出去了：状态码、正文、缓存头与 Content-Type。
// Content-Type 不对时浏览器拒绝执行脚本或忽略样式，界面整块失效且程序侧看不出异常。
func TestGuiAssetsAreServedWithUsableHeaders(t *testing.T) {
	h, ok := guiAssetHandler()
	if !ok {
		t.Fatal("内嵌的 assets/ 取不到，/assets/ 这条路由不会被挂上")
	}
	h = guiCachePolicy(h)
	cases := []struct{ url, file, wantType string }{
		{guiCSSPath, "gui.css", "text/css"},
		{guiJSPath, "gui.js", "text/javascript"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.url, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s 应当取得到，实际 %d", c.url, w.Code)
		}
		if got, want := w.Body.String(), guiAssetString(c.file); got != want {
			t.Errorf("%s 发出去的正文与内嵌文件不一致（%d 字节 vs %d 字节）", c.url, len(got), len(want))
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s 的 Cache-Control 是 %q，期望 no-store —— 否则换新版 exe 后会拿到旧的那一份", c.url, got)
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, c.wantType) {
			t.Errorf("%s 的 Content-Type 是 %q，期望 %s 开头。"+
				"类型不对时浏览器会拒绝执行脚本 / 忽略样式，界面整块失效且这一侧看不出异常",
				c.url, got, c.wantType)
		}
	}
	// 确认内嵌目录里就是这两个文件名，避免路径写岔却有别的文件顶上
	if _, err := guiAssetFS.ReadFile(path.Join("assets", "gui.css")); err != nil {
		t.Errorf("assets/gui.css 不在内嵌目录里：%v", err)
	}
}
