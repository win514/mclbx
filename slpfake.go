package main

// slpfake.go —— 排查用假服务端：只响应服务器列表查询，用 MOTD 区分客户端实际连的端口。

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"time"
)

func cmdSLPFake(args []string) error {
	fs := flag.NewFlagSet("slpfake", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:25566", "假服务端监听地址")
	motd := fs.String("motd", "MCLBX-FAKE-SLP", "返回的 MOTD，用于区分端口")
	versionName := fs.String("version", "mclbx-fake", "返回的版本名")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败：%w", *listen, err)
	}
	logf("假服务端已监听 %s，MOTD = %q；只响应服务器列表查询，不处理登录", *listen, *motd)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go fakeSLPServe(c, *motd, *versionName)
	}
}

func fakeSLPServe(c net.Conn, motd, versionName string) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(c)

	body, err := readFrame(r, 1<<20)
	if err != nil {
		logf("[假SLP] %s 读握手包失败：%v", c.RemoteAddr(), err)
		return
	}
	hs, err := parseHandshake(body)
	if err != nil {
		logf("[假SLP] %s 握手包解析失败：%v", c.RemoteAddr(), err)
		return
	}
	logf("[假SLP] 收到列表查询：来自 %s，地址 %q，端口 %d，nextState=%d",
		c.RemoteAddr(), hs.Address, hs.Port, hs.NextState)
	if hs.NextState != 1 {
		logf("[假SLP] %s 不是列表查询，nextState=%d；假服务端结束", c.RemoteAddr(), hs.NextState)
		return
	}
	if _, err := readFrame(r, 1<<20); err != nil {
		logf("[假SLP] %s 读状态请求失败：%v", c.RemoteAddr(), err)
		return
	}

	status := map[string]any{
		"version":     map[string]any{"name": versionName, "protocol": 774},
		"players":     map[string]any{"online": 7, "max": 99},
		"description": map[string]any{"text": motd},
	}
	js, err := json.Marshal(status)
	if err != nil {
		return
	}
	var resp []byte
	resp = appendVarint(resp, 0x00)
	resp = appendVarint(resp, int32(len(js)))
	resp = append(resp, js...)
	if err := writeFrame(c, resp); err != nil {
		logf("[假SLP] %s 回状态失败：%v", c.RemoteAddr(), err)
		return
	}
	logf("[假SLP] 已回状态给 %s：MOTD = %q", c.RemoteAddr(), motd)

	// 客户端通常会再发一个 ping（0x01 + i64），照原样回 pong
	if body, err := readFrame(r, 1<<20); err == nil && len(body) >= 9 && body[0] == 0x01 {
		var pong []byte
		pong = appendVarint(pong, 0x01)
		pong = append(pong, body[1:9]...)
		if err := writeFrame(c, pong); err != nil && err != io.EOF {
			logf("[假SLP] 回 pong 失败：%v", err)
		}
	}
}
