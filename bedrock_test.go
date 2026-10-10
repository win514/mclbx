package main

// bedrock_test.go 覆盖基岩版端口识别测试。

import (
	"strings"
	"testing"
)

func TestBedrockPortNoteOnlyForBedrockPort(t *testing.T) {
	if got := bedrockPortNote("127.0.0.1", 25565); got != "" {
		t.Errorf("Java 版默认端口不该给基岩版提示，却得到：%s", got)
	}
	if got := bedrockPortNote("127.0.0.1", 0); got != "" {
		t.Errorf("端口 0 不该给基岩版提示，却得到：%s", got)
	}

	got := bedrockPortNote("mc.example", bedrockDefaultPort)
	for _, want := range []string{"基岩版", "19132", "UDP", "Java 版", "25565"} {
		if !strings.Contains(got, want) {
			t.Errorf("提示里没有提到 %q：%s", want, got)
		}
	}
	// 主机名为空时提示不应出现缺主机的 ":19132" 写法
	if empty := bedrockPortNote("", bedrockDefaultPort); strings.Contains(empty, "：19132") ||
		!strings.Contains(empty, "端口 19132") {
		t.Errorf("主机名为空时的说法不对：%s", empty)
	}
}

// ping 必须在发起连接之前停下：本用例未起监听，真去连会报拒绝连接。
func TestPingStopsBeforeDialingOnBedrockPort(t *testing.T) {
	err := cmdPing([]string{"--host", "127.0.0.1", "--port", "19132"})
	if err == nil {
		t.Fatal("对 19132 居然没有报错")
	}
	if !strings.Contains(err.Error(), "基岩版") {
		t.Errorf("报的应当是基岩版，实际是：%v", err)
	}
	if strings.Contains(err.Error(), "连接") && strings.Contains(err.Error(), "失败") {
		t.Errorf("它去连了，说明判断放在了连接之后：%v", err)
	}
}

// verify 保留四步框架，将「连入口端口」这一步报成失败并附上说明。
func TestVerifyReportsBedrockPortAtStep3(t *testing.T) {
	err := cmdVerify([]string{"--addr", "127.0.0.1:19132"})
	if err == nil {
		t.Fatal("对 19132 居然没有报错")
	}
	if !strings.Contains(err.Error(), "基岩版") {
		t.Errorf("报的应当是基岩版，实际是：%v", err)
	}
}
