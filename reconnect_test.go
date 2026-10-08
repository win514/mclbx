package main

// reconnect_test.go —— 断线重连四项基础的回归测试。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 房主侧：代次接纳

// 只有更高的代次才重新接纳。
func TestGuestRegistryAcceptsOnlyNewerGenerations(t *testing.T) {
	g := newGuestRegistry()
	const key = "guest-abc"

	if got := g.claim(key, 1); got != claimAccepted {
		t.Fatalf("第一次登记应当被接纳，实际 %v", got)
	}
	g.release(key)

	if got := g.claim(key, 1); got != claimStale {
		t.Fatalf("同一个代次又来一次不该重新建链（轮询每转一圈都会看到同一份候选），实际 %v", got)
	}
	if got := g.claim(key, 2); got != claimAccepted {
		t.Fatalf("代次变高就是重连，必须重新接纳 —— 否则断线之后这个人再也进不来，实际 %v", got)
	}
	g.release(key)

	if got := g.claim(key, 3); got != claimAccepted {
		t.Fatalf("更高的代次应当被接纳，实际 %v", got)
	}
}

// 「上一代还在处理」与「代次不够新」必须区分，二者处置相反。
func TestClaimDistinguishesBusyFromStale(t *testing.T) {
	g := newGuestRegistry()
	const key = "guest-x"

	if got := g.claim(key, 1); got != claimAccepted {
		t.Fatalf("首次登记应当被接纳，实际 %v", got)
	}
	if got := g.claim(key, 2); got != claimBusy {
		t.Fatalf("上一代还在处理时应当报「忙」，实际 %v", got)
	}
	g.release(key)
	if got := g.claim(key, 1); got != claimStale {
		t.Fatalf("代次不比已接纳的高，应当报「代次不够新」，实际 %v", got)
	}
	if got := g.claim(key, 2); got != claimAccepted {
		t.Fatalf("腾出手之后，更高的代次应当被接纳，实际 %v", got)
	}
}

// 长期运行时应清理空闲键。
func TestGuestRegistrySweepsIdleButNotBusy(t *testing.T) {
	g := newGuestRegistry()
	g.claim("guest-idle", 1)
	g.release("guest-idle")
	g.claim("guest-live", 1) // 一直忙

	g.mu.Lock()
	g.m["guest-idle"].seenAt = time.Now().Add(-time.Hour)
	g.m["guest-live"].seenAt = time.Now().Add(-time.Hour)
	g.mu.Unlock()

	if n := g.sweep(10 * time.Minute); n != 1 {
		t.Fatalf("应当只回收 1 个空闲键，实际 %d", n)
	}
	g.mu.Lock()
	_, idleLeft := g.m["guest-idle"]
	_, liveLeft := g.m["guest-live"]
	g.mu.Unlock()
	if idleLeft {
		t.Fatal("长时间没动静的键应当被回收")
	}
	if !liveLeft {
		t.Fatal("正在处理的键不能被回收，否则它的 release 会落到空处")
	}
}

// 玩家侧：代次对齐

// 不能把上次断线留下的候选当作本次回话。
func TestHostBlobForGenRejectsStaleAnswer(t *testing.T) {
	cases := []struct {
		blobGen int
		myGen   int
		want    bool
		why     string
	}{
		{0, 1, true, "旧版本房主不回代次，第一次尝试按老行为放行"},
		{0, 2, false, "重连时不能拿旧房主的回话当本次的"},
		{1, 2, false, "这是上一次断线留下的那一份"},
		{2, 2, true, "针对本次尝试的回话"},
		{3, 2, true, "房主比我更靠前也算对上了"},
	}
	for _, c := range cases {
		if got := hostBlobForGen(candBlob{Gen: c.blobGen}, c.myGen); got != c.want {
			t.Fatalf("房主代次 %d、本次代次 %d：期望 %v（%s），实际 %v",
				c.blobGen, c.myGen, c.want, c.why, got)
		}
	}
}

// 隧道间接层

// 隧道未建好时本地连接应被明确拒绝并返回可判定错误。
func TestTunnelRefRefusesBeforeFirstTunnel(t *testing.T) {
	var ref tunnelRef
	if ref.get() != nil {
		t.Fatal("还没建过隧道时应当是空")
	}
	_, err := ref.open()
	if !errors.Is(err, errTunnelDown) {
		t.Fatalf("隧道为空时应当返回 errTunnelDown，实际 %v", err)
	}

	tun := &guestTunnel{}
	ref.set(tun)
	if ref.get() != tun {
		t.Fatal("set 之后应当取到同一条隧道")
	}
	ref.set(nil)
	if ref.get() != nil {
		t.Fatal("断开后要放回空值，下一次连接才会被拒绝而不是用到死隧道")
	}
}

// 失败分类与退避

// 不可重试的错误必须可识别。
func TestFatalErrorsSurviveWrapping(t *testing.T) {
	base := fatal("房主公布的房间码 %q 与本地不一致", "abc123")
	if !isFatal(base) {
		t.Fatal("fatal 造出来的错误应当被判为不可重试")
	}
	if !isFatal(fmt.Errorf("外层包装：%w", base)) {
		t.Fatal("被包装之后仍要认得出它是不可重试的")
	}
	if !strings.Contains(base.Error(), "abc123") {
		t.Fatalf("原因要保留在文案里，实际 %q", base.Error())
	}
	if isFatal(errors.New("一般的网络错误")) {
		t.Fatal("普通错误不能被当成不可重试")
	}
}

// 断网期间「信箱不可达」必须可重试，不能判为致命。
func TestSignallingFailureIsRetryableNotFatal(t *testing.T) {
	id, err := newIdentity()
	if err != nil {
		t.Fatal(err)
	}
	selfFP, err := parseFingerprint(id.fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	sess := &guestSession{
		base: "http://127.0.0.1:1", key: "tst001", room: "abc123",
		id: id, selfFP: selfFP, useSTUN: false, wait: 2 * time.Second,
	}
	_, _, err = establishGuestTunnel(context.Background(), sess, 1, nil)
	if err == nil {
		t.Fatal("信箱不可达时应当报错")
	}
	if !strings.Contains(err.Error(), "登记失败") {
		t.Fatalf("应当卡在登记这一步，实际：%v", err)
	}
	if isFatal(err) {
		t.Fatal("登记失败必须可重试：断网期间正是这种情况，判成致命会让断线不再重连")
	}
}

// 退避须单调不减且封顶。
func TestBackoffGrowsAndIsCapped(t *testing.T) {
	if got := backoffFor(1); got != reconnectBackoffMin {
		t.Fatalf("第一次失败后应当等 %v，实际 %v", reconnectBackoffMin, got)
	}
	if got := backoffFor(2); got != 2*reconnectBackoffMin {
		t.Fatalf("第二次失败后应当翻倍到 %v，实际 %v", 2*reconnectBackoffMin, got)
	}
	prev := time.Duration(0)
	for n := 1; n <= 40; n++ {
		d := backoffFor(n)
		if d < prev {
			t.Fatalf("第 %d 次的退避 %v 比上一次 %v 还小", n, d, prev)
		}
		if d > reconnectBackoffMax {
			t.Fatalf("第 %d 次的退避 %v 超过了上限 %v", n, d, reconnectBackoffMax)
		}
		prev = d
	}
	if got := backoffFor(1000); got != reconnectBackoffMax {
		t.Fatalf("失败很多次之后应当稳定在上限 %v，实际 %v", reconnectBackoffMax, got)
	}
}

// 信箱计数

// /rev 每次写入都自增，覆盖同一个键也算一次。
func TestMailboxRevAdvancesOnEveryWrite(t *testing.T) {
	mb := newMailbox()
	srv := httptest.NewServer(mb)
	defer srv.Close()

	rev, ok := mailboxRev(srv.URL, 3*time.Second)
	if !ok || rev != 0 {
		t.Fatalf("新信箱的计数应当是 0 且可读，实际 rev=%d ok=%v", rev, ok)
	}

	if err := postBlob(srv.URL, "guest-a", candBlob{Room: "r1", Gen: 1}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	rev1, ok := mailboxRev(srv.URL, 3*time.Second)
	if !ok || rev1 != 1 {
		t.Fatalf("写入一次之后应当是 1，实际 rev=%d ok=%v", rev1, ok)
	}

	// 同一个键再写一次（重连就是这个动作）：计数必须继续往前走
	if err := postBlob(srv.URL, "guest-a", candBlob{Room: "r1", Gen: 2}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	rev2, _ := mailboxRev(srv.URL, 3*time.Second)
	if rev2 != 2 {
		t.Fatalf("同一个键再写一次也要让计数往前走（否则房主会漏掉重连），实际 rev=%d", rev2)
	}

	// 没有任何写入时读数不能变，否则房主每轮都会去列表取信
	rev3, _ := mailboxRev(srv.URL, 3*time.Second)
	if rev3 != rev2 {
		t.Fatalf("没有写入时计数不该变：%d -> %d", rev2, rev3)
	}
}

// 取不到 /rev 时应可识别，调用方退回逐个取信。
func TestMailboxRevReportsMissingEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if _, ok := mailboxRev(srv.URL, 3*time.Second); ok {
		t.Fatal("对端没有 /rev 时应当报 ok=false，好让调用方退回逐个取信")
	}
	// 地址不通也算取不到
	if _, ok := mailboxRev("http://127.0.0.1:1", 300*time.Millisecond); ok {
		t.Fatal("连不上时应当报 ok=false")
	}
}

// 代次总线：0 不写入 JSON，非 0 带上。
func TestCandBlobGenRoundTrip(t *testing.T) {
	b, err := json.Marshal(candBlob{Ufrag: "u", Pwd: "p", Room: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "gen") {
		t.Fatalf("代次为 0 时不该出现在 JSON 里：%s", b)
	}

	b, err = json.Marshal(candBlob{Ufrag: "u", Pwd: "p", Room: "r", Gen: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"gen":3`) {
		t.Fatalf("代次非 0 时要带上：%s", b)
	}

	var back candBlob
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Gen != 3 {
		t.Fatalf("回读之后代次应当是 3，实际 %d", back.Gen)
	}
}
