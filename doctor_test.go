package main

// doctor_test.go —— 体检判断逻辑（纯函数）的回归测试。

import (
	"strings"
	"testing"
)

func wantFindings(t *testing.T, f docFacts, mustHave ...string) {
	t.Helper()
	var all strings.Builder
	for _, x := range judge(f) {
		all.WriteString(x.What)
		all.WriteString(" | ")
		all.WriteString(x.Why)
		all.WriteString(" | ")
		all.WriteString(x.Fix)
		all.WriteString("\n")
	}
	text := all.String()
	for _, m := range mustHave {
		if !strings.Contains(text, m) {
			t.Errorf("结论里应当提到 %q，实际结论：\n%s", m, text)
		}
	}
}

func TestDoctorSaysDirectWhenIPv6Exists(t *testing.T) {
	f := docFacts{OS: "windows", V6Global: true, NatShape: "锥形 NAT", GamePortFree: true}
	wantFindings(t, f, "公网直连可用")
	if got := recommend(f); !strings.Contains(got, "公网直连") {
		t.Errorf("有公网 IPv6 时应当推荐公网直连，实际：%s", got)
	}
}

func TestDoctorSuggestsNatmapWhenNoIPv6ButRouterCooperates(t *testing.T) {
	f := docFacts{OS: "windows", V6Global: false, NatShape: "锥形 NAT", UpnpOK: true, GamePortFree: true}
	wantFindings(t, f, "自动端口映射可用")
	if got := recommend(f); !strings.Contains(got, "自动端口映射") {
		t.Errorf("没有 IPv6 但能开洞时应当推荐自动端口映射，实际：%s", got)
	}
	// 不能同时报「直连不可用」与「映射可用」
	if strings.Contains(strings.Join(findingsText(f), " "), "公网直连不可用") {
		t.Error("路由器能开洞时不该说公网直连不可用")
	}
}

func TestDoctorWarnsWhenNothingWorks(t *testing.T) {
	f := docFacts{OS: "windows", V6Global: false, NatShape: "对称型 NAT（难打洞）", GamePortFree: true}
	wantFindings(t, f, "公网直连不可用", "NAT 为对称型", "中继")
	if got := recommend(f); !strings.Contains(got, "中继") {
		t.Errorf("什么都不行时应当推荐中继转发，实际：%s", got)
	}
}

func TestDoctorFlagsSymmetricNat(t *testing.T) {
	wantFindings(t, docFacts{V6Global: true, NatShape: "公网映射 1.2.3.4:36900（多端口不一致），疑似对称型 NAT（难打洞）"},
		"NAT 为对称型", "中继转发")
}

func TestDoctorFlagsMissingFirewallRule(t *testing.T) {
	f := docFacts{OS: "windows", V6Global: true, FwRules: 0, GamePort: 25565}
	wantFindings(t, f, "Windows 防火墙尚未配置入站规则", "mclbx firewall")
	// 已有规则时不再提示
	wantFindings(t, docFacts{OS: "windows", V6Global: true, FwRules: 2, GamePort: 25565}, "已配置 2 条")
	if hasFinding(docFacts{OS: "windows", V6Global: true, FwRules: 2, GamePort: 25565}, "尚未配置") {
		t.Error("已经有规则时不该再提示去放行")
	}
}

func TestDoctorDoesNotNagOnLinuxAboutWindowsFirewall(t *testing.T) {
	if hasFinding(docFacts{OS: "linux", V6Global: true, FwRules: 0}, "Windows 防火墙") {
		t.Error("非 Windows 平台不该提 Windows 防火墙")
	}
}

// IPv6 出口可达性

func TestDoctorWarnsWhenIPv6AddressExistsButOutboundIsDead(t *testing.T) {
	f := docFacts{OS: "windows", V6Global: true, V6Tried: true, NatShape: "锥形 NAT", GamePortFree: true}
	wantFindings(t, f, "IPv6 出口探测无应答", "公网直连可用")
	// 探测只证明「出去通」，不能据此判定入站不通，故仍推荐公网入口。
	if got := recommend(f); !strings.Contains(got, "公网直连") {
		t.Errorf("有 IPv6 地址时仍应推荐公网直连，实际：%s", got)
	}
}

func TestDoctorConfirmsIPv6WhenProbeReaches(t *testing.T) {
	f := docFacts{OS: "windows", V6Global: true, V6Tried: true, V6Reach: true,
		V6Out: "2001:db8::9", NatShape: "锥形 NAT", GamePortFree: true}
	wantFindings(t, f, "公网 IPv6 已验通", "2001:db8::9")
	if hasFinding(f, "IPv6 出口探测无应答") {
		t.Error("探通了就不该再报无应答")
	}
}

func TestDoctorDoesNotWarnWhenIPv6OutboundNotProbed(t *testing.T) {
	// 未探测过（--stun 关闭或本机无 IPv6）时不应报无应答
	f := docFacts{OS: "windows", V6Global: true, NatShape: "锥形 NAT", GamePortFree: true}
	if hasFinding(f, "IPv6 出口探测无应答") {
		t.Error("没探过 IPv6 出口时不该给「无应答」的警告")
	}
}

func TestDoctorFlagsBusyPort(t *testing.T) {
	wantFindings(t, docFacts{V6Global: true, GamePort: 25565, GamePortFree: false}, "已被占用")
}

func findingsText(f docFacts) []string {
	var out []string
	for _, x := range judge(f) {
		out = append(out, x.What+" "+x.Why+" "+x.Fix)
	}
	return out
}

func hasFinding(f docFacts, needle string) bool {
	return strings.Contains(strings.Join(findingsText(f), "\n"), needle)
}
