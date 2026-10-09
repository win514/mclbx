package main

// guiconfig.go 按「任务 + 字段」记住界面填写过的值，存于 %LOCALAPPDATA%\mclbx\config.json。
// 采用原子写（先写 .tmp 再改名）；内容为明文，删除该文件即等于忘掉。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// guiConfig 为配置文件内容，仅存填写过的值。
type guiConfig struct {
	// Inputs: 任务 key → 字段 id → 值；空串表示用户清空，回落到默认值。
	Inputs map[string]map[string]string `json:"inputs,omitempty"`

	// UI: 界面自身偏好（主题、强调色、默认端口等），与具体任务无关。
	UI guiUIState `json:"ui,omitempty"`
}

// guiUIState 界面偏好；读取时统一归一化，非法取值回落到默认。
type guiUIState struct {
	Theme    string `json:"theme,omitempty"`    // auto（跟随系统）/ dark / light / contrast
	Accent   string `json:"accent,omitempty"`   // mint / blue / violet
	Motion   string `json:"motion,omitempty"`   // full / lite
	Backdrop string `json:"backdrop,omitempty"` // on / off
	Scale    string `json:"scale,omitempty"`    // std / big（界面字号，独立于主题）
	Flat     string `json:"flat,omitempty"`     // on / off（扁平化，独立于主题）
	Glass    string `json:"glass,omitempty"`    // on / off（毛玻璃）：off = 实心不做模糊，on = 最透明那一档
	Fade     string `json:"fade,omitempty"`     // on / off（面板淡入）
	BgImage  string `json:"bgImage,omitempty"`  // 背景图片名（存档 wallpapers 目录里的一个文件）；空 = 不用
	Radii    string `json:"radii,omitempty"`    // sharp / std / round（界面圆角大小）
	Rail     string `json:"rail,omitempty"`     // std / compact（侧边栏样式）

	// 高级选项：两个 0-100 的百分比，出厂都是 50。只在离开出厂值时才覆盖样式。
	Transparency string `json:"transparency,omitempty"` // 面板透明度：越大面板越透
	WallBright   string `json:"wallBright,omitempty"`   // 背景图明暗：越大照片越清楚
	MusicMode    string `json:"musicMode,omitempty"`    // order / shuffle（顺序 / 随机）
	MusicLoop    string `json:"musicLoop,omitempty"`    // off / all / one（不循环 / 列表循环 / 单曲循环）
	MusicVol     string `json:"musicVol,omitempty"`     // 0-100，十进制字符串
	Remember     string `json:"remember,omitempty"`     // "0" = 不记住填写内容；空 = 记住
	LogKeep      string `json:"logKeep,omitempty"`      // 日志每层保留行数
	DefPort      string `json:"defPort,omitempty"`      // 默认游戏端口；空 = 不干预
	DefRelay     string `json:"defRelay,omitempty"`     // 默认中转服务器；空 = 不干预
}

// themeValues 主题白名单，须与 CSS 的 :root[data-theme=…] 及 guihtml.go 的 uiThemes() 一致。
// （auto 由首屏脚本解析成 light/dark，不在此列。）
var themeValues = []string{"dark", "light", "contrast"}

// accentValues 强调色白名单，与 uiAccents() 对应。
var accentValues = []string{"blue", "violet"}

// scaleValues 界面字号白名单，是与 Motion/Backdrop 同级的独立维度。
var scaleValues = []string{"std", "big"}

// 毛玻璃：只有开与关两态。
//
// 此前是 0-10 的档位，实测相邻档位看不出差别（0 到 10 的三档不透明度只差 0.35、面板模糊
// 只差 13px），而每一档都要在 CSS 里维护一块、在设置里解释一遍。一个调了看不出效果的滑杆
// 比一个开关更糟：用户会以为是自己没调对。
//
// 开态就是此前最透明的那一档（见 CSS 里 :root[data-glass="on"] 那一块），不再有中间态，
// 因此也不必再维护一张档位表 —— 只有"值清单"用来核对取值与 CSS 块是否一一对应。
const (
	uiGlassOff     = "off"
	uiGlassOn      = "on"
	uiGlassDefault = uiGlassOn
)

// glassValues 全部合法取值。用例拿它核对"取值清单与 CSS 逐块对应"。
var glassValues = []string{uiGlassOff, uiGlassOn}

// 高级显示选项：两个 0-100 的百分比，出厂值都在正中间。
//
// 出厂值下前端不写任何覆盖（CSS 里的倍率回落到 1），所以"没动过这两根滑杆"的用户拿到的
// 就是此前那组已核对过可读性的取值。往"更冒险"的方向调 —— 面板更透明、照片更亮 ——
// 会牺牲文字的可读性；这由用户自己决定，界面上当场提示，不硬卡。
const (
	uiPctMin     = 0
	uiPctMax     = 100
	uiPctDefault = 50
)

// clampPercent 把 0-100 的百分比收成合法值：非数字、越界、空值一律回到出厂值。
func clampPercent(s string) string {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < uiPctMin || n > uiPctMax {
		return strconv.Itoa(uiPctDefault)
	}
	return strconv.Itoa(n)
}

// radiiValues 界面圆角白名单，与 CSS 的 :root[data-radii=…] 三个块对应。
//
// 与 glass 同理：调的是一组预定义圆角令牌而非像素值，可保证层次关系不乱。
var radiiValues = []string{"sharp", "std", "round"}

// railValues 侧边栏样式白名单，与 CSS 的 :root[data-rail=…] 对应。
var railValues = []string{"std", "compact"}

// 日志每层保留的行数上限与下限。
const (
	uiLogKeepDefault = 2000
	uiLogKeepMin     = 200
	uiLogKeepMax     = 20000
)

// normalizeUI 将任意输入收回到合法取值，空值一律变为默认。
func normalizeUI(u guiUIState) guiUIState {
	if !containsStr(themeValues, u.Theme) {
		u.Theme = "auto" // auto 无对应 CSS 块，为默认值
	}
	if !containsStr(accentValues, u.Accent) {
		u.Accent = "mint"
	}
	if u.Motion != "lite" {
		u.Motion = "full"
	}
	if u.Backdrop != "off" {
		u.Backdrop = "on"
	}
	if !containsStr(scaleValues, u.Scale) {
		u.Scale = "std"
	}
	// 扁平化与毛玻璃都是 on/off，默认关：默认外观与升级前完全一致。
	if u.Flat != "on" {
		u.Flat = "off"
	}
	// 毛玻璃只有开与关。旧的数字档（0…10）与更早的 low/mid/high 在这里迁移：
	// 非零档一律迁到 on（当初调高就是要更透，不该悄悄抹掉），"0" 迁到 off，
	// 认不出来的值回落到默认（on）。
	switch strings.ToLower(strings.TrimSpace(u.Glass)) {
	case uiGlassOff:
		u.Glass = uiGlassOff
	case uiGlassOn, "low", "mid", "high":
		u.Glass = uiGlassOn
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(u.Glass)); err == nil && n == 0 {
			u.Glass = uiGlassOff
		} else {
			u.Glass = uiGlassDefault
		}
	}
	// 高级选项的两个百分比：越界与非数字一律回到出厂值，不留半个合法值。
	u.Transparency = clampPercent(u.Transparency)
	u.WallBright = clampPercent(u.WallBright)
	// 面板淡入默认开；它属于视觉美化那一层，恒开，没有总开关。
	if u.Fade != "off" {
		u.Fade = "on"
	}
	// 圆角与侧边栏样式：白名单之外一律回落到默认（取值直接命中 CSS 里对应的块）。
	if !containsStr(radiiValues, u.Radii) {
		u.Radii = "std"
	}
	if !containsStr(railValues, u.Rail) {
		u.Rail = "std"
	}
	// 旧配置存的是路径，在此换成导入进存档的副本（读写两条路都经过这里，迁移一次即可）。
	u.BgImage = adoptLegacyWallPath(cleanWallPath(u.BgImage))
	if !containsStr([]string{"order", "shuffle"}, u.MusicMode) {
		u.MusicMode = "order"
	}
	if !containsStr([]string{"off", "all", "one"}, u.MusicLoop) {
		u.MusicLoop = "all" // 背景音乐默认列表循环
	}
	if n, err := strconv.Atoi(strings.TrimSpace(u.MusicVol)); err != nil || n < 0 || n > 100 {
		u.MusicVol = "70"
	} else {
		u.MusicVol = strconv.Itoa(n)
	}
	if u.Remember != "0" {
		u.Remember = ""
	}
	if n, err := strconv.Atoi(strings.TrimSpace(u.LogKeep)); err != nil || n < uiLogKeepMin || n > uiLogKeepMax {
		u.LogKeep = ""
	} else {
		u.LogKeep = strconv.Itoa(n)
	}
	u.DefPort = strings.TrimSpace(u.DefPort)
	if p, err := strconv.Atoi(u.DefPort); err != nil || p < 1 || p > 65535 {
		u.DefPort = ""
	}
	u.DefRelay = strings.TrimSpace(u.DefRelay)
	return u
}

// cleanWallPath 收拾背景图路径：去首尾空白，以及「复制文件地址」粘过来时自带的那对引号。
func cleanWallPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"`)
	return strings.TrimSpace(p)
}

func (u guiUIState) rememberOn() bool { return u.Remember != "0" }

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (u guiUIState) logKeep() int {
	if n, err := strconv.Atoi(u.LogKeep); err == nil {
		return n
	}
	return uiLogKeepDefault
}

// htmlAttr 拼出 <html> 的主题属性，由服务端注入以避免首屏闪烁。
// 视觉美化那几项也注进去（决定首屏是否"先实心再闪成玻璃"），不在 CSS 里用总开关去推。
func (u guiUIState) htmlAttr() string {
	return fmt.Sprintf(` data-theme="%s" data-accent="%s" data-motion="%s" data-backdrop="%s" data-scale="%s" data-flat="%s" data-glass="%s" data-fade="%s" data-radii="%s" data-rail="%s"`,
		u.Theme, u.Accent, u.Motion, u.Backdrop, u.Scale, u.Flat, u.Glass, u.Fade, u.Radii, u.Rail)
}

// wallAttr 拼出背景图片那一段：data-wall 标记加一个行内 --wall 变量；没有可用图片时返回空串。
// 变量走行内样式，注入点层叠优先级最高，不需要一套「主题 × 图片」的规则。
func wallAttr(w wallResult) string {
	if !w.on() {
		return ""
	}
	return fmt.Sprintf(` data-wall="on" style="--wall:url('%s')"`, w.URL)
}

// uiStartupJSON 注入页面启动所需字段：跟随系统的原始主题值与日志行数。
//
// 外观那几项必须在这里就交给前端：属性注入（htmlAttr）管的是"值直接对应一个 CSS 块"的项，
// 而高级选项的两个百分比要先在浏览器端换算成倍率，服务端只发取值。少了它们，前端在首屏
// 无从知道用户调过面板透明度与背景明暗，界面会一直停在出厂观感，直到用户动一次设置。
func (u guiUIState) uiStartupJSON() string {
	b, err := json.Marshal(struct {
		Theme     string `json:"theme"`
		FollowOS  bool   `json:"followOS"`
		LogKeep   int    `json:"logKeep"`
		MusicMode string `json:"musicMode"`
		MusicLoop string `json:"musicLoop"`
		MusicVol  string `json:"musicVol"`
		// 视觉美化那一组要在首屏就知道，省一次取设置的往返。
		Glass string `json:"glass"`
		Fade  string `json:"fade"`
		Radii string `json:"radii"`
		Rail  string `json:"rail"`
		// 高级选项的两个百分比：倍率由前端算，服务端只把取值发下去。
		Transparency string `json:"transparency"`
		WallBright   string `json:"wallBright"`
	}{
		Theme:        u.Theme,
		FollowOS:     u.Theme == "auto",
		LogKeep:      u.logKeep(),
		MusicMode:    u.MusicMode,
		MusicLoop:    u.MusicLoop,
		MusicVol:     u.MusicVol,
		Glass:        u.Glass,
		Fade:         u.Fade,
		Radii:        u.Radii,
		Rail:         u.Rail,
		Transparency: u.Transparency,
		WallBright:   u.WallBright,
	})
	if err != nil {
		return "{}"
	}
	// json.Marshal 默认转义 < > &，可直接放入 <script>
	return string(b)
}

func loadUI() guiUIState {
	guiCfgMu.Lock()
	defer guiCfgMu.Unlock()
	return normalizeUI(loadConfigLocked().UI)
}

// saveUI 整份覆盖并落盘（界面提交完整一份，故清空默认值也能生效）。
// 顺带在锁外记一条变更记录（见 uicfg_io.go），它失败不影响本次保存。
func saveUI(next guiUIState) (guiUIState, error) {
	guiCfgMu.Lock()
	c := loadConfigLocked()
	prev := normalizeUI(c.UI)
	c.UI = normalizeUI(next)
	err := writeConfigLocked(c)
	out := c.UI
	guiCfgMu.Unlock()
	if err == nil {
		recordUIChanges(prev, out)
	}
	return out, err
}

// forgetSavedInputs 只清填写过的值，保留界面设置（清全部用 mclbx forget）。
func forgetSavedInputs() error {
	guiCfgMu.Lock()
	defer guiCfgMu.Unlock()
	c := loadConfigLocked()
	c.Inputs = map[string]map[string]string{}
	return writeConfigLocked(c)
}

// globalDefaultFor 返回某字段的全局默认值（按字段 id 匹配）。
func globalDefaultFor(fieldID string) string {
	ui := loadUI()
	switch fieldID {
	case "relayServer":
		return ui.DefRelay
	case "port":
		return ui.DefPort
	}
	return ""
}

// 记录任务数上限，防止文件无限增长。
const guiConfigMaxTasks = 64

var (
	guiCfgMu sync.Mutex
	guiCfg   *guiConfig

	// 做成变量，便于测试指向临时目录
	guiConfigFile = func() string {
		d := dataDir()
		if d == "" {
			// 没有可写位置时返回空串：读退回默认值、写会失败。
			// 不能让 filepath.Join("", ...) 生效 —— 那会把配置写到当前工作目录。
			return ""
		}
		return filepath.Join(d, "config.json")
	}
)

// resetGuiConfigCache 丢弃内存缓存，下次访问时重新读文件。
func resetGuiConfigCache() {
	guiCfgMu.Lock()
	guiCfg = nil
	guiCfgMu.Unlock()
}

// loadGuiConfig 读取配置并缓存；读取失败时视为空配置。
func loadGuiConfig() *guiConfig {
	guiCfgMu.Lock()
	defer guiCfgMu.Unlock()
	return loadConfigLocked()
}

// loadConfigLocked 同 loadGuiConfig，但要求调用方已持锁。
func loadConfigLocked() *guiConfig {
	if guiCfg != nil {
		return guiCfg
	}
	c := &guiConfig{}
	b, err := os.ReadFile(guiConfigFile())
	if err == nil && strings.TrimSpace(string(b)) != "" {
		if e := json.Unmarshal(b, c); e != nil {
			// 文件在但读不出来：整份丢弃回到默认，并记下这次故障。
			// 不能只忽略错误继续用 —— json.Unmarshal 出错会把已解析的部分留在 c 里，形成半旧半默认的混合状态。
			*c = guiConfig{}
			noteConfigFault(e)
		}
	}
	if c.Inputs == nil {
		c.Inputs = map[string]map[string]string{}
	}
	guiCfg = c
	return guiCfg
}

// 配置损坏只在当前这次运行里被记一次；不写进文件，也不跨次运行保留。
var (
	configFaultMu sync.Mutex
	configFault   string
)

func noteConfigFault(err error) {
	configFaultMu.Lock()
	defer configFaultMu.Unlock()
	if configFault == "" && err != nil {
		configFault = err.Error()
	}
}

// configFaultReason 返回本次运行里配置文件的读取故障原因；空串表示一切正常。
func configFaultReason() string {
	configFaultMu.Lock()
	defer configFaultMu.Unlock()
	return configFault
}

// savedField 返回某任务某字段上次的值；第二返回值表示确实存过。
func savedField(taskKey, fieldID string) (string, bool) {
	v, ok := loadGuiConfig().Inputs[taskKey][fieldID]
	return v, ok
}

// fieldDefault 决定字段显示值，优先级：本任务上次填的 → 全局默认 → 字段默认。
func fieldDefault(taskKey string, f guiField) string {
	if v, ok := savedField(taskKey, f.ID); ok {
		if strings.TrimSpace(v) != "" {
			return v
		}
		return f.Default // 曾清空，回落到默认值
	}
	if v := globalDefaultFor(f.ID); v != "" {
		return v
	}
	return f.Default
}

// fieldChecked 判断勾选项是否勾上：存过按存储值，否则按默认（"1" 表示勾上）。
func fieldChecked(taskKey string, f guiField) bool {
	if v, ok := savedField(taskKey, f.ID); ok {
		return v == "1"
	}
	return f.Default == "1"
}

// rememberFieldValues 记录本次填写的值并落盘；写失败仅记日志。
func rememberFieldValues(taskKey string, in map[string]string) {
	if taskKey == "" || len(in) == 0 {
		return
	}
	// 关闭「记住上次填写的值」时不写入；判断集中在此供三条界面路径共用
	if !loadUI().rememberOn() {
		return
	}
	writeTaskInputs(taskKey, in)
}

// writeTaskInputs 无条件写入某任务的输入（「记住填写内容」开关不作用于设置项本身）。
func writeTaskInputs(taskKey string, in map[string]string) {
	if taskKey == "" || len(in) == 0 {
		return
	}
	guiCfgMu.Lock()
	c := loadConfigLocked()
	if c.Inputs == nil {
		c.Inputs = map[string]map[string]string{}
	}
	// 值与已存一致时跳过写盘
	if sameInputs(c.Inputs[taskKey], in) {
		guiCfgMu.Unlock()
		return
	}
	m := make(map[string]string, len(in))
	for k, v := range in {
		m[k] = strings.TrimSpace(v)
	}
	c.Inputs[taskKey] = m
	pruneConfig(c)
	err := writeConfigLocked(c)
	guiCfgMu.Unlock()
	if err != nil {
		guiLog("配置没写下去（不影响使用）：%v", err)
	}
}

func sameInputs(old, in map[string]string) bool {
	if len(old) != len(in) {
		return false
	}
	for k, v := range in {
		ov, ok := old[k]
		if !ok || ov != strings.TrimSpace(v) {
			return false
		}
	}
	return true
}

// pruneConfig 超过上限时按任务名排序删除多余记录。
func pruneConfig(c *guiConfig) {
	if len(c.Inputs) <= guiConfigMaxTasks {
		return
	}
	keys := make([]string, 0, len(c.Inputs))
	for k := range c.Inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys[:len(keys)-guiConfigMaxTasks] {
		delete(c.Inputs, k)
	}
}

// writeConfigLocked 原子写（先写临时文件再改名）；调用前须持有 guiCfgMu。
func writeConfigLocked(c *guiConfig) error {
	path := guiConfigFile()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// 0600：可能含中继凭据，限制同机其他用户读取
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // Windows 上亦会覆盖已有文件
}

// forgetGuiConfig 删除配置文件（mclbx forget）。
func forgetGuiConfig() error {
	guiCfgMu.Lock()
	defer guiCfgMu.Unlock()
	path := guiConfigFile()
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	guiCfg = &guiConfig{Inputs: map[string]map[string]string{}}
	return nil
}

// cmdForget 清除上次填过的内容。
func cmdForget(args []string) error {
	path := guiConfigFile()
	if err := forgetGuiConfig(); err != nil {
		return fmt.Errorf("删不掉 %s：%w", path, err)
	}
	fmt.Printf("已忘掉上次填过的内容（%s）\n", path)
	fmt.Println("下次打开界面，各个输入框会回到默认值。")
	return nil
}

// autoProbeOn 返回打开界面时是否自动执行环境体检；默认开启。
func autoProbeOn() bool {
	for _, t := range guiTasks {
		if t.Key != "probe" {
			continue
		}
		for _, f := range t.Fields {
			if f.ID == "auto" {
				return fieldChecked(t.Key, f)
			}
		}
	}
	return true
}

// setAutoProbe 设置「打开界面时自动体检」；该值存于 probe 任务的勾选项中，两种界面共用。
func setAutoProbe(on bool) {
	in := map[string]string{}
	if cur, ok := savedTaskInputs("probe"); ok {
		for k, v := range cur {
			in[k] = v
		}
	}
	if on {
		in["auto"] = "1"
	} else {
		in["auto"] = "" // 空串表示未勾选（见 fieldChecked）
	}
	writeTaskInputs("probe", in)
}

// savedTaskInputs 取某个任务当前记着的那份输入（副本）。
func savedTaskInputs(taskKey string) (map[string]string, bool) {
	cur, ok := loadGuiConfig().Inputs[taskKey]
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(cur))
	for k, v := range cur {
		out[k] = v
	}
	return out, true
}

// guiTasksForWeb 返回带上次填写值的任务表，供网页版 /api/tasks 使用。
func guiTasksForWeb() []guiTask {
	out := make([]guiTask, len(guiTasks))
	copy(out, guiTasks)
	for i := range out {
		if len(out[i].Fields) == 0 {
			continue
		}
		fs := make([]guiField, len(out[i].Fields))
		copy(fs, out[i].Fields)
		for j := range fs {
			if fs[j].Kind == "check" {
				// 网页版按 default === '1' 判断勾选
				if fieldChecked(out[i].Key, fs[j]) {
					fs[j].Default = "1"
				} else {
					fs[j].Default = ""
				}
				continue
			}
			fs[j].Default = fieldDefault(out[i].Key, fs[j])
		}
		out[i].Fields = fs
	}
	return out
}
