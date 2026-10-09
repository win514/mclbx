package main

// firewall.go 用 netsh 添加 Windows 入站放行规则，仅针对本程序与指定端口。
// Windows 防火墙默认拦截所有入站连接，且被拦时本程序不会报错。

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// firewallRuleName 生成带固定前缀的规则名，便于识别与删除。
func firewallRuleName(port int, proto string) string {
	return fmt.Sprintf("mclbx-%s-%d", strings.ToLower(proto), port)
}

// parsePortList 解析 --port，支持中英文逗号、分号或空格分隔的多个端口。
func parsePortList(spec string) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	fields := strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == ' ' || r == '\t'
	})
	for _, part := range fields {
		p, err := strconv.Atoi(part)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("端口 %q 不合法：应当是 1-65535 的整数（多个端口用逗号分隔，如 25565,8090）", part)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--port 不能为空；本工具默认需要游戏端口与信令端口 8090")
	}
	return out, nil
}

// firewallAddArgs 构造放行命令，为纯函数以便测试。
func firewallAddArgs(ruleName string, port int, proto, exePath string) []string {
	return []string{
		"advfirewall", "firewall", "add", "rule",
		"name=" + ruleName,
		"dir=in",
		"action=allow",
		"protocol=" + strings.ToUpper(proto),
		"localport=" + fmt.Sprint(port),
		"program=" + exePath,
		"profile=any",
		"enable=yes",
	}
}

func firewallDelArgs(ruleName string) []string {
	return []string{"advfirewall", "firewall", "delete", "rule", "name=" + ruleName}
}

// firewallShowArgs 构造查询规则的命令。
func firewallShowArgs(ruleName string) []string {
	return []string{"advfirewall", "firewall", "show", "rule", "name=" + ruleName}
}

func cmdFirewall(args []string) error {
	fs := flag.NewFlagSet("firewall", flag.ContinueOnError)
	portSpec := fs.String("port", "25565", "入站规则使用的端口；可用逗号分隔多个，如 25565,8090")
	proto := fs.String("proto", "both", "入站规则协议：tcp / udp / both")
	remove := fs.Bool("remove", false, "删除本工具加的规则")
	dryRun := fs.Bool("dry-run", false, "只打印将要执行的命令，不改动系统")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	ports, err := parsePortList(*portSpec)
	if err != nil {
		return err
	}

	if runtime.GOOS != "windows" {
		return fmt.Errorf("这条命令是给 Windows 防火墙用的；当前系统是 %s，Linux/macOS 请用各自的防火墙工具", runtime.GOOS)
	}

	protos, err := parseProtos(*proto)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	portStrs := make([]string, 0, len(ports))
	for _, p := range ports {
		portStrs = append(portStrs, fmt.Sprint(p))
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s · 添加入站规则\n", version)
	fmt.Printf("================================================\n")
	fmt.Printf(" 程序 : %s\n", exe)
	fmt.Printf(" 端口 : %s %s\n", strings.Join(portStrs, ", "), strings.Join(protos, " + "))
	fmt.Printf(" 说明 : 仅针对当前程序与端口，不修改防火墙开关，也不影响其他规则\n")
	fmt.Println("------------------------------------------------")

	var cmds [][]string
	for _, port := range ports {
		for _, p := range protos {
			name := firewallRuleName(port, p)
			if *remove {
				cmds = append(cmds, firewallDelArgs(name))
			} else {
				cmds = append(cmds, firewallAddArgs(name, port, p, exe))
			}
		}
	}

	for _, c := range cmds {
		fmt.Printf(" netsh %s\n", strings.Join(c, " "))
	}
	if *dryRun {
		fmt.Println("\n 仅打印，未做任何修改。")
		return nil
	}

	if !isElevated() {
		fmt.Println("\n 修改防火墙需要管理员权限，当前权限不足。")
		return elevateSelf()
	}

	for _, c := range cmds {
		if out, err := runCmd("netsh", c...); err != nil {
			return fmt.Errorf("执行 netsh 失败：%v，输出：%s", err, strings.TrimSpace(out))
		}
	}
	if *remove {
		fmt.Println("\n 规则已删除。")
	} else {
		fmt.Println("\n 入站规则已添加。规则名带 mclbx- 前缀，可用 --remove 删除：")
		for _, p := range protos {
			fmt.Printf("   mclbx firewall --port %s --proto %s --remove\n", strings.Join(portStrs, ","), strings.ToLower(p))
		}
	}
	return nil
}

// firewallRulesForPort 返回某端口应存在的 TCP 与 UDP 规则名。
func firewallRulesForPort(port int) []string {
	return []string{firewallRuleName(port, "TCP"), firewallRuleName(port, "UDP")}
}
