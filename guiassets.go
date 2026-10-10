package main

// guiassets.go 界面资源：样式与脚本为独立文件，随 exe 内嵌并由本程序托管。
// Go 侧只做三件事：渲染基础 HTML 模板、托管静态资源、提供业务接口。
// 它不操作 DOM、不参与动画逻辑；动效、交互、状态过渡、rAF 全在 assets/gui.css 与
// assets/gui.js 里由浏览器执行。服务端只把业务状态（当前设置、任务状态）写进页面
// 属性与首屏 JSON，其界面表现由浏览器决定。
// 骨架里的 <link> / <script src> 是这两个文件的唯一入口，见 guihtml.go。

import "strings"

const (
	// 两个路径同时出现在 guihtml.go 的骨架里，改一处必须改两处（有用例，见 gui_assets_test.go）。
	guiCSSPath = "/assets/gui.css"
	guiJSPath  = "/assets/gui.js"
)

// guiPageHTML 是把两份外链资源摊平回页面后的完整源码：骨架 + 样式 + 脚本。
//
// 生产路径不用它：服务端只发骨架，样式与脚本由浏览器各自到 /assets/ 取。
// 保留拼好的字符串供用例使用，界面判据多为"在源码里找某段规则或函数"，摊平后无需关心资源位置。
// 摊平结果与全部内联的版本逐字节相同。
var guiPageHTML = guiPageFlat()

// guiPageFlat 把骨架里的两处资源引用换回内联块。
func guiPageFlat() string {
	p := strings.Replace(guiShellHTML, `<link rel="stylesheet" href="`+guiCSSPath+`">`,
		"<style>\n"+guiAssetString("gui.css")+"</style>", 1)
	return strings.Replace(p, `<script src="`+guiJSPath+`"></script>`,
		"<script>\n"+guiAssetString("gui.js")+"</script>", 1)
}

// guiAssetString 读出 assets/ 下的文件；读不到直接 panic。
// 这两个文件在构建时嵌入，缺失说明打包出错，启动即失败优于运行半张无样式页面。
// （gui_assets_test.go 有用例确保两文件存在且非空。）
func guiAssetString(name string) string {
	b, err := guiAssetFS.ReadFile("assets/" + name)
	if err != nil {
		panic("界面资源缺失：" + name + "：" + err.Error())
	}
	return string(b)
}
