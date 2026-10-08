package main

// dns_test.go —— 报文编解码与「短名字 → 记录集」的单测：先按位对齐编解码，
// 再以端到端解析器查询收尾。

import (
	"encoding/binary"
	"net"
	"testing"
)

// 手工拼一个查询报文，用来喂给解析器
func buildTestQuery(id uint16, name string, typ uint16) []byte {
	var b []byte
	b = binary.BigEndian.AppendUint16(b, id)
	b = binary.BigEndian.AppendUint16(b, dnsFlagRD) // 只带 RD
	b = binary.BigEndian.AppendUint16(b, 1)         // QDCOUNT
	b = binary.BigEndian.AppendUint16(b, 0)
	b = binary.BigEndian.AppendUint16(b, 0)
	b = binary.BigEndian.AppendUint16(b, 0)
	b = append(b, encodeDNSName(name)...)
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, dnsClassIN)
	return b
}

func TestParseDNSQuery(t *testing.T) {
	pkt := buildTestQuery(0x1234, "_minecraft._tcp.room1.mclbx.test", dnsTypeSRV)
	q, err := parseDNSQuery(pkt)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if q.id != 0x1234 {
		t.Errorf("ID 不对：%#x", q.id)
	}
	if q.name != "_minecraft._tcp.room1.mclbx.test" {
		t.Errorf("名字不对：%q", q.name)
	}
	if q.typ != dnsTypeSRV {
		t.Errorf("类型不对：%d", q.typ)
	}
	if !q.rd {
		t.Error("RD 位应保留")
	}
}

func TestParseDNSQueryRejectsGarbage(t *testing.T) {
	cases := map[string][]byte{
		"太短":      {0x12, 0x34},
		"是响应不是查询": {0x12, 0x34, 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0, 0},
		"没有问题段":   {0x12, 0x34, 0x01, 0x00, 0, 0, 0, 0, 0, 0, 0, 0},
		"问题段被截断":  {0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'a', 'b'},
	}
	for name, pkt := range cases {
		if _, err := parseDNSQuery(pkt); err == nil {
			t.Errorf("%s：本应报错却通过了", name)
		}
	}
}

func TestDNSNameRoundTrip(t *testing.T) {
	for _, name := range []string{"mclbx.test", "room1.mclbx.test", "_minecraft._tcp.room1.mclbx.test"} {
		enc := encodeDNSName(name)
		got, off, err := readDNSName(enc, 0)
		if err != nil {
			t.Fatalf("读回 %s 失败：%v", name, err)
		}
		if got != name {
			t.Errorf("往返不一致：写入 %s，读回 %s", name, got)
		}
		if off != len(enc) {
			t.Errorf("%s：偏移不对，期望 %d 实际 %d", name, len(enc), off)
		}
	}
}

// 压缩指针：答案里的名字可能指回问题段。
func TestReadDNSNameCompression(t *testing.T) {
	pkt := buildTestQuery(1, "node1.mclbx.test", dnsTypeA)
	// 在报文尾部追加 "x." 与指向问题名的压缩指针
	pkt = append(pkt, 1, 'x', 0xC0, 0x0C)
	name, _, err := readDNSName(pkt, len(pkt)-4)
	if err != nil {
		t.Fatalf("解析压缩指针失败：%v", err)
	}
	if name != "x.node1.mclbx.test" {
		t.Errorf("压缩指针解出来不对：%q", name)
	}
}

func TestReadDNSNameRejectsLoop(t *testing.T) {
	// 指向自身的指针须被拦截，不能死循环
	pkt := make([]byte, 14)
	pkt[12] = 0xC0
	pkt[13] = 0x0C
	if _, _, err := readDNSName(pkt, 12); err == nil {
		t.Error("自指指针本应报错")
	}
}

func TestRecordsForRoom(t *testing.T) {
	ip := net.ParseIP("2001:db8:1111:2222::1")
	recs, err := recordsForRoom("room1.mclbx.test", ip, 25565, "", 30, "room=abc123")
	if err != nil {
		t.Fatalf("生成记录失败：%v", err)
	}
	var haveAddr, haveSRV, haveTXT bool
	for _, r := range recs {
		switch r.Type {
		case dnsTypeAAAA:
			haveAddr = r.Name == "room1.mclbx.test" && r.IP.Equal(ip)
		case dnsTypeSRV:
			haveSRV = r.Name == "_minecraft._tcp.room1.mclbx.test" &&
				r.Port == 25565 && r.Target == "room1.mclbx.test" && r.Weight == 5
		case dnsTypeTXT:
			haveTXT = r.Text == "room=abc123"
		}
	}
	if !haveAddr || !haveSRV || !haveTXT {
		t.Errorf("记录不齐：地址=%v SRV=%v TXT=%v（共 %d 条）", haveAddr, haveSRV, haveTXT, len(recs))
	}
}

// SRV 目标与名字不同时两个名字都要有地址记录，目标不能是 IP 字面量。
func TestRecordsForRoomWithDifferentTarget(t *testing.T) {
	ip := net.ParseIP("127.0.0.1")
	recs, err := recordsForRoom("room1.mclbx.test", ip, 25566, "node1.mclbx.test", 30, "")
	if err != nil {
		t.Fatalf("生成记录失败：%v", err)
	}
	addrNames := map[string]bool{}
	var srvTarget string
	var srvPort uint16
	for _, r := range recs {
		if r.Type == dnsTypeA {
			addrNames[r.Name] = true
		}
		if r.Type == dnsTypeSRV {
			srvTarget = r.Target
			srvPort = r.Port
		}
	}
	if !addrNames["room1.mclbx.test"] || !addrNames["node1.mclbx.test"] {
		t.Errorf("两个名字都应有地址记录，实际只有：%v", addrNames)
	}
	if srvTarget != "node1.mclbx.test" || srvPort != 25566 {
		t.Errorf("SRV 目标/端口不对：%s:%d", srvTarget, srvPort)
	}
	if net.ParseIP(srvTarget) != nil {
		t.Error("SRV 目标不能是 IP 字面量")
	}
}

func TestRecordsForRoomRejectsBadInput(t *testing.T) {
	if _, err := recordsForRoom("", net.ParseIP("127.0.0.1"), 25565, "", 30, ""); err == nil {
		t.Error("空名字本应报错")
	}
	if _, err := recordsForRoom("a.test", nil, 25565, "", 30, ""); err == nil {
		t.Error("没有地址本应报错")
	}
	if _, err := recordsForRoom("a.test", net.ParseIP("127.0.0.1"), 0, "", 30, ""); err == nil {
		t.Error("端口为 0 本应报错")
	}
}

// 从响应报文中解析答案。
type parsedAnswer struct {
	typ  uint16
	ip   net.IP
	port uint16
	tgt  string
	txt  string
}

func parseTestResponse(t *testing.T, resp []byte) []parsedAnswer {
	t.Helper()
	ancount := int(binary.BigEndian.Uint16(resp[6:8]))
	_, off, err := readDNSName(resp, 12)
	if err != nil {
		t.Fatalf("读问题段失败：%v", err)
	}
	off += 4
	var out []parsedAnswer
	for i := 0; i < ancount; i++ {
		if off+2 > len(resp) {
			t.Fatal("答案段被截断")
		}
		// 实现固定使用指向偏移 12 的压缩指针
		if resp[off] != 0xC0 {
			t.Fatalf("第 %d 条答案的名字不是压缩指针（首字节 %#x）", i, resp[off])
		}
		off += 2
		typ := binary.BigEndian.Uint16(resp[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(resp[off+8 : off+10]))
		rd := resp[off+10 : off+10+rdlen]
		a := parsedAnswer{typ: typ}
		switch typ {
		case dnsTypeA:
			a.ip = net.IP(append([]byte(nil), rd...))
		case dnsTypeAAAA:
			a.ip = net.IP(append([]byte(nil), rd...))
		case dnsTypeSRV:
			a.port = binary.BigEndian.Uint16(rd[4:6])
			tgt, _, err := readDNSName(rd, 6)
			if err != nil {
				t.Fatalf("读 SRV 目标失败：%v", err)
			}
			a.tgt = tgt
		case dnsTypeTXT:
			if len(rd) > 0 {
				a.txt = string(rd[1 : 1+int(rd[0])])
			}
		}
		out = append(out, a)
		off += 10 + rdlen
	}
	return out
}

func TestZoneHandleSRV(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	recs, err := recordsForRoom("room1.mclbx.test", net.ParseIP("127.0.0.1"), 25566, "node1.mclbx.test", 30, "")
	if err != nil {
		t.Fatal(err)
	}
	z.setRecords(recs)

	resp := z.handle(buildTestQuery(7, "_minecraft._tcp.room1.mclbx.test", dnsTypeSRV))
	if resp == nil {
		t.Fatal("没有回包")
	}
	if rcode := binary.BigEndian.Uint16(resp[2:4]) & 0x000F; rcode != dnsRcodeOK {
		t.Fatalf("rcode 应为 0，实际 %d", rcode)
	}
	if ip := binary.BigEndian.Uint16(resp[0:2]); ip != 7 {
		t.Errorf("回包 ID 应与请求一致，期望 7 实际 %d", ip)
	}
	answers := parseTestResponse(t, resp)
	if len(answers) != 1 {
		t.Fatalf("应有 1 条答案，实际 %d", len(answers))
	}
	if answers[0].typ != dnsTypeSRV || answers[0].port != 25566 || answers[0].tgt != "node1.mclbx.test" {
		t.Errorf("SRV 答案不对：%+v", answers[0])
	}
}

// 名字存在但无该类型：回 NOERROR + 0 条（不能是 NXDOMAIN）。
func TestZoneHandleNoDataIsNotNXDomain(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	recs, _ := recordsForRoom("room1.mclbx.test", net.ParseIP("127.0.0.1"), 25566, "", 30, "")
	z.setRecords(recs)

	resp := z.handle(buildTestQuery(8, "room1.mclbx.test", dnsTypeAAAA))
	rcode := binary.BigEndian.Uint16(resp[2:4]) & 0x000F
	if rcode != dnsRcodeOK {
		t.Errorf("名字存在但没这个类型时应回 NOERROR，实际 rcode=%d", rcode)
	}
	if ancount := binary.BigEndian.Uint16(resp[6:8]); ancount != 0 {
		t.Errorf("应回 0 条答案，实际 %d", ancount)
	}
}

func TestZoneHandleUnknownName(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	z.setRecords(nil)
	resp := z.handle(buildTestQuery(9, "nope.mclbx.test", dnsTypeA))
	if rcode := binary.BigEndian.Uint16(resp[2:4]) & 0x000F; rcode != dnsRcodeNXDomain {
		t.Errorf("不存在的名字应回 NXDOMAIN，实际 rcode=%d", rcode)
	}
}

// 区外且未配置上游的名字：拒答。
func TestZoneRefusesForeignName(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test", quiet: true}
	z.setRecords(nil)
	resp := z.handle(buildTestQuery(10, "www.example.com", dnsTypeA))
	if rcode := binary.BigEndian.Uint16(resp[2:4]) & 0x000F; rcode != dnsRcodeRefused {
		t.Errorf("区外名字应拒答，实际 rcode=%d", rcode)
	}
}

func TestZoneOwns(t *testing.T) {
	z := &dnsZone{origin: "mclbx.test"}
	cases := map[string]bool{
		"mclbx.test":                       true,
		"room1.mclbx.test":                 true,
		"_minecraft._tcp.room1.mclbx.test": true,
		"notmclbx.test":                    false,
		"mclbx.test.evil.com":              false,
	}
	for name, want := range cases {
		if got := z.owns(name); got != want {
			t.Errorf("owns(%q) = %v，期望 %v", name, got, want)
		}
	}
}
