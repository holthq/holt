package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是贮藏（git stash）：贮藏改动、Git 栏里每个仓库段的贮藏列表上的应用 / 弹出 / 丢弃、展开看文件、只应用其中几个文件，
// 和仓库的网页（GitHub、GitLab、Bitbucket、Gitea 或通用，设置里能按主机指定）。列表本身是 git.ReadExtra 只读读来的。

// Stash 贮藏改动。
func (a *App) Stash(r *Repo) {
	if r.Status.Staged+r.Status.Unstaged == 0 {
		a.Toast = i18n.T("没有可贮藏的改动")
		return
	}
	a.RunOp(r, i18n.T("贮藏"), "stash", "push", "-q")
}

// StashPop 弹出最新的贮藏。
func (a *App) StashPop(r *Repo) {
	if len(r.Extra.Stashes) == 0 {
		a.Toast = i18n.T("没有贮藏")
		return
	}
	a.RunOp(r, i18n.T("弹出贮藏"), "stash", "pop", "-q")
}

// StashApply 把一条贮藏的改动放回工作区，贮藏留着。
func (a *App) StashApply(r *Repo, s git.Stash) {
	a.RunOp(r, i18n.T("应用贮藏"), "stash", "apply", "-q", s.Ref)
}

// StashPopRef 把一条贮藏的改动放回工作区，成功了就删掉它（有冲突时 git 留着它）。
func (a *App) StashPopRef(r *Repo, s git.Stash) {
	a.RunOp(r, i18n.T("弹出贮藏"), "stash", "pop", "-q", s.Ref)
}

// StashDrop 丢掉一条贮藏；先问。问完再核对一次 stash@{n} 还指着它（中间别处贮藏 / 弹出过，序号会挪）。
func (a *App) StashDrop(r *Repo, s git.Stash) {
	a.AskDanger(i18n.Tf("丢弃贮藏 %s？", s.Ref), i18n.Tf("“%s”里的改动会丢掉，不能撤销。", s.Message), i18n.T("丢弃"), func() {
		up := a.Update
		go func() {
			out, err := git.Run(r.Root, "rev-parse", "-q", "--verify", s.Ref)
			up(func() {
				if err != nil || strings.TrimSpace(string(out)) != s.Hash {
					a.Fail(i18n.T("丢弃贮藏"), errors.New(i18n.T("贮藏列表刚变过，看一眼再丢")))
					a.RefreshRepo(r, WhyOp)
					return
				}
				a.RunOp(r, i18n.T("丢弃贮藏"), "stash", "drop", "-q", s.Ref)
			})
		}()
	})
}

// StashFileRows 是右键贮藏里的文件 cr 时“应用”管哪些文件：它在两行以上的选中里，就是选中的、同一条贮藏里的文件；不然只有它。
func (a *App) StashFileRows(cr ChangeRow) []git.Change {
	if a.GitChosen.Has(cr.Key()) {
		var out []git.Change
		for _, o := range a.GitRows() {
			if o.Kind == RowStashFile && o.Repo == cr.Repo && o.Stash.Hash == cr.Stash.Hash && a.GitChosen.Has(o.Key()) {
				out = append(out, o.Change)
			}
		}
		if len(out) > 1 {
			return out
		}
	}
	return []git.Change{cr.Change}
}

// ApplyStashFiles 只把贮藏里的这几个文件放回工作区（git restore --source --worktree：暂存区不动，贮藏留着）。
// 跟踪的文件从贮藏的提交取，没跟踪的从它的第三个父提交取（Change.Commit 已经分好）；贮藏里删掉的文件不管，
// 改名的只放回新名字。工作区里这些文件已经有改动的先问，会覆盖掉。
func (a *App) ApplyStashFiles(r *Repo, files []git.Change) {
	groups := map[string][]string{}
	var order, dirty []string
	n := 0
	for _, ch := range files {
		if ch.Code == 'D' {
			continue
		}
		if _, ok := groups[ch.Commit]; !ok {
			order = append(order, ch.Commit)
		}
		groups[ch.Commit] = append(groups[ch.Commit], ch.Path)
		n++
		if slices.ContainsFunc(r.Status.Changes, func(c git.Change) bool { return c.Path == ch.Path }) {
			dirty = append(dirty, ch.Path)
		}
	}
	if n == 0 {
		return
	}
	run := func() {
		for i, c := range order {
			var then func(string)
			if i == len(order)-1 {
				then = func(string) { a.Toast = i18n.Tn("已应用 %d 个文件", n) }
			}
			a.RunOpThen(r, i18n.T("应用贮藏里的文件"), then, append([]string{"restore", "--source=" + c, "--worktree", "--"}, groups[c]...)...)
		}
	}
	if len(dirty) == 0 {
		run()
		return
	}
	a.AskDanger(i18n.T("覆盖工作区的改动？"), i18n.Tf("会覆盖工作区里 %s 的改动，不能撤销。", i18n.List(dirty)), i18n.T("应用改动"), run)
}

// ToggleStash 展开 / 收起 Git 栏里的一条贮藏（点它、回车、点箭头、右键）：展开时在后台读它改了哪些文件（只读），
// 下面一行一个；一个仓库同时只展开一条。Git 栏的选中停到这条贮藏上（点它的时候列表也在选它，别抢回去）。
func (a *App) ToggleStash(r *Repo, s git.Stash) {
	a.gitWt, a.gitStash, a.gitStashDoc = "", s.Hash, a.CurrentDoc()
	if r.StashOpen == s.Hash {
		r.closeStash()
		a.syncChangeSel()
		return
	}
	r.StashOpen, r.StashFiles, r.StashFilesFor, r.StashLoading = s.Hash, nil, "", true
	a.syncChangeSel()
	up := a.Update
	go func() {
		fs, err := git.StashFiles(r.ctx, r.Root, s)
		up(func() {
			if r.StashOpen != s.Hash || r.gone() { // 读的时候收起了、换了一条、移除了仓库
				return
			}
			r.StashLoading = false
			if err != nil {
				r.closeStash()
				a.syncChangeSel()
				a.Fail(i18n.T("读贮藏的文件失败"), err)
				return
			}
			r.StashFiles, r.StashFilesFor = fs, s.Hash
			a.syncChangeSel()
		})
	}()
}

// closeStash 收起展开着的贮藏。
func (r *Repo) closeStash() {
	r.StashOpen, r.StashFiles, r.StashFilesFor, r.StashLoading = "", nil, "", false
}

// syncStashOpen 在重读了贮藏列表之后：展开着的那条不在了（弹出、丢弃）就收起。
func (r *Repo) syncStashOpen() {
	if r.StashOpen != "" && !slices.ContainsFunc(r.Extra.Stashes, func(s git.Stash) bool { return s.Hash == r.StashOpen }) {
		r.closeStash()
	}
}

// InStash 是 ch 是展开的贮藏里的文件（贮藏的提交不推送，网页上没有）。
func (r *Repo) InStash(ch git.Change) bool {
	return ch.Kind == git.InCommit && slices.ContainsFunc(r.StashFiles, func(f git.Change) bool { return f.Commit == ch.Commit })
}

// OpenWeb 用浏览器打开仓库在托管网站上的一个网页。
func (a *App) OpenWeb(u string) {
	if err := a.OpenURL(u); err != nil {
		a.Fail(i18n.T("打开网页失败"), err)
	}
}

// WebOf 是这个仓库的网页：从远程地址认出来的风格，设置 → Git 的"网页风格"表里给这台主机指定了就按那个。
func (a *App) WebOf(r *Repo) git.Web {
	w := r.Extra.Web
	return w.With(git.WebKind(a.St.Settings.WebKindFor(w.Host)))
}

// WebLines 是编辑器右键"在 … 打开这几行"的网页：选区（没选就是光标那行）在网页上的那几行，钉在 WebRef。
// 文件没保存、工作区里有改动、没跟踪时行号对不上网页，ok 为 false（菜单项灰）；没有网页时 u 也是空的。
func (a *App) WebLines(d *docs.Doc) (u string, ok bool) {
	r := a.RepoOf(d)
	if r == nil || d.Kind != docs.File || d.Rel == "" || !r.Extra.Web.OK() {
		return "", false
	}
	sel := d.St.Selection()
	from, to := min(sel.Anchor, sel.Head), max(sel.Anchor, sel.Head)
	l0, l1 := d.Buf.Pos(from).Line, d.Buf.Pos(to).Line
	if l1 > l0 && d.Buf.Pos(to).Col == 0 {
		l1-- // 选到下一行行首的不算那一行
	}
	u = a.WebOf(r).Lines(r.WebRef(), d.Rel, l0+1, l1+1)
	dirty := slices.ContainsFunc(r.Status.Changes, func(ch git.Change) bool { return ch.Path == d.Rel })
	return u, !d.Modified() && !dirty
}

// WebRef 是在网页上看工作区里的文件时用的版本：HEAD 推送过就钉在它上面，否则是远程的默认分支。
func (r *Repo) WebRef() string {
	if r.Extra.HeadPushed() {
		return r.Extra.Head
	}
	return "HEAD"
}
