package main

// guihtml.go 控制台页面骨架，编在 exe 内，不落文件、不依赖外网。
// 界面按信号链组织（本机 → 入口 → 房间 → 玩家），空闲时无无限动画。
// 只有 DOM 骨架与资源引用：样式在 assets/gui.css、脚本在 assets/gui.js，
// 两者随 exe 内嵌并由本程序托管在 /assets/ 下（见 guiassets.go）。
// Go 侧不操作 DOM、不生成动画逻辑，只把当前设置写进 <html> 属性与首屏 JSON。

const guiShellHTML = `<!doctype html>
<!--
  界面页：服务端一次性渲染，不依赖外网；设置存在存档目录的 config.json 里。

  样式改动的性能约束：
    模糊只加在局部面板，整屏图层（.bg / .aurora / .grid / .modal）与
    持续追加或随滚动、搜索重算的区域（.logpanel / .log / .tasks / .op / 侧栏面板）不加；
    任务执行期间整体让位（data-vfx-busy），结束后恢复；不写 will-change，淡入只动 opacity。
    美化层（gui.css 的「视觉美化（VFX）」段与 gui.js 的 VFX 模块）可整段删除，
    界面回到实心扁平外观，功能不受影响。
-->
<html lang="zh-CN"@@UIATTRS@@>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mclbx 联机工具</title>
<link rel="icon" href="/assets/favicon.png">
<script>
/* 主题在首屏之前定下来：把"跟随系统"解析成当前系统偏好并跟随它变化。
   服务端已把用户选择写进 <html> 属性；此处不能等界面脚本加载完，否则浅色系统会先闪深色。 */
(function(){
  var u = @@UIJSON@@;
  window.MCLBX_UI = u;
  var el = document.documentElement, mq = null;
  try{ mq = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)'); }catch(e){ mq = null; }
  function resolve(){
    var t = u.theme;
    if(u.followOS){ t = (mq && mq.matches) ? 'light' : 'dark'; }
    el.setAttribute('data-theme', t);
  }
  resolve();
  if(u.followOS && mq && mq.addEventListener){ mq.addEventListener('change', resolve); }
  // 设置面板改主题的唯一入口：切到"跟随系统"时重新解析。
  // 解析函数只此一份，别处不得重复实现。
  window.MCLBX_THEME = function(theme){
    u.theme = theme || 'auto';
    u.followOS = (u.theme === 'auto');
    resolve();
  };
})();
</script>
<link rel="stylesheet" href="/assets/gui.css">
</head>
<body>
<div class="bg"><div class="wall"></div><div class="scrim"></div><div class="aurora"></div><div class="grid"></div></div>

<div class="shell" id="app">
  <!-- 顶栏 = 身份 + 页面导航 + 连接进展 + 当前状态，顺序固定。
       窗口变窄时依次让位进度链、导航说明；身份与状态始终保留。 -->
  <header class="topbar">
    <span class="glyph"></span>
    <div class="brand"><b>mclbx</b><span class="sub">联机工具</span></div>
    <nav class="nav" id="nav" aria-label="页面">
      <button class="navi on" id="btnWork" data-page="work">工作台</button>
      <button class="navi" id="btnSettings" data-page="settings" title="主题外观、辅助工具与程序状态">设置</button>
      <button class="navi" id="btnManual" data-page="manual">说明书</button>
    </nav>
    <div class="spacer"></div>
    <div class="linkbar" id="linkbar" title="当前连接进展">
      <span class="n on">本机</span><span class="s on"></span>
      <span class="n" id="lnkEntry">入口</span><span class="s" id="lnkS2"></span>
      <span class="n" id="lnkRoom">房间</span><span class="s" id="lnkS3"></span>
      <span class="n" id="lnkPeer">玩家</span>
    </div>
    <div class="stat"><span class="dot" id="pulse"></span><span id="status">空闲</span></div>
  </header>

  <div class="notice" id="notice"><i></i><span id="noticeText"></span></div>

  <!-- ── 页一：工作台 ────────────────────────────────────────────────── -->
  <main class="page" id="pageWork">
    <aside class="side">
      <section class="panel panel-flat">
        <div class="ph"><h2>本机环境</h2></div>
        <div class="cap" id="cap"></div>
      </section>
      <!-- 操作列表常驻：按用途分组、带搜索。 -->
      <section class="panel panel-flat grow">
        <div class="ph"><h2>操作</h2>
          <button class="linkbtn" id="btnProbe" title="重新执行环境检测">重新检测</button></div>
        <div class="search"><span class="ic"></span><input id="q" placeholder="搜索操作" autocomplete="off"></div>
        <div class="sidecount" id="cnt"></div>
        <div class="tasks" id="tasks"></div>
      </section>
    </aside>

    <div class="stage">
      <!-- 一步开局：房主填一个房间码，玩家一次粘贴；其余参数后置到侧栏操作列表与悬浮提示。 -->
      <section class="panel quick" id="quick">
        <div class="qhead">
          <span class="qt">快速开始</span>
          <span class="qs" id="qHint">选择一项操作开始</span>
        </div>
        <div class="qbtns">
          <button class="btn qbtn" id="qHost">
            <span class="qi">创建房间</span>
            <span class="qd">在本机开启入口，供玩家连接</span>
          </button>
          <button class="btn qbtn" id="qGuest">
            <span class="qi">加入房间</span>
            <span class="qd">使用房主提供的入口连接</span>
          </button>
        </div>

        <div class="qform" id="qHostForm" hidden>
          <div class="qpre">请先在游戏内开启局域网：<b>Esc → 对局域网开放</b>，然后回到此处创建房间。</div>
          <label for="qRoom">房间码</label>
          <input id="qRoom" placeholder="留空自动生成" maxlength="24" autocomplete="off">
          <label for="qPort">端口</label>
          <input id="qPort" class="tiny" value="25565" inputmode="numeric" autocomplete="off"
                 title="与游戏内「对局域网开放」显示的端口一致">
          <button class="btn primary" id="qHostGo">创建房间</button>
          <button class="linkbtn" id="qHostCancel">取消</button>
          <div class="qnote">完成后生成邀请文本，可整段发送给玩家。</div>
        </div>

        <div class="qform" id="qGuestForm" hidden>
          <label for="qInvite">房主提供的邀请</label>
          <textarea id="qInvite" rows="2" placeholder="粘贴邀请文本" autocomplete="off"></textarea>
          <button class="btn primary" id="qGuestGo">连接</button>
          <button class="linkbtn" id="qGuestCancel">取消</button>
          <div class="qnote" id="qGuestNote">将自动提取其中的地址与房间码。</div>
        </div>
      </section>

      <div class="banner" id="fwBanner" hidden>
        <span class="bi"></span>
        <span class="bt" id="fwText"></span>
        <button class="btn sm amber" id="fwFix">放行入站</button>
        <button class="linkbtn" id="fwSkip">忽略</button>
      </div>

      <!-- 入口地址卡：本工具唯一交付物，字号最大、位置最靠前 -->
      <section class="panel hero" id="cardAddr" hidden>
        <span class="shine" id="sheen"></span>
        <div class="k">玩家入口地址</div>
        <div class="plate"><div class="addr" id="addr">-</div></div>
        <div class="rack">
          <button class="btn sm" id="btnCopyAddr">复制地址</button>
          <span class="chip grp" id="roomChip" hidden></span>
        </div>
        <!-- 邀请卡：地址、房间码、未装工具的连法、已装工具的命令，配一个复制按钮。 -->
        <div class="invite" id="inviteWrap" hidden>
          <div class="invhead">
            <span>发送给玩家的邀请</span>
            <button class="btn sm" id="btnCopyInvite">复制邀请</button>
          </div>
          <div class="invtext" id="inviteText"></div>
        </div>
        <!-- 在场名单：仅在有人通过身份校验后出现（连上不算）。安全码放在显眼位置。 -->
        <div class="guests" id="guestWrap" hidden>
          <div class="ghead">在场玩家 <span id="guestN">0</span> 人</div>
          <ul id="guestList"></ul>
        </div>
      </section>

      <section class="panel" id="workCard"></section>

      <section class="panel logpanel">
        <div class="loghead">
          <div class="k">日志</div>
          <div class="segs" id="segs">
            <button class="seg on" data-f="user">结论</button>
            <button class="seg" data-f="raw">原始输出</button>
          </div>
          <div class="r">
            <span class="linecnt" id="linecnt">0 行</span>
            <button class="btn sm" id="btnClear">清空</button>
            <button class="btn sm" id="btnCopyLog">复制</button>
          </div>
        </div>
        <div class="log" id="logUser" role="log" aria-live="polite"></div>
        <div class="log" id="logRaw" role="log" aria-live="polite" hidden></div>
      </section>
    </div>
  </main>

  <!-- ── 页二：设置（主题外观 / 辅助工具 / 关于与状态） ──────────────── -->
  <main class="page setpage" id="pageSettings" hidden>
    <div class="mset" id="settingsBody"></div>
  </main>

  <!-- ── 页三：说明书。独立文档（iframe 内自带排版）。 -->
  <main class="page" id="pageManual" hidden>
    <iframe id="manualFrame" title="使用说明书"></iframe>
  </main>

  <!-- 状态栏 = 当前任务 + 保存状态 + 常驻小工具 + 快捷键 + 生命周期动作。 -->
  <footer class="statusbar">
    <span class="sb-task" id="ftTask">未选择操作</span>
    <span class="sb-save" id="saveState"></span>
    <span class="gap"></span>
    <span class="bgm"><button class="btn sm" id="bgmToggle" title="背景音乐：左键播放/暂停，右键下一首">♪ <span id="bgmName">未播放</span></button></span>
    <span class="sb-keys">Enter 执行 · Esc 取消</span>
    <span class="sb-life">
      <button class="btn sm danger" id="btnStop" disabled>停止</button>
      <button class="btn sm" id="btnQuit">退出</button>
    </span>
  </footer>
</div>

<!-- 背景音乐：播放交给界面内核的 <audio>，Go 侧只列曲库与提供文件。
     preload=none：不点播放不读写磁盘，空闲时路径开销为零。 -->
<audio id="bgm" preload="none"></audio>

<!-- 唯一的浮层：操作原理性说明（点开看完即关）。 -->
<div class="modal" id="modal" hidden role="dialog" aria-modal="true" aria-labelledby="modalTitle">
  <div class="mbox" tabindex="-1">
    <div class="mhead"><span class="mt" id="modalTitle"></span><span class="spacer"></span>
      <button class="btn sm" id="modalClose">关闭</button></div>
    <div class="mbody" id="modalBody"></div>
  </div>
</div>

<div class="toast" id="toast" hidden></div>

<script src="/assets/gui.js"></script>
</body>
</html>
`
