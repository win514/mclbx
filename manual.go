package main

import (
	_ "embed"
	"net/http"
	"strings"
)

// 说明书页面内嵌于 exe（manual.html）。
//
//go:embed manual.html
var manualHTML string

// manualThemeAttr 把 ?theme= 参数映射为页面属性（dark/contrast 共用深色）。
func manualThemeAttr(theme string) string {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "dark", "contrast":
		return ` data-theme="dark"`
	}
	return ""
}

// serveManual 输出说明书页面。
func serveManual(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	page := strings.Replace(manualHTML, "@@MANUAL_THEME@@", manualThemeAttr(r.URL.Query().Get("theme")), 1)
	_, _ = w.Write([]byte(page))
}
