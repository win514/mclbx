package main

// diag.go 实现一键导出诊断包（版本、系统与网卡、体检结论、最近日志）。

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func cmdDiag(args []string) error {
	fs := flag.NewFlagSet("diag", flag.ContinueOnError)
	out := fs.String("out", "", "输出路径；留空写到桌面；- 表示只打印不落盘")
	tail := fs.Int("log", 400, "附带最近一次运行日志的最后多少行；0 表示不带")
	port := fs.Int("port", 25565, "体检使用的游戏端口")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 体检含网络探测，可能耗时十几秒，先提示。
	fmt.Printf("正在收集环境信息，最长约 20 秒…\n")
	text := diagText(*port, *tail)

	if *out == "-" {
		fmt.Print(text)
		return nil
	}
	path := *out
	if path == "" {
		path = defaultDiagPath()
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("写入诊断包失败：%w", err)
	}
	fmt.Printf("结论：诊断包已生成\n")
	fmt.Printf("      文件：%s\n", path)
	fmt.Printf("      大小：%d 字节、%d 行\n", len(text), strings.Count(text, "\n")+1)
	fmt.Printf("      发给他人之前请先查看：文件里含本机网络地址。\n")
	return nil
}

// diagHeader 返回诊断包开头的两行标题。
func diagHeader() string {
	return fmt.Sprintf("==== mclbx 诊断包 ====\n"+
		"生成时间 : %s\n"+
		"注意     : 本文件含本机网络地址与端口。发给他人之前请先自行检查。\n\n",
		time.Now().Format("2006-01-02 15:04:05 -0700"))
}

// diagText 组装整份诊断包。分成四段：本机与程序、网卡地址、环境体检、最近日志。
func diagText(port, logTail int) string {
	var b strings.Builder

	b.WriteString(diagHeader())

	b.WriteString("---- 一、程序与平台 ----\n")
	fmt.Fprintf(&b, "程序版本 : %s\n", version)
	fmt.Fprintf(&b, "构建环境 : %s %s/%s，%d 逻辑核心\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Fprintf(&b, "管理员   : %s\n", yesNo(isElevated()))
	fmt.Fprintf(&b, "界面模式 : %s\n", guiModeText())
	if d := dataDir(); d != "" {
		fmt.Fprintf(&b, "数据目录 : %s\n", d)
	}
	if p := sessionLogPath(); p != "" {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "会话日志 : %s（%.1f KB，%s）\n",
				p, float64(st.Size())/1024, st.ModTime().Format("01-02 15:04"))
		} else {
			fmt.Fprintf(&b, "会话日志 : %s（暂无；执行过一次操作之后才会有）\n", p)
		}
	}
	fmt.Fprintf(&b, "\n")

	b.WriteString("---- 二、本机网卡与地址 ----\n")
	b.WriteString(netInterfacesText())
	b.WriteString("\n")

	b.WriteString("---- 三、环境体检 ----\n")
	// say 是体检过程的原样输出，一并带上。
	var said strings.Builder
	facts := collectFacts(port, false, func(format string, a ...any) {
		fmt.Fprintf(&said, format, a...)
		if !strings.HasSuffix(format, "\n") {
			said.WriteString("\n")
		}
	})
	b.WriteString(said.String())
	b.WriteString("\n体检结论：\n")
	findings := judge(facts)
	if len(findings) == 0 {
		b.WriteString("  （没有需要提醒的项）\n")
	}
	for _, f := range findings {
		fmt.Fprintf(&b, "  [%s] %s\n", diagLevelText(f.Level), f.What)
		if f.Why != "" {
			fmt.Fprintf(&b, "        原因：%s\n", f.Why)
		}
		if f.Fix != "" {
			fmt.Fprintf(&b, "        建议：%s\n", f.Fix)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "---- 四、最近一次运行日志")
	if logTail > 0 {
		fmt.Fprintf(&b, "（最后 %d 行）", logTail)
	}
	fmt.Fprintf(&b, " ----\n")
	b.WriteString(sessionLogTail(logTail))

	return b.String()
}

// netInterfacesText 列出启用中的网卡与地址。
func netInterfacesText() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return "  （读取网卡失败：" + err.Error() + "）\n"
	}
	var b strings.Builder
	n := 0
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		n++
		fmt.Fprintf(&b, "  [%s]\n", it.Name)
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "        %s%s\n", ipnet.String(), addrTag(ipnet.IP))
		}
	}
	if n == 0 {
		return "  （没有启用中的物理网卡）\n"
	}
	return b.String()
}

// addrTag 给地址标注公网/内网/链路本地。
func addrTag(ip net.IP) string {
	if ip.To4() != nil {
		if ip.IsPrivate() {
			return "（IPv4，内网）"
		}
		if ip.IsGlobalUnicast() {
			return "（IPv4，公网）"
		}
		return ""
	}
	switch {
	case ip.IsLinkLocalUnicast():
		return "（IPv6，链路本地）"
	case ip.IsPrivate():
		return "（IPv6，内网）"
	case ip.IsGlobalUnicast():
		return "（IPv6，公网）"
	}
	return ""
}

// sessionLogTail 取会话日志的最后 n 行，取不到则说明原因。
func sessionLogTail(n int) string {
	if n <= 0 {
		return "（未附带日志）\n"
	}
	p := sessionLogPath()
	if p == "" {
		return "（取不到数据目录，无法定位会话日志）\n"
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "（暂无会话日志：执行过一次操作之后才会有）\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		var b strings.Builder
		fmt.Fprintf(&b, "（共 %d 行，以下为最后 %d 行）\n", len(lines), n)
		b.WriteString(strings.Join(lines[len(lines)-n:], "\n"))
		b.WriteString("\n")
		return b.String()
	}
	return strings.Join(lines, "\n") + "\n"
}

func diagLevelText(level string) string {
	switch level {
	case "ok":
		return "正常"
	case "warn":
		return "注意"
	case "bad":
		return "问题"
	}
	return level
}

func guiModeText() string {
	if guiMode() {
		return "网页界面（任务子进程）"
	}
	return "命令行"
}

func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

// defaultDiagPath 返回默认输出路径（桌面，取不到时退回数据目录）。
func defaultDiagPath() string {
	name := "mclbx 诊断包-" + time.Now().Format("20060102-150405") + ".txt"
	if home, err := os.UserHomeDir(); err == nil {
		if desk := filepath.Join(home, "Desktop"); dirExists(desk) {
			return filepath.Join(desk, name)
		}
	}
	if d := dataDir(); d != "" {
		return filepath.Join(d, name)
	}
	return name // 兜底：当前目录
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func sessionLogPath() string {
	d := dataDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "最近一次.log")
}
