package main

// firewall_test.go 测试防火墙放行命令的拼装逐字正确。

import (
	"strings"
	"testing"
)

func TestFirewallAddArgsIsExact(t *testing.T) {
	got := firewallAddArgs("mclbx-tcp-25565", 25565, "TCP", `C:\Tools\mclbx.exe`)
	want := []string{
		"advfirewall", "firewall", "add", "rule",
		"name=mclbx-tcp-25565", "dir=in", "action=allow",
		"protocol=TCP", "localport=25565",
		"program=" + `C:\Tools\mclbx.exe`, "profile=any", "enable=yes",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("放行命令拼错：\n得到 %v\n期望 %v", got, want)
	}
}

func TestFirewallProtoUpperCased(t *testing.T) {
	// netsh 认大写协议名，小写会被拒
	if a := firewallAddArgs("x", 1, "udp", "/p"); !contains(a, "protocol=UDP") {
		t.Errorf("协议名应当转成大写：%v", a)
	}
}

func TestFirewallRuleNameIsStableAndDeletable(t *testing.T) {
	n1 := firewallRuleName(25565, "TCP")
	n2 := firewallRuleName(25565, "tcp")
	if n1 != n2 {
		t.Errorf("大小写不同不该产生两个规则名：%q vs %q", n1, n2)
	}
	// 删除用的名字须与添加时完全一致，否则删不掉
	got := firewallDelArgs(n1)
	if len(got) != 5 || got[4] != "name="+n1 {
		t.Errorf("删除命令对不上：%v", got)
	}
	if !strings.HasPrefix(n1, "mclbx-") {
		t.Errorf("规则名要带前缀，方便用户自己认出来：%q", n1)
	}
}

func TestFirewallRulesForPortCoversBothProtocols(t *testing.T) {
	// 游戏要 TCP、ICE 打洞要 UDP，两条都要。
	got := firewallRulesForPort(25565)
	if len(got) != 2 {
		t.Fatalf("应当同时准备 TCP 与 UDP 两条：%v", got)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "tcp-25565") || !strings.Contains(joined, "udp-25565") {
		t.Errorf("规则名不对：%v", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestParsePortListAcceptsMultiple 测试 --port 支持逗号分隔的多个端口。
func TestParsePortListAcceptsMultiple(t *testing.T) {
	cases := []struct {
		spec string
		want []int
	}{
		{"25565", []int{25565}},
		{"25565,8090", []int{25565, 8090}},
		{"25565，8090", []int{25565, 8090}}, // 中文逗号也是逗号
		{" 25565 , 8090 ", []int{25565, 8090}},
		{"25565,25565,8090", []int{25565, 8090}}, // 重复的只留一条
	}
	for _, c := range cases {
		got, err := parsePortList(c.spec)
		if err != nil {
			t.Errorf("parsePortList(%q) 报错：%v", c.spec, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("parsePortList(%q) = %v，期望 %v", c.spec, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parsePortList(%q) = %v，期望 %v", c.spec, got, c.want)
				break
			}
		}
	}
}

func TestParsePortListRejectsGarbage(t *testing.T) {
	for _, spec := range []string{"", "  ", "abc", "0", "65536", "25565,,x", "-1"} {
		if _, err := parsePortList(spec); err == nil {
			t.Errorf("parsePortList(%q) 应当报错", spec)
		}
	}
}
