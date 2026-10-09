package main

// lab.go 内置 STUN(/TURN) 服务端：提供公网映射探测（srflx 候选），TURN 为可选中转。

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TURN REST 凭据的算法规定用 HMAC-SHA1
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pion/turn/v5"
)

func cmdStun(args []string) error {
	fs := flag.NewFlagSet("stun", flag.ContinueOnError)
	port := fs.Int("port", 3478, "监听端口，UDP")
	listen := fs.String("listen", "0.0.0.0", "监听地址；多网卡或多公网 IP 的机器可只绑定其中一个")
	withTurn := fs.Bool("turn", false, "同时开放 TURN 中继服务，需要指定中继地址")
	realm := fs.String("realm", "mclbx", "TURN realm")
	user := fs.String("user", "mclbx", "TURN 用户名")
	pass := fs.String("pass", "mclbx", "TURN 密码")
	restSecret := fs.String("rest", "", "改用 REST 临时凭据：不再下发固定密码，客户端按 <过期时间>:<用户名> 与共享密钥计算 HMAC 密码")
	relayIP := fs.String("relay-ip", "", "TURN 中继地址；不填则取本机第一个非回环 IPv4")
	publicIP := fs.String("public-ip", "", "--relay-ip 的别名：网卡为内网地址、EIP 为公网地址的云主机更适用")
	portRange := fs.String("relay-port-range", "", "限制中继端口段，例如 49160-49200；不限制则端口不固定，防火墙无法放行")
	udpBuf := fs.Int("udp-buffer", 4<<20, "UDP 收发缓冲区（字节）；高带宽中继时调大可少丢包，0 表示用系统默认")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *publicIP != "" {
		*relayIP = *publicIP
	}

	pc, err := net.ListenPacket("udp4", net.JoinHostPort(*listen, strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("监听 UDP %s:%d 失败：%w", *listen, *port, err)
	}
	udpBufNote := setUDPBuffers(pc, *udpBuf)

	var minPort, maxPort uint16
	if *portRange != "" {
		lo, hi, err := parsePortRange(*portRange)
		if err != nil {
			return fmt.Errorf("--relay-port-range 无效：%w", err)
		}
		minPort, maxPort = uint16(lo), uint16(hi)
	}

	cfg := turn.ServerConfig{
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: pc}},
	}
	relay := ""
	if *withTurn {
		relay = *relayIP
		if relay == "" {
			relay = firstNonLoopbackIPv4()
		}
		if relay == "" {
			return fmt.Errorf("--turn 需要中继地址，但未找到非回环 IPv4，请用 --relay-ip 指定")
		}
		cfg.Realm = *realm
		if *restSecret != "" {
			cfg.AuthHandler = restAuthHandler(*restSecret, *realm)
		} else {
			key := turn.GenerateAuthKey(*user, *realm, *pass)
			cfg.AuthHandler = func(ra *turn.RequestAttributes) (string, []byte, bool) {
				if ra.Username != *user {
					// 拒绝时记录日志：客户端只会看到 400/401
					logf("拒绝：账号不匹配，收到 %q，期望 %q", ra.Username, *user)
					return "", nil, false
				}
				return ra.Username, key, true
			}
		}
		if minPort != 0 {
			// 指定端口段：中继 socket 仅在该范围内分配
			cfg.PacketConnConfigs[0].RelayAddressGenerator = &turn.RelayAddressGeneratorPortRange{
				RelayAddress: net.ParseIP(relay),
				Address:      relay,
				MinPort:      minPort,
				MaxPort:      maxPort,
			}
		} else {
			cfg.PacketConnConfigs[0].RelayAddressGenerator = &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP(relay),
				// 中继 socket 也绑定该地址，避免多网卡下源地址不一致
				Address: relay,
			}
		}
	}

	s, err := turn.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("启动 STUN/TURN 失败：%w", err)
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s · STUN / TURN 服务端\n", version)
	fmt.Printf("================================================\n")
	fmt.Printf(" 监听     : udp %s:%d\n", *listen, *port)
	if udpBufNote != "" {
		fmt.Printf(" UDP 缓冲 : %s\n", udpBufNote)
	}
	if *withTurn {
		fmt.Printf(" TURN     : realm=%s  中继地址=%s\n", *realm, relay)
		if *restSecret != "" {
			fmt.Printf(" 凭据     : REST 临时凭据，共享密钥已设置，不写入日志\n")
		} else {
			fmt.Printf(" 凭据     : 静态账号 %s / %s\n", *user, *pass)
		}
		if minPort != 0 {
			fmt.Printf(" 中继端口 : 仅分配 %d-%d，防火墙开放该段即可\n", minPort, maxPort)
		} else {
			fmt.Printf(" 中继端口 : 随机，每次范围不同；建议用 --relay-port-range 限制\n")
		}
		// 直接打印房主端应填写的链接
		fmt.Printf("------------------------------------------------\n")
		fmt.Printf(" 房主端把下面这一行填进「中继服务器」即可：\n")
		if *restSecret != "" {
			fmt.Printf("   --relay-server turn:%s:%d?secret=%s\n",
				relay, *port, escCred(*restSecret))
		} else {
			fmt.Printf("   --relay-server turn:%s:%s@%s:%d\n",
				escCred(*user), escCred(*pass), relay, *port)
		}
		fmt.Printf(" 校验可用性：mclbx relaycheck <上面那一行>\n")
		fmt.Printf("------------------------------------------------\n")
		fmt.Printf(" 需要开放的端口：UDP %d", *port)
		if minPort != 0 {
			fmt.Printf(" + UDP %d-%d 中继", minPort, maxPort)
		}
		fmt.Printf("\n")
	} else {
		fmt.Printf(" 说明     : 只报告对方看到的映射，不转发流量\n")
	}
	fmt.Printf(" 客户端侧 : 把上面那一行填进 --relay-server，或设置环境变量 MCLBX_RELAY\n")
	fmt.Printf(" Ctrl+C 退出。\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	return s.Close()
}

// setUDPBuffers 尝试扩大 UDP socket 收发缓冲，返回说明文本（空串表示未改动）。
// 仅作用于监听 socket；TURN 中继 socket 由 pion 内部创建，无法调整。
func setUDPBuffers(pc net.PacketConn, want int) string {
	if want <= 0 {
		return ""
	}
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		return ""
	}
	mib := func(n int) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
	rerr := uc.SetReadBuffer(want)
	werr := uc.SetWriteBuffer(want)
	if rerr != nil || werr != nil {
		return fmt.Sprintf("申请 %s 失败（读=%v，写=%v），沿用系统默认", mib(want), rerr, werr)
	}
	return fmt.Sprintf("收发各申请 %s（系统可能按自身上限调整）", mib(want))
}

// restAuthHandler 按 TURN REST 约定校验临时凭据。
// 用户名为 <过期时间戳>:<名字>，密码为 base64(HMAC-SHA1(共享密钥, 用户名))。
func restAuthHandler(secret, realm string) turn.AuthHandler {
	return func(ra *turn.RequestAttributes) (string, []byte, bool) {
		tsPart, _, ok := strings.Cut(ra.Username, ":")
		if !ok {
			logf("拒绝：临时凭据格式不合法，应为 <过期时间戳>:<名字>，收到 %q", ra.Username)
			return "", nil, false
		}
		ts, err := strconv.ParseInt(tsPart, 10, 64)
		if err != nil {
			logf("拒绝：临时凭据的时间戳不是整数：%q", ra.Username)
			return "", nil, false
		}
		if ts < time.Now().Unix() {
			logf("拒绝：临时凭据已过期，凭据时间 %d，当前时间 %d", ts, time.Now().Unix())
			return "", nil, false
		}
		mac := hmac.New(sha1.New, []byte(secret))
		_, _ = mac.Write([]byte(ra.Username))
		pass := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		// 密码错误也走到这里，由库比对消息完整性并返回状态码
		return ra.Username, turn.GenerateAuthKey(ra.Username, realm, pass), true
	}
}

// cmdMailbox 启动信令会合点，供房主被 NAT 挡住时双方交换地址。
func cmdMailbox(args []string) error {
	fs := flag.NewFlagSet("mailbox", flag.ContinueOnError)
	listen := fs.String("listen", defaultListenAddr, "监听地址；这是对外服务，默认双栈（IPv4 与 IPv6 都收）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mb := newMailbox()
	srv := &http.Server{Addr: *listen, Handler: mb, ReadHeaderTimeout: 5 * time.Second}
	// 通配监听地址（:8090）不能直接拼进 URL，这里改为占位写法
	shown := *listen
	if h, p, err := net.SplitHostPort(*listen); err == nil {
		if h == "" || h == "0.0.0.0" || h == "::" {
			shown = "<本机地址>:" + p
		}
	}
	fmt.Printf("信令会合点已就绪：http://%s/blob/<名字>   列表：GET /blobs\n", shown)
	fmt.Printf("监听地址       : %s（IPv4 与 IPv6 都收）\n", *listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// firstNonLoopbackIPv4 返回本机第一个非回环 IPv4 地址。
func firstNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					return ip4.String()
				}
			}
		}
	}
	return ""
}
