# mclbx

我的世界 Java 版联机工具。将房主一侧的游戏端口暴露为玩家可连接的地址，不可用时退回加密隧道或中继转发。Windows 端是单个可执行文件，纯 Go 编写，免安装、免注册账号、不上传任何数据。

## 运行

```powershell
mclbx gui                 # 图形界面（网页版，默认）
mclbx gui --native        # 图形界面（原生控件版）
mclbx room --port 25565   # 房主侧：命令行开房间
mclbx join --host "[2408:...]:8090" --room abc123
mclbx help                # 全部命令与参数
```

图形界面默认申请管理员权限，用于按需添加防火墙入站规则。不需要时用 `mclbx gui --no-elevate`，或设 `MCLBX_NO_ELEVATE=1`。

用到两个端口：游戏端口（默认 25565）与信令端口（默认 8090）。一次放行两个：`mclbx firewall --port 25565,8090`。

## 构建

需要 Go 1.24 或更高版本，`CGO_ENABLED=0`。

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w" -o dist/mclbx.exe .
```

一次构建五个平台（Windows amd64 / 386 / arm64、Linux amd64 / arm64），产物输出到 `dist\`：

```powershell
.\build-release.ps1
```

国内网络可先设 `$env:GOPROXY = 'https://goproxy.cn,direct'`；`go` 不在 PATH 时可用 `$env:MCLBX_GO` 指定完整路径。

测试：`go test ./...`。

## 前端与接口

本仓库只包含后端与接口，界面（页面骨架、样式、脚本）不在其中。

程序仍可从零构建与运行：未装入前端时 `/` 返回一份最小占位页，说明本程序只提供接口、界面另行实现，并列出可用接口。要得到带完整界面的产物，构建前把前端三个文件 `gui.html`、`gui.css`、`gui.js` 放到本机前端目录 `D:\mclbx-frontend`（可用环境变量 `MCLBX_FRONTEND` 指定别处），再运行 `build-release.ps1`：它会在构建前把这三个文件装入仓库的 `frontend\` 目录（该目录除 `说明.txt` 外都被 `.gitignore` 挡住），并在输出里说明本次产物是否带界面。直接用 `go build` 构建时，若 `frontend\` 下没有这三个文件，产物即为仅接口版。

界面用到的接口：

| 接口 | 用途 |
|---|---|
| `GET /` | 界面页（未装入前端时为占位页） |
| `GET /api/state` | 运行状态 |
| `GET /api/tasks` | 任务清单 |
| `POST /api/start` | 启动任务 |
| `POST /api/stop` | 停止当前任务 |
| `POST /api/quit` | 退出程序 |
| `GET` / `POST /api/settings` | 读取 / 保存设置 |
| `POST /api/settings/export` | 导出设置 |
| `POST /api/settings/import` | 导入设置 |
| `POST /api/settings/reset` | 重置设置 |
| `POST /api/wall/import` | 导入背景图 |
| `GET /manual` | 说明书页 |
| `GET /bg/` | 背景图 |
| `GET /music/` | 背景音乐文件 |
| `GET /assets/` | 静态资源（图标、样式、脚本） |

写接口须带同源自定义头 `X-MCLBX-GUI: 1`。界面服务只绑定本机 `127.0.0.1`。

## 依赖

| 组件 | 用途 | 许可证 |
|---|---|---|
| github.com/pion/ice/v4 | ICE 打洞 | MIT |
| github.com/pion/dtls/v3 | 数据面加密 | MIT |
| github.com/pion/sctp | 隧道传输层 | MIT |
| github.com/pion/stun/v4 | STUN 探测 | MIT |
| github.com/pion/turn/v5 | TURN 客户端（间接引入） | MIT |
| golang.org/x/crypto | 密钥交换与 AEAD | BSD-3-Clause |
| github.com/google/uuid、golang.org/x/* | 间接依赖 | BSD-3-Clause |
| github.com/wlynxg/anet | 网卡枚举（间接引入） | MIT |
| WebView2Loader.dll | 承载网页版界面 | Microsoft，见 `assets/来源与许可.txt` |

完整的 `go.mod` 依赖树见 [go.mod](go.mod)。

## 许可证

MIT，见 [LICENSE](LICENSE)。
