package main

import (
	"bufio"
	"bytes"
	"testing"
)

// 握手包解析是门卫的安全边界：玩家输入的名字在其中，解析错误会导致校验失效。

func buildHandshake(proto int32, addr string, port uint16, next int32) []byte {
	var b []byte
	b = appendVarint(b, 0x00)
	b = appendVarint(b, proto)
	b = appendVarint(b, int32(len(addr)))
	b = append(b, addr...)
	b = append(b, byte(port>>8), byte(port&0xFF))
	b = appendVarint(b, next)
	return b
}

func TestParseHandshake(t *testing.T) {
	// 探测协议版本为 -1（5 字节 varint），地址为玩家输入的名字
	addr := "abc123.2001-db8-1111-2222-3333-4444-5555-6670.sslip.io"
	body := buildHandshake(-1, addr, 25565, 1)

	h, err := parseHandshake(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if h.Protocol != -1 {
		t.Errorf("协议版本 = %d，期望 -1", h.Protocol)
	}
	if h.Address != addr {
		t.Errorf("地址 = %q，期望 %q", h.Address, addr)
	}
	if h.Port != 25565 {
		t.Errorf("端口 = %d，期望 25565", h.Port)
	}
	if h.NextState != 1 {
		t.Errorf("nextState = %d，期望 1", h.NextState)
	}
}

func TestParseHandshakeRejectsGarbage(t *testing.T) {
	// 随机字节 / 错误的包 ID / 长度越界，都必须被拒绝而不是崩掉
	bad := [][]byte{
		{0x05, 0x01, 0x02},
		buildHandshake(-1, "x", 25565, 2)[:3],
		{0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F, 0x40},
	}
	for i, b := range bad {
		if h, err := parseHandshake(b); err == nil && h.Address != "" {
			t.Errorf("第 %d 组垃圾数据居然解析成功了: %+v", i, h)
		}
	}
}

func TestLoginStartUsername(t *testing.T) {
	var b []byte
	b = appendVarint(b, 0x00)
	name := "ethanacrter"
	b = appendVarint(b, int32(len(name)))
	b = append(b, name...)
	b = append(b, 0x00) // 后面还有 UUID，但门卫只取名字

	got, err := parseLoginStart(b)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != name {
		t.Errorf("玩家名 = %q，期望 %q", got, name)
	}
}

// 帧读写需原样往返（门卫读完后要把字节补发给服务端）。
func TestFrameRoundTrip(t *testing.T) {
	body := buildHandshake(-1, "abc123.example", 25570, 2)
	var buf bytes.Buffer
	if err := writeFrame(&buf, body); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	got, err := readFrame(bufio.NewReader(&buf), 1<<20)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("帧内容不一致：%d 字节 vs %d 字节", len(got), len(body))
	}
}
