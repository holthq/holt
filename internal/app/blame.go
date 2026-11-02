package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// blame 装订线（⌃⌘B）：文件、历史版本标签左边一列，连着几行是同一次提交改的就归成一段，标出是谁、什么时候、哪次提交。
// 只读（git blame --porcelain），按标签开关，不进 state.json。界面在 ui/blame.go。
// 还有行内 blame（设置 Git 页，所有标签一起开关）：光标所在行的行尾淡淡一句谁、什么时候、哪次提交，读的是同一份。

// blameMaxHistPages 是在历史里找 blame 的那次提交时最多往下多读几页。
const blameMaxHistPages = 3

// CanBlame 是这个标签能开 blame：Git 仓库里的文件或历史版本（读好了、不是图片、二进制和大文件）。
func (a *App) CanBlame(d *docs.Doc) bool {
	if d == nil || d.Root == "" || d.Rel == "" || d.Buf == nil || d.Bitmap != nil || d.Info != "" || d.Big || d.Hex {
		return false
	}
	if d.Kind != docs.File && d.Kind != docs.Version {
		return false
	}
	r := a.RepoOf(d)
	return r != nil && !r.Plain
}

// ToggleBlame 开关当前标签的 blame 装订线。
func (a *App) ToggleBlame() { a.ToggleDocBlame(a.CurrentDoc()) }

// ToggleDocBlame 开关 d 的 blame 装订线（编辑器右键的那一项不一定是当前标签）。关掉时（行内 blame 也没开）扔掉读好的（再开重读）。
func (a *App) ToggleDocBlame(d *docs.Doc) {
	if !a.CanBlame(d) {
		return
	}
	d.ShowBlame = !d.ShowBlame
	if !a.wantBlame(d) {
		dropBlame(d)
	}
}

// wantBlame 是 d 要读 blame：开着装订线，或开着行内 blame 的文件标签。
func (a *App) wantBlame(d *docs.Doc) bool {
	return d.ShowBlame || a.St.Settings.InlineBlame && d.Kind == docs.File
}

func dropBlame(d *docs.Doc) {
	d.Blame, d.BlameErr, d.BlameFor = nil, "", docs.BlameKey{}
}

// ToggleInlineBlame 开关行内 blame（命令面板、视图菜单、编辑器右键；和设置 Git 页那一项是同一个，所有窗口一起）。
func (a *App) ToggleInlineBlame() {
	a.St.Settings.InlineBlame = !a.St.Settings.InlineBlame
	a.SettingsChanged()
}

// dropInlineBlames 在关掉行内 blame 后扔掉没开装订线的标签读好的 blame（设置改了之后调）。行尾那一条下一帧画它的时候拿掉（SyncInlineBlame）。
func (a *App) dropInlineBlames() {
	for _, d := range a.AllDocs() {
		if !a.wantBlame(d) && (d.Blame != nil || d.BlameErr != "") {
			dropBlame(d)
		}
	}
}

// inlineBlameMax 是行内 blame 里提交摘要最多几个字，再长截断加"…"。
const inlineBlameMax = 60

// SyncInlineBlame 每帧画编辑器前调（行内 blame）：键盘所在（current）的文件标签、开着行内 blame 时按需读 blame，
// 在光标所在行（多光标是最新的那个，和状态栏的行列一样）的行尾放一条"作者，多久前 · 摘要"。别的标签、关掉了、
// 改了没存或正在重读（行号可能对不上，免得说错人）、读不了、太大都不放（装订线那边有说明）。
// 和上一帧一样就不再交给编辑器（SetInlays 每次都重排那一行）。
func (a *App) SyncInlineBlame(d *docs.Doc, current bool) {
	var in editor.Inlay
	if current && a.St.Settings.InlineBlame && d.Kind == docs.File && a.CanBlame(d) {
		a.EnsureBlame(d)
		in = inlineBlame(d)
	}
	if in == d.BlameInlay {
		return
	}
	d.BlameInlay = in
	if in.Text == "" {
		d.St.SetInlays(nil)
		return
	}
	d.St.SetInlays([]editor.Inlay{in})
}

// inlineBlame 是 d 光标所在行行尾的那一条；Text 为空 = 不放。
func inlineBlame(d *docs.Doc) editor.Inlay {
	b := d.Blame
	if b == nil || d.BlameLoading || BlameStale(d) {
		return editor.Inlay{}
	}
	line := d.St.CursorPos().Line
	if line < 0 || line >= len(b.Lines) {
		return editor.Inlay{}
	}
	cm := b.Commits[b.Lines[line].Hash]
	if cm == nil {
		return editor.Inlay{}
	}
	text := i18n.T("未提交的改动")
	if !cm.Uncommitted() {
		sum := []rune(cm.Summary)
		if len(sum) > inlineBlameMax {
			sum = append(sum[:inlineBlameMax], '…')
		}
		comma := "，" // 没有汉字的串不进翻译表，英文界面换成半角
		if i18n.Lang() == "en" {
			comma = ", "
		}
		text = cm.Author + comma + git.Ago(cm.Time) + " · " + string(sum)
	}
	// 和行里的字隔开四个空格；不能有 Tab（编辑器按制表位排，宽度会跟着列变）
	text = "    " + strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, text)
	return editor.Inlay{Offset: d.Buf.Offset(editor.Pos{Line: line, Col: len(d.Buf.Line(line))}), Text: text}
}

// blameKey 是这个标签现在的内容算 blame 该用的那一版：重读、保存（缓冲区版本变了）、提交或切分支（HEAD 变了）都不一样。
func blameKey(r *Repo, d *docs.Doc) docs.BlameKey {
	k := docs.BlameKey{Gen: d.Gen, Ver: d.Buf.Version()}
	if d.Kind == docs.File {
		k.Head = r.Extra.Head
	}
	return k
}

// BlameStale 是装订线上画的 blame 对不上现在的内容：改了没保存（行号可能错开）。
func BlameStale(d *docs.Doc) bool {
	return d.Blame != nil && d.Modified()
}

// EnsureBlame 在开着 blame（装订线或行内）的标签上按需（重）读：界面每帧画装订线前、SyncInlineBlame 里调。改了没保存时不重读（git blame 读的是
// 磁盘上的那份），接着用上一次的、画淡一点；一次都还没读过（改着的时候才打开）就先读磁盘上的那份。
func (a *App) EnsureBlame(d *docs.Doc) {
	if !a.wantBlame(d) || d.Loading || d.BlameLoading || !a.CanBlame(d) {
		return
	}
	dirty := d.Modified()
	if dirty && d.BlameFor != (docs.BlameKey{}) {
		return
	}
	r := a.RepoOf(d)
	key := blameKey(r, d)
	if dirty {
		key.Ver = 0 // 读的是磁盘上的，不是这一版：存了以后要重读
	}
	if key == d.BlameFor {
		return
	}
	d.BlameFor = key
	if d.Buf.Len() > git.BlameMaxBytes || d.Buf.LineCount() > git.BlameMaxLines {
		d.Blame, d.BlameErr = nil, blameErrText(git.ErrBlameTooBig)
		return
	}
	d.BlameLoading = true
	root, commit, rel, ctx := r.Root, d.Commit, d.Rel, r.ctx
	up := a.Update
	go func() {
		b, err := git.ReadBlame(ctx, root, commit, rel)
		up(func() {
			d.BlameLoading = false
			if r.gone() || !a.wantBlame(d) || errors.Is(err, git.ErrCanceled) {
				return
			}
			if err != nil {
				d.Blame, d.BlameErr = nil, blameErrText(err)
				return
			}
			d.Blame, d.BlameErr = b, ""
		})
	}()
}

// blameErrText 是读不了 blame 时装订线顶上的说明。
func blameErrText(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, git.ErrBlameTooBig):
		return i18n.Tf("文件太大（超过 %d MB 或 %d 行），不算 blame", git.BlameMaxBytes>>20, git.BlameMaxLines)
	case strings.Contains(msg, "no such path"), strings.Contains(msg, "no such ref"):
		return i18n.T("这个文件还没提交过，没有 blame")
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return i18n.Tf("读 blame 失败：%s", msg)
}

// BlameCommits 是 blame 里有几次提交（不算还没提交的改动）。
func BlameCommits(b *git.Blame) int {
	n := len(b.Commits)
	if b.Commits[git.ZeroHash] != nil {
		n--
	}
	return n
}

// ShowInHistory 在右栏"当前仓库"的历史里选中、展开这次提交（单击 blame 的一段）。还没读到就往下多读几页
// （最多 blameMaxHistPages 页），还没有就说一声。
func (a *App) ShowInHistory(r *Repo, hash string) {
	a.UseRepo(r)
	if a.St.HistAll || !a.St.RightBar {
		a.St.HistAll, a.St.RightBar = false, true
		a.SaveState()
	}
	h := &r.Hist
	h.want, h.wantLeft = hash, blameMaxHistPages
	if !h.Loaded && !h.Loading {
		a.LoadHistory(r, true)
		return
	}
	a.findWanted(r)
}

// findWanted 接着找 ShowInHistory 要的那次提交：读完一页历史后也调。
func (a *App) findWanted(r *Repo) {
	h := &r.Hist
	if h.want == "" || h.Loading {
		return
	}
	if i := slices.IndexFunc(h.Commits, func(c git.Commit) bool { return c.Hash == h.want }); i >= 0 {
		h.want = ""
		setHistSel(r, i, -1)
		a.OnHistSelected(r)
		a.HistList.ScrollIntoView(h.Sel)
		return
	}
	if h.Err != nil || h.Done || h.wantLeft <= 0 {
		h.want = ""
		if h.Err == nil {
			a.Toast = i18n.T("这次提交太早，历史里没读到")
		}
		return
	}
	h.wantLeft--
	a.LoadHistory(r, false)
}

// blamePath 是这个文件在那次提交里的路径（改过名时和标签上的不一样）。
func blamePath(d *docs.Doc, c *git.BlameCommit) string {
	if c.Path != "" {
		return c.Path
	}
	return d.Rel
}

// BlameDiff 打开这次提交里这个文件的 diff（blame 一段的右键菜单）。第一次加进来的文件是新增。
func (a *App) BlameDiff(d *docs.Doc, c *git.BlameCommit) {
	r := a.RepoOf(d)
	if r == nil || c.Uncommitted() {
		return
	}
	ch := git.Change{Kind: git.InCommit, Commit: c.Hash, Parent: c.Previous, Path: blamePath(d, c), Code: 'M'}
	switch {
	case c.Previous == "":
		ch.Code = 'A'
	case c.PrevPath != "" && c.PrevPath != ch.Path:
		ch.Code, ch.Orig = 'R', c.PrevPath
	}
	a.OpenDiff(r, ch, true)
}

// BlamePrevious 只读地打开这一段改之前的那一版（blame 一段的右键菜单"查看上一版"）；第一次加进来的没有上一版。
func (a *App) BlamePrevious(d *docs.Doc, c *git.BlameCommit) {
	r := a.RepoOf(d)
	if r == nil || c.Uncommitted() || c.Previous == "" {
		return
	}
	rel := c.PrevPath
	if rel == "" {
		rel = blamePath(d, c)
	}
	a.OpenVersion(r, git.Change{Kind: git.InCommit, Commit: c.Previous, Path: rel, Code: 'M'})
}

// CopyBlameHash 把这次提交的完整哈希复制到剪贴板。
func (a *App) CopyBlameHash(c *git.BlameCommit) {
	CopyText(c.Hash)
	a.Toast = i18n.Tf("已复制 %s", c.Short)
}
