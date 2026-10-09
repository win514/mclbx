package main

// gui_cache_test.go 管界面服务的缓存口径。
// 页面无缓存头时浏览器会按启发式规则缓存旧页面，换新版 exe 后仍打开旧界面；
// 但不能一刀切 no-store：音频要 Range 拖动，背景图名字里带内容哈希。

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 界面本体、所有 JSON 接口，以及界面的样式与脚本，一律不许缓存。
func TestGuiCachePolicyCoversPageAndAPIs(t *testing.T) {
	for _, p := range []string{"/", "/api/state", "/api/tasks", "/api/settings", "/api/wall/import", "/api/start", "/api/quit",
		guiCSSPath, guiJSPath} {
		if !guiNoStorePath(p) {
			t.Errorf("%s 是随 exe 一起变的，必须不让缓存", p)
		}
	}
}

// 音频、背景图、其它静态资源各按自己的策略缓存，不能被这个中间件顺手抹掉。
// 注意 /assets/ 下只有界面样式与脚本走 no-store，favicon 不在其列。
func TestGuiCachePolicyLeavesMediaAlone(t *testing.T) {
	for _, p := range []string{"/music/歌.mp3", "/bg/bg-0123456789abcdef.jpg", "/assets/favicon.png", "/manual", "/apiary"} {
		if guiNoStorePath(p) {
			t.Errorf("%s 不该由缓存中间件决定缓存头 —— 它有自己的策略（Range 拖动 / 内容哈希）", p)
		}
	}
}

// 中间件只改该改的那一类路径，其余请求原样放行。
func TestGuiCachePolicyHeaderPerPath(t *testing.T) {
	cases := []struct {
		path     string
		wantNone bool
	}{
		{"/", true},
		{"/api/state", true},
		{guiCSSPath, true},
		{guiJSPath, true},
		{"/music/歌.mp3", false},
		{"/bg/bg-0123456789abcdef.jpg", false},
	}
	for _, c := range cases {
		var seen string
		h := guiCachePolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = w.Header().Get("Cache-Control")
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c.path, nil))
		if got := seen == "no-store"; got != c.wantNone {
			t.Errorf("%s 的 Cache-Control 是 %q，期望 no-store 存在=%v", c.path, seen, c.wantNone)
		}
	}
}

// 策略必须真的挂在服务上；少了这一行，上面的用例全都照样通过。
func TestGuiCachePolicyIsWired(t *testing.T) {
	b, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Handler: guiCachePolicy(mux)") {
		t.Error("界面服务没有挂上 guiCachePolicy —— 页面与 /api/ 又会变成可缓存的，" +
			"换上新版 exe 后浏览器仍可能拿旧界面")
	}
}

// 音频不设 no-store（否则每拖一次进度整段重下），也不要长缓存。
func TestMusicResponseStaysRevalidated(t *testing.T) {
	dir := withTempMusic(t)
	writeTestMP3(t, filepath.Join(dir, "歌.mp3"), 4096, true)

	w := httptest.NewRecorder()
	serveMusic(w, httptest.NewRequest(http.MethodGet, "/music/%E6%AD%8C.mp3", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("应当取得到音频，实际 %d", w.Code)
	}
	cc := w.Header().Get("Cache-Control")
	if cc == "" {
		t.Error("音频没有缓存头：浏览器会按启发式规则猜一个新鲜期，换掉文件后界面听不到新的")
	}
	if strings.Contains(cc, "no-store") {
		t.Errorf("音频不该是 %q —— no-store 会让拖动进度变成整段重下", cc)
	}
}
