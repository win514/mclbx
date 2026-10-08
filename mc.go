package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// mc.go 解析我的世界 Java 版协议，仅取第一个握手包。
//
// 玩家在「直接连接」中输入的字符串会原样进入握手包的服务器地址字段。

// bReader 从缓冲区读取 varint（little-endian base-128）。
type bReader struct {
	b []byte
	i int
}

func (r *bReader) varint() (int32, error) {
	var v int32
	var shift uint
	for k := 0; k < 5; k++ {
		if r.i >= len(r.b) {
			return 0, io.ErrUnexpectedEOF
		}
		c := r.b[r.i]
		r.i++
		v |= int32(c&0x7F) << shift
		if c&0x80 == 0 {
			return v, nil
		}
		shift += 7
	}
	return 0, errors.New("varint 过长")
}

func (r *bReader) str() (string, error) {
	n, err := r.varint()
	if err != nil {
		return "", err
	}
	if n < 0 || int(n) > len(r.b)-r.i {
		return "", errors.New("字符串长度越界")
	}
	s := string(r.b[r.i : r.i+int(n)])
	r.i += int(n)
	return s, nil
}

func (r *bReader) u16() (uint16, error) {
	if r.i+2 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := uint16(r.b[r.i])<<8 | uint16(r.b[r.i+1])
	r.i += 2
	return v, nil
}

type handshake struct {
	Protocol  int32
	Address   string // 玩家输入的那串字
	Port      uint16
	NextState int32 // 1 = 服务器列表查询，2 = 登录
}

func parseHandshake(body []byte) (handshake, error) {
	var h handshake
	r := &bReader{b: body}
	id, err := r.varint()
	if err != nil {
		return h, err
	}
	if id != 0x00 {
		return h, errors.New("首个包不是握手包")
	}
	if h.Protocol, err = r.varint(); err != nil {
		return h, err
	}
	if h.Address, err = r.str(); err != nil {
		return h, err
	}
	if h.Port, err = r.u16(); err != nil {
		return h, err
	}
	if h.NextState, err = r.varint(); err != nil {
		return h, err
	}
	return h, nil
}

// parseLoginStart 读登录包中的玩家名（1.19+ 名字后有 UUID，这里只取名字）。
func parseLoginStart(body []byte) (string, error) {
	r := &bReader{b: body}
	id, err := r.varint()
	if err != nil {
		return "", err
	}
	if id != 0x00 {
		return "", errors.New("不是 Login Start 包")
	}
	return r.str()
}

// readFrame 从流中读一个带 varint 长度前缀的包，只返回包体。
func readFrame(m *bufio.Reader, maxLen int) ([]byte, error) {
	var n int32
	var shift uint
	for k := 0; k < 5; k++ {
		c, err := m.ReadByte()
		if err != nil {
			return nil, err
		}
		n |= int32(c&0x7F) << shift
		if c&0x80 == 0 {
			break
		}
		shift += 7
	}
	if n <= 0 || int(n) > maxLen {
		return nil, errors.New("包长度异常")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(m, b); err != nil {
		return nil, err
	}
	return b, nil
}

// appendVarint 将 int32 编成 varint 追加到缓冲区（-1 编成 5 字节）。
func appendVarint(b []byte, v int32) []byte {
	u := uint32(v)
	for {
		c := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			c |= 0x80
		}
		b = append(b, c)
		if u == 0 {
			return b
		}
	}
}

func writeFrame(w io.Writer, body []byte) error {
	n := len(body)
	var hdr []byte
	for {
		b := byte(n & 0x7F)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		hdr = append(hdr, b)
		if n == 0 {
			break
		}
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// 基岩版（Bedrock）识别：该版本走 UDP、默认端口 19132，与本工具的 Java TCP 协议不符。
//
// 该判断须放在可见用户所填端口处（如 ping / verify），不能放进 errhint 的文本匹配表。

// bedrockDefaultPort 为基岩版默认端口（Java 版默认 25565）。
const bedrockDefaultPort = 19132

// bedrockPortNote 在目标端口是基岩版默认端口时给出一句说明，否则返回空串。
func bedrockPortNote(host string, port int) string {
	if port != bedrockDefaultPort {
		return ""
	}
	target := fmt.Sprintf("%s:%d", host, port)
	if host == "" {
		target = fmt.Sprintf("端口 %d", port)
	}
	return fmt.Sprintf("这看起来是基岩版（Bedrock）：%s 是基岩版的默认端口。基岩版走 UDP、不监听 TCP，"+
		"而本工具只支持我的世界 Java 版（默认端口 25565），所以这一步不会成功。请确认服务端的版本与端口。", target)
}
