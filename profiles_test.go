package main

// profiles_test.go 暴露档位自我约束的可执行形式：
// 展开出的子命令必须已存在，用到的开关必须已定义，缺输入必须报错，档位标识必须稳定。

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// routeSourceFile 记录档位展开出的子命令定义在哪个文件，用于核对开关确实存在。
var routeSourceFile = map[string]string{
	"expose": "expose.go",
	"room":   "room.go",
}

// 五个档位的标识与顺序是稳定契约，界面与文档均按它引用。
func TestExposureProfileIDsAreStable(t *testing.T) {
	want := []string{"P0", "P1", "P2", "P3", "P4"}
	got := profileIDs()
	if len(got) != len(want) {
		t.Fatalf("档位是 %d 个（%v），期望 %d 个（%v）", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个档位是 %q，期望 %q —— 档位标识是配置与文档的引用目标，不能改",
				i+1, got[i], want[i])
		}
	}
}

// 每个档位都要有标题、代价说明与展开说明。
func TestExposureProfilesAreFullyDescribed(t *testing.T) {
	for _, p := range exposureProfiles {
		if p.Title == "" {
			t.Errorf("档位 %s 没有标题", p.ID)
		}
		if p.Tradeoff == "" {
			t.Errorf("档位 %s 没有代价说明 —— 档位的意义就是把这笔代价讲清楚", p.ID)
		}
		if p.Note == "" {
			t.Errorf("档位 %s 没有展开说明", p.ID)
		}
	}
}

// 档位只是预设：展开结果的第一项必须是 main.go 认得的子命令，不得引入新命令。
func TestExposureProfilesRouteToKnownSubcommands(t *testing.T) {
	known := map[string]bool{}
	for _, name := range cliSurfaceBaseline {
		known[name] = true
	}
	for _, p := range exposureProfiles {
		if p.Route == "" {
			continue // P0 不起入口，下面单独检查它的报错
		}
		if !known[p.Route] {
			t.Errorf("档位 %s 展开成子命令 %q，它不在子命令清单里 —— 档位不许引入新链路",
				p.ID, p.Route)
		}
	}
}

// 展开是确定的：相同输入必须得到相同命令，且与预期一致。
func TestExposureProfilesExpandExactly(t *testing.T) {
	const room = "abc123"
	const relay = "turn:1.2.3.4:3478"
	cases := []struct {
		id    string
		args  []string
		fails bool
	}{
		{id: "P0", fails: true},
		{id: "P1", args: []string{"expose", "--mode", "raw", "--no-gate"}},
		{id: "P2", args: []string{"expose", "--mode", "dns", "--room", room}},
		{id: "P3", args: []string{"room", "--mode", "dns", "--room", room, "--relay-server", relay}},
		{id: "P4", args: []string{"expose", "--mode", "dns", "--room", room, "--no-map"}},
	}
	for _, c := range cases {
		p, ok := profileByID(c.id)
		if !ok {
			t.Fatalf("找不到档位 %s", c.id)
		}
		got, err := expandProfile(p, room, relay)
		if c.fails {
			if err == nil {
				t.Errorf("档位 %s 应当报错（它不起入口），却展开成了 %v", c.id, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("档位 %s 展开失败：%v", c.id, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(c.args, " ") {
			t.Errorf("档位 %s 展开成\n  %v\n期望\n  %v", c.id, got, c.args)
		}
	}
}

// 缺少必要输入必须报错，不得以随机值或默认值代替，否则门槛看似开启实则未生效。
func TestExposureProfilesRefuseMissingInput(t *testing.T) {
	const room = "abc123"
	const relay = "turn:1.2.3.4:3478"
	cases := []struct {
		id          string
		room, relay string
		wantErr     string // 报错中必须出现的关键词，用于确认报的是该错误
	}{
		{id: "P2", room: "", relay: "", wantErr: "房间码"},
		{id: "P3", room: "", relay: relay, wantErr: "房间码"},
		{id: "P3", room: room, relay: "", wantErr: "中继"},
		{id: "P4", room: "", relay: "", wantErr: "房间码"},
	}
	for _, c := range cases {
		p, ok := profileByID(c.id)
		if !ok {
			t.Fatalf("找不到档位 %s", c.id)
		}
		got, err := expandProfile(p, c.room, c.relay)
		if err == nil {
			t.Errorf("档位 %s（room=%q relay=%q）缺输入却没有报错，展开成了 %v",
				c.id, c.room, c.relay, got)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("档位 %s 的报错是 %q，没有提到 %q —— 用户不知道该补哪一项",
				c.id, err.Error(), c.wantErr)
		}
	}
}

// 展开里用到的每个开关都必须定义在对应子命令的源码中，防止运行期才暴露的错配。
func TestExposureProfilesUseOnlyRealFlags(t *testing.T) {
	const room = "abc123"
	const relay = "turn:1.2.3.4:3478"

	sources := map[string]string{}
	for route, file := range routeSourceFile {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读不到 %s：%v", file, err)
		}
		if !strings.Contains(string(raw), "fs.") {
			t.Fatalf("%s 里没有 fs.* 定义 —— 文件找错了，这条断言会变成空转", file)
		}
		sources[route] = string(raw)
	}

	// fs.String("mode" / fs.Bool("no-gate" / fs.Int("port" 三种定义形式。
	defRe := regexp.MustCompile(`fs\.(?:String|Int|Bool)\(\s*"([a-z0-9-]+)"`)
	defs := map[string]map[string]bool{}
	for route, src := range sources {
		defs[route] = map[string]bool{}
		for _, m := range defRe.FindAllStringSubmatch(src, -1) {
			defs[route][m[1]] = true
		}
	}

	for _, p := range exposureProfiles {
		if p.Route == "" {
			continue
		}
		argv, err := expandProfile(p, room, relay)
		if err != nil {
			t.Fatalf("档位 %s 展开失败：%v", p.ID, err)
		}
		allowed, ok := defs[argv[0]]
		if !ok {
			t.Errorf("档位 %s 展开成子命令 %q，但没有对应的源码文件可核对", p.ID, argv[0])
			continue
		}
		for _, a := range argv[1:] {
			if !strings.HasPrefix(a, "--") {
				continue
			}
			name := strings.TrimPrefix(a, "--")
			if !allowed[name] {
				t.Errorf("档位 %s 用了 --%s，但 %s 里没有定义这个开关 —— 档位不许发明参数",
					p.ID, name, routeSourceFile[argv[0]])
			}
		}
	}
}
