package main

// uispec_impl.go —— 设计清单与「本工具真正实现了哪些」之间的对照表。
//
// uispec_gen.go 是整份设计全集（14 个模块 / 287 条），这个文件回答其中哪些已经能操作。
// 每落地一批功能，就往这里加几行；清单本身不动，也不重新生成。
//
// 一条对照由四样组成：
//   Item   设计文档里的条目名 —— 必须与生成文件里逐字一致，用例会逐条核对
//   Key    界面上这条控件的标识（data-seg / data-sw / data-in 取值）
//   Field  对应的 guiUIState 字段名；重置与变更记录靠它定位。空 = 不在配置里
//   Ctl    控件形态，见下
//
// Ctl 取值：
//   seg     分段控件（取值固定几项）
//   sw      勾选框
//   field   文本输入框
//   wall    背景图选择器（含导入 / 打开目录 / 刷新三个动作）
//   music   曲库那一行（含打开文件夹 / 刷新两个动作）
//   act     纯动作项，没有取值，点了就执行
//   chrome  由面板的结构本身承担，不在条目列表里重复出现：
//           「重置全部设置」在底栏，「重置本分类为默认」在模块标题右上角

type uiSpecImpl struct {
	Item  string
	Key   string
	Field string
	Ctl   string
}

// uiImplByModule 模块名 → 该模块里已经能操作的条目。
// 模块名必须与 uispec_gen.go 里的逐字一致。
var uiImplByModule = map[string][]uiSpecImpl{
	"界面与外观设置": {
		{"主题", "theme", "theme", "seg"},
		{"强调色", "accent", "accent", "dots"},
		{"材质", "flat", "flat", "seg"},
		{"毛玻璃档位", "glass", "glass", "seg"},
		{"视觉美化总开关", "vfx", "vfx", "sw"},
		{"科幻轮廓微光", "glow", "glow", "sw"},
		{"面板淡入", "fade", "fade", "sw"},
		{"背景光效", "backdrop", "backdrop", "sw"},
		{"背景图片", "bgImage", "bgImage", "wall"},
		{"界面字号", "scale", "scale", "seg"},
		{"动画效果", "motion", "motion", "seg"},
		{"帧率自动降级", "noDegrade", "noDegrade", "sw"},
		{"帧率实测", "vfxMeasure", "", "act"},
	},
	"软件基础行为与通用偏好": {
		// autoProbe 存在 probe 任务的勾选项里，不在 guiUIState 中，所以 Field 为空
		{"打开界面时自动体检", "autoProbe", "", "sw"},
		{"记住上次填过的值", "remember", "remember", "sw"},
		{"清除记住的填写内容", "forgetInputs", "", "act"},
		{"日志保留行数", "logKeep", "logKeep", "field"},
		{"默认游戏端口", "defPort", "defPort", "field"},
		{"默认中转服务器", "defRelay", "defRelay", "field"},
	},
	"文件保存、输出路径与文件行为": {
		{"存档目录位置", "dataDir", "", "act"},
	},
	"配置管理（导入、导出、重置、预设）": {
		{"导出全部配置", "exportCfg", "", "act"},
		{"导入配置", "importCfg", "", "act"},
		{"重置全部设置", "resetAll", "", "chrome"},
		{"重置本分类为默认", "resetModule", "", "chrome"},
	},
	"关于、帮助与反馈入口": {
		{"版本信息", "version", "", "act"},
		{"内置说明书", "manual", "", "act"},
		{"导出诊断包", "diag", "", "act"},
	},
}

// uiExtraByModule 设计文档没有收录、而本工具确实有的条目。
//
// 音乐这一组不在那 14 个模块里 —— 当初的设计任务是按「行业通用设置」列模块，
// 而音乐是 mclbx 自己的东西。放在「基础行为与通用偏好」末尾，并标出来源，
// 免得将来有人拿界面和设计文档对照时以为对不上。
var uiExtraByModule = map[string][]uiSpecNode{
	"软件基础行为与通用偏好": {
		{Name: "曲库", Desc: "存档目录里 music 文件夹的内容。放进去的音频文件会被扫成曲库，界面只负责播放。",
			Values: "该文件夹里的音频文件（MP3 / WAV / FLAC / M4A / OGG）", Effect: "实时生效",
			Mark: "land", Key: "musicList", Ctl: "music", Extra: true},
		{Name: "播放顺序", Desc: "顺序播放时按曲库里的文件名排序；随机播放不会连续两首抽到同一首。",
			Values: "顺序 / 随机", Effect: "实时生效", Mark: "land", Key: "musicMode", Ctl: "seg",
			Field: "musicMode", Extra: true},
		{Name: "循环", Desc: "选「不循环」时，顺序播到最后一首就停下；随机模式下会继续。",
			Values: "不循环 / 列表循环 / 单曲循环", Effect: "实时生效", Mark: "land", Key: "musicLoop",
			Ctl: "seg", Field: "musicLoop", Extra: true},
		{Name: "音量", Desc: "与系统音量是叠乘关系：听不见时先看系统音量，再看这里是不是被调成了 0。",
			Values: "0 ~ 100（默认 70）", Effect: "实时生效", Mark: "land", Key: "musicVol",
			Ctl: "field", Field: "musicVol", Extra: true},
		{Name: "播放控制", Desc: "底栏那个音符按钮也能播放与暂停，不用每次打开设置。切歌与放完自动下一首都按上面的顺序与循环设置走。",
			Values: "上一首 / 播放暂停 / 下一首", Effect: "点击即执行", Mark: "land", Key: "bgmCtl",
			Ctl: "act", Extra: true},
	},
}

// uiSpecNode 发给界面的一个条目：设计内容 + 是否可操作 + 控件信息。
type uiSpecNode struct {
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Values string `json:"values"`
	Effect string `json:"effect"`
	Mark   string `json:"mark"`
	Key    string `json:"key,omitempty"`   // 空 = 这一条当前不可操作
	Ctl    string `json:"ctl,omitempty"`   // 控件形态，界面据此渲染
	Extra  bool   `json:"extra,omitempty"` // 设计文档未收录，本工具自有
	// Field 只有服务端用（重置范围与变更记录），不必发给界面
	Field string `json:"-"`
}

// uiImplsFor 某个模块下全部「已实现」的对照，含文档未收录的自有条目。
//
// 重置范围与变更记录都从这里取，所以自有的音乐几项也能被正确重置与记账。
func uiImplsFor(module string) []uiSpecImpl {
	out := append([]uiSpecImpl{}, uiImplByModule[module]...)
	for _, e := range uiExtraByModule[module] {
		out = append(out, uiSpecImpl{Item: e.Name, Key: e.Key, Field: e.Field, Ctl: e.Ctl})
	}
	return out
}

// uiSpecGroup 一个模块。
type uiSpecGroup struct {
	Name    string       `json:"name"`
	Planned int          `json:"planned"` // 本模块「当前可落地」的条目数（含尚未实现的）
	Live    int          `json:"live"`    // 本模块已经能操作的条目数
	Items   []uiSpecNode `json:"items"`
}

// uiSpecForWeb 把设计清单与实现对照合成界面要用的结构。
//
// 每个模块的条目顺序保持设计文档里的原样（不按可操作性重排），
// 这样界面上的位置与文档里的位置一一对应，对照起来不用来回找。
func uiSpecForWeb() []uiSpecGroup {
	out := make([]uiSpecGroup, 0, len(uiSpecModules))
	for _, m := range uiSpecModules {
		impls := uiImplByModule[m.Name]
		byItem := make(map[string]uiSpecImpl, len(impls))
		for _, im := range impls {
			byItem[im.Item] = im
		}
		g := uiSpecGroup{Name: m.Name, Items: make([]uiSpecNode, 0, len(m.Items)+4)}
		for _, it := range m.Items {
			if it.Mark == "land" {
				g.Planned++
			}
			n := uiSpecNode{Name: it.Name, Desc: it.Desc, Values: it.Values,
				Effect: it.Effect, Mark: it.Mark}
			if im, ok := byItem[it.Name]; ok {
				n.Key, n.Ctl = im.Key, im.Ctl
				if im.Ctl != "chrome" {
					g.Live++
				}
			}
			g.Items = append(g.Items, n)
		}
		if extras := uiExtraByModule[m.Name]; len(extras) > 0 {
			g.Items = append(g.Items, extras...)
			g.Live += len(extras)
		}
		out = append(out, g)
	}
	return out
}

// uiItemLabel 反查某个配置字段对应的「模块 + 选项名称」，供变更记录写清楚改的是哪一项。
//
// 一个字段可能对应多条（目前没有），取第一条即可。
// 找不到对应条目时返回空串，调用方就不要记这一条 ——
// 记一条「改了某字段」但说不出选项名的记录，对用户没有价值。
func uiItemLabel(field string) (module, item string) {
	if field == "" {
		return "", ""
	}
	for _, m := range uiSpecModules {
		for _, im := range uiImplsFor(m.Name) {
			if im.Field == field {
				return m.Name, im.Item
			}
		}
	}
	return "", ""
}

// uiModuleFields 某个模块下所有「会落到配置里」的字段名。
// 模块级重置用它圈出要恢复默认的范围。
func uiModuleFields(module string) []string {
	var out []string
	seen := map[string]bool{}
	for _, im := range uiImplsFor(module) {
		if im.Field == "" || seen[im.Field] {
			continue
		}
		seen[im.Field] = true
		out = append(out, im.Field)
	}
	return out
}

// uiKnownModules 界面导航用的模块名（顺序即设计文档里的顺序）。
func uiKnownModules() []string {
	out := make([]string, 0, len(uiSpecModules))
	for _, m := range uiSpecModules {
		out = append(out, m.Name)
	}
	return out
}
