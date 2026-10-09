package main

// guiassets.go —— 界面资源：样式与脚本是独立文件，随 exe 内嵌、由本程序自己托管。
//
// 分工：Go 这一侧只做三件事 —— 渲染基础 HTML 模板、托管静态资源、提供业务接口。
// 它不操作 DOM、不参与任何动画逻辑：动效、交互、状态过渡、rAF 全在 assets/gui.css 与
// assets/gui.js 里由浏览器执行。服务端只把业务状态（当前设置、任务状态）写进页面的
// 属性与首屏 JSON，至于这些状态在界面上怎么表现，由浏览器自己决定。
//
// 骨架里那句 <link> / <script src> 是这两个文件的唯一入口，看 guihtml.go。

import "strings"

const (
	// 这两个路径同时出现在 guihtml.go 的骨架里，改一处必须改两处 —— 有用例盯着（见 gui_assets_test.go）。
	guiCSSPath = "/assets/gui.css"
	guiJSPath  = "/assets/gui.js"
)

// guiPageHTML 是"把两份外链资源摊平回页面"之后的完整源码：骨架 + 样式 + 脚本。
//
// 生产路径不用它 —— 服务端只发骨架，样式与脚本由浏览器各自到 /assets/ 去取。
// 留住这个拼好的字符串是给用例用的：界面判据绝大多数是"在源码里找某段规则或某个函数"
// （模糊挂在哪几个选择器上、某个函数体里写了什么），摊平之后它们不必关心资源放在哪。
// 摊平结果与"把全部内容内联在页面里"的那一版逐字节相同，所以这次拆分没有改变任何判据的含义。
var guiPageHTML = guiPageFlat()

// guiPageFlat 把骨架里的两处资源引用换回内联块。
func guiPageFlat() string {
	p := strings.Replace(guiShellHTML, `<link rel="stylesheet" href="`+guiCSSPath+`">`,
		"<style>\n"+guiAssetString("gui.css")+"</style>", 1)
	return strings.Replace(p, `<script src="`+guiJSPath+`"></script>`,
		"<script>\n"+guiAssetString("gui.js")+"</script>", 1)
}

// guiAssetString 读出 assets/ 下的那个文件。
// 读不到就直接崩：这两个文件是构建时嵌进来的，缺了说明打包写错了 ——
// 与其带着半张没有样式、没有脚本的页面跑起来，不如在启动那一刻就说清楚。
// （gui_assets_test.go 里有一条用例专门盯这两个文件存在且非空。）
func guiAssetString(name string) string {
	b, err := guiAssetFS.ReadFile("assets/" + name)
	if err != nil {
		panic("界面资源缺失：" + name + "：" + err.Error())
	}
	return string(b)
}
