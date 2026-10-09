package main

// errhint.go 在原始英文报错下补两行中文提示：一行原因，一行处理。
// 未识别的报错不补提示，避免给出无依据的结论。

import "strings"

// errHint 描述一条"现象 → 原因 → 处理"映射。
// match 的子串按小写匹配任一即命中；also 非空时还须再命中其中一条。
type errHint struct {
	key   string // 去重用
	match []string
	also  []string
	codes []string // Windows 错误码（须独立且带错误码语境，见 hitsCode）
	what  string   // 原因
	fix   string   // 处理（可空）
}

// 越具体的条目放越前。
var errHints = []errHint{
	{
		key: "port-used",
		match: []string{
			"only one usage of each socket address",
			"address already in use",
			"wsaeaddrinuse",
			// 中文 Windows 上的原文
			"每个套接字地址",
			"只允许使用一次",
		},
		codes: []string{"10048"},
		what:  "该端口已被其他程序占用（游戏正在运行，或上一次的进程尚未退出）",
		fix: "关闭占用该端口的程序后重试；「游戏端口」留空时程序会自动探测 25565/25566/25567，" +
			"入口端口可用 --entry 指定",
	},
	{
		key: "port-forbidden",
		match: []string{
			"in a way forbidden",
			"access permissions",
			"permission denied",
			"access is denied",
		},
		// permission denied 仅在 socket 语境下才算端口问题
		also:  []string{"bind", "listen", "socket", "dial", "so_"},
		codes: []string{"10013"}, // WSAEACCES，仅出现在套接字上
		what:  "该端口被系统保留或被安全软件阻止，普通权限无法使用",
		fix:   "改用 1024 以上的端口；已安装安全软件的，为其放行本程序一次",
	},
	{
		key: "refused",
		// 两个平台的报错原文不同，都需匹配
		match: []string{"connection refused", "actively refused", "target machine actively refused", "积极拒绝"},
		codes: []string{"10061"},
		what:  "目标主机可达，但该端口无服务响应",
		fix:   "确认对方的中继或服务正在运行，且端口与链接中的一致；对方重启后端口可能变化，需重新获取链接",
	},
	{
		key: "no-route",
		match: []string{
			"no route to host",
			"network is unreachable",
			"unreachable network",
			"unreachable host",
			"host is down",
		},
		codes: []string{"10065", "10051"}, // WSAEHOSTUNREACH / WSAENETUNREACH
		what:  "目标地址不可达（地址存在但链路不通）",
		fix:   "请对端执行一次「检测本机环境」确认是否具备公网 IPv6；否则改用中继转发",
	},
	{
		key:   "socket-buffer",
		match: []string{"由于系统缓冲区空间不足或队列已满"},
		codes: []string{"10055"}, // WSAENOBUFS
		what:  "本机套接字缓冲或队列已满，保持的连接过多",
		fix:   "关闭部分占用连接的进程后重试；若持续出现，请提供本次日志",
	},
	{
		key:   "dns",
		match: []string{"no such host", "server misbehaving", "temporary failure in name resolution"},
		what:  "域名解析失败",
		fix:   "检查网络连接；或改用 IP 地址（IPv6 地址需加方括号）",
	},
	{
		key:   "addr-family",
		match: []string{"no suitable address found", "address family not supported"},
		what:  "地址类型与套接字协议不匹配（IPv6 地址用于 IPv4 套接字）",
		fix:   "更换地址或链接后重试；若持续出现，请提供这段日志",
	},
	{
		key: "timeout",
		match: []string{
			"i/o timeout",
			"timed out",
			"operation timed out",
			"context deadline exceeded",
			"all retransmissions failed",
			// Windows 原文
			"did not properly respond after a period of time",
			"semaphore timeout period has expired",
		},
		codes: []string{"10060"}, // WSAETIMEDOUT
		what:  "目标未响应：数据已发出但未收到回音，通常为中间链路阻断",
		fix: "改用 TCP 路径（两端均为主动连接，穿透性最强）；" +
			"仍不通时，在一台可联机的机器上运行中继服务",
	},
	{
		key: "reset",
		match: []string{
			"connection reset by peer",
			"forcibly closed",
			"broken pipe",
			"connection aborted",
			// Windows 本机与对端各有一条原文
			"aborted by the software in your host machine",
			"remote host closed the connection",
			"强迫关闭",
		},
		codes: []string{"10054"}, // WSAECONNRESET
		what:  "连接被对端中断（对端退出、被防火墙拦截，或中间设备关闭该连接）",
		fix:   "确认对端进程仍在运行；重新执行一次通常即可恢复",
	},
	{
		key: "turn-alloc",
		// TURN 分配失败由服务器返回（凭据或额度），与本机网络无关
		match: []string{
			"allocation mismatch",
			"insufficient capacity",
			"insufficient bandwidth",
			"quota",
			"allocate error",
			"401 unauthorized", // TURN 长期凭据不正确
		},
		what: "中继服务器拒绝了本次分配：凭据或额度不允许，与本机网络无关",
		fix:  "更换中继地址后重试；使用他人中继时，请向对方索取新的凭据（凭据通常有有效期）",
	},
}

// explainError 对一行输出返回要补的提示行（0~2 行），为纯函数。
func explainError(line string) []hintLine {
	if line == "" {
		return nil
	}
	ls := strings.ToLower(line)
	for _, h := range errHints {
		viaCode := hitsCode(ls, h.codes)
		viaText := hitsAny(ls, h.match) && (len(h.also) == 0 || hitsAny(ls, h.also))
		if !viaCode && !viaText {
			continue
		}
		out := []hintLine{{key: h.key, text: "  原因：" + h.what}}
		if h.fix != "" {
			out = append(out, hintLine{key: h.key + "/fix", text: "  处理：" + h.fix})
		}
		return out
	}
	return nil
}

func hitsAny(s string, subs []string) bool {
	for _, m := range subs {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// errCodeCues 数字前出现这些词时才视为错误码。
var errCodeCues = []string{"错误码", "错误代码", "errno", "winerror", "error code", "wsa"}

// codeEnvelope 判断 s[j:k] 处的数字是否处于错误码语境：仅认 (10048) 与前置关键字两种形态。
func codeEnvelope(s string, j int) bool {
	left := strings.TrimRight(s[:j], " ")
	// 形态一：左括号（半角或全角）
	if strings.HasSuffix(left, "(") || strings.HasSuffix(left, "（") {
		return true
	}
	// 形态二：前置关键字
	for _, cue := range errCodeCues {
		if strings.HasSuffix(left, cue) {
			return true
		}
	}
	return false
}

// hitsCode 判断一行里是否出现独立且处于错误码语境的 Windows 错误码。
// 要求数字两侧均非字母数字，且被 codeEnvelope 认可；命中时不再要求 also。
func hitsCode(s string, codes []string) bool {
	for _, code := range codes {
		for i := 0; ; {
			j := strings.Index(s[i:], code)
			if j < 0 {
				break
			}
			j += i
			k := j + len(code)
			left := j == 0 || !isASCIIAlnum(s[j-1])
			right := k >= len(s) || !isASCIIAlnum(s[k])
			if left && right && codeEnvelope(s, j) {
				return true
			}
			i = j + 1
		}
	}
	return false
}

func isASCIIAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// hintLine 一行提示：key 供去重，text 为写入日志的内容。
type hintLine struct {
	key  string
	text string
}
