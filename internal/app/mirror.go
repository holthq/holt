package app

import (
	"slices"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 同一个文件在两组里开（镜像标签）：view.openInOtherGroup（⇧⌘\）在另一组里再开一份，和正本共用缓冲区、
// 对比基准，光标、滚动、折叠、查找、预览各自的。改动在同一个缓冲区里，两边自然一起变；读盘、保存、外部改动、
// 换编码 / 语言 / 缩进 / 换行只落在一份上，落完 relink 让别的几份跟上（docs.Doc.Share）。打开文件（文件树、⌘P、跳转）
// 找的是正本（Copy 为 0），镜像只从这条命令来。

// siblings 是和 d 同一个文件的所有份（正本和镜像，含 d 自己），按组的顺序；d 不是文件标签为 nil。
func (a *App) siblings(d *docs.Doc) []*docs.Doc {
	if d == nil || d.Kind != docs.File {
		return nil
	}
	k := d.Key()
	k.Copy = 0
	var out []*docs.Doc
	for _, o := range a.AllDocs() {
		if ok := o.Key(); o.Kind == docs.File {
			if ok.Copy = 0; ok == k {
				out = append(out, o)
			}
		}
	}
	return out
}

// hasCopy 是 d 还有别的份开着（关它不算真的关：不问没保存的改动、⇧⌘T 不记）。
func (a *App) hasCopy(d *docs.Doc) bool { return len(a.siblings(d)) > 1 }

// relink 让 d 的别的几份跟上 d：d 重读（换了缓冲区）、保存、冲突、换了对比基准 / 编码 / 语言 / 缩进 / 换行之后。
func (a *App) relink(d *docs.Doc) {
	for _, o := range a.siblings(d) {
		if o != d {
			o.Share(d)
		}
	}
}

// CanMirror 是 d 能不能在另一组里再开一份：读好了的文本文件（不是十六进制、图片、Diff、历史版本、终端文字）。
func (a *App) CanMirror(d *docs.Doc) bool {
	return d != nil && d.Kind == docs.File && d.Buf != nil && !d.Hex && !d.Loading && a.GroupOf(d) != nil
}

// otherGroup 是再开一份去的那组：from 后面那组，from 是最后一组就是它前面那组；只有一组为 nil（要向右分出一组）。
func (a *App) otherGroup(from *Group) *Group {
	gs := a.Groups()
	switch i := slices.Index(gs, from); {
	case len(gs) < 2 || i < 0:
		return nil
	case i+1 < len(gs):
		return gs[i+1]
	default:
		return gs[i-1]
	}
}

// OpenMirror 在另一组里再开一份 d（view.openInOtherGroup、标签和编辑器的右键菜单）：去 otherGroup，只有一组时向右分出一组
// （太小就提示、不开）；那组里已经有这个文件的一份就切过去。新的一份固定，光标、滚动照 d 的，键盘跟过去。
func (a *App) OpenMirror(d *docs.Doc) *docs.Doc {
	if !a.CanMirror(d) {
		return nil
	}
	from := a.GroupOf(d)
	to := a.otherGroup(from)
	if to == nil && !from.roomFor(true) {
		a.Toast = i18n.T("这一组太小，分不开了")
		return nil
	}
	sibs := a.siblings(d)
	if to != nil {
		ds, _ := a.GroupDocs(to)
		if i := slices.IndexFunc(ds, func(o *docs.Doc) bool { return slices.Contains(sibs, o) }); i >= 0 {
			a.SelectIn(to, i)
			return ds[i]
		}
	}
	m := &docs.Doc{Kind: docs.File, StickyCur: d.StickyCur}
	m.Share(d)
	for _, o := range sibs {
		m.Copy = max(m.Copy, o.Copy+1)
	}
	p := d.St.CursorPos()
	m.JumpTo(docs.Jump{Line: p.Line, Col: p.Col})
	if first, last := d.St.VisibleLines(); last > 0 {
		m.Top = first // 编辑器排过一次版后界面滚过去（同会话恢复）
	}
	ds, sel := a.GroupDocs(from) // 先放在 d 后面，再搬过去（MoveDocTo / splitOff 管键盘、‹ ›、会话）
	i := slices.Index(ds, d)
	if sel > i {
		sel++
	}
	a.setGroupDocs(from, slices.Insert(ds, i+1, m), sel)
	if to == nil {
		a.splitOff(m, from, SideRight)
	} else {
		a.MoveDocTo(m, to, -1)
	}
	return m
}

// OpenMirrorCurrent 是 ⇧⌘\：当前标签在另一组里再开一份。
func (a *App) OpenMirrorCurrent() { a.OpenMirror(a.CurrentDoc()) }

// promoteCopy 是关掉正本 d 时把 Copy 最小的那份提成正本（它的 Key 变成正本的，会话里也跟着）；
// d 还在读时那次读作废了（RemoveDoc 里 Gen++），提上来的重读一次。
func (a *App) promoteCopy(d *docs.Doc) {
	if d.Copy != 0 {
		return
	}
	var next *docs.Doc
	for _, o := range a.siblings(d) {
		if o != d && (next == nil || o.Copy < next.Copy) {
			next = o
		}
	}
	if next == nil {
		return
	}
	next.Copy = 0
	if d.Loading || next.Buf == nil {
		a.loadDoc(next)
	}
}

// mirrorOf 是会话恢复时同一个路径第二次出现：在有键盘的组里给 d 再开一份（restoreLayout 再分回各组）；
// d 还在读的读完了跟上（ApplyFile 之后的 relink），记下的跳转那时跳。
func (a *App) mirrorOf(d *docs.Doc) *docs.Doc {
	m := &docs.Doc{Kind: docs.File, Pinned: true}
	m.Share(d)
	for _, o := range a.siblings(d) {
		m.Copy = max(m.Copy, o.Copy+1)
	}
	a.Docs = append(a.Docs, m)
	return m
}

// SetDocEOL 是状态栏换行菜单里选的（"\n" / "\r\n"）：换了算改过，别的几份跟着。
func (a *App) SetDocEOL(d *docs.Doc, eol string) {
	d.SetEOL(eol)
	a.relink(d)
}
