package main

// dns.go 把「短名字 -> 地址+端口」写成 AAAA 与 SRV 记录，客户端查 _minecraft._tcp.<名字> 的 SRV 即可连接；含 DNS 编解码器与本地权威服务。

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	dnsTypeA    uint16 = 1
	dnsTypeTXT  uint16 = 16
	dnsTypeAAAA uint16 = 28
	dnsTypeSRV  uint16 = 33
	dnsClassIN  uint16 = 1

	dnsFlagQR uint16 = 1 << 15
	dnsFlagAA uint16 = 1 << 10
	dnsFlagRD uint16 = 1 << 8
	dnsFlagRA uint16 = 1 << 7

	dnsRcodeOK       uint16 = 0
	dnsRcodeServFail uint16 = 2
	dnsRcodeNXDomain uint16 = 3
	dnsRcodeNotImpl  uint16 = 4
	dnsRcodeRefused  uint16 = 5
)

func dnsTypeName(t uint16) string {
	switch t {
	case dnsTypeA:
		return "A"
	case dnsTypeAAAA:
		return "AAAA"
	case dnsTypeSRV:
		return "SRV"
	case dnsTypeTXT:
		return "TXT"
	}
	return fmt.Sprintf("TYPE%d", t)
}

func normalizeDNSName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	return strings.ToLower(s)
}

// readDNSName 读取域名，支持压缩指针。
func readDNSName(pkt []byte, off int) (string, int, error) {
	var labels []string
	next := 0
	hops := 0
	cur := off
	for {
		if cur >= len(pkt) {
			return "", 0, errors.New("DNS 名字越界")
		}
		b := pkt[cur]
		switch {
		case b == 0:
			cur++
			if next == 0 {
				next = cur
			}
			return strings.Join(labels, "."), next, nil
		case b&0xC0 == 0xC0:
			if cur+1 >= len(pkt) {
				return "", 0, errors.New("DNS 压缩指针被截断")
			}
			ptr := int(binary.BigEndian.Uint16(pkt[cur:cur+2]) & 0x3FFF)
			if next == 0 {
				next = cur + 2
			}
			hops++
			if hops > 16 {
				return "", 0, errors.New("DNS 压缩指针成环")
			}
			cur = ptr
		case b&0xC0 == 0:
			n := int(b)
			if cur+1+n > len(pkt) {
				return "", 0, errors.New("DNS 标签越界")
			}
			labels = append(labels, string(pkt[cur+1:cur+1+n]))
			cur += 1 + n
		default:
			return "", 0, errors.New("DNS 标签长度非法")
		}
	}
}

// encodeDNSName 编码域名，不压缩。
func encodeDNSName(name string) []byte {
	name = normalizeDNSName(name)
	out := make([]byte, 0, len(name)+2)
	if name != "" {
		for _, label := range strings.Split(name, ".") {
			if len(label) > 63 {
				label = label[:63]
			}
			out = append(out, byte(len(label)))
			out = append(out, label...)
		}
	}
	return append(out, 0)
}

type dnsQuery struct {
	id          uint16
	rd          bool
	name        string
	typ         uint16
	cls         uint16
	raw         []byte
	questionEnd int
}

func parseDNSQuery(pkt []byte) (*dnsQuery, error) {
	if len(pkt) < 12 {
		return nil, errors.New("DNS 报文太短")
	}
	q := &dnsQuery{id: binary.BigEndian.Uint16(pkt[0:2]), raw: pkt}
	flags := binary.BigEndian.Uint16(pkt[2:4])
	q.rd = flags&dnsFlagRD != 0
	if flags&dnsFlagQR != 0 {
		return nil, errors.New("这是响应报文，不是查询")
	}
	if binary.BigEndian.Uint16(pkt[4:6]) == 0 {
		return nil, errors.New("DNS 查询里没有问题段")
	}
	name, off, err := readDNSName(pkt, 12)
	if err != nil {
		return nil, err
	}
	if off+4 > len(pkt) {
		return nil, errors.New("DNS 查询被截断")
	}
	q.name = normalizeDNSName(name)
	q.typ = binary.BigEndian.Uint16(pkt[off : off+2])
	q.cls = binary.BigEndian.Uint16(pkt[off+2 : off+4])
	q.questionEnd = off + 4
	return q, nil
}

type dnsRecord struct {
	Name     string
	Type     uint16
	TTL      uint32
	IP       net.IP // A / AAAA
	Priority uint16 // SRV
	Weight   uint16 // SRV
	Port     uint16 // SRV
	Target   string // SRV
	Text     string // TXT
}

func (r dnsRecord) rdata() []byte {
	switch r.Type {
	case dnsTypeA:
		if v4 := r.IP.To4(); v4 != nil {
			return v4
		}
		return nil
	case dnsTypeAAAA:
		if r.IP.To4() == nil {
			if v6 := r.IP.To16(); v6 != nil {
				return v6
			}
		}
		return nil
	case dnsTypeSRV:
		b := make([]byte, 6)
		binary.BigEndian.PutUint16(b[0:2], r.Priority)
		binary.BigEndian.PutUint16(b[2:4], r.Weight)
		binary.BigEndian.PutUint16(b[4:6], r.Port)
		return append(b, encodeDNSName(r.Target)...)
	case dnsTypeTXT:
		t := r.Text
		if len(t) > 255 {
			t = t[:255]
		}
		return append([]byte{byte(len(t))}, t...)
	}
	return nil
}

func (r dnsRecord) describe() string {
	switch r.Type {
	case dnsTypeA, dnsTypeAAAA:
		return fmt.Sprintf("%s %s %s", r.Name, dnsTypeName(r.Type), r.IP)
	case dnsTypeSRV:
		return fmt.Sprintf("%s SRV 优先级=%d 权重=%d 端口=%d 目标=%s", r.Name, r.Priority, r.Weight, r.Port, r.Target)
	case dnsTypeTXT:
		return fmt.Sprintf("%s TXT %q", r.Name, r.Text)
	}
	return fmt.Sprintf("%s %s", r.Name, dnsTypeName(r.Type))
}

// buildAnswer 拼装响应：问题段原样回显，答案名用压缩指针指回问题段。
func buildAnswer(q *dnsQuery, recs []dnsRecord, rcode uint16, authoritative bool) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[0:2], q.id)
	flags := dnsFlagQR | dnsFlagRA | (rcode & 0x000F)
	if q.rd {
		flags |= dnsFlagRD
	}
	if authoritative {
		flags |= dnsFlagAA
	}
	binary.BigEndian.PutUint16(buf[2:4], flags)
	binary.BigEndian.PutUint16(buf[4:6], 1) // QDCOUNT
	binary.BigEndian.PutUint16(buf[6:8], uint16(len(recs)))
	buf = append(buf, q.raw[12:q.questionEnd]...)

	for _, r := range recs {
		rd := r.rdata()
		if rd == nil {
			continue
		}
		buf = append(buf, 0xC0, 0x0C) // NAME 指向偏移 12 的问题名
		var hdr [10]byte
		binary.BigEndian.PutUint16(hdr[0:2], r.Type)
		binary.BigEndian.PutUint16(hdr[2:4], dnsClassIN)
		binary.BigEndian.PutUint32(hdr[4:8], r.TTL)
		binary.BigEndian.PutUint16(hdr[8:10], uint16(len(rd)))
		buf = append(buf, hdr[:]...)
		buf = append(buf, rd...)
	}
	return buf
}

func addrRecord(name string, ip net.IP, ttl uint32) dnsRecord {
	if v4 := ip.To4(); v4 != nil {
		return dnsRecord{Name: normalizeDNSName(name), Type: dnsTypeA, TTL: ttl, IP: v4}
	}
	return dnsRecord{Name: normalizeDNSName(name), Type: dnsTypeAAAA, TTL: ttl, IP: ip}
}

// recordsForRoom 由名字、地址与端口生成整组记录：<名字> 的 A/AAAA、_minecraft._tcp.<名字> 的 SRV 与可选 TXT；SRV 目标须为域名（RFC 2782），目标名自身也要一条地址记录。
func recordsForRoom(name string, ip net.IP, port uint16, target string, ttl uint32, txt string) ([]dnsRecord, error) {
	name = normalizeDNSName(name)
	if name == "" {
		return nil, errors.New("名字不能为空")
	}
	if ip == nil {
		return nil, errors.New("没有可用的地址")
	}
	if target == "" {
		target = name
	}
	target = normalizeDNSName(target)
	if port == 0 {
		return nil, errors.New("端口不能为 0")
	}

	recs := []dnsRecord{addrRecord(name, ip, ttl)}
	if target != name {
		recs = append(recs, addrRecord(target, ip, ttl))
	}
	recs = append(recs, dnsRecord{
		Name: "_minecraft._tcp." + name, Type: dnsTypeSRV, TTL: ttl,
		Priority: 0, Weight: 5, Port: port, Target: target,
	})
	if txt != "" {
		recs = append(recs, dnsRecord{Name: name, Type: dnsTypeTXT, TTL: ttl, Text: txt})
	}
	return recs, nil
}

type dnsZone struct {
	origin   string // 本服务权威的区
	upstream string // 区外的名字转发给谁；留空则拒答
	quiet    bool

	mu   sync.RWMutex
	recs []dnsRecord
}

func (z *dnsZone) owns(name string) bool {
	o := normalizeDNSName(z.origin)
	if o == "" {
		return false
	}
	name = normalizeDNSName(name)
	return name == o || strings.HasSuffix(name, "."+o)
}

func (z *dnsZone) nameExists(name string) bool {
	name = normalizeDNSName(name)
	z.mu.RLock()
	defer z.mu.RUnlock()
	for _, r := range z.recs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (z *dnsZone) lookup(name string, typ uint16) []dnsRecord {
	name = normalizeDNSName(name)
	z.mu.RLock()
	defer z.mu.RUnlock()
	var out []dnsRecord
	for _, r := range z.recs {
		if r.Name == name && r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

func (z *dnsZone) setRecords(recs []dnsRecord) {
	z.mu.Lock()
	z.recs = recs
	z.mu.Unlock()
}

func (z *dnsZone) snapshot() []dnsRecord {
	z.mu.RLock()
	defer z.mu.RUnlock()
	out := make([]dnsRecord, len(z.recs))
	copy(out, z.recs)
	return out
}

// handle 为纯函数：输入查询报文，返回响应报文，便于单测。
func (z *dnsZone) handle(pkt []byte) []byte {
	q, err := parseDNSQuery(pkt)
	if err != nil {
		logf("[DNS] 忽略无法解析的报文：%v", err)
		return nil
	}

	if z.owns(q.name) {
		if q.cls != dnsClassIN {
			return buildAnswer(q, nil, dnsRcodeNotImpl, true)
		}
		recs := z.lookup(q.name, q.typ)
		if len(recs) == 0 {
			if z.nameExists(q.name) {
				// 名称存在但无该类型：回 NOERROR + 0 条，不能回 NXDOMAIN（否则会被缓存为名称不存在）。
				logf("[DNS] 查询 %s %s，本区存在该名称但无此类型，NOERROR/NODATA", q.name, dnsTypeName(q.typ))
				return buildAnswer(q, nil, dnsRcodeOK, true)
			}
			logf("[DNS] 查询 %s %s，本区无此名称，NXDOMAIN", q.name, dnsTypeName(q.typ))
			return buildAnswer(q, nil, dnsRcodeNXDomain, true)
		}
		logf("[DNS] 查询 %s %s，命中 %d 条", q.name, dnsTypeName(q.typ), len(recs))
		for _, r := range recs {
			logf("[DNS]      %s", r.describe())
		}
		return buildAnswer(q, recs, dnsRcodeOK, true)
	}

	if z.upstream == "" {
		logf("[DNS] 查询 %s %s，不在本区且未配置上游，拒答", q.name, dnsTypeName(q.typ))
		return buildAnswer(q, nil, dnsRcodeRefused, false)
	}
	resp, err := forwardDNS(pkt, z.upstream, 4*time.Second)
	if err != nil {
		logf("[DNS] 转发 %s 失败：%v", q.name, err)
		return buildAnswer(q, nil, dnsRcodeServFail, false)
	}
	if !z.quiet {
		logf("[DNS] 查询 %s %s，转发至上游 %s", q.name, dnsTypeName(q.typ), z.upstream)
	}
	return resp
}

func forwardDNS(pkt []byte, upstream string, timeout time.Duration) ([]byte, error) {
	c, err := net.DialTimeout("udp", withPort(upstream, "53"), timeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write(pkt); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func withPort(addr, def string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, def)
}

// serveDNS 同时监听 UDP 与 TCP（解析器在特殊情况下会退回 TCP）。
func serveDNS(addr string, z *dnsZone) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("DNS 监听 UDP %s 失败：%w", addr, err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, peer, err := pc.ReadFrom(buf)
			if err != nil {
				logf("[DNS] UDP 读失败：%v", err)
				return
			}
			pkt := append([]byte(nil), buf[:n]...)
			resp := z.handle(pkt)
			if resp == nil {
				continue
			}
			if _, err := pc.WriteTo(resp, peer); err != nil {
				logf("[DNS] 回包至 %s 失败：%v", peer, err)
			}
		}
	}()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logf("[DNS] TCP 监听失败，仅用 UDP 也可运行：%v", err)
		return nil
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				logf("[DNS] TCP 接受失败：%v", err)
				return
			}
			go handleDNSTCP(c, z)
		}
	}()
	return nil
}

func handleDNSTCP(c net.Conn, z *dnsZone) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	for {
		var l [2]byte
		if _, err := readFull(c, l[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(l[:]))
		if n <= 0 || n > 4096 {
			return
		}
		pkt := make([]byte, n)
		if _, err := readFull(c, pkt); err != nil {
			return
		}
		resp := z.handle(pkt)
		if resp == nil {
			return
		}
		var out []byte
		out = binary.BigEndian.AppendUint16(out, uint16(len(resp)))
		out = append(out, resp...)
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

func readFull(c net.Conn, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := c.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// resolveMinecraftSRV 查询 _minecraft._tcp.<名字> 的 SRV；target 为空表示未查到，调用方按默认端口处理；resolver 非空时用指定服务器。
func resolveMinecraftSRV(host, resolver string, timeout time.Duration) (string, int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r := resolverFor(resolver, timeout)
	_, addrs, err := r.LookupSRV(ctx, "minecraft", "tcp", host)
	if err != nil || len(addrs) == 0 {
		return "", 0, fmt.Sprintf("%s 无 _minecraft._tcp SRV 记录，使用默认端口", host), nil
	}
	pick := addrs[0]
	target := strings.TrimSuffix(pick.Target, ".")
	if target == "" {
		// RFC 2782：目标为单个 "." 表示该服务不提供
		return "", 0, fmt.Sprintf("%s 的 SRV 记录目标为 \".\"，表示该服务不提供", host), nil
	}
	note := fmt.Sprintf("%s 的 SRV 指向 %s:%d，优先级 %d，权重 %d", host, target, pick.Port, pick.Priority, pick.Weight)
	return target, int(pick.Port), note, nil
}

// resolverFor 在指定服务器时使用该服务器，否则使用系统解析器。
func resolverFor(server string, timeout time.Duration) *net.Resolver {
	if server == "" {
		return net.DefaultResolver
	}
	addr := withPort(server, "53")
	return &net.Resolver{
		PreferGo: true, // 必须开，否则自定义 Dial 不会被用上
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

// lookupIPWith 用指定解析器把名字解析为地址。
func lookupIPWith(host, server string, timeout time.Duration) (net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ips, err := resolverFor(server, timeout).LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s 未解析出地址", host)
	}
	return ips[0].IP, nil
}

func cmdDNS(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：mclbx dns local|publish [选项]")
	}
	switch args[0] {
	case "local":
		return cmdDNSLocal(args[1:])
	case "publish":
		return cmdDNSPublish(args[1:])
	}
	return fmt.Errorf("未知的 dns 子命令：%s，可选 local 或 publish", args[0])
}

// pickGlobalIPv6 选择一个稳定的全局 IPv6 地址。
func pickGlobalIPv6() net.IP {
	list := globalIPv6()
	for _, a := range list {
		if !a.Temporary {
			return a.IP
		}
	}
	if len(list) > 0 {
		return list[0].IP
	}
	return nil
}

func cmdDNSLocal(args []string) error {
	fs := flag.NewFlagSet("dns local", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:53", "监听地址，测试时将系统 DNS 指向该地址")
	zone := fs.String("zone", "mclbx.test", "本服务权威的域名区")
	name := fs.String("name", "room1.mclbx.test", "玩家需要输入的短名称")
	port := fs.Int("port", 25565, "SRV 记录的端口，即玩家实际连接的端口")
	target := fs.String("target", "", "SRV 的目标名称，默认与 --name 相同")
	addr := fs.String("addr", "", "地址记录指向的地址，默认自动选择本机公网 IPv6")
	upstream := fs.String("upstream", "", "区外名称的转发目标，填写原 DNS，例如 192.168.1.1")
	ttl := fs.Int("ttl", 30, "记录 TTL，单位秒")
	txt := fs.String("txt", "", "可选的 TXT 内容，例如房间信息")
	quiet := fs.Bool("quiet", false, "不打印转发日志")
	if err := fs.Parse(args); err != nil {
		return err
	}

	zname := normalizeDNSName(*zone)
	if zname == "" {
		return errors.New("--zone 不能为空")
	}
	qname := normalizeDNSName(*name)
	if !strings.HasSuffix(qname, "."+zname) && qname != zname {
		return fmt.Errorf("--name %s 必须位于 --zone %s 内，否则本服务无权应答", qname, zname)
	}
	if *port <= 0 || *port > 65535 {
		return fmt.Errorf("--port 不合法：%d", *port)
	}
	qtarget := normalizeDNSName(*target)
	if qtarget != "" && !strings.HasSuffix(qtarget, "."+zname) && qtarget != zname {
		return fmt.Errorf("--target %s 也必须位于 --zone %s 内", qtarget, zname)
	}

	var ip net.IP
	if *addr != "" {
		ip = net.ParseIP(*addr)
		if ip == nil {
			return fmt.Errorf("--addr 不是合法地址：%s", *addr)
		}
	} else {
		ip = pickGlobalIPv6()
		if ip == nil {
			return errors.New("本机未找到公网 IPv6，请用 --addr 指定地址")
		}
		logf("自动选择本机地址：%s", ip)
	}

	recs, err := recordsForRoom(qname, ip, uint16(*port), qtarget, uint32(*ttl), *txt)
	if err != nil {
		return err
	}
	z := &dnsZone{origin: zname, upstream: *upstream, quiet: *quiet}
	z.setRecords(recs)

	if err := serveDNS(*listen, z); err != nil {
		return err
	}

	fmt.Printf("==== mclbx dns local · 本地权威 DNS ====\n")
	fmt.Printf("监听        : %s\n", *listen)
	fmt.Printf("权威区      : %s\n", zname)
	if *upstream == "" {
		fmt.Printf("区外查询    : 拒答，如需同时访问外网，请加 --upstream <原 DNS>\n")
	} else {
		fmt.Printf("区外查询    : 转发至 %s\n", *upstream)
	}
	fmt.Printf("正在提供以下记录：\n")
	for _, r := range z.snapshot() {
		fmt.Printf("  %s\n", r.describe())
	}
	fmt.Printf("玩家名称    : %s   无需填写端口\n", qname)
	fmt.Printf("等待查询，客户端每次查询都会记录一行日志\n")
	select {}
}
