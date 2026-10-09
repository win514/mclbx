package main

// a11y_test.go —— 键盘可用性的接线。
//
// 这一组判据有个共同点：删掉它们，界面照样跑、鼠标照样能用，只有键盘与读屏用户会掉坑，
// 而掉坑既不报错也不崩、截图里也看不出来。所以只能靠源码级用例钉住。
// 契约来自 WAI-ARIA 的 dialog 模式（浮层）与 log 角色（输出区）。

import (
	"strings"
	"testing"
)

// 浮层要满足 dialog 模式的三条：语义（role/aria-modal/标签）、焦点进框、焦点圈在框内且关掉归还。
func TestModalFollowsDialogContract(t *testing.T) {
	for _, want := range []string{
		`role="dialog"`, `aria-modal="true"`, `aria-labelledby="modalTitle"`,
	} {
		if !strings.Contains(guiShellHTML, want) {
			t.Errorf("浮层骨架里少了 %s —— 读屏不会把它当对话框，也不会念它的标题", want)
		}
	}
	if !strings.Contains(guiShellHTML, `class="mbox" tabindex="-1"`) {
		t.Error(`对话框框体没有 tabindex="-1"：打开时无法把焦点移进去，键盘用户会留在背后的页面上`)
	}
	for _, want := range []string{"function openModal(", "function closeModal(", "function modalTrap("} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("浮层缺少 %s —— 焦点管理被删掉了吗？", want)
		}
	}
	if !strings.Contains(guiPageHTML, "addEventListener('keydown', modalTrap, true)") {
		t.Error("没有挂 Tab 拦截器：焦点会从浮层漏到后面的页面上")
	}
	// 关闭只准有一条路。直接设 hidden 的写法只应出现在 closeModal 里；
	// 多出一处，就有一条路径不归还焦点 —— 这种问题只在键盘上出现，鼠标点不出来。
	if n := strings.Count(guiPageHTML, "setHidden($('modal'), true)"); n != 1 {
		t.Errorf("直接关浮层的地方有 %d 处，应当只有 closeModal() 里那一处 —— "+
			"多出来的路径不会把焦点还给当初打开它的按钮", n)
	}
	// 「详情」那条入口必须走 openModal，不能自己把浮层显示出来
	if body := bodyBetween(t, "function showHelp(title, body){", "function buildArgs"); !strings.Contains(body, "openModal()") {
		t.Error("showHelp 没有走 openModal —— 从这条入口打开浮层时不会移焦、也没有 Tab 圈")
	}
}

// 输出区要能被辅助技术感知：role=log + polite，新追加的行会被念出来。
func TestLogIsALiveRegion(t *testing.T) {
	for _, want := range []string{
		`<div class="log" id="logUser" role="log" aria-live="polite"></div>`,
		`<div class="log" id="logRaw" role="log" aria-live="polite" hidden></div>`,
	} {
		if !strings.Contains(guiShellHTML, want) {
			t.Errorf("日志容器不是 live region：%s\n执行完的结果读屏不会念，"+
				"键盘与读屏用户只能自己猜什么时候去看", want)
		}
	}
}

// 视觉上的"高亮 / 选中 / 当前"必须同时有语言级的说法 —— 那层颜色对读屏不可见。
func TestVisualStateHasAnAccessibleEquivalent(t *testing.T) {
	for _, want := range []string{
		`setAttribute('aria-current', 'page')`,                 // 当前在哪一页
		`setAttribute('aria-pressed', cur ? 'true' : 'false')`, // 日志在看哪一层
	} {
		if !strings.Contains(guiPageHTML, want) {
			t.Errorf("缺少 %s —— 那一处的高亮只对眼睛可见", want)
		}
	}
	if !strings.Contains(guiShellHTML, `aria-label="页面"`) {
		t.Error("页面导航没有名字：读屏会把三个按钮念成「按钮 按钮 按钮」")
	}
	// 勾选框的真 input 是 opacity:0 铺在上面的，焦点环必须画在旁边看得见的方框上
	if !strings.Contains(cssRegion(t), ".ck input:focus-visible+.bx{outline:") {
		t.Error("勾选框没有可见的焦点环：键盘用户看不出焦点落在哪一项（真 input 是透明的）")
	}
}

// 状态栏那个音符按钮：左键播放/暂停，右键下一首。
// 右键这条入口必须拦掉 contextmenu（否则弹出的是 WebView2 自带菜单，右键看起来"没反应"），
// 而且必须复用「下一首」按钮走的同一个 bgmStep —— 各写一套，两处迟早会走偏。
// 键盘用户的通路是设置里那三个显式按钮，所以这里不另外发明快捷键（这是有意的，不是漏了）。
func TestMusicButtonHasBothClicks(t *testing.T) {
	if !strings.Contains(guiPageHTML, "$('bgmToggle').oncontextmenu") ||
		!strings.Contains(guiPageHTML, "e.preventDefault(); bgmStep(1)") {
		t.Error("状态栏音符按钮没有接右键下一首，或没有拦 contextmenu —— 右键会弹出浏览器自带菜单")
	}
	if !strings.Contains(guiPageHTML, "function bgmStep(") {
		t.Error("找不到 bgmStep —— 右键那条路应当与设置里的「下一首」按钮共用它")
	}
	if !strings.Contains(guiPageHTML, `data-act="bgmNext">下一首`) {
		t.Error("设置里那个显式的「下一首」按钮没了 —— 右键之外必须留着键盘可达的那一条")
	}
}

// 音量是 0-100 的滑动条，而且滑杆的值必须真的被收集上去（拖完要能存住）。
func TestVolumeIsASliderThatGetsCollected(t *testing.T) {
	if !strings.Contains(guiPageHTML, "uiRange('musicVol', u.musicVol, 0, 100)") {
		t.Error("音量不是 0-100 的滑动条")
	}
	if !strings.Contains(guiPageHTML, "querySelectorAll('#settingsBody [data-rng]')") {
		t.Error("滑杆没有被收集 —— 拖动之后保存不上去")
	}
	// 服务端那一侧也要认这个区间（0-100 之外会被归一化，用例见 uiconfig_test.go）
	if !strings.Contains(guiPageHTML, "musicVol:u.musicVol || '70'") {
		t.Error("默认音量没了 —— 没渲染设置页时这一项会丢")
	}
}
