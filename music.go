package main

// music.go —— 背景音乐：曲库就是存档里的 music 目录，播放交给界面里的 <audio>。
//
// 为什么不把播放做在 Go 这一侧（系统 MCI）：
//   · 要 syscall、要处理命令里的引号与超长路径（短路径兜底在关了 8.3 名的卷上还会失败）；
//   · 它是进程级单设备，切歌必须 close 再 open，连点下一首就会让两者交错；
//   · 音量在 WAV 那一档设备上不可用（实测报「驱动程序无法识别指定的命令」）；
//   · 还得把 Windows 专用代码从 Linux 构建里隔离出去。
//
// 界面本身就是 Chromium：<audio> 原生解 MP3/WAV/FLAC/OGG/M4A，音量对所有格式都有效，
// 拖动进度靠 Range 请求，播完有 ended 事件 —— 上面那堆麻烦一次性全没了。
// 代价只有两条：关掉窗口就停（通常正合预期），以及原生界面听不到（音乐本来就是网页界面优先）。
//
// 于是这个文件只做三件事：找到 music 目录、把曲库列出来、把文件提供出去。
// 没有播放状态机、没有轮询、没有定时器 —— 播放状态归界面那一侧。

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 曲库目录与单文件上限。上限挡的是「误把一个几百 MB 的音频塞进来」这种情形：
// 界面要把它整个读进内存才能播。
const musicMaxBytes = 512 << 20

var musicDir = func() string {
	d, err := dataSub("music")
	if err != nil {
		return ""
	}
	return d
}

// isMusicExt 第一层：扩展名白名单。
func isMusicExt(ext string) bool {
	switch ext {
	case ".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wma":
		return true
	}
	return false
}

// sniffMusicExt 第二层：只看文件头。
//
// 这一层挡的是「改了扩展名的非音频文件」。PCL2 的更新日志里有过两条与音乐有关的修复
// ——「背景音乐数量显示有误」与「背景音乐数量错误地计入了非音乐文件」—— 说的就是这类问题。
// 第三层（这个文件到底能不能解码）交给界面：<audio> 报 error 时我们就标记它放不了，
// 而不是在这里替浏览器猜。
func sniffMusicExt(head []byte) string {
	switch {
	case len(head) >= 3 && string(head[:3]) == "ID3":
		return ".mp3" // 带 ID3 标签
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return ".mp3" // MPEG 帧同步：没有 ID3 标签的 MP3
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return ".wav"
	case len(head) >= 4 && string(head[:4]) == "fLaC":
		return ".flac"
	case len(head) >= 4 && string(head[:4]) == "OggS":
		return ".ogg"
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		return ".m4a"
	case len(head) >= 4 && head[0] == 0x30 && head[1] == 0x26 && head[2] == 0xB2 && head[3] == 0x75:
		return ".wma"
	}
	return ""
}

// musicTrack 是曲库里的一首。界面显示的就是文件名 —— 曲库的真相在磁盘上。
type musicTrack struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// listMusicTracks 列出真正的音频文件，按文件名排序。
func listMusicTracks() []musicTrack {
	dir := musicDir()
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]musicTrack, 0, 16)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if n == "" || strings.HasPrefix(n, ".") || !isMusicExt(strings.ToLower(filepath.Ext(n))) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 || info.Size() > musicMaxBytes {
			continue
		}
		if sniffMusicExt(readMusicHead(filepath.Join(dir, n))) == "" {
			continue // 改了后缀的非音频，不算进曲库
		}
		out = append(out, musicTrack{Name: n, Size: info.Size()})
		if len(out) >= 500 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func readMusicHead(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, 12)
	n, _ := f.Read(b)
	return b[:n]
}

// cleanMusicName 只接受一个纯粹的文件名。
//
// 这里比背景图那边严：背景图的名字是用户手填/上传带过来的，取最后一段是合理的清理；
// 而这个名字直接来自 URL，所以**只要出现路径分隔符就整个拒绝**，不是悄悄取最后一段 ——
// 悄悄取的话，「子目录/歌.mp3」也能播到歌.mp3，行为会变得很难解释。
func cleanMusicName(raw string) string {
	n := strings.TrimSpace(raw)
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
		return ""
	}
	return n
}

// serveMusic 提供曲库里的音频文件。
//
// 白名单是「名字里没有路径分隔符 + 扩展名在名单里 + 文件头也对得上 + 确实是个普通文件」：
// 这样即使有人在曲库目录里放一个指到别处的链接，也拿不到它指向的文件；
// 顺手也让 /music/ 只暴露真正的音频，而不是目录里的任何东西。
// http.ServeFile 自带 Range 支持，界面拖动进度靠的就是它。
func serveMusic(w http.ResponseWriter, r *http.Request) {
	name := cleanMusicName(strings.TrimPrefix(r.URL.Path, "/music/"))
	dir := musicDir()
	if name == "" || dir == "" {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(dir, name)
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	if !isMusicExt(strings.ToLower(filepath.Ext(name))) || sniffMusicExt(readMusicHead(p)) == "" {
		http.NotFound(w, r)
		return
	}
	// 曲库是用户自己的文件：改了就应当立刻听到新的，不给长缓存
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, p)
}
