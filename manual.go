package main

import (
	_ "embed"
	"net/http"
	"strings"
)

// 说明书与程序同在一个 exe 内，页面本体是仓库根目录的 manual.html。
//
//go:embed manual.html
var manualHTML string

// manualThemeAttr 把 /manual?theme=… 翻成页面上的属性；高对比与深色共用一套，其余按浅色。
func manualThemeAttr(theme string) string {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "dark", "contrast":
		return ` data-theme="dark"`
	}
	return ""
}

// serveManual 输出说明书页面；界面服务只监听本机地址，不再单独鉴权。
func serveManual(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	page := strings.Replace(manualHTML, "@@MANUAL_THEME@@", manualThemeAttr(r.URL.Query().Get("theme")), 1)
	_, _ = w.Write([]byte(page))
}
