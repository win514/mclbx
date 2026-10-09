package main

// profiles.go —— 暴露档位。
//
// 这个文件回答一个此前散在各处的问题：这台机器愿意对公网暴露多少。
// 它把“要不要门槛、要不要中继、许不许改系统设置”这些默认值收拢成一次选择，
// 而不是让用户从十几个开关里自己拼。
//
// 三条自我约束，都写成了用例（见 profiles_test.go）：
//
//	1. 档位只是预设，不是新链路。每个档位展开后都是一串**现有**参数，落到某个
//	   已有子命令上。判定方式：展开结果的第一项必须在 main.go 的子命令清单里。
//	2. 档位不改变默认值。不给 --profile 时，各子命令的行为与引入档位之前完全一致；
//	   本文件不碰任何现有默认值。
//	3. 档位不下线任何链路。P0 到 P4 都能落到现有六条链路上的某一条。
//
// 字段只保留“策略”粒度（愿不愿意公开、要不要门槛、要不要中继），
// 端口、域名、后缀这类细节仍归各子命令自己。

import (
	"fmt"
	"strings"
)

// exposureProfile 一个档位的全部信息。Route 为空表示这一档不需要起入口。
type exposureProfile struct {
	ID       string // 稳定标识，出现在命令行输出里
	Title    string // 一句话名字
	Tradeoff string // 代价说明，直接列给用户看
	Note     string // 展开之后是什么，写给用户和排查者看

	Route      string // 展开成哪个子命令（expose / room）
	Mode       string // --mode 的取值
	Gate       bool   // 是否开启进门门槛（房间码写进玩家填入的地址）
	NeedRoom   bool   // 展开时是否必须给出房间码
	NeedsRelay bool   // 展开时是否必须给出一个中继
	NoMap      bool   // 是否显式关掉自动端口映射
}

// exposureProfiles 五个档位。顺序即列表顺序，也是“从最公开到最保守”的方向。
var exposureProfiles = []exposureProfile{
	{
		ID: "P0", Title: "仅内网",
		Tradeoff: "不出网：适合同宿舍、同办公室，代价是外面的人连不上。",
		Note:     "不需要起入口：让同一局域网内的玩家直接连本机地址即可。",
		Route:    "",
	},
	{
		ID: "P1", Title: "公开直连",
		Tradeoff: "房主地址会出现在分享内容里，且没有门槛：适合开公开服或做直播。",
		Note:     "公网入口 + 地址形式 raw + 关闭门槛。",
		Route:    "expose", Mode: "raw", Gate: false,
	},
	{
		ID: "P2", Title: "私密直连",
		Tradeoff: "房主地址仍会出现，但只有拿到房间码的人进得来：适合熟人局。",
		Note:     "公网入口 + 地址形式 dns + 房间码门槛；房间码藏在玩家填入的地址里。",
		Route:    "expose", Mode: "dns", Gate: true, NeedRoom: true,
	},
	{
		ID: "P3", Title: "隐藏房主",
		Tradeoff: "需要一台不是你房主机的机器当中继，否则流量只能落在本机。",
		Note:     "公网入口与软件入口，经你指定的那台中继兜底；中继跑在别的机器上，房主地址才不出现在链接里。",
		Route:    "room", Mode: "dns", Gate: true, NeedRoom: true, NeedsRelay: true,
	},
	{
		ID: "P4", Title: "受管环境",
		Tradeoff: "不自动做端口映射、不写防火墙规则：适合学校或公司网络。",
		Note:     "公网入口 + 地址形式 dns + 房间码门槛，并显式关掉自动端口映射。",
		Route:    "expose", Mode: "dns", Gate: true, NeedRoom: true, NoMap: true,
	},
}

// profileIDs 全部档位标识，按声明顺序。
func profileIDs() []string {
	out := make([]string, 0, len(exposureProfiles))
	for _, p := range exposureProfiles {
		out = append(out, p.ID)
	}
	return out
}

// profileByID 查档位，大小写不敏感。
func profileByID(id string) (exposureProfile, bool) {
	want := strings.ToUpper(strings.TrimSpace(id))
	for _, p := range exposureProfiles {
		if p.ID == want {
			return p, true
		}
	}
	return exposureProfile{}, false
}

// expandProfile 把档位翻译成一串现有参数，首项是子命令。
//
// 它是纯函数：不解析命令行、不读配置、不落盘、不改任何默认值。
// room 与 relay 由调用方给出（缺了该给的输入就报错，而不是悄悄用默认值顶上）。
func expandProfile(p exposureProfile, room, relay string) ([]string, error) {
	if p.Route == "" {
		return nil, fmt.Errorf("档位 %s（%s）不需要起入口：%s", p.ID, p.Title, p.Note)
	}
	args := []string{p.Route, "--mode", p.Mode}
	if !p.Gate {
		args = append(args, "--no-gate")
	}
	if p.NeedRoom {
		if strings.TrimSpace(room) == "" {
			return nil, fmt.Errorf("档位 %s（%s）需要房间码：请给出 --room <房间码>", p.ID, p.Title)
		}
		args = append(args, "--room", strings.TrimSpace(room))
	}
	if p.NeedsRelay {
		if strings.TrimSpace(relay) == "" {
			return nil, fmt.Errorf("档位 %s（%s）需要一台中继：把中继放到一台不是你房主机的机器上运行，"+
				"再用 --relay-server <中继链接> 指过来", p.ID, p.Title)
		}
		args = append(args, "--relay-server", strings.TrimSpace(relay))
	}
	if p.NoMap {
		args = append(args, "--no-map")
	}
	return args, nil
}

// cmdProfile 只读：列出档位，或打印某一档展开成的命令。
// 它不改任何设置、不起任何服务，所以可以放心让用户先看再选。
func cmdProfile(args []string) error {
	if len(args) == 0 {
		fmt.Println("暴露档位：先选“这台机器愿意暴露多少”，其余默认值由档位展开。")
		fmt.Println("档位只是预设，不是新链路 —— 它展开成的命令你也可以手工敲。")
		fmt.Println()
		for _, p := range exposureProfiles {
			fmt.Printf("  %s  %s\n", p.ID, p.Title)
			fmt.Printf("      %s\n", p.Tradeoff)
		}
		fmt.Println()
		fmt.Println("看某一档展开成什么：mclbx profile P2")
		return nil
	}

	p, ok := profileByID(args[0])
	if !ok {
		return fmt.Errorf("没有这个档位：%q；可选 %s", args[0], strings.Join(profileIDs(), " / "))
	}

	fmt.Printf("  %s  %s\n", p.ID, p.Title)
	fmt.Printf("      %s\n", p.Tradeoff)

	// 展开要用的输入用占位符顶上，好让用户看到命令的形状。
	room, relay := "", ""
	if p.NeedRoom {
		room = "<房间码>"
	}
	if p.NeedsRelay {
		relay = "<中继链接>"
	}
	argv, err := expandProfile(p, room, relay)
	if err != nil {
		fmt.Printf("      展开：%v\n", err)
		return nil
	}
	fmt.Printf("      展开：mclbx %s\n", strings.Join(argv, " "))
	fmt.Printf("      %s\n", p.Note)
	return nil
}
