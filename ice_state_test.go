package main

// ice_state_test.go 约束 ICE 状态回调只有一个注册点，且仅在 Failed 时通知。
// 生产代码中该接口只应出现在 newIceAgent 一处，其余经 iceStateCallback 复用。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pion/ice/v4"
)

func TestIceStateCallbackOnlyNotifiesOnFailed(t *testing.T) {
	fired := 0
	cb := iceStateCallback(func() { fired++ })

	for _, s := range []ice.ConnectionState{
		ice.ConnectionStateNew,
		ice.ConnectionStateChecking,
		ice.ConnectionStateConnected,
		ice.ConnectionStateCompleted,
		// Disconnected 可能自行恢复（pion 默认 5 秒无响应进入），不应触发重建
		ice.ConnectionStateDisconnected,
	} {
		cb(s)
	}
	if fired != 0 {
		t.Fatalf("Failed 之前的状态都不该触发重连，实际触发 %d 次", fired)
	}

	cb(ice.ConnectionStateFailed)
	if fired != 1 {
		t.Fatalf("Failed 应当触发一次，实际 %d 次", fired)
	}

	cb(ice.ConnectionStateClosed)
	if fired != 1 {
		t.Fatalf("Closed 不该再触发，实际 %d 次", fired)
	}

	// 一次性命令（ice host / ice guest）传 nil，不能因此崩溃
	iceStateCallback(nil)(ice.ConnectionStateFailed)
}

// 注册点必须只有一处，否则存在相互覆盖的隐性依赖。
func TestIceStateRegistrationHasExactlyOneSite(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("没有找到任何 .go 文件，用例的工作目录不对")
	}

	const needle = ".OnConnectionStateChange("
	total := 0
	var where []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue // 只看生产代码
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(src), needle); n > 0 {
			total += n
			where = append(where, fmt.Sprintf("%s(%d)", f, n))
		}
	}

	if total != 1 {
		t.Fatalf("ICE 状态回调的注册点应当只有 1 处（理由见 iceStateCallback 的注释），"+
			"实际 %d 处：%s\n  改法：不要在别处再注册，改用 iceStateCallback 传参",
			total, strings.Join(where, ", "))
	}
	if len(where) != 1 || !strings.HasPrefix(where[0], "ice.go(") {
		t.Fatalf("唯一的那处注册应当在 ice.go 里，实际：%s", strings.Join(where, ", "))
	}
}
