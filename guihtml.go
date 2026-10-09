package main

// guihtml.go —— 控制台的页面（编在 exe 里，不落文件、不依赖外网）。
// 界面按信号链组织（本机 → 入口 → 房间 → 玩家），空闲时无无限动画以控制显卡占用。

const guiPageHTML = `<!doctype html>
<!--
  ================== 使用说明 · 特效入口 · 性能注意事项 ==================
  · 这个页面是 mclbx 联机工具的界面：左边选一件事，右边填参数、跑起来、看日志。
    由服务端一次性渲染，不依赖外网；设置都存在存档目录的 config.json 里。

  · 视觉特效在哪里调：点右上角「设置」→ 分组【视觉特效】。
      视觉美化        总开关。关掉 = 面板实心、去掉微光与淡入，工具功能完全不变
      毛玻璃          关闭 / 低 5px / 中 9px / 高 14px（"关闭"只表示不做实时模糊）
      科幻轮廓微光    面板描边与四角的静态光晕
      面板淡入        只动透明度；选「动效：精简」或系统"减少动态效果"时自动不生效
      帧率自动降级    连续偏低时问一次要不要降档；想强制维持高特效就取消勾选
    改完立刻生效也立刻落盘。同一组里还有一行「帧率实测」，在当前档位就地量一秒帧率。

  · 四档怎么测（关闭 / 低 / 中 / 高）：在地址栏加参数即可逐档对比，**不改设置、不落盘**：
      http://localhost:<端口>/?vfx=off    ?vfx=low    ?vfx=mid    ?vfx=high
    每一档都按这份清单走一遍，四档的表现应当完全一致：
      ① 左侧条目能选中、能切换；② 表单能输入、能回车提交；
      ③ 任务能启动、日志会滚动、能停止；④ 设置面板能开、能关、能保存。
    想要数字：控制台执行 await VFX.measure(2000) 看 fps 与最慢一帧，或 VFX.stats()。
    四档的数字应当接近 —— 差得多就说明这一档的玻璃没被限住。

  · 性能硬约束（改这一段时请守住，各条都有可执行的判据）：
      · 模糊只准加在**局部面板**上，禁止整屏图层（.bg / .aurora / .grid / .modal）；
      · 内容持续追加的地方不许加模糊（.logcard / .log / .top，后者的进度点在呼吸）；
      · 悬停会位移的元素不许加模糊（.t / .gcard 带 transform，位移等于每帧重新取样）；
      · 任务执行期间整体让位（data-vfx-busy），跑完立刻恢复；
      · 不写 will-change —— 那会为每一块玻璃永久占一个合成层；
      · 淡入只动 opacity；一旦动 transform，背后那层模糊就得每帧重采样。
    判据在 appearance_test.go 里，以 TestBackdrop / TestGlass / TestFade 开头的那些。
    全部美化（CSS 里【视觉美化，非核心功能】那一整块 + 脚本里的 VFX 模块）可整段删除：
    删掉之后界面回到实心扁平外观，工具功能一件不少。
  ======================================================================
-->
<html lang="zh-CN"@@UIATTRS@@>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mclbx 联机工具</title>
<link rel="icon" href="/assets/favicon.png">
<script>
/* 主题在首屏之前就定下来。
   服务端已经把用户选的（或者"跟随系统"）写进 <html> 的属性里，这里只做一件事：
   把"跟随系统"解析成当前的系统偏好，并跟着它变 —— 不能等界面脚本加载完，
   否则开着浅色系统的机器会先闪一下深色。 */
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
  // 设置面板改主题时从这里走：切到"跟随系统"要重新解析一次，
  // 而且那个解析函数只在这儿有一份，别的地方不该再写一遍。
  window.MCLBX_THEME = function(theme){
    u.theme = theme || 'auto';
    u.followOS = (u.theme === 'auto');
    resolve();
  };
})();
</script>
<style>
/* ==========================================================================
   字体只用系统自带的：Bahnschrift 是 Win10 1709+ 自带的 DIN 风标题字，
   Cascadia Mono 是终端字，正文回落雅黑。一律不外链，离线机器上完全一致。
   ========================================================================== */
:root{
  /* ---- 底色与面 ----
     四个面由浅到深：bg（页底）→ surf（卡片）→ surf2（卡片里的控件底）→ surf3（悬停）。
     chrome 是顶栏与底栏，panel 是"压在页底上的半透明层"（日志、遮罩），
     field 是所有输入框与只读文本框的底。 */
  --bg:#070B11; --bg2:#0A1017;
  --surf:#0D151E; --surf2:#101B25; --surf3:#14212D;
  --chrome:#090E14; --panel:rgba(3,6,10,.72); --field:rgba(5,9,14,.72);
  --search:rgba(7,11,17,.75); --plate:rgba(4,9,8,.85); --pre-bg:rgba(62,224,161,.06);
  --line:#1A2836; --line2:#233A4D; --hair:rgba(255,255,255,.08); --hair2:rgba(255,255,255,.14);
  --grid-ink:rgba(255,255,255,.032); --sheen:rgba(255,255,255,.12);
  --scroll:#1E3040; --scroll-hi:#2A4356;
  --aurora-a:rgba(62,224,161,.045); --aurora-b:rgba(99,169,255,.10);
  /* ---- 字 ----
     四级灰阶 + 一组"彩色字"：后者只用在状态与日志上，作用是让人一眼看出这句话是什么性质。
     **四级都必须读得清**（对比度见 uisettings_test.go 的那张表）：原来的四级 muted2 只有
     2.08（它是行号与底栏），等于把"读不清"当成了"层次"。层次靠字号与位置去做，不靠让人看不见。 */
  --ink:#E9F2FB; --ink2:#9DB1C6; --muted:#8190A1; --muted2:#7C8B98;
  --ok-ink:#7CF0C4; --bad-ink:#FFB0BE; --warn-ink:#FFD79A; --info-ink:#A8CDFF;
  --addr-ink:#DDFFF2; --violet-ink:#E4DDFF; --bar-ink:#06120C; --cheek:#062A1E;
  --on-sig:#07231A; --sel:rgba(62,224,161,.28);
  /* ---- 强调色 ----
     这一个色族由 data-accent 换（见下）。深浅两套主题共用同一组半透明值，
     只有"实心色"和"压在实心色上的字"需要按主题分别给 —— 浅底上必须更深才够对比。 */
  --sig:#3EE0A1; --sig-deep:#1FA87A; --sig-soft:rgba(62,224,161,.13);
  --sig-edge:rgba(62,224,161,.34); --sig-ring:rgba(62,224,161,.14);
  --sig-focus:rgba(62,224,161,.55); --sig-live:rgba(62,224,161,.7); --sig-glow:rgba(62,224,161,.9);
  /* ---- 语义色：固定，不随强调色走 ---- */
  --amber:#FFB454; --amber-soft:rgba(255,180,84,.12); --amber-edge:rgba(255,180,84,.34);
  --amber-ink:#FFDFAE; --amber-on:#3A2A0E;
  --rose:#FF6E85; --rose-soft:rgba(255,110,133,.12); --rose-soft2:rgba(255,110,133,.15);
  --rose-edge:#4A2230; --rose-ink:#FFC9D2;
  --blue:#63A9FF; --violet:#B08CFF; --violet-soft:rgba(176,140,255,.07); --violet-edge:rgba(176,140,255,.28);
  --toast-ok-bg:rgba(9,26,20,.96); --toast-ok-ink:#D9FFF1; --toast-ok-edge:rgba(62,224,161,.5);
  --toast-bad-bg:rgba(32,11,17,.96); --toast-bad-ink:#FFD3DA; --toast-bad-edge:rgba(255,110,133,.5);
  /* ---- 尺寸与字体 ---- */
  --s1:4px; --s2:8px; --s3:12px; --s4:16px; --s5:24px;
  --r1:6px; --r2:9px; --r3:13px; --pill:999px;
  /* 字号：**名字就是它当前的像素值**（--fs-12-5 = 12.5px）。取这个名字是为了"看上去是几号字"和
     变量名一一对应，加减档位时不会记错。规矩只有一条：**所有字号只能写 var(--fs-*)，不许再写裸 px**
     —— 界面的"大字号"开关靠的就是这一点（见 TestEveryFontSizeGoesThroughToken）。 */
  --fs-10:10px; --fs-10-5:10.5px; --fs-11:11px; --fs-11-5:11.5px; --fs-12:12px; --fs-12-5:12.5px;
  --fs-13:13px; --fs-13-5:13.5px; --fs-14:14px; --fs-15:15px; --fs-16:16px; --fs-17:17px; --fs-18:18px;
  --fs-9:9px; --fs-26:26px;
  /* 顶栏与底栏的高度跟着字号走：字号一放大，固定高度的这两条会把字切掉。 */
  --row-top:56px; --row-bot:30px;
  /* 背景图片的地址由服务端按当前设置注入到 <html> 的行内样式里。这里必须给一个默认值：
     样式表里一旦写了 var(--wall)，定义它的地方就得在样式表里（有用例逐个变量核对）。 */
  --wall:none;
  /* ---- 毛玻璃与壁纸用的静态色 ----
     这几个值写死成 rgba()，而不是用 color-mix() 从 --surf 现算。
     原因：color-mix() 是 Chromium 111（2023-03）才有的函数，而这个界面跑在系统自带的
     WebView2 上 —— 那台机器上的 Chromium 版本由微软的更新决定，不由这个 exe 决定。
     不支持的引擎会在解析期把整条声明丢掉，后果分两档：
       · 毛玻璃那几条只是"没效果"（面板回落到原本的实心色），还算能看；
       · **暗化层会直接消失**，照片上的字就没有东西托着了 —— 这一档是危险的。
     所以这里不接受任何版本相关的色彩函数，宁可每个主题各写一份静态值
     （有没有漏写由「每个主题必须声明全部主题私有变量」那条用例兜住）。 */
  --glass:rgba(13,21,30,.74); --glass-2:rgba(16,27,37,.78); --glass-3:rgba(20,33,45,.78);
  /* 【视觉美化，非核心功能】设置面板自己的底：它比普通面板实一档，因为里面是表单，
     字要最好读；比实心又多透出一点，好让背后那层模糊看得出来。 */
  --glass-panel:rgba(13,21,30,.88);
  --wall-scrim:rgba(7,11,17,.58);
  --disp:"Bahnschrift","Segoe UI Variable Display","Microsoft YaHei UI",sans-serif;
  --ui:"Microsoft YaHei UI","Segoe UI Variable Text","Segoe UI",sans-serif;
  --mono:"Cascadia Mono","Consolas","Microsoft YaHei UI",monospace;
}

/* ==========================================================================
   加一个主题只准动两个地方：这里的一组选择器块，加上 guiconfig.go 的 normalizeUI 取值表。

   为什么写死这一条：主题是**纯属性选择器 + 变量覆盖**，运行时没有任何分支、没有动画、
   没有额外资源 —— 页面上多一套主题只是多几百字节的 CSS，换主题只等于写一次属性 + 一次样式重算。
   一旦有人改成"在注入路径上加分支"或"按主题换版式"，这个成本模型当场失效。
   服务端注入（renderGuiPage）是唯一的闸门，它只认归一化后的取值；绕过它就没有闸门了。

   变量分三类，改之前先认清楚自己在动哪一类：
     · 主题私有：每一个主题块都必须自己声明全部（见 uisettings_test.go 的完整性用例）。
       漏一个的症状是"某一块还是上一个主题的颜色"，不报错、不崩。
     · 强调色私有：--sig / --sig-deep / --sel / --pre-bg / --aurora-a。只有强调色块与
       "确实需要为浅底重新调实心色"的主题（浅色）才准动它。
     · 两类共用：尺寸与字体；以及强调色那一族的半透明值（深浅底上都成立，见用例里的例外表）。
   注意 --pre-bg 与 --aurora-a 是"强调色顺带决定页面氛围"的一对耦合：强调色换了，片段底色与
   极光倾向也跟着换。真要加"背景类主题"（中性底/纯黑/暖色），必须先把这两个从强调色块里
   拿出来交给主题 —— **顺序反了就会得到『主题 × 强调色』的全组合**。
   ========================================================================== */

/* 强调色：只换这一个色族。几个色都取自界面里本来就在用的颜色，
   所以换它不会破坏"信号绿 = 这条路通了"这层意思，只是换了个口味。
   这一组不区分深浅主题：半透明的那些在两种底色上都成立。 */
:root[data-accent="blue"]{
  --sig:#63A9FF; --sig-deep:#2F7BD8; --sig-soft:rgba(99,169,255,.13);
  --sig-edge:rgba(99,169,255,.34); --sig-ring:rgba(99,169,255,.14);
  --sig-focus:rgba(99,169,255,.55); --sig-live:rgba(99,169,255,.7); --sig-glow:rgba(99,169,255,.45);
  --sel:rgba(99,169,255,.28); --pre-bg:rgba(99,169,255,.06); --aurora-a:rgba(99,169,255,.05);
}
:root[data-accent="violet"]{
  --sig:#B08CFF; --sig-deep:#7C55D8; --sig-soft:rgba(176,140,255,.13);
  --sig-edge:rgba(176,140,255,.34); --sig-ring:rgba(176,140,255,.14);
  --sig-focus:rgba(176,140,255,.55); --sig-live:rgba(176,140,255,.7); --sig-glow:rgba(176,140,255,.45);
  --sel:rgba(176,140,255,.28); --pre-bg:rgba(176,140,255,.06); --aurora-a:rgba(176,140,255,.05);
}
/* 这里**没有**琥珀色。琥珀在这套界面里已经是一个专用含义（"要注意"：未放行入站、
   名单超限、警告行），把它同时做成"正常路径"的颜色，两者就分不出来了。
   宁可少一个口味，也不要一个"看着一样、意思相反"的组合。 */

/* 浅色主题：同一套变量、同一个版式，只换底色与字色。
   有意不做成"另一个界面" —— 版式、间距、层级都不动，这样两种主题下的操作习惯是同一套。
   注意它必须在强调色那一组之后：两边特异性相同（都是 :root + 一个属性选择器），
   靠源序决定谁赢；真正需要"浅色 × 某个强调色"的值时，用下面的复合选择器单独给。 */
:root[data-theme="light"]{
  --bg:#F2F5F9; --bg2:#E9EEF5;
  --surf:#FFFFFF; --surf2:#F6F9FC; --surf3:#EDF2F8;
  --chrome:#FFFFFF; --panel:rgba(9,17,26,.045); --field:rgba(9,17,26,.045);
  --search:rgba(9,17,26,.05); --plate:rgba(255,255,255,.72); --pre-bg:rgba(14,159,110,.08);
  --line:#DCE4EC; --line2:#BFCEDC; --hair:rgba(9,17,26,.10); --hair2:rgba(9,17,26,.16);
  --grid-ink:rgba(9,17,26,.05); --sheen:rgba(255,255,255,.55);
  --scroll:#C6D2DE; --scroll-hi:#AEBECB;
  --aurora-a:rgba(14,159,110,.05); --aurora-b:rgba(42,111,214,.06);
  --glass:rgba(255,255,255,.74); --glass-2:rgba(246,249,252,.78); --glass-3:rgba(237,242,248,.78);
  --glass-panel:rgba(255,255,255,.90);
  --wall-scrim:rgba(242,245,249,.58);
  --ink:#0D1620; --ink2:#4A5A6C; --muted:#5A6675; --muted2:#636B75;
  --ok-ink:#0B7A4F; --bad-ink:#C42744; --warn-ink:#8A5300; --info-ink:#1F5FA8;
  --addr-ink:#0B3B2A; --violet-ink:#4A2E86; --bar-ink:#FFFFFF; --cheek:#FFFFFF;
  --on-sig:#FFFFFF; --sel:rgba(14,159,110,.20);
  /* 实心色必须压到白字读得清为止：#0E9F6E 上压白字只有 3.39，而"浅色 + 默认强调色"
     恰恰是最常被用到的一格（顶栏进度链点亮的段、设置面板里被选中的那一格）。
     压深到 #0B7A54 之后白字 5.35，同时它自己做文字/描边在浅底上也有 4.89。 */
  --sig:#0B7A54; --sig-deep:#076242; --sig-glow:rgba(14,159,110,.35);
  --amber:#B26A00; --amber-soft:rgba(178,106,0,.10); --amber-edge:rgba(178,106,0,.30);
  --amber-ink:#7A4700; --amber-on:#FFFFFF;
  --rose:#D0364F; --rose-soft:rgba(208,54,79,.09); --rose-soft2:rgba(208,54,79,.14);
  --rose-edge:#E7B6C0; --rose-ink:#9E2038;
  --blue:#2A6FD6; --violet:#7A4FD0; --violet-soft:rgba(122,79,208,.07); --violet-edge:rgba(122,79,208,.28);
  --toast-ok-bg:rgba(233,250,241,.98); --toast-ok-ink:#0B5C3B; --toast-ok-edge:rgba(14,159,110,.45);
  --toast-bad-bg:rgba(255,238,241,.98); --toast-bad-ink:#93203A; --toast-bad-edge:rgba(208,54,79,.45);
}
:root[data-theme="light"][data-accent="blue"]{
  --sig:#2A6FD6; --sig-deep:#1F55A8; --sig-glow:rgba(42,111,214,.32);
  --sel:rgba(42,111,214,.20); --pre-bg:rgba(42,111,214,.08); --aurora-a:rgba(42,111,214,.06);
}
:root[data-theme="light"][data-accent="violet"]{
  --sig:#7A4FD0; --sig-deep:#5B37A2; --sig-glow:rgba(122,79,208,.32);
  --sel:rgba(122,79,208,.20); --pre-bg:rgba(122,79,208,.08); --aurora-a:rgba(122,79,208,.06);
}

/* 高对比：给"看不清"而不是"不喜欢"的处境用 —— 低端 TN 屏、白天靠窗的反光、投屏给旁边的人看。
   它**不是**第 4 种口味，而是深色那一套把对比度推到顶：正文 20:1、次级 16:1、最低一级也有 8:1。
   两点刻意的取舍：
     · 不动强调色族（那一族本来就是为深底调的，所以这一个主题只有这一块，不需要
       『主题 × 强调色』的复合规则 —— 那是加法里最贵的一种）；
     · 页面氛围（极光 + 网格）让位给对比度：装饰与"看清"在这类处境里是直接冲突的。
   命名与取值口径见项目内部开发记录的对应轮次。 */
:root[data-theme="contrast"]{
  --bg:#000308; --bg2:#04070C;
  --surf:#080D13; --surf2:#0C1219; --surf3:#111922;
  --chrome:#000308; --panel:rgba(0,0,0,.88); --field:rgba(255,255,255,.06);
  --search:rgba(0,0,0,.88); --plate:rgba(0,0,0,.92);
  --line:#2C3E52; --line2:#4A6A8A; --hair:rgba(255,255,255,.20); --hair2:rgba(255,255,255,.34);
  --grid-ink:rgba(255,255,255,0); --sheen:rgba(255,255,255,.10);
  --scroll:#3A5470; --scroll-hi:#4E7092;
  --aurora-a:rgba(0,0,0,0); --aurora-b:rgba(0,0,0,0);
  --glass:rgba(8,13,19,.74); --glass-2:rgba(12,18,25,.78); --glass-3:rgba(17,25,34,.78);
  /* 高对比这一档不参与毛玻璃（面板保持实心，见 VFX 那一段）：它的存在理由是"看不清"，
     半透明与模糊都与"看清"直接冲突。这里仍然声明，是为了满足"每个主题声明全部"的口径。 */
  --glass-panel:rgba(0,0,0,.98);
  --wall-scrim:rgba(0,3,8,.58);
  --ink:#FFFFFF; --ink2:#DAE6F2; --muted:#A9BCD0; --muted2:#93A7BC;
  --ok-ink:#7CF5C8; --bad-ink:#FFB8C4; --warn-ink:#FFD79A; --info-ink:#B6D4FF;
  --addr-ink:#E6FFF6; --violet-ink:#EDE7FF; --bar-ink:#03110B; --cheek:#03110B;
  --on-sig:#03110B;
  --amber:#FFC470; --amber-soft:rgba(255,196,112,.16); --amber-edge:rgba(255,196,112,.5);
  --amber-ink:#FFE0B0; --amber-on:#2A1A00;
  --rose:#FF8AA0; --rose-soft:rgba(255,138,160,.16); --rose-soft2:rgba(255,138,160,.26);
  --rose-edge:#7A3348; --rose-ink:#FFD3DA;
  --blue:#8CC4FF; --violet:#C4A8FF; --violet-soft:rgba(196,168,255,.12); --violet-edge:rgba(196,168,255,.45);
  --toast-ok-bg:rgba(0,20,14,.97); --toast-ok-ink:#DFFFF0; --toast-ok-edge:rgba(62,224,161,.7);
  --toast-bad-bg:rgba(30,0,8,.97); --toast-bad-ink:#FFDCE2; --toast-bad-edge:rgba(255,138,160,.7);
}

/* 动效「精简」：效果上等同于系统的 prefers-reduced-motion，但**这里是手抄的第二份**
   （系统那一份见本文件末尾的 @media 块）。两处必须逐条相同 —— 只改一处就会走偏，
   而"走偏"之后没有任何症状，所以有一条用例把两个集合逐条比对（TestLiteMotionMatchesSystemPreference）。
   注意执行中的状态点仍然留着（它是"正在干活"的唯一反馈），只是不呼吸。
   另：这一档**不许**碰背景光效（那是 data-backdrop 的事），用例会拦住。 */
:root[data-motion="lite"] *{transition:none !important}
:root[data-motion="lite"] .dot.run::after{animation:none;opacity:.45}
/* 面板淡入也是一处"动"。它走的是时长变量而不是 transition，所以光有关掉 transition 那条还不够 ——
   把时长归零即等于关掉（下面 @media 里必须逐条相同，用例会比对两份）。 */
:root[data-motion="lite"]{--vfx-fade:0s}
/* 背景光效关掉：极光与网格是纯装饰，关掉不影响任何信息。
   它只管这两层 —— 自选背景图片是另一件事（由 data-wall 决定），关掉光效不该把图一起关掉。 */
:root[data-backdrop="off"] .aurora,
:root[data-backdrop="off"] .grid{display:none}

/* ---- 大字号（独立开关，**不是主题**） ----
   为什么不做成第四个主题：主题的维度是"配色"，而字号动的是**版式**。把它塞进主题行，
   会得到"主题 × 字号"的组合爆炸，而且用户没法从"深色/浅色"这类名字里读出字号。
   所以它是和 data-motion / data-backdrop 同级的**独立维度**，默认 std，只有需要的人去打开。

   覆盖的只是字号 token 与顶栏/底栏高度：**不给任何控件单独写样式**，所以布局结构与默认档完全一致，
   区别只有"字大了一号"。这一档解决的是一件事——高对比能解决"看不清对比度"，解决不了"字太小"。

   加档位时注意：这里必须覆盖 :root 里声明的**每一个** --fs-*，少一个就会出现"大部分变大、某一块没变"
   （用例 TestBigScaleCoversEveryFontToken 会拦住）。 */
:root[data-scale="big"]{
  --fs-10:11px; --fs-10-5:11.5px; --fs-11:12px; --fs-11-5:12.5px; --fs-12:13px; --fs-12-5:13.5px;
  --fs-13:14px; --fs-13-5:14.5px; --fs-14:15px; --fs-15:16px; --fs-16:17.5px; --fs-17:18.5px; --fs-18:19.5px;
  --fs-9:10px; --fs-26:29px;
  --row-top:62px; --row-bot:34px;
}
/* ---- 背景图片（独立于背景光效） ----
   一层静态贴图 + 一层压暗。**静态是硬要求**：给背景做平移、缓慢缩放（Ken Burns）或视差，
   等于整屏逐帧重新光栅化，直接回到「空闲 13%」那条红线上（见 guiperf_test.go）。
   盖一层压暗（scrim）不是装饰，是为了让任意一张照片上的文字仍然读得清；
   它的颜色取自当前主题底色，所以深浅两套主题各自得到合适的压暗方向。 */
.wall{position:absolute;inset:0;background-image:var(--wall);
  background-size:cover;background-position:center;background-repeat:no-repeat}
.scrim{position:absolute;inset:0;display:none}
:root[data-wall="on"] .scrim{display:block;background:var(--wall-scrim)}

/* ---- 扁平化（独立开关，**不是主题**） ----
   为什么不做成主题：主题的维度是「配色」，扁平化动的是**材质与层次**（圆角、阴影、渐变、光晕）。
   与字号同理（见上面 data-scale 那段）—— 塞进主题行会得到「配色 × 材质」的组合爆炸，
   而用户没法从「深色 / 浅色」这类名字里读出「扁平」。

   它做的全是减法：圆角归零、外阴影去掉、渐变改纯色、光晕与扫光去掉。
   渲染开销只降不升（阴影会把图层边界向外撑大，圆角要抗锯齿裁剪）；
   真正被删掉的是一个隐患：一旦有人给阴影挂上 transition，每次过渡都要逐帧重新光栅化。

   两条边界：
     · 焦点态必须留下 —— 原来的外阴影聚焦环换成 outline（不占布局、不需要模糊）。焦点看不见等于键盘不能用。
     · **不许出现 .bg / .aurora / .grid** —— 背景光效归 data-backdrop 管，两个开关各管各的
       （用例 TestFlatNeverTouchesTheBackdropLayer 会拦住越界）。 */
:root[data-flat="on"]{--r1:0px;--r2:0px;--r3:0px;--pill:0px}
:root[data-flat="on"] .glyph{background:var(--sig);box-shadow:none}
:root[data-flat="on"] .card,
:root[data-flat="on"] .hero,
:root[data-flat="on"] .plate{background:var(--surf)}
:root[data-flat="on"] .plate .shine{display:none}
:root[data-flat="on"] .run{box-shadow:none}
:root[data-flat="on"] .mdot.on{box-shadow:none;outline:2px solid var(--sig-edge);outline-offset:2px}
:root[data-flat="on"] .qbtn.on{box-shadow:none;outline:2px solid var(--sig-ring);outline-offset:0}
:root[data-flat="on"] .search input:focus,
:root[data-flat="on"] .fld input[type=text]:focus,
:root[data-flat="on"] .mtxt:focus{box-shadow:none;outline:2px solid var(--sig-focus);outline-offset:1px}
:root[data-flat="on"] ::-webkit-scrollbar-thumb,
:root[data-flat="on"] .chip,
:root[data-flat="on"] .linkbtn,
:root[data-flat="on"] .t .nm .lg,
:root[data-flat="on"] .t .lim,
:root[data-flat="on"] .t.on::before,
:root[data-flat="on"] .grp .tag,
:root[data-flat="on"] .subtitle .v,
:root[data-flat="on"] .ck .bx,
:root[data-flat="on"] .qbtn,
:root[data-flat="on"] .qform input,
:root[data-flat="on"] .qform textarea,
:root[data-flat="on"] .qpre,
:root[data-flat="on"] .invtext,
:root[data-flat="on"] .mtxt,
:root[data-flat="on"] .glyph{border-radius:0}

/* ============ 视觉美化（VFX）：一层可以整段删掉的纯装饰 ============
   【视觉美化，非核心功能】
   这一段与配套的 VFX 脚本都不参与任何业务判断：删掉它们，界面回到实心扁平外观，
   功能一件不少。业务代码只做一件事 —— 把设置里那几个值写成 <html> 上的属性。

   开关（全部由服务端在首屏注入，避免"先实心、再闪成玻璃"）：
     data-vfx       on/off              总开关。关掉 = 实心、无光、无淡入
     data-glass     off/low/mid/high    毛玻璃档位。off 只表示"不做实时模糊"，面板照样半透明
     data-glow      on/off              科幻轮廓微光
     data-fade      on/off              面板淡入
     data-vfx-busy  on/off              运算期间自动让位（脚本在任务执行时挂上，见 VFX.busy）

   ---- 为什么这里现在允许真毛玻璃，边界又在哪 ----
   原先这一段是全表禁 backdrop-filter，理由是那条实测红线「空闲 13%、运行中 51%」。
   那次禁的是**占满视口又带模糊或动画**的图层：背后一变就要重新回读、重新模糊，
   而"背后变"由别人触发 —— 一个正在运行的呼吸点就够让它逐帧重算。
   代价按 玻璃面积 × 变化频率 增长，几块面板叠起来就等价于整屏。

   现在放开的理由不是"模糊变便宜了"，而是把这两个乘数都压住了：
     · 只给**局部**面板加模糊，并刻意避开三类地方：
       整屏层（.modal / .bg / .aurora / .grid）；
       悬停会位移的行（.t / .gcard 带 transform，一移动就得每帧重新取样）；
       内容持续追加的日志（.logcard / .log）。
     · 运算期间整体让位：任务一跑起来就挂 data-vfx-busy，backdrop-filter 全部退回 none，
       跑完再恢复。日志在流、进度点在转的那段时间，正是最不该花这个钱的时候。
     · 帧率持续过低时脚本会问用户要不要降档，用户也可以选择维持高特效（见 VFX.watch）。
   三条各有一份可执行的判据：TestBackdropFilterOnlyOnLocalSurfaces、TestGlassYieldsWhileBusy、
   TestNoBackdropFilterOnChurningSurfaces。

   ---- will-change 一个都没写，是故意的 ----
   给玻璃容器挂 will-change:backdrop-filter 会为每一块永久分配一个合成层。这里常驻的玻璃面
   有好几个，等于拿常驻显存换一次首帧；而这些面板平时是静止的，"即将变化"根本不成立。
   按需合成的代价比常驻小，所以它不写在这里，而不是漏了。

   ---- contain 的分工 ----
   .mbox / .toast 用 contain:layout paint，把渲染范围框在面板自己身上。
   .card / .foot 只用 layout，**不能用 paint**：.card 的四角描边画在 -1px 处（.card::before），
   paint containment 会把它们裁掉 —— 那是这套界面里最不该丢的一处细节。

   ---- 为什么设了背景图时毛玻璃一律让位（选择器里那个 :not 不是随手加的）----
   文字要读得清，就得和它实际压着的那个合成色差够远。半透明面板压在一张任意照片上时，
   合成色 = 玻璃色 × α + 压暗色 × (1-α) 再与照片混合，而**照片的内容由用户决定**：
   一张纯白图就能把它推到最亮。实测（把全白图与全黑图各合成一遍）：
   深色主题配全白图时第四级灰落在 3.97，浅色主题配全黑图时落在 4.15，而门槛是 4.5。
   要把它压回 4.5，面板不透明度得提到 0.94 以上 —— 那时照片已经透不出来了，等于没做玻璃。
   反过来把第三、第四级灰各自调亮/调暗到能承受照片，会让这两级收敛成同一个颜色，
   四级灰阶就塌成三级。
   所以这里不去两头凑，直接把两个功能做成互斥：设了背景图，面板保持实心。
   面板上会就地把这件事说清楚（见 uiSettingsHTML 里毛玻璃那一行），不是静默失效。

   有意**不**玻璃化的几处，不是漏了：.capcell / .linkbar / .stat 是嵌在玻璃条里的控件，
   再叠一层半透明会重复压暗一层，而且 .cap 靠 1px 间隙画分隔线，透过去会露出线色。 */
:root[data-vfx="on"]:not([data-wall="on"]) .top,
:root[data-vfx="on"]:not([data-wall="on"]) .foot,
:root[data-vfx="on"]:not([data-wall="on"]) .card,
:root[data-vfx="on"]:not([data-wall="on"]) .gcard,
:root[data-vfx="on"]:not([data-wall="on"]) .t,
:root[data-vfx="on"]:not([data-wall="on"]) .log{
  background-image:none;
  background-color:var(--glass)}
/* 顶边比其余三边亮一档。.top 只有下边框（它贴的是视口上沿），那一档得落在下边框上 ——
   给它写 border-top-color 是一条永远不生效的死规则。 */
:root[data-vfx="on"]:not([data-wall="on"]) .card,
:root[data-vfx="on"]:not([data-wall="on"]) .gcard,
:root[data-vfx="on"]:not([data-wall="on"]) .t,
:root[data-vfx="on"]:not([data-wall="on"]) .log,
:root[data-vfx="on"]:not([data-wall="on"]) .foot{border-top-color:var(--line2)}
:root[data-vfx="on"]:not([data-wall="on"]) .top{border-bottom-color:var(--line2)}
/* 面板自己的悬停/选中态原本是实心色，玻璃下要跟着一起变透明，否则鼠标一划过就"啪"地变实了 */
:root[data-vfx="on"]:not([data-wall="on"]) .t:hover,
:root[data-vfx="on"]:not([data-wall="on"]) .t.on{background-color:var(--glass-2)}
:root[data-vfx="on"]:not([data-wall="on"]) .gcard:hover{background-color:var(--glass-3)}
/* 被判定"环境不支持"的条目本来就是置灰的，鼠标划过不该亮起玻璃悬停色。
   上面那条 :hover 的特异性比 .t.off:hover 高，所以这里必须显式压回去。 */
:root[data-vfx="on"]:not([data-wall="on"]) .t.off:hover{background-color:var(--surf)}

/* 四档模糊值。**档位是离散的**，所以用户拿不到"模糊 200px"这种把界面拖垮的取值；
   低档压在 5px 以内，高档 14px 并且面板上会就地提醒代价。 */
:root[data-glass="low"]{--glass-blur:5px}
:root[data-glass="mid"]{--glass-blur:9px}
:root[data-glass="high"]{--glass-blur:14px}

/* 真毛玻璃只在这几处。挑它们的依据是"背后确实有东西可看"：
   .card 压着网格与极光、.foot 压着极光下缘、.mbox 压着整个主界面 —— 模糊看得出来。
   被排除的三类地方见上面的说明。:not([data-vfx-busy="on"]) 就是运算期间的让位。 */
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .card:not(.logcard),
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .foot,
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .mbox{
  contain:layout;
  backdrop-filter:blur(var(--glass-blur)) saturate(1.08)}
/* 设置面板要能看出背后那层模糊，就得先比实心透一档。高对比主题除外 ——
   那一档存在的理由就是"看不清"，半透明与模糊都跟它直接冲突，面板保持实心。 */
:root[data-vfx="on"]:not([data-wall="on"]):not([data-theme="contrast"]) .mbox{
  contain:layout paint;
  background-color:var(--glass-panel)}

/* 科幻轮廓微光：静态的一次性光晕，**不挂 transition** ——
   给阴影挂过渡等于每次过渡都逐帧重新光栅化，那正是扁平化那一段里记下的坑。 */
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .card:not(.logcard){
  box-shadow:0 0 0 1px var(--sig-ring),0 0 16px -10px var(--sig-glow)}
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .mbox,
:root[data-vfx="on"][data-glow="on"] .toast{
  box-shadow:0 0 0 1px var(--sig-ring),0 0 22px -12px var(--sig-glow)}
/* 轮廓微光同时把四角描边点亮一档：细描边是这套界面的科幻感来源，光晕只是它的补充。 */
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .card:not(.logcard)::before,
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .card:not(.logcard)::after{opacity:.55}

/* 面板淡入。只动 opacity，**不动 transform** ——
   玻璃面板一旦位移，背后那块纹理就得每帧重新采样，等于自己把红线请回来。
   时长走变量，精简动效与系统的"减少动态效果"把变量归零即等于关掉（两处必须逐条相同）。 */
:root{--vfx-fade:.16s}
@keyframes vfx-fade{from{opacity:0}to{opacity:1}}
:root[data-vfx="on"][data-fade="on"] .mbox{animation:vfx-fade var(--vfx-fade) ease-out both}

/* 帧率偏低时那条询问。它也属于美化这一层：关掉总开关就整段消失，业务侧一行都不用改。
   底色先给实心的一份，玻璃那份单列一条 —— 没有背景图时才半透明，
   于是"压在任何一张照片上都读得清"这条口径对它同样成立（见下面那条让位规则）。
   它不带模糊 —— 一次提示不值得再多一块玻璃面。 */
.vfxask{position:fixed;left:50%;bottom:78px;transform:translateX(-50%);z-index:60;
  display:flex;align-items:center;gap:10px;max-width:min(560px,86vw);padding:10px 14px;
  border:1px solid var(--line2);border-radius:var(--r2);background:var(--bg2);
  color:var(--ink2);font-size:var(--fs-12-5)}
:root[data-vfx="on"]:not([data-wall="on"]) .vfxask{background:var(--glass-panel)}
.vfxask button{flex:0 0 auto;padding:4px 10px;border:1px solid var(--line2);
  border-radius:var(--r1);color:var(--ink)}
.vfxask button:hover{border-color:var(--sig-edge)}

*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%}
body{background:var(--bg);color:var(--ink);font:var(--fs-13-5)/1.6 var(--ui);overflow:hidden;
  -webkit-font-smoothing:antialiased;font-variant-numeric:tabular-nums}
button{font:inherit;color:inherit;background:none;border:0;cursor:pointer}
input{font:inherit;color:inherit}
::selection{background:var(--sel)}
[hidden]{display:none !important}

/* 背景：静态。这一层占满视口，所以它绝对不能带 filter / backdrop-filter / 动画。 */
.bg{position:fixed;inset:0;z-index:0;pointer-events:none;overflow:hidden}
.aurora{position:absolute;inset:0;background:
    linear-gradient(180deg,var(--aurora-a),transparent 42%),
    radial-gradient(52% 40% at 88% -6%,var(--aurora-b),transparent 68%)}
.grid{position:absolute;inset:0;opacity:.5;
  background-image:linear-gradient(var(--grid-ink) 1px,transparent 1px),
                   linear-gradient(90deg,var(--grid-ink) 1px,transparent 1px);
  background-size:48px 48px;
  mask-image:radial-gradient(130% 100% at 50% 0,#000 24%,transparent 92%)}

.app{position:relative;z-index:1;display:grid;grid-template-rows:var(--row-top) 1fr var(--row-bot);height:100%}

/* ---------------------------------------------------------------- 顶栏 ---- */
.top{display:flex;align-items:center;gap:var(--s3);padding:0 20px;
  border-bottom:1px solid var(--line);background:var(--chrome)}
.glyph{position:relative;width:22px;height:22px;flex:0 0 auto;border-radius:6px;
  background:linear-gradient(150deg,var(--sig),var(--sig-deep));box-shadow:0 0 0 1px var(--sig-edge)}
.glyph::after{content:"";position:absolute;left:6px;top:6px;width:10px;height:10px;border-radius:2px;background:var(--bg)}
.glyph::before{content:"";position:absolute;left:9px;top:9px;width:4px;height:4px;border-radius:1px;background:var(--sig)}
.brand{display:flex;align-items:baseline;gap:9px;min-width:0}
.brand b{font:600 var(--fs-16)/1 var(--disp);letter-spacing:.03em}
.brand .sub{font-size:var(--fs-12-5);color:var(--ink2)}
.brand .ver{font:var(--fs-11-5)/1 var(--mono);color:var(--muted);padding-left:2px}
.spacer{flex:1}

.linkbar{display:flex;align-items:center;padding:4px 4px 4px 10px;
  border:1px solid var(--line);border-radius:var(--pill);background:var(--surf)}
.linkbar .n{font:var(--fs-11-5)/1 var(--mono);color:var(--muted);padding:4px 9px;border-radius:var(--pill)}
.linkbar .n.on{color:var(--on-sig);background:var(--sig)}
.linkbar .s{width:14px;height:1px;background:var(--line2);flex:0 0 auto}
.linkbar .s.on{background:var(--sig)}

.stat{display:flex;align-items:center;gap:9px;padding:6px 13px 6px 11px;
  border:1px solid var(--line);border-radius:var(--pill);background:var(--surf)}
.dot{position:relative;width:8px;height:8px;border-radius:50%;background:var(--muted2);flex:0 0 auto}
.dot.run{background:var(--sig)}
.dot.bad{background:var(--rose)}
/* 执行中的呼吸圈：只动 transform + opacity；只在有任务时存在，空闲时页面上没有无限动画。 */
.dot.run::after{content:"";position:absolute;inset:-1px;border-radius:50%;
  border:1.5px solid var(--sig-live);animation:beat 1.7s ease-out infinite}
@keyframes beat{0%{transform:scale(1);opacity:.9}70%{transform:scale(3);opacity:0}100%{transform:scale(3);opacity:0}}
.stat span{font-size:var(--fs-12-5);color:var(--ink2);white-space:nowrap}

.btn{padding:7px 14px;border:1px solid var(--line);border-radius:var(--r1);
  color:var(--ink2);font-size:var(--fs-12-5);background:var(--surf2);transition:color .16s,background .16s,border-color .16s}
.btn:hover:not(:disabled){color:var(--ink);border-color:var(--line2);background:var(--surf3)}
.btn:disabled{opacity:.38;cursor:not-allowed}
.btn.sm{padding:5px 10px;font-size:var(--fs-11-5)}
.btn.danger{color:var(--rose-ink);border-color:var(--rose-edge);background:var(--rose-soft)}
.btn.danger:hover:not(:disabled){background:var(--rose-soft2)}

/* 顶部提示条：界面只有一扇窗，凡是"本来会弹个黑框告诉你"的事都写在这里 */
.notice{display:none;align-items:center;gap:10px;padding:9px 20px;font-size:var(--fs-12-5);color:var(--amber-ink);
  background:var(--amber-soft);border-bottom:1px solid var(--amber-edge)}
.notice i{width:6px;height:6px;border-radius:50%;background:var(--amber);flex:0 0 auto}

/* ---------------------------------------------------------------- 主体 ---- */
main{display:grid;grid-template-columns:340px minmax(0,1fr);gap:var(--s4);
  min-height:0;padding:var(--s4) 20px}
.col{display:flex;flex-direction:column;gap:var(--s3);min-width:0;min-height:0}
::-webkit-scrollbar{width:10px;height:10px}
::-webkit-scrollbar-thumb{background:var(--scroll);border-radius:9px;border:2px solid transparent;background-clip:content-box}
::-webkit-scrollbar-thumb:hover{background:var(--scroll-hi);background-clip:content-box}
::-webkit-scrollbar-track{background:transparent}

.rail{display:flex;flex-direction:column;min-height:0;gap:var(--s2)}
/* 「更多功能」展开后，操作列表必须自己滚，不能把上面的能力条挤没：
   实测（headless 截图）收起的列表展开时，能力条会被压成一条 1px 的线。
   根因是 #railmore 只有内容高度、撑破了这一列，于是 flex 收缩落到了能力条头上。
   三处都写死不许收缩、把滚动交给 .tasks，问题消失。 */
.cap,.morebtn{flex:0 0 auto}
#railmore{flex:1;min-height:0;display:flex;flex-direction:column;overflow:hidden}
.rowline{display:flex;align-items:center;gap:var(--s2)}
.search{position:relative;flex:1}
.search input{width:100%;padding:8px 11px 8px 30px;border:1px solid var(--line);border-radius:var(--r1);
  background:var(--search);outline:none;font-size:var(--fs-12-5);transition:border-color .16s,box-shadow .16s}
.search input:focus{border-color:var(--sig-focus);box-shadow:0 0 0 3px var(--sig-soft)}
.search input::placeholder{color:var(--muted2)}
.search .ic{position:absolute;left:10px;top:50%;width:11px;height:11px;margin-top:-6px;
  border:1.5px solid var(--muted);border-radius:50%}
.search .ic::after{content:"";position:absolute;right:-4px;bottom:-3px;width:5px;height:1.5px;
  background:var(--muted);transform:rotate(45deg);border-radius:1px}
.railcount{font:var(--fs-11)/1 var(--mono);color:var(--muted2);white-space:nowrap}

/* 环境能力条：体检结论的精简投影。不可用的能力标红，对应的操作会被置灰。 */
.cap{display:grid;grid-template-columns:1fr 1fr;gap:1px;background:var(--line);
  border:1px solid var(--line);border-radius:var(--r2);overflow:hidden}
.capcell{display:flex;align-items:center;gap:7px;padding:7px 10px;background:var(--surf);
  text-align:left;transition:background .16s}
.capcell:hover{background:var(--surf2)}
.capcell .cl{font:var(--fs-10-5)/1 var(--mono);color:var(--muted2);letter-spacing:.06em}
.capcell .cv{font-size:var(--fs-12);color:var(--ink2);margin-left:auto;white-space:nowrap}
.capcell.ok .cv{color:var(--sig)}
.capcell.no .cv{color:var(--rose)}
.capcell.unk .cv{color:var(--muted2)}
/* 未检测时并成一格：横跨两列，值那一侧用信号绿，明确它是"可点的动作"而不是结论 */
.capcell.wide{grid-column:1 / -1;cursor:pointer}
.capcell.wide .cv{color:var(--sig)}
.capnote{font-size:var(--fs-11-5);color:var(--muted2);padding:0 2px;line-height:1.5}
/* 未选操作时主区那一句话。轻量到不抢视线，只负责把"其他操作在哪"说清楚。 */
.idle{font-size:var(--fs-12-5);color:var(--muted2);line-height:1.7}
.idle b{color:var(--ink2);font-weight:600}

.tasks{flex:1;min-height:0;overflow:auto;padding-right:5px}
.grp{display:flex;align-items:center;gap:8px;margin:var(--s4) 0 var(--s2);
  padding:5px 0 5px 2px;background:var(--bg)}
.grp:first-child{margin-top:2px}
.grp .tag{width:5px;height:5px;border-radius:1.5px;flex:0 0 auto;background:var(--muted2)}
.grp .t{font:var(--fs-11)/1 var(--mono);letter-spacing:.14em;color:var(--muted)}
.grp .rule{flex:1;height:1px;background:var(--line)}
.grp.g1 .tag{background:var(--sig)} .grp.g2 .tag{background:var(--blue)}
.grp.g3 .tag{background:var(--violet)} .grp.g4 .tag{background:var(--amber)}
.grp.g5 .tag{background:var(--muted)} .grp.g6 .tag{background:var(--rose)}

.t{display:block;width:100%;text-align:left;padding:9px 12px 9px 13px;margin-bottom:6px;
  border:1px solid var(--line);border-radius:var(--r2);background:var(--surf);position:relative;
  transition:border-color .16s,background .16s,transform .16s}
.t:hover{border-color:var(--line2);background:var(--surf2);transform:translateX(2px)}
.t .nm{display:flex;align-items:center;gap:7px;font-size:var(--fs-13);font-weight:600;margin-bottom:2px}
.t .nm .lg{margin-left:auto;font:var(--fs-10)/1 var(--mono);color:var(--muted);border:1px solid var(--line2);
  border-radius:3px;padding:2px 4px;white-space:nowrap}
.t .ds{font-size:var(--fs-11-5);line-height:1.5;color:var(--muted);
  display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
.t.on{border-color:var(--sig-edge);background:var(--surf2)}
.t.on::before{content:"";position:absolute;left:-1px;top:9px;bottom:9px;width:2px;border-radius:2px;background:var(--sig)}
.t.on .ds{color:var(--ink2)}
/* 环境不支持的操作：置灰，鼠标提示只给一句原因（见 .t.off 的 title） */
.t.off{opacity:.42;cursor:not-allowed}
.t.off:hover{border-color:var(--line);background:var(--surf);transform:none}
/* 环境存疑但不禁用：琥珀色标记，同样把原因放在 title 里 */
.t .lim{font:var(--fs-10)/1 var(--mono);color:var(--amber);border:1px solid var(--amber-edge);
  border-radius:3px;padding:2px 4px;background:var(--amber-soft);white-space:nowrap}
.t.hide{display:none}
.tempty{padding:26px 10px;text-align:center;color:var(--muted2);font-size:var(--fs-12-5)}

.card{position:relative;border:1px solid var(--line);border-radius:var(--r3);
  background:linear-gradient(180deg,var(--surf),var(--bg2))}
.card::before,.card::after{content:"";position:absolute;width:10px;height:10px;pointer-events:none;
  opacity:0;transition:opacity .18s}
.card::before{top:-1px;left:-1px;border-top:1px solid var(--sig);border-left:1px solid var(--sig);border-radius:var(--r3) 0 0 0}
.card::after{bottom:-1px;right:-1px;border-bottom:1px solid var(--sig);border-right:1px solid var(--sig);border-radius:0 0 var(--r3) 0}
.card:hover::before,.card:hover::after{opacity:.9}

.k{display:flex;align-items:center;gap:8px;font:var(--fs-11)/1 var(--mono);letter-spacing:.14em;
  color:var(--muted);margin-bottom:var(--s3)}
.k::before{content:"";width:12px;height:1px;background:var(--muted2)}
.hint{font-size:var(--fs-12-5);color:var(--ink2)}

/* 首屏：未选择操作时的入口 */
.guide{padding:var(--s5) var(--s5) var(--s4)}
.gtitle{font:600 var(--fs-18)/1.35 var(--disp);letter-spacing:.01em;margin-bottom:6px}
.gsub{font-size:var(--fs-12-5);color:var(--ink2);margin-bottom:var(--s5);max-width:62ch}
.gcards{display:grid;grid-template-columns:repeat(3,1fr);gap:var(--s3)}
.gcard{position:relative;text-align:left;padding:var(--s4);border:1px solid var(--line);border-radius:var(--r2);
  background:var(--surf2);overflow:hidden;transition:border-color .18s,background .18s,transform .18s}
.gcard:hover{border-color:var(--sig-edge);background:var(--surf3);transform:translateY(-2px)}
.gcard .no{font:var(--fs-11)/1 var(--mono);color:var(--muted2);letter-spacing:.1em}
.gcard .gt{font-size:var(--fs-14);font-weight:600;margin:9px 0 5px}
.gcard .gd{font-size:var(--fs-12);line-height:1.55;color:var(--muted)}
.gcard .bar{position:absolute;left:0;top:0;bottom:0;width:2px;background:var(--sig);opacity:.5}
.gcard.g-b .bar{background:var(--blue)} .gcard.g-v .bar{background:var(--violet)}
.gcard.off{opacity:.42;cursor:not-allowed}
.gcard.off:hover{border-color:var(--line);background:var(--surf2);transform:none}
.gsteps{margin-top:var(--s5);padding-top:var(--s4);border-top:1px solid var(--line);display:flex;gap:var(--s5);flex-wrap:wrap}
.gstep{display:flex;gap:9px;align-items:flex-start;font-size:var(--fs-12);color:var(--ink2);max-width:32ch}
.gstep b{display:flex;align-items:center;justify-content:center;width:17px;height:17px;flex:0 0 auto;
  border-radius:50%;font:var(--fs-11)/1 var(--mono);color:var(--bg);background:var(--sig)}

/* 操作头 + 表单 */
.formcard{padding:0;display:flex;flex-direction:column;flex:0 1 auto;max-height:min(56vh,600px)}
.workhead{padding:var(--s4) var(--s5) var(--s4)}
.whTop{display:flex;align-items:center;gap:var(--s3);flex-wrap:wrap}
.whTop h2{font:600 var(--fs-17)/1.3 var(--disp);letter-spacing:.01em}
.chip{font:var(--fs-10-5)/1 var(--mono);padding:3px 7px;border-radius:4px;border:1px solid var(--line2);color:var(--ink2)}
/* 在场玩家：房主唯一能看到"谁进来了"的地方。安全码用等宽字体放大一点，
   因为它是要和玩家对着念、逐位核对的一串字符。 */
.guests{margin-top:var(--s5);padding-top:var(--s4);border-top:1px solid var(--line)}
.guests .ghead{font:var(--fs-11)/1 var(--mono);letter-spacing:.14em;color:var(--muted);margin-bottom:10px}
.guests ul{list-style:none;margin:0;padding:0}
.guests li{display:flex;align-items:baseline;justify-content:space-between;gap:var(--s4);
  padding:7px 0;border-bottom:1px solid var(--line)}
.guests li:last-child{border-bottom:0}
.guests li .gc{font:var(--fs-13)/1 var(--mono);color:var(--ink);letter-spacing:.08em}
.guests li .gm{font:var(--fs-12)/1 var(--mono);color:var(--muted2)}
.chip.run-long{color:var(--amber);border-color:var(--amber-edge);background:var(--amber-soft)}
.chip.grp{color:var(--muted)}
.whDesc{font-size:var(--fs-12-5);color:var(--ink2);margin-top:7px;max-width:76ch}
.whtool{margin-left:auto;display:flex;gap:var(--s2)}
.linkbtn{font:var(--fs-11-5)/1 var(--mono);color:var(--muted);padding:4px 6px;border-radius:4px}
.linkbtn:hover{color:var(--sig);background:var(--surf2)}
.cmd{display:block;margin-top:var(--s3);padding:9px 11px;border:1px dashed var(--line2);border-radius:var(--r1);
  background:var(--field);font:var(--fs-12)/1.5 var(--mono);color:var(--ink2);white-space:pre-wrap;word-break:break-all}
.fields{display:flex;flex-direction:column;gap:var(--s4);flex:1;min-height:0;overflow:auto;padding:0 var(--s5)}
.fld label{display:flex;align-items:baseline;gap:7px;font-size:var(--fs-12-5);color:var(--ink2);margin-bottom:6px}
.fld label .r{font:var(--fs-10)/1 var(--mono);color:var(--rose)}
.fld label .qi{width:12px;height:12px;border-radius:50%;border:1px solid var(--line2);
  font:var(--fs-9)/10px var(--mono);text-align:center;color:var(--muted2);cursor:help;flex:0 0 auto}
.fld input[type=text]{width:100%;padding:10px 12px;border:1px solid var(--line);border-radius:var(--r1);
  background:var(--field);outline:none;font:var(--fs-13)/1.4 var(--mono);transition:border-color .16s,box-shadow .16s}
.fld input[type=text]::placeholder{font-family:var(--ui);color:var(--muted2)}
.fld input[type=text]:focus{border-color:var(--sig-focus);box-shadow:0 0 0 3px var(--sig-soft)}
.ck{display:flex;align-items:center;gap:11px;padding:10px 12px;border:1px solid var(--line);
  border-radius:var(--r1);background:var(--field);cursor:pointer;transition:border-color .16s,background .16s}
.ck:hover{border-color:var(--line2);background:var(--surf2)}
.ck .bx{position:relative;width:17px;height:17px;flex:0 0 auto;border:1.5px solid var(--line2);
  border-radius:4px;background:var(--field);display:block;transition:background .16s,border-color .16s}
.ck input{position:absolute;opacity:0;pointer-events:none}
.ck .tx{display:block;font-size:var(--fs-12-5);color:var(--ink2)}
.ck input:checked+.bx{background:var(--sig);border-color:var(--sig)}
.ck input:checked+.bx::after{content:"";position:absolute;left:4.5px;top:1.5px;width:5px;height:9px;
  border-right:2px solid var(--cheek);border-bottom:2px solid var(--cheek);transform:rotate(42deg)}
.ck input:checked~.tx{color:var(--ink)}
.runbar{display:flex;align-items:center;gap:var(--s3);padding:var(--s3) var(--s5) var(--s4);
  border-top:1px solid var(--line)}
.run{padding:11px 26px;border-radius:var(--r1);font:600 var(--fs-13-5)/1 var(--ui);color:var(--cheek);
  background:var(--sig);box-shadow:0 10px 24px -14px var(--sig-glow);transition:filter .16s}
.run:hover:not(:disabled){filter:brightness(1.07)}
.run:disabled{opacity:.45;cursor:not-allowed;box-shadow:none}
.runhint{font-size:var(--fs-11-5);color:var(--muted2);margin-left:auto;text-align:right;line-height:1.45}

/* 入口地址卡：本工具唯一要交付给用户的东西，所以出现在最上面、字号最大 */
.hero{padding:var(--s4) var(--s5) var(--s5);overflow:hidden;border-color:var(--sig-edge);
  background:linear-gradient(160deg,var(--sig-soft),var(--surf) 46%)}
.hero .k{color:var(--sig)} .hero .k::before{background:var(--sig)}
.plate{position:relative;margin:2px 0 var(--s3);padding:15px 16px;border-radius:var(--r2);
  border:1px solid var(--sig-edge);background:var(--plate);overflow:hidden}
.plate .shine{position:absolute;inset:0;pointer-events:none;
  background:linear-gradient(100deg,transparent 32%,var(--sheen) 50%,transparent 68%);
  transform:translateX(-120%)}
.addr{font:600 var(--fs-26)/1.32 var(--mono);letter-spacing:.005em;word-break:break-all;color:var(--addr-ink);
  -webkit-user-select:all;user-select:all}
.rack{display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap;margin-top:var(--s3)}
.steps{display:flex;flex-direction:column;gap:7px;margin-top:var(--s4);padding-top:var(--s3);border-top:1px solid var(--line)}
.step{display:flex;gap:9px;font-size:var(--fs-12-5);color:var(--ink2)}
.step b{font:var(--fs-11)/1.5 var(--mono);color:var(--sig);flex:0 0 auto}
.jin{margin-top:var(--s4)}
.subtitle{display:flex;align-items:center;gap:8px;margin:0 0 var(--s2);
  font:var(--fs-11)/1 var(--mono);letter-spacing:.12em;color:var(--muted)}
.subtitle .v{width:9px;height:9px;border-radius:2px;background:var(--violet)}
.joinline{font:var(--fs-12-5)/1.5 var(--mono);color:var(--violet-ink);word-break:break-all;
  -webkit-user-select:all;user-select:all;padding:10px 12px;border:1px solid var(--violet-edge);
  border-radius:var(--r1);background:var(--violet-soft)}

/* 说明书覆盖层：整屏接管，内部是独立文档（自带浅色排版），与应用界面互不影响样式 */
.manual{position:fixed;inset:0;z-index:60;display:flex;flex-direction:column;background:var(--bg)}
.manual-h{display:flex;align-items:center;gap:var(--s3);height:46px;padding:0 16px;
  border-bottom:1px solid var(--line);background:var(--chrome);flex:0 0 auto}
.manual-h span{flex:1;font-size:var(--fs-13);color:var(--ink2)}
.manual iframe{flex:1;border:0;width:100%;background:#fff}

/* 一步开局：默认入口。房主只要一个房间码，玩家只要一次粘贴。
   颜色与圆角沿用既有配色（信号绿 var(--sig) / 四级文字 / 圆角 8-16），不引第二套样式。 */
.quick{padding:16px 16px 18px}
.qhead{display:flex;align-items:baseline;gap:12px;flex-wrap:wrap;margin-bottom:14px}
.qhead .qt{font-size:var(--fs-17);font-weight:600;color:var(--ink)}
.qhead .qs{font-size:var(--fs-12-5);color:var(--muted)}
.qbtns{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.qbtn{flex-direction:column;align-items:flex-start;gap:3px;height:auto;min-height:58px;
  padding:12px 14px;text-align:left;line-height:1.35;border-radius:12px;cursor:pointer}
.qbtn .qi{font-size:var(--fs-15);font-weight:600}
.qbtn .qd{font-size:var(--fs-12-5);font-weight:400;color:var(--muted)}
.qbtn:hover{border-color:var(--sig)}
.qbtn.on{border-color:var(--sig);box-shadow:0 0 0 3px var(--sig-ring)}
.btn.primary{border-color:var(--sig);background:var(--sig);color:var(--bar-ink);font-weight:600}
.qform{display:flex;flex-wrap:wrap;align-items:center;gap:12px;margin-top:16px;
  padding-top:14px;border-top:1px solid var(--hair)}
.qform label{font-size:var(--fs-12-5);color:var(--ink2)}
.qform input,.qform textarea{padding:0 10px;height:34px;border-radius:8px;
  border:1px solid var(--hair2);background:var(--field);color:var(--ink);
  font:inherit;font-size:var(--fs-13)}
.qform input{width:190px}
.qform input.tiny{width:86px;text-align:center}
.qpre{flex:1 1 100%;font-size:var(--fs-12-5);color:var(--ink2);background:var(--pre-bg);
  border-left:2px solid var(--sig);border-radius:0 8px 8px 0;padding:8px 12px;margin-bottom:2px}
.qpre b{color:var(--ink);font-weight:600}
.qform textarea{flex:1 1 320px;height:auto;min-height:58px;padding:7px 10px;resize:vertical;
  font-size:var(--fs-12-5);line-height:1.6}
.qnote{flex:1 1 100%;font-size:var(--fs-12);color:var(--muted)}
@media (max-width:820px){ .qbtns{grid-template-columns:1fr} }

/* 邀请卡片：房主唯一需要转发的一段话 */
.invite{margin-top:16px;border-top:1px solid var(--hair);padding-top:12px}
.invhead{display:flex;align-items:center;gap:12px;margin-bottom:8px}
.invhead span{flex:1;font-size:var(--fs-12-5);color:var(--ink2)}
.invtext{font-family:Consolas,"Cascadia Mono",monospace;font-size:var(--fs-12-5);line-height:1.7;
  white-space:pre-wrap;word-break:break-all;background:var(--field);
  border:1px solid var(--hair);border-radius:8px;padding:10px 12px;
  color:var(--ink2);max-height:280px;overflow:auto}

/* 「更多功能」：操作列表收起后留在左栏的入口 */
.morebtn{display:block;width:100%;text-align:left;margin-top:8px}

/* 提示条：一句结论 + 一个动作。目前只用于"入站未放行"这一件事。
   Windows 防火墙默认拦掉所有入站，这是"我这边正常、玩家就是连不上"的头号原因，
   所以提示放在最上面；但要不要放行由用户点，界面不自动改系统。 */
.banner{display:flex;align-items:center;gap:var(--s3);padding:11px 14px;
  border:1px solid var(--amber-edge);border-radius:var(--r2);background:var(--amber-soft);
  color:var(--amber-ink);font-size:var(--fs-12-5)}
.banner .bi{width:6px;height:6px;border-radius:50%;background:var(--amber);flex:0 0 auto}
.banner .bt{flex:1;min-width:0}
.banner .btn.amber{color:var(--amber-on);background:var(--amber);border-color:transparent}
.banner .btn.amber:hover:not(:disabled){background:var(--amber);color:var(--amber-on);filter:brightness(1.06)}

/* 日志：两层各一个容器，切换靠显示/隐藏，不重建节点 */
.logcard{flex:1;min-height:140px;display:flex;flex-direction:column;padding:var(--s3) var(--s4) var(--s4)}
.loghead{display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap;margin-bottom:var(--s2)}
.loghead .k{margin:0}
.segs{display:flex;border:1px solid var(--line);border-radius:var(--r1);overflow:hidden}
.seg{padding:4px 10px;font:var(--fs-11-5)/1.5 var(--mono);color:var(--muted);transition:color .16s,background .16s}
.seg+.seg{border-left:1px solid var(--line)}
.seg:hover{color:var(--ink2);background:var(--surf2)}
.seg.on{color:var(--surf);background:var(--ink2)}
.loghead .r{margin-left:auto;display:flex;align-items:center;gap:var(--s2)}
.linecnt{font:var(--fs-11)/1 var(--mono);color:var(--muted2)}
.log{flex:1;min-height:0;overflow:auto;padding:var(--s3) 0;
  background:var(--panel);border:1px solid var(--line);border-radius:var(--r2)}
.ln{display:flex;gap:10px;font:var(--fs-12-5)/1.66 var(--mono);padding:0 12px;white-space:pre-wrap;word-break:break-word}
/* 行号：用最低那一级灰阶，但**不再叠 opacity** —— 原来 .6 的透明度把它压到对比度 1.4，
   等于看不见。层次交给字号与位置，不交给"让人读不到"。 */
.ln .no{flex:0 0 auto;width:34px;text-align:right;color:var(--muted2);-webkit-user-select:none;user-select:none}
.ln .tx{flex:1;min-width:0}
.ln.dim .tx{color:var(--muted)}
.ln.ok .tx{color:var(--ok-ink)}
.ln.bad .tx{color:var(--bad-ink)}
.ln.warn .tx{color:var(--warn-ink)}
.ln.info .tx{color:var(--info-ink)}
.lgempty{padding:30px 16px;text-align:center;color:var(--muted2);font:var(--fs-12)/1.7 var(--mono)}

/* 详情面板：原理性内容放这里，不铺在主界面 */
.modal{position:fixed;inset:0;z-index:40;display:flex;align-items:center;justify-content:center;
  background:var(--panel)}
/* 面板宽度按设计文档取 760：620 放下表单够用，但要再塞进一列 172 的模块导航就不够了 */
.mbox{width:min(760px,92vw);max-height:76vh;display:flex;flex-direction:column;
  border:1px solid var(--line2);border-radius:var(--r3);background:var(--bg2)}
.mhead{display:flex;align-items:center;gap:var(--s3);padding:var(--s4) var(--s5);
  border-bottom:1px solid var(--line)}
.mhead .mt{font:600 var(--fs-15)/1.3 var(--disp)}
.mbody{padding:var(--s4) var(--s5);overflow:auto;font-size:var(--fs-13);line-height:1.75;color:var(--ink2);
  white-space:pre-wrap}

/* 设置面板。它复用「详情」那扇门（#modal）—— 同样是"点开看完就关掉"的一块内容，
   再开一扇门只会让人多记一个位置。唯一的差别是里面是表单，所以把 pre-wrap 关掉。 */
.mset{white-space:normal}
.mgrp{display:flex;align-items:center;gap:var(--s2);margin:20px 0 8px;
  font:var(--fs-11)/1 var(--mono);letter-spacing:.12em;color:var(--muted)}
.mgrp:first-child{margin-top:0}
.mgrp::after{content:"";flex:1;height:1px;background:var(--line)}
.mrow{display:flex;align-items:center;gap:var(--s3);padding:6px 0}
.mrow>.ml{flex:0 0 118px;font-size:var(--fs-12-5);color:var(--ink2)}
.mrow>.md{flex:1;min-width:0;display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap}
.mhint{flex:1 1 100%;font-size:var(--fs-11-5);color:var(--muted);line-height:1.55}

/* ---- 设置面板的骨架：左模块导航 + 右内容 ----
   结构按设计文档第一部分来。顶部那条搜索与视图开关放在**右栏内部**而不是整宽横条，
   这样它与左导航能共用一个 sticky 的 top 值，彼此不必依赖对方的高度。
   左导航按模块列出「可操作 / 计划」的比值，一眼能看出哪些模块现在有东西。 */
.mset{white-space:normal;display:flex;align-items:flex-start;gap:var(--s4)}
.mset.simple .mmeta{display:none}
.mnav{flex:0 0 172px;position:sticky;top:calc(-1 * var(--s4));align-self:flex-start;
  z-index:1;display:flex;flex-direction:column;gap:2px;padding:var(--s4) var(--s2) var(--s4) 0;
  background:var(--bg2)}
.mnavi{display:flex;align-items:baseline;gap:6px;padding:5px 8px;border-radius:var(--r1);
  text-align:left;color:var(--muted);font-size:var(--fs-11-5);line-height:1.35}
.mnavi:hover{color:var(--ink2);background:var(--surf2)}
.mnavi.on{color:var(--on-sig);background:var(--sig)}
.mnavt{flex:1;min-width:0}
.mnavc{flex:0 0 auto;font:var(--fs-11)/1 var(--mono);opacity:.75}
.mcontent{flex:1;min-width:0}
.mtop{position:sticky;top:calc(-1 * var(--s4));z-index:2;background:var(--bg2);
  padding:var(--s4) 0 var(--s2);margin-bottom:var(--s2);border-bottom:1px solid var(--line)}
.mtop .msearch{display:flex;align-items:center;gap:var(--s2)}
.mtop input{flex:1;min-width:0;height:30px;padding:0 10px;border-radius:8px;
  border:1px solid var(--hair2);background:var(--field);color:var(--ink);font-size:var(--fs-12-5)}
.mtop input:focus{outline:none;border-color:var(--sig-focus);box-shadow:0 0 0 3px var(--sig-soft)}
.msvc{display:flex;align-items:center;gap:var(--s2);margin-top:8px;font-size:var(--fs-11-5);color:var(--muted)}
.mmodh{display:flex;align-items:baseline;gap:var(--s3);flex-wrap:wrap;margin:var(--s4) 0 var(--s2)}
.mmt{font-size:var(--fs-14);font-weight:600;color:var(--ink)}
.mmc{flex:1;min-width:0;font-size:var(--fs-11-5);color:var(--muted)}
.mrow.sv{align-items:flex-start}
.mrow.sv>.ml{flex:0 0 152px;padding-top:5px}
.mrow.sv .mdesc{flex:1 1 100%;font-size:var(--fs-11-5);color:var(--muted);line-height:1.55}
.mrow.sv.off>.ml,.mrow.sv.off .mdesc,.mrow.sv.off .mmeta{color:var(--muted2)}
.mrow.sv.off{opacity:.74}
.mrow.hl{background:var(--sig-soft);border-radius:var(--r1)}
.mchip{display:inline-block;margin-left:6px;padding:0 6px;border-radius:var(--pill);
  font-size:var(--fs-11);line-height:1.5;border:1px solid var(--line2);color:var(--muted);white-space:nowrap}
.mchip-live{border-color:var(--sig-edge);background:var(--sig);color:var(--on-sig)}
.mchip-plan{background:var(--surf2);color:var(--ink2)}
.mchip-src{border-style:dashed}
.mmeta{flex:1 1 100%;font-size:var(--fs-11);color:var(--muted2);line-height:1.5}
.mhits{display:flex;flex-direction:column;gap:1px;margin:var(--s2) 0}
.mhit{display:flex;align-items:baseline;gap:8px;padding:4px 8px;border-radius:var(--r1);
  text-align:left;font-size:var(--fs-12);color:var(--ink2)}
.mhit:hover{background:var(--surf2)}
.mhit b{font-weight:500;color:var(--ink)}
.mhit span{margin-left:auto;flex:0 0 auto;font-size:var(--fs-11);color:var(--muted)}
.mbar{margin:var(--s2) 0 var(--s3);padding:8px 12px;border-radius:var(--r1);
  border:1px solid var(--line2);background:var(--surf2);font-size:var(--fs-11-5);color:var(--ink2)}
.mchg{display:flex;flex-direction:column;margin:var(--s2) 0}
.mchgr{display:flex;align-items:baseline;gap:8px;padding:5px 0;
  border-bottom:1px solid var(--hair);font-size:var(--fs-11-5);color:var(--ink2)}
.mchgr .t{flex:0 0 118px;font-family:var(--mono);color:var(--muted)}
.mchgr .m{flex:0 0 112px;color:var(--muted)}
.mchgr .i{flex:1;min-width:0}
.mchgr .v{flex:0 0 auto;font-family:var(--mono);color:var(--muted)}
@media (max-width:820px){
  .mset{display:block}
  .mnav{position:static;flex-direction:row;overflow:auto;gap:4px;padding:0 0 var(--s2)}
  .mnavi{white-space:nowrap}
  .mnavt{flex:0 0 auto}
  .mtop{position:static;padding-top:0}
  .mrow.sv>.ml{flex:0 0 118px}
}
.mseg{display:flex;border:1px solid var(--line);border-radius:var(--r1);overflow:hidden}
.mseg button{padding:5px 12px;font-size:var(--fs-12);color:var(--muted);transition:color .16s,background .16s}
.mseg button+button{border-left:1px solid var(--line)}
.mseg button:hover{color:var(--ink2);background:var(--surf2)}
.mseg button.on{color:var(--on-sig);background:var(--sig)}
.msw{display:flex;align-items:center;gap:8px;font-size:var(--fs-12-5);color:var(--ink2);cursor:pointer}
.msw input{width:15px;height:15px;accent-color:var(--sig);cursor:pointer}
.mtxt{width:100%;max-width:330px;padding:0 10px;height:32px;border-radius:8px;
  border:1px solid var(--hair2);background:var(--field);color:var(--ink);font-size:var(--fs-12-5)}
.mtxt:focus{outline:none;border-color:var(--sig-focus);box-shadow:0 0 0 3px var(--sig-soft)}
.mtxt.small{max-width:120px}
/* 路径输入框要占满整行：完整路径本来就长，挤在 330px 里没法核对填错在哪一段 */
.mtxt.path{max-width:100%;font-family:var(--mono);font-size:var(--fs-11-5)}
.mdot{width:22px;height:22px;border-radius:50%;border:1px solid var(--line2);cursor:pointer;padding:0}
.mdot.on{box-shadow:0 0 0 2px var(--surf),0 0 0 4px var(--sig-edge)}
.mfoot{display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap;margin-top:18px;
  padding-top:14px;border-top:1px solid var(--line)}
.mfoot .mnote{flex:1 1 100%;font-size:var(--fs-11-5);color:var(--muted);line-height:1.6}
.msaved{font-size:var(--fs-11-5);color:var(--sig)}

/* 底栏 */
.foot{display:flex;align-items:center;gap:var(--s3);padding:0 20px;border-top:1px solid var(--line);
  background:var(--chrome);font:var(--fs-11)/1 var(--mono);color:var(--muted2);white-space:nowrap;overflow:hidden}
.foot>span{min-width:0;overflow:hidden;text-overflow:ellipsis}
.foot .cmdline{color:var(--muted);max-width:34vw}
.foot .gap{flex:1;overflow:visible}
/* 底栏的背景音乐入口：只显示当前曲名，**不做任何动画** —— 常驻区域对动画最不宽容，
   而且它每 700ms 会跟着状态刷新一次，动画会让这条底线变得很难守。 */
#bgmName{display:inline-block;max-width:22vw;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;vertical-align:bottom}

.toast{position:fixed;left:50%;bottom:44px;transform:translateX(-50%);z-index:50;
  padding:9px 17px;border-radius:var(--pill);border:1px solid var(--sig-edge);
  background:var(--toast-ok-bg);color:var(--toast-ok-ink);font-size:var(--fs-12-5);max-width:70vw}
.toast.bad{border-color:var(--toast-bad-edge);background:var(--toast-bad-bg);color:var(--toast-bad-ink)}
@media (prefers-reduced-motion:reduce){
  .dot.run::after{animation:none;opacity:.45}
  *{transition:none !important}
  :root{--vfx-fade:0s}
}
@media (max-width:960px){main{grid-template-columns:1fr}.rail{max-height:340px}}
/* 窗口被拖窄时，最先让位的是那条进度链：它是"现在走到哪一段"的提示，
   而同样的信息在状态胶囊里也有一份。不让位的话，右上角那几个按钮会被挤出去。 */
@media (max-width:1040px){.linkbar{display:none}}
</style>
</head>
<body>
<div class="bg"><div class="wall"></div><div class="scrim"></div><div class="aurora"></div><div class="grid"></div></div>

<div class="app" id="app">
  <header class="top">
    <span class="glyph"></span>
    <div class="brand"><b>mclbx</b><span class="sub">联机工具</span><span class="ver" id="ver"></span></div>
    <div class="spacer"></div>
    <div class="linkbar" id="linkbar" title="当前连接进展">
      <span class="n on">本机</span><span class="s on"></span>
      <span class="n" id="lnkEntry">入口</span><span class="s" id="lnkS2"></span>
      <span class="n" id="lnkRoom">房间</span><span class="s" id="lnkS3"></span>
      <span class="n" id="lnkPeer">玩家</span>
    </div>
    <div class="stat"><span class="dot" id="pulse"></span><span id="status">空闲</span></div>
    <button class="btn sm" id="btnSettings" title="外观、默认值与数据">设置</button>
    <button class="btn" id="btnManual">说明书</button>
    <button class="btn danger" id="btnStop" disabled>停止</button>
    <button class="btn" id="btnQuit">退出</button>
  </header>
  <div class="notice" id="notice"><i></i><span id="noticeText"></span></div>

  <!-- 说明书覆盖层：内容由 /manual 提供，与程序同在一个 exe 内。
       首次点开才设置 iframe 的地址 —— 不打开就不加载，空闲时不占资源。 -->
  <div class="manual" id="manual" hidden>
    <div class="manual-h">
      <span>使用说明书</span>
      <button class="btn sm" id="manualClose">关闭</button>
    </div>
    <iframe id="manualFrame" title="使用说明书"></iframe>
  </div>

  <main>
    <aside class="rail">
      <div class="cap" id="cap"></div>
      <!-- 操作列表默认收起。分享出去之后反馈最多的一句话是"不知道点哪个" ——
           所以默认只留环境结论，十来个低频操作收进这里，一个都没删。 -->
      <button class="linkbtn morebtn" id="btnMore" hidden>更多功能</button>
      <div id="railmore" hidden>
        <div class="rowline">
          <div class="search"><span class="ic"></span><input id="q" placeholder="搜索操作"></div>
          <button class="btn sm" id="btnProbe" title="重新执行环境检测">重新检测</button>
        </div>
        <div class="rowline">
          <span class="railcount" id="cnt"></span>
          <div class="spacer"></div>
          <span class="railcount">Esc 取消选择</span>
        </div>
        <div class="tasks" id="tasks"></div>
      </div>
    </aside>

    <section class="col">
      <!-- 一步开局：默认入口。房主要给的就一个房间码，玩家要做的就一次粘贴；
           其余参数与说明都后置到「更多功能」和悬浮提示里。 -->
      <section class="card quick" id="quick">
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
          <!-- 这条前置说明是实测补出来的：新手点"开一个房间"时，游戏里的世界往往还没开，
               程序只能回一句"端口无法连接"，看着像工具坏了。 -->
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
      <div class="card hero" id="cardAddr" hidden>
        <span class="shine" id="sheen"></span>
        <div class="k">玩家入口地址</div>
        <div class="plate"><div class="addr" id="addr">-</div></div>
        <div class="rack">
          <button class="btn sm" id="btnCopyAddr">复制地址</button>
          <span class="chip grp" id="roomChip" hidden></span>
        </div>
        <!-- 邀请卡片：房主唯一需要"转发出去"的东西 —— 地址、房间码、没装工具的连法、
             装了工具的命令，全在这一段里。以前这里是三块（地址、命令、步骤），
             每块配一个复制按钮，分享之后被问得最多的一句是"我该复制哪个"。
             现在只留这一段、只留一个复制按钮，整段发出去就完事。 -->
        <div class="invite" id="inviteWrap" hidden>
          <div class="invhead">
            <span>发送给玩家的邀请</span>
            <button class="btn sm" id="btnCopyInvite">复制邀请</button>
          </div>
          <div class="invtext" id="inviteText"></div>
        </div>
        <!-- 在场名单：只在有人真的通过身份校验之后出现（光连上不算）。
             安全码是要和玩家对着念的那一串，所以放在最显眼的位置。 -->
        <div class="guests" id="guestWrap" hidden>
          <div class="ghead">在场玩家 <span id="guestN">0</span> 人</div>
          <ul id="guestList"></ul>
        </div>
      </div>

      <div class="card" id="workCard"></div>

      <div class="card logcard">
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
        <div class="log" id="logUser"></div>
        <div class="log" id="logRaw" hidden></div>
      </div>
    </section>
  </main>

  <footer class="foot">
    <span id="ftTask">未选择操作</span>
    <span class="cmdline" id="ftCmd"></span>
    <span class="gap"></span>
    <span class="bgm"><button class="btn sm" id="bgmToggle" title="背景音乐：点一下播放或暂停">♪ <span id="bgmName">未播放</span></button></span>
    <span class="gap"></span>
    <span>Enter 执行 · Esc 取消</span>
    <span class="gap"></span>
    <span id="ftVer"></span>
  </footer>
</div>

<!-- 背景音乐：播放完全交给界面内核的 <audio>，Go 侧只负责列出曲库与提供文件。
     preload=none 是有意的：不点播放就不去碰磁盘，空闲时这条路径的开销是零。 -->
<audio id="bgm" preload="none"></audio>

<div class="modal" id="modal" hidden>
  <div class="mbox">
    <div class="mhead"><span class="mt" id="modalTitle"></span><span class="spacer"></span>
      <button class="btn sm" id="modalClose">关闭</button></div>
    <div class="mbody" id="modalBody"></div>
  </div>
</div>

<div class="toast" id="toast" hidden></div>

<script>
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
     · 首屏        VFX.init(cfg, 保存回调)
   每个入口都兜了 try，所以这一段自身出问题也影响不到工具本身。

   三件事在这里：
     · apply()  写属性；浏览器不支持 backdrop-filter 时自动退成"关闭模糊"，不报错也不破版
     · busy()   任务执行期间摘掉毛玻璃（见 CSS 里那一段的说明）
     · watch()  帧率持续过低时问用户要不要降档，用户也可以选择维持高特效
   另有一个 VFX.measure()，把帧率量出来交给调用方 —— 四档对比测试就是靠它（见文件顶部说明）。 */
var VFX = (function(){
  var st = { vfx:'on', glass:'mid', noDegrade:false, level:'off' };
  var hook = null, timer = null, bursts = null, low = 0, asked = false, probe = null;

  function root(){ return document.documentElement; }
  /* 浏览器到底认不认 backdrop-filter。不认就退成"关闭模糊" —— 半透明底还在，版式不变。 */
  function supported(){
    try{
      return !!(window.CSS && CSS.supports && CSS.supports('backdrop-filter','blur(4px)'));
    }catch(e){ return false; }
  }
  /* 地址栏上的 ?vfx=off|low|mid|high：只为"四档各测一遍"用，**不落盘**。
     同一份界面不用改设置就能逐档对比，测完刷新即恢复用户自己的设置。 */
  function forced(){
    try{
      var m = /[?&]vfx=(off|low|mid|high)(?:&|$)/.exec(location.search || '');
      return m ? m[1] : '';
    }catch(e){ return ''; }
  }
  function busy(on){ try{ root().setAttribute('data-vfx-busy', on ? 'on' : 'off'); }catch(e){} }

  function apply(u){
    try{
      u = u || {};
      if(u.vfx){ st.vfx = u.vfx === 'off' ? 'off' : 'on'; }
      if(u.glass){ st.glass = u.glass; }
      if(typeof u.noDegrade === 'boolean'){ st.noDegrade = u.noDegrade; }

      var lv = st.vfx === 'off' ? 'off' : st.glass;
      if(lv !== 'off' && !supported()){ lv = 'off'; }
      var f = forced();
      if(f){ lv = f; }
      st.level = lv;

      var e = root();
      e.setAttribute('data-vfx', st.vfx);
      e.setAttribute('data-glass', lv);
      e.setAttribute('data-glow', u.glow === 'off' ? 'off' : 'on');
      e.setAttribute('data-fade', u.fade === 'off' ? 'off' : 'on');
    }catch(err){ /* 美化失败不该影响工具 */ }
    watch();
  }
  function init(cfg, saveHook){
    try{
      hook = saveHook || null;
      if(cfg){ apply(cfg); }
    }catch(e){}
  }

  /* ---- 帧率巡检 ----
     只在"真的可能有问题"时跑：总开关关着、档位是 off、用户已经选了维持高特效、
     页面在后台 —— 这四种情况一律不测，连定时器都不留。
     测法是**有界的一小段**：连抓约一秒的帧间隔就停，不做常驻 rAF 循环。
     它只在空闲时测：任务执行期间毛玻璃本来就让位了，那时候的帧率代表不了玻璃的开销。 */
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
  function stopWatch(){
    if(timer){ clearInterval(timer); timer = null; }
    bursts = null;
  }
  function watch(){
    try{
      stopWatch();
      if(st.vfx !== 'on' || st.level === 'off' || st.noDegrade || forced()){ return; }
      // 20 秒一次，每次只抓约一秒。空闲时的这点开销远小于一次 tick。
      timer = setInterval(function(){
        if(document.hidden || asked){ return; }
        if(bursts){ return; }                 // 上一次还没回来就不叠加
        bursts = sample(1000).then(function(r){
          bursts = null;
          probe = r;
          if(r.fps < 45){ low++; } else { low = 0; }
          if(low >= 2){ ask(); }               // 连续两次偏低才开口，避免一次抖动就打扰
        }).catch(function(){ bursts = null; });
      }, 20000);
    }catch(e){ stopWatch(); }
  }
  /* 问一次。属于美化这一层：关掉总开关时它跟着消失，业务侧一行都不用改。 */
  function ask(){
    if(asked){ return; }
    asked = true;
    stopWatch();
    var d = document.createElement('div');
    d.className = 'vfxask';
    d.innerHTML = '<span>界面帧率偏低，可能是毛玻璃的开销。要自动降到「低」档吗？</span>' +
      '<button type="button" data-vfx-act="low">降到低档</button>' +
      '<button type="button" data-vfx-act="keep">保持现状</button>';
    d.onclick = function(ev){
      var b = ev.target && ev.target.closest ? ev.target.closest('button') : null;
      if(!b){ return; }
      var a = b.getAttribute('data-vfx-act');
      if(d.parentNode){ d.parentNode.removeChild(d); }
      if(!hook){ return; }
      // 「保持现状」= 以后不再问（存进设置，换台机器打开也还是不再问）
      hook(a === 'low' ? { glass:'low' } : { noDegrade:true });
    };
    document.body.appendChild(d);
  }
  return {
    apply: apply,
    init: init,
    busy: busy,
    watch: watch,
    // 量一段帧率交给调用方，同时记一份供面板/控制台查看
    measure: function(ms){ return sample(ms || 1500).then(function(r){ probe = r; return r; }); },
    stats: function(){ return { level:st.level, vfx:st.vfx, glass:st.glass,
      noDegrade:st.noDegrade, supported:supported(), last:probe }; }
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
    html += '<div class="grp g' + ((gi % 6) + 1) + '" data-g="' + esc(g) + '">'
          +   '<span class="tag"></span><span class="t">' + esc(g) + '</span><span class="rule"></span></div>';
    byGroup[g].forEach(function(t){
      var l = lim[t.key] || null;
      var attrs = ' data-key="' + esc(t.key) + '"';
      if(l && l.hard){ attrs += ' data-off="1" title="不可用：' + esc(l.why) + '"'; }
      html += '<button class="t' + (l && l.hard ? ' off' : '') + '"' + attrs + '>'
            +   '<div class="nm">' + esc(t.name)
            +     (t['long'] ? '<span class="lg">长时</span>' : '')
            +     (l && !l.hard ? '<span class="lim" title="' + esc(l.why) + '">注意</span>' : '')
            +   '</div>'
            +   '<div class="ds">' + esc(t.desc) + '</div>'
            + '</button>';
    });
  });
  var box = $('tasks');
  box.innerHTML = html || '<div class="tempty">没有可用的操作</div>';
  Array.prototype.forEach.call(box.querySelectorAll('.t'), function(b){
    b.onclick = function(){
      if(b.getAttribute('data-off')){
        toast(b.getAttribute('title').replace(/^不可用：/, ''), true);
        return;
      }
      pick(b.getAttribute('data-key'));
    };
  });
  // 操作列表默认收起，这里顺手把入口按钮上的项数补上
  setText($('btnMore'), '更多功能 · ' + tasks.length + ' 项');
  setHidden($('btnMore'), false);
  filterTasks();
}
function filterTasks(){
  var q = ($('q').value || '').trim().toLowerCase();
  var box = $('tasks'), shown = 0;
  Array.prototype.forEach.call(box.children, function(grp){
    if(!grp.classList || !grp.classList.contains('grp')){ return; }
    var g = (grp.getAttribute('data-g') || '').toLowerCase();
    var n = 0, hitGroup = !q || g.indexOf(q) >= 0;
    var next = grp.nextElementSibling;
    while(next && next.classList && next.classList.contains('t')){
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
  Array.prototype.forEach.call($('tasks').querySelectorAll('.t'), function(b){
    b.classList.toggle('on', !!sel && b.getAttribute('data-key') === sel.key);
  });
  renderWork();
}
function clearSel(){
  sel = null;
  Array.prototype.forEach.call($('tasks').querySelectorAll('.t'), function(b){ b.classList.remove('on'); });
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
function renderIdle(){
  var w = $('workCard');
  w.className = 'card';
  var n = tasks.length ? ('其余 ' + tasks.length + ' 项操作位于') : '其余操作位于';
  setHTML(w, '<div class="idle">请选择上方的一项操作。' + esc(n)
    + '左侧「<b>更多功能</b>」中。</div>');
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
  w.className = 'card formcard';
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
  setHidden($('modal'), false);
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
function syncFoot(){
  setText($('ftTask'), sel ? ('操作：' + sel.name) : '未选择操作');
  // 页脚不挂命令行：它是给写脚本的人看的，普通使用者只需要知道执行结果；
  // 需要核对时，表单右上角的「命令」可展开显示等价命令行。
  setHidden($('ftCmd'), true);
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
  if(s.ver){ setText($('ver'), s.ver); }
  if(!$('ftVer').textContent && s.ver){ setText($('ftVer'), s.ver + ' · 网页版'); }
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
    s.classList.toggle('on', s.getAttribute('data-f') === layer);
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
function toggleMore(force){
  var open = (typeof force === 'boolean') ? force : $('railmore').hidden;
  setHidden($('railmore'), !open);
  setText($('btnMore'), open ? '收起更多功能' : ('更多功能 · ' + tasks.length + ' 项操作'));
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
$('btnMore').onclick = function(){ toggleMore(); };
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
    toast('无法识别邀请内容，可在「更多功能 → 加入房间」中手动填写地址', true);
    return;
  }
  var ok = await startWith('join', { host: p.host, room: p.room });
  if(ok){
    setText($('qGuestNote'), '正在连接 ' + (p.host || '') + (p.room ? ('（房间码 ' + p.room + '）') : ''));
  }
};
$('btnManual').onclick = function(){ showManual(true); };
$('manualClose').onclick = function(){ showManual(false); };
/* 当前**解析后**的主题：documentElement 上的那个值就是它 —— "跟随系统"在首屏脚本里
   已经按系统偏好写成了 light 或 dark。 */
function resolvedTheme(){
  return document.documentElement.getAttribute('data-theme') || 'dark';
}
function showManual(on){
  var m = $('manual');
  if(!m){ return; }
  if(on){
    var f = $('manualFrame');
    // 说明书是另一个文档（自带一套变量），主题得从查询串带过去：不带的话，
    // 深色主题下点开会得到一整页白 —— 暗环境里那一下很刺眼。
    if(f && !f.getAttribute('src')){ f.setAttribute('src', '/manual?theme=' + encodeURIComponent(resolvedTheme())); }
  }
  setHidden(m, !on);
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
var uiSaveTimer = null;

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
/* 背景图与毛玻璃那两行的说明文字。
   抽成函数是因为它们会随**另一个**设置项变（填没填背景图），保存之后必须就地重刷：
   只渲染一次的话，用户填了个读不出来的路径，面板上还挂着上次打开面板时的旧文案。 */
function wallHintText(u){
  if(u.wallReason){ return '这张图用不了：' + uiEsc(u.wallReason); }
  return '点「导入图片…」把一张图复制进存档（JPEG / PNG / GIF，单张上限 32MB），也可以直接把图丢进存档目录里的 wallpapers 文件夹再点刷新。导入之后原文件改名、移走、删掉都不影响，整个存档文件夹拷到别的机器上背景图也还在。图片会先缩到长边 2560，所以放 4K 图也不会拖慢界面。建议用不透明的图：带透明区域的 PNG 转成 JPEG 之后透明部分会发黑。';
}
function glassHintText(u){
  if(u.bgImage){
    return '已经填了背景图片，毛玻璃会让位、面板保持实心：半透明面板压在一张任意照片上时，第三、四级灰会掉到 4.5 的对比度门槛以下（实测最差 3.69）。这两个功能只能取一个。';
  }
  if(u.vfx === 'off'){ return '总开关关着，这一档暂时不生效；重新打开「视觉美化」后回到你选的这一档。'; }
  var lv = u.glass || 'off';
  if(lv === 'off'){
    return '只留半透明底色，不做实时模糊。面板背后是极光与网格这类低频内容，所以观感与开模糊差别很小 —— 这是最省的一档。';
  }
  var cost = lv === 'high'
    ? '高模糊（14px）是画质档：会明显增加显卡与浏览器的开销，低配设备可能掉帧。建议先用下面「帧率实测」量一下再留在这一档。'
    : '任务执行期间会自动让位（日志在流、进度点在转时不做模糊），跑完立刻恢复。';
  return '面板半透明，并实时模糊背后的内容。模糊只加在局部面板上，不会铺到整页。' + cost;
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
  return '<select class="mtxt" data-in="bgImage">' + opts + '</select> ' +
    '<input type="file" id="wallFile" accept="image/*" style="display:none">' +
    '<button type="button" class="btn sm" data-act="wallImport">导入图片…</button> ' +
    '<button type="button" class="btn sm" data-act="wallFolder">打开存档目录</button> ' +
    '<button type="button" class="btn sm" data-act="wallRefresh">刷新</button>';
}
/* 曲库那一行。曲库的真相在磁盘上，所以这里只报数量 + 给「打开文件夹」「刷新」两个动作，
   不做内嵌的播放列表管理 —— 用户用资源管理器管理自己的文件，比在设置里做一套增删更省心。 */
function uiMusicList(u){
  var list = u.musicList || [];
  var n = list.length;
  var txt = n ? ('共 ' + n + ' 首') : '（还没有音乐）';
  return '<span class="mnote">' + txt + '</span> ' +
    '<button type="button" class="btn sm" data-act="musicFolder">打开文件夹</button> ' +
    '<button type="button" class="btn sm" data-act="musicRefresh">刷新</button>';
}
function musicHintText(u){
  if(!(u.musicList || []).length){
    return '把音乐文件（MP3 / WAV / FLAC / M4A / OGG）放进存档目录的 music 文件夹，再点「刷新」。只把真正的音频算进曲库：改了后缀的其它文件不会出现在这里。';
  }
  return '曲库就是存档里的 music 文件夹，你可以自己往里放、改名、删除，点「刷新」后生效。播放时不另占 CPU —— 音频由界面内核直接解码；关掉窗口就停。某个文件放不了会被标出来，不会静默跳过。';
}
/* ==================== 设置面板 ====================
   面板的结构与条目来自服务端下发的 spec（14 个模块 / 287 条，见 uispec_gen.go），
   每条带一个分类标记；uispec_impl.go 里那张对照表决定它可不可操作。
   这么做的直接好处：以后每落地一批功能，只在服务端那张表里加几行，界面不用改 ——
   也就不可能出现「面板上写着有、点下去没反应」这种自相矛盾。

   两档视图：
     简洁视图   只列当前可操作的条目，日常用这一档
     完整参考   把规划中与仅作参考的条目一并列出并置灰，用来对照行业做法
   搜索跨模块检索，命中列表里标出所属模块，点一下跳过去并高亮。 */

var uiMod = '';        // 当前模块名
var uiQuery = '';      // 搜索词
var uiFull = false;    // false = 简洁视图；true = 完整参考视图
var uiView = 'main';   // main / changes
var uiHits = [];       // 当前搜索结果，下标对应 data-hit
var uiHl = '';         // 跳转后需要短暂高亮的条目名
var uiHlTimer = null;

/* 控件表：key → 怎么渲染。
   选项文案集中在这里，而不是散在几十行字符串拼接里 ——
   面板现在是按清单铺出来的，控件必须能按 key 查到才与清单对得上。 */
var UI_CTL = {
  theme:{opts:uiThemes()},
  accent:{dots:true},
  flat:{opts:[['off','立体'],['on','扁平']]},
  glass:{opts:[['off','关闭'],['low','低'],['mid','中'],['high','高']]},
  scale:{opts:[['std','标准'],['big','大']]},
  motion:{opts:[['full','完整'],['lite','精简']]},
  musicMode:{opts:[['order','顺序'],['shuffle','随机']]},
  musicLoop:{opts:[['off','不循环'],['all','列表循环'],['one','单曲循环']]},
  vfx:{label:'科幻外观总开关', on:function(u){ return u.vfx !== 'off'; }},
  glow:{label:'面板描边与四角微光', on:function(u){ return u.glow !== 'off'; }},
  fade:{label:'设置面板出现时淡入', on:function(u){ return u.fade !== 'off'; }},
  backdrop:{label:'显示背景渐变与网格', on:function(u){ return u.backdrop !== 'off'; }},
  remember:{label:'记住上次填过的值', on:function(u){ return u.remember !== '0'; }},
  noDegrade:{label:'帧率过低时询问我', on:function(u){ return !u.noDegrade; }},
  autoProbe:{label:'自动检测本机环境', on:function(u){ return !!u.autoProbe; }},
  musicVol:{cls:'small', ph:'70'},
  logKeep:{cls:'small', ph:'2000', suf:'行'},
  defPort:{cls:'small', ph:'25565'},
  defRelay:{cls:'', ph:'turn:主机:3478 或 mclbx://…'}
};
/* 动作表：key → 按钮上的字。act/chrome 两类条目走这里。 */
var UI_ACT = {
  vfxMeasure:{act:'vfxMeasure', label:'在当前档位测一秒',
    after:'<span class="mnote" id="vfxStats">尚未测过</span>'},
  forgetInputs:{act:'forgetInputs', label:'清除记住的填写内容'},
  dataDir:{act:'openData', label:'打开存档目录'},
  exportCfg:{act:'exportCfg', label:'导出全部配置'},
  importCfg:{act:'importCfg', label:'导入配置…'},
  version:{act:'copyVersion', label:'复制版本信息'},
  manual:{act:'manual', label:'打开说明书'},
  diag:{act:'diag', label:'导出诊断包'}
};
/* 说明文字：能用运行时的就说运行时的（例如背景图那一行要讲清缩放与导入），
   其余用设计清单里的原文。 */
var UI_HINT = { bgImage:wallHintText, glass:glassHintText, musicList:musicHintText };

function uiSpecGroups(){ return (ui && ui.spec) || []; }
function uiShortName(n){ return String(n || '').split('（')[0]; }
function uiGroupByName(n){
  var g = uiSpecGroups();
  for(var i = 0; i < g.length; i++){ if(g[i].name === n){ return g[i]; } }
  return null;
}
function uiAccentDots(u){
  return uiAccents().map(function(a){
    return '<button type="button" class="mdot' + (u.accent === a[0] ? ' on' : '') +
      '" data-accent="' + a[0] + '" title="' + a[1] + '" aria-label="强调色 ' + a[1] +
      '" style="background:' + a[2] + '"></button>';
  }).join('');
}
/* 按条目渲染它的控件。ctl 为空说明这条当前不可操作，交给调用方置灰。 */
function uiCtl(item, u){
  var key = item.key, c = UI_CTL[key] || {};
  switch(item.ctl){
    case 'dots':  return uiAccentDots(u);
    case 'seg':   return uiSeg(key, u[key], c.opts || []);
    case 'sw':    return uiSw(key, c.on ? c.on(u) : false, c.label || '');
    case 'wall':  return uiWallPicker(u);
    case 'music': return uiMusicList(u);
    case 'field': return uiField(key, c.cls || 'small', u[key], c.ph || '', c.suf || '');
    case 'act':
      if(key === 'bgmCtl'){
        return '<button type="button" class="btn sm" data-act="bgmPrev">上一首</button> ' +
          '<button type="button" class="btn sm" data-act="bgmPlay">播放 / 暂停</button> ' +
          '<button type="button" class="btn sm" data-act="bgmNext">下一首</button>';
      }
      var a = UI_ACT[key];
      return a ? '<button type="button" class="btn sm" data-act="' + a.act + '">' + a.label +
        '</button>' + (a.after ? ' ' + a.after : '') : '';
    default: return '';
  }
}
function uiLive(item){ return !!(item.key && item.ctl && item.ctl !== 'chrome'); }

function uiRowHTML(item, u){
  var chip;
  if(uiLive(item)){ chip = '<span class="mchip mchip-live">可用</span>'; }
  else if(item.mark === 'ref'){ chip = '<span class="mchip">仅作参考</span>'; }
  else if(item.mark === 'land'){ chip = '<span class="mchip mchip-plan">规划中</span>'; }
  else { chip = '<span class="mchip mchip-plan">预留</span>'; }
  if(item.extra){ chip += '<span class="mchip mchip-src">本工具自有</span>'; }

  var meta = '<span class="mmeta">可选值 ' + uiEsc(item.values || '—') +
    '　·　生效 ' + uiEsc(item.effect || '—') + '</span>';
  var hl = (uiHl && uiHl === item.name) ? ' hl' : '';

  if(!uiLive(item)){
    // 置灰条目：整行带提示，说明为什么点不了
    return '<div class="mrow sv off' + hl + '" title="该功能暂未开发，仅预留配置项，当前修改无效。">' +
      '<span class="ml">' + uiEsc(item.name) + chip + '</span>' +
      '<div class="md"><span class="mdesc">' + uiEsc(item.desc || '') + '</span>' + meta + '</div></div>';
  }
  var hf = UI_HINT[item.key];
  var hint = hf ? hf(u) : uiEsc(item.desc || '');
  return '<div class="mrow sv' + hl + '"><span class="ml">' + uiEsc(item.name) + chip + '</span>' +
    '<div class="md">' + uiCtl(item, u) + '<span class="mhint">' + hint + '</span>' + meta + '</div></div>';
}
function uiNavHTML(){
  var g = uiSpecGroups(), h = '';
  for(var i = 0; i < g.length; i++){
    h += '<button type="button" class="mnavi' + (g[i].name === uiMod ? ' on' : '') +
      '" data-gomod="' + uiEsc(g[i].name) + '"><span class="mnavt">' + (i + 1) + '　' +
      uiEsc(uiShortName(g[i].name)) + '</span><span class="mnavc">' + g[i].live + '/' + g[i].planned +
      '</span></button>';
  }
  return h;
}
/* 这个模块里有没有「会落盘」的控件。
   只有动作项的模块（配置管理、关于与帮助）没有可重置的东西 ——
   给它们显示一个重置按钮，点下去只会得到一句"没有可重置项"。 */
function uiResettable(g){
  for(var i = 0; i < g.items.length; i++){
    var it = g.items[i];
    if(it.key && (it.ctl === 'seg' || it.ctl === 'sw' || it.ctl === 'field' || it.ctl === 'dots')){
      return true;
    }
  }
  return false;
}
function uiModHTML(g, u){
  var rows = '';
  for(var i = 0; i < g.items.length; i++){
    var it = g.items[i];
    if(it.ctl === 'chrome'){ continue; }                       // 由标题栏与底栏承担，不重复出现
    if(!uiFull && !uiLive(it)){ continue; }                    // 简洁视图只列可操作的
    rows += uiRowHTML(it, u);
  }
  if(!rows){
    rows = '<div class="mbar">这个模块目前没有可操作的设置项。切到「完整参考」可以看到它规划中的条目。</div>';
  }
  return '<div class="mmodh"><span class="mmt">' + uiEsc(uiShortName(g.name)) + '</span>' +
    '<span class="mmc">' + g.live + ' 项可操作 · 规划 ' + g.planned + ' 项</span>' +
    (uiResettable(g) ? '<button type="button" class="btn sm" data-act="resetModule">重置本分类为默认</button>' : '') +
    '</div>' + rows;
}
/* 搜索结果。名称命中排在说明命中之前 —— 搜「毛玻璃」时想要的是那一行设置本身，
   而不是所有提到过它的说明。 */
function uiSearchHTML(){
  uiHits = [];
  if(!uiQuery){ return ''; }
  var q = uiQuery.toLowerCase(), g = uiSpecGroups(), byName = [], byBody = [];
  for(var i = 0; i < g.length; i++){
    var items = g[i].items;
    for(var j = 0; j < items.length; j++){
      var it = items[j];
      if(String(it.name || '').toLowerCase().indexOf(q) >= 0){
        byName.push({mod:g[i].name, item:it});
      }else if(((it.desc || '') + ' ' + (it.values || '')).toLowerCase().indexOf(q) >= 0){
        byBody.push({mod:g[i].name, item:it});
      }
    }
  }
  uiHits = byName.concat(byBody);
  if(!uiHits.length){
    return '<div class="mbar">没有匹配「' + uiEsc(uiQuery) + '」的设置项。</div>';
  }
  var h = '<div class="mhits">', max = Math.min(uiHits.length, 60);
  for(var k = 0; k < max; k++){
    h += '<button type="button" class="mhit" data-hit="' + k + '"><b>' + uiEsc(uiHits[k].item.name) +
      '</b><span>' + uiEsc(uiShortName(uiHits[k].mod)) + '</span></button>';
  }
  if(uiHits.length > max){
    h += '<div class="mbar">还有 ' + (uiHits.length - max) + ' 条命中，先显示前 ' + max + ' 条。</div>';
  }
  return h + '</div>';
}
function uiTopHTML(){
  return '<div class="mtop"><div class="msearch">' +
    '<input type="search" id="mSearch" placeholder="搜索设置项：名称、说明或可选值" value="' + uiEsc(uiQuery) + '">' +
    '<div class="mseg" data-view="' + (uiFull ? 'full' : 'simple') + '">' +
    '<button type="button" data-vv="simple"' + (uiFull ? '' : ' class="on"') + '>简洁</button>' +
    '<button type="button" data-vv="full"' + (uiFull ? ' class="on"' : '') + '>完整参考</button></div></div>' +
    (uiFull ? '<div class="msvc">完整视图把规划中与仅作参考的条目一并列出并置灰，' +
      '灰掉的条目当前修改无效，悬浮可看说明。</div>' : '') +
    '<div id="mHits">' + uiSearchHTML() + '</div>' +
    (uiImportNote ? '<div class="mbar">' + uiImportNote + '</div>' : '') + '</div>';
}
function uiFootHTML(u){
  return '<div class="mfoot">' +
    '<button type="button" class="btn sm" data-act="resetAll">重置全部设置</button>' +
    '<button type="button" class="btn sm" data-act="exportCfg">导出全部配置</button>' +
    '<button type="button" class="btn sm" data-act="importCfg">导入配置…</button>' +
    '<button type="button" class="btn sm" data-act="changes">变更记录</button>' +
    '<span class="msaved" id="mSaved" hidden>已保存</span>' +
    '<span class="mnote">当前版本 ' + uiEsc(u.version || '') + '。设置与日志都在数据目录 <code>' +
    uiEsc(u.dataDir || '') + '</code> 下（config.json / gui.log）。' +
    '导出的配置里含中转凭据，发给别人之前先自己看一眼。</span></div>';
}
function uiChangesHTML(u){
  var list = (u && u.changes) || [];
  var h = '<div class="mmodh"><span class="mmt">设置变更记录</span>' +
    '<span class="mmc">共 ' + list.length + ' 条，上限 ' + (u.changeMax || 500) + ' 条</span>' +
    '<button type="button" class="btn sm" data-act="changesBack">返回设置</button>' +
    '<button type="button" class="btn sm" data-act="changesClear">清空记录</button></div>';
  if(!list.length){
    return h + '<div class="mbar">还没有改动记录。改过设置之后，这里会逐条记下时间、模块、' +
      '选项名与前后取值；记录只存取值，不含任何与网络环境或个人身份有关的内容。</div>';
  }
  h += '<div class="mchg">';
  for(var i = list.length - 1; i >= 0; i--){   // 倒序：最近改的排在最上面
    var c = list[i];
    h += '<div class="mchgr"><span class="t">' + uiEsc(c.at) + '</span>' +
      '<span class="m">' + uiEsc(uiShortName(c.module)) + '</span>' +
      '<span class="i">' + uiEsc(c.item) + '</span>' +
      '<span class="v">' + uiEsc(c.from) + ' → ' + uiEsc(c.to) + '</span></div>';
  }
  return h + '</div>';
}
function uiSettingsHTML(u){
  if(uiView === 'changes'){
    return '<div class="mset">' + uiChangesHTML(u) + '</div>';
  }
  var gs = uiSpecGroups();
  if(!uiMod || !uiGroupByName(uiMod)){ uiMod = gs.length ? gs[0].name : ''; }
  var g = uiGroupByName(uiMod);
  return '<div class="mset' + (uiFull ? '' : ' simple') + '">' +
    '<div class="mnav">' + uiNavHTML() + '</div>' +
    '<div class="mcontent">' + uiTopHTML() + (g ? uiModHTML(g, u) : '') + uiFootHTML(u) + '</div>' +
    '</div><input type="file" id="cfgFile" accept=".json,application/json" style="display:none">';
}
async function openSettings(){
  if(!ui){
    try{ ui = await (await fetch('/api/settings')).json(); }catch(e){ ui = null; }
    if(!ui || !ui.ok){ toast('读不到设置', true); return; }
  }
  setText($('modalTitle'), '设置');
  $('modalBody').innerHTML = uiSettingsHTML(ui);
  uiBind();
  setHidden($('modal'), false);
}
/* 跳到某条设置：切到它所在的模块、清掉搜索词，并让它亮一下。
   高亮是短暂的 —— 它只是告诉你"到了"，不该在面板上长期占一个视觉重点。 */
function uiJumpTo(mod, itemName){
  uiMod = mod || uiMod;
  uiQuery = '';
  uiHl = itemName || '';
  openSettings();
  if(uiHlTimer){ clearTimeout(uiHlTimer); }
  uiHlTimer = setTimeout(function(){
    uiHl = '';
    var el = document.querySelector('#modalBody .mrow.hl');
    if(el){ el.classList.remove('hl'); }
  }, 1600);
}
function uiBind(){
  var body = $('modalBody');
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
    // 切模块与切视图都会重画整个面板。重画而不是就地显隐：
    // 模块数量不多，重画的成本远低于维护两套显示逻辑。
    if(b.hasAttribute('data-gomod')){ uiMod = b.getAttribute('data-gomod'); openSettings(); return; }
    if(b.hasAttribute('data-vv')){
      uiFull = b.getAttribute('data-vv') === 'full';
      openSettings();
      return;
    }
    if(b.hasAttribute('data-hit')){
      var h = uiHits[parseInt(b.getAttribute('data-hit'), 10)];
      if(h){ uiJumpTo(h.mod, h.item.name); }
      return;
    }
    if(b.hasAttribute('data-act')){ uiAction(b.getAttribute('data-act')); }
  };
  body.onchange = function(e){
    if(e.target && e.target.getAttribute && e.target.getAttribute('data-sw')){ uiSave(); }
  };
  Array.prototype.forEach.call(body.querySelectorAll('[data-in]'), function(inp){
    inp.onchange = uiSave;
    inp.onkeydown = function(e){ if(e.key === 'Enter'){ uiSave(); } };
  });
  /* 搜索只在结果区上重画，不重画整个面板 —— 重画会把输入框连同光标一起换掉，
     于是每敲一个字焦点就没了。 */
  var si = $('mSearch');
  if(si){
    si.oninput = function(){
      uiQuery = (si.value || '').trim();
      var box = $('mHits');
      if(box){ box.innerHTML = uiSearchHTML(); }
    };
    si.onkeydown = function(e){
      if(e.key === 'Escape'){
        si.value = ''; uiQuery = '';
        var box = $('mHits');
        if(box){ box.innerHTML = ''; }
      }
      if(e.key === 'Enter' && uiHits.length){ uiJumpTo(uiHits[0].mod, uiHits[0].item.name); }
    };
  }
  var fi = $('wallFile');
  if(fi){ fi.onchange = function(){ if(fi.files && fi.files[0]){ uiImportWall(fi.files[0]); } }; }
  var cf = $('cfgFile');
  if(cf){ cf.onchange = function(){ if(cf.files && cf.files[0]){ uiImportCfg(cf.files[0]); } }; }
}
/* 收集面板上的全部设置，提交给服务端。
   **起点必须是服务端那份 ui，不能是写死的默认值。** 面板一次只渲染一个模块，
   没渲染的那些字段在 DOM 里根本不存在 —— 如果从这里开始是一串硬编码默认值，
   那么在任何一个模块里改一项、保存，都会把其它模块的设置一起打回默认。
   这条以前不会发作（老面板一次渲染全部），改成分模块之后它就变成一个静默的重置按钮。 */
function uiCollect(){
  var u = ui || {};
  var out = {
    theme:u.theme || 'auto', accent:u.accent || 'mint', motion:u.motion || 'full',
    backdrop:u.backdrop || 'on', scale:u.scale || 'std', flat:u.flat || 'off',
    glass:u.glass || 'off', vfx:u.vfx || 'on', glow:u.glow || 'on', fade:u.fade || 'on',
    bgImage:u.bgImage || '', musicMode:u.musicMode || 'order', musicLoop:u.musicLoop || 'all',
    musicVol:u.musicVol || '70', remember:u.remember || '', logKeep:u.logKeep || '',
    defPort:u.defPort || '', defRelay:u.defRelay || '',
    autoProbe:!!u.autoProbe, noDegrade:u.noDegrade ? '1' : ''
  };
  Array.prototype.forEach.call(document.querySelectorAll('#modalBody [data-seg]'), function(seg){
    var on = seg.querySelector('button.on');
    if(on){ out[seg.getAttribute('data-seg')] = on.getAttribute('data-v'); }
  });
  var acc = document.querySelector('#modalBody .mdot.on');
  if(acc){ out.accent = acc.getAttribute('data-accent'); }
  Array.prototype.forEach.call(document.querySelectorAll('#modalBody [data-sw]'), function(c){
    var k = c.getAttribute('data-sw');
    if(k === 'autoProbe'){ out.autoProbe = c.checked; }
    if(k === 'remember'){ out.remember = c.checked ? '' : '0'; }
    // 背景光效：**这里以前漏了**，于是 out.backdrop 永远是写死的 'on' —— 复选框点下去没反应，
    // 重开面板又勾回来，而且任何一次保存都会把用户手改的 backdrop:"off" 打回 on。
    // 现在有一条用例（TestEverySwitchInThePanelIsCollected）逐键比对面板上的开关与这里的读取。
    if(k === 'backdrop'){ out.backdrop = c.checked ? 'on' : 'off'; }
    if(k === 'flat'){ out.flat = c.checked ? 'on' : 'off'; }
    // 【视觉美化，非核心功能】三个开关：总开关与两个附加项
    if(k === 'vfx'){ out.vfx = c.checked ? 'on' : 'off'; }
    if(k === 'glow'){ out.glow = c.checked ? 'on' : 'off'; }
    if(k === 'fade'){ out.fade = c.checked ? 'on' : 'off'; }
    // 这一格问的是"帧率过低时要不要询问我"，存的是它的反面 ——
    // 与「记住上次填过的值」那种"存的是关掉"的口径一致，避免多一个反义字段名。
    // 注意发出去必须是字符串（服务端那个字段是 string，布尔会直接解不出来）
    if(k === 'noDegrade'){ out.noDegrade = c.checked ? '' : '1'; }
  });
  Array.prototype.forEach.call(document.querySelectorAll('#modalBody [data-in]'), function(i){
    out[i.getAttribute('data-in')] = (i.value || '').trim();
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
  // 【视觉美化，非核心功能】data-vfx / data-glass / data-glow / data-fade 四项交给 VFX 去写。
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
/* 保存之后就地把这两行的说明重刷一遍。
   只改文字、不重渲染整个面板 —— 重渲染会把焦点和光标位置一起弄丢，
   而用户往往正是在输入框里改完直接回车保存的。 */
function uiRefreshHints(u){
  var body = $('modalBody');
  if(!body || !u){ return; }
  // 毛玻璃那一行从勾选框改成了四档，选择器跟着换 —— 换漏的症状是"改完档位说明还挂着上一档的话"
  [['[data-in="bgImage"]', wallHintText(u)], ['[data-seg="glass"]', glassHintText(u)]].forEach(function(pr){
    var el = body.querySelector(pr[0]);
    var row = el && el.closest ? el.closest('.mrow') : null;
    var hint = row ? row.querySelector('.mhint') : null;
    if(hint){ hint.textContent = pr[1]; }
  });
}
async function uiSave(){
  var r = await api('/api/settings', uiCollect());
  if(!r || !r.ok){ toast((r && r.err) || '设置没能保存', true); return; }
  ui = r.ui;
  uiApply(ui);
  uiRefreshHints(ui);
  // 音量属于"改完立刻生效"那一类：不用等下一首
  if(bgmEl){ bgmEl.volume = bgmGain(); }
  // logKeep 按十进制字符串收：服务端两条路（读设置 / 保存）给的是同一种形态，客户端不再猜类型
  var lk = parseInt(r.logKeep, 10);
  if(!isNaN(lk)){ LOG_KEEP = lk; }
  var s = $('mSaved');
  if(s){
    setHidden(s, false);
    if(uiSaveTimer){ clearTimeout(uiSaveTimer); }
    uiSaveTimer = setTimeout(function(){ setHidden(s, true); }, 1600);
  }
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
  openSettings(); // 图库列表变了，重渲染面板
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
var uiPendingReset = null;   // 待确认的重置范围
var uiImportNote = '';       // 上一次导入的结果说明（跳过项要逐条讲清）

/* 重置前的确认条。就地在面板里插一条，不弹窗 ——
   弹窗在 WebView 里的行为各平台不一致，而且这里恰恰需要让用户看清"影响范围"这句话。 */
function uiAskReset(scope, label){
  var old = $('mConfirm');
  if(old && old.parentNode){ old.parentNode.removeChild(old); }
  uiPendingReset = {scope:scope, label:label};
  var d = document.createElement('div');
  d.id = 'mConfirm';
  d.className = 'mbar';
  d.innerHTML = '重置' + label + '会把它下面的设置项恢复成出厂值；' +
    '存档里的文件、记住的填写内容都不受影响。' +
    '<button type="button" class="btn sm" data-act="resetDo">确认重置</button> ' +
    '<button type="button" class="btn sm" data-act="resetCancel">取消</button>';
  var foot = document.querySelector('#modalBody .mfoot');
  if(foot && foot.parentNode){ foot.parentNode.insertBefore(d, foot); }
  else { $('modalBody').appendChild(d); }
}
function uiReadFile(file, cb){
  var fr = new FileReader();
  fr.onload = function(){ cb(String(fr.result == null ? '' : fr.result)); };
  fr.onerror = function(){ cb(null); };
  fr.readAsText(file, 'utf-8');
}
/* 导入一份配置文件。跳过项要逐条列出来 —— 只报一个"导入成功"，
   用户不会知道少了一半设置。 */
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
  var skip = (r.skipped || []);
  if(skip.length){
    uiImportNote = '<b>已导入，生效 ' + r.applied + ' 项</b>；另有 ' + skip.length +
      ' 项本程序没有、已跳过：' +
      skip.slice(0, 10).map(function(x){
        return '<br>' + uiEsc(x.key) + '　' + uiEsc(x.why);
      }).join('') + (skip.length > 10 ? '<br>还有 ' + (skip.length - 10) + ' 项未列出。' : '');
  }else{
    uiImportNote = '<b>已导入，生效 ' + r.applied + ' 项</b>，没有跳过的条目。';
  }
  uiView = 'main';
  openSettings();
  toast('已导入，生效 ' + r.applied + ' 项');
}
async function uiAction(a){
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
  /* ---- 配置管理：导出 / 导入 / 重置 / 变更记录 / 复制版本 ----
     导出与导入都走服务端的专用接口，而不是拼一个下载链接 ——
     写成文件再告诉用户在哪，与导出诊断包是同一套做法，也避开了各平台 WebView 下载行为不一的问题。 */
  if(a === 'exportCfg'){
    var er = await api('/api/settings/export', {});
    if(!er || !er.ok){ toast((er && er.err) || '导出失败', true); return; }
    ui = er.ui;
    openSettings();
    toast('已导出到 ' + er.path);
    return;
  }
  if(a === 'importCfg'){
    var cf = $('cfgFile');
    // 每次都要先清空：连着两次选同一个文件，第二次不会触发 change
    if(cf){ cf.value = ''; cf.click(); }
    return;
  }
  if(a === 'changes'){
    var cr = await api('/api/settings/changes', null);
    if(cr && cr.changes && ui){ ui.changes = cr.changes; }
    uiView = 'changes';
    openSettings();
    return;
  }
  if(a === 'changesBack'){ uiView = 'main'; openSettings(); return; }
  if(a === 'changesClear'){
    var cl = await api('/api/settings/changes', { clear:true });
    if(cl && cl.changes && ui){ ui.changes = cl.changes; }
    openSettings();
    toast('已清空 ' + ((cl && cl.cleared) || 0) + ' 条记录');
    return;
  }
  if(a === 'resetAll' || a === 'resetModule'){
    // 先问一句再动手：重置是"会连改好几项"的操作，不能点一下就生效
    uiAskReset(a === 'resetAll' ? 'all' : uiMod,
      a === 'resetAll' ? '全部设置' : ('「' + uiShortName(uiMod) + '」这一组'));
    return;
  }
  if(a === 'resetCancel'){
    uiPendingReset = null;
    var cd = $('mConfirm');
    if(cd && cd.parentNode){ cd.parentNode.removeChild(cd); }
    return;
  }
  if(a === 'resetDo'){
    var pr = uiPendingReset || {};
    uiPendingReset = null;
    var cd2 = $('mConfirm');
    if(cd2 && cd2.parentNode){ cd2.parentNode.removeChild(cd2); }
    if(!pr.scope){ return; }
    var rr = await api('/api/settings/reset', { scope:pr.scope });
    if(!rr || !rr.ok){ toast((rr && rr.err) || '重置失败', true); return; }
    if(!rr.changed){ toast(pr.label + '本来就是默认值'); return; }
    ui = rr.ui;
    uiApply(ui);
    openSettings();
    toast('已把' + pr.label + '恢复默认，共改动 ' + rr.changed + ' 项');
    return;
  }
  if(a === 'copyVersion'){ copy(((ui && ui.version) || 'mclbx') + ''); return; }
  if(a === 'manual'){ setHidden($('modal'), true); showManual(true); return; }
  if(a === 'musicRefresh'){
    try{
      var mr = await (await fetch('/api/settings')).json();
      if(mr && mr.ok){ ui = mr; bgmSetList(mr.musicList); }
    }catch(e){}
    openSettings();
    return;
  }
  // 打开的是存档根目录，music 就在它下面 —— 复用已有的「打开数据目录」动作
  if(a === 'musicFolder'){ a = 'openData'; }
  if(a === 'bgmPrev'){ bgmStep(-1); return; }
  if(a === 'bgmNext'){ bgmStep(1); return; }
  if(a === 'bgmPlay'){ bgmToggle(); return; }
  if(a === 'wallImport'){ var fi = $('wallFile'); if(fi){ fi.value = ''; fi.click(); } return; }
  if(a === 'wallRefresh'){ openSettings(); return; }
  // 打开的是存档根目录，wallpapers 就在它下面 —— 复用已有的「打开数据目录」动作
  if(a === 'wallFolder'){ a = 'openData'; }
  if(a === 'diag'){
    // 导出诊断包是个真正的"操作"，所以照操作那条路走（会出现在日志区里）
    setHidden($('modal'), true);
    var j = await api('/api/start', { key:'diag', inputs:{} });
    if(!j || !j.ok){ toast((j && j.err) || '启动失败', true); return; }
    setRunningUI(true); tick();
    return;
  }
  var r = await api('/api/settings', { action:a });
  if(!r || !r.ok){ toast((r && r.err) || '操作失败', true); return; }
  toast(a === 'forgetInputs' ? '已清除记住的填写内容' : '已打开数据目录');
}

// 允许用 ?manual=1 直接打开说明书（便于从外部链接进来，也方便自动化核对排版）
if(location.search.indexOf('manual=1') >= 0){ showManual(true); }
// 同一类深链，用于核对"一步开局"的两个表单态与"更多功能"展开态（自动化截图用）
var qsInit = location.search || '';
if(qsInit.indexOf('quick=host') >= 0){ showQuickForm('host'); }
else if(qsInit.indexOf('quick=guest') >= 0){ showQuickForm('guest'); }
else if(qsInit.indexOf('more=1') >= 0){ toggleMore(true); }
$('fwSkip').onclick = function(){ fwSkipped = true; setHidden($('fwBanner'), true); };
$('btnSettings').onclick = openSettings;
// 底栏的背景音乐入口。曲库与曲目要等真正用的时候才去取，所以这里只绑事件、不拉数据。
bgmEl = $('bgm');
if(bgmEl){
  bgmEl.onended = bgmEnded;
  // 文件解不开时 <audio> 只发 error 事件、不抛异常，不接的话就是"点了没反应"
  bgmEl.onerror = bgmFailed;
  bgmEl.volume = bgmGain();
}
$('bgmToggle').onclick = bgmToggle;
// 允许用 ?settings=1 直接打开设置面板（自动化截图用，与 ?manual=1 同一套办法）
if(location.search.indexOf('settings=1') >= 0){ openSettings(); }
$('btnStop').onclick = stopJob;
$('btnQuit').onclick = quitApp;
$('btnClear').onclick = resetLog;
$('btnCopyLog').onclick = function(){ copy((layer === 'user' ? userLines : rawLines).join('\n')); };
$('btnCopyAddr').onclick = function(){ copy(addr); };
$('modalClose').onclick = function(){ setHidden($('modal'), true); };
$('modal').onclick = function(e){ if(e.target === $('modal')){ setHidden($('modal'), true); } };
/* 【视觉美化，非核心功能】启动美化层，并把"它想改设置"接回业务这一侧。
   VFX 本身不认识任何接口，它只说"降到低档 / 以后别再问"，落盘这件事由这里做 ——
   所以整段美化删掉时，这个函数也就没人调用了，接口那条路一行都不用改。 */
async function vfxSave(patch){
  try{
    if(!ui){ ui = await (await fetch('/api/settings')).json(); }
    if(!ui || !ui.ok){ return; }
    // 服务端收的是**整份**设置（缺字段会被归一化回默认值），所以照设置面板那条路的字段发一遍
    var body = { theme:ui.theme, accent:ui.accent, motion:ui.motion, backdrop:ui.backdrop,
      scale:ui.scale, flat:ui.flat, glass:ui.glass, vfx:ui.vfx, glow:ui.glow, fade:ui.fade,
      bgImage:ui.bgImage, musicMode:ui.musicMode, musicLoop:ui.musicLoop, musicVol:ui.musicVol,
      remember:ui.remember, logKeep:ui.logKeep, defPort:ui.defPort, defRelay:ui.defRelay,
      autoProbe:!!ui.autoProbe, noDegrade:ui.noDegrade ? '1' : '' };
    for(var k in patch){ body[k] = patch[k]; }
    var r = await api('/api/settings', body);
    if(!r || !r.ok){ return; }
    ui = r.ui;
    uiApply(ui);
    uiRefreshHints(ui);
    if(!$('modal').hidden){ openSettings(); }  // 面板开着就地重渲染，档位跟着变
  }catch(e){ /* 降级失败不该影响工具 */ }
}
VFX.init(window.MCLBX_UI || null, vfxSave);
Array.prototype.forEach.call($('segs').querySelectorAll('.seg'), function(sg){
  sg.onclick = function(){
    layer = sg.getAttribute('data-f');
    refreshLog();
  };
});
document.addEventListener('keydown', function(e){
  if(e.key === 'Escape'){
    if(!$('modal').hidden){ setHidden($('modal'), true); return; }
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
</script>
</body>
</html>
`
