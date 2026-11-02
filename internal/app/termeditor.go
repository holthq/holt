package app

import (
	"slices"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是开在编辑器区的终端：一个终端标签要么在底栏（Repo.Terms）、要么在编辑器区（Repo.EdTerms），
// 编辑器区的各是一个 docs.Term 标签（Root + TermNum 认回 TermTab），画法和底栏同一套（ui.termNode）。
// 挪来挪去 shell 不重启：同一个 TermTab 换个地方画。会话里记在 Terms 里（InEditor、Num）和标签列表里（TabTerm）；
// 最近打开、⇧⌘T 不记。

// NewEditorTerminal 在仓库 r 里开一个终端标签，开在编辑器区有键盘的那组，键盘给它（⌃⇧⌘`）。
func (a *App) NewEditorTerminal(r *Repo) {
	a.initTerms(r) // 号接着底栏的数
	tab := a.newTermTab(r, "", "")
	tab.InEditor = true
	r.EdTerms = append(r.EdTerms, tab)
	a.addDoc(docs.NewTerm(r.Root, tab.Num, r.TermLabel(tab)), true)
	a.FocusTermTab(tab)
	a.SaveState()
}

// MoveTermToEditor 把底栏的终端标签挪到编辑器区（底栏标签的右键菜单）：shell 不重启，键盘给它。
func (a *App) MoveTermToEditor(r *Repo, tab *TermTab) {
	i := slices.Index(r.Terms, tab)
	if i < 0 {
		return
	}
	r.Terms = slices.Delete(r.Terms, i, i+1)
	if r.TermSel > i || r.TermSel >= len(r.Terms) {
		r.TermSel = max(r.TermSel-1, 0)
	}
	if a.Rename.Tab == tab {
		a.Rename = TermRename{}
	}
	tab.InEditor = true
	r.EdTerms = append(r.EdTerms, tab)
	a.addDoc(docs.NewTerm(r.Root, tab.Num, r.TermLabel(tab)), true)
	a.FocusTermTab(tab)
	a.SaveState()
}

// MoveTermToBottom 把编辑器区的终端标签 d 挪回底栏（标签的右键菜单、工具栏）：排在底栏最后、切过去，shell 不重启。
func (a *App) MoveTermToBottom(d *docs.Doc) {
	r, tab := a.TermTabOf(d)
	if tab == nil {
		return
	}
	r.EdTerms = slices.DeleteFunc(r.EdTerms, func(t *TermTab) bool { return t == tab })
	tab.InEditor = false
	if a.Rename.Tab == tab {
		a.Rename = TermRename{}
	}
	a.RemoveDoc(d) // 已经不在 EdTerms 里了：只关标签
	r.Terms = append(r.Terms, tab)
	r.TermSel = len(r.Terms) - 1
	a.showTerm(r)
	a.SaveState()
}

// TermTabOf 是编辑器区终端标签 d 的仓库和终端标签；d 不是终端标签、对不上（仓库移除了、终端关了）为 nil。
func (a *App) TermTabOf(d *docs.Doc) (*Repo, *TermTab) {
	if d == nil || d.Kind != docs.Term {
		return nil, nil
	}
	r := a.RepoFor(d.Root)
	if r == nil && a.home != nil && a.home.Root == d.Root { // 空工作区时开的（家目录的终端不记进会话）
		r = a.home
	}
	if r == nil {
		return nil, nil
	}
	for _, t := range r.EdTerms {
		if t.Num == d.TermNum {
			return r, t
		}
	}
	return nil, nil
}

// termDocOf 是编辑器区终端标签 tab 的标签；没有为 nil。
func (a *App) termDocOf(r *Repo, tab *TermTab) *docs.Doc {
	for _, d := range a.AllDocs() {
		if d.Kind == docs.Term && d.Root == r.Root && d.TermNum == tab.Num {
			return d
		}
	}
	return nil
}

// syncTermDoc 让编辑器区终端标签的标签名跟上（改名、第一格换了终端配置）。
func (a *App) syncTermDoc(r *Repo, tab *TermTab) {
	if !tab.InEditor {
		return
	}
	if d := a.termDocOf(r, tab); d != nil {
		d.Title = r.TermLabel(tab)
	}
}

// closeEdTab 关掉编辑器区的终端标签（不问）：连它的标签一起。
func (a *App) closeEdTab(r *Repo, tab *TermTab) {
	if d := a.termDocOf(r, tab); d != nil {
		a.RemoveDoc(d) // RemoveDoc 再 killEdTab
		return
	}
	a.killEdTab(r, tab)
}

// killEdTab 关掉编辑器区终端标签的 shell、从 EdTerms 拿掉（关它的标签时，RemoveDoc）。
func (a *App) killEdTab(r *Repo, tab *TermTab) {
	i := slices.Index(r.EdTerms, tab)
	if i < 0 {
		return
	}
	r.EdTerms = slices.Delete(r.EdTerms, i, i+1)
	for _, p := range tab.Panes() {
		a.killPane(p)
	}
	if a.Rename.Tab == tab {
		a.Rename = TermRename{}
	}
	if a.edTab == tab {
		a.edTab, a.edRepo = nil, nil
	}
	if a.FocusTab == tab {
		a.FocusTerm, a.FocusTab = false, nil
	}
	a.SaveState()
}

// termBusy 是编辑器区的终端标签 d 里有格前台在跑命令（一起关好几个标签时留着它，不问）。
func (a *App) termBusy(d *docs.Doc) bool {
	_, tab := a.TermTabOf(d)
	if tab == nil {
		return false
	}
	return slices.ContainsFunc(tab.Panes(), func(p *TermPane) bool { return p.Job() != "" })
}

// busyToast 在一起关标签的提示 s 后面补上留着的、有命令在跑的终端标签数。
func busyToast(s string, busy int) string {
	if busy == 0 {
		return s
	}
	msg := i18n.Tn("%d 个终端标签有命令在运行，留着了", busy)
	if s == "" {
		return msg
	}
	return s + " · " + msg
}

// restoreTermDoc 重开会话里的一个编辑器区终端标签：仓库不在了、Terms 里对不上号的为 nil。不启动 shell。
func (a *App) restoreTermDoc(t workspace.Tab) *docs.Doc {
	r := a.RepoFor(t.Repo)
	if r == nil || t.TermNum <= 0 {
		return nil
	}
	a.initTerms(r)
	for _, tab := range r.EdTerms {
		if tab.saved == t.TermNum {
			tab.saved = 0
			d := docs.NewTerm(r.Root, tab.Num, r.TermLabel(tab))
			a.addDoc(d, true)
			return d
		}
	}
	return nil
}

// settleEdTerms 在重开标签之后把会话里记着、却没对上标签的编辑器区终端标签放回底栏（标签超了上限、布局丢了）：
// 不然它的 shell 没地方画。
func (a *App) settleEdTerms() {
	for _, r := range a.Repos {
		if s := r.savedTerms; s != nil && slices.ContainsFunc(s.Tabs, func(t workspace.TermTab) bool { return t.InEditor }) {
			a.initTerms(r)
		}
		for _, tab := range slices.Clone(r.EdTerms) {
			if tab.saved == 0 {
				continue
			}
			tab.saved, tab.InEditor = 0, false
			r.EdTerms = slices.DeleteFunc(r.EdTerms, func(t *TermTab) bool { return t == tab })
			r.Terms = append(r.Terms, tab)
		}
	}
}
