package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// webui.go 提供本地管理页：仅绑定 127.0.0.1，暴露状态查询与玩家地址接口。

type statusPayload struct {
	Room     string   `json:"room"`
	Address  string   `json:"address"`
	GamePort int      `json:"gamePort"`
	Entry    int      `json:"entry"`
	Accepted uint64   `json:"accepted"`
	Rejected uint64   `json:"rejected"`
	Nat      string   `json:"nat"`
	Upnp     string   `json:"upnp"`
	V6       []string `json:"v6"`
	Logs     []string `json:"logs"`
	Uptime   string   `json:"uptime"`
}

func (g *gateState) snapshot() statusPayload {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := statusPayload{
		Room:     g.room,
		Address:  g.address,
		GamePort: g.gamePort,
		Entry:    g.entryPort,
		Accepted: g.accepted.Load(),
		Rejected: g.rejected.Load(),
		Nat:      g.natInfo,
		Upnp:     g.upnpInfo,
		Uptime:   time.Since(g.started).Round(time.Second).String(),
	}
	for _, a := range g.v6 {
		tag := ""
		if a.Temporary {
			tag = "（临时）"
		}
		p.V6 = append(p.V6, fmt.Sprintf("%s%s  [%s]", a.IP, tag, a.IfName))
	}
	p.Logs = append(p.Logs, g.logs...)
	return p
}

func serveWeb(addr string, g *gateState) error {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return fmt.Errorf("管理页只允许绑定 127.0.0.1（当前为 %s）", addr)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(pageHTML))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(g.snapshot())
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	logf("管理页已启动：http://%s", addr)
	if err := srv.ListenAndServe(); err != nil {
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
	return nil
}

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mclbx 联机工具</title>
<style>
  :root{--bg:#0d1117;--fg:#e6edf3;--dim:#8b949e;--line:#21262d;--card:#161b22;--ok:#3fb950;--bad:#f85149;--accent:#2f81f7}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
  .wrap{max-width:860px;margin:0 auto;padding:32px 20px 60px}
  h1{font-size:20px;margin:0 0 4px}
  .sub{color:var(--dim);margin-bottom:24px}
  .card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:18px 20px;margin-bottom:16px}
  .label{color:var(--dim);font-size:12px;letter-spacing:.04em;text-transform:uppercase}
  .addr{font-family:ui-monospace,Consolas,monospace;font-size:16px;word-break:break-all;background:#0b0f14;border:1px solid var(--line);border-radius:8px;padding:12px 14px;margin:8px 0}
  button{background:var(--accent);color:#fff;border:0;border-radius:8px;padding:9px 16px;font-size:13px;cursor:pointer}
  button:hover{filter:brightness(1.1)}
  .stats{display:grid;grid-template-columns:repeat(4,1fr);gap:12px}
  .stat{background:#0b0f14;border:1px solid var(--line);border-radius:8px;padding:10px 12px}
  .stat b{display:block;font-size:20px;font-family:ui-monospace,Consolas,monospace}
  .grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
  .kv{font-size:13px}
  .kv span{color:var(--dim)}
  pre{background:#0b0f14;border:1px solid var(--line);border-radius:8px;padding:12px;max-height:300px;overflow:auto;font-size:12.5px;margin:8px 0 0}
  .ok{color:var(--ok)} .bad{color:var(--bad)}
  ol{padding-left:20px;color:var(--dim)} ol b{color:var(--fg)}
  @media(max-width:640px){.stats{grid-template-columns:repeat(2,1fr)}.grid{grid-template-columns:1fr}}
</style>
</head>
<body>
<div class="wrap">
  <h1>联机房间 <span id="room">…</span></h1>
  <div class="sub">玩家在游戏「多人游戏 → 直接连接」中粘贴以下地址即可。</div>

  <div class="card">
    <div class="label">玩家入口地址（房间码即口令）</div>
    <div class="addr" id="addr">…</div>
    <button onclick="copyIt()">复制地址</button>
    <button style="background:#30363d" onclick="copyMsg()">复制提示文本</button>
    <ol>
      <li>玩家打开游戏，进入 <b>多人游戏</b> → <b>直接连接</b></li>
      <li>粘贴上述地址并回车</li>
      <li>若无法连接，确认玩家一侧具备 IPv6 出口</li>
    </ol>
  </div>

  <div class="card">
    <div class="stats">
      <div class="stat"><div class="label">已接受</div><b class="ok" id="acc">0</b></div>
      <div class="stat"><div class="label">已拒绝</div><b class="bad" id="rej">0</b></div>
      <div class="stat"><div class="label">游戏端口</div><b id="gp">-</b></div>
      <div class="stat"><div class="label">已运行</div><b id="up" style="font-size:15px">-</b></div>
    </div>
  </div>

  <div class="card">
    <div class="grid">
      <div class="kv"><span>公网 IPv6</span><div id="v6">-</div></div>
      <div class="kv"><span>公网端口映射</span><div id="nat">-</div><div style="margin-top:8px"><span>自动端口映射</span><div id="upnp">-</div></div></div>
    </div>
  </div>

  <div class="card">
    <div class="label">连接日志</div>
    <pre id="logs">等待…</pre>
  </div>
</div>
<script>
let addr = "";
function copyIt(){ navigator.clipboard.writeText(addr); }
function copyMsg(){ navigator.clipboard.writeText("打开游戏，进入「多人游戏 → 直接连接」，粘贴以下地址：\n" + addr); }
async function tick(){
  try{
    const r = await fetch("/api/status");
    const s = await r.json();
    addr = s.address;
    document.getElementById("room").textContent = s.room;
    document.getElementById("addr").textContent = s.address;
    document.getElementById("acc").textContent = s.accepted;
    document.getElementById("rej").textContent = s.rejected;
    document.getElementById("gp").textContent = s.gamePort;
    document.getElementById("up").textContent = s.uptime;
    document.getElementById("v6").textContent = (s.v6 && s.v6.length) ? s.v6.join("  /  ") : "无";
    document.getElementById("nat").textContent = s.nat || "-";
    document.getElementById("upnp").textContent = s.upnp || "-";
    document.getElementById("logs").textContent = (s.logs || []).slice(-60).join("\n");
  }catch(e){}
}
tick(); setInterval(tick, 1000);
</script>
</body>
</html>
`
