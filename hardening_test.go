package main

// hardening_test.go 加固与边界用例。

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestUtf16zNeverReturnsEmptySlice(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通文本", "hello", "hello"},
		{"中文", "中文测试", "中文测试"},
		{"含 NUL 的日志行", "a\x00b", "ab"},
		{"整个都是 NUL", "\x00\x00", ""},
		{"空串", "", ""},
	}
	for _, c := range cases {
		got := utf16z(c.in)
		if len(got) == 0 {
			t.Fatalf("%s：utf16z 返回了空切片 —— 调用方要写 &buf[0]，这会直接 panic", c.name)
		}
		if got[len(got)-1] != 0 {
			t.Errorf("%s：结尾没有 NUL，Win32 的 -W 系列会读过头", c.name)
		}
		// 去掉结尾 NUL 后应等于剥掉 NUL 的原文。
		if s := string(utf16SliceToString(got)); s != c.want {
			t.Errorf("%s：得到 %q，期望 %q", c.name, s, c.want)
		}
	}
}

// utf16SliceToString 把 utf16z 的结果转回字符串（跳过结尾 NUL）
func utf16SliceToString(b []uint16) string {
	var sb strings.Builder
	for _, u := range b {
		if u == 0 {
			break
		}
		sb.WriteRune(rune(u))
	}
	return sb.String()
}

func TestTruncatedLogLineNeverSplitsAChar(t *testing.T) {
	// 11000 个汉字 = 33000 字节 > guiMaxLine(32768)，且 32768 不是 3 的倍数。
	long := strings.Repeat("汉", 11000)
	var lines []string
	pumpLines(strings.NewReader(long+"\n"), func(s string) { lines = append(lines, s) })

	if len(lines) != 1 {
		t.Fatalf("应当只产出一行，实际 %d 行", len(lines))
	}
	got := lines[0]
	if !strings.HasSuffix(got, guiLineCut) {
		t.Fatalf("超长行应当带上截断标记，实际尾部是 %q", tailOf(got, 24))
	}
	body := strings.TrimSuffix(got, guiLineCut)
	if !utf8.ValidString(body) {
		t.Fatalf("截断后的正文不是合法 UTF-8（把一个汉字劈成了两半）：尾部 %q", tailOf(body, 12))
	}
	if strings.ContainsRune(body, utf8.RuneError) {
		t.Fatalf("截断后的正文里出现了替换字符，说明切点没退到完整字符上：尾部 %q", tailOf(body, 12))
	}
	if n := utf8.RuneCountInString(body); n*3 != len(body) {
		t.Fatalf("正文长度 %d 字节 / %d 个字符，不是整数字符：说明还留着半个字符", len(body), n)
	}
}

func tailOf(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// guiRunningOn 探活时必须忽略重定向，否则会把别人的页面认成自己的界面。
func TestGuiRunningOnIgnoresRedirect(t *testing.T) {
	// 模拟 302 跳到含 key/group 字段名的页面。
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/fake", http.StatusFound)
	})
	mux.HandleFunc("/fake", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tasks":[{"key":"room","group":"开房间"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	if guiRunningOn(host, port) {
		t.Fatal("跟着 302 跳过去之后把别人的服务认成了自己的界面 —— 用户会看到「点了没反应」")
	}
}

func TestErrorHintsCoverWindowsCodesAndChineseText(t *testing.T) {
	cases := []struct {
		line string
		want string // 期望命中的 key（空串表示"不该有任何提示"）
	}{
		{"listen tcp 0.0.0.0:25565: bind: Only one usage of each socket address", "port-used"},
		{"bind: 通常每个套接字地址(协议/网络地址/端口)只允许使用一次。 (10048)", "port-used"},
		{"dial tcp 1.2.3.4:8090: connectex: 由于目标计算机积极拒绝，无法连接。", "refused"},
		{"connect: connection refused", "refused"},
		{"bind: An attempt was made to access a socket in a way forbidden (10013)", "port-forbidden"},
		{"read tcp: An existing connection was forcibly closed by the remote host (10054)", "reset"},
		{"由于系统缓冲区空间不足或队列已满，不能执行套接字上的操作 (10055)", "socket-buffer"},
		{"dial udp: i/o timeout", "timeout"},
		{"pion/turn: Allocate error: insufficient capacity", "turn-alloc"},
		// 只有错误码、没有原话时也要认。
		{"connectex 失败 (10061)", "refused"},
		{"wanroute: 错误码 10048", "port-used"},
		{"NatMap 返回：错误码 10013", "port-forbidden"},
		// 反面：日志里的无关数字不能被当成错误码。
		{"已转发 12310048 字节，耗时 5s", ""},
		{"房间 10013abc 已配对成功", ""},
		{"一切正常", ""},
		// 反面：数字独立但无「错误码」语境时不能命中。
		{"line-10013", ""},
		{"已转发 10048 字节，耗时 3ms", ""},
		{"监听端口 10048 已就绪", ""},
		{"耗时 10013 ms，重试 10054 次", ""},
	}
	for _, c := range cases {
		got := explainError(c.line)
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%q 不该有任何提示，却命中了 %q", c.line, got[0].key)
			}
			continue
		}
		if len(got) == 0 {
			t.Errorf("%q 应当命中 %q，实际没有任何提示", c.line, c.want)
			continue
		}
		if got[0].key != c.want {
			t.Errorf("%q 命中的是 %q，期望 %q", c.line, got[0].key, c.want)
		}
		if len(got) > 1 && !strings.Contains(got[1].text, "处理：") {
			t.Errorf("%q 的第二行应当带上处理：，实际 %q", c.line, got[1].text)
		}
	}
}

func TestVerifyTargetAcceptsEveryFormWeHandOut(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		host     string
		port     int
		room     string
		hasPort  bool
		wantFail bool
	}{
		{"IPv6 字面量带端口", "[2001:db8:3af0:3000::1]:25565", "2001:db8:3af0:3000::1", 25565, "", true, false},
		{"IPv4 带端口", "1.2.3.4:8090", "1.2.3.4", 8090, "", true, false},
		{"域名（走 SRV，端口按默认）", "room1.mclbx.test", "room1.mclbx.test", 25565, "", false, false},
		{"裸 IPv6（无端口）", "2001:db8:3af0:3000::1", "2001:db8:3af0:3000::1", 25565, "", false, false},
		{"中继链接", "mclbx://relay.example.com:8090?room=abc123", "relay.example.com", 8090, "abc123", true, false},
		{"空串", "", "", 0, "", false, true},
		{"端口越界", "1.2.3.4:99999", "", 0, "", false, true},
		{"既不像 host:port 也不像 IPv6", "1.2.3.4:abc", "", 0, "", false, true},
		// 只写端口会拆出空主机名，net.Dial(":25565") 连的是本机，必须拒掉。
		{"只写了端口", ":25565", "", 0, "", false, true},
		{"方括号里是空的", "[]:25565", "", 0, "", false, true},
		// 主机名里有空格必须拒掉。
		{"主机名里有空格", "1.2.3.4 :8090", "", 0, "", false, true},
		// 链接里的房间码解码后能含换行，会被回显到终端。
		{"链接里的房间码带换行", "mclbx://relay.example.com:8090?room=abc%0adef", "", 0, "", false, true},
		// 房间码大小写归一化。
		{"链接里的房间码大写", "mclbx://relay.example.com:8090?room=ABC123", "relay.example.com", 8090, "abc123", true, false},
	}
	for _, c := range cases {
		host, port, room, hasPort, err := verifyTarget(c.in)
		if c.wantFail {
			if err == nil {
				t.Errorf("%s：%q 应当被拒绝，却拆成了 %s:%d", c.name, c.in, host, port)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s：%q 应当能拆开，却报错 %v", c.name, c.in, err)
			continue
		}
		if host != c.host || port != c.port || room != c.room || hasPort != c.hasPort {
			t.Errorf("%s：拆成 %s / %d / room=%q / hasPort=%v，期望 %s / %d / room=%q / hasPort=%v",
				c.name, host, port, room, hasPort, c.host, c.port, c.room, c.hasPort)
		}
	}
}

// 中继空闲检测只在有玩家连着时生效。
func TestTcpTunnelIdleRuleOnlyFiresWhenSomeoneIsConnected(t *testing.T) {
	old := tcpIdleTimeout
	t.Cleanup(func() { tcpIdleTimeout = old })

	t.Run("有玩家连着→判定掉线并断开", func(t *testing.T) {
		tcpIdleTimeout = 300 * time.Millisecond
		relayAddr, stopRelay := startTestRelay(t)
		defer stopRelay()

		// 最小的游戏服务端：只接受连接并保持打开，不用 fakeGameServer。
		gameLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer gameLn.Close()
		hold := make(chan struct{})
		defer close(hold)
		go func() {
			for {
				gc, err := gameLn.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					<-hold
					_ = c.Close()
				}(gc)
			}
		}()

		done := make(chan struct{})
		go func() { _ = tcpTunnelHostRun(relayAddr, "halfopen", gameLn.Addr().String()); close(done) }()
		time.Sleep(200 * time.Millisecond)

		// 对端连中继、报同一房间、发一帧后不再发送（不关连接）。
		peer, err := net.DialTimeout("tcp", relayAddr, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = peer.Close() }()
		if _, err := fmt.Fprintf(peer, "%s%s\n", tcpHandshake, "halfopen"); err != nil {
			t.Fatal(err)
		}
		frame := []byte{tcpFrameOpen, 0, 0, 0, 1, 0, 0, 0, 0} // type=OPEN id=1 len=0
		if _, err := peer.Write(frame); err != nil {
			t.Fatal(err)
		}

		select {
		case <-done:
			// 正确：自己断开了，不会一直挂着
		case <-time.After(5 * time.Second):
			t.Fatal("房主侧没有在空闲超时后断开 —— 玩家拔网线会让它永远挂着（现象是「玩家还在线、日志不动」）")
		}
	})

	t.Run("没人连着→不许断", func(t *testing.T) {
		tcpIdleTimeout = 300 * time.Millisecond
		relayAddr, stopRelay := startTestRelay(t)
		defer stopRelay()

		done := make(chan struct{})
		go func() { _ = tcpTunnelHostRun(relayAddr, "nobody", "127.0.0.1:1"); close(done) }()
		select {
		case <-done:
			t.Fatal("还没有任何玩家进来就断开了 —— 这会把「开着等人进来」这个主要用法干掉")
		case <-time.After(1500 * time.Millisecond):
			// 正确：安静地等着（这时链路本来就没有数据）
		}
	})
}

// verify 第 4 步（服务器列表查询）的端到端与失败路径。
func TestVerifySlpQueryAgainstFakeServerAndClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go fakeSLPServe(c, "硬化测试用服务器", "1.21.11")
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	st, ms, err := slpQuery("127.0.0.1", port, "room1.mclbx.test", 5*time.Second)
	if err != nil {
		t.Fatalf("对假 SLP 服务器做状态查询失败：%v", err)
	}
	if st.Version.Name != "1.21.11" {
		t.Errorf("版本号应当是 1.21.11，实际 %q", st.Version.Name)
	}
	if ms < 0 {
		t.Errorf("耗时算出来是负数：%d", ms)
	}

	// 指向已关闭的端口，必须报错且不卡住。
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()
	_, deadPortStr, _ := net.SplitHostPort(deadAddr)
	deadPort, err := strconv.Atoi(deadPortStr)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, err := slpQuery("127.0.0.1", deadPort, "x", 2*time.Second); err == nil {
		t.Fatal("连一个没人听的端口居然成功了")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("失败得太慢（%v）—— 应该秒级返回", d)
	}
}
