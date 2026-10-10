package main

// verify.go 按真实客户端步骤校验地址：地址格式、域名/SRV、入口端口、Java 版握手，
// 每步给出通过或卡点；仅从本机发起，不代表外部可达性。

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type verifyStep struct {
	name string
	ok   bool
	note string
}

// verifyTarget 将地址拆为 host/port/room：支持 [IPv6]:port、裸 IPv6、域名、host:port 与 mclbx:// 链接；hasPort 决定是否查 SRV。
func verifyTarget(raw string) (host string, port int, room string, hasPort bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0, "", false, fmt.Errorf("未提供地址")
	}
	if len(s) >= len("mclbx://") && strings.EqualFold(s[:len("mclbx://")], "mclbx://") {
		plan, perr := parseMclbxLink(s)
		if perr != nil {
			return "", 0, "", false, perr
		}
		if len(plan.Hops) == 0 {
			return "", 0, "", false, fmt.Errorf("该链接中无任何落点")
		}
		h := plan.Hops[0]
		// 房间码须过同一套校验：room= 已 URL 解码，可能含换行且会回显到终端；空房间码不校验。
		rc := ""
		if plan.Room != "" {
			var rerr error
			if rc, rerr = normalizeRoomCode(plan.Room); rerr != nil {
				return "", 0, "", false, fmt.Errorf("链接中的房间码不可用：%w", rerr)
			}
		}
		if herr := checkHostUsable(s, h.Host); herr != nil {
			return "", 0, "", false, herr
		}
		return h.Host, h.Port, rc, true, nil
	}
	// 带方括号的 IPv6，或者 host:port
	if strings.HasPrefix(s, "[") {
		h, p, perr := net.SplitHostPort(s)
		if perr != nil {
			return "", 0, "", false, fmt.Errorf("地址形似 [IPv6]:端口，但无法解析：%w", perr)
		}
		n, perr := strconv.Atoi(p)
		if perr != nil || n < 1 || n > 65535 {
			return "", 0, "", false, fmt.Errorf("端口不合法：%q", p)
		}
		if herr := checkHostUsable(s, h); herr != nil {
			return "", 0, "", false, herr
		}
		return h, n, "", true, nil
	}
	if strings.Contains(s, ":") {
		// 可能是 host:port，也可能是裸 IPv6（后者会让 SplitHostPort 失败）
		if h, p, perr := net.SplitHostPort(s); perr == nil {
			n, e2 := strconv.Atoi(p)
			if e2 != nil || n < 1 || n > 65535 {
				return "", 0, "", false, fmt.Errorf("端口不合法：%q", p)
			}
			if herr := checkHostUsable(s, h); herr != nil {
				return "", 0, "", false, herr
			}
			return h, n, "", true, nil
		}
		if ip := net.ParseIP(s); ip != nil {
			return s, 25565, "", false, nil
		}
		return "", 0, "", false, fmt.Errorf("该地址既不是 host:port，也不是合法的 IPv6")
	}
	if herr := checkHostUsable(s, s); herr != nil {
		return "", 0, "", false, herr
	}
	return s, 25565, "", false, nil
}

// checkHostUsable 校验主机名非空且不含空白或控制字符。
func checkHostUsable(raw, host string) error {
	if host == "" {
		return fmt.Errorf("仅提供端口，缺少主机名：%q", raw)
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("主机名包含空白或控制字符：%q", host)
		}
	}
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	addr := fs.String("addr", "", "要验证的地址：房间地址 / host:port / mclbx:// 链接")
	name := fs.String("server-name", "", "握手包中填写的服务器地址，默认使用地址中的主机名")
	timeout := fs.Duration("timeout", 5*time.Second, "每一步的超时")
	resolver := fs.String("resolver", "", "使用指定的 DNS 服务器解析，默认使用系统解析器")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *addr == "" {
		return fmt.Errorf("用法：mclbx verify --addr <待验证地址>")
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" 校验入口地址\n")
	fmt.Printf("================================================\n")
	fmt.Printf(" 目标        : %s\n", *addr)
	fmt.Printf(" 说明        : 以下步骤均从本机发起。最后一步无法在本机完成，会另行说明。\n")
	fmt.Printf("------------------------------------------------\n")

	// 第 1 步：拆地址
	host, port, room, hasPort, err := verifyTarget(*addr)
	if err != nil {
		fmt.Printf(" ✗ 1/4 地址格式      : %v\n", err)
		fmt.Printf("      原因：地址格式无法解析\n")
		fmt.Printf("      处理：从房主提供的地址整段复制，避免手动输入遗漏端口\n")
		return fmt.Errorf("地址不合法")
	}
	if room != "" {
		fmt.Printf(" ✓ 1/4 地址格式      : 主机 %s，端口 %d，房间 %s\n", host, port, room)
	} else {
		fmt.Printf(" ✓ 1/4 地址格式      : 主机 %s，端口 %d%s\n", host, port, map[bool]string{true: "", false: "，未写端口，按 25565 计算"}[hasPort])
	}

	// 第 2 步：解析（域名才需要）
	dialHost := host
	if net.ParseIP(host) == nil {
		if !hasPort && *resolver == "" {
			target, tport, note, serr := resolveMinecraftSRV(host, *resolver, *timeout)
			if serr != nil {
				fmt.Printf(" ✗ 2/4 域名/SRV 解析 : %v\n", serr)
				hintFix(serr.Error())
				return fmt.Errorf("解析失败")
			}
			fmt.Printf(" ✓ 2/4 域名/SRV 解析 : %s\n", note)
			if target != "" {
				dialHost, port = target, tport
			}
		} else {
			ip, lerr := lookupIPWith(host, *resolver, *timeout)
			if lerr != nil {
				fmt.Printf(" ✗ 2/4 域名解析      : %v\n", lerr)
				hintFix(lerr.Error())
				return fmt.Errorf("解析失败")
			}
			fmt.Printf(" ✓ 2/4 域名解析      : %s 解析为 %s\n", host, ip)
			dialHost = ip.String()
		}
	} else {
		fmt.Printf(" - 2/4 域名解析      : 输入为 IP 字面量，跳过\n")
	}

	// 第 3 步：连接入口端口（端口在 SRV 解析后确定）；基岩版走 UDP，TCP 连接必然失败，先给出提示。
	if note := bedrockPortNote(dialHost, port); note != "" {
		fmt.Printf(" ✗ 3/4 连接入口端口  : %s\n", note)
		return fmt.Errorf("目标端口是基岩版的默认端口，本工具仅支持 Java 版")
	}
	addrStr := net.JoinHostPort(dialHost, strconv.Itoa(port))
	c, derr := net.DialTimeout("tcp", addrStr, *timeout)
	if derr != nil {
		fmt.Printf(" ✗ 3/4 连接入口端口  : 连接 %s 失败，%v\n", addrStr, derr)
		hintFix(derr.Error())
		return fmt.Errorf("连不上入口端口")
	}
	fmt.Printf(" ✓ 3/4 连接入口端口  : %s 已接受 TCP 连接\n", addrStr)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(*timeout))

	// 第 4 步：执行一次服务器列表查询
	addrField := host
	if *name != "" {
		addrField = *name
	}
	st, ms, qerr := slpQueryOn(c, port, addrField)
	if qerr != nil {
		fmt.Printf(" ✗ 4/4 Java 版握手   : %v\n", qerr)
		fmt.Printf("      原因：TCP 已连通但握手无响应，通常是入口后方的游戏端口未开放\n")
		fmt.Printf("      处理：在房主机器上执行 mclbx ping --host 127.0.0.1 确认游戏进程正常\n")
		// 门卫拦截：对端收到握手后直接挂断，通常是房间码填错
		if room != "" {
			fmt.Printf("      原因：玩家在游戏中填写的名称缺少房间码 %s，连接被拒绝\n", room)
		}
		return fmt.Errorf("握手失败")
	}

	fmt.Printf(" ✓ 4/4 Java 版握手   : %d ms，对端为 %s\n", ms, st.Version.Name)
	if motd := describeText(st.Description); motd != "" {
		fmt.Printf("      MOTD        : %s\n", motd)
	}
	fmt.Printf("      在线人数    : %d / %d\n", st.Players.Online, st.Players.Max)
	fmt.Printf("------------------------------------------------\n")
	fmt.Printf(" 结论：该地址从本机发起可以连通。\n")
	fmt.Printf(" 玩家一侧能否连通需从外部网络验证，本机无法确认。\n")
	fmt.Printf(" 可由玩家执行相同命令进行验证：\n")
	fmt.Printf("     mclbx verify --addr %s\n", *addr)
	fmt.Printf("================================================\n")
	return nil
}

// hintFix 将底层报错转换为一行处理建议。
func hintFix(errText string) {
	for _, h := range explainError(errText) {
		fmt.Printf("      %s\n", strings.TrimSpace(h.text))
	}
}
