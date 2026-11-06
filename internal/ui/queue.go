package ui

import (
	"strconv"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是 git 写命令队列（app/queue.go）在界面上的样子：Git 栏段头和标题栏上转圈的队列按钮，
// 点开是队列菜单（取消正在跑的、把排着的移出队列、停止全部抓取 / 拉取）；状态栏上全部抓取 / 拉取的进度。

// queueButton 是仓库有 git 写命令在跑或排队时的按钮：转圈，排着的有几条就写"排队 n"。点开是队列菜单。
func queueButton(a *app.App, c *wui.Context, r *app.Repo) {
	t := c.Theme()
	label := i18n.Tf("%s 的 git 队列", r.Name)
	tip := ""
	if o := r.Running(); o != nil {
		tip = i18n.Tf("正在%s…", o.Label)
	}
	if n := len(r.Queue); n > 0 {
		if tip != "" {
			tip += "\n"
		}
		tip += i18n.Tf("排队 %d 条", n)
	}
	b := wui.ButtonBase(c).Label(label).Tooltip(tip+"\n"+i18n.T("点开可以取消")).Height(20).Padding(0, 4).Gap(4).Radius(5).
		AlignItems(wui.Center).Cursor(wui.CursorPointer).Shrink(0)
	if b.Hovered() {
		b.Background(toolFill(t, 0.06))
	}
	b.Children(func() {
		wui.Spinner(c).Size(13, 13)
		if n := len(r.Queue); n > 0 {
			wui.Text(c, i18n.Tf("排队 %d", n)).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Margin(0, 0, textNudge, 0)
		}
	})
	menuButton(a, c, b, menuOwner{"queue", r.Root}, func(m menu) { queueMenu(a, m, r) })
}

// queueMenu 是仓库的 git 队列：正在跑的那条（能取消）、排着的（能移出队列），全部抓取 / 拉取在跑时还能停止。
func queueMenu(a *app.App, m menu, r *app.Repo) {
	if o := r.Running(); o != nil {
		m.Item(i18n.Tf("正在跑：%s", o.Cmd)).Disabled(true)
		if m.Item(i18n.Tf("取消%s", o.Label)).Chosen() {
			a.CancelOp(r)
		}
	}
	if len(r.Queue) > 0 {
		m.Separator()
		m.Item(i18n.Tf("排队 %d 条", len(r.Queue))).Disabled(true)
		for i, o := range r.Queue {
			if m.Item(strconv.Itoa(i+1) + ". " + i18n.Tf("移出队列：%s", o.Label)).Chosen() {
				a.Dequeue(r, i)
				break
			}
		}
	}
	if b := a.Batch; b != nil {
		m.Separator()
		if m.Item(i18n.Tf("停止全部%s", b.Label)).Chosen() {
			a.StopBatch()
		}
	}
}

// batchStatus 是状态栏左边全部抓取 / 拉取的进度："抓取 2/5：kero…"，后面一个停止的 ×。
func batchStatus(a *app.App, c *wui.Context, b *app.Batch) {
	t := c.Theme()
	text := i18n.Tf("%s %d/%d", b.Label, min(b.Done+1, b.Total), b.Total)
	if b.Cur != nil {
		text += i18n.Tf("：%s…", b.Cur.Name)
	}
	wui.Row(c).Gap(5).AlignItems(wui.Center).Shrink(0).Children(func() {
		wui.Spinner(c).Size(11, 11)
		wui.Text(c, text).FontSize(10.5).FontWeight(500).TextColor(t.TextMuted).SingleLine() // 不加 Label：读屏读的就是这段进度
		stop := i18n.Tf("停止全部%s", b.Label)
		squareButton(c, iconClose, stop, stop+i18n.T("（正在跑的那个仓库取消，后面的不跑了）"), 16, iconInline, false, a.StopBatch)
	})
}
