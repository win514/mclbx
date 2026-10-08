package main

// fuzz_test.go 对外部字节的解析入口做模糊测试。

import (
	"bufio"
	"bytes"
	"net"
	"testing"
	"unicode"
)

func FuzzParseHandshake(f *testing.F) {
	// 真样本：1.21.11 客户端的握手包（协议号 767）。
	f.Add([]byte{0x00, 0xff, 0x05, 0x09, 'r', 'o', 'o', 'm', '1', '.', 'm', 'c', 'l', 'b', 'x', '.', 't', 'e', 's', 't', 0x63, 0xdd, 0x02})
	f.Add([]byte{0x00})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		hs, err := parseHandshake(b)
		if err != nil {
			return
		}
		// 解析出的地址必须存在于原始字节里。
		if hs.Address != "" && !bytes.Contains(b, []byte(hs.Address)) {
			t.Fatalf("解析出的地址 %q 在原始字节里根本不存在（%x）", hs.Address, b)
		}
	})
}

func FuzzParseLoginStart(f *testing.F) {
	f.Add([]byte{0x00, 0x08, 'P', 'l', 'a', 'y', 'e', 'r', '0', '1'})
	f.Add([]byte{0x00})
	f.Add([]byte{0x01, 0x01, 'x'})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseLoginStart(b)
	})
}

// readFrame 读「长度前缀 + 包体」，长度来自攻击者。
func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0x05, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte{0x00})
	f.Add([]byte{0x80, 0x80, 0x80, 0x80, 0x08})       // 声明 2^31 附近
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x0f})       // 负数（-1）
	f.Add([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01}) // 6 字节 continuation
	f.Add([]byte{0x05, 'h'})                          // 声称 5 字节只给 1 字节
	f.Fuzz(func(t *testing.T, b []byte) {
		const maxLen = 1 << 20
		body, err := readFrame(bufio.NewReader(bytes.NewReader(b)), maxLen)
		if err != nil {
			return
		}
		if len(body) == 0 || len(body) > maxLen {
			t.Fatalf("readFrame 返回了长度 %d 的包体（maxLen=%d）—— 长度检查没兜住", len(body), maxLen)
		}
	})
}

func FuzzReadDNSName(f *testing.F) {
	f.Add([]byte{0x00}, 0)
	f.Add([]byte{0x03, 'a', 'b', 'c', 0x00}, 0)
	f.Add([]byte{0xc0, 0x00}, 0) // 自指压缩指针
	f.Add([]byte{0xc0, 0x02, 0xc0, 0x00}, 0)
	f.Add([]byte{0x3f}, 0) // 标签长度 63 但没有内容
	f.Add([]byte{0x40}, 0) // 非法标签类型（0x40）
	f.Fuzz(func(t *testing.T, pkt []byte, off int) {
		if off < 0 {
			return
		}
		name, next, err := readDNSName(pkt, off)
		if err != nil {
			return
		}
		if next < 0 || next > len(pkt) {
			t.Fatalf("readDNSName 返回的下一位置 %d 越界（pkt=%d 字节）", next, len(pkt))
		}
		_ = name
	})
}

func FuzzParseDNSQuery(f *testing.F) {
	f.Add(buildTestQuery(0x1234, "room1.mclbx.test", dnsTypeSRV))
	f.Add(buildTestQuery(1, "a", dnsTypeA))
	f.Add([]byte{})
	f.Add(make([]byte, 12))
	f.Fuzz(func(t *testing.T, pkt []byte) {
		q, err := parseDNSQuery(pkt)
		if err != nil {
			return
		}
		// 问题段的结束位置必须落在报文内。
		if q.questionEnd < 0 || q.questionEnd > len(pkt) {
			t.Fatalf("questionEnd=%d 越界（pkt=%d 字节）", q.questionEnd, len(pkt))
		}
	})
}

// FuzzDNSZoneHandle 覆盖解析查询 → 查表 → 拼应答的整条应答链路。
func FuzzDNSZoneHandle(f *testing.F) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	recs, err := recordsForRoom("room1.mclbx.test", net.ParseIP("127.0.0.1"), 25566, "node1.mclbx.test", 30, "txt")
	if err != nil {
		f.Fatal(err)
	}
	z.setRecords(recs)

	f.Add(buildTestQuery(0x1111, "room1.mclbx.test", dnsTypeSRV))
	f.Add(buildTestQuery(0x2222, "nope.mclbx.test", dnsTypeA))
	f.Add(buildTestQuery(0x3333, "room1.mclbx.test", 0x00ff)) // 无人认识的类型
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, pkt []byte) {
		resp := z.handle(pkt)
		if resp == nil {
			return
		}
		if len(resp) < 12 {
			t.Fatalf("应答比 DNS 头还短（%d 字节）", len(resp))
		}
		if len(pkt) >= 2 && !bytes.Equal(resp[0:2], pkt[0:2]) {
			t.Fatalf("应答的事务号 %x 和查询 %x 不一致 —— 客户端会丢掉这个响应", resp[0:2], pkt[0:2])
		}
	})
}

func FuzzParseMclbxLink(f *testing.F) {
	f.Add("mclbx://relay.example.com:8090?room=abc123")
	f.Add("mclbx+turn://user:pass@turn.example.com:3478?room=abc")
	f.Add("mclbx://[2001:db8::1]:8090?room=r&turn=turn:1.2.3.4:3478")
	f.Add("mclbx://h:8090?room=abc&room=def&sig=h:1")
	f.Add("")
	f.Add("mclbx://")
	f.Add("mclbx://h:99999?room=%00%0a%ff")
	f.Fuzz(func(t *testing.T, raw string) {
		plan, err := parseMclbxLink(raw)
		if err != nil {
			return
		}
		// 端口必须落在合法区间。
		for _, h := range plan.Hops {
			if h.Port < 1 || h.Port > 65535 {
				t.Fatalf("链接 %q 解析出端口 %d", raw, h.Port)
			}
		}
	})
}

func FuzzSplitQuery(f *testing.F) {
	f.Add("mclbx://h:1?room=abc")
	f.Add("mclbx://h:1?")
	f.Add("mclbx://h:1?a=%zz")
	f.Add("?")
	f.Add("mclbx://h:1?room=%00")
	f.Fuzz(func(t *testing.T, s string) {
		_, _, _ = splitQuery(s)
	})
}

func FuzzParseTurnEntry(f *testing.F) {
	f.Add("turn:turn.example.com:3478")
	f.Add("turns:turn.example.com:5349?transport=tcp")
	f.Add("turn:user:pass@h:3478")
	f.Add("turn:")
	f.Add(":")
	f.Fuzz(func(t *testing.T, s string) {
		_, _, _, _, _ = parseTurnEntry(s)
	})
}

func FuzzReadTcpFrame(f *testing.F) {
	f.Add([]byte{0x01, 0, 0, 0, 0x01, 0, 0, 0, 0x03, 'a', 'b', 'c'})
	f.Add([]byte{0x01, 0, 0, 0, 0x01, 0xff, 0xff, 0xff, 0xff}) // 声称 4GB
	f.Add(make([]byte, 9))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		typ, id, payload, err := readTcpFrame(bufio.NewReader(bytes.NewReader(b)))
		if err != nil {
			return
		}
		if len(payload) > tcpMaxFrame {
			t.Fatalf("帧体长度 %d 超过 tcpMaxFrame=%d", len(payload), tcpMaxFrame)
		}
		_ = typ
		_ = id
	})
}

func FuzzParseFingerprint(f *testing.F) {
	f.Add("00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff")
	f.Add("")
	f.Add("zz")
	f.Add("00:11")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseFingerprint(s)
	})
}

func FuzzNormalizeRoomCode(f *testing.F) {
	f.Add("room1")
	f.Add("ROOM1")
	f.Add("  room1  ")
	f.Add("room-1")
	f.Add("房间一")
	f.Add("room.1")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		got, err := normalizeRoomCode(s)
		if err != nil {
			return
		}
		if got == "" {
			t.Fatalf("空输入应当生成随机房间码，而不是返回空串")
		}
		for _, r := range got {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Fatalf("归一化后的房间码 %q 里有非法字符 %q", got, r)
			}
		}
	})
}

// FuzzGatewayParsers 覆盖 `route print` / `netstat -rn` / /proc/net/route 的解析。
func FuzzGatewayParsers(f *testing.F) {
	f.Add("          0.0.0.0          0.0.0.0      192.168.1.1     192.168.1.10     25")
	f.Add("0.0.0.0/0 via 192.168.1.1 dev eth0")
	f.Add("Kernel IP routing table\nDestination Gateway Genmask Flags\n0.0.0.0 192.168.1.1 0.0.0.0 UG")
	f.Add("Active Routes:\n  Network Destination  Netmask  Gateway  Interface  Metric\n  0.0.0.0  0.0.0.0  192.168.1.1  192.168.1.10  25")
	f.Add("\x00\xff\n\n")
	f.Fuzz(func(t *testing.T, out string) {
		_, _ = firstGatewayFromRoutePrint(out)
		_, _ = firstGatewayFromNetstat(out)
		_, _ = firstGatewayFromProcRoute(out)
	})
}

func FuzzParseXORMapped(f *testing.F) {
	f.Add([]byte{0x00, 0x01, 0x00, 0x08, 0x00, 0x01, 0x1e, 0x0c, 0x7f, 0x00, 0x00, 0x01}, []byte{})
	f.Add([]byte{}, make([]byte, 12))
	f.Fuzz(func(t *testing.T, buf []byte, txidRaw []byte) {
		// 事务号固定 12 字节，参数不支持定长数组故自行裁齐。
		var txid [12]byte
		copy(txid[:], txidRaw)
		ip, port, ok := parseXORMapped(buf, txid)
		if ok {
			if ip == nil {
				t.Fatal("ok=true 但 IP 是 nil")
			}
			if port < 0 || port > 65535 {
				t.Fatalf("端口 %d 越界", port)
			}
		}
	})
}

func FuzzDecodeMaybe(f *testing.F) {
	f.Add("abc")
	f.Add("%E4%B8%AD%E6%96%87")
	f.Add("%zz")
	f.Add("%")
	f.Fuzz(func(t *testing.T, s string) {
		_ = decodeMaybe(s)
	})
}

// FuzzVerifyTarget 覆盖 verify 子命令解析用户手输地址；拆出的 host/port 会直接用于连接与 DNS 查询。
func FuzzVerifyTarget(f *testing.F) {
	f.Add("room1.mclbx.test")
	f.Add("192.168.1.16:25565")
	f.Add("[2001:db8:3af0:3000::1]:25565")
	f.Add("2001:db8:3af0:3000::1")
	f.Add("node1.mclbx.test.")
	f.Add("mclbx://relay.example.com:8090?room=abc123")
	f.Add("mclbx://[2001:db8::1]:8090?room=r&room=def")
	f.Add("mclbx://h:8090?room=%00%0a%ff")
	f.Add(":25565")
	f.Add(":0")
	f.Add("[]:25565")
	f.Add("host:99999")
	f.Add("1.2.3.4 :8090")
	f.Add("   ")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		host, port, room, _, err := verifyTarget(raw)
		if err != nil {
			return
		}
		// 拆成功即用于 net.Dial/查 DNS：端口须合法，主机名须非空且无空白/控制字符。
		if port < 1 || port > 65535 {
			t.Fatalf("地址 %q 拆出端口 %d", raw, port)
		}
		if host == "" {
			t.Fatalf("地址 %q 拆成功但主机名是空的：后面 net.Dial(\":%d\") 连的是本机，"+
				"这个命令会对着一个用户没打算验的地址报「通了」", raw, port)
		}
		for _, r := range host {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				t.Fatalf("地址 %q 拆出的主机名里有空白/控制字符：%q", raw, host)
			}
		}
		// 房间码会被回显到终端，同样不能带控制字符。
		for _, r := range room {
			if unicode.IsControl(r) {
				t.Fatalf("地址 %q 拆出的房间码里有控制字符：%q", raw, room)
			}
		}
	})
}
