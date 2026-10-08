package main

// guiwindow_test.go —— 「只能有一个窗口」的保证：能认出既有服务，且认出后不再起第二个实例。

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// 把 httptest 给的地址拆成 guiRunningOn 需要的 host / port
func splitTestAddr(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析测试地址失败：%v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("测试地址里没有端口：%q", raw)
	}
	return u.Hostname(), port
}

// 覆盖 hasExistingGuiWindow，使用例不受桌面真实窗口影响。
func withNoGuiWindow(t *testing.T) {
	t.Helper()
	old := hasExistingGuiWindow
	hasExistingGuiWindow = func() bool { return false }
	t.Cleanup(func() { hasExistingGuiWindow = old })
}

func TestGuiRunningOnRecognizesOurGui(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"key":"room","group":"开房间","name":"开一个房间"}]`))
	}))
	defer srv.Close()
	host, port := splitTestAddr(t, srv.URL)
	if !guiRunningOn(host, port) {
		t.Error("端口上返回的就是本程序的任务表，应当认得出来")
	}
}

// 端口被别的程序占用时不得认成已有 mclbx 实例。
func TestGuiRunningOnRejectsForeignServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()
	host, port := splitTestAddr(t, srv.URL)
	if guiRunningOn(host, port) {
		t.Error("别的程序返回的 JSON 里没有本程序的字段，不该被认成 mclbx")
	}
}

func TestGuiRunningOnRejectsNotFoundAndDeadPort(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	host, port := splitTestAddr(t, srv.URL)
	if guiRunningOn(host, port) {
		t.Error("404 不该被认成 mclbx")
	}

	// 一个没人监听的端口：应当立刻返回 false，不能卡住
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if guiRunningOn("127.0.0.1", dead) {
		t.Error("没人监听的端口不该被认成 mclbx")
	}
}

// 已有实例在跑时直接接手，不再起第二个。
func TestGuiReuseRunningHandsOverToExistingInstance(t *testing.T) {
	withNoGuiWindow(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"key":"room","group":"x"}]`))
	}))
	defer srv.Close()
	host, port := splitTestAddr(t, srv.URL)

	handled, err := guiReuseRunning(net.JoinHostPort(host, strconv.Itoa(port)), true, false)
	if !handled || err != nil {
		t.Fatalf("已经有一个在跑时应当接手：handled=%v err=%v", handled, err)
	}
}

// 没有在跑的实例：必须返回 handled=false，让调用方去起自己的服务
func TestGuiReuseRunningStartsFreshWhenNothingRunning(t *testing.T) {
	withNoGuiWindow(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 放开端口，模拟"这个端口空着"

	handled, err := guiReuseRunning(addr, false, false)
	if handled || err != nil {
		t.Fatalf("没有在跑的实例时应当返回 handled=false：handled=%v err=%v", handled, err)
	}
}

// 已有窗口时忽略端口内容，直接将窗口置前。
func TestGuiReuseRunningWithWindowFocusesIt(t *testing.T) {
	old := hasExistingGuiWindow
	hasExistingGuiWindow = func() bool { return true }
	t.Cleanup(func() { hasExistingGuiWindow = old })

	handled, err := guiReuseRunning("127.0.0.1:1", false, false)
	if !handled || err != nil {
		t.Fatalf("已经有窗口时应当接手：handled=%v err=%v", handled, err)
	}
}

// 页面须包含提示条 notice。
func TestGuiPageHasNoticeBar(t *testing.T) {
	for _, want := range []string{`id="notice"`, `id="noticeText"`, "notice=", ".notice{"} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("界面 HTML 里应当有 %q（它承担「把话说清楚」这件事）", want)
		}
	}
}
