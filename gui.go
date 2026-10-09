package main

// gui.go 本地网页版图形控制台：把子命令当子进程拉起并显示其输出。
//
// 只绑 127.0.0.1；POST 要求自定义头 X-MCLBX-GUI 以防跨站调用。

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"log"
	"net"
	"net/http"
	// 起别名：下面有名为 url 的局部变量，不加别名会被遮住
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// 界面用的本地动画库（离线引用，不走 CDN）。
//
//go:embed assets
var guiAssetFS embed.FS

const (
	// 控制台保留的行数
	guiMaxLines = 3000
	// 单行最大字节数，超出即截断（不停止读取，见 pumpLines）
	guiMaxLine = 32 * 1024
	// 行截断时追加的标记
	guiLineCut = " …（本行过长已截断）"
)

// guiMode 判断当前是否由控制台拉起的子进程（环境变量 MCLBX_GUI=1）。
func guiMode() bool { return os.Getenv("MCLBX_GUI") == "1" }

// guiMarker 仅在控制台模式下返回 "##kind## value"，命令行模式返回空串。
func guiMarker(kind, value string) string {
	if !guiMode() || strings.TrimSpace(value) == "" {
		return ""
	}
	return fmt.Sprintf("##%s## %s", kind, value)
}

// guiDetail 输出排障用细节：界面模式下加 ##DBG## 前缀，命令行模式下原样输出。
func guiDetail(format string, a ...any) {
	guiDetailLine(fmt.Sprintf(format, a...))
}

// guiDetailf 同 guiDetail，但带时间戳前缀。
func guiDetailf(format string, a ...any) {
	guiDetailLine(fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...)))
}

func guiDetailLine(line string) {
	if guiMode() {
		fmt.Println("##DBG## " + line)
		return
	}
	fmt.Println(line)
}

// guiField 一个输入项；Hint 用作悬浮提示，不宜写入 Label。
type guiField struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Default     string `json:"default"`
	Hint        string `json:"hint"`
	Kind        string `json:"kind"` // ""=文本，check=勾选
	Required    bool   `json:"required"`
}

// guiTask 一个动作；Help 为完整说明，界面上置于「详情」中。
type guiTask struct {
	Key    string     `json:"key"`
	Group  string     `json:"group"`
	Name   string     `json:"name"`
	Desc   string     `json:"desc"`
	Help   string     `json:"help"`
	Long   bool       `json:"long"` // 长跑任务：要手动停
	Fields []guiField `json:"fields"`
	build  func(in map[string]string) ([]string, error)
}

func argIf(args []string, name, val string) []string {
	if v := strings.TrimSpace(val); v != "" {
		return append(args, name, v)
	}
	return args
}

// durationArg 把界面输入的纯数字秒转为带单位的时长（如 120 → 120s），带单位的原样透传。
func durationArg(sec string) string {
	s := strings.TrimSpace(sec)
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return s + "s"
	}
	return s
}

// listenArg 把界面输入的端口补成 net.Listen 可用的 "主机:端口"（纯数字补 127.0.0.1）。
func listenArg(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	if strings.Contains(s, ":") { // 已是 host:port（含 [v6]:port）
		return s
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "127.0.0.1:" + s
	}
	return s
}

// renderGuiPage 将界面设置注入 HTML，主题等属性须由服务端注入以避免首屏闪烁。
func renderGuiPage() string {
	ui := loadUI()
	// 背景图与主题属性一起注入：两者都要在首屏之前定下来，否则会先闪一下默认外观
	attrs := ui.htmlAttr() + wallAttr(wallFromConfig(ui.BgImage))
	p := strings.Replace(guiPageHTML, "@@UIATTRS@@", attrs, 1)
	return strings.Replace(p, "@@UIJSON@@", ui.uiStartupJSON(), 1)
}

// settingsPayload 返回设置面板所需的字段；autoProbe 只读不存。
func settingsPayload(ui guiUIState) map[string]any {
	wall := wallFromConfig(ui.BgImage)
	return map[string]any{
		"ok":       true,
		"theme":    ui.Theme,
		"accent":   ui.Accent,
		"motion":   ui.Motion,
		"backdrop": ui.Backdrop,
		"scale":    ui.Scale,
		"flat":     ui.Flat,
		"glass":    ui.Glass,
		"bgImage":  ui.BgImage,
		// 图库里的图片名。配置里存的就是这些名字之一，所以面板不需要用户敲路径。
		"wallList": listWallImages(),
		// 曲库：真相在存档的 music 目录里，这里只把扫出来的清单交给界面
		"musicList": listMusicTracks(),
		"musicMode": ui.MusicMode,
		"musicLoop": ui.MusicLoop,
		"musicVol":  ui.MusicVol,
		"remember":  ui.Remember,
		// 背景图已经缩放缓存好，这里只把地址交给界面；读不出来时 wallReason 给一句原因
		"wallURL":    wall.URL,
		"wallReason": wall.Reason,
		// 显示生效值而非原始配置：未配置时也给出实际默认
		"logKeep":   strconv.Itoa(ui.logKeep()),
		"defPort":   ui.DefPort,
		"defRelay":  ui.DefRelay,
		"autoProbe": autoProbeOn(),
		"dataDir":   dataDir(),
		"version":   version,
	}
}

// openDataDir 在资源管理器中打开数据目录。
func openDataDir() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("这条是给 Windows 资源管理器用的；当前系统是 %s，数据目录是 %s", runtime.GOOS, dataDir())
	}
	dir := dataDir()
	if dir == "" {
		return fmt.Errorf("本机找不到可写的数据目录")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("数据目录不可用：%w", err)
	}
	if err := exec.Command("explorer", dir).Start(); err != nil {
		return fmt.Errorf("打不开资源管理器：%w", err)
	}
	return nil
}

// relayServerField 返回各任务共用的「中转服务器」字段定义。
func relayServerField() guiField {
	return guiField{
		ID:          "relayServer",
		Label:       "中转服务器",
		Placeholder: "mclbx://主机?tcp=8090&room=abc123&secret=…",
		Hint:        "可以留空；只有直连不通时才会用到",
	}
}

func relayArgs(args []string, in map[string]string) []string {
	return argIf(args, "--relay-server", in["relayServer"])
}

// linkCarriesRoom 判断中继链接中是否已包含房间码。
func linkCarriesRoom(link string) bool {
	if strings.TrimSpace(link) == "" {
		return false
	}
	plan, err := resolveRelayLink(link)
	return err == nil && plan != nil && plan.Room != ""
}

var guiTasks = []guiTask{
	{
		// 开界面时会自动跑一次（见 autoProbeOn）
		Key: "probe", Group: "环境检测", Name: "检测本机环境",
		Desc: "检测网络环境并给出建议的连接方式",
		Help: "检测项包括公网 IPv6 地址、IPv4 出口与 NAT 类型、路由器端口映射能力、游戏端口占用情况，以及已配置的 Windows 入站规则。结论用于确定本次可用的连接方式。",
		Fields: []guiField{
			{ID: "port", Label: "游戏端口", Default: "25565", Hint: "同时检测该端口是否被占用"},
			{ID: "quick", Label: "跳过端口映射探测", Kind: "check", Hint: "检测更快，结论更保守"},
			{ID: "auto", Label: "打开界面时自动检测", Kind: "check", Default: "1", Hint: "该设置会被记住"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"doctor"}
			args = argIf(args, "--port", in["port"])
			if in["quick"] == "1" {
				args = append(args, "--quick")
			}
			return args, nil
		},
	},
	{
		Key: "room", Group: "房主模式", Name: "创建房间", Long: true,
		Desc: "同时开启公网入口与软件入口",
		Help: "一次开启两条入口：公网入口供未安装本工具的玩家使用，软件入口供已安装的玩家使用；两条同时工作，任一条可用即可联机。房间码用于标识本次连接，两端必须一致，留空时自动生成。玩家中途掉线后会自动重连，不需要重新建房。",
		Fields: []guiField{
			{ID: "room", Label: "房间码", Placeholder: "abc123", Hint: "留空时自动生成 6 位"},
			{ID: "port", Label: "游戏端口", Placeholder: "25565", Hint: "留空时自动检测"},
			relayServerField(),
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"room", "--web", "off"}
			args = argIf(args, "--room", in["room"])
			args = argIf(args, "--port", in["port"])
			return relayArgs(args, in), nil
		},
	},
	{
		Key: "expose-raw", Group: "房主模式", Name: "公网入口（IPv6）", Long: true,
		Desc: "仅开启 IPv6 公网入口",
		Help: "前提是本机具备公网 IPv6 地址，且玩家一侧具备 IPv6 出口；满足时玩家在游戏内「多人游戏 → 直接连接」填入本机地址即可进入，不需要在玩家一侧安装本工具。",
		Fields: []guiField{
			{ID: "port", Label: "游戏端口", Placeholder: "25565", Hint: "留空时自动检测"},
			{ID: "allow", Label: "玩家名称白名单", Placeholder: "Steve,Alex", Hint: "逗号分隔；留空时允许所有玩家。Java 版玩家名只含英文、数字与下划线，写中文名匹配不上"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"expose", "--mode", "raw", "--web", "off"}
			args = argIf(args, "--port", in["port"])
			args = argIf(args, "--allow", in["allow"])
			return args, nil
		},
	},
	{
		Key: "expose-dns", Group: "房主模式", Name: "公网入口（域名）", Long: true,
		Desc: "以域名作为入口，房间码作为口令",
		Help: "将短域名对应的 AAAA 与 SRV 记录发布到公共 DNS，玩家在游戏中输入该域名即可，无需填写端口。玩家输入的名称必须包含房间码，否则入口拒绝连接。",
		Fields: []guiField{
			{ID: "room", Label: "房间码", Placeholder: "abc123", Hint: "作为访问口令；留空时自动生成"},
			{ID: "port", Label: "游戏端口", Placeholder: "25565", Hint: "留空时自动检测"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"expose", "--mode", "dns", "--web", "off"}
			args = argIf(args, "--room", in["room"])
			args = argIf(args, "--port", in["port"])
			return args, nil
		},
	},
	{
		// 同组任务必须连续，否则列表会出现重复的分组标题
		Key: "ice-host", Group: "房主模式", Name: "直连模式（房主）", Long: true,
		Desc: "接受玩家直连并转发至本机端口",
		Help: "与玩家侧的「直连模式（玩家）」配对使用。启动后等待玩家接入，通道建立后将流量转发至指定的本机游戏端口。",
		Fields: []guiField{
			{ID: "to", Label: "转发目标", Placeholder: "127.0.0.1:25565", Hint: "默认为 127.0.0.1:25565"},
			relayServerField(),
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"ice", "host"}
			args = argIf(args, "--to", in["to"])
			return relayArgs(args, in), nil
		},
	},
	{
		Key: "join", Group: "玩家模式", Name: "加入房间", Long: true,
		Desc: "连接房主并建立加密通道",
		Help: "输入可以是房主提供的入口地址、含 sig= 参数的中继链接，或房间码配合中继服务器。直连不通时，已配置中继服务器会自动改用中继通道，未配置中继则中断并说明原因。通道中途断开会自动重连，本机监听地址保持不变，游戏内无需重新填写。",
		Fields: []guiField{
			{ID: "host", Label: "房主地址", Placeholder: "[2408:...]:8090", Hint: "已包含在中继链接中时可留空"},
			{ID: "room", Label: "房间码", Placeholder: "abc123", Hint: "链接中已包含时可留空"},
			// 本机监听端口与等待时长也开放到界面，供「本机也开游戏」「网络较慢」使用
			{ID: "local", Label: "本机监听端口", Placeholder: "25565", Hint: "本机自己也开着游戏时改成别的，例如 25566"},
			{ID: "wait", Label: "等待房主候选（秒）", Placeholder: "60", Hint: "网络较慢时可调大，例如 120"},
			{ID: "relay", Label: "同时开启中继转发", Kind: "check", Hint: "将本机地址也作为入口"},
			{ID: "relayAllow", Label: "中继转发入口白名单", Placeholder: "Steve,Alex", Hint: "仅在上面勾选时生效；留空时不校验玩家名"},
			relayServerField(),
		},
		build: func(in map[string]string) ([]string, error) {
			host := strings.TrimSpace(in["host"])
			link := strings.TrimSpace(in["relayServer"])
			if host == "" && link == "" {
				return nil, fmt.Errorf("请填写房主地址，或填写一条中继链接")
			}
			args := []string{"join"}
			args = argIf(args, "--host", host)
			args = argIf(args, "--room", in["room"])
			args = argIf(args, "--local", listenArg(in["local"]))
			args = argIf(args, "--wait", durationArg(in["wait"]))
			args = argIf(args, "--relay-server", link)
			if in["relay"] == "1" {
				args = append(args, "--relay")
				args = argIf(args, "--relay-allow", in["relayAllow"])
			}
			return args, nil
		},
	},
	{
		Key: "ice-guest", Group: "玩家模式", Name: "直连模式（玩家）", Long: true,
		Desc: "连接房主的直连入口，不使用房间码",
		Help: "与房主侧的「直连模式（房主）」配对使用。双方交换连接信息后建立点对点通道；两端网络均不利于直连时，需配合中继服务器使用。",
		Fields: []guiField{
			{ID: "host", Label: "房主地址", Placeholder: "[2408:...]:8090", Required: true},
			relayServerField(),
		},
		build: func(in map[string]string) ([]string, error) {
			if strings.TrimSpace(in["host"]) == "" {
				return nil, fmt.Errorf("请填写房主地址")
			}
			args := []string{"ice", "guest", "--host", strings.TrimSpace(in["host"])}
			return relayArgs(args, in), nil
		},
	},
	{
		Key: "natmap", Group: "故障排查", Name: "端口映射", Long: true,
		Desc: "通过路由器将本机端口映射至公网",
		Help: "适用于本机没有公网 IPv6、但路由器支持自动映射的场景。映射成功后，玩家可通过路由器的公网地址直连本机端口。映射存在租期，程序运行期间按需续期。",
		Fields: []guiField{
			{ID: "port", Label: "本机端口", Placeholder: "25565", Required: true, Hint: "需要映射至公网的端口"},
			{ID: "proto", Label: "协议", Default: "both", Hint: "tcp、udp 或 both"},
			{ID: "ext", Label: "外网端口", Placeholder: "25565", Hint: "留空时由路由器分配"},
		},
		build: func(in map[string]string) ([]string, error) {
			p := strings.TrimSpace(in["port"])
			if p == "" {
				return nil, fmt.Errorf("请填写需要映射的端口")
			}
			args := []string{"natmap", "--port", p, "--keep"}
			args = argIf(args, "--proto", in["proto"])
			args = argIf(args, "--ext", in["ext"])
			return args, nil
		},
	},
	{
		Key: "firewall", Group: "故障排查", Name: "入站规则",
		Desc: "配置 Windows 防火墙入站规则",
		Help: "规则仅对指定端口放行，且限定为本程序，不修改防火墙总开关。执行需要管理员权限，会弹出一次授权确认。勾选下方选项可撤销已添加的规则。",
		Fields: []guiField{
			{ID: "port", Label: "入站端口", Default: "25565"},
			{ID: "proto", Label: "协议", Default: "both", Hint: "tcp、udp 或 both"},
			{ID: "remove", Label: "删除已添加的规则", Kind: "check"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"firewall"}
			args = argIf(args, "--port", in["port"])
			args = argIf(args, "--proto", in["proto"])
			if in["remove"] == "1" {
				args = append(args, "--remove")
			}
			return args, nil
		},
	},
	{
		Key: "tcphost", Group: "故障排查", Name: "中继转发（房主）", Long: true,
		Desc: "UDP 不可用时经中继转发",
		Help: "两端均主动连接同一台中继，因此不受入站限制影响，可穿过阻断 UDP 的网络。流量经中继转发，为保证连通性采用明文传输。房间码两端需一致。",
		Fields: []guiField{
			{ID: "relay", Label: "中继地址", Placeholder: "1.2.3.4:8090", Hint: "公网中继地址；也可只填中继链接"},
			{ID: "room", Label: "房间码", Placeholder: "abc123", Hint: "链接中已包含时可留空"},
			{ID: "to", Label: "转发目标", Default: "127.0.0.1:25565", Hint: "本机游戏端口"},
			relayServerField(),
		},
		build: func(in map[string]string) ([]string, error) {
			relay := strings.TrimSpace(in["relay"])
			link := strings.TrimSpace(in["relayServer"])
			if relay == "" && link == "" {
				return nil, fmt.Errorf("中继地址与中继链接至少填写一项")
			}
			if strings.TrimSpace(in["room"]) == "" && !linkCarriesRoom(link) {
				return nil, fmt.Errorf("请填写房间码，并与对端保持一致")
			}
			args := []string{"tcptunnel", "host"}
			args = argIf(args, "--relay", relay)
			args = argIf(args, "--relay-server", link)
			args = argIf(args, "--room", in["room"])
			return argIf(args, "--to", in["to"]), nil
		},
	},
	{
		Key: "tcpguest", Group: "故障排查", Name: "中继转发（玩家）", Long: true,
		Desc: "接收中继流量并映射为本地端口",
		Help: "与「中继转发（房主）」配对使用。通道建立后，中继上的连接会出现在本机指定地址上，游戏内连接该地址即可进入。房间码需与房主一致。",
		Fields: []guiField{
			{ID: "relay", Label: "中继地址", Placeholder: "1.2.3.4:8090", Hint: "公网中继地址；也可只填中继链接"},
			{ID: "room", Label: "房间码", Placeholder: "abc123", Hint: "链接中已包含时可留空"},
			{ID: "local", Label: "本机监听地址", Default: "127.0.0.1:25565", Hint: "游戏内连接该地址"},
			relayServerField(),
		},
		build: func(in map[string]string) ([]string, error) {
			relay := strings.TrimSpace(in["relay"])
			link := strings.TrimSpace(in["relayServer"])
			if relay == "" && link == "" {
				return nil, fmt.Errorf("中继地址与中继链接至少填写一项")
			}
			if strings.TrimSpace(in["room"]) == "" && !linkCarriesRoom(link) {
				return nil, fmt.Errorf("请填写房间码，并与对端保持一致")
			}
			args := []string{"tcptunnel", "guest"}
			args = argIf(args, "--relay", relay)
			args = argIf(args, "--relay-server", link)
			args = argIf(args, "--room", in["room"])
			return argIf(args, "--local", in["local"]), nil
		},
	},
	{
		Key: "verify", Group: "故障排查", Name: "地址校验",
		Desc: "按客户端流程校验入口地址",
		Help: "校验顺序为地址解析、域名与 SRV 记录、入口端口连通性、游戏握手；失败步骤单独给出原因。玩家一侧是否可连入需从外部网络验证，本机无法确认。",
		Fields: []guiField{
			{ID: "addr", Label: "待校验地址", Required: true, Hint: "地址、host:port 或 mclbx:// 链接",
				Placeholder: "[2408:...]:25565 或 room1.mclbx.test"},
		},
		build: func(in map[string]string) ([]string, error) {
			a := strings.TrimSpace(in["addr"])
			if a == "" {
				return nil, fmt.Errorf("请填写待校验的地址")
			}
			return []string{"verify", "--addr", a}, nil
		},
	},
	{
		Key: "ping", Group: "实用工具", Name: "服务器查询",
		Desc: "查询目标服务器的状态",
		Help: "发送一次状态查询并显示返回结果，包括版本号与在线人数。可用于确认目标是否为正常运行的我的世界 Java 版服务端。",
		Fields: []guiField{
			{ID: "host", Label: "目标地址", Default: "127.0.0.1", Placeholder: "127.0.0.1"},
			{ID: "port", Label: "端口", Default: "25565"},
		},
		build: func(in map[string]string) ([]string, error) {
			h := strings.TrimSpace(in["host"])
			if h == "" {
				h = "127.0.0.1"
			}
			p := strings.TrimSpace(in["port"])
			if p == "" {
				p = "25565"
			}
			return []string{"ping", "--host", h, "--port", p}, nil
		},
	},
	{
		Key: "diag", Group: "实用工具", Name: "导出诊断包",
		Desc: "汇总版本、环境体检与最近一次日志，便于求助",
		Help: "生成一个文本文件：内含程序版本、系统与网卡、环境体检的全部结论，以及最近一次操作的日志。遇到自己解决不了的问题时报给别人，可省去反复截图。文件里含本机网络地址，发出之前请先自己看一眼。",
		Fields: []guiField{
			{ID: "port", Label: "游戏端口", Default: "25565", Hint: "体检用，与游戏内设置一致"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"diag"}
			args = argIf(args, "--port", in["port"])
			return args, nil
		},
	},
	{
		Key: "punch", Group: "实用工具", Name: "端口探测",
		Desc: "两端同时探测对端端口池，验证能否打穿",
		Help: "仅用于诊断：两端都处在对称型 NAT、内置直连与中继都走不通时，用它确认「这两端之间是否存在可打穿的路径」。两端的口令与端口范围必须一致。注意它只给结论，扫出来的地址无法直接用于联机 —— 内置直连会自己选端口，来源端口一变，对端 NAT 的映射就跟着变。探测出可打穿时，当前版本的可用手段仍然是中继。",
		Fields: []guiField{
			{ID: "peer", Label: "对端公网 IP", Placeholder: "203.0.113.7", Required: true, Hint: "对端检测报告中的出口 IP"},
			{ID: "ports", Label: "对端端口范围", Default: "30000-30099", Hint: "两端需一致"},
			{ID: "tag", Label: "口令", Default: "mclbx", Hint: "两端需一致"},
		},
		build: func(in map[string]string) ([]string, error) {
			peer := strings.TrimSpace(in["peer"])
			if peer == "" {
				return nil, fmt.Errorf("请填写对端公网 IP")
			}
			return []string{"punch", "--peer", peer, "--ports", strings.TrimSpace(in["ports"]), "--tag", strings.TrimSpace(in["tag"])}, nil
		},
	},
	{
		Key: "relaybox", Group: "中继服务", Name: "本机中继", Long: true,
		Desc: "以本机作为中继并生成链接",
		Help: "启动后生成一条包含地址与凭据的链接，发送给参与者即可使用，无需另行分发账号密码。默认尝试通过路由器自动映射端口。",
		Fields: []guiField{
			{ID: "public", Label: "对外地址", Placeholder: "留空自动检测", Hint: "留空时自动检测"},
			{ID: "noOpen", Label: "不修改路由器端口映射", Kind: "check", Hint: "默认会尝试自动映射"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"relaybox"}
			args = argIf(args, "--public", in["public"])
			if in["noOpen"] == "1" {
				args = append(args, "--no-open-ports")
			}
			return args, nil
		},
	},
	{
		Key: "relaycheck", Group: "中继服务", Name: "中继校验",
		Desc: "校验中继链接的可用性",
		Help: "完整执行一次连接流程：解析链接、探测映射、申请分配与数据往返；失败时指出具体环节。",
		Fields: []guiField{
			{ID: "link", Label: "中继链接", Placeholder: "mclbx://主机?tcp=8090&room=abc123&secret=…", Required: true},
			{ID: "user", Label: "账号", Placeholder: "mclbx", Hint: "链接中未包含时填写"},
			{ID: "pass", Label: "密码", Hint: "链接中未包含时填写"},
		},
		build: func(in map[string]string) ([]string, error) {
			link := strings.TrimSpace(in["link"])
			if link == "" {
				return nil, fmt.Errorf("请填写中继链接")
			}
			args := []string{"relaycheck"}
			args = argIf(args, "--user", in["user"])
			args = argIf(args, "--pass", in["pass"])
			return append(args, link), nil
		},
	},
	{
		Key: "relayserve", Group: "中继服务", Name: "公网中继", Long: true,
		Desc: "在具备公网地址的机器上运行中继",
		Help: "需运行在具备公网地址的服务器上，启动完成后输出供其他人填写的链接。云主机需显式指定公网地址，网卡地址通常为内网地址。",
		Fields: []guiField{
			{ID: "port", Label: "监听端口", Default: "3478", Hint: "UDP 端口"},
			{ID: "publicIP", Label: "公网地址", Placeholder: "1.2.3.4", Hint: "云主机需填写；网卡地址通常为内网地址"},
			{ID: "rest", Label: "密钥", Hint: "设置后无需分发固定密码"},
			{ID: "range", Label: "中继端口范围", Placeholder: "49160-49200", Hint: "建议填写，便于仅放行该范围"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"stun", "--turn", "--port", strings.TrimSpace(in["port"])}
			args = argIf(args, "--relay-ip", in["publicIP"])
			args = argIf(args, "--rest", in["rest"])
			args = argIf(args, "--relay-port-range", in["range"])
			return args, nil
		},
	},
	{
		Key: "stun", Group: "中继服务", Name: "STUN 服务", Long: true,
		Desc: "提供公网地址探测，不转发数据",
		Help: "用于两端探测各自的公网映射，本身不中转流量，带宽开销极低。需运行在具备公网地址的机器上。",
		Fields: []guiField{
			{ID: "port", Label: "监听端口", Default: "3478"},
			{ID: "turn", Label: "同时提供 UDP 中转", Kind: "check", Hint: "同时承担中继转发"},
		},
		build: func(in map[string]string) ([]string, error) {
			args := []string{"stun", "--port", strings.TrimSpace(in["port"])}
			if in["turn"] == "1" {
				args = append(args, "--turn")
			}
			return args, nil
		},
	},
	{
		Key: "mailbox", Group: "中继服务", Name: "信令信箱", Long: true,
		Desc: "为双方交换连接信息",
		Help: "房主无法被直接连接时，双方通过该服务交换当前可用的地址，再按新地址重试。最多保留 1024 条；条目 3 分钟未更新即被回收，等待中的一方会定期重新登记。",
		Fields: []guiField{
			// 默认值与命令行一致（defaultListenAddr），以便同时接受 IPv6 连接
			{ID: "listen", Label: "监听地址", Default: defaultListenAddr},
		},
		build: func(in map[string]string) ([]string, error) {
			return []string{"mailbox", "--listen", strings.TrimSpace(in["listen"])}, nil
		},
	},
}

type guiJob struct {
	id      string
	key     string
	title   string
	command string
	cmd     *exec.Cmd
	started time.Time
	done    bool
	code    int
	errMsg  string
	killed  bool // 是否已收过，避免重复清理

	// auto 表示本次任务由打开界面自动触发，可被用户任务顶掉。
	auto bool

	mu    sync.Mutex
	lines []string
	total int64 // 累计产出行数（绝对序号），界面据此做增量

	// 已补过的中文提示（按 errhint 的 key 去重）
	hinted map[string]bool
}

// 会话日志：每项任务开始时截断重写，只保留最后一次，单文件上限 4 MB。
const guiSessionLogMax = 4 << 20

type sessionLog struct {
	mu  sync.Mutex
	job string
	n   int // 本次任务已写入字节数
}

var guiSessionLog sessionLog

// write 写一行；job 变化时从头开始写。
func (s *sessionLog) write(job, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != job {
		s.job, s.n = job, 0
	}
	if s.n >= guiSessionLogMax {
		return
	}
	p := sessionLogPath()
	if p == "" {
		return
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if s.n == 0 {
		// 本次任务第一行：清空上一个任务的内容
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(p, flags, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if n, err := f.WriteString(line + "\r\n"); err == nil {
		s.n += n
	}
}

// append 记录一行输出，并在报错后补中文说明（见 errhint.go）。
func (j *guiJob) append(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pushLocked(line)
	for _, h := range explainError(line) {
		if j.hinted == nil {
			j.hinted = map[string]bool{}
		}
		if j.hinted[h.key] {
			continue
		}
		j.hinted[h.key] = true
		j.pushLocked(h.text)
	}
}

// pushLocked 把一行写入缓冲；调用方须持有 j.mu。
func (j *guiJob) pushLocked(line string) {
	j.lines = append(j.lines, line)
	j.total++
	// 落盘集中在此，保证诊断包日志与界面显示一致
	guiSessionLog.write(j.id, line)
	if len(j.lines) > guiMaxLines {
		// 原地丢弃老行，避免底层数组增长并释放旧字符串
		n := len(j.lines) - guiMaxLines
		j.lines = append(j.lines[:0], j.lines[n:]...)
	}
}

// pumpLines 逐行读取子进程输出并交给 emit。
//
// 管道有缓冲上限，必须持续读取；单行过长仅截断，绝不停读，否则子进程写满会阻塞。
func pumpLines(r io.Reader, emit func(string)) {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	truncated := false
	for {
		chunk, err := br.ReadSlice('\n')
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			if room := guiMaxLine - len(buf); i > room {
				if room > 0 {
					buf = append(buf, chunk[:room]...)
				}
				truncated = true
			} else {
				buf = append(buf, chunk[:i]...)
			}
			emitLine(buf, truncated, emit)
			buf, truncated = buf[:0], false
		} else if len(chunk) > 0 {
			switch room := guiMaxLine - len(buf); {
			case room <= 0:
				truncated = true
			case len(chunk) > room:
				buf = append(buf, chunk[:room]...)
				truncated = true
			default:
				buf = append(buf, chunk...)
			}
		}
		if err == nil {
			continue
		}
		if err == bufio.ErrBufferFull {
			continue // 本段未读到换行，继续读
		}
		break // EOF 或错误：发出剩余半行后结束
	}
	if len(buf) > 0 {
		emitLine(buf, truncated, emit)
	}
}

func emitLine(b []byte, truncated bool, emit func(string)) {
	if truncated {
		// 截断点退到完整字符边界，避免把一个多字节字符劈成两半
		b = trimPartialRune(b)
	}
	s := strings.TrimRight(string(b), "\r")
	if truncated {
		s += guiLineCut
	}
	emit(s)
}

// trimPartialRune 去掉尾部不完整的多字节字符（最多回退 3 字节）。
func trimPartialRune(b []byte) []byte {
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			return b
		}
		b = b[:len(b)-1]
	}
	return b
}

func (j *guiJob) pump(r io.Reader) {
	pumpLines(r, j.append)
}

// snapshot 返回 since 之后的新行。
// 服务端只保留最后 guiMaxLines 行，落后过多时首行会说明中间有多少行未保留。
func (j *guiJob) snapshot(since int64) []string {
	if len(j.lines) == 0 {
		return nil
	}
	oldest := j.total - int64(len(j.lines)) // 最老一行的绝对序号
	if since < oldest {
		out := make([]string, 0, len(j.lines)+1)
		if since > 0 {
			out = append(out, fmt.Sprintf("…（中间 %d 行没有保留，下面从最近 %d 行接上）", oldest-since, len(j.lines)))
		}
		return append(out, j.lines...)
	}
	off := int(since - oldest)
	if off >= len(j.lines) {
		return nil
	}
	return append(make([]string, 0, len(j.lines)-off), j.lines[off:]...)
}

func (j *guiJob) finish(err error) {
	j.mu.Lock()
	j.done = true
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			j.code = ee.ExitCode()
		} else {
			j.code = -1
		}
		if j.errMsg == "" {
			j.errMsg = err.Error()
		}
	}
	j.mu.Unlock()
}

func (j *guiJob) kill() {
	if j.cmd == nil || j.cmd.Process == nil {
		return
	}
	j.mu.Lock()
	if j.killed { // 幂等：多处关闭路径都可能调用
		j.mu.Unlock()
		return
	}
	j.killed = true
	j.errMsg = "已手动停止"
	j.mu.Unlock()
	if runtime.GOOS == "windows" {
		// 连同子进程树一起杀；须设无窗口控制台，否则 taskkill 会闪出黑框
		tk := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(j.cmd.Process.Pid))
		tk.SysProcAttr = hiddenConsoleProcAttr()
		_ = tk.Run()
		return
	}
	_ = j.cmd.Process.Kill()
}

type guiController struct {
	mu  sync.Mutex
	job *guiJob
}

func (c *guiController) start(t guiTask, in map[string]string) (*guiJob, error) {
	return c.startJob(t, in, false)
}

// startJob 启动一个任务；auto=true 表示界面自动触发，可被用户任务顶掉。
func (c *guiController) startJob(t guiTask, in map[string]string, auto bool) (*guiJob, error) {
	args, err := t.build(in)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("找不到自己的可执行文件：%w", err)
	}

	// 检查是否有任务在跑；自动任务不占用，可被直接顶掉
	if blockedBy, preempt := c.checkBusy(auto); blockedBy != "" {
		return nil, fmt.Errorf("已经有一个任务在跑（%s），先点「停止」再来", blockedBy)
	} else if preempt != nil {
		preempt.kill()
	}

	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "MCLBX_GUI=1")
	cmd.Stdin = nil
	// 必须给子进程无窗口控制台，否则每跑一个任务都会闪出黑框
	cmd.SysProcAttr = hiddenConsoleProcAttr()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	j := &guiJob{
		id:      strconv.FormatInt(time.Now().UnixNano(), 36),
		key:     t.Key,
		title:   t.Name,
		command: "mclbx " + strings.Join(args, " "),
		cmd:     cmd,
		started: time.Now(),
		auto:    auto,
	}
	c.mu.Lock()
	c.job = j
	c.mu.Unlock()

	if err := cmd.Start(); err != nil {
		j.mu.Lock()
		j.done, j.errMsg, j.code = true, "启动失败："+err.Error(), -1
		j.mu.Unlock()
		return nil, err
	}
	// 把子进程收进 Job Object，避免其成为占用端口的孤儿进程
	adoptChild(cmd.Process.Pid)
	go j.pump(stdout)
	go j.pump(stderr)
	go func() { j.finish(cmd.Wait()) }()
	return j, nil
}

func (c *guiController) stop() {
	c.mu.Lock()
	j := c.job
	c.mu.Unlock()
	if j != nil {
		j.kill()
	}
}

// checkBusy 判断能否启动新任务：blockedBy 非空表示被占用，preempt 非空表示可顶掉。
func (c *guiController) checkBusy(auto bool) (blockedBy string, preempt *guiJob) {
	c.mu.Lock()
	j := c.job
	c.mu.Unlock()
	if j == nil {
		return "", nil
	}
	j.mu.Lock()
	busy, prevAuto, name := !j.done, j.auto, j.title
	j.mu.Unlock()
	if !busy {
		return "", nil
	}
	if prevAuto && !auto {
		return "", j
	}
	return name, nil
}

func (c *guiController) snapshot(since int64) map[string]any {
	c.mu.Lock()
	j := c.job
	c.mu.Unlock()
	out := map[string]any{"running": false, "jobId": "", "seq": 0, "lines": []string{}}
	out["ver"] = version
	if j == nil {
		return out
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	out["lines"] = j.snapshot(since)
	out["seq"] = j.total
	out["kept"] = len(j.lines)
	out["jobId"] = j.id
	out["task"] = j.title
	out["taskKey"] = j.key
	out["command"] = j.command
	out["running"] = !j.done
	out["done"] = j.done
	out["code"] = j.code
	out["err"] = j.errMsg
	out["uptime"] = time.Since(j.started).Round(time.Second).String()
	return out
}

func guiAllowedHost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// guiNoStorePath —— 哪些路径的内容是随着 exe 一起变的。
//
// 只有这些：界面本体与 JSON 接口。它们不写缓存头时，浏览器会按启发式规则自己猜一个
// 新鲜期（拿 Last-Modified 的 10% 当期限一类做法），于是换上新版 exe 之后打开的仍是
// 上一份界面 —— 用户看到的是「更新了，毛病还在」。排查音乐放不出来时就撞上过这一条：
// 服务端已经在发修好的页面，标签页里跑的却还是旧的函数。
//
// 音频与背景图明确**不**在此列：它们各有该有的缓存策略，
// /music/ 的 no-cache 让拖动进度不被整段重下，/bg/ 的 immutable 靠名字里的内容哈希。
func guiNoStorePath(p string) bool {
	return p == "/" || strings.HasPrefix(p, "/api/")
}

func guiCachePolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if guiNoStorePath(r.URL.Path) {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func cmdGui(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", guiDefaultPort), "界面监听地址；仅允许绑定本机")
	noOpen := fs.Bool("no-open", false, "只启动本地服务，不打开窗口")
	useBrowser := fs.Bool("browser", false, "使用系统浏览器打开界面")
	web := fs.Bool("web", false, "使用网页版界面；该模式为默认，此开关仅为兼容旧命令行保留")
	native := fs.Bool("native", false, "使用原生界面；Windows 自带控件，内存约 35MB，不占用 GPU")
	keepConsole := fs.Bool("console", false, "保留控制台窗口；默认不保留终端窗口与 conhost 进程")
	noElevate := fs.Bool("no-elevate", false, "不以管理员身份启动；默认在打开界面时申请提权")
	if err := fs.Parse(args); err != nil {
		// flag 已自行打印用法，--help 不算出错
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	// 默认网页版；加 --native 切回原生界面
	useNative := *native && !*web && !*useBrowser && !*noOpen
	addrStr := *addr
	if !guiAllowedHost(addrStr) {
		return fmt.Errorf("界面服务只允许绑定 127.0.0.1（当前为 %s）：该服务仅在本机可用，不应对外暴露", addrStr)
	}

	// 单实例：若已有实例在跑，则把其窗口置前并返回。
	// 必须放在此处，确保桌面快捷方式（gui）第二次点击也走单实例判断。
	if handled, err := guiReuseRunning(addrStr, *noOpen, *useBrowser); handled {
		return err
	}

	// 默认以管理员身份运行（--no-elevate 或 MCLBX_NO_ELEVATE=1 可关）。
	// 只有修改防火墙入站规则需要管理员；提权失败则照常以普通权限继续。
	if !*noElevate && !envNoElevate() && !*noOpen && !isElevated() {
		if err := elevateSelf(); err == nil {
			return nil // 已经交给提权后的实例，本进程不必再往下走
		}
	}

	// --native：原生界面（Windows 自带控件，不起浏览器引擎）
	if useNative && runtime.GOOS == "windows" {
		if !*keepConsole {
			// FreeConsole 释放控制台，否则关闭终端会导致程序退出
			detachConsole()
		}
		guiLog("打开原生界面")
		if err := runNativeUI(&guiController{}); err == nil {
			guiLog("原生界面已关闭，退出")
			return nil
		} else {
			// 原生界面不可用时退回网页版
			guiLog("原生界面失败：%v，改用网页版", err)
			fmt.Printf(" 原生界面起不来（%v），改用网页版界面\n", err)
		}
	}

	ln, err := net.Listen("tcp", addrStr)
	if err != nil {
		// 不自动更换端口，以免掩盖已有实例在运行的情况
		return fmt.Errorf("端口 %s 无法绑定：%w\n"+
			"  · 若为其他程序占用：mclbx gui --addr 127.0.0.1:19871 更换端口\n"+
			"  · 若为另一个 mclbx 实例占用：再次双击原入口，已在运行的窗口将被置于前台",
			addrStr, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// 地址用 localhost：WebView2 导航到 127.0.0.1 会停在 about:blank
	url := fmt.Sprintf("http://localhost:%d/", port)
	// 调试用：MCLBX_GUI_URL 可覆盖界面地址
	if v := strings.TrimSpace(os.Getenv("MCLBX_GUI_URL")); v != "" {
		url = v
		guiLog("界面地址被 MCLBX_GUI_URL 改成 %s", v)
	}

	c := &guiController{}
	mux := http.NewServeMux()

	var pageOnce sync.Once
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		pageOnce.Do(func() {
			guiLog("界面已加载 %s", url)
			// 首次打开页面时按设置自动体检一次（而非进程启动时）
			if autoProbeOn() {
				for i := range guiTasks {
					if guiTasks[i].Key != "probe" {
						continue
					}
					if _, err := c.start(guiTasks[i], nil); err != nil {
						guiLog("自动体检未能启动：%v", err)
					}
					break
				}
			}
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(renderGuiPage()))
	})

	// 静态资源从内嵌文件系统提供
	if sub, err := iofs.Sub(guiAssetFS, "assets"); err == nil {
		fileServer := http.FileServer(http.FS(sub))
		mux.Handle("/assets/", http.StripPrefix("/assets/", fileServer))
	}

	// 背景图片：只按内容哈希取缓存好的那一份，不接受任何路径参数
	mux.HandleFunc("/bg/", serveWall)
	// 曲库里的音频：界面用 <audio> 直接播，拖动进度靠 Range 请求
	mux.HandleFunc("/music/", serveMusic)

	// 说明书内嵌于 exe，以覆盖层加载
	mux.HandleFunc("/manual", serveManual)
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(guiTasksForWeb())
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(c.snapshot(since))
	})
	post := func(h func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// 仅接受带自定义头且同源的请求
			if r.Method != http.MethodPost {
				http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("X-MCLBX-GUI") != "1" {
				http.Error(w, "缺少 X-MCLBX-GUI 头", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !strings.HasPrefix(o, url[:len(url)-1]) {
				http.Error(w, "拒绝跨站请求", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if err := h(w, r); err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			}
		}
	}

	mux.HandleFunc("/api/start", post(func(w http.ResponseWriter, r *http.Request) error {
		var req struct {
			Key    string            `json:"key"`
			Inputs map[string]string `json:"inputs"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			return fmt.Errorf("请求格式不对：%w", err)
		}
		for _, t := range guiTasks {
			if t.Key != req.Key {
				continue
			}
			j, err := c.start(t, req.Inputs)
			if err != nil {
				return err
			}
			return json.NewEncoder(w).Encode(map[string]any{"ok": true, "jobId": j.id, "command": j.command})
		}
		return fmt.Errorf("未知任务：%s", req.Key)
	}))

	mux.HandleFunc("/api/stop", post(func(w http.ResponseWriter, r *http.Request) error {
		c.stop()
		return json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))

	// 设置读写；写入走 post() 的同源校验（设置值会进入命令行）
	settingsSave := post(func(w http.ResponseWriter, r *http.Request) error {
		var req struct {
			guiUIState // 内嵌所有设置项
			// 新增设置项须在此结构体可见，否则服务端收不到
			Action    string `json:"action"`
			AutoProbe *bool  `json:"autoProbe"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			return fmt.Errorf("请求格式不对：%w", err)
		}
		switch req.Action {
		case "forgetInputs":
			if err := forgetSavedInputs(); err != nil {
				return err
			}
			guiLog("界面请求：清除记住的填写内容")
			return json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "openData":
			return openDataDir()
		}
		ui, err := saveUI(req.guiUIState)
		if err != nil {
			return err
		}
		if req.AutoProbe != nil {
			setAutoProbe(*req.AutoProbe)
		}
		return json.NewEncoder(w).Encode(map[string]any{
			// logKeep 与 GET /api/settings 保持同一种形态（十进制字符串）：
			// 同一个字段两条路给两种类型，客户端迟早要分叉。
			"ok": true, "ui": settingsPayload(ui), "logKeep": strconv.Itoa(ui.logKeep()),
		})
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			settingsSave(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(settingsPayload(loadUI()))
	})

	// 导入背景图：网页界面用 <input type=file> 取到文件再 POST 上来。
	// 走上传而不是让服务端按路径去读，是因为「导入」的意义就在于把图复制进存档 ——
	// 存完之后原文件改名、移走、删掉都不影响。
	mux.HandleFunc("/api/wall/import", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fail := func(msg string) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": msg})
		}
		if r.Method != http.MethodPost {
			fail("只接受 POST")
			return
		}
		// 上限挡在解析之前：请求体一超就直接断，不先把内容读进内存
		r.Body = http.MaxBytesReader(w, r.Body, wallImportMaxBytes)
		if err := r.ParseMultipartForm(wallImportMaxBytes); err != nil {
			fail("图片超过 32MB 或上传中断了")
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			fail("没有收到图片")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			fail("读取上传内容失败")
			return
		}
		name, err := importWallFile(hdr.Filename, data)
		if err != nil {
			fail(err.Error())
			return
		}
		// 导入即选中：省掉「导入完还要再去列表里点一下」这一步
		cur := loadUI()
		cur.BgImage = name
		ui, err := saveUI(cur)
		if err != nil {
			fail("图片已存下，但设置没能写入：" + err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(settingsPayload(ui))
	})

	mux.HandleFunc("/api/quit", post(func(w http.ResponseWriter, r *http.Request) error {
		// 先写回执，再异步执行关闭
		err := json.NewEncoder(w).Encode(map[string]any{"ok": true})
		go func() {
			guiLog("界面请求退出")
			// 先把关闭请求交给界面线程（先隐藏窗口），再停任务与关闭
			if requestClose() {
				// 窗口关闭后走正常退出流程
				return
			}
			// 无内置窗口时自行收尾并退出
			c.stop()
			os.Exit(0)
		}()
		return err
	}))

	srv := &http.Server{
		// 缓存策略在这里统一决定，不散在各个 handler 里：
		// 新增一个 /api/ 接口时不会漏掉，也不可能顺手把音频的 Range 缓存抹掉。
		Handler: guiCachePolicy(mux),
		// 各阶段均设明确时限，避免单个卡住的连接阻塞其他请求
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          log.New(io.Discard, "", 0), // 丢弃 http 自身日志，避免混入任务输出
	}

	// 默认释放控制台（非仅隐藏）；保留终端加 --console
	if runtime.GOOS == "windows" && !*keepConsole && !*noOpen {
		detachConsole()
	}

	fmt.Printf("================================================\n")
	fmt.Printf(" %s · 控制台\n", version)
	fmt.Printf("================================================\n")
	fmt.Printf(" 界面地址 : %s\n", url)
	fmt.Printf(" 安全说明 : 只绑本机回环（127.0.0.1 / ::1），同一网络里的其他机器访问不到\n")
	fmt.Printf(" 结束方式 : 关掉窗口，或按 Ctrl+C\n\n")

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	// 同时监听 IPv6 回环 ::1，因 localhost 可能先解析到 ::1
	if ln6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port)); err == nil {
		go func() { _ = srv.Serve(ln6) }()
	}

	// 默认用内置窗口（WebView2）；--browser 或内置窗口失败时用浏览器打开。
	// openURL 与 url 分开：同源检查以 url 为基准，给 url 挂查询串会破坏它。
	openURL := url
	if !*noOpen && !*useBrowser && runtime.GOOS == "windows" {
		guiLog("打开内置窗口")
		if err := runNativeWindow(url, guiWindowTitle); err == nil {
			c.stop() // 窗口关闭即退出，收掉在跑的任务
			guiLog("窗口已关闭，退出")
			_ = srv.Close()
			return nil
		} else {
			guiLog("内置窗口失败：%v", err)
			// 不弹出黑框，改为在页面顶部提示原因
			msg := fmt.Sprintf("内置窗口起不来（%v），已改用浏览器打开", err)
			fmt.Printf(" %s\n", msg)
			openURL = url + "?notice=" + neturl.QueryEscape(msg)
		}
	}
	if !*noOpen {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(openURL); err != nil {
				fmt.Printf(" 打开界面失败（%v），请手动访问上面的地址\n", err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
		fmt.Printf("\n正在关闭控制台…\n")
		c.stop()
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	}
	_ = srv.Close()
	return nil
}

// guiDefaultPort 控制台默认端口，单实例检测也用它。
const guiDefaultPort = 19870

// guiWindowTitle 界面窗口标题，与桌面快捷方式名称一致。
const guiWindowTitle = "mclbx 联机工具"

// hasExistingGuiWindow 判断是否已有打开的界面窗口，做成变量以便测试替换。
var hasExistingGuiWindow = findExistingGuiWindow

// guiReuseRunning 处理已有实例：有窗口则置前，无窗口则为其补开窗口，返回 handled=true。
func guiReuseRunning(addr string, noOpen, useBrowser bool) (bool, error) {
	if hasExistingGuiWindow() {
		return true, nil
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || !guiRunningOn(host, port) {
		return false, nil
	}

	if noOpen {
		return true, nil // 本次也不需要窗口，已有实例即可
	}
	u := fmt.Sprintf("http://localhost:%d/", port)
	if useBrowser || runtime.GOOS != "windows" {
		return true, openBrowser(u)
	}
	if err := runNativeWindow(u, guiWindowTitle); err != nil {
		return true, fmt.Errorf("已有 mclbx 实例在运行（%s），但无法将其窗口置于前台：%w", u, err)
	}
	return true, nil
}

// guiRunningOn 判断端口上运行的是否为本程序的界面。
//
// 仅凭可连接不够：须要求 /api/tasks 返回含 key / group 字段的任务表。
func guiRunningOn(host string, port int) bool {
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{
		Timeout: 900 * time.Millisecond,
		// 不跟随重定向，避免把跳转后含 key/group 文本的服务误判为本程序
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d/api/tasks", host, port))
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false
	}
	s := string(body)
	return strings.Contains(s, `"key"`) && strings.Contains(s, `"group"`)
}

// GUI 日志：界面模式下控制台隐藏，日志文件用于排查
var guiLogMu sync.Mutex

// 日志轮转：单文件上限，超出即滚动为 gui.log.1（只保留一份）。
const guiLogMax = 1 << 20

func guiLog(format string, a ...any) {
	guiLogMu.Lock()
	defer guiLogMu.Unlock()
	dir := dataDir()
	if dir == "" {
		return // 一个能写的位置都没有：这条日志只能放弃，但不能因此阻塞界面
	}
	path := filepath.Join(dir, "gui.log")
	if st, err := os.Stat(path); err == nil && st.Size() > guiLogMax {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

// openBrowser 打开界面：Windows 优先用 Edge/Chrome 应用模式，否则用默认浏览器。
func openBrowser(url string) error {
	if runtime.GOOS == "windows" {
		for _, c := range []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		} {
			if st, err := os.Stat(c); err != nil || st.IsDir() {
				continue
			}
			cmd := exec.Command(c, "--app="+url, "--window-size=1280,880", "--no-first-run",
				"--disable-features=msEdgeSidebarV2")
			if err := cmd.Start(); err == nil {
				fmt.Printf(" 已用应用模式打开窗口（%s）\n", filepath.Base(c))
				return nil
			}
		}
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
