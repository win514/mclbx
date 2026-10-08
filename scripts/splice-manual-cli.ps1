# splice-manual-cli.ps1 —— 把说明书里内嵌的那段 CLI 帮助重新生成为程序的实际输出
#
# 为什么需要它：说明书第 11 节那段「帮助文本原文」是一份副本，而手工维护的副本会漂 ——
# 它此前少了一行 `mclbx stun`，还把同一行以错的缩进插到了别处，可正文写着「原文」。
# 改完 main.go 里的 usageText 之后，用这个脚本把副本刷成一致，再跑测试确认：
#
#   go test -count=1 -run TestManualCliMatchesUsage .
#
# 用法：
#   .\scripts\splice-manual-cli.ps1
#   .\scripts\splice-manual-cli.ps1 -Exe '<部署目录>\mclbx.exe'
#
# 说明：
#   · 需要一个可用的 mclbx.exe；默认用仓库下 dist\mclbx.exe，取不到就报错并提示 -Exe。
#   · 只替换 <pre class="cli"> 与 </pre> 之间的内容：第一行保留 <b> 强调，
#     正文里的 <、>、& 转成 HTML 实体，其余原样。
#   · manual.html 是 LF 换行，脚本按原文件风格写回（不引入 CRLF，也不加 BOM）。
param(
  [string]$Exe = ''
)

$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
$manual = Join-Path $root 'manual.html'
if (-not (Test-Path $manual)) { throw "找不到 $manual" }

if (-not $Exe) { $Exe = Join-Path $root 'dist\mclbx.exe' }
if (-not (Test-Path $Exe)) {
  throw "找不到可用的 mclbx.exe（$Exe）。先构建一次，或用 -Exe 指定路径。"
}

$html = [System.IO.File]::ReadAllText($manual, [System.Text.Encoding]::UTF8)
$nl = if ($html.Contains("`r`n")) { "`r`n" } else { "`n" }

$startTag = '<pre class="cli">'
$i = $html.IndexOf($startTag)
if ($i -lt 0) { throw "manual.html 里找不到 $startTag" }
$i += $startTag.Length
$j = $html.IndexOf('</pre>', $i)
if ($j -lt 0) { throw 'manual.html 里找不到对应的 </pre>' }

$lines = @(& $Exe help)
while ($lines.Count -gt 0 -and "$($lines[-1])".Trim() -eq '') {
  $lines = @($lines[0..($lines.Count - 2)])
}
if ($lines.Count -lt 10) {
  throw "mclbx help 只输出了 $($lines.Count) 行，看着不对，先确认 $Exe 是可用的完整程序"
}

# HTML 实体先转 &，再转 < >，顺序不能反
$esc = { param($s) "$s".Replace('&', '&amp;').Replace('<', '&lt;').Replace('>', '&gt;') }

$out = New-Object System.Collections.Generic.List[string]
$out.Add('<b>' + (& $esc $lines[0]) + '</b>')
for ($k = 1; $k -lt $lines.Count; $k++) { $out.Add((& $esc $lines[$k])) }
$block = [string]::Join($nl, $out.ToArray())

$new = $html.Substring(0, $i) + $block + $html.Substring($j)
[System.IO.File]::WriteAllText($manual, $new, (New-Object System.Text.UTF8Encoding($false)))

Write-Host "已按 $Exe 的输出重写 $manual 里的 CLI 帮助，共 $($lines.Count) 行。"
Write-Host "接着跑：go test -count=1 -run TestManualCliMatchesUsage ."
