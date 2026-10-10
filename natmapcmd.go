package main

// natmapcmd.go 实现 `mclbx natmap` 命令：单独执行端口映射，可指定协议与常驻续约。

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func cmdNatmap(args []string) error {
	fs := flag.NewFlagSet("natmap", flag.ContinueOnError)
	port := fs.Int("port", 0, "要暴露的本机端口，必填")
	ext := fs.Int("ext", 0, "要占用的外网端口；默认与内网端口相同")
	proto := fs.String("proto", "both", "映射哪种协议：tcp / udp / both")
	keep := fs.Bool("keep", false, "保持运行：到租期自动续约，Ctrl+C 退出并撤销映射")
	lifetime := fs.Duration("life", natMapLifetime, "映射租期")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *port <= 0 {
		return fmt.Errorf("必须指定要暴露的本机端口：mclbx natmap --port 25565")
	}

	protos, err := parseProtos(*proto)
	if err != nil {
		return err
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s · 自动端口映射\n", version)
	fmt.Printf("================================================\n")
	fmt.Printf(" 内网端口 : %d\n", *port)
	if *ext > 0 {
		fmt.Printf(" 目标外网 : %d\n", *ext)
	}
	fmt.Printf(" 协议     : %s\n", strings.Join(protos, " + "))
	fmt.Println("------------------------------------------------")

	var opened []*natMapping
	for _, p := range protos {
		m, tried := openNatMapping(p, *port, *ext, *lifetime)
		for _, t := range tried {
			fmt.Printf(" %s %s\n", p, t)
		}
		if m != nil {
			opened = append(opened, m)
			fmt.Printf(" %.2s 成功：外网 %s:%d %s 租期 %s\n",
				p, m.ExtIP, m.ExtPort, m.Method, m.Lifetime.Round(time.Second))
		}
	}

	if len(opened) == 0 {
		fmt.Println("\n 结论：三套协议均未建立映射。")
		fmt.Println("   原因 1：路由器未启用 UPnP / NAT-PMP / PCP，请到路由器管理页开启")
		fmt.Println("   原因 2：上游运营商还存在一层 NAT，映射只能建立到运营商设备")
		fmt.Println("   原因 3：当前网络使用对称型 NAT 且网关不支持 PCP")
		return fmt.Errorf("自动端口映射失败")
	}

	fmt.Println("\n 玩家/对端连接上方「外网 地址:端口」即可。")
	if !*keep {
		for _, m := range opened {
			m.Release()
		}
		fmt.Println(" 未加 --keep，映射已撤销；如需长期保留，请加 --keep")
		return nil
	}

	fmt.Println(" 正在保活，租期过半自动续约；Ctrl+C 退出并撤销映射。")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	for _, m := range opened {
		m.Release()
	}
	fmt.Println("\n 映射已撤销。")
	return nil
}

func parseProtos(s string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tcp":
		return []string{"TCP"}, nil
	case "udp":
		return []string{"UDP"}, nil
	case "both", "":
		return []string{"TCP", "UDP"}, nil
	}
	return nil, fmt.Errorf("--proto 只能是 tcp / udp / both，当前为 %q", s)
}

// mapPortsQuiet 静默尝试映射一组端口，全部失败返回 nil。
func mapPortsQuiet(protos []string, intPort, wantExt int) *natMapPlan {
	plan := &natMapPlan{}
	for _, p := range protos {
		m, tried := openNatMapping(p, intPort, wantExt, natMapLifetime)
		plan.Tried = append(plan.Tried, tried...)
		if m != nil {
			plan.Mappings = append(plan.Mappings, m)
			if wantExt > 0 {
				wantExt = m.ExtPort // 后续协议尽量复用同一端口
			}
		}
	}
	if len(plan.Mappings) == 0 {
		return nil
	}
	return plan
}
