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
