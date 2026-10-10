package main

// uicfg_io.go 配置导出 / 导入 / 重置。
//
// 三者共用 uiFieldLabels 这份「字段 → 板块 / 选项名」对照，导出文件、界面里的名字因此一致。
// 导出用带缩进的可读文本；导入对不认识的键跳过并在结果里说明原因；重置只动界面偏好。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	uiConfigFormat  = "mclbx-config"
	uiConfigVersion = 1
	uiImportMaxLen  = 1 << 20 // 导入文件大小上限
)

// uiLegacyFields 是已废弃的旧字段名：仍需读入以便迁移，但不再当作可编辑设置。
// 面板、导出、导入与重置一律跳过它们。
var uiLegacyFields = map[string]bool{"musicMode": true, "musicLoop": true}

// ---- 字段名（从结构体标签取，避免与 guiUIState 各写一份）----

// uiFieldNames 取 guiUIState 上全部 json 字段名（不含遗留字段）。
func uiFieldNames() []string {
	t := reflect.TypeOf(guiUIState{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || uiLegacyFields[name] {
			continue
		}
		out = append(out, name)
	}
	return out
}

// zeroUIFields 把指定字段清成零值，交给 normalizeUI 落回默认。
func zeroUIFields(u guiUIState, fields map[string]bool) guiUIState {
	v := reflect.ValueOf(&u).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if fields[name] {
			v.Field(i).Set(reflect.Zero(t.Field(i).Type))
		}
	}
	return u
}

// uiValueMap 把界面偏好摊成「字段名 → 值文本」，供比对与记账。
func uiValueMap(u guiUIState) map[string]string {
	b, err := json.Marshal(u)
	if err != nil {
		return map[string]string{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// diffUIFields 找出两份界面偏好里取值不同的字段。
func diffUIFields(prev, next guiUIState) map[string][2]string {
	a, b := uiValueMap(prev), uiValueMap(next)
	out := map[string][2]string{}
	for _, f := range uiFieldNames() {
		x, y := a[f], b[f]
		if x != y {
			out[f] = [2]string{x, y}
		}
	}
	return out
}

// uiFieldInfo 一个可编辑设置项在界面上的位置与名字。
type uiFieldInfo struct {
	Face string // 板块：主题外观 / 辅助工具
	Name string // 选项名称
}

// uiFieldLabels 可编辑设置项的权威清单，唯一一份。
// 用途：变更记录把字段名翻成人能读的名字；重置据此划定范围。
// 只有真正会被读取、会改变行为的字段才允许进来，只读展示项不进这里。
var uiFieldLabels = map[string]uiFieldInfo{
	// 主题外观
	"theme":  {"主题外观", "主题"},
	"accent": {"主题外观", "强调色"},
	"scale":  {"主题外观", "界面字号"},
	"motion": {"主题外观", "动画效果"},
	"rail":   {"主题外观", "侧边栏样式"},
	// 辅助工具
	"musicPlay": {"辅助工具", "播放方式"},
	"musicVol":  {"辅助工具", "音量"},
	"remember":  {"辅助工具", "记住上次填过的值"},
	"logKeep":   {"辅助工具", "日志保留行数"},
	"defPort":   {"辅助工具", "默认游戏端口"},
	"defRelay":  {"辅助工具", "默认中转服务器"},
}

// uiFaces 两个板块的名字，顺序即界面上的顺序。
var uiFaces = []string{"主题外观", "辅助工具"}

// uiBoardFields 某个板块（"all" 或空串表示全部）下全部可编辑字段。
func uiBoardFields(face string) []string {
	if face == "" {
		face = "all"
	}
	out := make([]string, 0, len(uiFieldLabels))
	for _, f := range uiFieldNames() {
		info, ok := uiFieldLabels[f]
		if !ok {
			continue
		}
		if face == "all" || info.Face == face {
			out = append(out, f)
		}
	}
	return out
}

// uiFieldLabel 把字段名翻成人能读的「板块 / 选项名」。
func uiFieldLabel(field string) (face, name string) {
	info, ok := uiFieldLabels[field]
	if !ok {
		return "", ""
	}
	return info.Face, info.Name
}

// uiCorrectedFields 找出用户提交的取值不合法、被归一化改掉的字段。
//
// 提交值与生效值不一致时须明确告知"该值不可用，已回退"，而不是静默改成另一个值。
// 只比对非空提交值：留空表示"回到默认"，不算被纠正。
// used 必须看得懂：归一化常把非法值落成空串，直接报空串会得到「（999999 → ）」这类无意义提示，
// 故空串统一显示为「默认」。
func uiCorrectedFields(submitted, normalized guiUIState) []map[string]string {
	sub, got := uiValueMap(submitted), uiValueMap(normalized)
	out := []map[string]string{}
	for _, f := range uiFieldNames() {
		v := sub[f]
		if v == "" || v == got[f] {
			continue
		}
		_, name := uiFieldLabel(f)
		if name == "" {
			name = f // 没登记的字段也要报出来，否则"值被悄悄改了"是最难查的一类
		}
		used := got[f]
		if used == "" {
			used = "默认"
		}
		out = append(out, map[string]string{"field": f, "name": name, "sent": v, "used": used})
	}
	return out
}

// ---- 只读展示信息 ----
// 这组只读数字用来填充设置面板，不新增可编辑项；全部来自真实文件与目录。

// uiStorageStats 存档目录里各类产物的体积与文件数。
func uiStorageStats() map[string]any {
	dir := dataDir()
	out := map[string]any{"ok": false}
	if dir == "" {
		return out
	}
	// 分类口径与用户关心的东西对齐：曲库占一份，配置作为单文件另计
	buckets := []struct {
		key string
		sub string
	}{
		{"music", filepath.Join(dir, "music")},
	}
	total := int64(0)
	files := 0
	items := map[string]any{}
	for _, b := range buckets {
		var size int64
		var n int
		if entries, err := os.ReadDir(b.sub); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if info, err := e.Info(); err == nil {
					size += info.Size()
					n++
				}
			}
		}
		items[b.key] = map[string]any{"bytes": size, "files": n}
		total += size
		files += n
	}
	// 配置是单文件，单独统计
	{
		var size int64
		var n int
		if info, err := os.Stat(guiConfigFile()); err == nil {
			size, n = info.Size(), 1
		}
		items["config"] = map[string]any{"bytes": size, "files": n}
		total += size
		files += n
	}
	out["ok"] = true
	out["dir"] = dir
	out["items"] = items
	out["totalBytes"] = total
	out["totalFiles"] = files
	return out
}

// uiConfigFileState 配置文件的真实状态：路径、大小、最后写入时间。
// 界面上只做展示，不给任何入口。
func uiConfigFileState() map[string]any {
	p := guiConfigFile()
	out := map[string]any{"path": p}
	if info, err := os.Stat(p); err == nil {
		out["exists"] = true
		out["bytes"] = info.Size()
		out["modified"] = info.ModTime().Format("2006-01-02 15:04:05")
	} else {
		out["exists"] = false
	}
	return out
}

// uiConfigEnvelope 导出文件的外层结构。
// 带 format 与 version，导入时可明确判定「不是本程序的配置文件」，
// 而不是把任意 JSON 当配置读入并报成功。
type uiConfigEnvelope struct {
	Format     string                       `json:"format"`
	Version    int                          `json:"version"`
	App        string                       `json:"app"`
	ExportedAt string                       `json:"exportedAt"`
	UI         map[string]any               `json:"ui"`
	Inputs     map[string]map[string]string `json:"inputs,omitempty"`
}

// exportUIConfig 把当前全部设置写成一个可读文件，返回落盘路径。
// 写在存档目录而非让浏览器下载：内嵌 WebView 的下载行为在各平台不一致。
func exportUIConfig() (string, error) {
	ui := loadUI()
	var uiMap map[string]any
	b, err := json.Marshal(ui)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(b, &uiMap); err != nil {
		return "", err
	}

	env := uiConfigEnvelope{
		Format:  uiConfigFormat,
		Version: uiConfigVersion,
		App:     version,
		// 本地时间即可：这是给用户看的，不是协议字段
		ExportedAt: time.Now().Format("2006-01-02 15:04:05"),
		UI:         uiMap,
	}
	if c := loadGuiConfig(); c != nil && len(c.Inputs) > 0 {
		env.Inputs = c.Inputs
	}

	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')

	dir := dataDir()
	if dir == "" {
		return "", fmt.Errorf("本机找不到可写的存档目录，先把程序放到一个可以写文件的位置再试")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("存档目录不可写：%w", err)
	}
	name := "mclbx-config-" + time.Now().Format("20060102-150405") + ".json"
	path := filepath.Join(dir, name)
	// 0600：配置里可能有中转凭据，不留给同机其它账户读
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", fmt.Errorf("写入失败：%w", err)
	}
	return path, nil
}

// ---- 导入 ----

// uiImportSkip 一条被跳过的条目及原因。
type uiImportSkip struct {
	Key string `json:"key"`
	Why string `json:"why"`
}

// uiImportResult 导入结果。跳过的条目要逐条说明，不能只报一个数字。
type uiImportResult struct {
	Applied int            `json:"applied"`
	Filled  int            `json:"filled"`
	Skipped []uiImportSkip `json:"skipped"`
}

// importUIConfig 读入一份配置。
// mode: "overwrite" 覆盖现有设置；"fill" 只补当前仍是默认值的项。
// 不认识的键逐条跳过并记下原因返回界面：整份拒绝会让新版配置无法用于旧版，
// 静默丢弃则会让用户误以为导入成功。
func importUIConfig(data []byte, mode string) (uiImportResult, error) {
	var res uiImportResult
	if len(data) == 0 {
		return res, fmt.Errorf("文件是空的")
	}
	if len(data) > uiImportMaxLen {
		return res, fmt.Errorf("文件超过 %d KB，不像是一份配置文件", uiImportMaxLen/1024)
	}

	var env uiConfigEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return res, fmt.Errorf("这个文件不是合法的 JSON，可能选错了文件")
	}
	if env.Format != uiConfigFormat {
		return res, fmt.Errorf("这不是本程序导出的配置文件（format 应为 %q，实际是 %q）", uiConfigFormat, env.Format)
	}
	if env.Version > uiConfigVersion {
		return res, fmt.Errorf("这份配置来自更新的版本（格式版本 %d，本程序支持到 %d），请先升级本程序", env.Version, uiConfigVersion)
	}

	allowed := map[string]bool{}
	for _, n := range uiFieldNames() {
		allowed[n] = true
	}

	// 逐键筛：不认识的记下来，认识的收集起来。
	// 收集时用 json.RawMessage 原样带着走，让下一步的类型检查统一报错。
	accept := map[string]json.RawMessage{}
	keys := make([]string, 0, len(env.UI))
	for k := range env.UI {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		raw, _ := json.Marshal(env.UI[k])
		if !allowed[k] {
			res.Skipped = append(res.Skipped, uiImportSkip{k, "本程序没有这个设置项"})
			continue
		}
		accept[k] = raw
	}

	// 类型不对就整份拒绝：把 "abc" 塞进数字设置里，结果不可预期，
	// 此时报清楚比「导入了一部分」更负责任。
	packed, err := json.Marshal(accept)
	if err != nil {
		return res, err
	}
	var incoming guiUIState
	if err := json.Unmarshal(packed, &incoming); err != nil {
		return res, fmt.Errorf("文件里有类型不对的值，为避免设置被写坏，本次没有导入：%w", err)
	}

	cur := loadUI()
	next := incoming
	fill := strings.EqualFold(mode, "fill")
	if fill {
		// 只补「当前还是默认值」的项：以当前值为底，把文件里有的覆盖上去
		curMap := uiValueMap(cur)
		filled := map[string]bool{}
		for k := range accept {
			if _, has := curMap[k]; !has {
				filled[k] = true // 当前是空的 = 未设置过，可以补
			}
		}
		next = mergeUI(cur, incoming, filled)
		res.Filled = len(filled)
	}

	applied := diffUIFields(cur, normalizeUI(next))
	if len(applied) == 0 && len(accept) == 0 {
		return res, fmt.Errorf("文件里没有任何可以应用的设置项")
	}
	if _, err := saveUI(next); err != nil {
		return res, fmt.Errorf("写入配置失败：%w", err)
	}
	res.Applied = len(applied)
	return res, nil
}

// mergeUI 以 base 为底，把 over 里被 pick 选中的字段盖上去。
func mergeUI(base, over guiUIState, pick map[string]bool) guiUIState {
	out := base
	ov := reflect.ValueOf(over)
	ovt := ov.Type()
	res := reflect.ValueOf(&out).Elem()
	for i := 0; i < ovt.NumField(); i++ {
		name := strings.Split(ovt.Field(i).Tag.Get("json"), ",")[0]
		if pick[name] {
			res.Field(i).Set(ov.Field(i))
		}
	}
	return out
}

// sortStrings 极小的插入排序：这里最多几十个键，不值得引入 sort 包。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---- 重置 ----

// resetUIScope 把某个范围的可编辑设置恢复成默认。
// scope 为空或 "all" 表示全部，否则取 uiFaces 里的板块名。
// 返回真正发生变化的字段数，调用方据此决定是否提示「已经是默认值」。
// 只动可编辑设置：填过的表单值与记住的填写内容均不受影响。
func resetUIScope(scope string) (int, error) {
	cur := loadUI()
	if scope != "" && scope != "all" && !isUIFace(scope) {
		return 0, fmt.Errorf("没有这个板块：%s", scope)
	}
	fields := map[string]bool{}
	for _, f := range uiBoardFields(scope) {
		fields[f] = true
	}
	if len(fields) == 0 {
		return 0, fmt.Errorf("这个范围下没有可重置的设置项")
	}

	next := zeroUIFields(cur, fields)
	changed := len(diffUIFields(cur, normalizeUI(next)))
	if changed == 0 {
		return 0, nil // 本来就是默认值，不做没有意义的写入
	}
	if _, err := saveUI(next); err != nil {
		return 0, err
	}
	return changed, nil
}

// isUIFace 判断是不是两个板块之一。
func isUIFace(name string) bool {
	for _, f := range uiFaces {
		if f == name {
			return true
		}
	}
	return false
}
