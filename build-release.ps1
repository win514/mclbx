# build-release.ps1 —— 一次构建五个平台的发布产物
#
# 说明：
#   · 图标和清单靠 rsrc_windows_{amd64,386,arm64}.syso 自动链进 exe（Go 按
#     GOOS/GOARCH 自动挑选匹配的 .syso），因此这里不需要额外参数。
#   · -trimpath 去掉本机绝对路径；-s -w 去掉符号表与调试信息。
#   · 版本号通过 -ldflags -X 注入到 main.version，界面与 doctor 里都会显示。
#   · 产物统一输出到 dist\ 目录，该目录已在 .gitignore 中忽略。
#
# 前置条件：Go 1.24 或更高版本，且 go 命令在 PATH 中。
#   如果 go 不在 PATH，可用环境变量指定：
#     $env:MCLBX_GO = 'C:\Go\bin\go.exe'
#   国内网络可先设置代理：
#     $env:GOPROXY = 'https://goproxy.cn,direct'
#
# 用法：
#   .\build-release.ps1
#   .\build-release.ps1 -Version 'mclbx 1.47'
param(
  [string]$Version = 'mclbx 1.47'
)

$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '0'

$go = if ($env:MCLBX_GO) { $env:MCLBX_GO } else { 'go' }
if (-not (Get-Command $go -ErrorAction SilentlyContinue)) {
  throw "找不到 go 命令（$go）。请把 Go 装好并加入 PATH，或用 `$env:MCLBX_GO 指定 go.exe 的完整路径。"
}

$dist = Join-Path $PSScriptRoot 'dist'
if (-not (Test-Path $dist)) { New-Item -ItemType Directory -Path $dist | Out-Null }

$ld = "-s -w -X 'main.version=$Version'"

$targets = @(
  @{ os = 'windows'; arch = 'amd64'; out = 'mclbx.exe' },
  @{ os = 'windows'; arch = '386';   out = 'mclbx-win32.exe' },
  @{ os = 'windows'; arch = 'arm64'; out = 'mclbx-winarm64.exe' },
  @{ os = 'linux';   arch = 'amd64'; out = 'mclbx-linux-amd64' },
  @{ os = 'linux';   arch = 'arm64'; out = 'mclbx-linux-arm64' }
)

foreach ($t in $targets) {
  $env:GOOS = $t.os
  $env:GOARCH = $t.arch
  $out = Join-Path $dist $t.out
  & $go build -trimpath -ldflags $ld -o $out .
  if ($LASTEXITCODE -ne 0) { throw "构建失败：$($t.os)/$($t.arch)" }
  $mb = [math]::Round((Get-Item $out).Length / 1MB, 2)
  Write-Host ("OK  {0,-8} {1,-6} {2,-22} {3} MB" -f $t.os, $t.arch, $t.out, $mb)
}

Write-Host "全部完成：$Version"
Write-Host "产物目录：$dist"
