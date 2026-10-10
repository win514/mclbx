package main

// guihtml.go 控制台页面骨架：读取与注入。
//
// 骨架文本不编在本文件里：构建时放入 frontend/ 后经 //go:embed 嵌入（见 guiassets.go）。
// 界面按信号链组织（本机 → 入口 → 房间 → 玩家），空闲时无无限动画。
// 只有 DOM 骨架与资源引用：样式与脚本同样来自 frontend/，由本程序托管在 /assets/ 下。
// Go 侧不操作 DOM、不生成动画逻辑，只把当前设置写进 <html> 属性与首屏 JSON。
//
// 未随附界面时 guiShellHTML 为空串，renderGuiPage 改发一份说明页。

import "strings"

// guiShellHTML 是页面骨架；本次构建未装入前端时为空串。
var guiShellHTML, _ = guiFrontFile("gui.html")

// renderGuiPage 把界面设置注入 HTML 骨架；主题属性须服务端注入，避免首屏闪烁。
// 未装入前端（新克隆的仓库）时返回占位页。
func renderGuiPage() string {
	if guiShellHTML == "" {
		return guiPlaceholderPage()
	}
	ui := loadUI()
	// 背景图与主题属性一起注入，都要在首屏前定下来。
	attrs := ui.htmlAttr() + wallAttr(wallFromConfig(ui.BgImage))
	p := strings.Replace(guiShellHTML, "@@UIATTRS@@", attrs, 1)
	return strings.Replace(p, "@@UIJSON@@", ui.uiStartupJSON(), 1)
}

// guiPlaceholderPage 是未装入前端时的 / 页：说明本程序只提供接口、界面另行实现，并列出可用接口。
func guiPlaceholderPage() string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>我的世界联机工具</title>
<style>
  :root{color-scheme:dark}
  body{margin:0;background:#0d1117;color:#e6edf3;font:15px/1.7 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
  .wrap{max-width:760px;margin:0 auto;padding:48px 24px 64px}
  h1{font-size:20px;margin:0 0 8px}
  p{color:#8b949e}
  code{font-family:ui-monospace,Consolas,monospace;background:#161b22;border:1px solid #21262d;border-radius:6px;padding:2px 6px}
  ul{padding-left:22px;color:#c9d1d9}
  li{margin:6px 0}
  .m{color:#8b949e}
  .card{background:#161b22;border:1px solid #21262d;border-radius:10px;padding:16px 20px;margin-top:20px}
</style>
</head>
<body>
<div class="wrap">
  <h1>我的世界联机工具</h1>
  <p>本次运行的程序未随附界面。</p>
</div>
</body>
</html>
`
}
