package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/snippets"
)

// 这个文件是代码片段的应用一侧：给编辑器补全、认出选了哪一条、跳位会话、命令面板的 ">snip "、设置页的目录。
// 解析和展开在 internal/snippets；Tab / ⇧Tab / Esc 在 ui.docKeys，状态栏在 ui.docStatus，每帧的 SnipTick 在 ui.docContent。

// snipCheckEvery 是多久看一次片段目录变了没有（用到时才看，不常驻监视）。
const snipCheckEvery = time.Second

// SnippetDir 是片段目录：<状态目录>/snippets；没有状态目录（测试、不保存）时为空，只有内置的。
func (a *App) SnippetDir() string {
	if a.StateDir == "" {
		return ""
	}
	return filepath.Join(a.StateDir, "snippets")
}

// Snippets 是读好的片段（内置的和片段目录里的）：第一次用时读，之后用到时最多一秒看一次目录，文件变了重读。
func (a *App) Snippets() *snippets.Set {
	dir := a.SnippetDir()
	switch {
	case a.snips == nil || a.snips.Dir != dir:
		a.ReloadSnippets()
	case time.Since(a.snipCheck) >= snipCheckEvery:
		a.snipCheck = time.Now()
		if a.snips.Stale() {
			a.ReloadSnippets()
		}
	}
	return a.snips
}

// ReloadSnippets 重读片段目录（设置页的"重新加载"）。
func (a *App) ReloadSnippets() {
	a.snips, a.snipCheck = snippets.Load(a.SnippetDir()), time.Now()
}

// OpenSnippetDir 在访达里打开片段目录（设置页的按钮）：没有就建，里面一个 .json 都没有时先复制一份内置的当样板。
func (a *App) OpenSnippetDir() {
	dir := a.SnippetDir()
	if dir == "" {
		return
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(names) == 0 {
		if _, err := snippets.WriteBuiltins(dir); err != nil {
			a.Toast = i18n.Tf("建不了片段目录：%s", err.Error())
			return
		}
		a.ReloadSnippets()
	}
	if err := a.ShowPath(dir); err != nil {
		a.Toast = i18n.Tf("打不开 %s：%s", dir, err.Error())
	}
}

// snipLang 是 d 现在的语言对应的片段文件名（go、python…；纯文本是 ""）：状态栏里手动选的语言也算。
func snipLang(d *docs.Doc) string {
	if l := d.Buf.Language(); l != nil {
		return snippets.LangKey(l.Name)
	}
	return ""
}

// snipIndent 是在 off 插片段时第二行起补的缩进（那一行行首的空白）和一级缩进（片段里行首的 Tab 换成它，跟着文档的缩进设置）。
func (a *App) snipIndent(d *docs.Doc, off int) (indent, unit string) {
	line := d.Buf.Line(d.Buf.Pos(off).Line)
	indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	size, tabs := a.IndentOf(d)
	if tabs {
		return indent, "\t"
	}
	return indent, strings.Repeat(" ", size)
}

// snipEditable 是能不能往 d 里插片段：能改的文件（不是只读、十六进制、设置文档）。
func snipEditable(d *docs.Doc) bool {
	return d != nil && d.Kind == docs.File && d.Editable() && !d.St.ReadOnly
}

// SnippetCompletions 是编辑器要补全时（敲字、⌃Space）给的片段：前缀（不分大小写）以光标前的词开头的；
// 敲字时词至少一个字，⌃Space 时词空着就全给。跳位会话里不给（Tab 留给跳位）。给的同时记下来（SnipOffer），SnipTick 事后认。
func (a *App) SnippetCompletions(d *docs.Doc, req editor.CompletionRequest) {
	start, items, _ := a.snippetCompletionItems(d, req)
	if len(items) > 0 {
		d.St.ShowCompletions(start, items)
	}
}

// snippetItems 是 SnippetCompletions 算的那一批：补全的起点、条目和记下的 SnipOffer（没给片段时 nil）。
// 语言服务器那批补全和这些合在一起交给编辑器，所以分开算。
func (a *App) snippetCompletionItems(d *docs.Doc, req editor.CompletionRequest) (start int, items []editor.Completion, ok bool) {
	d.SnipOffer = nil
	if d.Snip != nil || !snipEditable(d) || !req.Explicit && req.Prefix == "" {
		return 0, nil, false
	}
	start = req.Offset - len(req.Prefix)
	indent, unit := a.snipIndent(d, start)
	low := strings.ToLower(req.Prefix)
	offer := &snippets.Offer{Start: start, Ver: d.Buf.Version()}
	for _, sn := range a.Snippets().For(snipLang(d)) {
		if !strings.HasPrefix(strings.ToLower(sn.Prefix), low) {
			continue
		}
		e := snippets.Expand(sn.Body, indent, unit)
		items = append(items, editor.Completion{Label: sn.Prefix, Detail: sn.Description(), Kind: editor.CompletionSnippet, Insert: e.Text, Documentation: sn.Body})
		offer.Items = append(offer.Items, e)
	}
	if len(items) == 0 {
		return start, nil, false
	}
	d.SnipOffer = offer
	return start, items, true
}

// SnipTick 每帧（画编辑器前）跟上 d：补全给过片段、缓冲区变了，光标前正好是哪一条展开的文字就认作选了它，开跳位会话；
// 光标跑到那个词前面或别的行，这批作废。跳位会话跟着缓冲区挪，改在别处（对不上）或光标出了片段就结束。
func (a *App) SnipTick(d *docs.Doc) {
	if d.Buf == nil {
		d.Snip, d.SnipOffer = nil, nil
		return
	}
	if o := d.SnipOffer; o != nil {
		cur, n := d.St.Cursor(), d.Buf.Len()
		if d.Buf.Version() != o.Ver {
			for _, e := range o.Items {
				if end := o.Start + len(e.Text); cur == end && end <= n && d.Buf.Slice(o.Start, end) == e.Text {
					d.SnipOffer = nil
					a.startSnippet(d, o.Start, e)
					return
				}
			}
		}
		if cur < o.Start || o.Start > n || d.Buf.Pos(cur).Line != d.Buf.Pos(o.Start).Line {
			d.SnipOffer = nil
		}
	}
	if s := d.Snip; s != nil {
		cur := d.St.Selection()
		if !s.Sync(d.Buf.Version(), d.Buf.Len(), d.Buf.Slice) || cur.Start() < s.Start || cur.End() > s.End {
			d.Snip = nil
		}
	}
}

// startSnippet 是在 at 插好了 e.Text 之后：有跳位的开会话、选中 $1 的几处（多光标）；只有 $0 的光标放到 $0（有默认值的选中它）。
func (a *App) startSnippet(d *docs.Doc, at int, e snippets.Expansion) {
	d.Snip = snippets.NewSession(at, e, d.Buf.Len(), d.Buf.Version())
	if d.Snip != nil {
		selectRanges(d, d.Snip.Ranges())
		return
	}
	r := e.Stops[len(e.Stops)-1].Ranges[0]
	d.St.Select(at+r[0], at+r[1])
}

// selectRanges 把几处都选上，第一处是主选区（最后一个是 mygo 的主选区）。
func selectRanges(d *docs.Doc, rs [][2]int) {
	sels := make([]editor.Selection, len(rs))
	for i, r := range rs {
		sels[len(rs)-1-i] = editor.Selection{Anchor: r[0], Head: r[1]}
	}
	d.St.SetSelections(sels...)
}

// SnipJump 是跳位会话里的 Tab（dir 1）/ ⇧Tab（-1）：选中下一个 / 上一个跳位；走到 $0 时光标放过去（有默认值的选中），会话结束。
// 没有会话返回 false（Tab 照编辑器的）。
func (a *App) SnipJump(d *docs.Doc, dir int) bool {
	s := d.Snip
	if s == nil {
		return false
	}
	a.SnipTick(d) // 这一帧刚打的字先对上
	if s = d.Snip; s == nil {
		return false
	}
	last := s.Move(dir)
	rs := s.Ranges()
	if last {
		d.Snip = nil
		if len(rs) == 0 {
			rs = [][2]int{{s.End, s.End}}
		}
		rs = rs[:1]
	}
	selectRanges(d, rs)
	return true
}

// EndSnippet 结束 d 的跳位会话（Esc）。
func (a *App) EndSnippet(d *docs.Doc) {
	if d != nil {
		d.Snip = nil
	}
}

// CanInsertSnippet 是"插入片段…"能不能用：当前标签是能改的文件。
func (a *App) CanInsertSnippet() bool { return snipEditable(a.CurrentDoc()) }

// InsertSnippet 把 sn 展开插到 d 的主选区（选着字的换掉），开跳位会话。
func (a *App) InsertSnippet(d *docs.Doc, sn snippets.Snippet) {
	if !snipEditable(d) {
		return
	}
	sel := d.St.Selection()
	indent, unit := a.snipIndent(d, sel.Start())
	e := snippets.Expand(sn.Body, indent, unit)
	d.SnipOffer = nil
	d.Buf.Edit(editor.Edit{Start: sel.Start(), End: sel.End(), Text: e.Text})
	a.startSnippet(d, sel.Start(), e)
}

// SnippetStatus 是状态栏的"片段 2/3"；没有会话为空。
func SnippetStatus(d *docs.Doc) string {
	if d == nil || d.Snip == nil {
		return ""
	}
	return i18n.Tf("片段 %d/%d", d.Snip.Current(), d.Snip.Count())
}

// SnippetFileOf 是一条片段从哪来，设置页"来源"那一列：内置，或片段目录里的文件名。
func SnippetFileOf(sn snippets.Snippet) string {
	if !sn.File {
		return i18n.T("内置")
	}
	return sn.Lang + ".json"
}

// SnippetDirExists 是片段目录建了没有（设置页提示"还没有，点按钮建"）。
func (a *App) SnippetDirExists() bool {
	dir := a.SnippetDir()
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
