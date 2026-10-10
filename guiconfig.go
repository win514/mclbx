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
	Theme  string `json:"theme,omitempty"`  // auto（跟随系统）/ dark / light / contrast
	Accent string `json:"accent,omitempty"` // mint / blue / violet
	Motion string `json:"motion,omitempty"` // full / lite
	Scale  string `json:"scale,omitempty"`  // std / big（界面字号，独立于主题）
	Rail   string `json:"rail,omitempty"`   // std / compact（侧边栏样式）

	MusicPlay string `json:"musicPlay,omitempty"` // loop / shuffle / one（循环播放 / 随机播放 / 单曲循环）
	MusicVol  string `json:"musicVol,omitempty"`  // 0-100，十进制字符串
	Remember  string `json:"remember,omitempty"`  // "0" = 不记住填写内容；空 = 记住
	LogKeep   string `json:"logKeep,omitempty"`   // 日志每层保留行数
	DefPort   string `json:"defPort,omitempty"`   // 默认游戏端口；空 = 不干预
	DefRelay  string `json:"defRelay,omitempty"`  // 默认中转服务器；空 = 不干预

	// 旧配置遗留：音乐原有的两个设置项，仅用于把老配置迁移到 MusicPlay。
	// 它们不再是可编辑设置，见 uiLegacyFields —— 面板、导出、导入与重置都不认它们。
	MusicMode string `json:"musicMode,omitempty"`
	MusicLoop string `json:"musicLoop,omitempty"`
}

// themeValues 主题白名单，须与 frontend/gui.js 的 uiThemes() 及 CSS 的 :root[data-theme=…] 一致。
// （auto 由首屏脚本解析成 light/dark，不在此列。）
var themeValues = []string{"dark", "light", "contrast"}

// accentValues 强调色白名单，与 frontend/gui.js 的 uiAccents() 对应。
var accentValues = []string{"blue", "violet"}

// scaleValues 界面字号白名单，是与 Motion 同级的独立维度。
var scaleValues = []string{"std", "big"}

// musicPlayValues 背景音乐播放方式白名单，与 frontend/gui.js 的 uiSeg('musicPlay') 一致。
var musicPlayValues = []string{"loop", "shuffle", "one"}

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
	if !containsStr(scaleValues, u.Scale) {
		u.Scale = "std"
	}
	if !containsStr(railValues, u.Rail) {
		u.Rail = "std"
	}
	// 背景音乐：旧的 musicMode / musicLoop 合并为一个 musicPlay，取值 loop / shuffle / one。
	// 迁移只看老配置：musicMode=shuffle 直接落到 shuffle；否则按 musicLoop 映射，
	// one → one，off 与 all → loop，认不出的回落 loop。迁移完成后清空遗留字段。
	if !containsStr(musicPlayValues, strings.ToLower(strings.TrimSpace(u.MusicPlay))) {
		u.MusicPlay = migrateMusicPlay(u.MusicMode, u.MusicLoop)
	} else {
		u.MusicPlay = strings.ToLower(strings.TrimSpace(u.MusicPlay))
	}
	u.MusicMode, u.MusicLoop = "", ""
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

// migrateMusicPlay 把旧的两个音乐设置项合并成一个取值。
// 契约：musicMode="shuffle" 落到 shuffle（随机优先）；否则按 musicLoop 映射，
// one → one，off 与 all → loop；认不出的值一律回落 loop。
func migrateMusicPlay(mode, loop string) string {
	if strings.ToLower(strings.TrimSpace(mode)) == "shuffle" {
		return "shuffle"
	}
	switch strings.ToLower(strings.TrimSpace(loop)) {
	case "one":
		return "one"
	default:
		return "loop"
	}
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
func (u guiUIState) htmlAttr() string {
	return fmt.Sprintf(` data-theme="%s" data-accent="%s" data-motion="%s" data-scale="%s" data-rail="%s"`,
		u.Theme, u.Accent, u.Motion, u.Scale, u.Rail)
}

// uiStartupJSON 注入页面启动所需字段：跟随系统的原始主题值与日志行数。
func (u guiUIState) uiStartupJSON() string {
	b, err := json.Marshal(struct {
		Theme     string `json:"theme"`
		FollowOS  bool   `json:"followOS"`
		LogKeep   int    `json:"logKeep"`
		MusicPlay string `json:"musicPlay"`
		MusicVol  string `json:"musicVol"`
		Rail      string `json:"rail"`
	}{
		Theme:     u.Theme,
		FollowOS:  u.Theme == "auto",
		LogKeep:   u.logKeep(),
		MusicPlay: u.MusicPlay,
		MusicVol:  u.MusicVol,
		Rail:      u.Rail,
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
func saveUI(next guiUIState) (guiUIState, error) {
	guiCfgMu.Lock()
	c := loadConfigLocked()
	c.UI = normalizeUI(next)
	err := writeConfigLocked(c)
	out := c.UI
	guiCfgMu.Unlock()
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
			// 不能让 filepath.Join("", ...) 生效：那会把配置写到当前工作目录。
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
			// 文件在但读不出来：整份丢弃回到默认并记故障。
			// 不能忽略错误继续用：json.Unmarshal 出错会把已解析部分留在 c 里，形成半旧半默认的混合状态。
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
		guiLog("配置未写入（不影响使用）：%v", err)
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
		return fmt.Errorf("无法删除 %s：%w", path, err)
	}
	fmt.Printf("已清除上次记住的填写内容（%s）\n", path)
	fmt.Println("下次打开界面时，各输入框恢复默认值。")
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
