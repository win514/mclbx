package main

// punch.go 端口盲扫打洞（针对双对称 NAT）：两端同时扫描对端端口池，靠生日碰撞命中，每次更换本地端口；仅作诊断。

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func parsePortRange(s string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(s), "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("端口范围要写成 起始-结束，例如 30000-30099")
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || lo <= 0 || hi > 65535 || lo > hi {
		return 0, 0, fmt.Errorf("端口范围不合法：%s", s)
	}
	return lo, hi, nil
}

// punchAttempt 绑定新本地端口并将对端端口池扫一遍，返回是否打穿、对端地址与收发计数。
func punchAttempt(try int, peerIP net.IP, lo, hi int, tagLine string, localPort int, interval, perTry time.Duration) (bool, *net.UDPAddr, int, int64, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: localPort})
	if err != nil {
		return false, nil, 0, 0, fmt.Errorf("绑定本地端口失败：%w", err)
	}
	defer conn.Close()
	selfPort := conn.LocalAddr().(*net.UDPAddr).Port

	var gotProbe int64
	hit := make(chan *net.UDPAddr, 8)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if strings.HasPrefix(string(buf[:n]), tagLine+"-ack") {
				continue // 回执单独处理，不计入打穿
			}
			if strings.HasPrefix(string(buf[:n]), tagLine) {
				atomic.AddInt64(&gotProbe, 1)
				select {
				case hit <- src:
				default:
				}
			}
		}
	}()

	logf("第 %d 次尝试：本地端口 %d，对端 %s 端口 %d-%d", try, selfPort, peerIP, lo, hi)
	deadline := time.Now().Add(perTry)
	sent, round := 0, 0
	for time.Now().Before(deadline) {
		round++
		for p := lo; p <= hi; p++ {
			if _, err := conn.WriteToUDP([]byte(tagLine), &net.UDPAddr{IP: peerIP, Port: p}); err != nil {
				return false, nil, sent, atomic.LoadInt64(&gotProbe), fmt.Errorf("发送探针失败：%w", err)
			}
			sent++
		}
		select {
		case src := <-hit:
			logf("穿透成功：对端探针来自 %s，第 %d 轮，累计 %d 包", src, round, sent)
			_, _ = conn.WriteToUDP([]byte(tagLine+"-ack"), src)
			// 稍候收对方回执，确认双向连通
			bye := time.Now().Add(2 * time.Second)
			for time.Now().Before(bye) {
				select {
				case s := <-hit:
					logf("再次收到对端探针：%s，双向连通", s)
					bye = time.Now()
				case <-time.After(200 * time.Millisecond):
				}
			}
			time.Sleep(1 * time.Second) // 等对方读完回执
			return true, src, sent, atomic.LoadInt64(&gotProbe), nil
		case <-time.After(interval):
		}
	}
	return false, nil, sent, atomic.LoadInt64(&gotProbe), nil
}

func cmdPunch(args []string) error {
	fs := flag.NewFlagSet("punch", flag.ContinueOnError)
	peer := fs.String("peer", "", "对端的公网 IP")
	ports := fs.String("ports", "30000-30099", "要探测的对端端口范围，形如 30000-30099")
	tag := fs.String("tag", "mclbx", "口令：仅接受带此口令的探针，两端必须一致")
	localPort := fs.Int("local", 0, "本地绑定端口；0 = 每次尝试随机，推荐")
	interval := fs.Duration("interval", 300*time.Millisecond, "两轮探测之间的间隔")
	tries := fs.Int("tries", 3, "重试次数；每次更换一个新的本地端口")
	perTry := fs.Duration("per-try", 8*time.Second, "单次尝试的探测时长")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lo, hi, err := parsePortRange(*ports)
	if err != nil {
		return err
	}
	peerIP := net.ParseIP(strings.TrimSpace(*peer))
	if peerIP == nil || peerIP.To4() == nil {
		return fmt.Errorf("--peer 必须是 IPv4 地址，例如 10.99.1.11")
	}
	if *tries < 1 {
		return fmt.Errorf("--tries 至少 1")
	}

	tagLine := "MCLBX-PUNCH " + *tag
	fmt.Printf("==== mclbx punch · 端口探测（双对称 NAT）====\n")
	fmt.Printf("对端      : %s  端口池 %d-%d 共 %d 个\n", peerIP, lo, hi, hi-lo+1)
	fmt.Printf("口令      : %s\n", *tag)
	fmt.Printf("策略      : 每次尝试更换本地端口，单次成功率约 %d%%，共 %d 次\n",
		hitRate(hi-lo+1), *tries)
	fmt.Printf("            两端需同时运行；收到带口令的探针即判定连通\n\n")

	for try := 1; try <= *tries; try++ {
		ok, src, sent, probes, err := punchAttempt(try, peerIP, lo, hi, tagLine, *localPort, *interval, *perTry)
		if err != nil {
			return err
		}
		if ok {
			fmt.Printf("\n结论：双对称 NAT 下端口探测连通，第 %d 次尝试\n", try)
			fmt.Printf("      本次发了 %d 个探针；对端 NAT 为这个目的地映射到 %s\n", sent, src)
			fmt.Printf("      这一条探测结果证明两端之间存在可打穿的路径，作用是确认「这条路走得通」。\n")
			fmt.Printf("      但它不能直接交给内置直连去用：内置直连会自己选本地端口，换一个来源端口，\n")
			fmt.Printf("      对方 NAT 映射出来的外网端口就跟着变了，这条结果对不上。\n")
			fmt.Printf("      要真正用上它，需要让后续的加密通道复用同一个 socket，本版本还没有这条通路。\n")
			return nil
		}
		logf("第 %d 次尝试未连通，发送 %d 个探针，收到 %d 个，更换本地端口重试", try, sent, probes)
	}

	fmt.Printf("\n结论：双对称 NAT 下端口探测未连通\n")
	fmt.Printf("      %d 次尝试均未连通；按生日碰撞理论，单次约 %d%%，%d 次至少命中一次约 %d%%\n",
		*tries, hitRate(hi-lo+1), *tries, oneMinus(*tries, hitRate(hi-lo+1)))
	fmt.Printf("      建议：增大 --tries，或让两端同时开多个 socket 探测\n")
	return fmt.Errorf("端口探测未连通")
}

// hitRate 单次尝试的理论命中率（百分数）：池子 N 个端口、双方各扫 N 个 ⇒ ≈1-1/e
func hitRate(n int) int {
	if n <= 1 {
		return 100
	}
	p := 1 - 1/float64(n)
	acc := 1.0
	for i := 0; i < n; i++ {
		acc *= p
	}
	return int((1 - acc) * 100)
}

func oneMinus(tries, ratePercent int) int {
	p := float64(ratePercent) / 100
	acc := 1.0
	for i := 0; i < tries; i++ {
		acc *= (1 - p)
	}
	return int((1 - acc) * 100)
}
