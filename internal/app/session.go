package app

import (
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件按工作区记住上次的样子：打开的文件标签（固定没有、光标、滚到哪）、当前仓库、
// 文件树里展开的仓库和目录、Git 栏里点过箭头的仓库段和收起的分组、展开的"工作树"块、底栏的终端标签（名字、顺序和目录）、三栏的开关和大小。关窗口和窗口切走时随 SaveState 存进 state.json，下次打开
// 同一个工作区文件时 New（展开状态）和 Start（标签）里放回去。历史版本和 Diff 标签也记（按提交的 SHA
// 重开；提交不在了、改动不在了的不再记），终端输出和变基向导的不记。

// sessionKey 是这个工作区在 state.json 里的键：工作区文件的绝对路径；空工作区没有会话，是 ""
// （filepath.Abs("") 是当前目录，会撞上把它当工作区打开过的会话）。
func (a *App) sessionKey() string {
	if a.WsPath == "" {
		return ""
	}
	if abs, err := filepath.Abs(a.WsPath); err == nil {
		return abs
	}
	return filepath.Clean(a.WsPath)
}

// session 是现在的样子。
func (a *App) session() *workspace.Session {
	s := &workspace.Session{Active: -1, Added: slices.Clone(a.St.Added), Hidden: slices.Clone(a.St.Hidden)}
	cur := a.CurrentDoc()
	idx := map[*docs.Doc]int{}
	for _, d := range a.AllDocs() {
		t, ok := sessionTab(d)
		if !ok || len(s.Tabs) == workspace.MaxTabs {
			continue
		}
		if d == cur {
			s.Active = len(s.Tabs)
		}
		idx[d] = len(s.Tabs)
		s.Tabs = append(s.Tabs, t)
	}
	s.Layout = a.layout(idx)
	s.Panels = a.St.Panels()
	if a.Zen { // 禅模式不持久：记的是进之前的三个面板
		s.Panels.Sidebar, s.Panels.RightBar, s.Panels.BottomBar = a.zenWas[0], a.zenWas[1], a.zenWas[2]
	}
	if r := a.Current(); r != nil {
		s.Repo = r.Root
	}
	for _, r := range a.Repos {
		if r.Expanded {
			s.Open = append(s.Open, r.Root)
		}
		if dirs := slices.Sorted(maps.Keys(r.Files.Open)); len(dirs) > 0 {
			if s.Dirs == nil {
				s.Dirs = map[string][]string{}
			}
			s.Dirs[r.Root] = dirs
		}
		if fold := foldNames[r.GitFold]; fold != "" {
			if s.Git == nil {
				s.Git = map[string]string{}
			}
			s.Git[r.Root] = fold
		}
		if groups := slices.Sorted(maps.Keys(r.GroupToggled)); len(groups) > 0 {
			if s.Groups == nil {
				s.Groups = map[string][]string{}
			}
			s.Groups[r.Root] = groups
		}
		if r.WtOpen {
			s.WtOpen = append(s.WtOpen, r.Root)
		}
		if ts := a.sessionTerms(r); ts != nil {
			if s.Terms == nil {
				s.Terms = map[string]*workspace.Terms{}
			}
			s.Terms[r.Root] = ts
		}
	}
	return s
}

// sessionTab 是标签存进会话的样子；不记的（终端输出、变基向导、提交或改动不在了的 Diff 和历史版本）ok 为假。
func sessionTab(d *docs.Doc) (t workspace.Tab, ok bool) {
	t = workspace.Tab{Pinned: d.Pinned, Preview: d.Preview, Top: d.Top}
	switch {
	case d.Kind == docs.File && d.Abs != "" && d.Snapshot == 0:
		t.Path = d.Abs
	case d.Kind == docs.Term && d.Root != "": // 编辑器区的终端：Terms 里记着它（InEditor、Num），这里只记位置
		return workspace.Tab{Path: d.Root, Kind: workspace.TabTerm, Repo: d.Root, TermNum: d.TermNum, Pinned: true}, true
	case (d.Kind == docs.Version || d.Kind == docs.Diff) && d.Root != "" && !d.Gone && d.Info != GoneNote:
		ch := d.Change
		t.Path, t.Repo, t.Change = d.Root, d.Root, &workspace.TabChange{Kind: changeKinds[ch.Kind], Path: ch.Path, Orig: ch.Orig,
			Sub: ch.Sub, Commit: ch.Commit, Parent: ch.Parent, Range: ch.Range}
		if ch.Code != 0 {
			t.Change.Code = string(rune(ch.Code))
		}
		t.Kind = workspace.TabVersion
		if d.Kind == docs.Diff { // Diff 不记滚动：两个编辑器，重开时还会重排
			t.Kind, t.Preview, t.Top = workspace.TabDiff, false, 0
			return t, true
		}
	default:
		return t, false
	}
	t.Enc, t.Hex = string(d.ForcedEnc), d.Hex
	if d.Kind == docs.File {
		t.Lang, t.Indent, t.Copy = d.Lang, d.Indent, d.Copy
	}
	if p, ok := d.Cursor(); ok {
		t.Line, t.Col = p.Line, p.Col
	} else if d.Pending != nil {
		t.Line, t.Col = d.Pending.Line, d.Pending.Col
	}
	if first, last := d.St.VisibleLines(); last > 0 { // 画过：以画出来的为准
		t.Top = first
	}
	return t, true
}

// changeKinds 是 git.Kind 在会话里的写法。
var changeKinds = map[git.Kind]string{git.Staged: "staged", git.Unstaged: "unstaged", git.Untracked: "untracked",
	git.InCommit: "commit", git.Conflict: "conflict"}

// gitTab 重开会话里的一个历史版本或 Diff 标签；仓库不在列表里、不是 Git 仓库、记的东西不全的为 nil。
func (a *App) gitTab(t workspace.Tab) *docs.Doc {
	r, c := a.RepoFor(t.Repo), t.Change
	if r == nil || r.Plain || c == nil || c.Path == "" || len(c.Code) > 1 || !inGit(r.Root) {
		return nil
	}
	ch := git.Change{Path: c.Path, Orig: c.Orig, Sub: c.Sub, Commit: c.Commit, Parent: c.Parent, Range: c.Range}
	if c.Code != "" {
		ch.Code = c.Code[0]
	}
	kind := -1
	for k, name := range changeKinds {
		if name == c.Kind {
			kind = int(k)
		}
	}
	if kind < 0 || git.Kind(kind) == git.InCommit && c.Commit == "" {
		return nil
	}
	ch.Kind = git.Kind(kind)
	switch {
	case t.Kind == workspace.TabVersion && ch.Kind == git.InCommit:
		return a.OpenVersion(r, ch)
	case t.Kind == workspace.TabDiff:
		return a.OpenDiff(r, ch, true)
	}
	return nil
}

// inGit 是 dir 或它上面哪一层有 .git（目录，或工作树、子模块里的文件）：和 git 自己找仓库一样往上找，只 stat、不跑 git。
// 重开会话时仓库的状态还没读，Repo.Plain 还不知道。
func inGit(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		up := filepath.Dir(dir)
		if up == dir {
			return false
		}
		dir = up
	}
}

// groupKeys 是 state.json 里认得的分组键；别的（手改坏的、以后版本加的）读的时候不要。
// 旧的 "untracked"（未跟踪以前单独一组）也不认：读到就扔，不用升格式版本。
var groupKeys = []string{GroupConflict, GroupStaged, GroupUnstaged, GroupStash}

// foldNames 是 Git 栏仓库段的折叠在 state.json 里的写法。
var foldNames = map[int]string{FoldOpen: "open", FoldClosed: "closed"}

// restoreTree 放回当前仓库和文件树的展开状态（New 里，仓库列表建好之后）。
func (a *App) restoreTree(s *workspace.Session) {
	if i := slices.IndexFunc(a.Repos, func(r *Repo) bool { return r.Root == s.Repo }); i >= 0 {
		a.Selected = i
	}
	for _, r := range a.Repos {
		r.Expanded = slices.Contains(s.Open, r.Root)
		for _, d := range s.Dirs[r.Root] {
			r.Files.SetOpen(d, true)
		}
		switch s.Git[r.Root] {
		case "open":
			r.GitFold = FoldOpen
		case "closed":
			r.GitFold = FoldClosed
		}
		r.GroupToggled = nil
		for _, g := range s.Groups[r.Root] {
			if slices.Contains(groupKeys, g) {
				if r.GroupToggled == nil {
					r.GroupToggled = map[string]bool{}
				}
				r.GroupToggled[g] = true
			}
		}
		r.WtOpen = slices.Contains(s.WtOpen, r.Root)
		r.savedTerms = s.Terms[r.Root] // 第一次要显示终端时才按它建标签（initTerms）
	}
}

// restoreTabs 重新打开上次的标签（Start 里）：磁盘上已经没有的文件、仓库不在了的历史版本和 Diff 跳过；光标读完再放，
// 滚动等编辑器第一次排好版再滚（界面里看 Doc.Top）。‹ › 的历史从当前标签重新开始，
// "最近打开"的顺序不因为这次重开而变。编辑器区的终端按号对上仓库的 Terms，对不上的标签丢掉、对不上的终端放回底栏。
func (a *App) restoreTabs() {
	defer a.settleEdTerms()
	s := a.St.Sessions[a.sessionKey()]
	if s == nil || len(s.Tabs) == 0 {
		return
	}
	recent := slices.Clone(a.St.Recent)
	var active *docs.Doc
	opened := make([]*docs.Doc, len(s.Tabs))
	seen := map[string]*docs.Doc{} // 文件标签的路径 → 第一次开的那份：再出现就是它的镜像
	for i, t := range s.Tabs {
		var d *docs.Doc
		if t.Kind == workspace.TabTerm {
			if d = a.restoreTermDoc(t); d != nil {
				opened[i] = d
				if i == s.Active {
					active = d
				}
			}
			continue
		}
		mirror := false
		switch first := seen[t.Path]; {
		case t.Kind != "":
			d = a.gitTab(t)
		case first != nil && !first.Hex && first.Kind == docs.File:
			d, mirror = a.mirrorOf(first), true
		default:
			if fi, err := os.Stat(t.Path); err == nil && !fi.IsDir() {
				d = a.OpenPath(t.Path)
				seen[t.Path] = d
			}
		}
		if d == nil {
			continue
		}
		opened[i] = d
		if !mirror { // 镜像的编码、十六进制跟着第一份
			a.reopenAs(d, docs.Encoding(t.Enc))
			if t.Hex && !d.Hex && (d.Kind == docs.File || d.Kind == docs.Version) { // 照十六进制开（二进制读好了自己就是）
				d.Hex, d.Buf, d.Bitmap = true, nil, nil
				a.readDoc(d, true)
			}
		}
		restoreLook(d, t.Lang, t.Indent)
		d.Pinned = t.Pinned
		d.Preview = t.Preview && (docs.IsMarkdown(d.Name()) || docs.IsSVG(d.Name()))
		if t.Line > 0 || t.Col > 0 {
			d.JumpTo(docs.Jump{Line: t.Line, Col: t.Col})
		}
		d.Top = t.Top
		if i == s.Active {
			active = d
		}
	}
	a.St.Recent = recent
	a.restoreLayout(s.Layout, opened)
	a.nav, a.navPos = nil, 0
	if i := slices.Index(a.Docs, active); i >= 0 {
		a.DocSel = i
	}
	if d := a.CurrentDoc(); d != nil {
		a.visit(d.Key())
		a.syncChangeSel()
	}
}
