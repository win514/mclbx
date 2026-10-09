package main

// guihtml.go —— 控制台的页面（编在 exe 里，不落文件、不依赖外网）。
// 界面按信号链组织（本机 → 入口 → 房间 → 玩家），空闲时无无限动画以控制显卡占用。

const guiPageHTML = `<!doctype html>
<!--
  界面页：由服务端一次性渲染，不依赖外网；设置存在存档目录的 config.json 里。

  样式改动需守住的性能约束（每一条都有对应的用例）：
    · 模糊只加在局部面板上，禁止整屏图层（.bg / .aurora / .grid / .modal）；
    · 内容持续追加、或会随滚动与搜索重算的地方不加模糊（.logpanel / .log / .tasks / .op / 侧栏面板）；
    · 任务执行期间整体让位（data-vfx-busy），跑完立刻恢复；
    · 不写 will-change；淡入只动 opacity。
    · 全部美化（CSS 里「视觉美化（VFX）」那一段 + 脚本里的 VFX 模块）可整段删除，
      删掉之后界面回到实心扁平外观，功能不受影响。
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
  /* 顶栏与状态栏的高度跟着字号走：字号一放大，固定高度的这两条会把字切掉。
     状态栏比原来高 10px —— 停止/退出挪进来之后它要放得下按钮，不再只是一行小字。 */
  --row-top:56px; --row-bot:40px;
  /* 背景图片的地址由服务端按当前设置注入到 <html> 的行内样式里。这里必须给一个默认值：
     样式表里一旦写了 var(--wall)，定义它的地方就得在样式表里（有用例逐个变量核对）。 */
  --wall:none;
  /* ---- 毛玻璃用的静态色（三档） ----
     写死成 rgba() 而不是用 color-mix() 现算：color-mix() 需要 Chromium 111+，
     而这个界面跑在系统自带的 WebView2 上，内核版本不由本程序决定。不支持的引擎会在解析期
     丢掉整条声明 —— 毛玻璃那几条只是"没效果"，但暗化层消失会让照片上的字失去衬托。
     三档对应界面上三层：--glass-1 主容器（最实）／--glass-2 次级卡片（更透，靠 1px 边框成形）
     ／--glass-3 浮层（最实 + 最强模糊）。层次靠不透明度差 + 边框 + 阴影，不靠每层都加模糊。 */
  --glass-1:rgba(13,21,30,.82); --glass-2:rgba(16,27,37,.62); --glass-3:rgba(13,21,30,.94);
  --wall-scrim:rgba(7,11,17,.58);
  --disp:"Bahnschrift","Segoe UI Variable Display","Microsoft YaHei UI",sans-serif;
  --ui:"Microsoft YaHei UI","Segoe UI Variable Text","Segoe UI",sans-serif;
  --mono:"Cascadia Mono","Consolas","Microsoft YaHei UI",monospace;
}

/* ==========================================================================
   主题 = 纯属性选择器 + 变量覆盖，运行时没有分支、动画与额外资源。
   加一个主题只动两处：这里的一组选择器块，以及 guiconfig.go 的 normalizeUI 取值表。
   服务端注入（renderGuiPage）是唯一的闸门，它只认归一化后的取值。

   变量分三类：
     · 主题私有：每个主题块必须自己声明全部，漏一个的症状是"某一块还是上一个主题的颜色"；
     · 强调色私有：--sig / --sig-deep / --sel / --pre-bg / --aurora-a；
       只有强调色块与浅色主题才准动它（浅底需要重新调实心色）；
     · 共用：尺寸与字体，以及强调色那一族的半透明值。
   --pre-bg 与 --aurora-a 是"强调色顺带决定页面氛围"的耦合；若要加背景类主题，
   必须先把这两个从强调色块里拿出来交给主题，否则会得到"主题 × 强调色"的全组合。
   ========================================================================== */

/* 强调色：只换这一个色族，不区分深浅主题（半透明的那些在两种底色上都成立）。 */
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
  --glass-1:rgba(255,255,255,.82); --glass-2:rgba(255,255,255,.62); --glass-3:rgba(255,255,255,.94);
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
  /* 高对比这一档不参与毛玻璃：它的存在理由是"看不清"，半透明与模糊都与"看清"直接冲突。
     三档面在这里都压到接近不透明 —— 属性仍然声明（满足"每个主题声明全部"的口径），
     但透出来的一点点底色既不影响对比度，也不至于让这一档看起来像被挖空了。 */
  --glass-1:rgba(8,13,19,.97); --glass-2:rgba(12,18,25,.92); --glass-3:rgba(0,0,0,.99);
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

/* ---- 大字号（独立开关，不属于主题） ----
   字号动的是版式，主题动的是配色，两者是两个维度：塞进主题会得到"主题 × 字号"的组合。
   它只覆盖字号 token 与顶栏/状态栏高度，不给任何控件单独写样式。
   加档位时必须覆盖 :root 里声明的每一个 --fs-*，少一个就会出现"大部分变大、某一块没变"。 */
:root[data-scale="big"]{
  --fs-10:11px; --fs-10-5:11.5px; --fs-11:12px; --fs-11-5:12.5px; --fs-12:13px; --fs-12-5:13.5px;
  --fs-13:14px; --fs-13-5:14.5px; --fs-14:15px; --fs-15:16px; --fs-16:17.5px; --fs-17:18.5px; --fs-18:19.5px;
  --fs-9:10px; --fs-26:29px;
  --row-top:62px; --row-bot:46px;
}
/* ---- 背景图片（独立于背景光效） ----
   一层静态贴图 + 一层压暗。静态是硬要求：平移、缓慢缩放或视差都是整屏逐帧重新光栅化。
   压暗层不是装饰 —— 它让任意一张照片上的文字仍然读得清，颜色取自当前主题底色。 */
.wall{position:absolute;inset:0;background-image:var(--wall);
  background-size:cover;background-position:center;background-repeat:no-repeat}
.scrim{position:absolute;inset:0;display:none}
:root[data-wall="on"] .scrim{display:block;background:var(--wall-scrim)}

/* ---- 扁平化（独立开关，不属于主题） ----
   它动的是材质与层次（圆角、阴影、渐变、光晕），全是减法。两条边界：
     · 焦点态必须留下：外阴影聚焦环换成 outline，焦点看不见等于键盘不能用；
     · 不许出现 .bg / .aurora / .grid —— 背景光效归 data-backdrop 管。 */
/* 圆角大小：三个离散档，改的是同一组圆角令牌。放在「扁平」之前 —— 扁平把圆角一律归零。 */
:root[data-radii="sharp"]{--r1:3px;--r2:4px;--r3:6px}
:root[data-radii="round"]{--r1:10px;--r2:14px;--r3:20px}

/* 侧边栏样式「紧凑」：去掉操作行里的说明行、收紧行距，一屏能多看几项。
   只动侧栏的排版，操作的名称、顺序与行为一概不变。 */
:root[data-rail="compact"] .op{padding:6px 10px 6px 11px;margin-bottom:3px}
:root[data-rail="compact"] .op .ds{display:none}
:root[data-rail="compact"] .op .nm{margin-bottom:0}

:root[data-flat="on"]{--r1:0px;--r2:0px;--r3:0px;--pill:0px}
:root[data-flat="on"] .glyph{background:var(--sig);box-shadow:none}
:root[data-flat="on"] .panel,
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
:root[data-flat="on"] .navi,
:root[data-flat="on"] .op .nm .lg,
:root[data-flat="on"] .op .lim,
:root[data-flat="on"] .op.on::before,
:root[data-flat="on"] .opgrp .tag,
:root[data-flat="on"] .ck .bx,
:root[data-flat="on"] .qbtn,
:root[data-flat="on"] .qform input,
:root[data-flat="on"] .qform textarea,
:root[data-flat="on"] .qpre,
:root[data-flat="on"] .invtext,
:root[data-flat="on"] .mtxt,
:root[data-flat="on"] .glyph{border-radius:0}

/* ============ 视觉美化（VFX）：可整段删除的装饰层 ============
   本段与脚本里的 VFX 模块不参与业务判断，删掉后界面回到实心扁平外观。

   开关（均由服务端首屏注入，避免"先实心、再闪成玻璃"）：
     data-vfx       on/off              总开关，关掉 = 实心、无光、无淡入
     data-glass     off/low/mid/high    毛玻璃档位；off 只表示不做实时模糊，面板仍半透明
     data-glow      on/off              轮廓微光
     data-fade      on/off              浮层淡入
     data-vfx-busy  on/off              运算期间让位（脚本在任务执行时挂上）

   边界 —— 成本随「玻璃面积 × 变化频率」增长，以下几条就是压住这两个乘数的手段：
     · 只给局部面板加模糊；整屏层（.modal / .bg / .aurora / .grid）、
       随滚动与搜索重排的列表（.tasks / .op / 侧栏面板）、持续追加的日志（.logpanel / .log）不加；
     · 运算期间 backdrop-filter 全部退回 none，跑完恢复；
     · 不写 will-change（那会为每块玻璃常驻一个合成层）；
     · .mbox 用 contain:layout paint（独立浮层）；.panel 等只用 layout ——
       paint 会把 .panel 四角的 -1px 描边裁掉。

   层次：--glass-1 主容器（带模糊）／--glass-2 面板内的次级卡片（只半透明）／--glass-3 浮层（模糊更强）。
   设了背景图时整段让位（选择器里的 :not([data-wall="on"])）：半透明面板压在任意照片上文字对比度不达标，
   两个功能互斥，设置页里会就地说明。 */
/* ---- 层次一：主容器的面 ---- */
:root[data-vfx="on"]:not([data-wall="on"]) .panel,
:root[data-vfx="on"]:not([data-wall="on"]) .topbar,
:root[data-vfx="on"]:not([data-wall="on"]) .statusbar{background-image:none;background-color:var(--glass-1)}
/* ---- 层次二：面板内部的次级卡片与控件底。更透，靠边框与阴影撑出形 ---- */
:root[data-vfx="on"]:not([data-wall="on"]) .op,
:root[data-vfx="on"]:not([data-wall="on"]) .capcell,
:root[data-vfx="on"]:not([data-wall="on"]) .log{background-color:var(--glass-2)}
/* 顶边比其余三边亮一档。顶栏只有下边框（它贴的是视口上沿），那一档得落在下边框上 ——
   给它写 border-top-color 是一条永远不生效的死规则。 */
:root[data-vfx="on"]:not([data-wall="on"]) .panel,
:root[data-vfx="on"]:not([data-wall="on"]) .op,
:root[data-vfx="on"]:not([data-wall="on"]) .log,
:root[data-vfx="on"]:not([data-wall="on"]) .statusbar{border-top-color:var(--line2)}
:root[data-vfx="on"]:not([data-wall="on"]) .topbar{border-bottom-color:var(--line2)}
/* 面板自己的悬停/选中态原本是实心色，玻璃下要跟着一起变透明，否则鼠标一划过就"啪"地变实了 */
:root[data-vfx="on"]:not([data-wall="on"]) .op:hover,
:root[data-vfx="on"]:not([data-wall="on"]) .op.on{background-color:var(--glass-1)}
/* 被判定"环境不支持"的条目本来就是置灰的，鼠标划过不该亮起玻璃悬停色。
   上面那条 :hover 的特异性比 .op.off:hover 高，所以这里必须显式压回去。 */
:root[data-vfx="on"]:not([data-wall="on"]) .op.off:hover{background-color:var(--surf)}

/* 两档模糊值。**档位是离散的**，所以用户拿不到"模糊 200px"这种把界面拖垮的取值；
   低档压在 5px 以内，高档 14px 并且面板上会就地提醒代价。
   浮层另给一档更强的：与主容器同强度就分不出主次了。 */
:root[data-glass="low"]{--glass-blur:5px;--blur-3:9px}
:root[data-glass="mid"]{--glass-blur:9px;--blur-3:16px}
:root[data-glass="high"]{--glass-blur:14px;--blur-3:20px}

/* 真毛玻璃只在这两处 —— 一是薄条或静止内容的主容器，二是浮层。
   :not([data-vfx-busy="on"]) 就是运算期间的让位。 */
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .topbar,
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .statusbar,
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .stage>.panel:not(.logpanel){
  contain:layout;
  backdrop-filter:blur(var(--glass-blur)) saturate(1.08)}
/* 设置页外壳：一页占满，内部滚动。模糊加在外壳上而不是那些分类卡上 ——
   外壳不动，背后那层静止背景的模糊结果就能一直复用；卡片跟着滚，加在卡上就要每帧重算。 */
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .setpage{
  contain:layout;
  backdrop-filter:blur(var(--glass-blur)) saturate(1.08)}
/* 浮层：最实的一档 + 最强的一档模糊，并比主容器多含一层绘制隔离。 */
:root[data-vfx="on"]:not([data-glass="off"]):not([data-wall="on"]):not([data-vfx-busy="on"]) .mbox{
  contain:layout paint;
  backdrop-filter:blur(var(--blur-3)) saturate(1.12)}
:root[data-vfx="on"]:not([data-wall="on"]) .mbox{background-color:var(--glass-3)}

/* 科幻轮廓微光：静态的一次性光晕，**不挂 transition** ——
   给阴影挂过渡等于每次过渡都逐帧重新光栅化，那正是扁平化那一段里记下的坑。 */
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .panel:not(.logpanel){
  box-shadow:0 0 0 1px var(--sig-ring),0 0 16px -10px var(--sig-glow)}
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .mbox,
:root[data-vfx="on"][data-glow="on"] .toast{
  box-shadow:0 0 0 1px var(--sig-ring),0 0 22px -12px var(--sig-glow)}
/* 轮廓微光同时把四角描边点亮一档：细描边是这套界面的科幻感来源，光晕只是它的补充。 */
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .panel:not(.logpanel)::before,
:root[data-vfx="on"][data-glow="on"]:not([data-wall="on"]) .panel:not(.logpanel)::after{opacity:.55}

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
:root[data-vfx="on"]:not([data-wall="on"]) .vfxask{background:var(--glass-3)}
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

/* ===== 外壳：顶栏 / 页面 / 状态栏 三段纵向 =====
   三段高度都由 token 决定（--row-top / --row-bot），字号一放大它们跟着长，
   否则字会被切掉。三条横向带夹着中间那一页，是这套界面唯一的结构。 */
.shell{position:relative;z-index:1;display:grid;grid-template-rows:var(--row-top) 1fr var(--row-bot);height:100%}

/* ---------------------------------------------------------------- 顶栏 ----
   四段各司其职：身份（这是哪个工具）、页面导航（我在哪、能去哪）、
   连接进展（现在走到哪一步）、当前状态（此刻在干什么）。 */
.topbar{display:flex;align-items:center;gap:var(--s3);padding:0 18px;
  border-bottom:1px solid var(--line);background:var(--chrome)}
.glyph{position:relative;width:22px;height:22px;flex:0 0 auto;border-radius:6px;
  background:linear-gradient(150deg,var(--sig),var(--sig-deep));box-shadow:0 0 0 1px var(--sig-edge)}
.glyph::after{content:"";position:absolute;left:6px;top:6px;width:10px;height:10px;border-radius:2px;background:var(--bg)}
.glyph::before{content:"";position:absolute;left:9px;top:9px;width:4px;height:4px;border-radius:1px;background:var(--sig)}
.brand{display:flex;align-items:baseline;gap:9px;min-width:0}
.brand b{font:600 var(--fs-16)/1 var(--disp);letter-spacing:.03em}
.brand .sub{font-size:var(--fs-12-5);color:var(--ink2)}
.spacer{flex:1}

/* 页面导航：三段式，当前页高亮。它替代了原先散在右上角的两个按钮 ——
   "我在哪、能去哪"现在只有一处答案，说明书也顺势从整屏覆盖层降成了一页。 */
.nav{display:flex;align-items:center;gap:2px;padding:3px;border:1px solid var(--line);
  border-radius:var(--pill);background:var(--surf)}
.navi{padding:5px 13px;border-radius:var(--pill);font-size:var(--fs-12-5);color:var(--muted);
  transition:color .16s,background .16s}
.navi:hover{color:var(--ink2);background:var(--surf2)}
.navi.on{color:var(--on-sig);background:var(--sig);font-weight:600}

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

/* ---------------------------------------------------------------- 页面 ----
   一页 = 侧栏 + 主区。工作台用这个两列；设置与说明书是整页内容，另有自己的排法。
   窗口变窄时侧栏让位到上面（见文件末尾的媒体查询），主区永远保持可读宽度。 */
.page{display:grid;grid-template-columns:330px minmax(0,1fr);gap:var(--s4);
  min-height:0;padding:var(--s4) 18px}
.side{display:flex;flex-direction:column;min-height:0;gap:var(--s2)}
.stage{display:flex;flex-direction:column;gap:var(--s3);min-width:0;min-height:0}
::-webkit-scrollbar{width:10px;height:10px}
::-webkit-scrollbar-thumb{background:var(--scroll);border-radius:9px;border:2px solid transparent;background-clip:content-box}
::-webkit-scrollbar-thumb:hover{background:var(--scroll-hi);background-clip:content-box}
::-webkit-scrollbar-track{background:transparent}

/* 侧栏的两块：环境结论 + 操作。都是"面板"，但用 panel-flat ——
   它们的面由 --glass-2 这一级给（见 VFX 那一段），因为它们的内容会随搜索与滚动重算。 */
.side>.panel{flex:0 0 auto;padding:var(--s3)}
.side>.panel.grow{flex:1;min-height:0;display:flex;flex-direction:column}
/* 面板自己的小标题。它替代了原来"每块内容各写一种标题样式"的做法：
   现在整页只有 .ph 这一种面板标题，字号、字距、左侧短横线都一致。 */
.ph{display:flex;align-items:center;gap:var(--s2);margin:0 0 var(--s2)}
.ph h2{font:var(--fs-11)/1 var(--mono);letter-spacing:.14em;color:var(--muted);font-weight:400}
.ph h2::before{content:"";display:inline-block;width:10px;height:1px;background:var(--muted2);
  vertical-align:middle;margin-right:7px}
.ph .linkbtn{margin-left:auto}

.search{position:relative;flex:0 0 auto}
.search input{width:100%;padding:8px 11px 8px 30px;border:1px solid var(--line);border-radius:var(--r1);
  background:var(--search);outline:none;font-size:var(--fs-12-5);transition:border-color .16s,box-shadow .16s}
.search input:focus{border-color:var(--sig-focus);box-shadow:0 0 0 3px var(--sig-soft)}
.search input::placeholder{color:var(--muted2)}
.search .ic{position:absolute;left:10px;top:50%;width:11px;height:11px;margin-top:-6px;
  border:1.5px solid var(--muted);border-radius:50%}
.search .ic::after{content:"";position:absolute;right:-4px;bottom:-3px;width:5px;height:1.5px;
  background:var(--muted);transform:rotate(45deg);border-radius:1px}
.sidecount{font:var(--fs-11)/1.5 var(--mono);color:var(--muted2);white-space:nowrap;
  padding:var(--s2) 2px var(--s2)}

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
/* 未选操作时主区那一句话。轻量到不抢视线，只负责把"其他操作在哪"说清楚。 */
.idle{font-size:var(--fs-12-5);color:var(--muted2);line-height:1.7}
.idle b{color:var(--ink2);font-weight:600}

/* ---- 操作列表：常驻，按用途分组 ----
   行名 .op 是"operation"，不再叫 .t（那个名字读不出是什么）。
   悬停只换边框与底色、**不做位移**：位移会让这一行所在的玻璃面每帧重新取样，
   而它带来的观感收益只有 2px 那么多。
   注意这里用的是**实心**的 --surf/--surf2：半透明与玻璃只在 VFX 那一段里覆写
   （那一段的选择器都带着"设了背景图就让位"的排除）。基础样式一旦自己用玻璃色，
   美化层就删不干净，设了背景图时也没有东西把它压回实心。 */
.tasks{flex:1;min-height:0;overflow:auto;padding-right:5px}
.opgrp{display:flex;align-items:center;gap:8px;margin:var(--s4) 0 var(--s2);
  padding:5px 0 5px 2px}
.opgrp:first-child{margin-top:2px}
.opgrp .tag{width:5px;height:5px;border-radius:1.5px;flex:0 0 auto;background:var(--muted2)}
.opgrp .gl{font:var(--fs-11)/1 var(--mono);letter-spacing:.14em;color:var(--muted)}
.opgrp .rule{flex:1;height:1px;background:var(--line)}
.opgrp.g1 .tag{background:var(--sig)} .opgrp.g2 .tag{background:var(--blue)}
.opgrp.g3 .tag{background:var(--violet)} .opgrp.g4 .tag{background:var(--amber)}
.opgrp.g5 .tag{background:var(--muted)} .opgrp.g6 .tag{background:var(--rose)}

.op{display:block;width:100%;text-align:left;padding:9px 12px 9px 13px;margin-bottom:6px;
  border:1px solid var(--line);border-radius:var(--r2);background:var(--surf);position:relative;
  transition:border-color .16s,background .16s}
.op:hover{border-color:var(--line2);background:var(--surf2)}
.op .nm{display:flex;align-items:center;gap:7px;font-size:var(--fs-13);font-weight:600;margin-bottom:2px}
.op .nm .lg{margin-left:auto;font:var(--fs-10)/1 var(--mono);color:var(--muted);border:1px solid var(--line2);
  border-radius:3px;padding:2px 4px;white-space:nowrap}
.op .ds{font-size:var(--fs-11-5);line-height:1.5;color:var(--muted);
  display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
.op.on{border-color:var(--sig-edge);background:var(--surf2)}
.op.on::before{content:"";position:absolute;left:-1px;top:9px;bottom:9px;width:2px;border-radius:2px;background:var(--sig)}
.op.on .ds{color:var(--ink2)}
/* 环境不支持的操作：置灰，鼠标提示只给一句原因（见 .op.off 的 title） */
.op.off{opacity:.42;cursor:not-allowed}
.op.off:hover{border-color:var(--line);background:var(--surf)}
/* 环境存疑但不禁用：琥珀色标记，同样把原因放在 title 里 */
.op .lim{font:var(--fs-10)/1 var(--mono);color:var(--amber);border:1px solid var(--amber-edge);
  border-radius:3px;padding:2px 4px;background:var(--amber-soft);white-space:nowrap}
.op.hide{display:none}
.opempty{padding:26px 10px;text-align:center;color:var(--muted2);font-size:var(--fs-12-5)}

/* ---- 面板：一级容器 ----
   面板 = 界面上所有"一块内容"的统一容器。以前这里有三套写法（内容用 .card、
   浮层用 .mbox、设置用 .mset），圆角与边框各写一遍、命名也读不出同一件事；
   现在统一到 .panel 这一套，只有浮层例外 —— 它要压在一切之上，圆角与阴影都更重一档。 */
.panel{position:relative;border:1px solid var(--line);border-radius:var(--r3);
  background:linear-gradient(180deg,var(--surf),var(--bg2))}
.panel::before,.panel::after{content:"";position:absolute;width:10px;height:10px;pointer-events:none;
  opacity:0;transition:opacity .18s}
.panel::before{top:-1px;left:-1px;border-top:1px solid var(--sig);border-left:1px solid var(--sig);border-radius:var(--r3) 0 0 0}
.panel::after{bottom:-1px;right:-1px;border-bottom:1px solid var(--sig);border-right:1px solid var(--sig);border-radius:0 0 var(--r3) 0}
.panel:hover::before,.panel:hover::after{opacity:.9}
/* panel-flat：面板里还嵌着面板时用。去掉渐变，只留一层更透的底，靠边框分层。 */
.panel-flat{background:var(--surf)}

.k{display:flex;align-items:center;gap:8px;font:var(--fs-11)/1 var(--mono);letter-spacing:.14em;
  color:var(--muted);margin-bottom:var(--s3)}
.k::before{content:"";width:12px;height:1px;background:var(--muted2)}
.hint{font-size:var(--fs-12-5);color:var(--ink2)}

/* 操作头 + 表单 */
.formpanel{padding:0;display:flex;flex-direction:column;flex:0 1 auto;max-height:min(56vh,600px)}
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

/* 说明书页：它本身就是一份独立文档（iframe 内自带排版与主题），这里只负责给它一块地方。
   以前它是整屏覆盖层，还自带一条标题栏与关闭按钮 —— 标题栏由顶栏导航承担之后，
   那一整层（定位、层级、关闭语义）连同它的按钮一起删掉了。 */
#pageManual{display:block;padding:0}
#manualFrame{width:100%;height:100%;border:0;background:#fff;display:block}

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
.logpanel{flex:1;min-height:140px;display:flex;flex-direction:column;padding:var(--s3) var(--s4) var(--s4)}
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

/* ---- 详情浮层：界面上唯一的浮层 ----
   操作里那些原理性内容（握手过程、为什么这样做）放在这里，不铺在主界面。
   设置与说明书都不再走这里 —— 它们是页，不是"点开看完就关掉"的东西。 */
.modal{position:fixed;inset:0;z-index:40;display:flex;align-items:center;justify-content:center;
  background:var(--panel)}
.mbox{width:min(760px,92vw);max-height:76vh;display:flex;flex-direction:column;
  border:1px solid var(--line2);border-radius:var(--r3);background:var(--bg2)}
.mhead{display:flex;align-items:center;gap:var(--s3);padding:var(--s4) var(--s5);
  border-bottom:1px solid var(--line)}
.mhead .mt{font:600 var(--fs-15)/1.3 var(--disp)}
.mbody{padding:var(--s4) var(--s5);overflow:auto;font-size:var(--fs-13);line-height:1.75;color:var(--ink2);
  white-space:pre-wrap}

/* ==================== 设置页 ====================
   三个一级分类，顺序固定，各自只装一类信息：
     主题外观   —— 配色、背景、特效、排版。全是"看一眼就知道改了什么"的外观项。
     辅助工具   —— 背景音乐、填表与日志。软件自带的附加能力，关掉任何一项联机照常。
     关于与状态 —— 只读信息集中在这里（版本、环境、目录、占用），另加维护动作。
   每个设置项的结构统一为：标题 + 状态标签 / 说明 / 控件 / 悬停可见的更细提示。
   面板上只放真的会生效的设置 —— 做不到的东西不进来（有用例逐项兜着）。
   为什么状态信息只在"关于与状态"里出现一次：它以前在顶栏、底栏、设置里各有一份，
   三处不一致时没人知道该信哪一个。 */
/* 设置页外壳：占满一页、自己不滚，滚动交给里面的 .mset。
   玻璃加在这一层而不是那些分类卡上 —— 外壳不动，背后静止背景的模糊结果就能一直复用。 */
.setpage{display:block;padding:0;overflow:hidden}
.mset{height:100%;overflow:auto;padding:var(--s5) 22px var(--s5);
  display:flex;flex-direction:column;gap:var(--s4);white-space:normal}
/* 页首一句话：这一页怎么用。它替掉了原先面板底部那段"没有未保存状态"的说明 ——
   那句话是给"找不到保存按钮的人"看的，放在最前面才有用。 */
.mintro{font-size:var(--fs-12-5);color:var(--ink2);line-height:1.7;max-width:78ch}
.mintro b{color:var(--ink)}

/* 一级分类卡 */
.mcat{border:1px solid var(--line);border-radius:var(--r3);background:var(--surf);
  padding:var(--s4) var(--s5) var(--s3)}
.mcathead{display:flex;align-items:center;gap:var(--s3);margin-bottom:var(--s3)}
.mcathead .ic{flex:0 0 auto;display:flex;color:var(--sig)}
.mcathead .tt{font:600 var(--fs-15)/1.2 var(--disp)}
.mcathead .sub{flex:1;min-width:0;font-size:var(--fs-11-5);color:var(--muted);line-height:1.5}

/* 设置项：一行一项。左边标题+说明，右边控件 —— 控件位置固定，扫一列就知道改哪个。 */
.mitem{display:grid;grid-template-columns:minmax(0,1fr) max-content;gap:var(--s2) var(--s5);
  align-items:center;padding:11px 0;border-top:1px solid var(--hair)}
.mitem:first-child{border-top:0}
.mi-h{display:flex;align-items:center;gap:var(--s2);margin-bottom:3px}
.mi-t{font-size:var(--fs-13);font-weight:600;color:var(--ink)}
/* 状态标签：只说"这一项现在是什么状态"，不重复控件里的信息。 */
.mi-tag{font:var(--fs-10)/1 var(--mono);padding:2px 6px;border-radius:3px;white-space:nowrap;
  border:1px solid var(--line2);color:var(--muted)}
.mi-tag.chg{border-color:var(--sig-edge);color:var(--sig);background:var(--sig-soft)}
.mi-d{font-size:var(--fs-11-5);color:var(--muted);line-height:1.6;max-width:64ch}
.mi-c{display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap;justify-content:flex-end}
/* 控件旁边的一小句结果（例如帧率实测的数字）。等宽字体，因为它量的是数字。 */
.mi-out{font:var(--fs-11-5)/1 var(--mono);color:var(--muted2)}
/* 分类内部的小标题：用在只读信息与变更记录之间，只做分隔，不再承载分组语义。 */
.msub{display:flex;align-items:baseline;gap:var(--s3);margin:var(--s4) 0 var(--s2);
  padding-top:var(--s3);border-top:1px solid var(--hair)}
.msub:first-child{margin-top:0;padding-top:0;border-top:0}
.msub .tt{font:var(--fs-11)/1.5 var(--mono);letter-spacing:.12em;color:var(--muted);white-space:nowrap}
.msub .sp{flex:1}
/* 窄窗口下控件换到下一行铺开：硬挤在同一行会把说明压成一条缝 */
@media (max-width:760px){
  .mitem{grid-template-columns:1fr}
  .mi-c{justify-content:flex-start}
}

/* 辅助板块的定位声明：把"这是附加项"写在脸上，而不是让用户去猜 */
.mbenefit{display:flex;gap:var(--s2);margin:0 0 var(--s3);padding:var(--s2) var(--s3);
  border:1px solid var(--line);border-radius:var(--r1);background:var(--surf2);
  font-size:var(--fs-11-5);line-height:1.6;color:var(--ink2)}
.mbenefit .ic{flex:0 0 auto;display:flex;color:var(--muted)}

/* 关于与状态：整块只读，不给任何输入控件留位置（有用例守住这一条） */
.mabout{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:6px var(--s5);margin:0;
  font-size:var(--fs-12)}
.mabout dt{color:var(--muted)}
.mabout dd{margin:0;color:var(--ink2);overflow-wrap:anywhere}
.mabout dd.mono{font-family:var(--mono);font-size:var(--fs-11-5)}
.mstats{display:grid;grid-template-columns:repeat(4,1fr);gap:var(--s2);margin-top:var(--s3)}
.mstat{padding:var(--s2) var(--s3);border:1px solid var(--hair);border-radius:var(--r1);
  background:var(--surf2)}
.mstat .k{font-size:var(--fs-11);color:var(--muted)}
.mstat .v{font:600 var(--fs-13)/1.3 var(--mono);color:var(--ink);margin-top:2px}
.mstat .n{font-size:var(--fs-11);color:var(--muted2)}
.mnotice{display:flex;gap:var(--s2);margin-top:var(--s3);padding:var(--s2) var(--s3);
  border:1px solid var(--amber-edge);border-radius:var(--r1);background:var(--amber-soft);
  font-size:var(--fs-11-5);line-height:1.6;color:var(--ink2)}
.mnotice .ic{flex:0 0 auto;display:flex;color:var(--amber)}

/* 变更记录：就放在"关于与状态"里，不再是一扇独立的门 ——
   它是状态的一部分（这份配置被谁改过），不是第二个设置页。 */
.mchg{display:flex;flex-direction:column;margin:var(--s2) 0}
.mchgr{display:flex;align-items:baseline;gap:8px;padding:5px 0;
  border-bottom:1px solid var(--hair);font-size:var(--fs-11-5);color:var(--ink2)}
.mchgr .t{flex:0 0 118px;font-family:var(--mono);color:var(--muted)}
.mchgr .m{flex:0 0 92px;color:var(--muted)}
.mchgr .i{flex:1;min-width:0}
.mchgr .v{flex:0 0 auto;font-family:var(--mono);color:var(--muted)}
.mchgempty{padding:var(--s3) 2px;color:var(--muted2);font-size:var(--fs-11-5)}

/* ---- 设置控件（统一命名 m 前缀） ----
   分段器 / 开关 / 输入框 / 色点，四种就够：面板上不该出现第五种控件形态。 */
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
.mdot{width:22px;height:22px;border-radius:50%;border:1px solid var(--line2);cursor:pointer;padding:0}
.mdot.on{box-shadow:0 0 0 2px var(--surf),0 0 0 4px var(--sig-edge)}

/* ---- 维护操作：一律集中在这里 ----
   恢复默认、导出、导入、变更记录、打开数据目录、导出诊断包 —— 全是"动作"而不是设置，
   所以它们不在上面三个分类里，也不会散落在各分类的标题旁边。
   恢复默认仍然保留两个范围（当前分类 / 全部），但入口只有一个，点开后再选范围。 */
.mmaint{border:1px solid var(--line);border-radius:var(--r3);background:var(--surf);
  padding:var(--s4) var(--s5)}
.mmaint .acts{display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap;margin-top:var(--s3)}
.mmaint .mnote{flex:1 1 100%;margin-top:var(--s2);font-size:var(--fs-11-5);color:var(--muted);line-height:1.6}
/* 就地确认条：重置是"会连改好几项"的操作，先问一句再动手，不弹系统对话框。 */
.mconfirm{display:flex;align-items:center;gap:var(--s2);flex-wrap:wrap;margin-top:var(--s3);
  padding:var(--s2) var(--s3);border:1px solid var(--amber-edge);border-radius:var(--r1);
  background:var(--amber-soft);font-size:var(--fs-11-5);color:var(--ink2)}
.mconfirm .sp{flex:1}
.mconfirm .q{font-size:var(--fs-11-5);color:var(--ink2)}

/* 状态栏：当前任务 + 保存状态 + 常驻小工具 + 快捷键 + 生命周期动作。
   它以前只放一句任务名和一个版本号（那个版本号还和顶栏重复），中间一大片空白；
   版本号撤掉、停止/退出挪进来之后，这一条才真的在用。 */
.statusbar{display:flex;align-items:center;gap:var(--s3);padding:0 18px;border-top:1px solid var(--line);
  background:var(--chrome);font:var(--fs-11)/1 var(--mono);color:var(--muted2);white-space:nowrap;overflow:hidden}
.statusbar>span{min-width:0;overflow:hidden;text-overflow:ellipsis}
.statusbar .sb-task{color:var(--muted)}
.statusbar .sb-save{color:var(--sig)}
.statusbar .sb-save.bad{color:var(--rose-ink)}
.statusbar .gap{flex:1;overflow:visible}
.statusbar .sb-life{flex:0 0 auto;display:flex;align-items:center;gap:var(--s2)}
/* 状态栏的背景音乐入口：只显示当前曲名，**不做任何动画** —— 常驻区域对动画最不宽容，
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
/* 窗口被拖窄时，让位的顺序是按"信息是否还有别处"排的：
   进度链最先走（同样的信息在状态胶囊里也有一份），然后是导航的文字内边距，
   最后侧栏从"左边一列"改成"上面一块"。身份与状态栏的动作永远留着。 */
@media (max-width:1040px){.linkbar{display:none}}
@media (max-width:900px){.nav .navi{padding:5px 9px}}
@media (max-width:960px){
  .page{grid-template-columns:1fr;grid-auto-rows:min-content}
  .side{max-height:46vh}
  .side>.panel.grow{min-height:200px}
}
</style>
</head>
<body>
<div class="bg"><div class="wall"></div><div class="scrim"></div><div class="aurora"></div><div class="grid"></div></div>

<div class="shell" id="app">
  <!-- 顶栏 = 身份 + 页面导航 + 连接进展 + 当前状态。
       四件事各占一段，顺序固定；窗口变窄时先让位的依次是进度链、导航说明，
       身份与状态永远留着（它们是"这是哪个工具、现在在干什么"的答案）。 -->
  <header class="topbar">
    <span class="glyph"></span>
    <div class="brand"><b>mclbx</b><span class="sub">联机工具</span></div>
    <nav class="nav" id="nav">
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
      <!-- 操作列表常驻。它以前收在「更多功能」后面，于是这一列平时只剩一张环境表，
           而"操作在哪"又变成一个新问题（分享出去后最多的一句反馈就是不知道点哪个）。
           现在列表本身就是这一列的用途：按用途分组、带搜索，快速开始那两张卡仍是默认动线。 -->
      <section class="panel panel-flat grow">
        <div class="ph"><h2>操作</h2>
          <button class="linkbtn" id="btnProbe" title="重新执行环境检测">重新检测</button></div>
        <div class="search"><span class="ic"></span><input id="q" placeholder="搜索操作" autocomplete="off"></div>
        <div class="sidecount" id="cnt"></div>
        <div class="tasks" id="tasks"></div>
      </section>
    </aside>

    <div class="stage">
      <!-- 一步开局：默认入口。房主要给的就一个房间码，玩家要做的就一次粘贴；
           其余参数与说明都后置到侧栏操作列表与悬浮提示里。 -->
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

      <!-- 入口地址卡：本工具唯一要交付给用户的东西，所以字号最大、位置最靠前 -->
      <section class="panel hero" id="cardAddr" hidden>
        <span class="shine" id="sheen"></span>
        <div class="k">玩家入口地址</div>
        <div class="plate"><div class="addr" id="addr">-</div></div>
        <div class="rack">
          <button class="btn sm" id="btnCopyAddr">复制地址</button>
          <span class="chip grp" id="roomChip" hidden></span>
        </div>
        <!-- 邀请卡：房主唯一需要"转发出去"的东西 —— 地址、房间码、没装工具的连法、
             装了工具的命令，全在这一段里，只配一个复制按钮。 -->
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
        <div class="log" id="logUser"></div>
        <div class="log" id="logRaw" hidden></div>
      </section>
    </div>
  </main>

  <!-- ── 页二：设置（主题外观 / 辅助工具 / 关于与状态） ──────────────── -->
  <main class="page setpage" id="pageSettings" hidden>
    <div class="mset" id="settingsBody"></div>
  </main>

  <!-- ── 页三：说明书。它是一份独立文档（iframe 内自带排版），
       以前是个整屏覆盖层，现在是一页 —— 少一层弹窗叠加，返回也交给顶栏导航。 -->
  <main class="page" id="pageManual" hidden>
    <iframe id="manualFrame" title="使用说明书"></iframe>
  </main>

  <!-- 状态栏 = 当前任务 + 保存状态 + 常驻小工具 + 快捷键 + 生命周期动作。
       停止/退出从顶栏挪到这里：它们作用于"当前这次运行"，和上面那行状态是同一件事，
       顶栏则专管"我是谁、在哪个页、连到哪一步"。 -->
  <footer class="statusbar">
    <span class="sb-task" id="ftTask">未选择操作</span>
    <span class="sb-save" id="saveState"></span>
    <span class="gap"></span>
    <span class="bgm"><button class="btn sm" id="bgmToggle" title="背景音乐：点一下播放或暂停">♪ <span id="bgmName">未播放</span></button></span>
    <span class="sb-keys">Enter 执行 · Esc 取消</span>
    <span class="sb-life">
      <button class="btn sm danger" id="btnStop" disabled>停止</button>
      <button class="btn sm" id="btnQuit">退出</button>
    </span>
  </footer>
</div>

<!-- 背景音乐：播放完全交给界面内核的 <audio>，Go 侧只负责列出曲库与提供文件。
     preload=none 是有意的：不点播放就不去碰磁盘，空闲时这条路径的开销是零。 -->
<audio id="bgm" preload="none"></audio>

<!-- 唯一的浮层：操作原理性说明（点开看完就关）。设置不再是浮层，说明书也已经是页。 -->
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
    b.classList.toggle('on', b.getAttribute('data-page') === next);
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
  if(u.bgImage){
    return '已设背景图片，毛玻璃让位、面板保持实心：半透明面板压在任意照片上时文字对比度不达标。两个功能只能取一个。';
  }
  if(u.vfx === 'off'){ return '总开关关着，这一档暂时不生效；重新打开「视觉美化」后回到这一档。'; }
  var lv = u.glass || 'off';
  if(lv === 'off'){
    return '只留半透明底色，不做实时模糊 —— 最省的一档。';
  }
  var cost = lv === 'high'
    ? '高档（14px）明显增加显卡与浏览器开销，低配设备可能掉帧；建议先用「帧率实测」量一下。'
    : '任务执行期间自动让位，跑完立刻恢复。';
  return '面板半透明，并实时模糊背后的内容；模糊只加在局部面板上，不会铺到整页。' + cost;
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
   noDegrade / remember 存的是"关掉"的反面，logKeep 服务端给的是生效值，都要先还原成同一个口径再比。 */
var UI_DEF = {
  theme:'auto', accent:'mint', flat:'off', radii:'std', backdrop:'on', bgImage:'',
  vfx:'on', glass:'mid', glow:'on', fade:'on', noDegrade:'', scale:'std', motion:'full',
  rail:'std', musicMode:'order', musicLoop:'all', musicVol:'70', remember:'',
  logKeep:'2000', defPort:'', defRelay:''
};
function uiTag(u, key){
  // 纯动作行（帧率实测、曲库、播放控制、清除…）没有对应的设置字段，key 传空串。
  // 不加这一句的话它们会全部挂上「已改动」—— 因为空键在两张表里都取不到值，比出来就是"不同"。
  if(!key){ return null; }
  var v = u ? u[key] : '';
  if(key === 'noDegrade'){ v = u.noDegrade ? '1' : ''; }
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
    uiSub('视觉特效', '面板的半透明与模糊，以及为它服务的几个开关') +
    uiItem('vfx', '视觉美化', uiSw('vfx', u.vfx !== 'off', '科幻外观总开关'),
      '关掉等于面板实心、去掉光晕与淡入，回到基础扁平外观。',
      '总开关只影响观感，开关前后功能与计算结果完全一致。') +
    uiItem('glass', '毛玻璃', uiSeg('glass', u.glass, [['off','关闭'],['low','低'],['mid','中'],['high','高']]),
      glassHintText(u),
      '模糊按层次分配：主容器一档、浮层更强；面板内部的卡片只做半透明。') +
    uiItem('glow', '科幻轮廓微光', uiSw('glow', u.glow !== 'off', '面板描边与四角微光'),
      '静态光晕，画好一次就不再重绘。') +
    uiItem('fade', '面板淡入', uiSw('fade', u.fade !== 'off', '浮层出现时淡入'),
      '只动透明度、不动位置。',
      '选「动画效果：精简」或系统开了「减少动态效果」时自动不生效。') +
    uiItem('noDegrade', '帧率自动降级', uiSw('noDegrade', !u.noDegrade, '帧率过低时询问我'),
      '连续两次量到偏低就问一次要不要降到「低」档。',
      '每 20 秒量一次，每次约一秒，任务执行期间不量。想强制维持高特效就取消勾选。') +
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
    glass:u.glass || 'off', vfx:u.vfx || 'on', glow:u.glow || 'on', fade:u.fade || 'on',
    bgImage:u.bgImage || '', musicMode:u.musicMode || 'order', musicLoop:u.musicLoop || 'all',
    musicVol:u.musicVol || '70', remember:u.remember || '', logKeep:u.logKeep || '',
    defPort:u.defPort || '', defRelay:u.defRelay || '',
    autoProbe:!!u.autoProbe, noDegrade:u.noDegrade ? '1' : ''
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
    // 【视觉美化，非核心功能】三个开关：总开关与两个附加项
    if(k === 'vfx'){ out.vfx = c.checked ? 'on' : 'off'; }
    if(k === 'glow'){ out.glow = c.checked ? 'on' : 'off'; }
    if(k === 'fade'){ out.fade = c.checked ? 'on' : 'off'; }
    // 这一格问的是"帧率过低时要不要询问我"，存的是它的反面 ——
    // 与「记住上次填过的值」那种"存的是关掉"的口径一致，避免多一个反义字段名。
    // 注意发出去必须是字符串（服务端那个字段是 string，布尔会直接解不出来）
    if(k === 'noDegrade'){ out.noDegrade = c.checked ? '' : '1'; }
  });
  Array.prototype.forEach.call(document.querySelectorAll('#settingsBody [data-in]'), function(i){
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
  // 圆角与侧边栏这两个是"改了要立刻看见"的项：不写在这里的话，
  // 面板上选了新档位，界面要等下次刷新才变 —— 看起来就像没保存成功。
  el.setAttribute('data-radii', u.radii || 'std');
  el.setAttribute('data-rail', u.rail || 'std');
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
/* 保存之后就地把会随别的设置变的那两行说明重刷一遍。
   只改文字、不重渲染整页 —— 重渲染会把焦点和光标位置一起弄丢，
   而用户往往正是在输入框里改完直接回车保存的。 */
function uiRefreshHints(u){
  var body = $('settingsBody');
  if(!body || !u){ return; }
  [['[data-in="bgImage"]', wallHintText(u)], ['[data-seg="glass"]', glassHintText(u)]].forEach(function(pr){
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
    setHidden($('modal'), true);
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
    if(page === 'settings'){ renderSettings(); }  // 设置页开着就地重渲染，档位跟着变
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
