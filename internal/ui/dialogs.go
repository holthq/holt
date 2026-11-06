package ui

import (
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
)

// dialogs 画窗口里的对话框：确认、出错、新建分支、工作树、快速打开 / 命令面板、关标签和退出时的"没保存"、凭据。
// 都走 dialogFrame（命令面板是自己的框，脸对齐）。
func dialogs(a *app.App, c *wui.Context) {
	confirmDialog(a, c)
	alertDialog(a, c)
	if a.Branch.Open {
		branchDialog(a, c)
	}
	if a.WtDialog.Open {
		worktreeDialog(a, c)
	}
	if a.RefDlg.Open {
		refDialog(a, c)
	}
	if a.CloneDlg.Open {
		cloneDialog(a, c)
	}
	palette(a, c)
	closeDialog(a, c)
	leaveDialog(a, c)
	credDialog(a, c) // 凭据框在最上面：git 在等它
}

// confirmDialog 是 a.Ask / a.AskDanger 的确认：问号（危险的是红的警告和红按钮），⏎ 是确认、Esc 和点遮罩是取消。
func confirmDialog(a *app.App, c *wui.Context) {
	q := a.Confirm
	if q == nil {
		return
	}
	kind, btn := dlgQuestion, btnPrimary
	if q.Danger {
		kind, btn = dlgDanger, btnDanger
	}
	yes := false
	dialogFrame(c, &a.ConfirmOpen, dialogOpts{Kind: kind, Title: q.Title, Note: q.Message, Width: dlgConfirm, Hint: q.Button,
		Enter: func() { yes = true }}, nil, func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			a.ConfirmOpen = false
		}
		if dlgButton(c, q.Button, btn, false, true) {
			yes = true
		}
	})
	switch {
	case yes:
		a.Confirm, a.ConfirmOpen = nil, false
		q.Fn() // 可能接着问下一个（又设了 Confirm）
	case !a.ConfirmOpen:
		a.Confirm = nil
	}
}

// alertDialog 是 a.Fail 的出错提示（红的 ✕），一个"好"。认出了 git 的原话时（AlertDetail）说明是一句人话，原话的最后几行等宽小字放在下面、能选中拷走。
func alertDialog(a *app.App, c *wui.Context) {
	if !a.AlertOpen {
		return
	}
	kind := dlgInfo
	if a.AlertErr {
		kind = dlgError
	}
	ok := false
	var body func()
	width := float32(dlgConfirm)
	if a.AlertDetail != "" {
		t := c.Theme()
		width = cloneWidth // 原话里常有地址，宽一档
		body = func() {
			wui.Column(c).Padding(7, 10).Radius(6).Background(t.Text.Alpha(0.05)).Selectable().MinWidth(0).Children(func() {
				wui.Text(c, a.AlertDetail).Font(mono()).FontSize(10.5).LineHeight(1.45).TextColor(t.TextMuted).MinWidth(0)
			})
		}
	}
	dialogFrame(c, &a.AlertOpen, dialogOpts{Kind: kind, Title: a.AlertTitle, Note: a.AlertMsg, Width: width, Hint: i18n.T("好"),
		Single: true, Enter: func() { ok = true }}, body, func() {
		if dlgButton(c, i18n.T("好"), btnPrimary, false, true) {
			ok = true
		}
	})
	if ok {
		a.AlertOpen = false
	}
}

func branchDialog(a *app.App, c *wui.Context) {
	r := a.Branch.Repo
	if r == nil || a.RepoFor(r.Root) != r {
		r = a.Current()
	}
	if r == nil {
		a.Branch = app.BranchDialog{}
		return
	}
	from := r.Status.Branch
	if a.Branch.From != "" {
		from = i18n.Tf("提交 %s", a.Branch.From)
	}
	create := func() {
		name := strings.TrimSpace(a.Branch.Name)
		if name == "" {
			return
		}
		start := a.Branch.From
		a.Branch = app.BranchDialog{}
		a.CreateBranch(r, name, start)
	}
	ok := strings.TrimSpace(a.Branch.Name) != ""
	o := dialogOpts{Title: i18n.T("新建分支"), Note: i18n.Tf("在 %s 里从 %s 新建并切换过去", r.Name, from), Hint: i18n.T("新建")}
	dialogFrame(c, &a.Branch.Open, o, func() {
		dlgField(c, i18n.T("分支名"), "", func() {
			if dlgInput(c, wui.TextInput(c, &a.Branch.Name).Placeholder(i18n.T("分支名")).Label(i18n.T("分支名")).AutoFocus()).Submitted() {
				create()
			}
		})
		if name := strings.TrimSpace(a.Branch.Name); name != "" {
			cmd := "git switch -c " + name
			if a.Branch.From != "" {
				cmd += " " + a.Branch.From
			}
			dlgCommand(c, cmd)
		}
	}, func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			a.Branch = app.BranchDialog{}
		}
		if dlgButton(c, i18n.T("新建"), btnPrimary, !ok, false) {
			create()
		}
	})
	if !a.Branch.Open {
		a.Branch = app.BranchDialog{}
	}
}

// closeDialog 是关一个有没保存改动的标签时的确认：⏎ 保存、Esc 取消。
func closeDialog(a *app.App, c *wui.Context) {
	d := a.CloseReq
	if d == nil {
		return
	}
	choice := -1
	o := dialogOpts{Kind: dlgWarning, Title: i18n.Tf("保存对 %s 的修改？", d.Title), Note: i18n.T("不保存的话这些改动就丢了。"),
		Width: dlgConfirm, Hint: i18n.T("保存"), Enter: func() { choice = 2 }}
	dialogFrame(c, &a.CloseOpen, o, nil, func() {
		for i, b := range []string{i18n.T("取消"), i18n.T("不保存"), i18n.T("保存")} {
			k := btnPlain
			if i == 2 {
				k = btnPrimary
			}
			if dlgButton(c, b, k, false, i == 2) {
				choice = i
			}
		}
	})
	switch choice {
	case 2:
		a.CloseReq, a.CloseOpen = nil, false
		if a.WriteDoc(d, true) == nil {
			a.RemoveDoc(d)
		}
	case 1:
		a.CloseReq, a.CloseOpen = nil, false
		a.RemoveDoc(d)
	case 0:
		a.CloseReq, a.CloseOpen = nil, false
	}
	if !a.CloseOpen {
		a.CloseReq = nil
	}
}

// leaveDialog 是退出或换工作区之前问的（app.Leave）：没保存的文件、在跑的 git 写命令、终端里在跑的程序，一步一个。
// 按钮的最后一个是主按钮（⏎），Esc 和点遮罩答 wui.AlertDismissed（和取消一样）。
func leaveDialog(a *app.App, c *wui.Context) {
	l := a.Leave
	if l == nil || !l.Open {
		return
	}
	title, msg, buttons := a.LeaveQuestion()
	choice := -1
	o := dialogOpts{Kind: dlgWarning, Title: title, Note: msg, Width: dlgConfirm}
	if n := len(buttons); n > 0 {
		o.Hint, o.Enter = buttons[n-1], func() { choice = n - 1 }
	}
	dialogFrame(c, &l.Open, o, nil, func() {
		for i, b := range buttons {
			k := btnPlain
			if i == len(buttons)-1 {
				k = btnPrimary
			}
			if dlgButton(c, b, k, false, i == len(buttons)-1) {
				choice = i
			}
		}
	})
	switch {
	case choice >= 0:
		l.Open = false
		a.LeaveAnswer(choice)
	case !l.Open:
		a.LeaveAnswer(wui.AlertDismissed)
	}
}
