package main

// guiassets.go 界面资源：骨架、样式与脚本为独立文件，随构建装入后内嵌。
// Go 侧只做三件事：渲染基础 HTML 模板、托管静态资源、提供业务接口。
// 它不操作 DOM、不参与动画逻辑；动效、交互、状态过渡、rAF 全在前端文件里由浏览器执行。
// 服务端只把业务状态（当前设置、任务状态）写进页面属性与首屏 JSON，其界面表现由浏览器决定。
//
// 前端实现不在本仓库：构建时由 build-release.ps1 从 D:\mclbx-frontend 装入 frontend/，
// 再经 //go:embed 嵌入。仓库只提交 frontend/说明.txt，故新克隆构建出的程序不含界面，
// renderGuiPage 会改发占位页（见 guihtml.go）。
// 骨架里的 <link> / <script src> 是样式与脚本这两份资源的唯一入口。

import (
	"embed"
	"strings"
)

const (
	// 两个路径同时出现在页面骨架里，改一处必须改两处。
	guiCSSPath = "/assets/gui.css"
	guiJSPath  = "/assets/gui.js"
)

// 构建时装入前端资源的目录。仓库只提交 frontend/说明.txt，其余内容由 .gitignore 挡住，
// 因此新克隆的仓库里只有说明文件，构建时装入 gui.html / gui.css / gui.js 后再嵌入。
//
//go:embed frontend
var guiFrontFS embed.FS

// guiFrontFile 读取 frontend/ 下的前端文件；不存在时返回 false。
func guiFrontFile(name string) (string, bool) {
	b, err := guiFrontFS.ReadFile("frontend/" + name)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// guiAssetString 读出 frontend/ 下的前端文件；缺失时返回空串。
func guiAssetString(name string) string {
	s, _ := guiFrontFile(name)
	return s
}

// guiPageHTML 是把两份外链资源摊平回页面后的完整源码：骨架 + 样式 + 脚本。
//
// 生产路径不用它：服务端只发骨架，样式与脚本由浏览器各自到 /assets/ 取。
// 保留拼好的字符串供用例与排障使用；未装入前端时为空串。
var guiPageHTML = guiPageFlat()

// guiPageFlat 把骨架里的两处资源引用换回内联块。
func guiPageFlat() string {
	shell, ok := guiFrontFile("gui.html")
	if !ok {
		return ""
	}
	p := strings.Replace(shell, `<link rel="stylesheet" href="`+guiCSSPath+`">`,
		"<style>\n"+guiAssetString("gui.css")+"</style>", 1)
	return strings.Replace(p, `<script src="`+guiJSPath+`"></script>`,
		"<script>\n"+guiAssetString("gui.js")+"</script>", 1)
}
