package main

// datadir.go 本程序的两个目录只在此处算一次：存档 dataDir()（用户可见、可整个拷走：配置、日志、图片、音乐）
// 与内部 internalDir()（机器产物：WebView2 用户目录、解出的 DLL，不进存档）。
// 「可写」用探测文件判断而非 Stat：Windows 的 mode 位反映不了 ACL、只读属性与 UAC 虚拟化。

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
// 键里必须带上 LOCALAPPDATA：它是决定结果的环境量，缺了它测试之间会互相沿用旧目录、隔离失效。
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

// migrateLegacyArchive 把旧位置的 config.json 复制进新存档。
// 只在「新位置没有、旧位置有」时复制一次，且只复制不移动；日志与图片缓存不迁移。
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

// exeSiblingArchive 给出 exe 同级的存档目录；exe 位于临时目录时不采用（数据会被系统清理带走）。
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

// isUnderTemp 判断目录是否落在临时目录下；比较前统一小写并清理（Windows 路径大小写不敏感）。
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

// dataSub 返回存档下的一个子目录并确保它存在；目录被删掉后能自己长回来，长不出来则返回原因。
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
