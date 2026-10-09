package main

// 数据目录是整个「存档」的地基：配置、日志、导入的图片、音乐都挂在它下面。
// 这几条用例把选址规则钉住 —— 规则一旦被改坏，症状是「设置看起来丢了」或者
// 「数据被系统清掉了」，用户几乎不可能自己查出原因。

import (
	"os"
	"path/filepath"
	"testing"
)

func clearDirCaches() {
	archiveCache = dirCache{}
	internalCache = dirCache{}
}

// 存档优先放在 exe 同级 —— 这样「存档」才名副其实：整个文件夹拷走就带走全部设置。
func TestDataDirPrefersExeSibling(t *testing.T) {
	clearDirCaches()
	exeDir := t.TempDir()
	oldExe, oldRoots := exePath, tempRoots
	exePath = func() (string, error) { return filepath.Join(exeDir, "mclbx.exe"), nil }
	// 把「临时目录」指到别处，好让 exeDir 被当成普通目录（t.TempDir 本身在临时目录下）
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

	// 用户把它删掉之后，下一次访问要能自己长回来
	if err := os.RemoveAll(got); err != nil {
		t.Fatal(err)
	}
	if dirExists(got) {
		t.Fatal("目录没删掉")
	}
	clearDirCaches() // 模拟下一次访问（进程内的记忆也要跟着重算）
	again := dataDir()
	if again != want {
		t.Errorf("删掉之后应当仍然选中同一位置 %s，实际 %s", want, again)
	}
	if !dirExists(again) {
		t.Errorf("存档目录被删后没有自动重建：%s", again)
	}
}

// 从临时目录运行时不采用 exe 同级：那种情况下系统清理临时目录会把用户的数据一起带走，
// 而「存档」的意义恰好是留得住。
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

// 浏览器用户目录与解出来的 DLL 不进存档：一个浏览器档子有几百个文件，
// 塞进用户要拷走的目录里会让「存档」没法看；而且它跟这台机器绑定。
// 这条也把 Codex 指出的那个风险钉死：存档搬到 exe 同级时，WebView2 那条路不受影响。
func TestInternalDirStaysOutOfArchive(t *testing.T) {
	clearDirCaches()
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	defer clearDirCaches()

	if got, want := internalDir(), filepath.Join(local, "mclbx"); got != want {
		t.Errorf("内部目录应当落在 LOCALAPPDATA 下（%s），实际 %s", want, got)
	}
}

// 「可写」必须是真的写得进去，而不是 Stat 说目录在。
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

// 存档位置换了之后，用户既有的设置必须跟着过来 —— 否则他看到的第一个现象是「设置全丢了」。
// 而且只复制不移动：新位置以后用不了时，旧位置还得是完整的。
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
	// 换了两个决定结果的条件，就得让缓存失效。
	// 缓存只按 LOCALAPPDATA 做键（见 datadir.go 里那段说明），认不出 exePath / tempRoots 变了；
	// 而上面那次 saveUI 已经解析过一次（保存设置时会顺带记一条变更记录，也要知道存档在哪儿），
	// 不显式清掉的话这里会读到那个已经过时的结果。
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

// 全都不通时必须返回空串，而不是悄悄把路径拼成相对当前目录 ——
// filepath.Join("", "config.json") 会把配置写进用户的任意工作目录，等于设置凭空消失。
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
