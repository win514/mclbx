package main

// 数据目录是「存档」的根：配置、日志、图片、音乐都放在其下；下列用例固定选址规则。

import (
	"os"
	"path/filepath"
	"testing"
)

func clearDirCaches() {
	archiveCache = dirCache{}
	internalCache = dirCache{}
}

// 存档优先放在 exe 同级：整个文件夹拷走即带走全部设置。
func TestDataDirPrefersExeSibling(t *testing.T) {
	clearDirCaches()
	exeDir := t.TempDir()
	oldExe, oldRoots := exePath, tempRoots
	exePath = func() (string, error) { return filepath.Join(exeDir, "mclbx.exe"), nil }
	// 把「临时目录」指到别处，使 exeDir 被当成普通目录（t.TempDir 本身在临时目录下）
	tempRoots = func() []string { return []string{filepath.Join(exeDir, "nope")} }
	defer func() { exePath, tempRoots = oldExe, oldRoots; clearDirCaches() }()

	want := filepath.Join(exeDir, archiveDirName)
	got := dataDir()
	if got != want {
		t.Fatalf("存档目录应当是 exe 同级的 %s，实际是 %s", want, got)
	}
	if !dirExists(got) {
		t.Fatalf("存档目录没有被建出来：%s", got)
	}

	// 用户删除后，下一次访问须自动重建
	if err := os.RemoveAll(got); err != nil {
		t.Fatal(err)
	}
	if dirExists(got) {
		t.Fatal("目录没删掉")
	}
	clearDirCaches() // 模拟下一次访问（进程内缓存随之重算）
	again := dataDir()
	if again != want {
		t.Errorf("删掉之后应当仍然选中同一位置 %s，实际 %s", want, again)
	}
	if !dirExists(again) {
		t.Errorf("存档目录被删后没有自动重建：%s", again)
	}
}

// exe 位于临时目录时不采用 exe 同级：系统清理临时目录会连同用户数据一起删除。
func TestDataDirSkipsTempExeDir(t *testing.T) {
	clearDirCaches()
	exeDir := t.TempDir() // 本身就在临时目录下
	local := t.TempDir()
	oldExe := exePath
	exePath = func() (string, error) { return filepath.Join(exeDir, "mclbx.exe"), nil }
	t.Setenv("LOCALAPPDATA", local)
	defer func() { exePath = oldExe; clearDirCaches() }()

	if got, want := dataDir(), filepath.Join(local, "mclbx"); got != want {
		t.Errorf("exe 在临时目录里时应当回退到 %s，实际 %s", want, got)
	}
	if dirExists(filepath.Join(exeDir, archiveDirName)) {
		t.Error("不该在临时目录里建存档 —— 那些数据会被系统清理掉")
	}
}

// 浏览器用户目录与解出来的 DLL 不进存档（文件多且与本机绑定）。
func TestInternalDirStaysOutOfArchive(t *testing.T) {
	clearDirCaches()
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	defer clearDirCaches()

	if got, want := internalDir(), filepath.Join(local, "mclbx"); got != want {
		t.Errorf("内部目录应当落在 LOCALAPPDATA 下（%s），实际 %s", want, got)
	}
}

// 「可写」须确实能写入，而非仅 Stat 判断目录存在。
func TestDataDirIsActuallyWritable(t *testing.T) {
	clearDirCaches()
	exeDir := t.TempDir()
	oldExe, oldRoots := exePath, tempRoots
	exePath = func() (string, error) { return filepath.Join(exeDir, "mclbx.exe"), nil }
	tempRoots = func() []string { return []string{filepath.Join(exeDir, "nope")} }
	defer func() { exePath, tempRoots = oldExe, oldRoots; clearDirCaches() }()

	d := dataDir()
	if d == "" {
		t.Fatal("能建目录却报不可用")
	}
	f, err := os.CreateTemp(d, "probe-*")
	if err != nil {
		t.Fatalf("选中的目录其实写不进去：%v", err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}

// 存档位置更换后，用户既有设置须迁移过来，且只复制不移动。
func TestArchiveMigrationBringsLegacySettingsOver(t *testing.T) {
	clearDirCaches()
	legacyBase := t.TempDir()
	exeDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", legacyBase)

	oldFile := guiConfigFile
	oldExe, oldRoots := exePath, tempRoots
	defer func() {
		guiConfigFile = oldFile
		exePath, tempRoots = oldExe, oldRoots
		clearDirCaches()
	}()

	// 先在旧位置落一份配置
	legacyPath := filepath.Join(legacyBase, "mclbx", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	guiConfigFile = func() string { return legacyPath }
	if _, err := saveUI(guiUIState{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	resetGuiConfigCache()

	// 再把 exe 放到普通目录，让存档切到 exe 同级
	exePath = func() (string, error) { return filepath.Join(exeDir, "mclbx.exe"), nil }
	tempRoots = func() []string { return []string{filepath.Join(exeDir, "nope")} }
	// 换了 exePath / tempRoots 后须显式清缓存：缓存只按 LOCALAPPDATA 做键（见 datadir.go），
	// 认不出这两个条件已变，而上面那次 saveUI 已经解析过一次。
	clearDirCaches()

	archive := filepath.Join(exeDir, archiveDirName)
	if got := dataDir(); got != archive {
		t.Fatalf("存档应当是 %s，实际 %s", archive, got)
	}
	migrated := filepath.Join(archive, "config.json")
	if _, err := os.Stat(migrated); err != nil {
		t.Fatalf("旧配置没有被复制到新存档：%v", err)
	}
	guiConfigFile = func() string { return migrated }
	resetGuiConfigCache()
	if ui := loadUI(); ui.Theme != "dark" {
		t.Errorf("迁移之后应当读到旧设置（主题 dark），实际读到 %q", ui.Theme)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("迁移应当是复制而不是移动，旧文件不该消失：%v", err)
	}
}

// 全部方案都不可用时须返回空串，而不是拼成相对当前目录的路径（会导致设置落进工作目录）。
func TestDataDirReturnsEmptyWhenNothingWorks(t *testing.T) {
	clearDirCaches()
	oldExe := exePath
	exePath = func() (string, error) { return "", os.ErrNotExist }
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_CACHE_HOME", "")
	defer func() { exePath = oldExe; clearDirCaches() }()

	if got := dataDir(); got != "" {
		if filepath.IsAbs(got) {
			t.Skipf("本机 UserCacheDir() 仍然可用（%s），这条只能验证到「没有退化成相对路径」", got)
		}
		t.Errorf("取不到可写位置时应当返回空串，实际返回了相对路径 %q", got)
	}
}
