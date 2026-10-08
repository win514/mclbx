package main

// datadir.go —— 本程序的两个目录只在这里算一次。
//
// 分成两个是有意的：
//
//	存档 dataDir()      用户看得见、能整个拷走的东西：配置、日志、导入的图片、音乐。
//	                    优先放在 exe 同级，这样「存档」才名副其实 —— 拷走文件夹就带走全部设置。
//	内部 internalDir()  机器自己的东西：WebView2 的浏览器用户目录，以及从 exe 里解出来的 DLL。
//	                    这些不进存档：一个浏览器档子有几百个文件，塞进用户目录会让「存档」没法看，
//	                    而且它跟这台机器绑定，拷到别处未必能用。
//
// 这个划分还顺手消掉了一个风险：如果把浏览器用户目录也搬到 exe 同级，会遇到「配置文件写得进去、
// 但 WebView2 拒绝在那里建档子」这种半可用状态（Program Files、UAC 虚拟化、临时目录都会引出它），
// 排查起来很费时间。浏览器档子一律留在 LOCALAPPDATA，那条路就不会出现。
//
// 「可写」用探测文件判断，不用 Stat：Windows 上 mode 位既反映不了 ACL 与只读属性，
// 也反映不了 UAC 虚拟化 —— Stat 说目录在，不等于真的写得进去。探测文件写完立刻删掉，不留垃圾。

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 存档目录名。放在 exe 同级时用这个名字，一眼能看出是干什么的。
const archiveDirName = "mclbx-data"

// 两个接缝，测试用它们把目录指到临时位置（与 guiConfigFile / wallCacheDir 同一套做法）。
var (
	archiveDirOverride  string
	internalDirOverride string
)

// exePath 与 tempRoots 也是接缝：
//   - os.Executable 的结果由运行方式决定，测试没法把它改成「放在非临时目录里的 exe」；
//   - t.TempDir 本身就在临时目录下，不给 tempRoots 留口子就构造不出「exe 在普通目录」的情形。
var (
	exePath   = os.Executable
	tempRoots = func() []string { return []string{os.Getenv("TEMP"), os.Getenv("TMP"), os.TempDir()} }
)

// dirCache 记一次解析结果。
//
// 键里必须带上 LOCALAPPDATA：它是决定结果的环境量，而 diag 那几条用例正是靠
// t.Setenv("LOCALAPPDATA", 临时目录) 来隔离的。少了这个键，第一条用例解析出的目录会被
// 后面的用例一直沿用，隔离就悄悄失效了 —— 表现为「文件明明删了，尾读还能读到内容」。
type dirCache struct {
	mu   sync.Mutex
	key  string
	path string
}

var (
	archiveCache  dirCache
	internalCache dirCache
)

// dataDir 返回存档目录；全都不可用时返回空串，调用方必须容忍空串。
func dataDir() string {
	if archiveDirOverride != "" {
		return archiveDirOverride
	}
	return archiveCache.get(resolveArchiveDir)
}

// internalDir 返回只放机器内部产物的目录；取不到时返回空串。
func internalDir() string {
	if internalDirOverride != "" {
		return internalDirOverride
	}
	return internalCache.get(resolveInternalDir)
}

func (c *dirCache) get(resolve func() string) string {
	key := os.Getenv("LOCALAPPDATA")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path != "" && c.key == key {
		return c.path
	}
	c.key, c.path = key, resolve()
	return c.path
}

func resolveArchiveDir() string {
	if d, ok := exeSiblingArchive(); ok {
		migrateLegacyArchive(d)
		return d
	}
	return anyWritableDataDir()
}

// migrateLegacyArchive 把旧位置里的配置复制进新存档。
//
// 存档从 %LOCALAPPDATA%\mclbx 改到 exe 同级之后，少了这一步，用户看到的是「设置全丢了」——
// 配置其实还在旧位置，只是没人去读它。
//
// 只在「新位置还没有配置文件、旧位置有」时复制一次，**只复制不移动**：
// 万一新位置以后用不了，旧位置仍然完整。日志与图片缓存不迁移 —— 它们是可再生的，
// 而设置是用户手调出来的。
func migrateLegacyArchive(archive string) {
	legacy := legacyDataDir()
	if legacy == "" || legacy == archive {
		return
	}
	dst := filepath.Join(archive, "config.json")
	if _, err := os.Stat(dst); err == nil {
		return // 新位置已经有了，不动
	}
	b, err := os.ReadFile(filepath.Join(legacy, "config.json"))
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, b, 0o644)
}

// legacyDataDir 是改动之前那个数据目录位置。
func legacyDataDir() string {
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), userCacheBase()} {
		if base == "" {
			continue
		}
		return filepath.Join(base, "mclbx")
	}
	return ""
}

func resolveInternalDir() string {
	return anyWritableDataDir()
}

// anyWritableDataDir 按「本地应用数据 → 用户缓存目录」的顺序找出一个写得进去的目录。
func anyWritableDataDir() string {
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), userCacheBase()} {
		if base == "" {
			continue
		}
		d := filepath.Join(base, "mclbx")
		if ensureWritableDir(d) {
			return d
		}
	}
	return ""
}

// exeSiblingArchive 给出 exe 同级的存档目录。
//
// 从临时目录运行时不采用：那种情况下系统清理临时目录会把用户的数据一起带走，
// 而「存档」的意义恰好是留得住。顺带一个好处 —— Go 跑测试时二进制就在临时目录里，
// 于是测试天然走不到这一支，靠 LOCALAPPDATA 隔离的那些用例不受影响。
func exeSiblingArchive() (string, bool) {
	exe, err := exePath()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(exe)
	if dir == "" || isUnderTemp(dir) {
		return "", false
	}
	d := filepath.Join(dir, archiveDirName)
	return d, ensureWritableDir(d)
}

// isUnderTemp 判断目录是否落在临时目录下。
// 比较前统一小写并清理，因为 Windows 的路径大小写不敏感。
func isUnderTemp(dir string) bool {
	low := strings.ToLower(filepath.Clean(dir))
	for _, t := range tempRoots() {
		if t == "" {
			continue
		}
		t = strings.ToLower(filepath.Clean(t))
		if t == "" || t == "." {
			continue
		}
		if low == t || strings.HasPrefix(low, t+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// ensureWritableDir 建目录，并真的写一个探测文件来确认写得进去，写完立刻删除。
func ensureWritableDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func userCacheBase() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return d
}

// dataSub 返回存档下的一个子目录，并确保它存在。
// 每次写入前都调一次，这样目录被用户在资源管理器里删掉之后能自己长回来；
// 真长不出来就把原因返回给调用方，不做静默降级。
func dataSub(name string) (string, error) {
	root := dataDir()
	if root == "" {
		return "", os.ErrNotExist
	}
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}
