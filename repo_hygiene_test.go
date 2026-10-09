package main

// repo_hygiene_test.go 检查仓库自身文件的约定，与程序逻辑无关。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 带中文的 .ps1 必须是 UTF-8 with BOM：PowerShell 5.1 对无 BOM 脚本按本地代码页解码，
// 中文会乱码，乱码中出现引号或括号即导致语法错误。
func TestPs1FilesHaveBOM(t *testing.T) {
	files, err := filepath.Glob("*.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if more, err := filepath.Glob(filepath.Join("scripts", "*.ps1")); err == nil {
		files = append(files, more...)
	}
	if len(files) == 0 {
		t.Fatal("一个 .ps1 都没找到，用例的工作目录不对")
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) < 3 || b[0] != 0xEF || b[1] != 0xBB || b[2] != 0xBF {
			t.Errorf("%s 缺少 UTF-8 BOM：PowerShell 5.1 会按本地代码页解码，中文会乱码并导致语法错误", f)
			continue
		}
		// 纯 ASCII 脚本带 BOM 无害，此处仅作说明
		nonASCII := false
		for _, c := range b {
			if c > 0x7F {
				nonASCII = true
				break
			}
		}
		if !nonASCII {
			t.Logf("%s 带 BOM（脚本内没有非 ASCII 字符，BOM 无害）", f)
		}
	}
}

// 工作区里不许有「名字只由点组成」的目录（`...`、`....`）。
// Windows 的路径解析会剥掉结尾的点，这种目录按路径寻址不到，
// 且会让 `git add` 整个失败而 `commit` 仍成功。
func TestNoDotOnlyDirectories(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		n := e.Name()
		if n != "" && strings.Trim(n, ".") == "" {
			t.Errorf("工作区里有名字只由点组成的目录 %q。先删掉它再提交：\n"+
				"  · Windows 上按路径寻址不到它（结尾的点会被解析器剥掉），只能用目录对象操作；\n"+
				"  · 它会让 git add 整个失败，而 commit 仍然成功 —— 提交看起来做了，实际没带上改动。", n)
		}
	}
}

// 工作区里不许留会被提交进去的临时文件与探针。
// 只抓两类：一次性探针（zz_ 前缀）与程序生成的运行数据目录（存档）。.gitignore 挡住的不管。
func TestNoStrayFilesInWorkTree(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		n := e.Name()
		switch {
		case strings.HasPrefix(n, "zz_"):
			t.Errorf("工作区里留着一次性探针 %q —— 用完就删，它是为了避免进仓库才这样命名的", n)
		case strings.HasSuffix(n, ".bak"), strings.HasSuffix(n, ".orig"), strings.HasSuffix(n, ".tmp"):
			t.Errorf("工作区里留着临时文件 %q —— 提交前删掉", n)
		case n == archiveDirName:
			t.Errorf("工作区里出现了程序自己的存档目录 %q —— 它是运行产物，不该进仓库，"+
				"提交前删掉（同时确认 .gitignore 里挡住了它）", n)
		}
	}
}

// 存档目录名必须被 .gitignore 挡住（它生成在 exe 同级，而 exe 常在仓库目录里）。
func TestArchiveDirIsIgnored(t *testing.T) {
	b, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), archiveDirName) {
		t.Errorf(".gitignore 里没有挡住存档目录 %q —— 它是运行产物，不该进仓库", archiveDirName)
	}
}
