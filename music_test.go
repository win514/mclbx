package main

// 音乐这一块：曲库只算真正的音频，提供文件那条路不接受任何路径。
// 播放本身交给界面的 <audio>，所以这里没有播放状态机可测 —— 那是界面那一侧的事。

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempMusic 把曲库目录指到临时目录。
func withTempMusic(t *testing.T) string {
	t.Helper()
	old := musicDir
	dir := t.TempDir()
	musicDir = func() string { return dir }
	t.Cleanup(func() { musicDir = old })
	return dir
}

// 一个合法的 MP3 头就够了：曲库只嗅文件头，解码交给界面。
func writeTestMP3(t *testing.T, p string, size int, id3 bool) {
	t.Helper()
	b := make([]byte, size)
	if id3 {
		copy(b, []byte("ID3"))
	} else {
		// 没有 ID3 标签时看 MPEG 帧同步：前 11 位为 1
		b[0], b[1] = 0xFF, 0xFB
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// 只算真正的音频：改了后缀的其它文件不算（PCL2 为这类问题修过两次 bug）。
func TestMusicListCountsOnlyRealAudio(t *testing.T) {
	dir := withTempMusic(t)

	writeTestMP3(t, filepath.Join(dir, "有标签.mp3"), 512, true)
	writeTestMP3(t, filepath.Join(dir, "无标签.mp3"), 512, false)
	// 假货四连：改了后缀的非音频、没有扩展名的、空文件、同名目录
	if err := os.WriteFile(filepath.Join(dir, "假的.mp3"), []byte("这不是音频的内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "没有扩展名"), []byte("ID3xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "空的.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "目录.mp3"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := listMusicTracks()
	if len(got) != 2 {
		t.Fatalf("曲库应当只有 2 首真的音频，实际 %d 首：%+v", len(got), got)
	}
	if got[0].Name != "无标签.mp3" || got[1].Name != "有标签.mp3" {
		t.Errorf("曲库应当按文件名排序，实际先后是 %q 与 %q", got[0].Name, got[1].Name)
	}
	if got[0].Size == 0 {
		t.Error("每首都要报出文件大小 —— 界面上要显示它")
	}
}

// /music/ 只提供曲库里的音频，且名字里不许出现路径分隔符。
func TestServeMusicOnlyServesPlainAudioNames(t *testing.T) {
	dir := withTempMusic(t)
	writeTestMP3(t, filepath.Join(dir, "歌.mp3"), 64, true)
	if err := os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "子目录"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want int
	}{
		{"/music/歌.mp3", http.StatusOK},
		{"/music/说明.txt", http.StatusNotFound}, // 目录里的东西，但不是音频
		{"/music/", http.StatusNotFound},
		{"/music/../config.json", http.StatusNotFound},
		{"/music/..%2Fconfig.json", http.StatusNotFound},
		{"/music/子目录/歌.mp3", http.StatusNotFound}, // 带分隔符一律拒绝，不是悄悄取最后一段
		{"/music/不存在的.mp3", http.StatusNotFound},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		serveMusic(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		if w.Code != c.want {
			t.Errorf("%s 应当返回 %d，实际 %d", c.path, c.want, w.Code)
		}
	}
}

// 偏好值必须被夹到合法范围：非法值退回默认，而不是原样存进去、
// 然后在界面上表现成「选了没反应」。
func TestMusicPrefsAreNormalized(t *testing.T) {
	withTempConfig(t)
	cases := []struct{ mode, loop, vol, wantMode, wantLoop, wantVol string }{
		{"shuffle", "one", "35", "shuffle", "one", "35"},
		{"", "", "", "order", "all", "70"},
		{"乱填", "乱填", "abc", "order", "all", "70"},
		{"order", "off", "101", "order", "off", "70"},
		{"order", "all", "-5", "order", "all", "70"},
		{"order", "all", "007", "order", "all", "7"},
	}
	for _, c := range cases {
		u := normalizeUI(guiUIState{MusicMode: c.mode, MusicLoop: c.loop, MusicVol: c.vol})
		if u.MusicMode != c.wantMode || u.MusicLoop != c.wantLoop || u.MusicVol != c.wantVol {
			t.Errorf("输入 %q/%q/%q 应当被收拾成 %q/%q/%q，实际得到 %q/%q/%q",
				c.mode, c.loop, c.vol, c.wantMode, c.wantLoop, c.wantVol,
				u.MusicMode, u.MusicLoop, u.MusicVol)
		}
	}
}

// 音乐要在界面上真的有入口：底栏那个按钮、<audio> 元素、设置里的音乐组，缺一个用户就找不到。
func TestMusicControlsExistInThePage(t *testing.T) {
	for _, want := range []string{
		`id="bgm"`, `id="bgmToggle"`, `id="bgmName"`,
		"uiMusicList(", `'/music/'`, "bgmToggle", `data-act="musicRefresh"`,
	} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("页面上缺少 %s —— 这个能力在界面上没有入口", want)
		}
	}
	if !strings.Contains(guiPageHTML, `id="bgm" preload="none"`) {
		t.Error("<audio> 应当设 preload=none —— 不点播放就不该去碰磁盘")
	}
	if !strings.Contains(cssRegion(t), "#bgmName{") {
		t.Error("曲名那一行没有样式约束 —— 长文件名会把底栏撑开")
	}
}
