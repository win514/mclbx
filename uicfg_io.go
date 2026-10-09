package main

// uicfg_io.go —— 配置的导出、导入、重置，以及设置变更记录。
//
// 四件事共用同一份「字段 → 模块 / 选项名称」的对照（见 uispec_impl.go），
// 所以导出文件里的名字、界面上看到的名字、变更记录里写的名字是同一个，
// 不会出现「日志说改了 A、界面里找不到 A」这种情况。
//
// 设计取舍记在这里：
//   · 导出写成**带缩进的可读文本**，而不是压缩后的单行。它是给人看、给人改的。
//   · 导入采取「不认识就跳过并说明」，不整份拒绝、也不静默丢弃 —— 见 doImport。
//   · 重置只动界面偏好，不动「记住的填写内容」，也不删任何文件。
//   · 变更记录另存一个文件，不混进配置里；清空记录不会碰到设置。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	uiConfigFormat  = "mclbx-config"
	uiConfigVersion = 1
	uiChangeMax     = 500     // 变更记录条数上限
	uiImportMaxLen  = 1 << 20 // 导入文件大小上限：1 MB，远超一份正常配置
)

// ---- 字段名（从结构体标签取，避免和 guiUIState 各写一份）----

// uiFieldNames 取 guiUIState 上全部 json 字段名。
func uiFieldNames() []string {
	t := reflect.TypeOf(guiUIState{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
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

// ---- 导出 ----

// uiConfigEnvelope 导出文件的外层结构。
//
// 带 format 与 version 是为了导入时能明确说「这不是本程序的配置文件」，
// 而不是把随便一个 JSON 当配置读进去、把设置搞乱之后还报成功。
type uiConfigEnvelope struct {
	Format     string                       `json:"format"`
	Version    int                          `json:"version"`
	App        string                       `json:"app"`
	ExportedAt string                       `json:"exportedAt"`
	UI         map[string]any               `json:"ui"`
	Inputs     map[string]map[string]string `json:"inputs,omitempty"`
}

// exportUIConfig 把当前全部设置写成一个可读文件，返回落盘路径。
//
// 写在存档目录里而不是让浏览器下载：这个界面是内嵌的 WebView，
// 下载行为在各平台的 WebView2 / WKWebView 上并不一致；
// 写成文件再告诉用户在哪儿，与导出诊断包的做法一致，也更好找。
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
//
// mode: "overwrite" 覆盖现有设置；"fill" 只补当前仍是默认值的项。
//
// 文件里出现本程序不认识的键，逐条跳过并记下原因返回给界面 ——
// 整份拒绝会让「从新版导出的配置拿到旧版用」变成不可能，
// 静默丢弃则更糟：用户会以为导入成功了，实际少了一半设置。
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

// resetUIScope 把某个范围的界面偏好恢复成默认。
//
// scope 为空或 "all" 表示全部；否则是模块名。
// 返回真正发生变化的字段数 —— 呼叫方据此决定要不要说「已经是默认值了」。
func resetUIScope(scope string) (int, error) {
	cur := loadUI()
	var fields map[string]bool

	if scope == "" || scope == "all" {
		fields = map[string]bool{}
		for _, f := range uiFieldNames() {
			fields[f] = true
		}
	} else {
		list := uiModuleFields(scope)
		if len(list) == 0 {
			return 0, fmt.Errorf("没有这个模块，或者它当前还没有可重置的设置项")
		}
		fields = map[string]bool{}
		for _, f := range list {
			fields[f] = true
		}
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

// ---- 变更记录 ----

// uiChange 一条设置改动。字段固定五个：时间 / 模块 / 选项名称 / 原值 / 新值。
type uiChange struct {
	At     string `json:"at"`
	Module string `json:"module"`
	Item   string `json:"item"`
	From   string `json:"from"`
	To     string `json:"to"`
}

var (
	uiChangeMu   sync.Mutex
	uiChangeFile = func() string { return filepath.Join(dataDir(), "ui-changes.json") }
)

// loadChanges 读变更记录；倒序由界面自行处理，这里保持写入顺序。
func loadChanges() []uiChange {
	uiChangeMu.Lock()
	defer uiChangeMu.Unlock()
	return loadChangesLocked()
}

func loadChangesLocked() []uiChange {
	b, err := os.ReadFile(uiChangeFile())
	if err != nil {
		return nil
	}
	var out []uiChange
	if err := json.Unmarshal(b, &out); err != nil {
		return nil // 记录坏了就当没有：它是辅助信息，不该拦住界面
	}
	return out
}

// recordUIChanges 比对前后两份设置，把差异记进变更记录。
//
// 在设计上它**不是**可失败路径：写不进去只是少一条记录，
// 绝不能因此让保存设置本身报错。
func recordUIChanges(prev, next guiUIState) {
	// 存档目录都不可用时不要记：filepath.Join("", "ui-changes.json") 会变成一个
	// 相对当前目录的文件名，等于往用户的任意工作目录里丢一个文件。
	// 这条推论与配置本身是同一条（见 datadir_test.go 末尾那条用例）。
	if dataDir() == "" {
		return
	}
	diff := diffUIFields(prev, next)
	if len(diff) == 0 {
		return
	}
	fields := make([]string, 0, len(diff))
	for f := range diff {
		fields = append(fields, f)
	}
	sortStrings(fields)

	now := time.Now().Format("2006-01-02 15:04:05")
	add := make([]uiChange, 0, len(fields))
	for _, f := range fields {
		module, item := uiItemLabel(f)
		if item == "" {
			continue // 说不出是哪个选项的字段就不记，记了也没法读
		}
		from, to := diff[f][0], diff[f][1]
		add = append(add, uiChange{At: now, Module: module, Item: item,
			From: showUIValue(from), To: showUIValue(to)})
	}
	if len(add) == 0 {
		return
	}

	uiChangeMu.Lock()
	defer uiChangeMu.Unlock()
	all := append(loadChangesLocked(), add...)
	if len(all) > uiChangeMax {
		all = all[len(all)-uiChangeMax:] // 超出上限丢最旧的
	}
	b, err := json.MarshalIndent(all, "", " ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(dataDir(), 0o700)
	_ = os.WriteFile(uiChangeFile(), b, 0o600)
}

// showUIValue 把空值写成「默认」，让记录读起来是句人话。
func showUIValue(v string) string {
	if v == "" {
		return "（默认）"
	}
	return v
}

// clearUIChanges 清空记录，返回被清掉的条数。
func clearUIChanges() int {
	uiChangeMu.Lock()
	defer uiChangeMu.Unlock()
	if dataDir() == "" {
		return 0 // 同上：目录不可用时不拼相对路径
	}
	n := len(loadChangesLocked())
	_ = os.Remove(uiChangeFile())
	return n
}
