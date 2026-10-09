package main

// music.go —— 背景音乐：曲库是存档里的 music 目录，播放交给界面里的 <audio>（Chromium 原生解
// MP3/WAV/FLAC/OGG/M4A，靠 Range 请求拖动进度）；本文件只做：找目录、列曲库、提供文件，播放状态归界面。

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 曲库单文件上限；界面要把整个文件读进内存才能播，故挡掉过大的文件。
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

// mp3FrameAt 判断从这个位置开始是不是一个说得通的 MPEG 音频帧头。
// 只看同步位太容易误判，所以把版本/层/码率/采样率里的保留值一并排掉。
func mp3FrameAt(b []byte, i int) bool {
	if i+1 >= len(b) || b[i] != 0xFF || b[i+1]&0xE0 != 0xE0 {
		return false
	}
	if i+2 >= len(b) {
		return false
	}
	if (b[i+1]>>3)&0x03 == 0x01 { // 版本保留值
		return false
	}
	if (b[i+1]>>1)&0x03 == 0x00 { // 层保留值
		return false
	}
	if br := (b[i+2] >> 4) & 0x0F; br == 0x00 || br == 0x0F { // 码率保留值
		return false
	}
	if (b[i+2]>>2)&0x03 == 0x03 { // 采样率保留值
		return false
	}
	return true
}

// sniffMusicExt 第二层：看文件头，挡掉改了扩展名的非音频文件（能否解码交给界面 <audio> 判）。
// MP3 不能只看开头：有些下载工具会在文件前塞垃圾，故认不出已知文件头时往前扫帧同步；
// 要求扫到两个说得通的帧头，纯文本之类才不会被误认成音频。
func sniffMusicExt(head []byte) string {
	switch {
	case len(head) >= 3 && string(head[:3]) == "ID3":
		return ".mp3" // 带 ID3 标签
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
	frames := 0
	for i := 0; i+2 < len(head); i++ {
		if mp3FrameAt(head, i) {
			frames++
			if frames >= 2 {
				return ".mp3"
			}
		}
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

// musicHeadBytes 嗅探时最多读这么多字节：要往前扫帧同步，8KB 足以覆盖「文件前有垃圾」的情形。
const musicHeadBytes = 8 << 10

func readMusicHead(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, musicHeadBytes)
	n, _ := io.ReadFull(f, b)
	return b[:n]
}

// cleanMusicName 只接受一个纯粹的文件名。
// 名字直接来自 URL，故只要出现路径分隔符就整个拒绝（而非取最后一段）。
func cleanMusicName(raw string) string {
	n := strings.TrimSpace(raw)
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
		return ""
	}
	return n
}

// serveMusic 提供曲库里的音频文件。
// 白名单＝无路径分隔符 + 扩展名在册 + 文件头对得上 + 普通文件；http.ServeFile 自带 Range，供进度拖动。
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
