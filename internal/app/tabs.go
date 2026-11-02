package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是主区的标签：文件、只读的历史版本、Diff。读文件、版本和 diff 都在后台；
// 写磁盘只在用户按 ⌘S、点"保存"或在关标签 / 退出的确认框里选了保存之后。
//
// 预览语义：单击打开的是预览标签（斜体），下一个预览会顶掉它；双击、回车、编辑后固定。

// closedDoc 记着关掉的标签，⇧⌘T 重新打开。
type closedDoc struct {
	kind docs.Kind
	root string
	rel  string
	abs  string
	ch   git.Change
	enc  docs.Encoding // 手动选的编码（Doc.ForcedEnc）
	lang string        // 手动选的语言和缩进（Doc.Lang / Doc.Indent）
	ind  string
}

// maxNav 是 ‹ › 记的最多步数。
const maxNav = 100

// CurrentDoc 是当前标签；没有为 nil。
func (a *App) CurrentDoc() *docs.Doc {
	if a.DocSel < 0 || a.DocSel >= len(a.Docs) {
		return nil
	}
	return a.Docs[a.DocSel]
}

// RepoOf 是标签所属的仓库；仓库外的文件、仓库已移出列表为 nil。
func (a *App) RepoOf(d *docs.Doc) *Repo { return a.RepoFor(d.Root) }

// SelectDoc 切到第 i 个标签。
func (a *App) SelectDoc(i int) {
	if i < 0 || i >= len(a.Docs) {
		return
	}
	a.markNav()
	a.DocSel = i
	a.visit(a.Docs[i].Key())
	a.noteRecent(a.Docs[i])
	a.syncChangeSel()
}

// navPoint 是 ‹ › 历史里的一步：哪个标签，at 时还有光标位置（离开时记下的，或跳过去的地方）。
type navPoint struct {
	key docs.Key
	pos editor.Pos
	at  bool
}

// markNav 在离开当前这一步之前记下光标在哪，回来时放回去。
func (a *App) markNav() {
	d := a.CurrentDoc()
	if d == nil || a.navPos >= len(a.nav) || a.nav[a.navPos].key != d.Key() {
		return
	}
	if p, ok := d.Cursor(); ok {
		a.nav[a.navPos].pos, a.nav[a.navPos].at = p, true
	}
}

// visit 把切到的标签记进 ‹ › 的历史；在历史中间切到新标签就丢掉"前进"的那些。
func (a *App) visit(k docs.Key) {
	if a.navPos < len(a.nav) && a.nav[a.navPos].key == k {
		return
	}
	a.push(navPoint{key: k})
}

func (a *App) push(p navPoint) {
	if len(a.nav) > 0 {
		a.nav = a.nav[:a.navPos+1]
	}
	a.nav = append(a.nav, p)
	if len(a.nav) > maxNav {
		a.nav = slices.Delete(a.nav, 0, len(a.nav)-maxNav)
	}
	a.navPos = len(a.nav) - 1
}

// CanBack / CanForward 是 ‹ › 能不能走（关掉的标签跳过）。
func (a *App) CanBack() bool    { return a.navTarget(-1) >= 0 }
func (a *App) CanForward() bool { return a.navTarget(1) >= 0 }

func (a *App) navTarget(step int) int {
	for p := a.navPos + step; p >= 0 && p < len(a.nav); p += step {
		if g, _ := a.locate(a.nav[p].key); g != nil {
			return p
		}
	}
	return -1
}

// Back 回到上一个看过的地方（⌃-）：别的标签，或同一个文件里跳之前的那一行。
func (a *App) Back() { a.navGo(-1) }

// Forward 走到下一个看过的地方（⌃⇧-）。
func (a *App) Forward() { a.navGo(1) }

func (a *App) navGo(step int) {
	p := a.navTarget(step)
	if p < 0 {
		return
	}
	a.markNav()
	a.navPos = p
	g, i := a.locate(a.nav[p].key)
	a.toGroup(g)
	a.DocSel = i
	if np := a.nav[p]; np.at {
		a.Docs[a.DocSel].JumpTo(docs.Jump{Line: np.pos.Line, Col: np.pos.Col})
	}
	a.syncChangeSel()
}

// OpenAt 打开仓库里的文件并跳到 j（搜索结果、快速打开的 名字:行号、终端里 ⌘点的路径），
// 跳之前的位置留在 ‹ › 里：同一个文件里跳到别的行也算一步。
func (a *App) OpenAt(r *Repo, rel string, j docs.Jump, pin bool) *docs.Doc {
	prev := a.CurrentDoc()
	same := prev != nil && prev.Kind == docs.File && prev.Root == r.Root && prev.Rel == rel
	if same {
		a.markNav()
	}
	d := a.OpenFile(r, rel, pin)
	a.jumpIn(d, j, same)
	return d
}

// JumpHere 在当前标签里跳到 j（快速打开里的 :行号、跳到行）。
func (a *App) JumpHere(j docs.Jump) {
	d := a.CurrentDoc()
	if d == nil || d.IsDiffView() {
		return
	}
	a.markNav()
	a.jumpIn(d, j, true)
}

// jumpIn 让 d 跳到 j 并记进 ‹ ›：d 本来就是当前这一步（same）且已经记了别的行，就为 j 再加一步；
// 否则就把当前这一步的位置定在 j。
func (a *App) jumpIn(d *docs.Doc, j docs.Jump, same bool) {
	d.JumpTo(j)
	to := navPoint{key: d.Key(), pos: editor.Pos{Line: j.Line, Col: j.Col}, at: true}
	if a.navPos >= len(a.nav) || a.nav[a.navPos].key != to.key {
		a.push(to)
		return
	}
	if cur := a.nav[a.navPos]; same && cur.at && cur.pos.Line != j.Line {
		a.push(to)
		return
	}
	a.nav[a.navPos] = to
}

// addDoc 把标签加进去并切过去：不固定时顶掉一个没固定、没改过的预览标签。
func (a *App) addDoc(d *docs.Doc, pin bool) {
	d.Pinned = pin
	if !pin {
		for i, old := range a.Docs {
			if !old.Pinned && !old.Modified() {
				old.Gen++ // 还在读的话作废
				a.Docs[i] = d
				a.SelectDoc(i)
				return
			}
		}
	}
	a.Docs = append(a.Docs, d)
	a.SelectDoc(len(a.Docs) - 1)
}

// reuse 是已经开着的同一个标签：切过去（在别的组就连键盘一起换过去），pin 时固定它。
func (a *App) reuse(k docs.Key, pin bool) *docs.Doc {
	g, i := a.locate(k)
	if g == nil {
		return nil
	}
	a.markNav()
	a.toGroup(g)
	d := a.Docs[i]
	d.Pinned = d.Pinned || pin
	a.SelectDoc(i)
	return d
}

// OpenFile 在主区打开仓库里的一个文件；pin 为 false 时是预览标签。
func (a *App) OpenFile(r *Repo, rel string, pin bool) *docs.Doc {
	d := docs.NewFile(r.Root, rel)
	if old := a.reuse(d.Key(), pin); old != nil {
		return old
	}
	a.addDoc(d, pin)
	a.loadDoc(d)
	return d
}

// OpenPath 打开一个磁盘路径（"打开文件…"对话框）：在列表里某个仓库下的算那个仓库的文件。
func (a *App) OpenPath(abs string) *docs.Doc {
	abs = filepath.Clean(abs)
	for _, r := range a.Repos {
		if rel, err := filepath.Rel(r.Root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return a.OpenFile(r, filepath.ToSlash(rel), true)
		}
	}
	d := docs.NewPath(abs)
	if old := a.reuse(d.Key(), true); old != nil {
		return old
	}
	a.addDoc(d, true)
	a.loadDoc(d)
	return d
}

// OpenVersion 只读地打开历史里某次提交的一个文件；槽里标出它相对父提交的改动。
func (a *App) OpenVersion(r *Repo, ch git.Change) *docs.Doc {
	d := docs.NewVersion(r.Root, ch)
	if old := a.reuse(d.Key(), true); old != nil {
		return old
	}
	a.addDoc(d, true)
	a.loadDoc(d)
	return d
}

// OpenDiff 在 Diff 标签里显示一项变更（Git 栏、历史里点的文件）；pin 为 false 时是预览标签。
func (a *App) OpenDiff(r *Repo, ch git.Change, pin bool) *docs.Doc {
	d := docs.NewDiff(r.Root, ch)
	if old := a.reuse(d.Key(), pin); old != nil {
		return old
	}
	a.addDoc(d, pin)
	a.loadDoc(d)
	return d
}

// ShowDiffFor 打开文件的未提交改动的 Diff（未暂存的优先，其次已暂存、未跟踪）。
func (a *App) ShowDiffFor(r *Repo, rel string) {
	ch, ok := r.ChangeOf(rel)
	if !ok {
		a.Toast = i18n.Tf("%s 没有未提交的改动", rel)
		return
	}
	a.OpenDiff(r, ch, true)
}

// loadDoc 在后台（重）读标签的内容：解码、建缓冲区（新标签、大文件）、读 HEAD 版本都在后台。
func (a *App) loadDoc(d *docs.Doc) {
	a.readDoc(d, d.Buf == nil || d.Big) // 大文件重读也在后台建一个新的，不在主线程上 Reset
}

// readDoc 是 loadDoc；fresh 时在后台建一个新的缓冲区换上（光标、滚动留着），不在主线程上 Reset（Reset 的旧文字进撤销）。
func (a *App) readDoc(d *docs.Doc, fresh bool) {
	gen := d.Start()
	abs, root, rel, ch, enc, hex := d.Abs, d.Root, d.Rel, d.Change, d.ForcedEnc, d.Hex // 后台只读拷贝：标签可能同时在主线程上改名（Moved）
	d.ReloadAt = time.Now()
	up := a.Update
	switch d.Kind {
	case docs.File:
		go func() {
			var l docs.Loaded
			if hex { // 十六进制视图：只读前 HexMax 字节
				l = docs.ReadHexFile(abs)
			} else {
				l = docs.ReadFileAs(abs, root, rel, fresh, enc)
			}
			up(func() {
				if d.ApplyFile(gen, l) {
					a.relink(d) // 镜像：重读换了缓冲区，别的几份跟上
				}
			})
		}()
	case docs.Version:
		go func() {
			var l docs.Loaded
			if hex {
				l = docs.ReadHexVersion(root, ch)
			} else {
				l = docs.ReadVersionAs(root, ch, fresh, enc)
			}
			up(func() { d.ApplyVersion(gen, l) })
		}()
	case docs.Compare:
		right, clip, snap := d.Right, d.RightText, d.Snapshot != 0
		go func() {
			dc, err := docs.ReadCompare(abs, right, clip)
			if snap {
				dc.OldName = right // 本地历史的内容文件没有扩展名：语言按工作区的文件认
			}
			up(func() { d.ApplyDiff(gen, dc, err, a.St.FullDiff) })
		}()
	case docs.Diff:
		ctx := context.Background()
		if r := a.RepoFor(root); r != nil {
			ctx = r.ctx // 切工作区、移除仓库时没读完的 diff 不读了
		}
		limit := git.MaxDiffBytes
		if docs.IsImage(ch.Path) {
			limit = docs.MaxEditBytes // 图片并排看两边，每边到 8 MB
		}
		go func() {
			dc, err := git.LoadDiffContentMax(ctx, root, ch, limit)
			var gone string
			if err != nil && ch.Kind == git.InCommit {
				if gone = docs.GoneCommit(root, ch.Commit, ch.Parent); gone != "" {
					dc, err = git.DiffContent{Note: docs.CommitGoneNote(gone)}, nil
				}
			}
			up(func() {
				if d.ApplyDiff(gen, dc, err, a.St.FullDiff) {
					d.Gone = gone != ""
				}
			})
		}()
	}
}

// Reload 丢掉这里的改动重读磁盘上的（冲突条的"重新读取"）。
func (a *App) Reload(d *docs.Doc) { a.loadDoc(d) }

// KeepMine 是冲突条的"保留我的"：下次 ⌘S 直接覆盖，不再问。
func (a *App) KeepMine(d *docs.Doc) {
	d.KeepMine()
	a.relink(d)
}

// Save 把标签写回磁盘（⌘S）；磁盘上的文件被别处改过就先问一声。开着保存时格式化的先在后台格式化（saveFormatted），
// 格式化着的时候再按不另起。设置文档（settings.json）不写盘、不格式化：⌘S 是套用设置（applySettingsDoc）。
func (a *App) Save(d *docs.Doc) {
	if d == nil || !d.Editable() || !d.Modified() || d.Formatting {
		return
	}
	if d.Kind == docs.SettingsDoc {
		a.applySettingsDoc(d)
		return
	}
	if d.NeedsOverwrite() {
		a.AskDanger(i18n.Tf("覆盖 %s？", d.Title), i18n.T("这个文件在磁盘上被别的程序改过了，保存会覆盖那些改动。"), i18n.T("覆盖"), func() { a.saveFormatted(d, nil) })
		return
	}
	a.saveFormatted(d, nil)
}

// WriteDoc 真的写磁盘；tidy 时先按设置整理（去尾空白、补末尾换行，见 TidyDoc）。成功后缓冲区算已保存，
// 并重读所属仓库的 git 状态。设置文档（全部保存、关标签时的保存走到这里）是套用设置，不写盘。
func (a *App) WriteDoc(d *docs.Doc, tidy bool) error {
	if d.Kind == docs.SettingsDoc {
		return a.applySettingsDoc(d)
	}
	n := 0
	if tidy {
		n = a.TidyDoc(d)
	}
	if err := a.diskWrite(d, d.Write); err != nil {
		if ue := (*docs.UnencodableError)(nil); errors.As(err, &ue) {
			a.askUTF8(d, ue)
			return err
		}
		a.Fail(i18n.T("保存失败"), err)
		return err
	}
	a.Toast = i18n.Tf("已保存 %s", d.Title)
	if n > 0 {
		a.Toast = i18n.Tn("已保存 %s · 整理了 %d 行", d.Title, n)
	}
	a.refreshSaved(d)
	return nil
}

// askUTF8 是保存时有字按文件的编码写不进去（老编码里的 emoji 之类）：问要不要转成 UTF-8 保存，取消就不保存。
// 这次没存上（WriteDoc 照样返回错误，关标签、退出那些就停下），点了"转成 UTF-8"才按 UTF-8 写。
func (a *App) askUTF8(d *docs.Doc, ue *docs.UnencodableError) {
	a.Ask(i18n.Tn("有 %d 个字 %s 写不了：转成 UTF-8 保存？", ue.N, ue.Enc.Label()),
		i18n.T("文件会按 UTF-8 重写，git 里会显示整个文件改了；不能撤销。"), i18n.T("转成 UTF-8"), func() {
			if err := a.diskWrite(d, d.ConvertUTF8); err != nil {
				a.Fail(i18n.T("保存失败"), err)
				return
			}
			a.Toast = i18n.Tf("已按 UTF-8 保存 %s", d.Title)
			a.refreshSaved(d)
		})
}

// ConvertUTF8 是只读条和编码菜单里的"转成 UTF-8…"：问过之后把不是 UTF-8 的文件按 UTF-8 重写，之后可编辑。
func (a *App) ConvertUTF8(d *docs.Doc) {
	if !d.Convertible() {
		return
	}
	msg := i18n.T("文件会按 UTF-8 重写，git 里会显示整个文件改了；不能撤销。")
	if d.Lossy {
		msg += i18n.T("文件里有编回去和原来不一样的字节（比如单独的 0x80），这些会按读出来的字写。")
	}
	a.Ask(i18n.Tf("把 %s 转成 UTF-8 保存？", d.Title), msg, i18n.T("转换"), func() {
		if err := a.diskWrite(d, d.ConvertUTF8); err != nil {
			a.Fail(i18n.T("没能转成 UTF-8"), err)
			return
		}
		a.Toast = i18n.Tf("%s 已转成 UTF-8", d.Title)
		a.refreshSaved(d)
	})
}

// ReopenWithEncoding 是编码菜单和只读条"换编码…"里选的：按 enc（"" 为自动认）重读文件或历史版本，会话里记着；
// 改了没存的先问。后台建新的缓冲区换上，光标、滚动留着。
func (a *App) ReopenWithEncoding(d *docs.Doc, enc docs.Encoding) {
	if d == nil || d.Kind != docs.File && d.Kind != docs.Version {
		return
	}
	reopen := func() {
		d.ForcedEnc = enc
		a.readDoc(d, true)
		a.SaveState()
	}
	if !d.Modified() {
		reopen()
		return
	}
	name := enc.Label()
	if enc == "" {
		name = i18n.T("自动识别的编码")
	}
	a.AskDanger(i18n.Tf("按 %s 重新打开 %s？", name, d.Title), i18n.T("没保存的改动会丢掉。"), i18n.T("重新打开"), reopen)
}

// reopenAs 让刚重开的标签（会话、⇧⌘T）按上次手动选的编码读：""、不认识的、已经是它的不管。
func (a *App) reopenAs(d *docs.Doc, enc docs.Encoding) {
	if d == nil || enc == "" || enc.Family() != enc || d.ForcedEnc == enc || d.Modified() || d.Kind != docs.File && d.Kind != docs.Version {
		return
	}
	d.ForcedEnc = enc
	a.readDoc(d, true)
}

// TidyDoc 按设置（保存时去掉行尾空白 / 补上末尾换行）整理要保存的 d，光标所在的行不动；返回动了几行。
func (a *App) TidyDoc(d *docs.Doc) int {
	s := &a.St.Settings
	if !s.TrimTrailing && !s.FinalNewline {
		return 0
	}
	return d.Tidy(docs.Tidy{Trim: s.TrimTrailing, Newline: s.FinalNewline, Keep: d.CursorLines()})
}

// SaveAll 马上保存所有组里改过的标签（退出确认里的"全部保存"），不等格式化；⌥⌘S 是 SaveEach。
func (a *App) SaveAll() {
	for _, d := range slices.Clone(a.AllDocs()) {
		if d.Modified() && d.Editable() {
			if a.WriteDoc(d, true) != nil {
				return
			}
		}
	}
}

// DirtyDocs 是所有组里没保存的标签数（同一个文件的几份算一个）。
func (a *App) DirtyDocs() int {
	n := 0
	for _, d := range a.AllDocs() {
		if d.Modified() && d.Copy == 0 {
			n++
		}
	}
	return n
}

// CloseDoc 关一个标签；有没保存的改动就先问（取消 / 不保存 / 保存），编辑器区的终端前台有命令在跑先问。
// 同一个文件还有别的份开着（镜像）不问：改动还在那几份里。
func (a *App) CloseDoc(d *docs.Doc) {
	if r, tab := a.TermTabOf(d); tab != nil {
		a.CloseTermTab(r, tab)
		return
	}
	if d.Modified() && !a.hasCopy(d) {
		a.CloseReq, a.CloseOpen = d, true
		return
	}
	a.RemoveDoc(d)
}

// RemoveDoc 直接关掉标签（在哪一组都行），记下它好让 ⇧⌘T 重新打开（终端输出的快照、变基向导、替换预览、比较、终端、设置文档不记）。组里最后一个关了组就收起。
// 编辑器区的终端标签连 shell 一起关（不问）。同一个文件还有别的份（镜像）的不记，关的是正本就把下一份提成正本。
func (a *App) RemoveDoc(d *docs.Doc) {
	g := a.GroupOf(d)
	if g == nil {
		return
	}
	copies := a.hasCopy(d)
	d.Gen++
	if copies {
		a.promoteCopy(d)
	}
	if a.Peek.Doc == d {
		a.ClosePeek()
	}
	if p := d.Rebase; p != nil && !p.Running && (!p.Stopped || p.Ended) { // 变基还停着的，临时文件留到结束（后面的 exec 还要读）
		a.dropRebaseFiles(p)
	}
	if src := searchSrcOf(d); src != nil {
		src.stop()
	}
	if r, tab := a.TermTabOf(d); tab != nil {
		a.killEdTab(r, tab)
	}
	if !copies && d.Snapshot == 0 && d.Kind != docs.TermText && d.Kind != docs.Rebase && d.Kind != docs.ReplacePreview && d.Kind != docs.SearchDoc && d.Kind != docs.Compare && d.Kind != docs.Term && d.Kind != docs.SettingsDoc { // 终端输出的快照、变基向导、替换预览、搜索编辑器、比较、终端、设置文档关了就没了
		a.closed = append(a.closed, closedDoc{kind: d.Kind, root: d.Root, rel: d.Rel, abs: d.Abs, ch: d.Change, enc: d.ForcedEnc,
			lang: d.Lang, ind: d.Indent})
		if len(a.closed) > 20 {
			a.closed = a.closed[1:]
		}
	}
	a.takeOut(g, d)
	if ds, _ := a.GroupDocs(g); len(ds) == 0 {
		a.collapse(g)
	}
	if d := a.CurrentDoc(); d != nil {
		a.visit(d.Key())
	}
	a.syncChangeSel()
}

// CloseCurrent 关当前标签（⌘W）；没有标签时关窗口。
func (a *App) CloseCurrent() {
	if d := a.CurrentDoc(); d != nil {
		a.CloseDoc(d)
	} else if a.Win != nil {
		a.Win.Close()
	}
}

// CloseOthers 关掉有键盘的这组里除 keep 之外没改过的标签；改过的、有命令在跑的终端留着并提示。keep 为 nil 就是关全部（⇧⌘W）。
func (a *App) CloseOthers(keep *docs.Doc) {
	left, busy := 0, 0
	for _, d := range slices.Clone(a.Docs) {
		switch {
		case d == keep:
		case d.Modified():
			left++
		case a.termBusy(d):
			busy++
		default:
			a.RemoveDoc(d)
		}
	}
	msg := ""
	if left > 0 {
		msg = i18n.Tn("%d 个没保存的标签留着了", left)
	}
	if msg = busyToast(msg, busy); msg != "" {
		a.Toast = msg
	}
}

// CloseSaved 关掉有键盘的这组里没改过的标签（包括预览标签），改过的留着（"关闭已保存的标签"，⌥⌘W）。
func (a *App) CloseSaved() {
	n, left, busy := 0, 0, 0
	for _, d := range slices.Clone(a.Docs) { // 关完这组可能收起、a.Docs 换成兄弟的：按拷贝数
		switch {
		case d.Modified():
			left++
			continue
		case a.termBusy(d):
			busy++
			continue
		}
		a.RemoveDoc(d)
		n++
	}
	msg := ""
	if left > 0 {
		msg = i18n.Tn("关了 %d 个已保存的标签，%d 个没保存的留着", n, left)
	}
	if msg = busyToast(msg, busy); msg != "" {
		a.Toast = msg
	}
}

// MoveDoc 把标签从 from 挪到 to（拖标签）；当前标签不变。顺序按工作区记进会话。
func (a *App) MoveDoc(from, to int) {
	if from == to || from < 0 || to < 0 || from >= len(a.Docs) || to >= len(a.Docs) {
		return
	}
	cur := a.CurrentDoc()
	d := a.Docs[from]
	a.Docs = slices.Insert(slices.Delete(a.Docs, from, from+1), to, d)
	a.DocSel = slices.Index(a.Docs, cur)
	a.SaveState()
}

// CanReopen 是有没有关掉的标签可以重新打开。
func (a *App) CanReopen() bool { return len(a.closed) > 0 }

// ReopenClosed 重新打开最近关掉的标签（⇧⌘T）；所属仓库不在列表里了就跳过它。
func (a *App) ReopenClosed() {
	for len(a.closed) > 0 {
		c := a.closed[len(a.closed)-1]
		a.closed = a.closed[:len(a.closed)-1]
		r := a.RepoFor(c.root)
		var d *docs.Doc
		switch {
		case c.kind == docs.File && c.root == "":
			d = a.OpenPath(c.abs)
		case r == nil:
			continue
		case c.kind == docs.File:
			d = a.OpenFile(r, c.rel, true)
		case c.kind == docs.Version:
			d = a.OpenVersion(r, c.ch)
		case c.kind == docs.Diff:
			a.OpenDiff(r, c.ch, true)
			return
		}
		a.reopenAs(d, c.enc)
		restoreLook(d, c.lang, c.ind)
		return
	}
}

// ChooseFiles 用系统对话框选文件打开。
func (a *App) ChooseFiles() {
	var dir string
	if r := a.Current(); r != nil {
		dir = r.Root
	}
	up, win, open := a.Update, a.Win, a.OpenDialog
	go func() {
		paths, err := open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       i18n.T("打开文件"),
			ButtonLabel: i18n.T("打开"),
			DefaultPath: dir,
			Multiple:    true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		up(func() {
			for _, p := range paths {
				a.OpenPath(p)
			}
		})
	}()
}

// ToggleLayout 在并排和统一 diff 之间切换（⌥⌘L），所有 Diff 标签都跟着。
func (a *App) ToggleLayout() {
	a.St.Unified = !a.St.Unified
	a.SaveState()
}

// ToggleScope 在"只看改动附近"和"全文"之间切换（⌥⌘F），所有 Diff 标签都跟着，光标所在那块改动留在视图里。
func (a *App) ToggleScope() {
	a.St.FullDiff = !a.St.FullDiff
	for _, d := range a.AllDocs() {
		if d.DiffSt == nil {
			continue
		}
		if a.St.FullDiff {
			d.DiffSt.ExpandAll()
		} else {
			d.DiffSt.CollapseAll()
		}
		d.ToHunk = true // 展开 / 收起后滚动位置不跟着改动走：让界面把光标所在那块滚回来
	}
	a.SaveState()
}

// GoneNote 是 Diff 标签那项改动已经不在了（暂存、提交或丢弃之后）时的说明。
const GoneNote = "这项改动已经不在了（可能已暂存、提交或丢弃）"

// syncDocs 在仓库状态重读后让它的标签跟上：没改过的文件在别处变了就重读，改过的标出冲突；
// HEAD 里的版本变了（提交、切分支之后）就换掉槽里的对比基准；未提交改动的 Diff 重读，
// 改动没了就显示一句说明、标签留着。
func (a *App) syncDocs(r *Repo) {
	for _, d := range a.AllDocs() {
		if d.Kind == docs.Compare && !d.Loading && compareTouches(d, r) {
			a.loadDoc(d) // 两边可能在不同的仓库里；没变的 ApplyDiff 留着滚动位置
			continue
		}
		if d.Root != r.Root || d.Loading {
			continue
		}
		switch {
		case d.Kind == docs.Diff && d.Change.Kind != git.InCommit:
			if !r.HasChange(d.Change.Kind, d.Change.Path) {
				d.Gen++
				d.Info, d.DiffSt = GoneNote, nil
				continue
			}
			a.loadDoc(d)
		case d.Kind == docs.File && d.Info == "" && d.Copy == 0: // 镜像跟着正本（relink）
			a.syncFile(r, d)
		}
	}
}

// syncFile 在后台看文件在磁盘上变没变、HEAD 里的版本变没变（和标签记的摘要比），都没变就不回主线程；
// HEAD 版本变了的在后台建好新的基准再换上。大文件不读 HEAD 版本，重读最多每 bigReloadGap 一次。
func (a *App) syncFile(r *Repo, d *docs.Doc) {
	gen, abs, rel, root := d.Gen, d.Abs, d.Rel, r.Root // 后台只读这几个拷贝：标签可能同时在主线程上改名（Moved）
	mtime, size0, sum, big, enc := d.Mtime, d.Size, d.BaseSum, d.Big, d.Enc
	noBase := big || d.Hex // 十六进制的不和 HEAD 对比
	up := a.Update
	go func() {
		mt, size, err := docs.Stat(abs)
		if err != nil {
			return
		}
		changed := !mt.Equal(mtime) || size != size0
		var base *editor.Buffer
		var nsum docs.Sum
		if !noBase {
			blob := git.Blob(root, "HEAD", rel)
			if nsum = docs.SumOf(blob); nsum != sum && docs.DiffBaseFits(size, len(blob)) {
				base = docs.BaseBufferAs(blob, enc)
			}
		}
		if !changed && nsum == sum {
			return
		}
		up(func() {
			if d.Gen != gen || d.Rel != rel {
				return
			}
			changed := d.ChangedOnDisk(mt, size)
			switch {
			case changed && d.Modified():
				d.Conflict = true
			case changed && d.Big:
				a.reloadBig(d)
				return
			case changed:
				a.loadDoc(d)
				return
			}
			if d.Buf != nil && !d.Big && !d.Hex && d.BaseSum != nsum {
				d.SetBase(base, nsum)
			}
			a.relink(d)
		})
	}()
}

// bigReloadGap 是一直在变的大文件（日志）两次重读之间至少隔多久：期间的变化合并，到点读最新的。
var bigReloadGap = 2 * time.Second

// reloadBig 重读磁盘上变了的大文件：离上次开始重读不到 bigReloadGap 就排一次到点的（已经排了就不再排）。
func (a *App) reloadBig(d *docs.Doc) {
	wait := bigReloadGap - time.Since(d.ReloadAt)
	if wait <= 0 {
		a.loadDoc(d)
		return
	}
	if d.ReloadQueued {
		return
	}
	d.ReloadQueued = true
	up := a.Update
	time.AfterFunc(wait, func() {
		up(func() {
			d.ReloadQueued = false
			if slices.Contains(a.AllDocs(), d) && d.Big && !d.Loading {
				a.loadDoc(d)
			}
		})
	})
}

// ShowSidebar 打开侧栏并切到 mode（文件 / 搜索 / Git / 大纲）。
func (a *App) ShowSidebar(mode string) {
	if a.St.Sidebar && a.St.SidebarMode == mode {
		return
	}
	a.St.Sidebar, a.St.SidebarMode = true, mode
	a.SaveState()
}

// ToggleSidebarMode 是 ⇧⌘E / ⇧⌘G：侧栏已经开在这个模式就关上，否则打开并切过去（⇧⌘F 总是打开搜索页、焦点给搜索框）。
func (a *App) ToggleSidebarMode(mode string) {
	if a.St.Sidebar && a.St.SidebarMode == mode {
		a.St.Sidebar = false
		a.SaveState()
		return
	}
	a.ShowSidebar(mode)
}

// ToggleArea 开关侧栏 / 右栏 / 底栏（⌘B / ⌥⌘B / ⌘J、标题栏按钮）。
func (a *App) ToggleArea(area *bool) {
	*area = !*area
	if area == &a.St.RightBar && *area {
		if r := a.Current(); r != nil {
			a.EnsureHistory(r)
		}
	}
	a.SaveState()
}
