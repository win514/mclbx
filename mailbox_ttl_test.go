package main

// mailbox_ttl_test.go —— 信箱过期回收与续登记测试。

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TTL 规格区间，写死在用例里，不引用生产常量以免放宽失效。
const (
	mailboxSpecTTLMin = 60 * time.Second // 再低会误杀正常等待的一方
	mailboxSpecTTLMax = 10 * time.Minute // 再高等于没有回收
)

func postStatus(t *testing.T, url string) int {
	t.Helper()
	const body = `{"ufrag":"u","pwd":"p","cands":["1.2.3.4:1"],"room":"r1"}`
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// 过期条目被回收后，新访客无需重启即可写入。
func TestMailboxExpiredEntriesFreeCapacityAgain(t *testing.T) {
	mb := newMailbox()
	srv := httptest.NewServer(mb)
	defer srv.Close()

	// 顺序灌到满（并发猛灌会让本机临时端口半路失败）
	posted := 0
	for i := 0; i < mailboxSpecSides*2; i++ {
		err := postBlob(srv.URL, fmt.Sprintf("junk-%05d", i), candBlob{Room: "r1"}, 5*time.Second)
		if err != nil {
			if !strings.Contains(err.Error(), "503") {
				t.Fatalf("灌第 %d 个键时出的不是「满了」，而是别的错：%v", i, err)
			}
			break
		}
		posted++
	}
	if posted != mailboxSpecSides {
		t.Fatalf("第 %d 个新键才被挡住，期望正好是规格上限 %d", posted+1, mailboxSpecSides)
	}

	// 先确认旧行为：满时新键进不来。
	if code := postStatus(t, srv.URL+"/blob/late-guest"); code != http.StatusServiceUnavailable {
		t.Fatalf("信箱满时新键应当被拒(503)，实际 %d", code)
	}

	// 把时间戳回拨成已过期，只改时间戳不调用 sweepLocked，由生产路径触发回收。
	stale := time.Now().Add(-mailboxTTL - time.Second)
	mb.mu.Lock()
	for k, e := range mb.m {
		e.seen = stale
		mb.m[k] = e
	}
	mb.mu.Unlock()

	// 同一个新访客此时应能写入（靠 POST 里的先回收再判容量）。
	if code := postStatus(t, srv.URL+"/blob/late-guest"); code != http.StatusNoContent {
		t.Fatalf("过期条目被回收之后，新键应当能存进来(204)，实际 %d"+
			"（如果这里是 503，说明 POST 路径上没有做回收）", code)
	}
	// 表里应只剩刚写入的这一条
	mb.mu.Lock()
	left := len(mb.m)
	_, only := mb.m["late-guest"]
	mb.mu.Unlock()
	if left != 1 || !only {
		t.Fatalf("回收 + 新写入之后表里应当只剩 late-guest 这一条，实际 %d 条，只看 late-guest=%v",
			left, only)
	}
}

// 续登记：先到的一方在等待期间不会因 TTL 过期。
func TestMailboxKeepAliveKeepsWaitingSideAlive(t *testing.T) {
	mb := newMailbox()
	srv := httptest.NewServer(mb)
	defer srv.Close()

	mk := func(side string) candBlob {
		b := candBlob{Ufrag: "u", Pwd: "p", Cands: []string{"1.2.3.4:1"}, Room: "r1"}
		if err := postBlob(srv.URL, side, b, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		return b
	}
	kept := mk("host") // 有续登记的一方
	_ = mk("idle")     // 对照组：无人续登记

	keep := newBlobKeeper(srv.URL, "host", kept)

	// 未到间隔不应重发
	if keep.tick(keep.last.Add(mailboxKeepAlive/2), time.Second) {
		t.Fatal("还没到间隔就重新登记了")
	}

	// 两条都回拨到只差 10 秒过期；因时钟粒度，不判断时间戳变大，只看回收结果。
	longAgo := time.Now().Add(-mailboxTTL + 10*time.Second)
	mb.mu.Lock()
	for _, k := range []string{"host", "idle"} {
		e := mb.m[k]
		e.seen = longAgo
		mb.m[k] = e
	}
	mb.mu.Unlock()

	// 到点重发一次（等待方心跳）
	if !keep.tick(keep.last.Add(mailboxKeepAlive), time.Second) {
		t.Fatal("到了间隔没有重新登记")
	}

	// 同一时刻清扫，两条结局须不同：续登记者时间戳被推后，仍存活。
	mb.mu.Lock()
	n := mb.sweepLocked(longAgo.Add(mailboxTTL + time.Second))
	_, keptAlive := mb.m["host"]
	_, idleAlive := mb.m["idle"]
	mb.mu.Unlock()
	if idleAlive {
		t.Fatal("对照组（没人续登记）在这个时刻本来就该过期：说明清扫时刻没算对，下面的结论不算数")
	}
	if !keptAlive {
		t.Fatal("续过登记的一方仍被回收了：等待方会被静默过期掉，而「开着游戏等人」正是这个工具的主要用法")
	}
	if n != 1 {
		t.Fatalf("应当只回收对照组那 1 条，实际回收 %d 条", n)
	}

	// 心跳间隔须显著小于 TTL
	if mailboxKeepAlive*3 > mailboxTTL {
		t.Fatalf("续登记间隔 %v 相对 TTL %v 太大：心跳漏几次就会把等待方过期掉",
			mailboxKeepAlive, mailboxTTL)
	}
	if mailboxTTL < mailboxSpecTTLMin || mailboxTTL > mailboxSpecTTLMax {
		t.Fatalf("TTL=%v 超出规格区间 [%v, %v]：太小会误杀等待方，太大等于没有回收",
			mailboxTTL, mailboxSpecTTLMin, mailboxSpecTTLMax)
	}

	// 续登记不能让条目永生：以其被续后的时间戳为基准，超 TTL 仍可过期。
	mb.mu.Lock()
	seenNow := mb.m["host"].seen
	n2 := mb.sweepLocked(seenNow.Add(mailboxTTL + time.Second))
	_, still := mb.m["host"]
	mb.mu.Unlock()
	if n2 != 1 || still {
		t.Fatalf("续登记之后的条目仍应可过期：回收 %d 条，仍在=%v", n2, still)
	}
}

// 取信方不应拿到过期候选。
func TestMailboxExpiredEntryReadsAsNotFound(t *testing.T) {
	mb := newMailbox()
	srv := httptest.NewServer(mb)
	defer srv.Close()

	if err := postBlob(srv.URL, "host", candBlob{Ufrag: "u", Pwd: "p", Room: "r1"}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	// 把时间戳推到 TTL 之前
	mb.mu.Lock()
	e := mb.m["host"]
	e.seen = time.Now().Add(-mailboxTTL - time.Second)
	mb.m["host"] = e
	mb.mu.Unlock()

	if _, err := fetchBlob(srv.URL, "host", 3*time.Second); err == nil {
		t.Fatal("过期的条目不该还能取到内容：取信方会拿着过期候选去连")
	}
	// 列表里也不应包含它
	keys, err := listBlobs(srv.URL, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("过期条目不该出现在列表里，实际还有 %v", keys)
	}
}
