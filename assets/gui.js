'use strict';
/* ============================================================================
   行为层。四条纪律贯穿全篇：
     · 写 DOM 之前先比一下（setText/setCls/setHidden）—— 轮询每 700ms 一次，
       无条件写 textContent 等于让页面永远在做样式重算；
     · 日志分两层各一个容器，只追加新行、不整段重建；层间切换只切显示，不重建节点；
     · 没有任何 requestAnimationFrame 循环，反馈一律走 CSS 过渡；
     · 环境能力（来自体检的 ##CAP## 标记）决定哪些操作置灰，原因只写在悬浮提示里。
   ========================================================================== */
var $ = function(id){ return document.getElementById(id); };
function esc(s){
  var d = document.createElement('div');
  d.textContent = s == null ? '' : s;
  return d.innerHTML;
}
function setText(el, v){ if(el && el.textContent !== v){ el.textContent = v; } }
function setCls(el, v){ if(el && el.className !== v){ el.className = v; } }
function setHidden(el, h){ if(el && el.hidden !== !!h){ el.hidden = !!h; } }
function setHTML(el, v){ if(el && el.innerHTML !== v){ el.innerHTML = v; } }

var tasks = [], groups = [], sel = null;
var since = 0, jobId = '', running = false, polling = false, quitting = false;
var userLines = [], rawLines = [], layer = 'user';
var addr = '', joinCmd = '', roomCode = '', caps = null, capKey = '';
/* 口令是「房间码」还是「地址本身」（来自 ##GATE## 标记，见 expose.go 的 gateModeValue）。
   raw / 端口映射 / 公网 IPv4 直连这三种形态下地址本身就是口令，房间码只对「方式二」有意义 ——
   邀请文本要按这个区别写，否则读起来像是「码才是口令」，好人会去找地方填码，坏人多一句可捡的东西。*/
var gateByRoom = false;
var fwPort = '', fwSkipped = false, fwFixed = false;
/* 本工具有两个默认入站端口：
     游戏端口（公网入口，体检的 caps.port）与信令端口 8090（软件入口）。
   只放行前者会出现「没装工具的玩家能进、装了工具的反而不行」——
   这条现象用户几乎不可能自己想到，所以一键放行时两个一起放（规则仍限本程序与该端口）。 */
var fwSigPort = '8090';
function fwPortSpec(){ return (fwPort || '25565') + ',' + fwSigPort; }
var LOG_KEEP = (window.MCLBX_UI && window.MCLBX_UI.logKeep) || 2000;   // 每层留多少行（设置里可改）

var NATNAME = { fullcone: '全锥形', restricted: '受限锥形', portrestricted: '端口受限', symmetric: '对称型', unknown: '未知' };

/* ---------------------------------------------------------------- 提示 ---- */
var toastT = null;
function toast(msg, bad){
  var t = $('toast');
  t.textContent = msg;
  setCls(t, 'toast' + (bad ? ' bad' : ''));
  setHidden(t, false);
  clearTimeout(toastT);
  toastT = setTimeout(function(){ setHidden(t, true); }, 2200);
}
function copy(text){
  if(!text){ return; }
  var done = function(){ toast('已复制'); };
  if(navigator.clipboard && navigator.clipboard.writeText){
    navigator.clipboard.writeText(text).then(done, function(){ toast('复制失败', true); });
  } else {
    var ta = document.createElement('textarea'); ta.value = text;
    document.body.appendChild(ta); ta.select();
    try{ document.execCommand('copy'); done(); }catch(e){ toast('复制失败', true); }
    document.body.removeChild(ta);
  }
}
/* ------------------------------------------------ 视觉美化（VFX）----
   【视觉美化，非核心功能】
   这一段和上面那段 CSS 是一整套，删掉即可：界面回到实心扁平外观，工具功能一件不少。

   它只做一件事 —— 把设置里那几个值写成 <html> 上的属性，其余全交给 CSS。
   业务代码与它的接触面只有三处，且三处都不看返回值：
     · 设置生效时  VFX.apply(ui)
     · 任务启停时  VFX.busy(running)
     · 首屏        VFX.init(cfg)
   每个入口都兜了 try，所以这一段自身出问题也影响不到工具本身。

   两件事在这里：
     · apply()  写属性；浏览器不支持 backdrop-filter 时自动退成"关闭模糊"，不报错也不破版
     · busy()   任务执行期间摘掉毛玻璃（见 CSS 里那一段的说明）
   另有一个 VFX.measure()，把帧率量出来交给调用方 —— 设置页那行「帧率实测」与逐档对比测试都靠它。
   （原来的帧率巡检与"要不要降档"的询问随「帧率自动降级」那一项一起撤销了：那个询问的
   "以后别再问"只能存在设置里，设置项没了它就会反复开口。） */
var VFX = (function(){
  /* 档位是 0-10 的整数，"0" 就是关闭。GLASS_DEF 与 guiconfig.go 的 uiGlassDefault、
     面板里 UI_DEF 的那一份必须一致 —— 三处都对不上时症状是"首屏与设置里显示的不是同一个值"。 */
  var GLASS_MAX = 10, GLASS_DEF = '1';
  var st = { glass:GLASS_DEF, level:'0' };
  var probe = null;

  function root(){ return document.documentElement; }
  /* 档位一律收成 "0"…"10" 的字符串。服务端给的是归一化过的，但 ?vfx= 与旧内存值不保证。 */
  function normLevel(v){
    var n = parseInt(v, 10);
    if(isNaN(n) || n < 0){ return GLASS_DEF; }
    if(n > GLASS_MAX){ return String(GLASS_MAX); }
    return String(n);
  }
  /* 浏览器到底认不认 backdrop-filter。不认就退成"关闭模糊" —— 半透明底还在，版式不变。 */
  function supported(){
    try{
      return !!(window.CSS && CSS.supports && CSS.supports('backdrop-filter','blur(4px)'));
    }catch(e){ return false; }
  }
  /* 地址栏上的 ?vfx=0…10（老的 off/low/mid/high 仍然认）：只为"逐档各测一遍"用，**不落盘**。
     同一份界面不用改设置就能逐档对比，测完刷新即恢复用户自己的设置。 */
  function forced(){
    try{
      var m = /[?&]vfx=(off|low|mid|high|10|[0-9])(?:&|$)/.exec(location.search || '');
      if(!m){ return ''; }
      var alias = { off:'0', low:'3', mid:'6', high:'10' };
      return alias[m[1]] || m[1];
    }catch(e){ return ''; }
  }
  function busy(on){ try{ root().setAttribute('data-vfx-busy', on ? 'on' : 'off'); }catch(e){} }

  function apply(u){
    try{
      u = u || {};
      if(u.glass !== undefined && u.glass !== null && u.glass !== ''){ st.glass = normLevel(u.glass); }

      var lv = st.glass;
      if(lv !== '0' && !supported()){ lv = '0'; }
      var f = forced();
      if(f){ lv = f; }
      st.level = lv;

      var e = root();
      e.setAttribute('data-glass', lv);
      e.setAttribute('data-fade', u.fade === 'off' ? 'off' : 'on');
    }catch(err){ /* 美化失败不该影响工具 */ }
  }
  function init(cfg){
    try{
      if(cfg){ apply(cfg); }
    }catch(e){}
  }

  /* 量一段帧率：**有界的一小段** —— 连抓约一秒的帧间隔就停，不做常驻 rAF 循环。
     设置页那行「帧率实测」与逐档对比都走它；它只在被叫到时才跑，页面闲着时不留任何定时器。 */
  function sample(ms){
    return new Promise(function(res){
      var frames = 0, worst = 0, t0 = 0, last = 0;
      function step(t){
        if(!t0){ t0 = t; last = t; }
        var dt = t - last;
        if(frames > 0 && dt > worst){ worst = dt; }
        last = t; frames++;
        var span = t - t0;
        if(span >= ms){
          res({ frames:frames, ms:Math.round(span), fps:Math.round(frames * 1000 / span),
            worst:Math.round(dt * 10) / 10 });
          return;
        }
        requestAnimationFrame(step);
      }
      requestAnimationFrame(step);
    });
  }
  return {
    apply: apply,
    init: init,
    busy: busy,
    // 量一段帧率交给调用方，同时记一份供面板/控制台查看
    measure: function(ms){ return sample(ms || 1500).then(function(r){ probe = r; return r; }); },
    stats: function(){ return { level:st.level, glass:st.glass,
      supported:supported(), last:probe }; }
  };
})();

async function api(path, body){
  var opt = { method:'POST', headers:{'X-MCLBX-GUI':'1','Content-Type':'application/json'} };
  if(body){ opt.body = JSON.stringify(body); }
  var r = await fetch(path, opt);
  return await r.json();
}

/* ------------------------------------------------------------ 环境能力 ---- */
/* 体检结论的精简投影：只回答"这台机器能不能走这条操作"。 */
function capState(){
  if(!caps){ return null; }
  return {
    v6: caps.v6 === '1',
    mapping: caps.upnp === '1' || caps.natpmp === '1' || caps.pcp === '1',
    nat: caps.nat || 'unknown',
    port: caps.port || '',
    portFree: caps.portfree === '1'
  };
}
function setCaps(s){
  if(s === capKey){ return; }
  capKey = s;
  caps = {};
  s.split(/\s+/).forEach(function(kv){
    var i = kv.indexOf('=');
    if(i > 0){ caps[kv.slice(0, i)] = kv.slice(i + 1); }
  });
  renderCap();
  renderTasks();
  maybeFwHint();
}
function renderCap(){
  // 还没检测过的时候，以前是四个格子一起写"未检测" —— 四遍同一句话，
  // 既占地方又没人知道能不能点。并成一个按钮：说清"还没测"和"点这里"。
  if(!caps){
    setHTML($('cap'), '<button class="capcell unk wide" title="点击开始环境检测">'
      + '<span class="cl">环境检测</span><span class="cv">未检测 · 点击开始</span></button>');
    $('cap').firstChild.onclick = startProbe;
    return;
  }
  var c = capState(), defs = [
    {k:'v6', label:'公网 IPv6'}, {k:'map', label:'自动端口映射'},
    {k:'nat', label:'NAT 类型'}, {k:'port', label:'游戏端口'}
  ];
  var html = defs.map(function(d){
    var st = 'unk', val = '未检测', tip = '';
    if(c){
      if(d.k === 'v6'){
        st = c.v6 ? 'ok' : 'no'; val = c.v6 ? '有' : '无';
        tip = c.v6 ? '本机具备公网 IPv6，玩家在游戏内「直接连接」填该地址即可'
                   : '本机无公网 IPv6，玩家需安装本工具';
      } else if(d.k === 'map'){
        st = c.mapping ? 'ok' : 'no'; val = c.mapping ? '可用' : '不可用';
        tip = c.mapping ? '自动端口映射可用，玩家在游戏内「直接连接」填该地址即可'
                        : '自动端口映射不可用，该方式无法使用';
      } else if(d.k === 'nat'){
        val = NATNAME[c.nat] || '未知';
        st = (c.nat === 'fullcone') ? 'ok' : (c.nat === 'unknown' ? 'unk' : 'no');
        tip = '出站 NAT 类型：' + (NATNAME[c.nat] || '未知')
            + (c.nat === 'fullcone' ? '，适合直连' : '，直连成功率低，建议使用中继');
      } else {
        st = c.portFree ? 'ok' : 'no'; val = c.portFree ? '空闲' : '已占用';
        tip = '游戏端口 ' + c.port + (c.portFree ? ' 空闲' : ' 已被占用');
      }
    }
    return '<button class="capcell ' + st + '" title="' + esc(tip) + '">'
         +   '<span class="cl">' + d.label + '</span><span class="cv">' + val + '</span>'
         + '</button>';
  }).join('');
  setHTML($('cap'), html);
  Array.prototype.forEach.call($('cap').children, function(b){
    b.onclick = function(){ if(!caps){ startProbe(); } };
  });
}
// 体检正在跑的时候，那一格别还写着"还没做" —— 否则看着像点了没反应。
function setCapBusy(on){
  var b = $('cap').firstChild;
  if(!b || !b.className || b.className.indexOf('wide') < 0){ return; }
  var v = b.querySelector('.cv');
  if(v){ setText(v, on ? '检测中…' : '未检测 · 点击开始'); }
}
/* limit 返回该操作在当前环境下的限制：hard=true 置灰，false 只标记。原因一句话。 */
function limit(t){
  var c = capState();
  if(!c){ return null; }
  if(t.key === 'expose-raw' || t.key === 'expose-dns'){
    if(!c.v6){ return { hard:true, why:'本机无公网 IPv6' }; }
  }
  if(t.key === 'room' || t.key === 'expose-raw' || t.key === 'expose-dns' ||
     t.key === 'natmap' || t.key === 'ice-host'){
    if(!c.portFree){ return { hard:false, why:'游戏端口 ' + c.port + ' 已被占用' }; }
  }
  return null;
}

/* ------------------------------------------------------------ 左侧操作 ---- */
function renderTasks(){
  var byGroup = {}, order = [], lim = {};
  tasks.forEach(function(t){
    var l = limit(t);
    if(l){ lim[t.key] = l; }
    if(!byGroup[t.group]){ byGroup[t.group] = []; order.push(t.group); }
    byGroup[t.group].push(t);
  });
  groups = order;
  var html = '';
  order.forEach(function(g, gi){
    html += '<div class="opgrp g' + ((gi % 6) + 1) + '" data-g="' + esc(g) + '">'
          +   '<span class="tag"></span><span class="gl">' + esc(g) + '</span><span class="rule"></span></div>';
    byGroup[g].forEach(function(t){
      var l = lim[t.key] || null;
      var attrs = ' data-key="' + esc(t.key) + '"';
      if(l && l.hard){ attrs += ' data-off="1" title="不可用：' + esc(l.why) + '"'; }
      html += '<button class="op' + (l && l.hard ? ' off' : '') + '"' + attrs + '>'
            +   '<div class="nm">' + esc(t.name)
            +     (t['long'] ? '<span class="lg">长时</span>' : '')
            +     (l && !l.hard ? '<span class="lim" title="' + esc(l.why) + '">注意</span>' : '')
            +   '</div>'
            +   '<div class="ds">' + esc(t.desc) + '</div>'
            + '</button>';
    });
  });
  var box = $('tasks');
  box.innerHTML = html || '<div class="opempty">没有可用的操作</div>';
  Array.prototype.forEach.call(box.querySelectorAll('.op'), function(b){
    b.onclick = function(){
      if(b.getAttribute('data-off')){
        toast(b.getAttribute('title').replace(/^不可用：/, ''), true);
        return;
      }
      pick(b.getAttribute('data-key'));
    };
  });
  filterTasks();
}
function filterTasks(){
  var q = ($('q').value || '').trim().toLowerCase();
  var box = $('tasks'), shown = 0;
  Array.prototype.forEach.call(box.children, function(grp){
    if(!grp.classList || !grp.classList.contains('opgrp')){ return; }
    var g = (grp.getAttribute('data-g') || '').toLowerCase();
    var n = 0, hitGroup = !q || g.indexOf(q) >= 0;
    var next = grp.nextElementSibling;
    while(next && next.classList && next.classList.contains('op')){
      var t = taskByKey(next.getAttribute('data-key'));
      var hit = !q || hitGroup || ((t.name + ' ' + t.desc + ' ' + t.key).toLowerCase().indexOf(q) >= 0);
      next.classList.toggle('hide', !hit);
      if(hit){ n++; shown++; }
      next = next.nextElementSibling;
    }
    grp.classList.toggle('hide', n === 0);
    grp.style.display = (n === 0) ? 'none' : '';
  });
  setText($('cnt'), q ? ('匹配 ' + shown + ' 项') : (tasks.length + ' 项操作 · ' + groups.length + ' 个分组'));
}
function taskByKey(k){
  for(var i = 0; i < tasks.length; i++){ if(tasks[i].key === k){ return tasks[i]; } }
  return { name:'', desc:'', key:'', fields:[] };
}
function pick(key, force){
  var t = null;
  for(var i = 0; i < tasks.length; i++){ if(tasks[i].key === key){ t = tasks[i]; } }
  var l = t ? limit(t) : null;
  if(l && l.hard && !force){ toast('不可用：' + l.why, true); return; }
  sel = t;
  Array.prototype.forEach.call($('tasks').querySelectorAll('.op'), function(b){
    b.classList.toggle('on', !!sel && b.getAttribute('data-key') === sel.key);
  });
  renderWork();
}
function clearSel(){
  sel = null;
  Array.prototype.forEach.call($('tasks').querySelectorAll('.op'), function(b){ b.classList.remove('on'); });
  renderWork();
}
function startProbe(){
  var has = false;
  for(var i = 0; i < tasks.length; i++){ if(tasks[i].key === 'probe'){ has = true; } }
  if(!has){ toast('未找到环境检测', true); return; }
  pick('probe', true);
  startTask();
}

/* ------------------------------------------------------------ 主区渲染 ---- */
// 没选操作时的空缺：这里以前是第二份"创建房间 / 加入房间 / 环境检测"三选一，
// 和上面那两张卡长得几乎一样。分享出去之后最常收到的一句话就是
// "两个地方都能点，我该点哪个" —— 所以重复的那份删掉，只留一句话指路。
// 现在指的路是"侧栏那一列"，不再需要先点开一个「更多功能」才看得到。
function renderIdle(){
  var w = $('workCard');
  w.className = 'panel';
  setHTML(w, '<div class="idle">请选择左侧的一项操作，' +
    '或者用上面的<b>快速开始</b>直接创建 / 加入房间。</div>');
  syncFoot();
}
function fieldHTML(f){
  var id = 'f_' + f.id;
  var tip = f.hint ? ' title="' + esc(f.hint) + '"' : '';
  if(f.kind === 'check'){
    return '<div class="fld"><label class="ck"' + tip + '><input type="checkbox" id="' + id + '"'
         + (f['default'] === '1' ? ' checked' : '') + '>'
         + '<span class="bx"></span><span class="tx">' + esc(f.label) + '</span></label></div>';
  }
  return '<div class="fld">'
       +   '<label for="' + id + '"' + tip + '><span>' + esc(f.label) + '</span>'
       +   (f.required ? '<span class="r">必填</span>' : '')
       +   (f.hint ? '<span class="qi" title="' + esc(f.hint) + '">?</span>' : '')
       +   '</label>'
       +   '<input type="text" id="' + id + '" value="' + esc(f['default']) + '" placeholder="' + esc(f.placeholder) + '"' + tip + '>'
       + '</div>';
}
function renderWork(){
  if(!sel){ renderIdle(); return; }
  var w = $('workCard');
  w.className = 'panel formpanel';
  var l = limit(sel);
  var fs = (sel.fields || []).map(fieldHTML).join('');
  w.innerHTML =
      '<div class="workhead">'
    +   '<div class="whTop"><h2>' + esc(sel.name) + '</h2>'
    +     '<span class="chip grp">' + esc(sel.group) + '</span>'
    +     (sel['long'] ? '<span class="chip run-long">长时</span>' : '')
    +     (l && !l.hard ? '<span class="chip run-long" title="' + esc(l.why) + '">' + esc(l.why) + '</span>' : '')
    +     '<span class="whtool">'
    +       (sel.help ? '<button class="linkbtn" id="btnHelp">详情</button>' : '')
    +       '<button class="linkbtn" id="cmdToggle">命令</button>'
    +     '</span></div>'
    +   '<div class="whDesc">' + esc(sel.desc) + '</div>'
    +   '<code class="cmd" id="cmdBox" hidden></code>'
    + '</div>'
    + '<div class="fields">' + (fs || '<div class="hint">该操作无需填写参数。</div>') + '</div>'
    + '<div class="runbar">'
    +   '<button class="run" id="btnRun">开始执行</button>'
    +   '<div class="runhint" id="runHint">' + (sel['long']
          ? '长时运行，需手动停止；关闭窗口会一并停止。'
          : '执行结束后自动停止，输出见下方日志。') + '</div>'
    + '</div>';
  $('btnRun').onclick = startTask;
  if($('btnHelp')){ $('btnHelp').onclick = function(){ showHelp(sel.name, sel.help); }; }
  $('cmdToggle').onclick = function(){
    var b = $('cmdBox'), on = b.hidden;
    b.hidden = !on;
    b.textContent = 'mclbx ' + buildArgs(sel);
    this.textContent = on ? '收起命令' : '命令';
  };
  setRunningUI(running);
  syncFoot();
}
function showHelp(title, body){
  setText($('modalTitle'), title);
  setText($('modalBody'), body + ' 完整说明见《使用说明书》。');
  openModal();
}
function buildArgs(t){
  var parts = [t.key];
  (t.fields || []).forEach(function(f){
    var el = $('f_' + f.id);
    if(!el){ return; }
    if(f.kind === 'check'){ if(el.checked){ parts.push('--' + f.id); } return; }
    var v = (el.value || '').trim();
    if(v){ parts.push('--' + f.id, v); }
  });
  return parts.join(' ');
}
/* 状态栏左侧那句"现在在跑什么"。它和顶栏的状态胶囊是**两件事**，不是同一件事的两处：
   胶囊说的是"程序此刻的状态"（空闲 / 执行中 / 已结束），这里说的是"这一次跑的是哪个操作"。
   命令行不再挂在这一行（它是给写脚本的人看的），要看等价命令时点开表单右上角的「命令」。 */
function syncFoot(){
  setText($('ftTask'), sel ? ('本次操作：' + sel.name) : '未选择操作');
}
function setRunningUI(on){
  var b = $('btnRun'); if(b){ b.disabled = on; }
  var s = $('btnStop'); if(s){ s.disabled = !on && !quitting; }
  var c = $('cmdToggle');
  if(c && c.textContent === '收起命令'){ c.textContent = '命令'; setHidden($('cmdBox'), true); }
  updateFwBtn();
}

/* ------------------------------------------------- 入站未放行的提示 ---- */
/* 体检结论里 fw=0 表示本工具还没给这个端口加过入站放行规则。
   只提示、只置灰按钮，绝不自动改系统；用户点一下才加。
   加的那条规则范围仅限本程序与该端口（netsh 命令由 firewall 任务生成，界面不自己拼）。 */
function maybeFwHint(){
  var box = $('fwBanner');
  if(!box){ return; }
  if(!caps || caps.plat !== 'win' || caps.fw !== '0' || fwFixed){
    setHidden(box, true);
    return;
  }
  fwPort = caps.port || '25565';
  setText($('fwText'), '本机尚未放行 ' + fwPort + ' 与 ' + fwSigPort + ' 入站，玩家可能无法连接');
  setHidden(box, fwSkipped);
  updateFwBtn();
}
function updateFwBtn(){
  var b = $('fwFix');
  if(!b){ return; }
  b.disabled = running;
  b.title = running ? '需先停止当前任务' : ('仅添加本程序与 ' + fwPortSpec() + ' 的入站规则，不修改防火墙开关');
}
async function fixFirewall(){
  if(running){ toast('需先停止当前任务', true); return; }
  var r = await api('/api/start', { key:'firewall', inputs:{ port: fwPortSpec(), proto: 'both' } });
  if(!r.ok){ toast(r.err || '启动失败', true); return; }
  setRunningUI(true);
  tick();
}

/* ---------------------------------------------------------------- 状态 ---- */
function setStatus(s){
  var cls = 'dot', txt = '空闲';
  if(s.running){
    cls = 'dot run'; txt = '执行中：' + s.task + ' · ' + s.uptime;
  } else if(s.done){
    var bad = s.err || (s.code && s.code !== 0);
    cls = 'dot' + (bad ? ' bad' : '');
    // 状态栏只给结论与原因，不显示退出码：退出码的用途在日志里。
    txt = bad ? ('执行失败：' + (s.err || '原因见日志')) : '执行结束';
  } else if(quitting){
    txt = '正在退出';
  }
  setCls($('pulse'), cls);
  setText($('status'), txt);
  // 版本号不在这里写：它只在「设置 → 关于与状态」里出现一次。
  // 之前顶栏、状态栏、设置里各有一份，三处不一致时没人知道该信哪一个。
}
function setLink(entry, room, peer){
  setCls($('lnkEntry'), 'n' + (entry ? ' on' : ''));
  setCls($('lnkS2'), 's' + (entry ? ' on' : ''));
  setCls($('lnkRoom'), 'n' + (room ? ' on' : ''));
  setCls($('lnkS3'), 's' + (room ? ' on' : ''));
  setCls($('lnkPeer'), 'n' + (peer ? ' on' : ''));
}

/* ---------------------------------------------------------------- 日志 ---- */
/* 结论层：结论 / 说明 / 建议，按 findings 的 ✓ ! ✗ 上色。
   原始层：带 ##DBG## 标记的明细，默认折叠。 */
function classify(t){
  var m = /^\s*([✓!✗])\s/.exec(t);
  if(m){ return m[1] === '✓' ? 'ok' : (m[1] === '!' ? 'warn' : 'bad'); }
  if(/^\.\.\s/.test(t)){ return 'dim'; }
  // 纯分隔线（==== / ---- / ~~~~）只是段落标记，单列一档压暗，不与结论抢同一级亮度
  if(/^[-=~─—]{4,}$/.test(t.trim())){ return 'sep'; }
  if(/^结论|^建议|^说明|^处理|^原因/.test(t)){ return 'info'; }
  if(/错误|失败|不可用|拒绝|超时|异常/.test(t)){ return 'bad'; }
  if(/成功|已建立|已就绪|已连接|完成/.test(t)){ return 'ok'; }
  return '';
}
function rowHTML(text, no, raw){
  var c = raw ? 'dim' : classify(text);
  return '<div class="ln' + (c ? ' ' + c : '') + '">'
       +   '<span class="no">' + no + '</span>'
       +   '<span class="tx">' + esc(text) + '</span>'
       + '</div>';
}
function emptyHTML(which){
  return which === 'user'
    ? '<div class="lgempty">暂无输出。<br>执行操作后，结果显示在这里。</div>'
    : '<div class="lgempty">暂无输出。<br>STUN 应答、端口探测等原始信息收纳在这里。</div>';
}
function appendTo(which, text){
  var box = $(which === 'user' ? 'logUser' : 'logRaw');
  var arr = which === 'user' ? userLines : rawLines;
  var emp = box.querySelector('.lgempty');
  if(emp){ box.removeChild(emp); }
  var d = document.createElement('div');
  d.innerHTML = rowHTML(text, arr.length, which === 'raw');
  box.appendChild(d.firstChild);
  if(arr.length > LOG_KEEP){
    var cut = arr.length - LOG_KEEP;
    arr.splice(0, cut);
    for(var i = 0; i < cut; i++){ if(box.firstChild){ box.removeChild(box.firstChild); } }
  }
  box.scrollTop = box.scrollHeight;
}
function pushUser(t){ userLines.push(t); appendTo('user', t); }
function pushRaw(t){ rawLines.push(t); appendTo('raw', t); }
function refreshLog(){
  setHidden($('logUser'), layer !== 'user');
  setHidden($('logRaw'), layer !== 'raw');
  Array.prototype.forEach.call($('segs').querySelectorAll('.seg'), function(s){
    var cur = s.getAttribute('data-f') === layer;
    s.classList.toggle('on', cur);
    s.setAttribute('aria-pressed', cur ? 'true' : 'false');   // 同理：高亮读屏读不到
    if(s.getAttribute('data-f') === 'raw'){ setText(s, '原始输出 ' + rawLines.length); }
  });
  setText($('linecnt'), '结论 ' + userLines.length + ' · 原始 ' + rawLines.length);
}
function resetLog(){
  userLines = []; rawLines = [];
  $('logUser').innerHTML = emptyHTML('user');
  $('logRaw').innerHTML = emptyHTML('raw');
  refreshLog();
}
function appendLines(list){
  list.forEach(function(t){
    var m = /^##([A-Z0-9]+)## ?(.*)$/.exec(t);
    if(m){
      var kind = m[1], val = m[2];
      if(kind === 'DBG'){ pushRaw(val); return; }
      if(kind === 'CAP'){ setCaps(val); qHintFromCaps(val); return; }
      noteMarker(kind, val);
      return;
    }
    pushUser(t);
  });
  refreshLog();
}
/* 机器标记随日志一起下来，收到就顺手更新界面，不用每次轮询去整段日志里搜 */
function noteMarker(kind, val){
  if(kind === 'ADDR'){ setAddr(val); }
  else if(kind === 'ROOM'){
    roomCode = val;
    var c = $('roomChip');
    c.textContent = '房间码 ' + val;
    setHidden(c, false);
  } else if(kind === 'JOIN'){ setJoin(val); }
  else if(kind === 'GUESTS'){ setGuests(val); }
  else if(kind === 'GATE'){ gateByRoom = (val === 'room'); refreshInvite(); }
}
function setAddr(a){
  if(!a || a === addr){ return; }
  addr = a;
  $('addr').textContent = a;
  setHidden($('cardAddr'), false);
  var s = $('sheen');
  s.style.transition = 'none';
  s.style.transform = 'translateX(-120%)';
  void s.offsetWidth;
  s.style.transition = 'transform .85s cubic-bezier(.4,0,.2,1)';
  s.style.transform = 'translateX(120%)';
  refreshInvite(); // 地址一变，那段"发给朋友的话"跟着重拼
}
function setJoin(j){
  // 这条命令不再单独占一块地方 —— 它就写在下面那段邀请文本的"方式二"里。
  // 单独摆一份 = 界面上同一个东西出现两次、配两个复制按钮，反而不知道该复制哪个。
  if(!j || j === joinCmd){ return; }
  joinCmd = j;
  refreshInvite();
}
/* -------------------------------------------------------- 在场玩家 ---- */
/* 房主侧的名单。时长在这里按当前时间算，而不是由后端每秒重发一遍 ——
   名单没变就不该让界面重绘。 */
var guests = [];
var guestsKey = '';   // 上次画出来的内容指纹（名单 + 分钟数），没变就不重画
function setGuests(v){
  var d;
  try { d = JSON.parse(v); } catch(e){ return; }
  guests = (d && d.who) ? d.who : [];
  guestsKey = '';
  renderGuests();
}
function renderGuests(){
  var wrap = $('guestWrap'), ul = $('guestList'), n = $('guestN');
  if(!wrap || !ul || !n){ return; }
  var key = guests.map(function(g){
    return (g.c || '') + '@' + Math.floor(Math.max(0, Date.now()/1000 - (g.s || 0)) / 60);
  }).join('|');
  if(key === guestsKey){ return; }   // 同一分钟里名单没变：不碰 DOM
  guestsKey = key;
  n.textContent = guests.length;
  setHidden(wrap, guests.length === 0);
  ul.innerHTML = guests.map(function(g){
    var secs = Math.max(0, Math.floor(Date.now()/1000 - (g.s || 0)));
    var m = Math.floor(secs / 60);
    var dur = secs < 60 ? '刚加入'
            : (m < 60 ? ('已连 ' + m + ' 分')
                      : ('已连 ' + Math.floor(m/60) + ' 小时' + (m%60 ? ' ' + (m%60) + ' 分' : '')));
    return '<li><span class="gc">' + esc(g.c || '') + '</span>' +
           '<span class="gm">' + esc(g.m || '直连') + ' · ' + dur + '</span></li>';
  }).join('');
}

function clearHero(){
  addr = ''; joinCmd = ''; roomCode = '';
  guests = []; guestsKey = '';
  renderGuests();
  setHidden($('cardAddr'), true);
  setHidden($('roomChip'), true);
  refreshInvite();
}

/* ---------------------------------------------------------------- 启停 ---- */
async function startTask(){
  if(!sel || running){ return; }
  var inputs = {};
  (sel.fields || []).forEach(function(f){
    var e = $('f_' + f.id);
    if(!e){ return; }
    inputs[f.id] = (f.kind === 'check') ? (e.checked ? '1' : '') : e.value.trim();
  });
  var b = $('btnRun');
  if(b){ b.disabled = true; }
  var r = await api('/api/start', { key: sel.key, inputs: inputs });
  if(!r.ok && /已经有一个任务在跑/.test(r.err || '')){
    // 打开界面时会自动跑一次环境检测（约十几秒）。这期间使用者点「运行」是明确意图，
    // 应当打断那次检测再来 —— 而不是把他挡回去、让他自己去找「停止」。
    await api('/api/stop');
    await waitIdle(15000);
    r = await api('/api/start', { key: sel.key, inputs: inputs });
  }
  if(!r.ok){
    if(b){ b.disabled = false; }
    toast(r.err || '启动失败', true);
    return;
  }
  setRunningUI(true);
  tick();
}
async function stopJob(){
  await api('/api/stop');
  tick();
}
function quitApp(){
  quitting = true;
  setCls($('pulse'), 'dot run');
  setText($('status'), '正在退出');
  var s = $('btnStop'); if(s){ s.disabled = true; }
  var q = $('btnQuit'); if(q){ q.disabled = true; }
  api('/api/quit');
}

/* ------------------------------------------------------ 一步开局 + 邀请 ---- */
/* 简化入口：房主只填房间码、玩家只粘贴邀请。两者的完整参数（端口、白名单、中继地址…）
   仍然在「更多功能」里逐项可调 —— 这里只是把最常走的那条路压到一步。 */
var quickRole = '';

// 等到当前任务真的停下（/api/stop 返回后进程还要几秒才退出，光等固定时间不够）。
async function waitIdle(ms){
  var t0 = Date.now();
  while(Date.now() - t0 < (ms || 15000)){
    try{
      var res = await fetch('/api/state?since=999999999');
      var s = await res.json();
      if(!s.running){ return true; }
    }catch(e){ /* 轮询失败就再试一次 */ }
    await new Promise(function(r){ setTimeout(r, 350); });
  }
  return false;
}

// startWith 用给定参数直接启动某个操作，跳过"先选中、再点运行"这两步。
async function startWith(key, inputs){
  var t = taskByKey(key);
  if(!t.key){ toast('未知操作', true); return false; }
  sel = t;
  renderWork();
  var r = await api('/api/start', { key: key, inputs: inputs || {} });
  if(!r.ok && /已经有一个任务在跑/.test(r.err || '')){
    // 同上：使用者主动点的那一下优先于后台的自动环境检测
    await api('/api/stop');
    await waitIdle(15000);
    r = await api('/api/start', { key: key, inputs: inputs || {} });
  }
  if(!r.ok){ toast(r.err || '启动失败', true); return false; }
  setRunningUI(true);
  tick();
  return true;
}

// 从房主发来的那段话里读地址与房间码。三种形式都要吃得下：
// 命令行（--host/--room）、纯地址、以及本工具自己生成的那段邀请文本。
function parseInvite(text){
  var t = (text || '').replace(/\r/g, '');
  var out = { host: '', room: '' };
  if(!t.trim()){ return out; }
  var m = /--host[= ]+("[^"]+"|'[^']+'|\S+)/.exec(t);
  if(m){ out.host = m[1].replace(/^["']|["']$/g, ''); }
  m = /--room[= ]+("[^"]+"|'[^']+'|\S+)/.exec(t);
  if(m){ out.room = m[1].replace(/^["']|["']$/g, ''); }
  if(!out.room){
    m = /房间码[:：\s]*([A-Za-z0-9_-]{2,32})/.exec(t);
    if(m){ out.room = m[1]; }
  }
  if(!out.host){
    m = /\[[0-9A-Fa-f:]+\](?::\d{1,5})?/.exec(t); // 带方括号的 IPv6
    if(m){ out.host = m[0]; }
  }
  if(!out.host){
    m = /((?:[0-9A-Za-z\u4e00-\u9fa5-]+\.)+[A-Za-z]{2,}|(?:\d{1,3}\.){3}\d{1,3})(?::\d{1,5})?/.exec(t);
    if(m){ out.host = m[0]; }
  }
  return out;
}

// 邀请文本：房主唯一需要转发的文本。整段复制即可，玩家按其中步骤操作。
// 内容按"先公网、后本工具"的顺序排列 —— 这是两条入口的推荐顺序。
function buildInvite(){
  if(!addr){ return ''; }
  var s = '【mclbx 房间邀请】\n';
  // 房间码那一行按「口令是什么」来写。raw / 映射 / 公网 IPv4 直连这三种形态下地址本身就是口令，
  // 房间码只对「方式二」有意义 —— 写反了会让好人以为要在游戏里填码，也让这段文本看起来
  // 比实际更像"钥匙"（见坏-1：这类文本被转到大群里的代价）。
  if(gateByRoom){
    s += '房间码：' + (roomCode || '见地址') + '（「方式一」的口令：名称里不含它会被拒绝）\n\n';
  } else {
    s += '房间码：' + (roomCode || '见地址') + '（只有「方式二」需要；「方式一」直接按地址进）\n\n';
  }
  s += '方式一（玩家用地址直接连接）：\n';
  s += '  在游戏内「多人游戏 → 直接连接」中输入：\n';
  s += '  ' + addr + '\n';
  if(joinCmd){
    s += '\n方式二（已安装 mclbx 时）：\n';
    s += '  执行以下命令：\n';
    s += '  ' + joinCmd + '\n';
  }
  s += '\n这段邀请等同于钥匙，请只发给要一起玩的人。\n';
  return s;
}
function refreshInvite(){
  var w = $('inviteWrap');
  if(!w){ return; }
  var t = buildInvite();
  if(!t){ setHidden(w, true); return; }
  setText($('inviteText'), t);
  setHidden(w, false);
}

// 环境结论一句话：告诉使用者"接下来会发生什么"，而不是让他去读四个指标。
function qHintFromCaps(raw){
  var h = $('qHint');
  if(!h){ return; }
  var get = function(k){
    var m = new RegExp('(^|\\s)' + k + '=([^\\s]*)').exec(raw || '');
    return m ? m[2] : '';
  };
  var v6 = get('v6'), nat = get('nat');
  var mapped = (get('upnp') === '1' || get('natpmp') === '1' || get('pcp') === '1');
  var text;
  if(v6 === '1'){ text = '本机具备公网 IPv6，玩家可直接连接'; }
  else if(mapped){ text = '路由器支持自动端口映射，玩家可直接连接'; }
  else if(nat === 'symmetric'){ text = 'NAT 为对称型，直连成功率低；配置中继服务器后可自动改用中继'; }
  else { text = '玩家一侧可能需要安装本工具；创建房间后将邀请发送给对方即可'; }
  setText(h, text);
}

// 在"开房 / 进房"之间切换那小半屏表单；传空串收起。
function showQuickForm(role){
  quickRole = role || '';
  setHidden($('qHostForm'), quickRole !== 'host');
  setHidden($('qGuestForm'), quickRole !== 'guest');
  $('qHost').classList.toggle('on', quickRole === 'host');
  $('qGuest').classList.toggle('on', quickRole === 'guest');
  if(quickRole === 'host'){ $('qRoom').focus(); }
  if(quickRole === 'guest'){ $('qInvite').focus(); }
}
/* ------------------------------------------------------------ 状态轮询 ---- */
async function tick(){
  if(polling || quitting){ return; }
  polling = true;
  var ctl = (typeof AbortController !== 'undefined') ? new AbortController() : null;
  var timer = ctl ? setTimeout(function(){ ctl.abort(); }, 4000) : null;
  try{
    var res = await fetch('/api/state?since=' + since, ctl ? { signal: ctl.signal } : undefined);
    var s = await res.json();
    if(s.jobId !== jobId){
      jobId = s.jobId; since = 0; clearHero(); resetLog();
      setLink(false, false, false);
    }
    if(s.lines && s.lines.length){ appendLines(s.lines); }
    // 名单里的"已连多久"是本机算的：不重新取名单，只让分钟数跟上
    if(guests.length){ renderGuests(); }
    if(typeof s.seq === 'number'){ since = s.seq; }
    var was = running;
    running = !!s.running;
    // 【视觉美化，非核心功能】任务一起一停，毛玻璃就摘掉/装回（见 CSS 里那一段）。
    // 只在这一处推进去，界面不需要知道美化层内部的任何细节。
    VFX.busy(running);
    if(was !== running){ setRunningUI(running); }
    setCapBusy(!!s.running && !caps && s.taskKey === 'probe');
    if(running){
      var entry = false, room = false, peer = false;
      if(sel && sel.key === 'room'){ entry = true; room = true; }
      else if(sel && (sel.key === 'expose-raw' || sel.key === 'expose-dns' || sel.key === 'ice-host')){ entry = true; }
      else if(sel && (sel.key === 'join' || sel.key === 'ice-guest')){ entry = true; peer = true; }
      if(addr){ entry = true; room = true; }
      setLink(entry, room, false);
    } else if(s.done){
      var ok = !(s.err || (s.code && s.code !== 0));
      // 刚跑完"放行入站"：把提示条收掉。规则是否真的写进去了以任务退出码为准，
      // 这里只负责别再挂着这条提示。
      if(s.taskKey === 'firewall' && ok){
        fwFixed = true;
        setHidden($('fwBanner'), true);
        toast('入站规则已添加');
      }
      if(s.taskKey === 'room' || s.taskKey === 'expose-raw' || s.taskKey === 'expose-dns' || s.taskKey === 'ice-host'){
        setLink(!!addr, !!addr, ok && !!addr);
      } else if(s.taskKey === 'join' || s.taskKey === 'ice-guest'){
        setLink(true, false, ok);
      } else {
        setLink(true, false, false);
      }
    }
    setStatus(s);
  }catch(e){
    setCls($('pulse'), 'dot bad');
    setText($('status'), '与控制台失去连接');
  }finally{
    if(timer){ clearTimeout(timer); }
    polling = false;
  }
}

/* ---------------------------------------------------------------- 绑定 ---- */
$('q').addEventListener('input', filterTasks);
$('btnProbe').onclick = startProbe;
$('fwFix').onclick = fixFirewall;
$('qHost').onclick = function(){ showQuickForm('host'); };
$('qGuest').onclick = function(){ showQuickForm('guest'); };
$('qHostCancel').onclick = function(){ showQuickForm(''); };
$('qGuestCancel').onclick = function(){ showQuickForm(''); };
$('btnCopyInvite').onclick = function(){ copy($('inviteText').textContent || ''); };
$('qHostGo').onclick = async function(){
  await startWith('room', {
    room: ($('qRoom').value || '').trim(),
    port: ($('qPort').value || '').trim()
  });
};
$('qGuestGo').onclick = async function(){
  var raw = ($('qInvite').value || '').trim();
  if(!raw){ toast('请先粘贴房主提供的邀请', true); return; }
  var p = parseInvite(raw);
  if(!p.host && !p.room){
    toast('无法识别邀请内容，可在左侧操作列表里选「加入房间」手动填写地址', true);
    return;
  }
  var ok = await startWith('join', { host: p.host, room: p.room });
  if(ok){
    setText($('qGuestNote'), '正在连接 ' + (p.host || '') + (p.room ? ('（房间码 ' + p.room + '）') : ''));
  }
};
/* ==================== 页面路由 ====================
   三个平级页面：工作台 / 设置 / 说明书。导航在顶栏，地址栏的 hash 跟着走。
   hash 不是"为了做成单页应用"—— 它只解决两件具体的事：刷新后还停在原页，
   以及浏览器后退能退回上一步。以前这里散着四个查询参数（?settings=1 / ?manual=1 /
   ?quick=host / ?more=1），每个都只服务于当时的一次截图核对；现在它们全部删掉，
   只留这一个**真正的**路由。 */
var PAGES = {work:'pageWork', settings:'pageSettings', manual:'pageManual'};
var page = 'work';
function go(next){
  if(!PAGES[next]){ next = 'work'; }
  if(next === page && next !== 'settings'){ return; }
  page = next;
  for(var k in PAGES){ setHidden($(PAGES[k]), k !== next); }
  Array.prototype.forEach.call($('nav').children, function(b){
    var cur = b.getAttribute('data-page') === next;
    b.classList.toggle('on', cur);
    // 给辅助技术一个明确说法：当前在哪一页。视觉上的高亮它读不到。
    if(cur){ b.setAttribute('aria-current', 'page'); } else { b.removeAttribute('aria-current'); }
  });
  if(next === 'settings'){ renderSettings(); }
  if(next === 'manual'){ loadManual(); }
  if(location.hash !== '#/' + next){ location.hash = '#/' + next; }
}
function loadManual(){
  var f = $('manualFrame');
  // 说明书是另一个文档（自带一套变量），主题得从查询串带过去：不带的话，
  // 深色主题下点开会得到一整页白 —— 暗环境里那一下很刺眼。
  // 只在第一次进入时设地址：不打开就不加载，空闲时不占资源。
  if(f && !f.getAttribute('src')){
    f.setAttribute('src', '/manual?theme=' + encodeURIComponent(resolvedTheme()));
  }
}
window.addEventListener('hashchange', function(){
  var p = (location.hash || '').replace(/^#\//, '');
  if(PAGES[p] && p !== page){ go(p); }
});
/* 当前**解析后**的主题：documentElement 上的那个值就是它 —— "跟随系统"在首屏脚本里
   已经按系统偏好写成了 light 或 dark。 */
function resolvedTheme(){
  return document.documentElement.getAttribute('data-theme') || 'dark';
}
/* 说明书开着的时候换了主题：那是个独立文档，属性要单独跟着改（同源，可以直接改） */
function syncManualTheme(){
  try{
    var f = $('manualFrame');
    if(!f || !f.getAttribute('src') || !f.contentDocument){ return; }
    f.contentDocument.documentElement.setAttribute('data-theme',
      resolvedTheme() === 'light' ? 'light' : 'dark');
  }catch(e){ /* 拿不到就算了：那说明它还没加载完，加载时用的已经是对的那一套 */ }
}
/* ---------------------------------------------------------------- 设置 ---- */
/* 设置是"这个程序怎么长、默认怎么表现"，与"这次要跑什么"是两件事：
   后者的入口是左边的操作列表，前者的入口是这里。

   两件事原来混在一起 —— 比如"打开界面时自动体检"藏在「检测本机环境」的字段里，
   而"默认中转服务器"干脆没有，每个带这个字段的操作各填一遍。现在它们都归到这里，
   同时保留原来的入口（原生界面还在用那个勾），读写的是同一个值。 */
var ui = null;

function uiEsc(s){
  return String(s == null ? '' : s).replace(/[&<>"']/g, function(c){
    return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];
  });
}
function uiSeg(key, cur, opts){
  return '<div class="mseg" data-seg="' + key + '">' + opts.map(function(o){
    return '<button data-v="' + o[0] + '"' + (o[0] === cur ? ' class="on"' : '') + '>' + o[1] + '</button>';
  }).join('') + '</div>';
}
function uiSw(key, on, label){
  return '<label class="msw"><input type="checkbox" data-sw="' + key + '"' + (on ? ' checked' : '') +
    '><span>' + label + '</span></label>';
}
function uiField(key, cls, val, ph, suffix){
  return '<input class="mtxt ' + cls + '" data-in="' + key + '" value="' + uiEsc(val) +
    '" placeholder="' + uiEsc(ph) + '">' + (suffix ? ' ' + suffix : '');
}
/* 档位滑杆：0-10 的整数。拖动时只更新右侧那个数字，**松手（change）才落盘** ——
   否则每挪一格就发一次保存请求。数字用等宽字体并占固定宽度，跳动时不会把版式撑动。 */
function uiRange(key, val, min, max){
  var v = (val === '' || val === null || val === undefined) ? String(min) : String(val);
  return '<div class="mrange"><input type="range" data-rng="' + key + '" min="' + min +
    '" max="' + max + '" step="1" value="' + uiEsc(v) + '" aria-label="' + key + '">' +
    '<span class="rv" data-rv="' + key + '">' + uiEsc(v) + '</span></div>';
}
/* 强调色圆点自带的预览色。看着和 CSS 重复，但**不能**改成 var(--sig)：
   点任意一个圆点会立刻保存并套用，data-accent 一变，所有圆点都会变成同一个颜色，
   预览就失去意义了。它必须是各自独立的色值（口径是"在深浅底上都看得出来的中间调"）。 */
function uiAccents(){
  return [['mint','信号绿','#22C58A'],['blue','蓝','#4A90E2'],['violet','紫','#9B77E8']];
}
/* 主题的取值必须与 guiconfig.go 的 normalizeUI 白名单、以及 CSS 里的
   :root[data-theme=…] 块一一对应；漏一处的症状是"选了但没变"或"永远选不到"。
   用例 TestThemeOptionsMatchTheCSS 会拿这份名单去比对 CSS。 */
function uiThemes(){
  return [['auto','跟随系统'],['dark','深色'],['light','浅色'],['contrast','高对比']];
}
/* 背景图与毛玻璃两行的说明文字。它们随另一个设置项变（填没填背景图），
   保存后要就地重刷，否则会挂着上一次的旧文案。 */
function wallHintText(u){
  if(u.wallReason){ return '这张图用不了：' + uiEsc(u.wallReason); }
  return '点「导入图片…」选一张图（JPEG / PNG / GIF，单张上限 32MB）；也可以把图放进存档的 wallpapers 目录再点「刷新」。建议用不透明的图：透明区域转成 JPEG 后会发黑。';
}
function glassHintText(u){
  if(u.vfx === 'off'){ return '总开关关着，这一档暂时不生效；重新打开「视觉美化」后回到这一档。'; }
  var n = parseInt(u.glass, 10);
  if(isNaN(n)){ n = 1; }
  if(n === 0){ return '0 = 关闭：面板实心、不做模糊，最省的一档。'; }
  var cost = n >= 8 ? '这一档开销明显，低配机器可能掉帧，建议先「帧率实测」。'
    : n >= 4 ? '中等档：任务执行期间自动让位，跑完立刻恢复。'
    : '偏保守的一档：观感接近实心，开销最小。';
  return '0 到 10：越大越透明、模糊越强。' + cost + (u.bgImage ? '已设背景图，面板会补一点不透明度。' : '');
}
/* 图库选择器。
   配置里存的是「导入后的文件名」，所以这里给下拉列表而不是让用户敲路径 —— 路径那种做法
   在用户把图挪个地方之后就会静默失效，而「导入」是把图复制进存档，之后怎么动原文件都不影响。
   列表来自服务端扫描 wallpapers 目录的结果，用户也可以自己往那个目录里丢图。 */
function uiWallPicker(u){
  var list = u.wallList || [];
  var opts = '<option value="">（不用背景图）</option>';
  list.forEach(function(n){
    opts += '<option value="' + uiEsc(n) + '"' + (n === u.bgImage ? ' selected' : '') + '>' + uiEsc(n) + '</option>';
  });
  if(u.bgImage && list.indexOf(u.bgImage) < 0){
    // 配置里选的那张已经不在图库里了：仍然列出来并选中，好让用户看见"是它丢了"
    opts += '<option value="' + uiEsc(u.bgImage) + '" selected>' + uiEsc(u.bgImage) + '（已不在图库里）</option>';
  }
  /* 「打开目录」不在这里 —— 整页只有一个「打开数据目录」，放在维护那一块。
     同一件事在三个地方各留一个按钮的时候，用户得先猜它们是不是同一个目录。 */
  return '<select class="mtxt" data-in="bgImage">' + opts + '</select>' +
    '<input type="file" id="wallFile" accept="image/*" style="display:none">' +
    '<button type="button" class="btn sm" data-act="wallImport">导入图片…</button>' +
    '<button type="button" class="btn sm" data-act="wallRefresh">刷新</button>';
}
/* 曲库那一行。曲库的真相在磁盘上，所以这里只报数量 + 一个「刷新」，
   不做内嵌的播放列表管理 —— 用户用资源管理器管理自己的文件，比在设置里做一套增删更省心。
   「打开目录」同样交给维护那一块，不在这里再放一个。 */
function uiMusicList(u){
  var list = u.musicList || [];
  var n = list.length;
  var txt = n ? ('共 ' + n + ' 首') : '（还没有音乐）';
  return '<span class="mi-out">' + txt + '</span>' +
    '<button type="button" class="btn sm" data-act="musicRefresh">刷新</button>';
}
function musicHintText(u){
  if(!(u.musicList || []).length){
    return '把音乐文件（MP3 / WAV / FLAC / M4A / OGG）放进存档目录的 music 文件夹，再点「刷新」。只把真正的音频算进曲库：改了后缀的其它文件不会出现在这里。';
  }
  return '曲库就是存档里的 music 文件夹，你可以自己往里放、改名、删除，点「刷新」后生效。播放时不另占 CPU —— 音频由界面内核直接解码；关掉窗口就停。某个文件放不了会被标出来，不会静默跳过。';
}
/* ==================== 设置页 ====================
   三个一级分类：主题外观 / 辅助工具 / 关于与状态；维护动作集中在最后一块。
   每个可编辑项在服务端都有对应字段与归一化逻辑，没有占位开关。
   改完即时落盘，最近一次写盘时间显示在状态栏（见 uiSaveState）；非法取值由服务端回退并在 uiSave 里回报。 */

/* 板块与卡片的小图标。内联 SVG，只跟着 currentColor 走 ——
   不引图标字体也不引外部文件：面板的观感不该给程序添依赖。 */
function uiIcon(name){
  var a = 'viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" ' +
    'stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"';
  if(name === 'theme'){
    return '<svg ' + a + '><circle cx="8" cy="8" r="5.4"/><path d="M8 2.6v10.8"/>' +
      '<path d="M2.6 8h10.8"/></svg>';
  }
  if(name === 'aux'){
    return '<svg ' + a + '><path d="M3 13l6.6-6.6"/><circle cx="11.2" cy="4.8" r="2.5"/>' +
      '<path d="M3.4 8.4l4.2 4.2"/></svg>';
  }
  if(name === 'wrench'){
    return '<svg ' + a + '><path d="M12.4 2.6 9.8 5.2l1 1 2.6-2.6a3.2 3.2 0 0 1-4 4l-4 4a1.6 1.6 0 1 1-2.2-2.2l4-4a3.2 3.2 0 0 1 4-4z"/></svg>';
  }
  return '<svg ' + a + '><circle cx="8" cy="8" r="5.6"/><path d="M8 7.3v3.9"/>' +
    '<path d="M8 5v.1"/></svg>';
}
/* 强调色圆点自带的预览色。看着和 CSS 重复，但**不能**改成 var(--sig)：
   点任意一个圆点会立刻保存并套用，data-accent 一变所有圆点都会变成同一个颜色，
   预览就失去意义了。它必须是各自独立的色值。 */
function uiAccentDots(u){
  return uiAccents().map(function(a){
    return '<button type="button" class="mdot' + (u.accent === a[0] ? ' on' : '') +
      '" data-accent="' + a[0] + '" title="' + a[1] + '" aria-label="强调色 ' + a[1] +
      '" style="background:' + a[2] + '"></button>';
  }).join('');
}
/* 一个设置项。结构固定为四件：
     标题 + 状态标签 / 说明 / 控件 / 悬停可见的更细提示。
   tip 走 title 而不另铺一行字：面板已经有一行说明了，第二行会把一屏能看到的项数砍掉三分之一。
   key 是服务端字段名：标签由 uiTag() 按它算，而 data-k 让"改完一项"能就地刷新标签，
   不必重渲染整页（重渲染会把焦点和光标位置一起弄丢）。 */
function uiItem(key, title, control, desc, tip){
  var tag = uiTag(ui, key);
  return '<div class="mitem" data-k="' + key + '"' + (tip ? ' title="' + uiEsc(tip) + '"' : '') + '>' +
    '<div><div class="mi-h"><span class="mi-t">' + title + '</span>' +
    '<span class="mi-tag' + (tag ? ' chg' : '') + '"' + (tag ? '' : ' hidden') + '>已改动</span>' +
    '</div><div class="mi-d">' + desc + '</div></div>' +
    '<div class="mi-c">' + control + '</div></div>';
}
/* 保存之后就地刷「已改动」标签：不重建节点，所以输入框的焦点与光标都留着。 */
function uiRefreshTags(){
  var body = $('settingsBody');
  if(!body || !ui){ return; }
  Array.prototype.forEach.call(body.querySelectorAll('.mitem[data-k]'), function(el){
    var tag = uiTag(ui, el.getAttribute('data-k'));
    var span = el.querySelector('.mi-tag');
    if(!span){ return; }
    span.hidden = !tag;
    span.className = 'mi-tag' + (tag ? ' chg' : '');
  });
}
/* 一级分类。整页只有这一种分类容器，圆角与标题样式统一 ——
   以前是"板块 + 卡片"两层，每层各写一套标题样式，读代码时看不出它们是一回事。 */
function uiCat(icon, title, sub, body){
  return '<section class="mcat"><div class="mcathead">' +
    '<span class="ic">' + uiIcon(icon) + '</span>' +
    '<span class="tt">' + title + '</span><span class="sub">' + sub + '</span></div>' +
    body + '</section>';
}
/* 分类内部的小标题：只用于把只读信息与变更记录分开，不再承载分组语义。 */
function uiSub(title, extra){
  return '<div class="msub"><span class="tt">' + title + '</span>' +
    (extra ? '<span class="mi-d">' + extra + '</span>' : '') + '<span class="sp"></span></div>';
}
/* 「已改动」标签的判据。默认值这份表与 uiCollect() 里那份是同一个口径 ——
   两边不一致的症状是"某一项明明是默认值却挂着已改动"，而那种错没人会去查。
   remember 存的是"关掉"的反面，logKeep 服务端给的是生效值，都要先还原成同一个口径再比。 */
var UI_DEF = {
  theme:'auto', accent:'mint', flat:'off', radii:'std', backdrop:'on', bgImage:'',
  glass:'1', fade:'on', scale:'std', motion:'full',
  rail:'std', musicMode:'order', musicLoop:'all', musicVol:'70', remember:'',
  logKeep:'2000', defPort:'', defRelay:''
};
function uiTag(u, key){
  // 纯动作行（帧率实测、曲库、播放控制、清除…）没有对应的设置字段，key 传空串。
  // 不加这一句的话它们会全部挂上「已改动」—— 因为空键在两张表里都取不到值，比出来就是"不同"。
  if(!key){ return null; }
  var v = u ? u[key] : '';
  if(key === 'remember'){ v = (u.remember === '0') ? '0' : ''; }
  if(key === 'logKeep'){ v = String((u && u.logKeep) || '2000'); }
  if(v === undefined || v === null){ v = ''; }
  return (String(v) === UI_DEF[key]) ? null : {text:'已改动', chg:true};
}
/* 只读信息：值全部来自服务端，界面上只有文本，没有任何可编辑控件。 */
function uiInfoRow(k, v, mono){
  return '<dt>' + k + '</dt><dd' + (mono ? ' class="mono"' : '') + '>' + (v || '—') + '</dd>';
}
function uiBytes(n){
  n = Number(n) || 0;
  if(n < 1024){ return n + ' B'; }
  if(n < 1024 * 1024){ return (n / 1024).toFixed(1) + ' KB'; }
  return (n / 1024 / 1024).toFixed(2) + ' MB';
}
function uiStat(k, bytes, files){
  return '<div class="mstat"><div class="k">' + k + '</div><div class="v">' + uiBytes(bytes) +
    '</div><div class="n">' + (files || 0) + ' 个文件</div></div>';
}

/* ---- 分类一：主题外观 ---- */
function uiThemeHTML(u){
  return uiItem('theme', '主题', uiSeg('theme', u.theme, uiThemes()),
      '浅色与高对比只换底色与字色，版式与按钮位置都不动。') +
    uiItem('accent', '强调色', uiAccentDots(u), '链路状态用的就是这个颜色。',
      '警告与错误固定为琥珀与红，不随强调色变。') +
    uiItem('flat', '材质', uiSeg('flat', u.flat, [['off','立体'],['on','扁平']]),
      '扁平去掉圆角、外阴影、渐变与光晕，只留颜色与 1px 线条。',
      '焦点框会换成描边，键盘操作一样看得见。') +
    uiItem('radii', '圆角大小', uiSeg('radii', u.radii, [['sharp','利落'],['std','标准'],['round','圆润']]),
      '统一调整面板、卡片与按钮的圆角。',
      '选「材质：扁平」时圆角一律归零，两者冲突以扁平为准。') +
    uiItem('backdrop', '背景光效', uiSw('backdrop', u.backdrop !== 'off', '显示极光与网格'),
      '两层装饰渐变，关掉它不影响背景图片。') +
    uiItem('bgImage', '背景图片', uiWallPicker(u), wallHintText(u),
      '图片按比例铺满窗口，上面压一层与主题同色的暗化层。') +
    /* 视觉特效自成一个分组：这一组管的是"面板长什么样"，
       与上面那几项（配色、背景）和下面那几项（字号、动效、侧栏）不是一类。 */
    uiSub('视觉特效', '面板的半透明与模糊；这一层恒开，没有总开关') +
    uiItem('glass', '毛玻璃', uiRange('glass', u.glass, 0, 10),
      glassHintText(u),
      '模糊按层次分配：主容器一档、浮层更强，面板内部的卡片只做半透明。' +
      '设了背景图时两者会一起用 —— 程序把照片压暗一些，并给面板补一点不透明度，' +
      '保证照片上的字仍达 4.5 的对比度。高对比主题不参与毛玻璃。') +
    uiItem('fade', '面板淡入', uiSw('fade', u.fade !== 'off', '浮层出现时淡入'),
      '只动透明度、不动位置。',
      '选「动画效果：精简」或系统开了「减少动态效果」时自动不生效。') +
    uiItem('', '帧率实测',
      '<button type="button" class="btn sm" data-act="vfxMeasure">在当前档位测一秒</button>' +
      '<span class="mi-out" id="vfxStats">尚未测过</span>',
      '哪个档位合适由机器说了算：换一档点一次。它不落盘、不改设置。') +
    uiItem('scale', '界面字号', uiSeg('scale', u.scale, [['std','标准'],['big','大']]),
      '只把字放大一号，控件位置与版式都不动。') +
    uiItem('motion', '动画效果', uiSeg('motion', u.motion, [['full','完整'],['lite','精简']]),
      '精简去掉所有过渡与运行点的呼吸动画（点本身还在）。',
      '系统设置里的「减少动态效果」始终优先。') +
    uiItem('rail', '侧边栏样式', uiSeg('rail', u.rail, [['std','标准'],['compact','紧凑']]),
      '紧凑去掉操作行里的说明行、收紧行距，一屏能多看几项。',
      '只影响侧栏排版，操作的名称、顺序与行为都不变。');
}

/* ---- 分类二：辅助工具 ---- */
function uiAuxHTML(u){
  return '<div class="mbenefit">' + uiIcon('aux') +
    '<span>本板块是附加能力，不影响软件主体的基础功能。关掉其中任何一项，联机本身照常工作。</span></div>' +
    uiItem('', '曲库', uiMusicList(u), musicHintText(u),
      '曲库就是存档里的 music 文件夹：自己往里放、改名、删除，点「刷新」后生效。') +
    uiItem('musicMode', '播放顺序', uiSeg('musicMode', u.musicMode, [['order','顺序'],['shuffle','随机']]),
      '随机播放不会连续两首抽到同一首。',
      '曲库里只有一首时，随机没有意义，此时与顺序播放等价。') +
    uiItem('musicLoop', '循环', uiSeg('musicLoop', u.musicLoop, [['off','不循环'],['all','列表循环'],['one','单曲循环']]),
      '选「不循环」时，顺序播到最后一首就停下。',
      '随机模式下会一直播下去，不受「不循环」影响。') +
    uiItem('musicVol', '音量', uiField('musicVol', 'small', u.musicVol, '70', ''),
      '0 到 100，与系统音量是叠乘关系。',
      '听不见时先看系统音量，再看这里是不是被调成了 0。') +
    uiItem('', '播放控制',
      '<button type="button" class="btn sm" data-act="bgmPrev">上一首</button>' +
      '<button type="button" class="btn sm" data-act="bgmPlay">播放 / 暂停</button>' +
      '<button type="button" class="btn sm" data-act="bgmNext">下一首</button>',
      '状态栏那个音符按钮也能播放与暂停，不用每次打开设置。',
      '它默认不自动播放 —— 这是个会长时间开着的工具，不该在没人点的时候出声。') +
    uiItem('remember', '记住上次填过的值', uiSw('remember', u.remember !== '0', '记住上次填过的值'),
      '按「任务 + 字段」记住填写内容，下次自动带出。',
      '关掉只是不再记新的；已经记下的用下面「清除」处理。') +
    uiItem('', '清除记住的填写内容',
      '<button type="button" class="btn sm" data-act="forgetInputs">清除</button>',
      '把已记住的填写值一次清空，界面设置不受影响。',
      '它只清填写过的表单值，不动面板上的任何设置。') +
    uiItem('logKeep', '日志保留行数', uiField('logKeep', 'small', u.logKeep, '2000', '行'),
      '两层（结论 / 原始输出）各留多少行，范围 200 ~ 20000。',
      '超出范围的取值会被回退到默认，并当场告诉你。') +
    uiItem('defPort', '默认游戏端口', uiField('defPort', 'small', u.defPort, '25565', ''),
      '填了它，所有「游戏端口」留空的表单都用这个值。',
      '单个任务里自己填过的以它为准，不受这里影响。') +
    uiItem('defRelay', '默认中转服务器', uiField('defRelay', '', u.defRelay, 'turn:主机:3478 或 mclbx://…', ''),
      '填了它，所有「中转服务器」留空的表单都用这个值。',
      '它只是省去重复填写，不会自动启用中继 —— 用不用中继仍由每个任务自己决定。');
}

/* ---- 分类三：关于与状态（整块只读 + 变更记录） ---- */
function uiAboutHTML(u){
  var st = u.storage || {}, it = st.items || {}, cf = u.configFile || {};
  var music = (u.musicList || []).length;
  var h = '<dl class="mabout">';
  h += uiInfoRow('程序版本', uiEsc(u.version || ''), true);
  h += uiInfoRow('运行环境', uiEsc(u.platform || ''), false);
  h += uiInfoRow('数据目录', uiEsc(u.dataDir || ''), true);
  h += uiInfoRow('配置文件', uiEsc(cf.path || '') + (cf.exists ? '' : '（还没生成）'), true);
  if(cf.exists){
    h += uiInfoRow('配置写入时间', uiEsc(cf.modified || '') + '　·　' + uiBytes(cf.bytes), true);
  }
  h += uiInfoRow('背景音乐', music ? (music + ' 首') : '还没有', false);
  h += uiInfoRow('存档合计', uiBytes(st.totalBytes) + '　·　' + (st.totalFiles || 0) + ' 个文件', true);
  h += uiInfoRow('隐私', '不联网、不上报、不采集使用数据；全部内容都在上面这个目录里', false);
  h += '</dl>';
  if(u.configFault){
    h += '<div class="mnotice"><span class="ic">' + uiIcon('info') + '</span><span>' +
      '上次读取配置文件时出错，已丢弃损坏内容并回到默认设置；原文件没有被改写，' +
      '需要的话可以从数据目录里取出来看。<br>原因：' + uiEsc(u.configFault) + '</span></div>';
  }
  h += uiSub('占用', '存档目录里各部分的实际体积与文件数');
  h += '<div class="mstats">' +
    uiStat('背景图缓存', it.cache && it.cache.bytes, it.cache && it.cache.files) +
    uiStat('壁纸原图', it.wall && it.wall.bytes, it.wall && it.wall.files) +
    uiStat('音乐', it.music && it.music.bytes, it.music && it.music.files) +
    uiStat('配置与日志',
      ((it.config && it.config.bytes) || 0) + ((it.changes && it.changes.bytes) || 0),
      ((it.config && it.config.files) || 0) + ((it.changes && it.changes.files) || 0)) +
    '</div>';
  h += uiChangesHTML(u);
  return h;
}
/* 变更记录。它以前是设置面板里的"第二页"（点「变更记录」整块换掉）——
   那是同一件事的两半：这份配置被谁改过，属于状态，不属于另一个设置页。
   现在它就是「关于与状态」里的一段，少一个视图状态、少两个按钮。 */
function uiChangesHTML(u){
  var list = (u && u.changes) || [];
  var h = uiSub('设置变更记录', '共 ' + list.length + ' 条，上限 ' + (u.changeMax || 500) + ' 条' +
    (list.length ? '　·　记录只存取值本身，不含与网络环境或个人身份有关的内容' : ''));
  if(!list.length){
    return h + '<div class="mchgempty">还没有改动记录。改过设置之后，这里会逐条记下时间、分类、' +
      '选项名与前后取值。</div>';
  }
  h += '<div class="mchg">';
  for(var i = list.length - 1; i >= 0; i--){   // 倒序：最近改的排在最上面
    var c = list[i];
    h += '<div class="mchgr"><span class="t">' + uiEsc(c.at) + '</span>' +
      '<span class="m">' + uiEsc(c.module) + '</span>' +
      '<span class="i">' + uiEsc(c.item) + '</span>' +
      '<span class="v">' + uiEsc(c.from) + ' → ' + uiEsc(c.to) + '</span></div>';
  }
  h += '</div>';
  return h;
}
/* ---- 维护：所有"动作"集中在这一块 ----
   恢复默认 / 导出 / 导入 / 打开目录 / 复制版本 / 诊断包。
   它们不是设置项，所以不在上面三个分类里；也不散落在各分类标题旁边 ——
   这一条是"重要操作集中放置"的可执行版本。
   恢复默认仍然保留两个范围，但入口只有一个：点开它之后才选"仅某一类"还是"全部"。 */
function uiMaintHTML(){
  return '<section class="mmaint">' +
    '<div class="mcathead"><span class="ic">' + uiIcon('wrench') + '</span>' +
    '<span class="tt">维护</span><span class="sub">以下是动作而不是设置：作用于整份配置或整个存档目录。</span></div>' +
    '<div class="acts">' +
    '<button type="button" class="btn sm" data-act="resetAsk">恢复默认…</button>' +
    '<button type="button" class="btn sm" data-act="exportCfg">导出全部配置</button>' +
    '<button type="button" class="btn sm" data-act="importCfg">导入配置…</button>' +
    '<button type="button" class="btn sm" data-act="changesClear">清空变更记录</button>' +
    '<button type="button" class="btn sm" data-act="openData">打开数据目录</button>' +
    '<button type="button" class="btn sm" data-act="copyVersion">复制版本信息</button>' +
    '<button type="button" class="btn sm" data-act="diag">导出诊断包</button>' +
    '</div>' +
    '<div class="mnote">改动即时生效也即时落盘，所以没有「保存」这一步；' +
    '状态栏上那句「已保存 时:分:秒」就是最近一次写盘的时间。' +
    '导出的配置文件里含中转凭据，发给别人之前先自己看一眼。' +
    '「导出诊断包」会把日志、环境信息与设置打成一个文件，排查问题时比截图准。</div>' +
    '</section>';
}
function uiSettingsHTML(u){
  return '<p class="mintro">本页所有选项<b>改完即时生效</b>，不需要重启，也没有「保存」这一步。' +
    '带<b>已改动</b>标记的是与默认值不同的项；把鼠标停在一行上可以看到更细的说明。</p>' +
    uiCat('theme', '主题外观', '配色、背景、特效与排版。改完立刻看到。', uiThemeHTML(u)) +
    uiCat('aux', '辅助工具', '软件附带的附加能力，可按需开关。', uiAuxHTML(u)) +
    uiCat('info', '关于与状态', '只读信息集中在这里，不重复出现在别处。', uiAboutHTML(u)) +
    uiMaintHTML() +
    '<input type="file" id="wallFile" accept="image/*" style="display:none">' +
    '<input type="file" id="cfgFile" accept=".json,application/json" style="display:none">';
}

/* 进设置页：取一次设置、渲染、绑定。
   重渲染时保留滚动位置 —— 改完一项会被重画（例如取值被回退），
   滚回顶部会让人以为"刚才那一步跳走了"。 */
async function renderSettings(){
  var body = $('settingsBody');
  if(!body){ return; }
  try{ ui = await (await fetch('/api/settings')).json(); }catch(e){ ui = null; }
  if(!ui || !ui.ok){
    body.innerHTML = '<p class="mintro">读不到设置：界面服务没有响应。' +
      '刷新页面再试一次；如果一直这样，用「导出诊断包」把日志发出去。</p>';
    return;
  }
  var keep = body.scrollTop;
  body.innerHTML = uiSettingsHTML(ui);
  uiBind();
  body.scrollTop = keep;
}
function openSettings(){ go('settings'); }
function uiBind(){
  var body = $('settingsBody');
  body.onclick = function(e){
    var b = e.target && e.target.closest ? e.target.closest('button') : null;
    if(!b || !body.contains(b)){ return; }
    if(b.hasAttribute('data-v')){
      var seg = b.parentNode;
      Array.prototype.forEach.call(seg.querySelectorAll('button'), function(x){ x.classList.remove('on'); });
      b.classList.add('on');
      uiSave();
      return;
    }
    if(b.hasAttribute('data-accent')){
      Array.prototype.forEach.call(body.querySelectorAll('[data-accent]'), function(x){ x.classList.remove('on'); });
      b.classList.add('on');
      uiSave();
      return;
    }
    if(b.hasAttribute('data-act')){ uiAction(b.getAttribute('data-act'), b); }
  };
  body.onchange = function(e){
    if(e.target && e.target.getAttribute && e.target.getAttribute('data-sw')){ uiSave(); }
  };
  Array.prototype.forEach.call(body.querySelectorAll('[data-in]'), function(inp){
    inp.onchange = uiSave;
    inp.onkeydown = function(e){ if(e.key === 'Enter'){ uiSave(); } };
  });
  Array.prototype.forEach.call(body.querySelectorAll('[data-rng]'), function(r){
    var out = body.querySelector('[data-rv="' + r.getAttribute('data-rng') + '"]');
    r.oninput = function(){ if(out){ out.textContent = r.value; } };
    r.onchange = uiSave;
  });
  var fi = $('wallFile');
  if(fi){ fi.onchange = function(){ if(fi.files && fi.files[0]){ uiImportWall(fi.files[0]); } }; }
  var cf = $('cfgFile');
  if(cf){ cf.onchange = function(){ if(cf.files && cf.files[0]){ uiImportCfg(cf.files[0]); } }; }
}
/* 收集面板上的全部设置，提交给服务端。
   **起点必须是服务端那份 ui，不能是写死的默认值。** 现在整页一次渲染全部条目，
   所以"没渲染的字段不存在"这个坑暂时不会发作 —— 但它随时会：只要有人给设置加一屏、
   或者把某一类折叠起来，从硬编码默认值起步就会变成"在 A 处改一项、顺手把 B 处打回默认"。
   这条以前真的发作过（分模块渲染的那一版），所以起点写死成 ui 并留着这条注释。 */
function uiCollect(){
  var u = ui || {};
  var out = {
    theme:u.theme || 'auto', accent:u.accent || 'mint', motion:u.motion || 'full',
    backdrop:u.backdrop || 'on', scale:u.scale || 'std', flat:u.flat || 'off',
    glass:u.glass || '1', fade:u.fade || 'on',
    bgImage:u.bgImage || '', musicMode:u.musicMode || 'order', musicLoop:u.musicLoop || 'all',
    musicVol:u.musicVol || '70', remember:u.remember || '', logKeep:u.logKeep || '',
    defPort:u.defPort || '', defRelay:u.defRelay || '',
    autoProbe:!!u.autoProbe
  };
  Array.prototype.forEach.call(document.querySelectorAll('#settingsBody [data-seg]'), function(seg){
    var on = seg.querySelector('button.on');
    if(on){ out[seg.getAttribute('data-seg')] = on.getAttribute('data-v'); }
  });
  var acc = document.querySelector('#settingsBody .mdot.on');
  if(acc){ out.accent = acc.getAttribute('data-accent'); }
  Array.prototype.forEach.call(document.querySelectorAll('#settingsBody [data-sw]'), function(c){
    var k = c.getAttribute('data-sw');
    if(k === 'autoProbe'){ out.autoProbe = c.checked; }
    if(k === 'remember'){ out.remember = c.checked ? '' : '0'; }
    // 背景光效：**这里以前漏了**，于是 out.backdrop 永远是写死的 'on' —— 复选框点下去没反应，
    // 重开面板又勾回来，而且任何一次保存都会把用户手改的 backdrop:"off" 打回 on。
    // 现在有一条用例（TestEverySwitchInThePanelIsCollected）逐键比对面板上的开关与这里的读取。
    if(k === 'backdrop'){ out.backdrop = c.checked ? 'on' : 'off'; }
    if(k === 'flat'){ out.flat = c.checked ? 'on' : 'off'; }
    // 【视觉美化，非核心功能】面板淡入
    if(k === 'fade'){ out.fade = c.checked ? 'on' : 'off'; }
  });
  Array.prototype.forEach.call(document.querySelectorAll('#settingsBody [data-in]'), function(i){
    out[i.getAttribute('data-in')] = (i.value || '').trim();
  });
  // 档位滑杆：值就是 0-10 的十进制字符串，与服务端收的形态一致，客户端不再猜类型。
  Array.prototype.forEach.call(document.querySelectorAll('#settingsBody [data-rng]'), function(r){
    out[r.getAttribute('data-rng')] = String(r.value);
  });
  return out;
}
/* 外观各项立刻生效（改完就能看到），其余的存在服务端、下次拼命令时生效 */
function uiApply(u){
  var el = document.documentElement;
  el.setAttribute('data-accent', u.accent || 'mint');
  el.setAttribute('data-motion', u.motion === 'lite' ? 'lite' : 'full');
  el.setAttribute('data-backdrop', u.backdrop === 'off' ? 'off' : 'on');
  el.setAttribute('data-scale', u.scale === 'big' ? 'big' : 'std');
  el.setAttribute('data-flat', u.flat === 'on' ? 'on' : 'off');
  // 圆角与侧边栏这两个是"改了要立刻看见"的项：不写在这里的话，
  // 面板上选了新档位，界面要等下次刷新才变 —— 看起来就像没保存成功。
  el.setAttribute('data-radii', u.radii || 'std');
  el.setAttribute('data-rail', u.rail || 'std');
  // 【视觉美化，非核心功能】data-glass / data-fade 两项交给 VFX 去写。
  // 集中在那里的原因：档位要先过一遍"浏览器认不认 backdrop-filter"，不支持时降级；
  // 业务侧只把设置递过去，不参与任何渲染决策，也**不看它的返回值**。
  VFX.apply(u);
  // 背景图由服务端缩放并缓存好之后返回地址，这里只负责把它挂上去/摘下来。
  // 换图与清空都要求立刻生效，否则得关掉界面重开才看得到。
  if(u.wallURL){
    el.setAttribute('data-wall', 'on');
    el.style.setProperty('--wall', "url('" + u.wallURL + "')");
  }else{
    el.removeAttribute('data-wall');
    el.style.removeProperty('--wall');
  }
  if(window.MCLBX_THEME){ window.MCLBX_THEME(u.theme || 'auto'); }
  else{ el.setAttribute('data-theme', u.theme || 'auto'); }
  syncManualTheme();
}
/* 保存之后就地把会随别的设置变的那两行说明重刷一遍。
   只改文字、不重渲染整页 —— 重渲染会把焦点和光标位置一起弄丢，
   而用户往往正是在输入框里改完直接回车保存的。 */
function uiRefreshHints(u){
  var body = $('settingsBody');
  if(!body || !u){ return; }
  [['[data-in="bgImage"]', wallHintText(u)], ['[data-rng="glass"]', glassHintText(u)]].forEach(function(pr){
    var el = body.querySelector(pr[0]);
    var row = el && el.closest ? el.closest('.mitem') : null;
    var hint = row ? row.querySelector('.mi-d') : null;
    if(hint){ hint.textContent = pr[1]; }
  });
}
/* 状态栏上那句保存状态。它是"改动有没有落盘"的唯一答复 ——
   面板里没有保存按钮，那就得有一句话随时说明此刻的状态，否则「我改了到底存上没」只能靠猜。
   常驻显示时间戳而不是几秒后消失：这是个会长时间开着的工具，事后回看一眼也知道最后写盘在什么时候。 */
function uiSaveState(txt, bad){
  var s = $('saveState');
  if(!s){ return; }
  setText(s, txt || '');
  setCls(s, 'sb-save' + (bad ? ' bad' : ''));
}
function clockNow(){
  var d = new Date();
  function p(n){ return (n < 10 ? '0' : '') + n; }
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
}
async function uiSave(){
  uiSaveState('正在保存…');
  var r = null;
  try{ r = await api('/api/settings', uiCollect()); }catch(e){ r = null; }
  if(!r || !r.ok){
    uiSaveState('保存失败：改动没有生效', true);
    toast((r && r.err) || '设置没能保存', true);
    return;
  }
  ui = r.ui;
  uiApply(ui);
  uiRefreshHints(ui);
  uiRefreshTags();
  // 音量属于"改完立刻生效"那一类：不用等下一首
  if(bgmEl){ bgmEl.volume = bgmGain(); }
  // logKeep 按十进制字符串收：服务端两条路（读设置 / 保存）给的是同一种形态，客户端不再猜类型
  var lk = parseInt(r.logKeep, 10);
  if(!isNaN(lk)){ LOG_KEEP = lk; }
  // 有取值不合法时把话说清楚：安静地把它改成另一个值，
  // 用户只会以为"保存没生效"，然后反复试同一个非法输入。
  var cor = r.corrected || [];
  if(cor.length){
    var parts = cor.slice(0, 3).map(function(c){
      return c.name + '（' + c.sent + ' → ' + c.used + '）';
    });
    uiSaveState('已回退 ' + cor.length + ' 项非法取值 ' + clockNow(), true);
    toast('有 ' + cor.length + ' 项取值不合法，已回退：' + parts.join('、') +
      (cor.length > 3 ? ' 等' : ''), true);
    // 重画一次，让控件上显示的是真正生效的值，而不是用户刚敲进去的那个。
    // 同时把「已改动」标签一起刷新 —— 值是服务端归一化之后的，标签得跟着它走。
    renderSettings();
    return;
  }
  uiSaveState('已保存 ' + clockNow());
}
/* 导入一张图片。走上传而不是把路径交给服务端去读 —— 导入的意义就是把图复制进存档，
   存完之后原文件改名、移走、删掉都不影响。 */
async function uiImportWall(file){
  var fd = new FormData();
  fd.append('file', file, file.name);
  var r = null;
  try {
    var resp = await fetch('/api/wall/import', { method:'POST', body: fd });
    r = await resp.json();
  } catch(e){ r = null; }
  if(!r || !r.ok){ toast((r && r.err) || '导入失败', true); return; }
  ui = r;
  uiApply(ui);
  renderSettings(); // 图库列表变了，重渲染这一页
  toast('已导入并设为背景图');
}
/* ---- 背景音乐 ----
   播放归界面这一侧：<audio> 原生解 MP3/WAV/FLAC/OGG/M4A，音量对所有格式都有效（实测 MCI 在
   WAV 那一档设备上不支持音量），拖动进度靠服务端 /music/ 的 Range 支持，播完有 ended 事件。
   于是不需要轮询、不需要定时器，Go 侧也不用维护一套播放状态机。

   曲库的真相在磁盘上：列表来自服务端扫描 music 目录的结果。界面只记住「放哪一首」与偏好
   （顺序、循环、音量），偏好存进 config.json。
   放不了的文件的记在 bgmBad 里并显示出来 —— 静默跳过会让用户不知道有一首是坏的。 */
var bgmEl = null, bgmList = [], bgmIdx = -1, bgmBad = {};

/* 配置里是 0-100 的字符串，<audio>.volume 要 0-1。 */
function bgmGain(){
  var v = parseInt((ui && ui.musicVol) || '70', 10);
  if(isNaN(v)){ v = 70; }
  if(v < 0){ v = 0; }
  if(v > 100){ v = 100; }
  return v / 100;
}
/* 底栏那一行只有曲名，没有别的花样，也没有任何动画。 */
function bgmPaint(){
  var el = $('bgmName');
  if(!el){ return; }
  if(bgmIdx < 0 || !bgmList[bgmIdx]){ el.textContent = '未播放'; return; }
  var n = bgmList[bgmIdx];
  if(bgmBad[n]){ el.textContent = n + '（放不了）'; return; }
  el.textContent = (bgmEl && !bgmEl.paused ? '正在放：' : '已暂停：') + n;
}
function bgmSetList(list){
  // 载荷里每首是 {name, size}，这里统一成文件名 —— 早先这里直接当字符串用了，
  // 于是每首都去请求 /music/[object Object]，现象是「列得出来但一首也放不了」。
  bgmList = (list || []).map(function(x){
    return (x && x.name) ? x.name : String(x);
  });
  if(bgmIdx >= 0 && !bgmList[bgmIdx]){ bgmIdx = -1; } // 那一首被删了
  bgmPaint();
}
/* 下一首的下标。随机时避开当前这首（只有一首时只能重复）。 */
function bgmNextIndex(){
  if(!bgmList.length){ return -1; }
  if(bgmList.length === 1){ return 0; }
  if(((ui && ui.musicMode) || 'order') === 'shuffle'){
    var n = bgmIdx;
    while(n === bgmIdx){ n = Math.floor(Math.random() * bgmList.length); }
    return n;
  }
  return bgmIdx < 0 ? 0 : (bgmIdx + 1) % bgmList.length;
}
function bgmPrevIndex(){
  if(!bgmList.length){ return -1; }
  return bgmIdx < 0 ? 0 : (bgmIdx - 1 + bgmList.length) % bgmList.length;
}
function bgmPlayAt(i){
  if(!bgmEl || i < 0 || !bgmList[i]){ return; }
  bgmIdx = i;
  bgmEl.src = '/music/' + encodeURIComponent(bgmList[i]);
  bgmEl.volume = bgmGain();
  var p = bgmEl.play();
  // play() 返回 Promise：被自动播放策略拦下或格式不支持都会在这里被拒。
  // 不接这个拒绝就是"点了没反应" —— 那正是这个项目最不想要的失败方式。
  if(p && p.catch){
    p.catch(function(e){
      var n = bgmList[bgmIdx];
      bgmBad[n] = '浏览器没能播放它（' + ((e && e.name) || '未知原因') + '）';
      toast('「' + n + '」放不了', true);
      bgmPaint();
    });
  }
  bgmPaint();
}
/* 一首放完：按循环设置决定重放本曲、下一首，还是停下。 */
function bgmEnded(){
  var loop = (ui && ui.musicLoop) || 'all';
  if(loop === 'one'){ bgmEl.currentTime = 0; bgmEl.play().catch(function(){}); return; }
  if(loop === 'off' && ((ui && ui.musicMode) || 'order') === 'order' && bgmIdx === bgmList.length - 1){
    bgmPaint();
    return; // 顺序播到最后一首且不循环：停下
  }
  bgmPlayAt(bgmNextIndex());
}
/* 某个文件放不了：记下来、说出来，再跳到还没失败过的一首。
   没有"全部失败就停"这个判断的话，它会一首一首转下去。 */
function bgmFailed(){
  var n = bgmList[bgmIdx];
  if(n){ bgmBad[n] = '放不了（格式不支持或文件损坏）'; }
  bgmPaint();
  if(!n){ return; }
  toast('「' + n + '」放不了，已跳到下一首', true);
  var left = 0;
  bgmList.forEach(function(x){ if(!bgmBad[x]){ left++; } });
  if(!left){ toast('曲库里的音乐都放不了', true); return; }
  bgmPlayAt(bgmNextIndex());
}
/* 曲库要等真正用的时候才去取：不点播放就不碰磁盘，也不占首屏。 */
async function bgmEnsure(){
  if(bgmList.length){ return true; }
  try{
    var r = await (await fetch('/api/settings')).json();
    if(r && r.ok){ ui = r; bgmSetList(r.musicList); return true; }
  }catch(e){}
  return false;
}
async function bgmToggle(){
  bgmEl = bgmEl || $('bgm');
  if(!bgmEl){ return; }
  if(bgmIdx < 0 || !bgmEl.src){
    if(!(await bgmEnsure())){ toast('读不到曲库', true); return; }
    if(!bgmList.length){
      toast('曲库还是空的：把音乐放进存档的 music 文件夹，再在设置里点「刷新」', true);
      return;
    }
    bgmPlayAt(bgmNextIndex());
    return;
  }
  if(bgmEl.paused){ bgmEl.play().catch(function(){}); } else { bgmEl.pause(); }
  bgmPaint();
}
async function bgmStep(delta){
  bgmEl = bgmEl || $('bgm');
  if(!bgmEl){ return; }
  if(!(await bgmEnsure())){ toast('读不到曲库', true); return; }
  if(!bgmList.length){ toast('曲库里还没有音乐', true); return; }
  bgmPlayAt(delta > 0 ? bgmNextIndex() : bgmPrevIndex());
}
/* 恢复默认前的确认条。就地插在维护那一块里，不弹系统对话框 ——
   那几个框在 WebView 里的行为各平台不一致，而这里恰恰需要让用户看清"影响范围"这句话。
   范围写在按钮的 data-board 上：三个范围共用同一个动作名，uiAction 只认那个属性。 */
function uiAskReset(){
  var old = $('mConfirm');
  if(old && old.parentNode){ old.parentNode.removeChild(old); }
  var d = document.createElement('div');
  d.id = 'mConfirm';
  d.className = 'mconfirm';
  d.innerHTML = '<span class="q">把设置项恢复成出厂值；存档里的文件与「记住的填写内容」都不受影响。' +
    '选择范围：</span>' +
    '<button type="button" class="btn sm" data-act="resetDo" data-board="主题外观">仅主题外观</button>' +
    '<button type="button" class="btn sm" data-act="resetDo" data-board="辅助工具">仅辅助工具</button>' +
    '<button type="button" class="btn sm danger" data-act="resetDo" data-board="all">全部设置</button>' +
    '<span class="sp"></span>' +
    '<button type="button" class="linkbtn" data-act="resetCancel">取消</button>';
  var acts = document.querySelector('#settingsBody .mmaint .acts');
  if(acts && acts.parentNode){ acts.parentNode.insertBefore(d, acts.nextSibling); }
  else { $('settingsBody').appendChild(d); }
}
function uiReadFile(file, cb){
  var fr = new FileReader();
  fr.onload = function(){ cb(String(fr.result == null ? '' : fr.result)); };
  fr.onerror = function(){ cb(null); };
  fr.readAsText(file, 'utf-8');
}
/* 导入一份配置文件。不认识的键由服务端跳过并回报数量。 */
async function uiImportCfg(file){
  uiReadFile(file, function(text){
    if(text == null){ toast('读不到文件内容', true); return; }
    uiImportCfgSend(text);
  });
}
async function uiImportCfgSend(text){
  var r = await api('/api/settings/import', { mode:'overwrite', content:text });
  if(!r || !r.ok){ toast((r && r.err) || '导入失败', true); return; }
  ui = r.ui;
  uiApply(ui);
  var skip = r.skipped || [];
  renderSettings();
  toast('已导入，生效 ' + r.applied + ' 项' +
    (skip.length ? '，跳过 ' + skip.length + ' 项（本程序没有的键）' : ''), !!skip.length);
}
/* 设置页上的按钮都从这里走。第二个参数是按钮本身 ——
   有几个动作的范围写在按钮属性上（例如"恢复哪一类"），比按动作名再拼字符串更不容易走偏。 */
async function uiAction(a, btn){
  // 【视觉美化，非核心功能】就地量一秒帧率。不落盘、不改设置，只为回答"这一档配不配这台机器"
  if(a === 'vfxMeasure'){
    var span = $('vfxStats');
    if(span){ span.textContent = '测量中…'; }
    try{
      var m = await VFX.measure(1000);
      if(span){ span.textContent = m.fps + ' 帧/秒，最慢一帧 ' + m.worst + 'ms'; }
    }catch(e){
      if(span){ span.textContent = '这台机器上测不出来'; }
    }
    return;
  }
  /* ---- 配置管理：导出 / 导入 / 恢复默认 / 变更记录 / 复制版本 ----
     导出与导入都走服务端的专用接口，而不是拼一个下载链接 ——
     写成文件再告诉用户在哪，与导出诊断包是同一套做法，也避开了各平台 WebView 下载行为不一的问题。 */
  if(a === 'exportCfg'){
    var er = await api('/api/settings/export', {});
    if(!er || !er.ok){ toast((er && er.err) || '导出失败', true); return; }
    ui = er.ui;
    renderSettings();
    toast('已导出到 ' + er.path);
    return;
  }
  if(a === 'importCfg'){
    var cf = $('cfgFile');
    // 每次都要先清空：连着两次选同一个文件，第二次不会触发 change
    if(cf){ cf.value = ''; cf.click(); }
    return;
  }
  if(a === 'changesClear'){
    var cl = await api('/api/settings/changes', { clear:true });
    if(cl && cl.changes && ui){ ui.changes = cl.changes; }
    renderSettings();
    toast('已清空 ' + ((cl && cl.cleared) || 0) + ' 条记录');
    return;
  }
  /* 恢复默认只有一个入口，范围在这里选。
     以前每个板块标题旁边各挂一个"恢复本板块默认"，加底部一个"重置全部设置" ——
     同一个动作三个按钮，还得先分清它们的范围差在哪。 */
  if(a === 'resetAsk'){ uiAskReset(); return; }
  if(a === 'resetCancel'){
    var cd = $('mConfirm');
    if(cd && cd.parentNode){ cd.parentNode.removeChild(cd); }
    return;
  }
  if(a === 'resetDo'){
    var board = (btn && btn.getAttribute('data-board')) || 'all';
    var label = (board === 'all') ? '全部设置' : ('「' + board + '」这一类');
    var rr = await api('/api/settings/reset', { scope: board });
    if(!rr || !rr.ok){ toast((rr && rr.err) || '恢复默认失败', true); return; }
    var cd2 = $('mConfirm');
    if(cd2 && cd2.parentNode){ cd2.parentNode.removeChild(cd2); }
    if(!rr.changed){ toast(label + '本来就是默认值'); return; }
    ui = rr.ui;
    uiApply(ui);
    renderSettings();
    uiSaveState('已恢复默认 ' + clockNow());
    toast('已把' + label + '恢复默认，共改动 ' + rr.changed + ' 项');
    return;
  }
  if(a === 'copyVersion'){ copy(((ui && ui.version) || 'mclbx') + ''); return; }
  if(a === 'musicRefresh'){
    try{
      var mr = await (await fetch('/api/settings')).json();
      if(mr && mr.ok){ ui = mr; bgmSetList(mr.musicList); }
    }catch(e){}
    renderSettings();
    return;
  }
  if(a === 'bgmPrev'){ bgmStep(-1); return; }
  if(a === 'bgmNext'){ bgmStep(1); return; }
  if(a === 'bgmPlay'){ bgmToggle(); return; }
  if(a === 'wallImport'){ var fi = $('wallFile'); if(fi){ fi.value = ''; fi.click(); } return; }
  if(a === 'wallRefresh'){ renderSettings(); return; }
  if(a === 'diag'){
    // 导出诊断包是个真正的"操作"，所以照操作那条路走（会出现在日志区里）。
    // 走之前先回工作台 —— 结果和日志都在那一页，留在设置页会看不到自己刚做了什么。
    closeModal();
    go('work');
    var j = await api('/api/start', { key:'diag', inputs:{} });
    if(!j || !j.ok){ toast((j && j.err) || '启动失败', true); return; }
    setRunningUI(true); tick();
    return;
  }
  var r = await api('/api/settings', { action:a });
  if(!r || !r.ok){ toast((r && r.err) || '操作失败', true); return; }
  toast(a === 'forgetInputs' ? '已清除记住的填写内容' : '已打开数据目录');
}

/* 起手：先按地址栏的 hash 决定落在哪一页，再铺界面、开始轮询。
   只认 #/work、#/settings、#/manual 三个值，其它（包括空）都落到工作台。
   这里以前有四条查询串深链（?manual=1 / ?settings=1 / ?quick=host / ?quick=guest / ?more=1），
   每条都只服务于当时的一次自动化截图核对 —— 它们已随新界面一并删除。 */
$('btnWork').onclick = function(){ go('work'); };
$('btnSettings').onclick = openSettings;
$('btnManual').onclick = function(){ go('manual'); };
var startPage = (location.hash || '').replace(/^#\//, '');
if(!PAGES[startPage]){ startPage = 'work'; }
page = '';            // 先清空，好让第一次 go() 真的把三页的显隐与导航高亮铺一遍
go(startPage);
$('fwSkip').onclick = function(){ fwSkipped = true; setHidden($('fwBanner'), true); };
// 底栏的背景音乐入口。曲库与曲目要等真正用的时候才去取，所以这里只绑事件、不拉数据。
bgmEl = $('bgm');
if(bgmEl){
  bgmEl.onended = bgmEnded;
  // 文件解不开时 <audio> 只发 error 事件、不抛异常，不接的话就是"点了没反应"
  bgmEl.onerror = bgmFailed;
  bgmEl.volume = bgmGain();
}
$('bgmToggle').onclick = bgmToggle;
$('btnStop').onclick = stopJob;
$('btnQuit').onclick = quitApp;
$('btnClear').onclick = resetLog;
$('btnCopyLog').onclick = function(){ copy((layer === 'user' ? userLines : rawLines).join('\n')); };
$('btnCopyAddr').onclick = function(){ copy(addr); };
/* ---- 浮层的键盘契约（对齐 WAI-ARIA 的 dialog 模式）----
   打开时把焦点移进对话框、Tab 只在框内循环、关闭时把焦点还给当初打开它的那个按钮。
   少任何一条，键盘用户要么"掉"到后面的页面上，要么关掉之后不知道自己在哪、只能从头 Tab。
   关闭入口有四条（关闭按钮、点背板、Esc、导出诊断包之后），全部走 closeModal ——
   所以"直接把 hidden 设成 true"这句话只准出现在 closeModal 里，别处再写一遍，
   焦点归还就会有的时候有、有的时候没有。 */
var modalOpener = null;
function modalFocusable(){
  var box = document.querySelector('.mbox');
  if(!box){ return []; }
  return Array.prototype.filter.call(
    box.querySelectorAll('button,[href],input,select,textarea,[tabindex]'),
    function(el){ return !el.disabled && el.tabIndex >= 0; });
}
function openModal(){
  modalOpener = document.activeElement;
  setHidden($('modal'), false);
  var box = document.querySelector('.mbox');
  if(box){ try{ box.focus(); }catch(e){} }   // 焦点进框：读屏会先念它的标题（aria-labelledby）
  document.addEventListener('keydown', modalTrap, true);
}
function closeModal(){
  if(!$('modal').hidden){ setHidden($('modal'), true); }
  document.removeEventListener('keydown', modalTrap, true);
  if(modalOpener && modalOpener.focus){ try{ modalOpener.focus(); }catch(e){} }
  modalOpener = null;
}
/* Tab 不许跑出框：正向的最后一个、反向的第一个都拦下来送到另一端。
   框里一个可聚焦元素都没有时把焦点按在框上，别让它漏到后面的页面。 */
function modalTrap(e){
  if(e.key !== 'Tab'){ return; }
  var box = document.querySelector('.mbox');
  if(!box){ return; }
  var items = modalFocusable(), first = items[0], last = items[items.length - 1];
  if(!first){ e.preventDefault(); box.focus(); return; }
  if(e.shiftKey && (document.activeElement === first || document.activeElement === box)){
    e.preventDefault(); last.focus(); return;
  }
  if(!e.shiftKey && document.activeElement === last){ e.preventDefault(); first.focus(); }
}
$('modalClose').onclick = function(){ closeModal(); };
$('modal').onclick = function(e){ if(e.target === $('modal')){ closeModal(); } };
/* 【视觉美化，非核心功能】启动美化层。
   它只把设置写成 <html> 上的属性，不落盘、也不回头改设置 —— 改档位仍然只从设置面板那一条路走。 */
VFX.init(window.MCLBX_UI || null);
Array.prototype.forEach.call($('segs').querySelectorAll('.seg'), function(sg){
  sg.onclick = function(){
    layer = sg.getAttribute('data-f');
    refreshLog();
  };
});
document.addEventListener('keydown', function(e){
  if(e.key === 'Escape'){
    if(!$('modal').hidden){ closeModal(); return; }
    clearSel();
    return;
  }
  var tag = document.activeElement ? document.activeElement.tagName : '';
  if(e.key === 'Enter' && tag !== 'INPUT'){ startTask(); }
});

/* 顶部提示条：界面只有一扇窗，所以"本来会弹个黑框告诉你"的事都改成写在这里。
   服务端在退回浏览器时会带上 ?notice=原因。 */
(function(){
  var m = /[?&]notice=([^&]*)/.exec(location.search);
  if(!m){ return; }
  var box = $('notice'), txt = $('noticeText');
  if(!box || !txt){ return; }
  var s = m[1];
  try{ s = decodeURIComponent(s.replace(/\+/g, ' ')); }catch(e){}
  txt.textContent = s;
  box.style.display = 'flex';
})();

/* 起手：拉操作表 → 铺界面 → 开始轮询 */
(async function(){
  try{
    tasks = await (await fetch('/api/tasks')).json();
  }catch(e){
    tasks = [];
  }
  renderCap();
  renderTasks();
  resetLog();
  renderWork();
  tick();
  setInterval(tick, 700);
})();
