package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是把终端内容开成标签（⇧⌘C）和复制全部输出：终端插件没有键盘选区的 API，要用键盘选一段输出、
// 复制它，就把那一格的屏幕和回滚原样开成主区里一个只读标签，在编辑器里选。标签是那一刻的快照，横幅上能刷新。

// CopyText 写剪贴板；测试里换掉。
var CopyText = func(s string) { mygo.Clipboard.WriteText(s) }

// paneText 是一格终端现在的内容（屏幕和回滚，每行末尾的空格已去掉），末尾的空行不要。
func paneText(p *TermPane) string {
	if p == nil || p.Term == nil {
		return ""
	}
	return strings.TrimRight(p.Term.Text(), "\n")
}

// hasTermOutput 是有键盘的那格终端建好了（"把终端内容开成标签"、"复制全部输出"能不能用）。
func hasTermOutput(a *App) bool {
	_, _, p := a.ActivePane()
	return p != nil && p.Term != nil
}

// OpenTermText 把有键盘的那格终端的内容开成一个只读标签（⇧⌘C）。
func (a *App) OpenTermText() { a.OpenPaneText(a.ActivePane()) }

// OpenPaneText 把仓库 r 的终端标签 tab 里的一格 p 的内容开成一个只读标签，切过去、键盘给它；
// 这一格已经开过的话刷新那个标签。
func (a *App) OpenPaneText(r *Repo, tab *TermTab, p *TermPane) {
	if p == nil || p.Term == nil {
		a.Toast = i18n.T("这个终端还没启动")
		return
	}
	d := docs.NewTermText(r.Root, p.ID, termTextTitle(r, tab))
	if old := a.reuse(d.Key(), true); old != nil {
		d = old
	} else {
		a.addDoc(d, true)
	}
	d.SetTermText(paneText(p), time.Now())
	a.FocusDoc = true
}

// termTextTitle 是终端输出标签的名字：终端 · 仓库；第 2 个起的终端标签带上它的号，改过名的带上名字。
func termTextTitle(r *Repo, tab *TermTab) string {
	switch {
	case tab.Name != "":
		return i18n.Tf("终端 · %s · %s", r.Name, tab.Name)
	case tab.Num <= 1:
		return i18n.Tf("终端 · %s", r.Name)
	}
	return i18n.Tf("终端 %d · %s", tab.Num, r.Name)
}

// termTextPane 是终端输出标签取自的那一格；关掉了为 nil。
func (a *App) termTextPane(d *docs.Doc) *TermPane {
	var found *TermPane
	a.eachPane(func(r *Repo, p *TermPane) {
		if r.Root == d.Root && p.ID == d.Pane {
			found = p
		}
	})
	return found
}

// RefreshTermText 是终端输出标签横幅上的"刷新"：重新取那一格的内容；那一格关了就留着现在的。
func (a *App) RefreshTermText(d *docs.Doc) {
	p := a.termTextPane(d)
	if p == nil || p.Term == nil {
		a.Toast = i18n.T("这个终端已经关了，标签里是关之前的内容")
		return
	}
	d.SetTermText(paneText(p), time.Now())
}

// CanRefreshTermText 是终端输出标签取自的那一格还在。
func (a *App) CanRefreshTermText(d *docs.Doc) bool {
	p := a.termTextPane(d)
	return p != nil && p.Term != nil
}

// copyLines 把 text 写进剪贴板，提示复制了几行。
func (a *App) copyLines(text string) {
	n := docs.CountLines(text)
	if n == 0 {
		a.Toast = i18n.T("终端里没有输出")
		return
	}
	CopyText(text)
	a.Toast = i18n.Tn("已复制 %d 行", n)
}

// CopyTermText 是终端输出标签横幅上的"复制全部"。
func (a *App) CopyTermText(d *docs.Doc) {
	if d.Buf != nil {
		a.copyLines(d.Buf.String())
	}
}

// CopyAllOutput 是"复制全部输出"：有键盘的那格终端的屏幕和回滚，末尾的空行不要。
func (a *App) CopyAllOutput() {
	_, _, p := a.ActivePane()
	a.CopyPaneOutput(p)
}

// CopyPaneOutput 复制一格终端的屏幕和回滚。
func (a *App) CopyPaneOutput(p *TermPane) {
	if p == nil || p.Term == nil {
		a.Toast = i18n.T("这个终端还没启动")
		return
	}
	a.copyLines(paneText(p))
}
