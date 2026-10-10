package main

// guiassets.go 界面资源：骨架、样式与脚本随构建装入后内嵌。
// 程序自身只做三件事：渲染基础页面、托管界面文件、提供接口。
// 页面表现、交互与动画全部由界面文件在浏览器里完成。
// 程序只把当前设置与任务状态写进页面属性与初始数据，展示方式由界面决定。
//
// 界面实现不随本项目提供：构建时把 gui.html、gui.css、gui.js 放入 frontend/ 后经
// //go:embed 嵌入；未放入时 renderGuiPage 改发说明页（见 guihtml.go）。
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

// 构建时装入界面资源的目录；仓库只提交一份说明文件。
//
//go:embed frontend
var guiFrontFS embed.FS

// guiFrontFile 读取 frontend/ 下的界面文件；不存在时返回 false。
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
// 保留拼好的字符串供排障使用；未装入界面文件时为空串。
var guiPageHTML = guiPageInlined()

// guiPageInlined 把骨架里的两处资源引用换回内联块。
func guiPageInlined() string {
	shell, ok := guiFrontFile("gui.html")
	if !ok {
		return ""
	}
	p := strings.Replace(shell, `<link rel="stylesheet" href="`+guiCSSPath+`">`,
		"<style>\n"+guiAssetString("gui.css")+"</style>", 1)
	return strings.Replace(p, `<script src="`+guiJSPath+`"></script>`,
		"<script>\n"+guiAssetString("gui.js")+"</script>", 1)
}
