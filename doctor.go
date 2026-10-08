package main

// doctor.go —— 一键体检：收集各路线探测事实并给出明确建议。事实收集（collectFacts，
// 依赖网络）与判断（judge，纯函数）分离，使判断逻辑可独立测试。

import (
	"flag"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"
)

// docFacts 体检收集到的事实
type docFacts struct {
	OS        string
	Arch      string
	Elevated  bool
	HaveAdmin bool // 当前是否已提权

	V6Global bool
	V6Addrs  []string
	// V6Tried 表示本次体检探过 IPv6 出口（无 IPv6 或关闭 STUN 时不探）；仅探过且未通才告警。
	V6Tried  bool
	V6Reach  bool
	V6Out    string
	V4OutIP  string
	NatShape string // 出站映射的判定结果（锥形/对称/…）

	UpnpOK    bool
	UpnpWhy   string
	NatPMPOK  bool
	NatPMPWhy string
	PCPOK     bool
	PCPWhy    string

	FwRules   int    // 本工具已加的入站放行规则条数
	FwWhy     string // 查不到原因时说明
	HaveWebV2 bool   // 内置窗口能不能用（WebView2 运行时在不在）

	GamePort     int
	GamePortFree bool
}

// docFinding 一条结论
type docFinding struct {
	Level string // ok / warn / bad
	What  string
	Why   string
	Fix   string
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	port := fs.Int("port", 25565, "游戏端口，同时检测占用情况")
	quick := fs.Bool("quick", false, "跳过自动端口映射探测，更快但结论更保守")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s · 环境检测\n", version)
	fmt.Printf("================================================\n")

	// 逐步报进度，走 guiDetail（界面归入折叠的「原始输出」，命令行输出不变）。
	f := collectFacts(*port, *quick, func(format string, a ...any) {
		guiDetail(" .. %s", fmt.Sprintf(format, a...))
	})
	printFacts(f)
	// 输出一行机器标记供界面判断哪些操作不可用并置灰；仅界面模式下输出。
	if m := guiCapMarker(f); m != "" {
		fmt.Println(m)
	}

	fmt.Println("------------------------------------------------")
	fmt.Println("结论：")
	findings := judge(f)
	for _, x := range findings {
		mark := "✓"
		switch x.Level {
		case "warn":
			mark = "!"
		case "bad":
			mark = "✗"
		}
		fmt.Printf(" %s %s\n", mark, x.What)
		if x.Why != "" {
			fmt.Printf("  说明：%s\n", x.Why)
		}
		if x.Fix != "" {
			fmt.Printf("  建议：%s\n", x.Fix)
		}
	}
	fmt.Println("------------------------------------------------")
	fmt.Printf("  建议：%s\n", recommend(f))
	return nil
}

// collectFacts 依次收集体检事实；say 用于报进度，可为 nil。
func collectFacts(port int, quick bool, say func(string, ...any)) docFacts {
	step := func(format string, a ...any) {
		if say != nil {
			say(format, a...)
		}
	}
	f := docFacts{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
	if runtime.GOOS == "windows" {
		f.Elevated = isElevated()
	}

	step("检测公网 IPv6 地址")
	for _, a := range globalIPv6() {
		f.V6Global = true
		f.V6Addrs = append(f.V6Addrs, a.IP.String())
	}

	// NAT 判定须用同一个 socket 打多台服务器：换 socket 会同时换本地端口，导致映射判定不稳定。
	step("探测 STUN 公网映射与 NAT 类型")
	if v4 := stunProbeShared(defaultStunServers(), 1500*time.Millisecond); len(v4) > 0 {
		f.V4OutIP = v4[0].IP.String()
		f.NatShape = natVerdict(v4)
	} else {
		f.NatShape = "STUN 无应答"
	}

	// 有公网 IPv6 地址时再强制走 IPv6 问一次 STUN，验证出口是否真的可用。
	if f.V6Global {
		step("验证 IPv6 出口是否真的可用")
		f.V6Tried = true
		if r, ok := stunProbeV6(defaultStunServers(), 1500*time.Millisecond); ok {
			f.V6Reach = true
			f.V6Out = r.IP.String()
		}
	}

	step("探测本机 %d 端口占用", port)
	f.GamePort = port
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		f.GamePortFree = true
		_ = ln.Close()
	}

	if !quick {
		step("探测自动端口映射，覆盖 UPnP / NAT-PMP / PCP")
		probe := func(fn func(string, int, int, time.Duration) (*natMapping, error)) (bool, string) {
			m, err := fn("TCP", port, port, 30*time.Second)
			if err != nil {
				return false, err.Error()
			}
			m.Release() // 体检不留下任何映射
			return true, ""
		}
		f.UpnpOK, f.UpnpWhy = probe(upnpMap)
		f.NatPMPOK, f.NatPMPWhy = probe(natpmpMap)
		f.PCPOK, f.PCPWhy = probe(pcpMap)
	}

	if runtime.GOOS == "windows" {
		step("查询 Windows 防火墙入站规则，端口 %d", port)
		if out, err := runCmd("netsh", firewallShowArgs(firewallRuleName(port, "TCP"))...); err == nil && strings.Contains(out, "mclbx-") {
			f.FwRules++
		}
		if out, err := runCmd("netsh", firewallShowArgs(firewallRuleName(port, "UDP"))...); err == nil && strings.Contains(out, "mclbx-") {
			f.FwRules++
		}
	}
	if webView2Available() {
		f.HaveWebV2 = true
	}
	return f
}

// printFacts 逐行输出探测事实，走 guiDetail（命令行下等同 Println）。
func printFacts(f docFacts) {
	sys := f.OS + "/" + f.Arch
	if f.OS == "windows" {
		if f.Elevated {
			sys += "，已提权"
		} else {
			sys += "，未提权，修改入站规则时会弹一次 UAC"
		}
	}
	guiDetail(" 系统        : %s", sys)
	if f.V6Global {
		shown := f.V6Addrs
		if len(shown) > 2 {
			shown = shown[:2]
		}
		guiDetail(" 公网 IPv6   : %d 个  %s", len(f.V6Addrs), strings.Join(shown, ", "))
		if f.V6Reach {
			guiDetail(" IPv6 出口   : 可用，%s", f.V6Out)
		} else if f.V6Tried {
			guiDetail(" IPv6 出口   : 无应答（有地址，但可能出不去）")
		}
	} else {
		guiDetail(" 公网 IPv6   : 无")
	}
	if f.V4OutIP != "" {
		guiDetail(" 出口 IP     : %s", f.V4OutIP)
		guiDetail(" 出站 NAT    : %s", f.NatShape)
	} else {
		guiDetail(" 出口 IP     : 未知，%s", f.NatShape)
	}
	if !f.UpnpOK && !f.NatPMPOK && !f.PCPOK && f.UpnpWhy == "" && f.NatPMPWhy == "" && f.PCPWhy == "" {
		guiDetail(" 端口映射    : 未探测")
	} else {
		guiDetail(" 端口映射    : UPnP=%s NAT-PMP=%s PCP=%s",
			yn(f.UpnpOK, f.UpnpWhy), yn(f.NatPMPOK, f.NatPMPWhy), yn(f.PCPOK, f.PCPWhy))
	}
	if f.OS == "windows" {
		guiDetail(" 入站规则    : %d 条，端口 %d，仅限本程序", f.FwRules, f.GamePort)
	}
	guiDetail(" 本机 %d   : %s", f.GamePort, map[bool]string{true: "空闲", false: "被占用，游戏可能正在运行"}[f.GamePortFree])
	guiDetail(" 内置窗口    : %s", map[bool]string{true: "可用，已安装 WebView2", false: "不可用，将改用浏览器"}[f.HaveWebV2])
}

// natToken 将 NAT 判定压成不带空格的记号，先排除不稳定，再从具体到笼统判定。
func natToken(shape string) string {
	switch {
	case strings.Contains(shape, "不稳定"):
		return "unknown"
	case strings.Contains(shape, "对称"):
		return "symmetric"
	case strings.Contains(shape, "端口受限"):
		return "portrestricted"
	case strings.Contains(shape, "受限"):
		return "restricted"
	case strings.Contains(shape, "全锥形"):
		return "fullcone"
	}
	return "unknown"
}

// guiCapMarker 将体检事实压成一行机器标记（仅界面模式下返回），值不带空格以便按空格切分。
// 界面据 v6、upnp/natpmp/pcp、port+portfree 三项置灰。
func guiCapMarker(f docFacts) string {
	if !guiMode() {
		return ""
	}
	bit := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	// plat 也在标记里：仅 Windows 支持提示放行入站。
	plat := "other"
	if runtime.GOOS == "windows" {
		plat = "win"
	}
	return fmt.Sprintf("##CAP## plat=%s v6=%s upnp=%s natpmp=%s pcp=%s nat=%s port=%d portfree=%s fw=%d wv2=%s",
		plat, bit(f.V6Global), bit(f.UpnpOK), bit(f.NatPMPOK), bit(f.PCPOK),
		natToken(f.NatShape), f.GamePort, bit(f.GamePortFree), f.FwRules, bit(f.HaveWebV2))
}

func yn(ok bool, why string) string {
	if ok {
		return "可用"
	}
	if why == "" {
		return "不可用"
	}
	// 按字符截断（不能按字节，否则会劈开汉字）。
	if r := []rune(why); len(r) > 20 {
		why = string(r[:20]) + "…"
	}
	return "不可用，" + why
}

func judge(f docFacts) []docFinding {
	var out []docFinding

	// 1) 公网直连：只看有无公网地址
	if f.V6Global {
		what := "公网直连可用：本机具备公网 IPv6"
		why := "玩家在游戏内「多人游戏 → 直接连接」填入该地址即可。"
		if f.V6Reach {
			what = fmt.Sprintf("公网直连可用：公网 IPv6 已验通（出口 %s）", f.V6Out)
			why = "用 IPv6 问到了 STUN，说明这个地址确实可路由；玩家在游戏内「多人游戏 → 直接连接」填入该地址即可。"
		}
		out = append(out, docFinding{Level: "ok", What: what, Why: why})

		// 地址在但出口不通：仅告警，不推翻公网入口的推荐。
		if f.V6Tried && !f.V6Reach {
			out = append(out, docFinding{
				Level: "warn",
				What:  "但 IPv6 出口探测无应答：地址在网卡上，未必出得去",
				Why:   "运营商没开 IPv6、路由器没下发前缀、或上游把 IPv6 丢进黑洞时，网卡上照样有公网 IPv6 地址，但发出去的包回不来。此时玩家按这个地址连会一直超时，而本机看不出任何异常。",
				Fix:   "先在这台机器上打开一个纯 IPv6 网站确认；打不开就先修 IPv6，或改用软件入场与中继转发。",
			})
		}
	} else {
		anyMap := f.UpnpOK || f.NatPMPOK || f.PCPOK
		if anyMap {
			out = append(out, docFinding{
				Level: "ok",
				What:  "自动端口映射可用：" + firstTrue(f) + "，玩家可直接用该地址连接",
				Why:   "路由器将本机端口映射至公网地址，玩家可直接连接，不依赖第三方。",
			})
		} else {
			out = append(out, docFinding{
				Level: "warn",
				What:  "公网直连不可用：本机无公网 IPv6，自动端口映射不可用",
				Why:   "玩家需主动连接本机，而本机位于 NAT 之后，路由器不具备自动映射能力。",
				Fix:   "在光猫或路由器开启 IPv6；在路由器手动配置端口映射；或改用中继转发。",
			})
		}
	}

	// 2) 打洞：看网络类型
	switch {
	case strings.Contains(f.NatShape, "对称"):
		out = append(out, docFinding{
			Level: "warn",
			What:  "NAT 为对称型：两端均在此类 NAT 后时无法直连",
			Why:   "此类 NAT 对每个目标分配不同端口，对端无法预测。",
			Fix:   "两端改用中继转发方式，无需额外配置。",
		})
	case strings.Contains(f.NatShape, "锥形"):
		out = append(out, docFinding{
			Level: "ok",
			What:  "NAT 为锥形：直连成功率较高",
			Why:   "同一出口端口对各目标保持稳定，对端可以预测。",
		})
	}

	// 3) 兜底：TCP 中继
	out = append(out, docFinding{
		Level: "ok",
		What:  "兜底方案：TCP 中继转发仅使用 TCP，可访问网页的网络均可用",
		Why:   "UDP 受限时仍可用；数据经中继转发且为明文。",
		Fix: "在任意一台有公网地址的机器上运行 mclbx relaybox" +
			"（本程序自带，不必另外装服务），把它输出的那一行 mclbx:// 填进「中继服务器」即可。见「中继服务」分组。",
	})

	// 4) Windows 防火墙
	if f.OS == "windows" && f.FwRules == 0 {
		out = append(out, docFinding{
			Level: "warn",
			What:  "Windows 防火墙尚未配置入站规则，玩家可能无法连接",
			Why:   "防火墙默认拦截入站连接；本程序不会报错，仅表现为连接无法建立。",
			Fix:   "点击界面上方提示条中的「放行入站」，或执行 mclbx firewall。仅对本程序与指定端口生效。",
		})
	} else if f.OS == "windows" {
		out = append(out, docFinding{
			Level: "ok",
			What:  fmt.Sprintf("Windows 防火墙已配置 %d 条入站规则，端口 %d，仅限本程序", f.FwRules, f.GamePort),
		})
	}

	// 5) 内置窗口
	if !f.HaveWebV2 {
		out = append(out, docFinding{
			Level: "warn",
			What:  "未安装 WebView2 运行时，内置窗口不可用",
			Why:   "将自动改用系统浏览器打开，功能不受影响。",
			Fix: "安装 WebView2 运行时。在浏览器里用时注意：" +
				"关掉标签页不会结束房间 —— 房间要在界面里点「停止」才会结束。",
		})
	}

	// 6) 端口占用
	if !f.GamePortFree {
		out = append(out, docFinding{
			Level: "warn",
			What:  fmt.Sprintf("本机端口 %d 已被占用", f.GamePort),
			Why:   "游戏运行时属正常现象；否则可能存在残留进程。",
			Fix:   "确认无残留进程，或更换端口。",
		})
	}
	return out
}

func firstTrue(f docFacts) string {
	switch {
	case f.UpnpOK:
		return "UPnP"
	case f.NatPMPOK:
		return "NAT-PMP"
	case f.PCPOK:
		return "PCP"
	}
	return "无"
}

// recommend 给出一句推荐路线。
func recommend(f docFacts) string {
	switch {
	case f.V6Global:
		return "采用公网直连；本机具备公网 IPv6，其余方式为备用"
	case f.UpnpOK || f.NatPMPOK || f.PCPOK:
		return "采用自动端口映射，向玩家提供直连地址"
	case strings.Contains(f.NatShape, "锥形"):
		return "采用直连模式，两端安装本工具，并以中继转发兜底"
	default:
		return "采用中继转发"
	}
}
