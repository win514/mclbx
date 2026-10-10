// mclbx 是《我的世界》Java 版联机工具。
package main

import (
	"fmt"
	"os"
	"time"
)

// 版本号可用 -ldflags "-X main.version=..." 覆盖。
var version = "mclbx 0.1"

func logf(format string, a ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

// usageText 是帮助文本原文。
const usageText = `mclbx —— 我的世界 Java 版联机工具

用法：
  mclbx gui [选项]         打开图形界面；已有实例时将其窗口置于前台
                           默认使用网页版界面；加 --native 使用原生界面
                           默认申请管理员权限，用于自动配置防火墙入站规则；
                           加 --no-elevate 或设 MCLBX_NO_ELEVATE=1 可关闭
  mclbx room [选项]        同时开启公网入口与软件入口
  mclbx join [选项]        以软件方式进入房间；加 --relay 可替其他玩家中继转发
  mclbx expose [选项]      只开启公网入口
  mclbx dns local          启动本地 DNS，将短名称映射到本机地址与端口
  mclbx dns publish        将同一组记录写入公网 DDNS；默认只打印、不写入
  mclbx ice host|guest     通过 ICE 建立直连模式通道，并在其上承载 TCP 隧道
  mclbx punch [选项]       端口盲扫：仅诊断，确认双对称 NAT 下是否存在可打穿的路径
  mclbx natmap [选项]      通过 UPnP / NAT-PMP / PCP 建立自动端口映射
  mclbx firewall [选项]    Windows 入站规则：放行本程序与指定端口
  mclbx doctor [选项]      环境体检：给出可用的连接方式与下一步命令
  mclbx diag [选项]        导出诊断包：版本、系统与网卡、体检结论、最近一次日志
  mclbx relaybox [选项]    将本机作为中继服务，开启 UDP 中继、TCP 中继转发与信令信箱
  mclbx stun [选项]       启动 STUN / TURN 服务端
  mclbx relay [选项]       TCP 中继转发；UDP 不可用时使用
  mclbx tcptunnel host|guest   TCP 兜底通道两端，双方均主动连接中继
  mclbx relaycheck [链接]  校验中继链接，并指出失败所在步骤
  mclbx ping [选项]        按游戏协议查询目标，返回版本号与在线人数
  mclbx verify [选项]      按真实客户端顺序校验地址，逐步骤报告结果
  mclbx probe              路况诊断：全球 IPv6 / IPv4 公网映射 / NAT 行为 / 自动端口映射
  mclbx forget             清除界面记住的输入，下次打开恢复默认值
  mclbx slpfake            排查用：仅应答服务器列表查询的假服务端
  mclbx profile [档位]     列出暴露档位；给档位则打印它展开成的命令
  mclbx help               显示本帮助

expose 选项：
  --port <端口>      本机游戏端口；不填则自动探测 25565 / 25566 / 25567
  --entry <端口>     入口端口；默认优先占用 25565
  --mode <形式>      auto：默认值。需要门槛时用域名形式（房间码编进玩家要填的名字），
                     否则用 IPv6 字面量；字面量不经过第三方，但没有门槛
                     raw：向玩家发送 IPv6 字面量，不注册域名，无第三方依赖；没有门槛
                     dns：使用 sslip.io 等公共通配域名；名称较短，但多一项依赖
  --room <房间码>    仅 dns 模式：玩家输入的名称必须包含房间码，以此为口令
  --dns <后缀>       仅 dns 模式：公共通配 DNS 后缀，默认 sslip.io
  --allow <名字>     仅允许这些玩家名，逗号分隔；raw 模式建议同时配置
  --web <地址>       本地管理页监听地址，默认 127.0.0.1:8080；填 off 关闭
  --no-gate          关闭房间码校验；不推荐，等同于公开入口
  --no-map           不启用自动端口映射
  --stun             使用 STUN 服务探测 IPv4 出口 IP

room 选项：
  --port <端口>      本机游戏端口；不填则自动探测
  --entry <端口>     入口端口；默认优先占用 25565
  --to <地址>        转发目标；默认与本机游戏端口一致
  --signal <地址>    软件入口的信令监听地址，默认 :8090（双栈，IPv4 与 IPv6 都收）
  --room <房间码>    默认随机 6 位；软件入口会校验该房间码
  --no-software      只开启公网入口
  --relay-server / --relay-user / --relay-pass   中继服务，写法见「中继服务器」一节
  --mode / --dns / --allow / --no-gate / --web / --stun   含义同 expose

join 选项：
  --host <地址>      房主侧信令地址，例如 [2408:...]:8090；room 命令会输出该地址
                     若填写带 sig= 的中继链接，此项可留空，程序会自动取出
  --room <房间码>    房主提供的房间码；链接中已包含时可留空
  --local <地址>     隧道在本机的监听地址，默认 127.0.0.1:25565
  --relay            同时将本机公网地址作为入口，替其他玩家中继转发
  --relay-entry <端口>  中继转发入口端口，默认 25580
  --relay-allow <名字>  中继转发入口仅允许这些玩家名，逗号分隔
  --relay-server / --relay-user / --relay-pass   中继服务，写法见「中继服务器」一节
  --stun / --wait    是否使用 STUN 收集公网候选 / 等待房主候选的最长时长

ping 选项：
  --host <地址>       目标地址，可为域名
  --port <端口>       目标端口；显式指定时不查询 SRV，与真实客户端规则一致
  --server-name <名字>  握手包中填写的服务器地址，模拟玩家输入的名称
  --timeout <时长>    超时，默认 5s
  --no-srv            不查询 SRV 记录，直接按端口连接

dns local 选项：
  --zone <区>         本服务权威的域名区，默认 mclbx.test
  --name <名字>       玩家输入的短名称，必须位于 --zone 内
  --port <端口>       SRV 记录中的端口，即客户端最终连接的端口
  --target <名字>     SRV 的目标名称，默认与 --name 相同
  --addr <地址>       地址记录指向的地址，默认自动选择本机全局 IPv6
  --upstream <DNS>    区外名称的转发目标，填写原 DNS；不填则拒绝应答
  --listen <地址>     监听地址，默认 127.0.0.1:53
  --ttl / --txt / --quiet

dns publish 选项：
  --provider <名字>   域名服务商，目前支持 dynv6
  --zone <区>         所拥有的域名区，例如 myname.dynv6.net
  --name <子域名>     区内的记录名，例如 abc123；留空表示使用区本身
  --port <端口>       SRV 记录中的端口
  --addr <地址>       默认自动选择本机全局 IPv6
  --token <令牌>      接口令牌，也可使用环境变量 MCLBX_DDNS_TOKEN
  --apply             实际写入；不加此参数仅打印将要发送的请求
  --watch <时长>      监视模式：地址变化后自动重写

ice host 选项：
  --signal <地址>     信令信箱监听地址，默认 :8090（双栈）；客户机需可访问
  --to <地址>         通道建立后的转发目标，默认 127.0.0.1:25566
  --transport <类型>  隧道传输层：sctp 或 raw；sctp 为默认值，可靠有序且带流控
  --relay-server <链接>  中继服务；直连失败时使用，写法见「中继服务器」一节
  --relay-user / --relay-pass   中继账号；链接中未提供时使用
  --tamper-fp         排查用：公布错误的证书指纹，用于检验绑定是否拒绝连接
  --stun / --wait     是否使用 STUN 收集公网候选 / 等待对端候选的最长时长

ice guest 选项：
  --host <地址>       主机侧信令信箱；VirtualBox NAT 下主机的别名为 10.0.2.2:8090
  --local <地址>      隧道在本机的监听地址，默认 127.0.0.1:25565
  --transport <类型>  同 host 侧；两端必须一致
  --relay-server / --relay-user / --relay-pass   同 host 侧，两端建议填写同一台
  --tamper-fp         同 host 侧

punch 选项：
  --peer <IP>         对端的公网 IP
  --ports <起-止>     要扫描的对端端口范围，默认 30000-30099
  --tag <口令>        仅接受带此口令的探针；两端必须一致
  --local <端口>      本地绑定端口，默认随机
  --interval <时长>   两轮探测之间的间隔，默认 300ms
  --tries <次数>      重试次数，默认 3；每次更换一个新的本地端口
  --per-try <时长>    单次尝试的探测时长，默认 8s
  两端各运行一次；端口范围越小，成功率越高，双方各扫 N 个端口时约 63%。

natmap 选项：
  --port <端口>       要映射的本机端口
  --proto <类型>      tcp / udp / both；游戏入口使用 tcp，ICE 使用 udp
  --ext <端口>        要占用的外网端口，默认与内网端口相同
  --keep              保持运行并自动续约，Ctrl+C 退出并撤销映射
  依次尝试 UPnP IGD、NAT-PMP、PCP；成功一条即使用，全部失败时逐条说明原因。

firewall 选项：
  --port <端口>       要放行的入站端口；可用逗号分隔多个，如 25565,8090
  --proto <类型>      tcp / udp / both
  --remove            删除本工具添加的规则，规则名带 mclbx- 前缀
  --dry-run           只打印将要执行的命令，不改动系统
  本工具有两个默认入站端口：游戏端口（公网入口）与信令端口 8090（软件入口）。
  仅放行前者会出现"未安装工具的玩家能进、已安装的反而不行"，建议两个一起放行。
  仅放行"本程序与指定端口"，不改动防火墙开关，不修改其他规则；需要管理员权限，会弹出一次 UAC。

tcptunnel 选项：
  relay：运行在具有公网 IP 的机器上，--listen <地址>，默认 :8090（双栈）
  host ：--relay <中继地址> --room <房间码> --to <本地目标>
  guest：--relay <中继地址> --room <相同房间码> --local <本机监听地址>
  仅使用 TCP；网络屏蔽 UDP 时可使用该通道。
  流量经中继转发且为明文传输，房间码是唯一门槛，因此仅作为最后手段。
  调试：环境变量 MCLBX_TCP_DEBUG=1 会输出每一帧的类型、连接号与长度。

中继服务器：
  房主可填写一个链接，作为直连失败时的兜底通道。
  所有接受该参数的位置均为 --relay-server，后接一个链接。支持的写法：

    turn:用户:密码@主机:3478      TURN 服务器；自建优先，商用 TURN 写法相同
    turn:主机:3478                账号另行提供：--relay-user / --relay-pass
    turns:主机:5349               TURN over TLS
    turn:主机:3478?secret=密钥     使用 REST 临时凭据；客户端按共享密钥计算 HMAC 密码
    tcp:主机:8090                 仅使用 TCP 的中继，即 mclbx relay
    主机:端口                      未知类型：先按 TURN 使用，兜底时按 TCP 使用
    mclbx://主机?turn=3478&tcp=8090&sig=8090&room=abc123&user=u&pass=p
                                  一条链接包含中继、信令与房间码
  多个链接使用逗号分隔，按顺序尝试。
  环境变量 MCLBX_RELAY / MCLBX_TURN 仍然有效。
  启动日志会输出"中继服务器："一行，便于确认配置是否生效。
  校验链接是否可用：mclbx relaycheck <链接>
  若没有可用的中继服务器，可运行 mclbx relaybox 将本机作为中继服务；
  其输出的链接已包含账号，对方无需再填写密码。
  手写 turn:主机:3478 等未带账号的写法时，才需要 --relay-user / --relay-pass。

relaybox 选项：
  --turn-port <端口>   UDP 中继端口，默认 3478
  --tcp-port <端口>    TCP 通道端口，默认 8090
  --signal-port <端口> 信令信箱端口，默认 8091
  --public <地址>      本机对外地址；不填则自动探测，优先公网 IPv6
  --room <房间码>      不填则随机生成，并写入输出链接
  --secret <密钥>      中继密钥；不填则每次随机，密钥包含在链接中
  --no-turn            不开启 UDP 中继，仅保留 TCP 通道
  --no-signal          不开启信令信箱
  --no-open-ports      不修改路由器；不加此参数时将尝试自动端口映射
  启动后输出一行链接，发送给其他玩家，填入「中继服务器」即可使用。
  没有公网地址时也可运行：支持 UPnP 的路由器上会自动建立 TCP 端口映射，
  TCP 通道即可满足联机；UDP 中继需要公网地址，无法开启时会明确提示。

stun 选项：
  --port <端口>      监听端口，默认 3478，UDP
  --turn             同时开放 TURN 中继服务；直连失败时使用，需要指定中继地址
  --relay-ip <IP>    中继地址；不填则自动选择本机第一个非回环 IPv4
  --udp-buffer <字节>  UDP 收发缓冲区；默认 4 MiB，高带宽中继可调大，0 表示用系统默认
  --user / --pass    中继账号，默认 mclbx / mclbx；长期运行建议改用 --rest 密钥
  客户端侧：通过环境变量 MCLBX_STUN=stun:地址:端口 指定 STUN 服务，逗号分隔可填多个

relaycheck 选项：
  --user / --pass     链接中未提供账号时使用
  --timeout <时长>    每一步的超时，默认 8s
  --room <房间码>     仅 TCP 中继自检使用；留空则自动生成
  校验过程会实际执行：解析地址、探测映射、申请分配并校验账号、建立两个分配并互传数据。
  失败时输出停止的步骤与原因，例如 401 账号错误、400 凭据时间戳过期、超时。

gui 选项：
  --native            使用原生界面；Windows 自带控件，内存约 35MB，不占用 GPU
  --web               使用网页版界面；该模式为默认，此开关仅为兼容旧命令行保留
  --addr <地址>       界面监听地址，默认 127.0.0.1:19870；仅允许绑定本机
  --no-open           只启动本地服务，不打开窗口
  --browser           使用系统浏览器打开界面
  --console           保留控制台窗口
  --no-elevate        不以管理员身份启动；默认在打开界面时申请提权
  双击程序本体或桌面快捷方式均进入此命令。已有实例运行时，仅将其窗口置于前台；
  端口被其他程序占用时会直接报错，不会自动更换端口。
  界面提供三项功能：选择操作、查看实时日志、复制给玩家的地址。
  每个操作会调用对应的子命令，界面会显示其等价命令。

身份绑定：两端各自生成自签证书并交换 SHA-256 指纹，随后在专用流上完成一次挑战-应答校验。
只有校验通过且客户机回执确认后，通道才算建立；任一步失败即断开并说明原因。
绑定完成后两端各显示一个安全码，比对一致即可排除中间人。

SRV 路线：
  房主将「短名称 -> 当前地址与端口」写入 DNS（AAAA 与 SRV 记录）。
  玩家在「直接连接」中输入该短名称即可。
  游戏客户端会查询 _minecraft._tcp.<名字> 的 SRV 记录，获取实际主机与端口，无需填写端口。
  SRV 的目标按 RFC 2782 必须为域名，不能使用 IP 字面量。
`

func usage() {
	fmt.Print(usageText)
}

func main() {
	// GDI+ 释放属进程级收尾，放在这里而非关窗口处。
	defer gpStop()

	// 不带参数（双击本体）时打开界面。
	if len(os.Args) < 2 {
		// 单实例判断由 cmdGui 处理。
		os.Args = append(os.Args, "gui")
	}
	var err error
	switch os.Args[1] {
	case "gui":
		err = cmdGui(os.Args[2:])
	case "expose":
		err = cmdExpose(os.Args[2:])
	case "room":
		err = cmdRoom(os.Args[2:])
	case "join":
		err = cmdJoin(os.Args[2:])
	case "stun":
		err = cmdStun(os.Args[2:])
	case "mailbox":
		err = cmdMailbox(os.Args[2:])
	case "probe":
		err = cmdProbe(os.Args[2:])
	case "ice":
		err = cmdIce(os.Args[2:])
	case "punch":
		err = cmdPunch(os.Args[2:])
	case "relay":
		err = cmdRelay(os.Args[2:])
	case "relaybox":
		err = cmdRelayBox(os.Args[2:])
	case "tcptunnel":
		err = cmdTcpTunnel(os.Args[2:])
	case "relaycheck":
		err = cmdRelayCheck(os.Args[2:])
	case "natmap":
		err = cmdNatmap(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "diag":
		err = cmdDiag(os.Args[2:])
	case "firewall":
		err = cmdFirewall(os.Args[2:])
	case "dns":
		err = cmdDNS(os.Args[2:])
	case "slpfake":
		err = cmdSLPFake(os.Args[2:])
	case "ping":
		err = cmdPing(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "forget":
		err = cmdForget(os.Args[2:])
	case "profile":
		err = cmdProfile(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n\n", os.Args[1])
		usage()
		gpStop()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		// 补充中文提示与下一步，见 errhint.go。
		for _, h := range explainError(err.Error()) {
			fmt.Fprintln(os.Stderr, h.text)
		}
		gpStop()
		os.Exit(1)
	}
}
