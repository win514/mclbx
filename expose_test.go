package main

// 房间码门卫的归一化：走 SRV 时客户端填的是 SRV 目标名且结尾带点，比对前须去掉结尾的点。

import "testing"

func TestRoomCodeAccepted(t *testing.T) {
	cases := []struct {
		name string
		addr string
		room string
		want bool
	}{
		{"直接连、名字里带房间码", "abc123.host.example.com", "abc123", true},
		{"直接连、大小写不同", "ABC123.host.example.com", "abc123", true},
		{"走 SRV：填的是目标名（带结尾的点）", "node1.abc123.host.example.com.", "abc123", true},
		{"走 SRV：目标名里没有房间码", "node1.host.example.com.", "abc123", false},
		{"路过的扫描器", "www.example.com", "abc123", false},
		{"空地址", "", "abc123", false},
		{"前后有空格", "  abc123.host.example.com  ", "abc123", true},
	}
	for _, c := range cases {
		if got := roomCodeAccepted(c.addr, c.room); got != c.want {
			t.Errorf("%s：roomCodeAccepted(%q, %q) = %v，期望 %v", c.name, c.addr, c.room, got, c.want)
		}
	}
}
