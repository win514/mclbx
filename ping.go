package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"strings"
	"time"
)

// ping.go —— 向目标发一次我的世界 Java 版服务器列表查询（SLP），用于确认链路真的连通。

type slpResponse struct {
	Version struct {
		Name string `json:"name"`
	} `json:"version"`
	Players struct {
		Online int `json:"online"`
		Max    int `json:"max"`
	} `json:"players"`
	Description json.RawMessage `json:"description"`
}

// 描述文本可能是纯字符串，也可能是 {"text":...,"extra":[...]} 组件
func describeText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Text  string            `json:"text"`
		Extra []json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		out := obj.Text
		for _, e := range obj.Extra {
			out += describeText(e)
		}
		return out
	}
	return string(raw)
}

func cmdPing(args []string) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	host := fs.String("host", "127.0.0.1", "目标地址，可为域名")
	port := fs.Int("port", 25565, "目标端口；显式指定时不查询 SRV，与真实客户端一致")
	name := fs.String("server-name", "", "握手包中填写的服务器地址，模拟玩家输入的名称")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	noSRV := fs.Bool("no-srv", false, "不查询 SRV 记录，直接按端口连接")
	resolver := fs.String("resolver", "", "使用指定的 DNS 服务器解析，例如 127.0.0.1:53；默认使用系统解析器")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 端口被显式指定时不查 SRV，直接连该端口。
	portSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})

	dialHost, dialPort := *host, *port
	if !portSet && !*noSRV && net.ParseIP(*host) == nil && !strings.Contains(*host, ":") {
		target, tport, note, err := resolveMinecraftSRV(*host, *resolver, *timeout)
		if err != nil {
			return err
		}
		fmt.Printf("  SRV 解析 : %s\n", note)
		if target != "" {
			dialHost, dialPort = target, tport
		}
	}
	// 指定解析器时，SRV 目标名的解析也走该解析器。
	if *resolver != "" && net.ParseIP(dialHost) == nil {
		ip, err := lookupIPWith(dialHost, *resolver, *timeout)
		if err != nil {
			return fmt.Errorf("解析 %s 失败：%w", dialHost, err)
		}
		fmt.Printf("  目标地址 : %s 解析为 %s\n", dialHost, ip)
		dialHost = ip.String()
	}

	addrField := *host
	if *name != "" {
		addrField = *name
	}

	// 在 SRV 解析之后判断端口，实际连接的是 dialPort。
	if note := bedrockPortNote(dialHost, dialPort); note != "" {
		return errors.New(note)
	}

	c, err := net.DialTimeout("tcp", net.JoinHostPort(dialHost, fmt.Sprintf("%d", dialPort)), *timeout)
	if err != nil {
		return fmt.Errorf("连接 %s:%d 失败：%w", dialHost, dialPort, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(*timeout))

	// 握手：包 ID=0，协议版本=-1（探测），地址字段填玩家输入的名字，nextState=1。
	st, ms, err := slpQueryOn(c, dialPort, addrField)
	if err != nil {
		return err
	}

	fmt.Printf("查询成功，%d ms，对端为我的世界 Java 版服务端\n", ms)
	fmt.Printf("  版本     : %s\n", st.Version.Name)
	fmt.Printf("  MOTD     : %s\n", describeText(st.Description))
	fmt.Printf("  在线人数 : %d / %d\n", st.Players.Online, st.Players.Max)
	return nil
}

// slpQuery 连上目标发一次服务器列表查询，返回对端服务器信息。
func slpQuery(host string, port int, addrField string, timeout time.Duration) (slpResponse, int64, error) {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), timeout)
	if err != nil {
		return slpResponse{}, 0, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	return slpQueryOn(c, port, addrField)
}

// slpQueryOn 在一条已经连上的连接上做握手 + 状态查询。
func slpQueryOn(c net.Conn, port int, addrField string) (slpResponse, int64, error) {
	var st slpResponse
	var hs []byte
	hs = appendVarint(hs, 0x00)
	hs = appendVarint(hs, -1)
	hs = appendVarint(hs, int32(len(addrField)))
	hs = append(hs, addrField...)
	hs = append(hs, byte(port>>8), byte(port))
	hs = appendVarint(hs, 0x01)
	if err := writeFrame(c, hs); err != nil {
		return st, 0, fmt.Errorf("发握手包失败：%w", err)
	}
	if _, err := c.Write([]byte{0x01, 0x00}); err != nil {
		return st, 0, fmt.Errorf("发状态请求失败：%w", err)
	}

	t0 := time.Now()
	body, err := readFrame(bufio.NewReader(c), 4<<20)
	if err != nil {
		return st, 0, fmt.Errorf("读响应失败，对端可能已断开：%w", err)
	}
	ms := time.Since(t0).Milliseconds()
	r := &bReader{b: body}
	id, err := r.varint()
	if err != nil {
		return st, ms, err
	}
	if id != 0x00 {
		return st, ms, fmt.Errorf("响应包 ID 异常：%d", id)
	}
	js, err := r.str()
	if err != nil {
		return st, ms, err
	}
	if err := json.Unmarshal([]byte(js), &st); err != nil {
		return st, ms, fmt.Errorf("解析状态 JSON 失败：%w", err)
	}
	return st, ms, nil
}
