package main

// reconnect.go 断线自动重连：玩家侧监督循环与房主侧按代次接纳。
// ICE agent 进入 Failed 后不可自愈，只能整条重建。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
)

// 失败分类

// fatalError 标记不可重试的失败（房间码不符、地址写错等），由监督循环直接上报。
type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }
func (e *fatalError) Unwrap() error { return e.err }

func fatal(format string, a ...any) error { return &fatalError{err: fmt.Errorf(format, a...)} }

func isFatal(err error) bool {
	var f *fatalError
	return errors.As(err, &f)
}

// localNetHint 玩家侧几处失败结论共用的补充说明，提示也可能出在本机网络。
const localNetHint = "  也可能出在本机一侧：延迟大 / 丢包 / UDP 被限制都可能表现为上述现象。" +
	"可在「更多功能 → 路况诊断」查看本机一侧的结论"

// 退避

const (
	reconnectBackoffMin = time.Second
	reconnectBackoffMax = 30 * time.Second

	// 「一次都没连上过」时的重试上限，超过即报出最后原因；连上过后不设上限。
	reconnectInitialLimit = 3

	// 重试时等待房主回话的上限（第一代用 --wait）。
	reconnectRetryWait = 30 * time.Second
)

// backoffFor 给出第 n 次失败后应该等多久：1s、2s、4s……封顶 30s。
func backoffFor(failures int) time.Duration {
	d := reconnectBackoffMin
	for i := 1; i < failures; i++ {
		if d >= reconnectBackoffMax {
			return reconnectBackoffMax
		}
		d *= 2
	}
	if d > reconnectBackoffMax {
		d = reconnectBackoffMax
	}
	return d
}

// 隧道间接层

// tunnelRef 用原子指针保存当前隧道，使本地监听口在重连前后保持不变。
type tunnelRef struct{ p atomic.Pointer[guestTunnel] }

func (r *tunnelRef) set(t *guestTunnel) { r.p.Store(t) }
func (r *tunnelRef) get() *guestTunnel  { return r.p.Load() }

var errTunnelDown = errors.New("隧道不可用（正在重连）")

// open 供本地监听口与中继转发入口使用：每次连接都取「当前」那条隧道。
func (r *tunnelRef) open() (io.ReadWriteCloser, error) {
	t := r.get()
	if t == nil {
		return nil, errTunnelDown
	}
	return t.open()
}

// 玩家侧

// guestSession 一场「加入房间」中不变的部分，重连时复用（尤其登记名 key）。
type guestSession struct {
	base    string
	key     string
	room    string
	id      *identity
	selfFP  [32]byte
	useSTUN bool
	wait    time.Duration
}

// hostBlobForGen 判断房主候选是否针对本次尝试；代次 0 留给旧版房主，仅首次放行。
func hostBlobForGen(b candBlob, gen int) bool {
	return b.Gen >= gen || (gen == 1 && b.Gen == 0)
}

// establishGuestTunnel 建立一次隧道，可重入；onIceFail 在 ICE 判定 Failed 时触发。
func establishGuestTunnel(ctx context.Context, sess *guestSession, gen int, onIceFail func()) (*guestTunnel, *ice.Agent, error) {
	label := "join"
	if gen > 1 {
		label = fmt.Sprintf("join#%d", gen)
	}
	a, cands, ufrag, pwd, err := prepareIceAgent(sess.useSTUN, label, 8*time.Second, onIceFail)
	if err != nil {
		return nil, nil, err
	}
	// 失败时由这里收尾；成功时把 agent 交给调用方（隧道还挂在它上面，不能关）。
	ok := false
	defer func() {
		if !ok {
			_ = a.Close()
		}
	}()

	blob := candBlob{
		Ufrag: ufrag, Pwd: pwd, Cands: cands,
		Fingerprint: sess.id.fingerprint(), Room: sess.room, Gen: gen,
	}
	// 登记失败可重试：断网时信箱不可达是主要情形；地址写错由首次重试上限兜住。
	if err := postBlob(sess.base, "guest-"+sess.key, blob, 10*time.Second); err != nil {
		return nil, nil, fmt.Errorf("登记失败：%w\n  原因：房主信令信箱不可达\n  处理：检查房主地址与网络\n%s", err, localNetHint)
	}
	guiDetailf(".. 已登记为 guest-%s（第 %d 代），等待房主候选", sess.key, gen)
	keep := newBlobKeeper(sess.base, "guest-"+sess.key, blob)

	// 第一代按用户给的 --wait；重试时收窄到 reconnectRetryWait。
	wait := sess.wait
	if gen > 1 && wait > reconnectRetryWait {
		wait = reconnectRetryWait
	}
	deadline := time.Now().Add(wait)
	var host candBlob
	for {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("ICE 判定通道失败，本次尝试作废")
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("等待房主候选超时\n  原因：房间码不一致，或房主未运行\n  处理：确认房间码与房主状态\n%s", localNetHint)
		}
		if keep.tick(time.Now(), 5*time.Second) {
			guiDetailf(".. [信令] 重新登记 guest-%s 的候选（信箱条目有存活时间）", sess.key)
		}
		// 必须等房主针对这一代回话，否则会取到上次断线留下的过期候选。
		if b, err := fetchBlob(sess.base, "host-"+sess.key, 5*time.Second); err == nil &&
			hostBlobForGen(b, gen) {
			host = b
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if host.Room != sess.room {
		return nil, nil, fatal("房主公布的房间码 %q 与本地不一致", host.Room)
	}
	if host.Fingerprint == "" {
		return nil, nil, fatal("房主没有公布证书指纹，拒绝在无身份认证的情况下建立连接")
	}
	peerFP, err := parseFingerprint(host.Fingerprint)
	if err != nil {
		return nil, nil, fatal("解析房主指纹失败：%v", err)
	}
	for _, s := range host.Cands {
		if c, err := ice.UnmarshalCandidate(s); err == nil {
			_ = a.AddRemoteCandidate(c)
		}
	}

	ctx90, cancel90 := context.WithTimeout(ctx, 90*time.Second)
	defer cancel90()
	guiDetailf(".. 等待房主发起连通性检查；本方为被控方")
	conn, err := a.Accept(ctx90, host.Ufrag, host.Pwd)
	if err != nil {
		return nil, nil, fmt.Errorf("直连失败：%w", err)
	}
	// 不能 defer conn.Close()：连接需存活，收尾交给 assoc 与 agent。

	logf("结论：ICE 通道已建立")
	dumpPair(a)

	assoc, err := guestAssoc(conn, iceRemoteAddr(a), sess.id, sess.selfFP, peerFP)
	if err != nil {
		return nil, nil, err
	}
	ok = true
	return newGuestTunnel(assoc), a, nil
}

// runGuest 玩家侧监督循环：建立、等待断开、重建，直到出现不可重试的失败。
func runGuest(sess *guestSession, ref *tunnelRef, onUp func(gen int, first bool)) error {
	failures := 0
	everUp := false

	for gen := 1; ; gen++ {
		attemptCtx, cancelAttempt := context.WithCancel(context.Background())

		tunnel, agent, err := establishGuestTunnel(attemptCtx, sess, gen, cancelAttempt)
		if err != nil {
			cancelAttempt()
			if isFatal(err) {
				return err
			}
			failures++
			if !everUp && failures >= reconnectInitialLimit {
				return fmt.Errorf("连续 %d 次都未能建立连接，已停止重试\n  最后一次失败：%w\n%s",
					failures, err, localNetHint)
			}
			logf("结论：第 %d 代未能建立连接（累计失败 %d 次）：%v", gen, failures, err)
			d := backoffFor(failures)
			logf(".. %d 秒后重试", int(d.Seconds()))
			time.Sleep(d)
			continue
		}

		failures = 0
		first := !everUp
		everUp = true
		ref.set(tunnel)
		if onUp != nil {
			onUp(gen, first)
		}

		// 等到这条隧道被判定不可用（ICE Failed 触发 cancelAttempt）。
		<-attemptCtx.Done()

		ref.set(nil)
		if tunnel.assoc != nil {
			_ = tunnel.assoc.Close()
		}
		if agent != nil {
			_ = agent.Close()
		}
		cancelAttempt()
		logf("结论：隧道已断开，开始重连")
	}
}

// 房主侧

// guestSlot 记一个客机的接纳状态；以代次区分「重连」（代次更高）与「同一份」。
type guestSlot struct {
	known  bool
	gen    int
	busy   bool
	seenAt time.Time

	// 在场状态：仅身份校验通过的那一代写入，断开时清掉；安全码按代次计算。
	conn   bool
	code   string
	mode   string
	connAt time.Time
	// gone 表示这一代已收尾，用于挡住迟到的 markConnected。
	gone bool
}

// rosterEntry 是推给界面的一条在场记录，字段名短以挤进一行日志。
type rosterEntry struct {
	Code  string `json:"c"` // 安全码，房主与玩家各自屏幕上的那一串
	Mode  string `json:"m"` // 直连 / 中继
	Since int64  `json:"s"` // 已连秒数（发出时刻的快照）
}

type guestRegistry struct {
	mu sync.Mutex
	m  map[string]*guestSlot
}

func newGuestRegistry() *guestRegistry { return &guestRegistry{m: map[string]*guestSlot{}} }

// claimResult 是「认领客机」的三种结果；「忙」需立即重试，「代次不够新」无需动作。
type claimResult int

const (
	claimAccepted claimResult = iota // 认领成功，交给调用方去建链
	claimBusy                        // 上一代还在处理中，等它结束再试
	claimStale                       // 代次不高于已接纳的，无需动作
)

func (r claimResult) String() string {
	switch r {
	case claimAccepted:
		return "已认领"
	case claimBusy:
		return "上一代还在处理"
	case claimStale:
		return "代次不够新"
	}
	return fmt.Sprintf("未知(%d)", int(r))
}

// markConnected 记录身份校验通过的在场状态；靠 gone 挡住错序到达的调用。
func (g *guestRegistry) markConnected(key, code, mode string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.m[key]
	if s == nil || !s.busy || s.gone {
		return
	}
	s.conn, s.code, s.mode, s.connAt = true, code, mode, time.Now()
}

// markGone 这一代下线：断开、失败、被清理都走这里。
func (g *guestRegistry) markGone(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.m[key]; s != nil {
		s.conn, s.code, s.mode, s.gone = false, "", "", true
	}
}

// roster 返回当前在场名单，按上线时刻倒序；Since 为上线时刻，时长由界面自算。
func (g *guestRegistry) roster() []rosterEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]rosterEntry, 0, len(g.m))
	for _, s := range g.m {
		if !s.conn {
			continue
		}
		mode := s.mode
		if mode == "" {
			mode = "直连"
		}
		out = append(out, rosterEntry{Code: s.code, Mode: mode, Since: s.connAt.Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since > out[j].Since })
	return out
}

// rosterEmitMu 串行化名单输出，避免多行内容交错拼出半行。
var rosterEmitMu sync.Mutex

// rosterPayload 组装名单那一行的 JSON 内容（不含 ##GUESTS## 前缀）。
func rosterPayload(g *guestRegistry) string {
	list := g.roster()
	if list == nil {
		list = []rosterEntry{} // 让 JSON 里是 []，界面少判一次 null
	}
	payload, err := json.Marshal(struct {
		N   int           `json:"n"`
		Who []rosterEntry `json:"who"`
	}{len(list), list})
	if err != nil {
		return ""
	}
	return string(payload)
}

// emitRoster 把在场名单推给界面，仅在内容变化时发送。
func emitRoster(g *guestRegistry) {
	line := guiMarker("GUESTS", rosterPayload(g))
	if line == "" {
		return
	}
	rosterEmitMu.Lock()
	defer rosterEmitMu.Unlock()
	if lastRosterEmit == line {
		return
	}
	lastRosterEmit = line
	fmt.Println(line)
}

var lastRosterEmit string

// claim 尝试认领一个客机：代次比已接纳的更高、且当前没有在处理的，返回 claimAccepted。
func (g *guestRegistry) claim(key string, gen int) claimResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.m[key]
	if s == nil {
		s = &guestSlot{}
		g.m[key] = s
	}
	if s.busy {
		return claimBusy
	}
	if s.known && gen <= s.gen {
		return claimStale
	}
	s.known, s.gen, s.busy, s.seenAt = true, gen, true, time.Now()
	s.conn, s.code, s.mode, s.gone = false, "", "", false
	return claimAccepted
}

func (g *guestRegistry) release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.m[key]; s != nil {
		s.busy = false
		s.seenAt = time.Now()
	}
}

// sweep 清理长时间无动静的键。
func (g *guestRegistry) sweep(idle time.Duration) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	n := 0
	for k, s := range g.m {
		if !s.busy && now.Sub(s.seenAt) > idle {
			delete(g.m, k)
			n++
		}
	}
	return n
}
