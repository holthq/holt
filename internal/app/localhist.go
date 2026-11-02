package app

import (
	"cmp"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/fuzzy"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/history"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是本地历史：写盘前后各记一份（internal/history 存在状态目录的 history 下），右栏文件历史里
// 和提交按时间混排，比较标签里能恢复；删错的文件从"从本地历史恢复文件…"拿回来。写盘只经 diskWrite 这一个口：
// 保存、全部保存、格式化后保存、自动保存、转成 UTF-8 都走它。应用里的重命名 / 移动（rename）跟着挪。

// PruneDelay 是启动后多久在后台清理一次本地历史（删过期的、超总量的）。
var PruneDelay = 30 * time.Second

var (
	histMu     sync.Mutex
	histStores = map[string]*history.Store{} // 几个窗口同一个状态目录共用一个（锁在 Store 里）
	histPruned = map[string]bool{}           // 这个进程里清理过的（几个窗口只清一次）
)

// LocalHist 是这个窗口的本地历史；没有状态目录（测试里的 New(ws, "")）是 nil。
func (a *App) LocalHist() *history.Store {
	if a.StateDir == "" {
		return nil
	}
	dir := filepath.Join(a.StateDir, "history")
	histMu.Lock()
	defer histMu.Unlock()
	s := histStores[dir]
	if s == nil {
		s = history.New(dir)
		histStores[dir] = s
	}
	return s
}

// keepsHistory 是 abs 记不记本地历史：开着、有状态目录；状态目录里的、.git 下面的、图片、files.exclude 藏掉的不记
// （二进制和太大的由调用方和 Record 挡）。
func (a *App) keepsHistory(abs string) bool {
	if abs == "" || a.St.Settings.NoLocalHistory || a.LocalHist() == nil || docs.IsImage(abs) {
		return false
	}
	sep := string(filepath.Separator)
	if strings.HasPrefix(abs, filepath.Clean(a.StateDir)+sep) || strings.Contains(abs, sep+".git"+sep) {
		return false
	}
	if r, rel := a.repoOfPath(abs); r != nil && r.exclude.MatchRoot(rel) {
		return false
	}
	return true
}

// diskWrite 是文件标签写盘的唯一的口：write 之前磁盘上的那份还没记过就先记一条"保存前磁盘上的"（第一次保存、
// 别的程序改过的那份），write 成功后记写进去的那份（10 s 内连着存的合成一条）。d 是 keybindings.json 时重读快捷键。
func (a *App) diskWrite(d *docs.Doc, write func() error) error {
	keep := d.Kind == docs.File && d.Enc != docs.Binary && a.keepsHistory(d.Abs)
	if keep {
		if _, err := a.LocalHist().BeforeWrite(d.Abs); err != nil {
			log.Printf("本地历史：记 %s 保存前的那份：%v", d.Abs, err)
		}
	}
	if err := write(); err != nil {
		return err
	}
	if keep {
		a.recordLocal(d.Abs, history.WhySave)
	}
	a.keybindingsSaved(d)
	return nil
}

// recordLocal 把磁盘上 abs 现在的内容记一条 why（太大、读不了的不记），右栏开着它的历史就跟着刷新。
func (a *App) recordLocal(abs, why string) {
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > history.MaxSnapshot {
		return
	}
	data, err := os.ReadFile(abs)
	if err == nil {
		_, err = a.LocalHist().Record(abs, data, why)
	}
	if err != nil {
		log.Printf("本地历史：记 %s：%v", abs, err)
	}
	a.localChanged(abs)
}

// localMoved 跟着应用里的重命名 / 移动（rename，含撤销文件操作时改回去）挪本地历史；dir 是挪的目录。
func (a *App) localMoved(oldAbs, newAbs string, dir bool) {
	s := a.LocalHist()
	if s == nil {
		return
	}
	if n, err := s.Move(oldAbs, newAbs, dir); err != nil {
		log.Printf("本地历史：%s 改名成 %s（挪了 %d 个文件的）：%v", oldAbs, newAbs, n, err)
	}
	if a.LocalHistPath == oldAbs {
		a.LocalHistPath = newAbs
	}
	a.localChanged(newAbs)
}

// startLocalHistory 在启动后 PruneDelay 在后台清理一次：删超过保留天数的条目、超总量的从最早的删起。
func (a *App) startLocalHistory() {
	s := a.LocalHist()
	if s == nil {
		return
	}
	histMu.Lock()
	done := histPruned[s.Dir]
	histPruned[s.Dir] = true
	histMu.Unlock()
	if done {
		return
	}
	days := a.St.Settings.LocalHistoryDays()
	time.AfterFunc(PruneDelay, func() {
		if n, err := s.Prune(days); n > 0 || err != nil {
			log.Printf("本地历史：清理了 %d 条（留 %d 天）%v", n, days, cmp.Or[any](err, ""))
		}
	})
}

// LocalStamp 是本地历史条目的时间：今天的 "14:32"，今年的 "11-01 14:32"，更早的带年份。
func LocalStamp(t time.Time) string {
	now := time.Now()
	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("01-02 15:04")
	}
	return t.Format("2006-01-02 15:04")
}

// LocalTitle 是本地历史一行的标题：保存 / 保存前磁盘上的 / 恢复前 / 改名自 old.go。
func LocalTitle(e history.Entry) string {
	switch e.Why {
	case history.WhyDisk:
		return i18n.T("保存前磁盘上的")
	case history.WhyRestore:
		return i18n.T("恢复前")
	case history.WhyRename:
		return i18n.Tf("改名自 %s", filepath.Base(e.From))
	}
	return i18n.T("保存")
}

// LocalMeta 是本地历史一行的第二行："5 分钟前 · 1.2 KB"。
func LocalMeta(e history.Entry) string {
	return git.Ago(e.Time()) + " · " + docs.HumanSize(e.Size)
}

// localChanged 是 abs 的本地历史变了：右栏正看着它的（文件历史、路径模式）重读条目。
func (a *App) localChanged(abs string) {
	for _, r := range a.Repos {
		if h := &r.Hist; h.localFor == abs {
			a.loadLocal(r)
		}
	}
	if a.LocalHistPath == abs {
		a.LocalRows = a.LocalHist().Load(abs).Entries
	}
}

// loadLocal 重读 r 的文件历史对应的本地历史（readLocal），选中的行跟着条目走（行号变了也还选着那条 / 那个提交）。
func (a *App) loadLocal(r *Repo) {
	h := &r.Hist
	ci, file := histKey(r)
	var selT int64
	if ci == LocalRow && file >= 0 && file < len(h.Local) {
		selT = h.Local[file].T
	}
	a.readLocal(r)
	switch {
	case selT != 0:
		setHistSel(r, LocalRow, slices.IndexFunc(h.Local, func(e history.Entry) bool { return e.T == selT }))
	case ci >= 0: // 本地历史只插在提交之间，上面"未提交的改动"、比较段的行号不变
		setHistSel(r, ci, file)
	}
}

// readLocal 读 r 的文件历史（Path 是文件）对应的本地历史；不是文件历史就清掉。
func (a *App) readLocal(r *Repo) {
	h := &r.Hist
	h.Local, h.localFor = nil, ""
	if s := a.LocalHist(); s != nil && h.Path != "" && !h.PathDir {
		h.localFor = filepath.Join(r.Root, filepath.FromSlash(h.Path))
		h.Local = s.Load(h.localFor).Entries
	}
}

// LocalAbs 是右栏现在看的本地历史是哪个文件：路径模式的，或文件历史的；都不是为空。
func (a *App) LocalAbs(r *Repo) string {
	if a.LocalHistPath != "" {
		return a.LocalHistPath
	}
	if r != nil {
		return r.Hist.localFor
	}
	return ""
}

// ShowLocalHistory 是"查看本地历史"（file.localHistory）：Git 仓库里已跟踪的文件就是文件历史（本地的混排在里面）；
// 仓库外、普通文件夹里、未跟踪的进右栏的路径模式，只列本地的。
func (a *App) ShowLocalHistory(abs string) {
	if abs == "" {
		return
	}
	abs = filepath.Clean(abs)
	if r, rel := a.repoOfPath(abs); r != nil && !r.Plain && ChangedIn(r, rel) != '?' {
		a.LocalHistPath = ""
		a.ShowFileHistory(r, rel, false)
		return
	}
	a.LocalHistPath, a.LocalSel = abs, -1
	a.LocalRows = nil
	if s := a.LocalHist(); s != nil {
		a.LocalRows = s.Load(abs).Entries
	}
	a.St.RightBar = true
	a.SaveState()
}

// ClearLocalPath 退出右栏的路径模式（胶囊上的 ×、切仓库）。
func (a *App) ClearLocalPath() { a.LocalHistPath, a.LocalRows, a.LocalSel = "", nil, -1 }

func hasLocalHist(a *App) bool { return a.LocalHist() != nil }

// CanShowLocalHistory 是当前标签是磁盘上的文件（命令 file.localHistory）。
func (a *App) CanShowLocalHistory() bool {
	d := a.CurrentDoc()
	return a.LocalHist() != nil && d != nil && d.Kind == docs.File && d.Abs != "" && d.Snapshot == 0
}

// OpenLocalCompare 打开 abs 的本地历史 e ↔ 工作区里的 abs 的只读比较标签（同一条再点切过去）；pin 为 false 是预览标签。
func (a *App) OpenLocalCompare(abs string, e history.Entry, pin bool) *docs.Doc {
	s := a.LocalHist()
	if s == nil {
		return nil
	}
	p, err := s.ContentPath(abs, e)
	if err != nil {
		a.Fail(i18n.T("读本地历史失败"), err)
		return nil
	}
	d := docs.NewCompare(p, abs, nil)
	d.Snapshot = e.T
	name := filepath.Base(abs)
	d.Title = i18n.Tf("%s · %s 的本地历史 ↔ %s", name, LocalStamp(e.Time()), name)
	return a.openCompareAs(d, pin)
}

// LocalCompareFile 是本地历史比较标签比的那个工作区文件（d.Right）所在的仓库和路径，面包屑用；
// 不是这种标签、或文件在仓库外时 r 为 nil。
func (a *App) LocalCompareFile(d *docs.Doc) (r *Repo, rel string) {
	if d.Kind != docs.Compare || d.Snapshot == 0 {
		return nil, ""
	}
	return a.repoOfPath(d.Right)
}

// RestoreSnapshot 是本地历史比较标签上的"恢复到这一份"：d.Right 恢复到 d.Snapshot 那一条。
func (a *App) RestoreSnapshot(d *docs.Doc) {
	s := a.LocalHist()
	if s == nil || d.Snapshot == 0 {
		return
	}
	for _, e := range s.Load(d.Right).Entries {
		if e.T == d.Snapshot {
			a.RestoreLocal(d.Right, e)
			return
		}
	}
	a.Toast = i18n.T("这一条本地历史已经没有了")
}

// OpenLocalFile 只读地打开 abs 的本地历史 e（右键"在编辑器中打开"）：标题 "a.go · 14:32"，不进会话、最近打开。
func (a *App) OpenLocalFile(abs string, e history.Entry) *docs.Doc {
	s := a.LocalHist()
	if s == nil {
		return nil
	}
	p, err := s.ContentPath(abs, e)
	if err != nil {
		a.Fail(i18n.T("读本地历史失败"), err)
		return nil
	}
	d := docs.NewPath(p)
	if old := a.reuse(d.Key(), true); old != nil {
		return old
	}
	d.Snapshot = e.T
	d.Title = filepath.Base(abs) + " · " + LocalStamp(e.Time())
	if l := editor.LanguageFor(abs); l != nil { // 内容文件没有扩展名，语言按原来的文件认
		d.Lang = l.Name
	}
	a.addDoc(d, true)
	a.loadDoc(d)
	return d
}

// RestoreLocal 把 abs 恢复到本地历史里的 e（比较标签的"恢复到这一份"、右键）：先把磁盘上现在的记一条"恢复前"，
// 再写回那份（权限照旧），开着的标签重读。开着的标签有没保存的改动先问。
func (a *App) RestoreLocal(abs string, e history.Entry) {
	s := a.LocalHist()
	if s == nil {
		return
	}
	run := func() {
		data, err := s.Content(abs, e)
		if err != nil {
			a.Fail(i18n.T("恢复失败"), err)
			return
		}
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(abs); err == nil {
			mode = fi.Mode().Perm()
			a.recordLocal(abs, history.WhyRestore)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			a.Fail(i18n.T("恢复失败"), err)
			return
		}
		if err := os.WriteFile(abs, data, mode); err != nil {
			a.Fail(i18n.T("恢复失败"), err)
			return
		}
		a.afterLocalWrite(abs)
		a.Toast = i18n.Tf("已恢复到 %s 的那一份（恢复前的留在本地历史里）", LocalStamp(e.Time()))
	}
	if slices.ContainsFunc(a.AllDocs(), func(d *docs.Doc) bool { return d.Kind == docs.File && d.Abs == abs && d.Modified() }) {
		a.Ask(i18n.Tf("%s 有没保存的改动，恢复会丢掉它们", filepath.Base(abs)), i18n.T("磁盘上现在的那份会留在本地历史里。"), i18n.T("恢复"), run)
		return
	}
	run()
}

// afterLocalWrite 是从本地历史写回 abs 之后：开着的标签重读，所在仓库重读状态，右栏的条目刷新。
func (a *App) afterLocalWrite(abs string) {
	for _, d := range a.AllDocs() {
		if d.Kind == docs.File && d.Abs == abs || d.Kind == docs.Compare && (d.Abs == abs || d.Right == abs) {
			a.Reload(d)
		}
	}
	if r, rel := a.repoOfPath(abs); r != nil {
		a.RefreshRepo(r, WhySave)
		a.SearchFilesChanged(r, []string{rel}, false)
	}
	a.localChanged(abs)
}

// DeleteLocal 删 abs 的一条本地历史（右键"删除这一条"），开着它的比较标签关掉。
func (a *App) DeleteLocal(abs string, e history.Entry) {
	if s := a.LocalHist(); s != nil {
		if err := s.Delete(abs, e.T); err != nil {
			a.Fail(i18n.T("没能删掉"), err)
		}
		for _, d := range slices.Clone(a.AllDocs()) { // CloseDoc 改 a.Docs
			if d.Snapshot == e.T && (d.Right == abs || d.Kind == docs.File) {
				a.CloseDoc(d)
			}
		}
		a.localChanged(abs)
	}
}

// ClearLocal 问过之后删掉 abs 的全部本地历史（右键"清除这个文件的本地历史…"）。
func (a *App) ClearLocal(abs string) {
	s := a.LocalHist()
	if s == nil {
		return
	}
	n := len(s.Load(abs).Entries)
	a.AskDanger(i18n.Tf("清除 %s 的本地历史？", filepath.Base(abs)), i18n.Tn("%d 条都会删掉，不能撤销。", n), i18n.T("清除"), func() {
		if err := s.Clear(abs); err != nil {
			a.Fail(i18n.T("没能清除"), err)
		}
		a.localChanged(abs)
	})
}

// ClearAllLocalHistory 是"清理本地历史…"（file.clearLocalHistory）：问过之后删掉整个 history 目录。
func (a *App) ClearAllLocalHistory() {
	s := a.LocalHist()
	if s == nil {
		return
	}
	size, n := s.Usage()
	if n == 0 {
		a.Toast = i18n.T("还没有本地历史")
		return
	}
	a.AskDanger(i18n.Tn("删掉全部 %d 个文件的本地历史（%s）？", n, docs.HumanSize(size)), i18n.T("比较、恢复都用不了了，不能撤销。"), i18n.T("全部删掉"), func() {
		if err := s.ClearAll(); err != nil {
			a.Fail(i18n.T("没能清除"), err)
		}
		a.localUsage = localUsage{}
		for _, r := range a.Repos {
			if r.Hist.localFor != "" {
				a.loadLocal(r)
			}
		}
		if a.LocalHistPath != "" {
			a.LocalRows = nil
		}
		a.Toast = i18n.T("已清除本地历史")
	})
}

// deletedItems 是 ">restore 名字"（"从本地历史恢复文件…"）：本地历史里记过、磁盘上已经没有的文件，最近的在前，
// 路径模糊匹配；右边"3 条 · 昨天"。
func (a *App) deletedItems(q string) ([]PaletteItem, string) {
	s := a.LocalHist()
	if s == nil {
		return nil, i18n.T("没有状态目录，不记本地历史")
	}
	pat := fuzzy.New(q)
	var items []PaletteItem
	for _, x := range s.Deleted() {
		text := tildePath(x.Path)
		name := strings.LastIndexByte(text, filepath.Separator) + 1
		if _, hits, ok := pat.Match(text, name); ok {
			last, _ := x.Latest()
			items = append(items, PaletteItem{Kind: ItemDeleted, Text: text, Name: name, Hits: hits, path: x.Path,
				Detail: i18n.Tn("%d 条 · %s", len(x.Entries), git.Ago(last.Time()))})
		}
	}
	if len(items) == 0 && q == "" {
		return nil, i18n.T("本地历史里没有删掉的文件")
	}
	return items, i18n.T("把删掉的文件写回原来的位置（用本地历史里最新的一份）")
}

// RestoreDeleted 把本地历史里 abs 最新的一份写回 abs（目录不在就建；路径又有了先问要不要覆盖），再打开它。
func (a *App) RestoreDeleted(abs string) {
	s := a.LocalHist()
	if s == nil {
		return
	}
	e, ok := s.Load(abs).Latest()
	if !ok {
		a.Toast = i18n.Tf("本地历史里没有 %s", filepath.Base(abs))
		return
	}
	run := func() {
		data, err := s.Content(abs, e)
		if err == nil {
			err = os.MkdirAll(filepath.Dir(abs), 0o755)
		}
		if err == nil {
			if _, serr := os.Stat(abs); serr == nil {
				a.recordLocal(abs, history.WhyRestore)
			}
			err = os.WriteFile(abs, data, 0o644)
		}
		if err != nil {
			a.Fail(i18n.T("恢复失败"), err)
			return
		}
		a.afterLocalWrite(abs)
		a.OpenPath(abs)
		a.Toast = i18n.Tf("已恢复 %s（%s 的那一份）", filepath.Base(abs), LocalStamp(e.Time()))
	}
	if _, err := os.Lstat(abs); err == nil {
		a.AskDanger(i18n.Tf("%s 又有了，覆盖它？", filepath.Base(abs)), i18n.T("磁盘上现在的那份会留在本地历史里。"), i18n.T("覆盖"), run)
		return
	}
	run()
}

// localUsage 是设置页那行"现在 12.3 MB · 87 个文件"：后台走一遍目录算的，10 s 内不再算。
type localUsage struct {
	bytes   int64
	files   int
	at      time.Time
	running bool
}

// LocalUsage 是本地历史占了多少、记了几个文件（设置页那行）；ok 为假是还没算出来（在后台算着）。
func (a *App) LocalUsage() (bytes int64, files int, ok bool) {
	u := &a.localUsage
	if s := a.LocalHist(); s != nil && !u.running && time.Since(u.at) > 10*time.Second {
		u.running = true
		up := a.Update
		go func() {
			b, n := s.Usage()
			up(func() { a.localUsage = localUsage{bytes: b, files: n, at: time.Now()} })
		}()
	}
	return u.bytes, u.files, !u.at.IsZero()
}

// LocalHistDir 是本地历史的目录（设置页的"在访达中显示"）；没有时建出来，访达才有东西可指。
func (a *App) LocalHistDir() string {
	s := a.LocalHist()
	if s == nil {
		return ""
	}
	os.MkdirAll(s.Dir, 0o700)
	return s.Dir
}
