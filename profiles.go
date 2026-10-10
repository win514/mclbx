package main

// profiles.go 定义暴露档位：把「门槛、中继、系统设置」等默认值收拢成一次选择。
// 档位只是预设，展开成一串现有参数，落到已有子命令上；本文件不改变各子命令的默认值。
// 数据只保留策略粒度（是否公开、要不要门槛、要不要中继），端口与域名等细节归各子命令。

import (
	"fmt"
	"strings"
)

// exposureProfile 一个档位的全部信息。Route 为空表示这一档不需要起入口。
type exposureProfile struct {
	ID       string // 稳定标识，出现在命令行输出中
	Title    string // 档位名称
	Tradeoff string // 代价说明，直接列给用户
	Note     string // 展开之后是什么，写给用户与排查者

	Route      string // 展开成哪个子命令（expose / room）
	Mode       string // --mode 的取值
	Gate       bool   // 是否开启进门门槛（房间码写进玩家填入的地址）
	NeedRoom   bool   // 展开时是否必须给出房间码
	NeedsRelay bool   // 展开时是否必须给出一个中继
	NoMap      bool   // 是否显式关掉自动端口映射
}

// exposureProfiles 五个档位，按从最公开到最保守排列。
var exposureProfiles = []exposureProfile{
	{
		ID: "P0", Title: "仅内网",
		Tradeoff: "不出网：适合同宿舍、同办公室，代价是外部玩家无法连接。",
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
		Tradeoff: "房主地址仍会出现，但仅持有房间码的玩家可进入：适合熟人之间联机。",
		Note:     "公网入口 + 地址形式 dns + 房间码门槛；房间码藏在玩家填入的地址里。",
		Route:    "expose", Mode: "dns", Gate: true, NeedRoom: true,
	},
	{
		ID: "P3", Title: "隐藏房主",
		Tradeoff: "需要一台非房主机的机器当中继，否则流量只能落在本机。",
		Note:     "公网入口与软件入口，经指定的那台中继兜底；中继运行在另一台机器上，房主地址才不出现在链接里。",
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

// expandProfile 把档位翻译成一串现有参数，首项是子命令；为纯函数。
// room 与 relay 由调用方给出，缺输入时报错，不套用默认值。
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
			return nil, fmt.Errorf("档位 %s（%s）需要一台中继：把中继放到一台非房主机的机器上运行，"+
				"再用 --relay-server <中继链接> 指定", p.ID, p.Title)
		}
		args = append(args, "--relay-server", strings.TrimSpace(relay))
	}
	if p.NoMap {
		args = append(args, "--no-map")
	}
	return args, nil
}

// cmdProfile 只读：列出档位，或打印某一档展开成的命令；不改设置、不起服务。
func cmdProfile(args []string) error {
	if len(args) == 0 {
		fmt.Println("暴露档位：先选“这台机器愿意暴露多少”，其余默认值由档位展开。")
		fmt.Println("档位只是预设，不引入新链路：展开结果为一串现有命令，也可自行手写。")
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
		return fmt.Errorf("未知档位：%q；可选 %s", args[0], strings.Join(profileIDs(), " / "))
	}

	fmt.Printf("  %s  %s\n", p.ID, p.Title)
	fmt.Printf("      %s\n", p.Tradeoff)

	// 展开用的输入以占位符顶上，便于展示命令形状。
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
