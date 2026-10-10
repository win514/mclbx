package main

// ddns.go 把「短名字 -> 地址 + 端口」写入公网 DNS（dynv6），默认只预览不写入。

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const dynv6Base = "https://dynv6.com/api/v2"

// ddnsRecord 是要写入 DNS 的一条记录（域名服务商接口里的形式）。
type ddnsRecord struct {
	Name     string // 主机名，写全（例如 abc123.myname.dynv6.net）
	Type     string // AAAA / A / SRV / TXT
	Data     string // AAAA: 地址；SRV: 目标主机名；TXT: 文本
	Priority int
	Weight   int
	Port     int
}

func (r ddnsRecord) String() string {
	switch r.Type {
	case "SRV":
		return fmt.Sprintf("%s SRV 优先级=%d 权重=%d 端口=%d 目标=%s", r.Name, r.Priority, r.Weight, r.Port, r.Data)
	case "TXT":
		return fmt.Sprintf("%s TXT %q", r.Name, r.Data)
	}
	return fmt.Sprintf("%s %s %s", r.Name, r.Type, r.Data)
}

// ddnsRecordsForRoom 返回一个短名字 + 端口 + 地址对应的整组服务商记录。
func ddnsRecordsForRoom(host string, ip net.IP, port uint16, txt string) ([]ddnsRecord, error) {
	host = normalizeDNSName(host)
	if host == "" {
		return nil, errors.New("主机名不能为空")
	}
	if ip == nil {
		return nil, errors.New("没有可用的地址")
	}
	if port == 0 {
		return nil, errors.New("端口不能为 0")
	}
	var recs []ddnsRecord
	if v4 := ip.To4(); v4 != nil {
		recs = append(recs, ddnsRecord{Name: host, Type: "A", Data: v4.String()})
	} else {
		recs = append(recs, ddnsRecord{Name: host, Type: "AAAA", Data: ip.String()})
	}
	recs = append(recs, ddnsRecord{
		Name: "_minecraft._tcp." + host, Type: "SRV", Data: host,
		Priority: 0, Weight: 5, Port: int(port),
	})
	if txt != "" {
		recs = append(recs, ddnsRecord{Name: host, Type: "TXT", Data: txt})
	}
	return recs, nil
}

type dynv6Zone struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type dynv6Record struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Data     string `json:"data"`
	Port     int    `json:"port"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

type dynv6 struct {
	token  string
	zone   string
	dryRun bool
	http   *http.Client
}

func (p *dynv6) printRequest(method, path string, body any) {
	fmt.Printf("[预览] %s %s%s\n", method, dynv6Base, path)
	if body != nil {
		b, _ := json.Marshal(body)
		fmt.Printf("          %s\n", string(b))
	}
}

func (p *dynv6) do(method, path string, body any) ([]byte, error) {
	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if p.dryRun {
		p.printRequest(method, path, body)
		return nil, nil
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequest(method, dynv6Base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s 返回 %d：%s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (p *dynv6) zoneID() (int64, error) {
	data, err := p.do("GET", "/zones/by-name/"+url.PathEscape(p.zone), nil)
	if err != nil {
		return 0, err
	}
	if p.dryRun {
		return 0, nil
	}
	var z dynv6Zone
	if err := json.Unmarshal(data, &z); err != nil {
		return 0, fmt.Errorf("解析 zone 信息失败：%w，原始返回：%s", err, string(data))
	}
	if z.ID == 0 {
		return 0, errors.New("zoneID 为 0，请确认 --zone 是本人拥有的域名区")
	}
	return z.ID, nil
}

func (p *dynv6) listRecords(zoneID int64) ([]dynv6Record, error) {
	data, err := p.do("GET", fmt.Sprintf("/zones/%d/records", zoneID), nil)
	if err != nil {
		return nil, err
	}
	if p.dryRun {
		return nil, nil
	}
	var out []dynv6Record
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("解析记录列表失败：%w", err)
	}
	return out, nil
}

// ensure 更新已存在的记录，不存在则新建。
func (p *dynv6) ensure(zoneID int64, want ddnsRecord) error {
	existing, err := p.listRecords(zoneID)
	if err != nil {
		return err
	}
	name := normalizeDNSName(want.Name)
	for _, e := range existing {
		if normalizeDNSName(e.Name) == name && strings.EqualFold(e.Type, want.Type) {
			body := map[string]any{"data": want.Data}
			if want.Type == "SRV" {
				body["priority"] = want.Priority
				body["weight"] = want.Weight
				body["port"] = want.Port
			}
			_, err := p.do("PATCH", fmt.Sprintf("/zones/%d/records/%d", zoneID, e.ID), body)
			if err != nil {
				return err
			}
			logf("更新记录 %s", want.String())
			return nil
		}
	}
	body := map[string]any{"name": want.Name, "type": want.Type, "data": want.Data}
	if want.Type == "SRV" {
		body["priority"] = want.Priority
		body["weight"] = want.Weight
		body["port"] = want.Port
	}
	if _, err := p.do("POST", fmt.Sprintf("/zones/%d/records", zoneID), body); err != nil {
		return err
	}
	logf("新建记录 %s", want.String())
	return nil
}

func (p *dynv6) publish(recs []ddnsRecord) error {
	if p.dryRun {
		fmt.Printf("[预览] 以下为将要发出的请求，加 --apply 后才会实际写入：\n")
		fmt.Printf("[预览] 1) 先按名称查询 zoneID：\n")
		if _, err := p.zoneID(); err != nil {
			return err
		}
		fmt.Printf("[预览] 2) 再对每条记录执行更新或新建：\n")
		for _, r := range recs {
			if err := p.ensure(0, r); err != nil {
				return err
			}
		}
		return nil
	}
	zoneID, err := p.zoneID()
	if err != nil {
		return err
	}
	logf("区 %s 的 zoneID = %d", p.zone, zoneID)
	for _, r := range recs {
		if err := p.ensure(zoneID, r); err != nil {
			return err
		}
	}
	return nil
}

func cmdDNSPublish(args []string) error {
	fs := flag.NewFlagSet("dns publish", flag.ContinueOnError)
	provider := fs.String("provider", "dynv6", "域名服务商，目前支持 dynv6")
	zone := fs.String("zone", "", "本人拥有的域名区，例如 myname.dynv6.net")
	sub := fs.String("name", "", "区内的记录名或子域名，例如 abc123；留空表示使用区本身")
	port := fs.Int("port", 25565, "SRV 记录的端口，即玩家实际连接的端口")
	addr := fs.String("addr", "", "地址记录指向的地址，默认自动选择本机公网 IPv6")
	txt := fs.String("txt", "", "可选的 TXT 内容，例如房间信息")
	token := fs.String("token", os.Getenv("MCLBX_DDNS_TOKEN"), "接口令牌，也可使用环境变量 MCLBX_DDNS_TOKEN")
	apply := fs.Bool("apply", false, "实际写入；不加此参数仅打印预览")
	watch := fs.Duration("watch", 0, "按该间隔检查地址是否变化；0 表示仅写入一次，不做检查")
	if err := fs.Parse(args); err != nil {
		return err
	}

	zname := normalizeDNSName(*zone)
	if zname == "" {
		return errors.New("必须用 --zone 指定本人拥有的域名区，例如 myname.dynv6.net")
	}
	host := zname
	if s := normalizeDNSName(*sub); s != "" {
		host = s + "." + zname
	}
	if !*apply {
		fmt.Printf("==== mclbx dns publish，仅预览不写入 ====\n")
	} else {
		fmt.Printf("==== mclbx dns publish，实际写入 ====\n")
	}
	fmt.Printf("服务商      : %s\n", *provider)
	fmt.Printf("域名区      : %s\n", zname)
	fmt.Printf("玩家名称    : %s   无需填写端口\n", host)

	resolve := func() (net.IP, error) {
		if *addr != "" {
			ip := net.ParseIP(*addr)
			if ip == nil {
				return nil, fmt.Errorf("--addr 不是合法地址：%s", *addr)
			}
			return ip, nil
		}
		ip := pickGlobalIPv6()
		if ip == nil {
			return nil, errors.New("本机未找到公网 IPv6，请用 --addr 指定")
		}
		return ip, nil
	}

	ip, err := resolve()
	if err != nil {
		return err
	}
	logf("将发布地址：%s", ip)

	recs, err := ddnsRecordsForRoom(host, ip, uint16(*port), *txt)
	if err != nil {
		return err
	}
	fmt.Printf("将写入的记录：\n")
	for _, r := range recs {
		fmt.Printf("  %s\n", r.String())
	}

	if *provider != "dynv6" {
		return fmt.Errorf("暂不支持的服务商：%s", *provider)
	}
	if *apply && *token == "" {
		return errors.New("--apply 需要令牌：使用 --token 或设置环境变量 MCLBX_DDNS_TOKEN")
	}
	p := &dynv6{token: *token, zone: zname, dryRun: !*apply, http: &http.Client{Timeout: 15 * time.Second}}
	if err := p.publish(recs); err != nil {
		return err
	}

	if !*apply {
		fmt.Printf("\n以上仅为预览。确认无误后加 --apply 重新执行即可实际写入。\n")
		return nil
	}

	logf("写入完成。玩家在游戏的「直接连接」中输入：%s", host)
	if *watch <= 0 {
		return nil
	}

	// 监视模式：地址变化后重新发布。
	logf("进入监视模式：每 %s 检查一次地址", *watch)
	last := ip.String()
	for {
		time.Sleep(*watch)
		now, err := resolve()
		if err != nil {
			logf("重新获取地址失败：%v", err)
			continue
		}
		if now.String() == last {
			continue
		}
		logf("地址由 %s 变为 %s，重新发布", last, now)
		recs, err := ddnsRecordsForRoom(host, now, uint16(*port), *txt)
		if err != nil {
			logf("重新写入失败：%v", err)
			continue
		}
		if err := p.publish(recs); err != nil {
			logf("重新写入失败：%v", err)
			continue
		}
		last = now.String()
	}
}
