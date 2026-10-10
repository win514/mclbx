package main

// wallpaper.go 背景图片：把用户选的本地图片缩到长边 2560 后缓存到存档，网页版经 /bg/<内容哈希>.jpg 取用。
// 指纹为「路径+修改时间+大小」（非内容），变化即换 URL，故可给 immutable 长缓存；URL 只含指纹不含路径，无目录穿越面。
// 仅用标准库解码 JPEG/PNG/GIF；EXIF 方向标签标准库不认，下面自己按 1~8 纠偏。

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	// wallLongEdge 缩放后长边的上限；解码内存从 33 MB 降到约 15 MB。
	wallLongEdge = 2560
	// wallSrcMaxPixels 源图像素数上限，防止一张畸形文件把内存吃光。
	// 类型是 int64：386 上 int 只有 32 位，两个大边长相乘会溢出成负数，守卫就形同虚设。
	wallSrcMaxPixels int64 = 80 << 20
	// wallSrcMaxBytes 源文件字节数上限。
	wallSrcMaxBytes = 64 << 20
	// wallJPEGQuality 缓存图的编码质量：装饰性背景，过高的质量只换来更大的传输量。
	wallJPEGQuality = 82
)

// wallResult 背景图的准备结果：URL 与 Reason 最多有一个非空。
type wallResult struct {
	URL    string // 例如 /bg/bg-3f2a....jpg；空表示没有可用的背景图
	Reason string // 失败原因，只在设置面板里解释一句，不影响其他功能
}

func (r wallResult) on() bool { return r.URL != "" }

// cached 判断这份结果指向的缓存文件是否还在。
// 缓存目录只保留最新一份，故记忆结果必须每次核对，否则会返回已失效的 URL。
func (r wallResult) cached() bool {
	if r.URL == "" {
		return true // 失败结果没有文件可核对
	}
	_, err := os.Stat(filepath.Join(wallCacheDir(), strings.TrimPrefix(r.URL, "/bg/")))
	return err == nil
}

var (
	wallMu sync.Mutex
	// wallMemo 源指纹 → 结果。成功与失败都记：一个损坏的文件不该每次打开界面都重新解码一遍。
	wallMemo = map[string]wallResult{}

	// 缩放后的图放在存档目录下的 ui-cache。做成变量，便于测试指向临时目录。
	wallCacheDir = func() string {
		d := dataDir()
		if d == "" {
			return "" // 同上：宁可缓存写不进去，也不要在当前工作目录里凭空造一个 ui-cache
		}
		return filepath.Join(d, "ui-cache")
	}

	// wallGalleryDir 是存档里放导入图片的目录（界面只读它、往里放，用户也可自行增删）。
	wallGalleryDir = func() string {
		d, err := dataSub("wallpapers")
		if err != nil {
			return ""
		}
		return d
	}
)

// errNoWall 表示用户没有配置背景图，它不是错误。
var errNoWall = errors.New("没有配置背景图")

// wallFingerprint 用路径 + 修改时间 + 大小标识一份源文件。
func wallFingerprint(p string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", errors.New("这是一个目录，不是图片文件")
	}
	if st.Size() == 0 {
		return "", errors.New("文件是空的")
	}
	if st.Size() > wallSrcMaxBytes {
		return "", fmt.Errorf("文件为 %.0f MB，超过上限", float64(st.Size())/(1<<20))
	}
	return fmt.Sprintf("%s\x00%d\x00%d", p, st.ModTime().UnixNano(), st.Size()), nil
}

// wallErrText 把系统错误译成一句能自己排查的话。
func wallErrText(err error) string {
	switch {
	case errors.Is(err, errNoWall):
		return ""
	case errors.Is(err, errWallMissing):
		return "该图片不在存档目录中：存档目录可能被清理过，或图片已被删除"
	case os.IsNotExist(err):
		return "找不到这个文件"
	case os.IsPermission(err):
		return "没有读取权限"
	}
	return err.Error()
}

// ---- 图库：配置里存的是「导入后的文件名」，不是路径 ----
// 图片被复制进存档，原文件之后改名/移走/删掉都不影响，存档拷走也能用。

// errWallMissing 表示配置里写着某个导入的图，但存档里已经没有这个文件了。
var errWallMissing = errors.New("wallMissing")

// wallImportMaxBytes 单张导入图片上限，同时用作请求体上限（挡在解析之前）。
const wallImportMaxBytes = 32 << 20

// cleanWallName 收拾背景图名：去空白与引号，并只取文件名部分。
// 名字会被拼进存档目录，去掉目录部分才不给出「按名字取任意文件」的能力。
func cleanWallName(name string) string {
	n := cleanWallPath(name)
	if n == "" {
		return ""
	}
	n = filepath.Base(filepath.FromSlash(n))
	if n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
		return ""
	}
	return n
}

// resolveWallImage 把配置里的名字解析成磁盘上的绝对路径。
func resolveWallImage(name string) (string, error) {
	n := cleanWallName(name)
	if n == "" {
		return "", errNoWall
	}
	dir := wallGalleryDir()
	if dir == "" {
		return "", errors.New("存档目录不可用")
	}
	p := filepath.Join(dir, n)
	if _, err := os.Stat(p); err != nil {
		return "", errWallMissing
	}
	return p, nil
}

// wallFromConfig 是「背景图」链路的唯一入口：配置值 → 缓存结果。
// 名字解析（配置能不能信）与 prepareWall 的路径处理（图能不能读）分层。
func wallFromConfig(name string) wallResult {
	p, err := resolveWallImage(name)
	if err != nil {
		if errors.Is(err, errNoWall) {
			return wallResult{}
		}
		return wallResult{Reason: wallErrText(err)}
	}
	return prepareWall(p)
}

// sniffWallExt 只看文件头定类型：只认扩展名的话，改了后缀的非图片会混进图库。
func sniffWallExt(data []byte) string {
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return ".jpg"
	}
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return ".png"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return ".gif"
	}
	return ""
}

func isWallImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	}
	return false
}

// importWallFile 把上传上来的图片存进图库，返回存下的文件名。
// 扩展名以文件头嗅探结果为准，不采用上传时的文件名（后者由客户端决定）。
func importWallFile(name string, data []byte) (string, error) {
	dir := wallGalleryDir()
	if dir == "" {
		return "", errors.New("存档目录不可用，图片没地方放")
	}
	ext := sniffWallExt(data)
	if ext == "" {
		return "", errors.New("这不是一张能识别的图片（支持 JPEG / PNG / GIF）")
	}
	base := strings.TrimSuffix(cleanWallName(name), filepath.Ext(cleanWallName(name)))
	if base == "" {
		base = "wallpaper"
	}
	n := base + ext
	if err := os.WriteFile(filepath.Join(dir, n), data, 0o644); err != nil {
		return "", fmt.Errorf("写进存档失败：%w", err)
	}
	return n, nil
}

// listWallImages 列出图库里真正的图片，排序后返回。
func listWallImages() []string {
	dir := wallGalleryDir()
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if n == "" || strings.HasPrefix(n, ".") || !isWallImageExt(strings.ToLower(filepath.Ext(n))) {
			continue
		}
		if sniffWallExt(readWallHead(filepath.Join(dir, n))) == "" {
			continue // 改了后缀的非图片，不算进图库
		}
		out = append(out, n)
		if len(out) >= 300 {
			break // 图库是给眼睛挑的，再多也没有意义
		}
	}
	sort.Strings(out)
	return out
}

func readWallHead(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, 8)
	n, _ := io.ReadFull(f, b)
	return b[:n]
}

// adoptLegacyWallPath 把旧配置里的「绝对路径」换成导入进来的副本（只复制、不动原文件）。
// 复制不成就保留旧值，不擅自丢弃用户配置。
func adoptLegacyWallPath(name string) string {
	if name == "" || cleanWallName(name) == name && !looksLikePath(name) {
		return name
	}
	src := cleanWallPath(name)
	st, err := os.Stat(src)
	if err != nil || st.IsDir() {
		return name
	}
	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 || len(data) > wallImportMaxBytes {
		return name
	}
	got, err := importWallFile(filepath.Base(src), data)
	if err != nil {
		return name
	}
	return got
}

// looksLikePath 判断这个值像不像一条路径（而不是一个文件名）。
func looksLikePath(v string) bool {
	return strings.ContainsAny(v, `/\`) || filepath.IsAbs(v)
}

// prepareWall 生成或命中背景图缓存，raw 为空表示不使用背景图。
// 常见路径必须廉价（一次 Stat、一次 map 查询、一次 Stat）；解码与缩放只在指纹变化时发生。
func prepareWall(raw string) wallResult {
	p := cleanWallPath(raw)
	if p == "" {
		return wallResult{}
	}
	key, err := wallFingerprint(p)
	if err != nil {
		return wallResult{Reason: wallErrText(err)}
	}
	wallMu.Lock()
	defer wallMu.Unlock()
	// 命中记忆也要核对文件还在不在：缓存目录只留最新一份，换过图之后旧的那份已经被清掉了。
	if r, ok := wallMemo[key]; ok && r.cached() {
		return r
	}
	r := buildWall(p, key)
	wallMemo[key] = r
	return r
}

// buildWall 真正做解码、缩放与落盘；调用方须持有 wallMu。
func buildWall(src, key string) wallResult {
	sum := sha256.Sum256([]byte(key))
	name := "bg-" + hex.EncodeToString(sum[:8]) + ".jpg"
	dst := filepath.Join(wallCacheDir(), name)
	// 同一份源文件上次已经生成过（例如重启后再打开界面）就直接用缓存。
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return wallResult{URL: "/bg/" + name}
	}
	img, err := decodeWall(src)
	if err != nil {
		return wallResult{Reason: err.Error()}
	}
	if err := os.MkdirAll(wallCacheDir(), 0o755); err != nil {
		return wallResult{Reason: "缓存目录不可用"}
	}
	if err := writeWallJPEG(dst, shrinkToLongEdge(toRGBA(img), wallLongEdge)); err != nil {
		return wallResult{Reason: "写缓存失败"}
	}
	pruneWallCache(name)
	return wallResult{URL: "/bg/" + name}
}

// decodeWall 解码源文件；先读头部把尺寸挡在外面，再整幅解码。
func decodeWall(src string) (image.Image, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, errors.New("打不开这个文件")
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, errors.New("认不出这个格式（只支持 JPEG / PNG / GIF）")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > wallSrcMaxPixels {
		return nil, errors.New("图片尺寸无法使用")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New("读不了这个文件")
	}
	img, format, err := image.Decode(f)
	if err != nil {
		return nil, errors.New("图片解不开，可能文件损坏了")
	}
	// JPEG 才有 EXIF 方向标签；其它格式按原样用。
	if format == "jpeg" {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			if o := exifOrientation(f); o >= 2 && o <= 8 {
				img = applyOrientation(toRGBA(img), o)
			}
		}
	}
	return img, nil
}

// exifOrientation 从 JPEG 头部读出方向标签（EXIF tag 0x0112）；读不到或取值超出 1~8 一律返回 1。
// 只沿标记段向前扫，遇到 SOS（图像数据开始）即停：APP1 只可能在那之前。
func exifOrientation(r io.Reader) int {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi[0] != 0xFF || soi[1] != 0xD8 {
		return 1
	}
	// 段数上限只是防御：正常照片的 EXIF 在前几个段里。
	for i := 0; i < 64; i++ {
		var mk [2]byte
		if _, err := io.ReadFull(r, mk[:]); err != nil || mk[0] != 0xFF {
			return 1
		}
		marker := mk[1]
		// 无长度字段的独立标记，跳过继续
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0xFF {
			continue
		}
		if marker == 0xD9 || marker == 0xDA { // 结束或图像数据开始：不会再有 EXIF
			return 1
		}
		var ln [2]byte
		if _, err := io.ReadFull(r, ln[:]); err != nil {
			return 1
		}
		n := int(ln[0])<<8 | int(ln[1]) // 段长含它自己这 2 字节
		if n < 2 {
			return 1
		}
		body := make([]byte, n-2)
		if _, err := io.ReadFull(r, body); err != nil {
			return 1
		}
		if marker == 0xE1 && len(body) >= 6 && string(body[:6]) == "Exif\x00\x00" {
			return orientationFromTIFF(body[6:])
		}
	}
	return 1
}

// orientationFromTIFF 在 TIFF 头（已跳过 "Exif\0\0"）里找 0x0112。
func orientationFromTIFF(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(t[2:4]) != 0x002A {
		return 1
	}
	off := int(order.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	cnt := int(order.Uint16(t[off : off+2]))
	for e := off + 2; cnt > 0; cnt-- {
		if e+12 > len(t) {
			return 1
		}
		ent := t[e : e+12]
		if order.Uint16(ent[0:2]) == 0x0112 {
			// 类型 3 = SHORT，值就落在最后 4 字节的前 2 字节里
			if order.Uint16(ent[2:4]) != 3 {
				return 1
			}
			if v := int(order.Uint16(ent[8:10])); v >= 2 && v <= 8 {
				return v
			}
			return 1
		}
		e += 12
	}
	return 1
}

// applyOrientation 按 EXIF 的 1~8 把图摆正（5~8 交换宽高）；映射取 EXIF 标准定义：
//
//	2 水平镜像   3 旋转 180   4 垂直镜像
//	5 沿主对角线镜像   6 顺时针 90   7 沿副对角线镜像   8 逆时针 90
func applyOrientation(src *image.RGBA, o int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if o < 2 || w == 0 || h == 0 {
		return src
	}
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			si := sy*src.Stride + sx*4
			di := y*dst.Stride + x*4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// toRGBA 先整幅转一次 RGBA：后面的面积平均可按字节读取，比逐点 At() 便宜得多。
func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// shrinkToLongEdge 按长边缩小；本来就够小就原样返回（不放大小图，放大只会更糊）。
func shrinkToLongEdge(src *image.RGBA, limit int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if w <= 0 || h <= 0 || (w <= limit && h <= limit) {
		return src
	}
	if w >= h {
		return boxDown(src, limit, max(1, h*limit/w))
	}
	return boxDown(src, max(1, w*limit/h), limit)
}

// boxDown 面积平均缩小；只服务背景图这一件事，标准库无缩放故手写一层即可。
func boxDown(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, (y+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sr, sg, sb, sa, n uint32
			for yy := y0; yy < y1; yy++ {
				row := src.Pix[yy*src.Stride:]
				for xx := x0; xx < x1; xx++ {
					i := xx * 4
					sr += uint32(row[i])
					sg += uint32(row[i+1])
					sb += uint32(row[i+2])
					sa += uint32(row[i+3])
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0] = uint8(sr / n)
			o[1] = uint8(sg / n)
			o[2] = uint8(sb / n)
			o[3] = uint8(sa / n)
		}
	}
	return dst
}

// writeWallJPEG 原子写（先写临时文件再改名），避免半个文件被当成缓存命中。
func writeWallJPEG(path string, img image.Image) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: wallJPEGQuality}); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// pruneWallCache 只留当前这一份：换过几次图之后缓存目录不该一直长大。
func pruneWallCache(keep string) {
	ents, err := os.ReadDir(wallCacheDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		if n == keep || !strings.HasPrefix(n, "bg-") {
			continue
		}
		_ = os.Remove(filepath.Join(wallCacheDir(), n))
	}
}

// wallName 允许出现的缓存文件名：只有 bg-<16 位十六进制>.jpg。
// 这个白名单是安全边界：名字来自 URL，必须窄到没有第二种解释。
var wallName = regexp.MustCompile(`^bg-[0-9a-f]{16}\.jpg$`)

// serveWall 按指纹提供背景图；名字含指纹，换图必换名，可给一年期长缓存。
func serveWall(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/bg/")
	if !wallName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, filepath.Join(wallCacheDir(), name))
}
