package app

import (
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/fuzzy"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/outline"
)

// 这个文件是工作区符号（⌘T，面板的 "#"）：所有仓库里大纲认的文件的符号放进一个索引，按符号名模糊匹配。
// 第一次用时建：主线程从 Files.Paths 里挑出语言认的文件，后台 symWorkers 个 worker 读盘、解析，
// 每 symFlush 交回主线程并进来一次（面板跟着重算，提示里报进度）；之后 watch 报的目录标脏、文件列表整个换了的，
// 下次用时只重新 stat 脏的和新加的、版本（olKey）变了才重新解析。开着的文件用标签的大纲（改了没存的也算）。
// 不常驻监听、不存盘；每个窗口一份。

// maxIndexSymbols 是索引里最多放多少个符号：到了就不再加文件（提示"只建了前 N 个文件"）。测试里改小。
var maxIndexSymbols = 200_000

const (
	maxSymbolItems = 200 // 列表最多列几条
	maxRecentSyms  = 10  // 记住最近跳过的几个符号（空查询时列出来）
	maxSymDetail   = 160 // 符号的小字（签名）最多记多少字节
	maxSymDirty    = 256 // 一个仓库标脏的目录多过这么多就当整个仓库都脏了
	symFlush       = 100 * time.Millisecond
)

// symEntry 是索引里的一个符号：名字和小字是从文件内容里拷出来的（不拷的话整个文件的文字都留在内存里）。
type symEntry struct {
	Name, Detail string
	Kind         outline.Kind
	Line         int32
}

// symFile 是索引里的一个文件：读的那一版和它的符号。
type symFile struct {
	r    *Repo
	rel  string
	key  olKey
	syms []symEntry
}

// symDirty 是一个仓库里 watch 报过变化、下次用时要重新 stat 的地方。
type symDirty struct {
	full bool
	dirs []string
}

func (d *symDirty) covers(rel string) bool {
	return d != nil && (d.full || slices.ContainsFunc(d.dirs, func(dir string) bool { return files.Under(rel, dir) }))
}

// symIndex 是工作区符号的索引（App.syms）。
type symIndex struct {
	files  map[string]*symFile // 磁盘路径 → 文件
	paths  map[*Repo][]string  // 每个仓库上次对照过的 Files.Paths（整个换掉了才要再对照）
	dirty  map[*Repo]*symDirty
	count  int // 符号总数
	capped bool

	building    bool
	stop        *atomic.Bool
	done, total int // 这一轮读了几个、一共要读几个

	ver    int           // 变了就加一，面板据此重算
	recent []PaletteItem // 最近跳过的符号，最新的在前
}

// symJob 是后台要读的一个文件；old 是索引里那一版（新加的为零值）。
type symJob struct {
	r        *Repo
	rel, abs string
	old      olKey
}

// symResult 是读完一个文件：same 是版本没变，gone 是没了、太大、读不了、是二进制。
type symResult struct {
	symJob
	key        olKey
	syms       []symEntry
	same, gone bool
}

// symWorkers 是后台几个 worker 读盘、解析：一半的核，至少一个。
func symWorkers() int { return max(1, runtime.GOMAXPROCS(0)/2) }

func (ix *symIndex) drop(abs string) {
	if f := ix.files[abs]; f != nil {
		ix.count -= len(f.syms)
		delete(ix.files, abs)
	}
}

// SymbolIndexBuilding 是索引在后台建（或重新 stat）着，和这一轮的进度。
func (a *App) SymbolIndexBuilding() (done, total int, ok bool) {
	ix := &a.syms
	return ix.done, ix.total, ix.building
}

// symFilesChanged 是 watch 报仓库 r 里 dirs 下面（full：说不清哪里）的文件变了、或在应用里保存了：
// 索引建过的话记下来，下次用 "#" 时重新 stat 那些文件；面板正开着 "#" 就马上重算。
func (a *App) symFilesChanged(r *Repo, dirs []string, full bool) {
	ix := &a.syms
	if _, ok := ix.paths[r]; !ok {
		return
	}
	if ix.dirty == nil {
		ix.dirty = map[*Repo]*symDirty{}
	}
	d := ix.dirty[r]
	if d == nil {
		d = &symDirty{}
		ix.dirty[r] = d
	}
	if !d.full {
		d.full = full || len(d.dirs)+len(dirs) > maxSymDirty
		d.dirs = append(d.dirs, dirs...)
	}
	if d.full {
		d.dirs = nil
	}
	if p := &a.Palette; p.Open && strings.HasPrefix(p.Text, "#") {
		p.sig = "\x00"
	}
}

// samePaths 是同一个 Files.Paths（文件列表只整个换掉、不改里面的，所以看底层数组就行）。
func samePaths(x, y []string) bool {
	return len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
}

// refreshSymbols 在算 "#" 的列表时调：没在建的话，把文件列表换了的、标脏的仓库里要读的文件交给后台。
// 文件列表还没读好的仓库先跳过（读好了面板会重算，再调到这里）。
func (a *App) refreshSymbols() {
	ix := &a.syms
	if ix.building {
		return
	}
	if ix.files == nil {
		ix.files, ix.paths = map[string]*symFile{}, map[*Repo][]string{}
	}
	live := map[*Repo]bool{}
	for _, r := range a.Repos {
		live[r] = true
	}
	for abs, f := range ix.files {
		if !live[f.r] {
			ix.drop(abs)
		}
	}
	for r := range ix.paths {
		if !live[r] {
			delete(ix.paths, r)
			delete(ix.dirty, r)
		}
	}
	var jobs []symJob
	for _, r := range a.Repos {
		if !r.Files.Loaded {
			continue
		}
		old, seen := ix.paths[r]
		dirty := ix.dirty[r]
		if seen && dirty == nil && samePaths(old, r.Files.Paths) {
			continue
		}
		want := map[string]bool{}
		for _, rel := range r.Files.Paths {
			if !outline.Supported(rel) {
				continue
			}
			abs := filepath.Join(r.Root, filepath.FromSlash(rel))
			want[abs] = true
			f := ix.files[abs]
			switch {
			case f == nil && ix.capped:
				continue // 封顶了：新文件不加
			case f != nil && !dirty.covers(rel):
				continue // 没报过变化
			}
			j := symJob{r: r, rel: rel, abs: abs}
			if f != nil {
				j.old = f.key
			}
			jobs = append(jobs, j)
		}
		for abs, f := range ix.files {
			if f.r == r && !want[abs] {
				ix.drop(abs)
			}
		}
		ix.paths[r] = r.Files.Paths
		delete(ix.dirty, r)
	}
	if len(jobs) == 0 {
		return
	}
	stop := new(atomic.Bool)
	ix.building, ix.stop, ix.done, ix.total = true, stop, 0, len(jobs)
	up := a.Update
	go func() {
		out := make(chan symResult, 64)
		var next atomic.Int64
		var wg sync.WaitGroup
		for range min(symWorkers(), len(jobs)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for !stop.Load() {
					i := int(next.Add(1)) - 1
					if i >= len(jobs) {
						return
					}
					out <- readSymbols(jobs[i])
				}
			}()
		}
		go func() { wg.Wait(); close(out) }()
		var batch []symResult
		last := time.Now()
		for res := range out {
			batch = append(batch, res)
			if time.Since(last) >= symFlush {
				b := batch
				batch, last = nil, time.Now()
				up(func() { a.mergeSymbols(b, stop, false) })
			}
		}
		up(func() { a.mergeSymbols(batch, stop, true) })
	}()
}

// readSymbols 在后台读一个文件的符号：版本和索引里的一样就不读。
func readSymbols(j symJob) symResult {
	res := symResult{symJob: j}
	fi, err := os.Stat(j.abs)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > outline.MaxBytes {
		res.gone = true
		return res
	}
	res.key = olKey{j.abs, fi.Size(), fi.ModTime().UnixNano()}
	if res.key == j.old {
		res.same = true
		return res
	}
	data, err := os.ReadFile(j.abs)
	if err != nil || len(data) > outline.MaxBytes {
		res.gone = true
		return res
	}
	text, enc, _ := docs.Decode(data)
	if enc == docs.Binary {
		res.gone = true
		return res
	}
	res.syms = symEntries(outline.Build(outline.Parse(j.rel, text)).Flat)
	return res
}

// symEntries 把摊平的大纲拷成索引里的符号。
func symEntries(flat []outline.Flat) []symEntry {
	out := make([]symEntry, len(flat))
	for i, f := range flat {
		detail := f.Detail
		if len(detail) > maxSymDetail {
			n := maxSymDetail
			for n > 0 && !utf8.RuneStart(detail[n]) {
				n--
			}
			detail = detail[:n] + "…"
		}
		out[i] = symEntry{Name: strings.Clone(f.Name), Detail: strings.Clone(detail), Kind: f.Kind, Line: int32(f.Line)}
	}
	return out
}

// mergeSymbols 把后台读好的一批并进索引（主线程）；符号到了 maxIndexSymbols 就叫后台停下，剩下的新文件不加。
func (a *App) mergeSymbols(rs []symResult, stop *atomic.Bool, last bool) {
	ix := &a.syms
	if ix.stop != stop {
		return
	}
	for _, res := range rs {
		ix.done++
		switch {
		case res.same:
			continue
		case res.gone:
			ix.drop(res.abs)
			continue
		}
		old := ix.files[res.abs]
		n := len(res.syms)
		if old != nil {
			n -= len(old.syms)
		}
		if ix.count+n > maxIndexSymbols {
			ix.capped = true
			stop.Store(true)
			continue
		}
		ix.files[res.abs] = &symFile{r: res.r, rel: res.rel, key: res.key, syms: res.syms}
		ix.count += n
	}
	if last {
		ix.building, ix.stop = false, nil
	}
	ix.ver++
}

// workspaceSymbolItems 是 "#"：所有仓库里名字模糊匹配 q 的符号，分高的、路径短的、名字短的靠前，最多 maxSymbolItems 条；
// q 为空时列最近跳过的符号。
func (a *App) workspaceSymbolItems(q string) ([]PaletteItem, string) {
	a.refreshSymbols()
	ix := &a.syms
	progress := func(note string) string {
		switch {
		case ix.building:
			return i18n.Tf("正在建索引 %d / %d…", ix.done, ix.total)
		case ix.capped:
			return i18n.Tf("索引太大，只建了前 %d 个文件", len(ix.files))
		}
		return note
	}
	pat := fuzzy.New(q)
	if pat.Empty() {
		var items []PaletteItem
		for _, it := range ix.recent {
			if a.RepoFor(it.repo.Root) == it.repo {
				items = append(items, it)
			}
		}
		return items, progress(i18n.T("敲符号名在所有仓库里找；名字@符号 只找一个文件"))
	}
	// 只留最好的 maxSymbolItems 个（排好序）：几十万个符号匹配上大半时不用全排
	var best []symHit
	total := 0
	add := func(h symHit) {
		total++
		if len(best) == maxSymbolItems && h.cmp(best[len(best)-1]) >= 0 {
			return
		}
		i, _ := slices.BinarySearchFunc(best, h, symHit.cmp)
		best = slices.Insert(best, i, h)
		best = best[:min(len(best), maxSymbolItems)]
	}
	// 开着的文件用标签的大纲
	open := map[string]bool{}
	for _, d := range a.AllDocs() {
		if d.Kind != docs.File || d.Buf == nil || open[d.Abs] || !outline.Supported(d.Name()) {
			continue
		}
		r, rel := a.repoOfPath(d.Abs)
		if r == nil {
			continue
		}
		open[d.Abs] = true
		for _, f := range d.Outline().Flat {
			if sc, ok := pat.ScorePath(f.Name, 0); ok {
				add(symHit{r: r, rel: rel, s: symEntry{Name: f.Name, Detail: f.Detail, Kind: f.Kind, Line: int32(f.Line)}, score: sc})
			}
		}
	}
	live := map[*Repo]bool{}
	for _, r := range a.Repos {
		live[r] = true
	}
	for abs, f := range ix.files {
		if open[abs] || !live[f.r] {
			continue
		}
		for _, s := range f.syms {
			if sc, ok := pat.ScorePath(s.Name, 0); ok {
				add(symHit{r: f.r, rel: f.rel, s: s, score: sc})
			}
		}
	}
	note := ""
	switch {
	case total == 0:
		note = i18n.T("没有匹配的符号")
	case total > maxSymbolItems:
		note = i18n.Tf("%d 个符号匹配，只列最好的 %d 个", total, maxSymbolItems)
	}
	items := make([]PaletteItem, len(best))
	for i, h := range best {
		_, hits, _ := pat.MatchPath(h.s.Name, 0)
		items[i] = PaletteItem{Kind: ItemSymbol, Text: h.s.Name, Hits: hits, Detail: h.s.Detail, Sym: h.s.Kind,
			Loc: h.r.Name + "/" + h.rel + ":" + strconv.Itoa(int(h.s.Line)+1), repo: h.r, path: h.rel, jump: docs.Jump{Line: int(h.s.Line)}, at: true}
	}
	return items, progress(note)
}

// symHit 是 "#" 匹配上的一个符号。
type symHit struct {
	r     *Repo
	rel   string
	s     symEntry
	score int
}

// cmp 排先后：分高的、仓库/路径短的、名字短的靠前，再按仓库、路径、行号。
func (x symHit) cmp(y symHit) int {
	return cmp.Or(y.score-x.score, len(x.r.Name)+len(x.rel)-len(y.r.Name)-len(y.rel), len(x.s.Name)-len(y.s.Name),
		strings.Compare(x.r.Name, y.r.Name), strings.Compare(x.rel, y.rel), int(x.s.Line-y.s.Line))
}

// noteSymbol 把刚跳过去的工作区符号记到最近跳过的最前面（只在内存里）。
func (a *App) noteSymbol(it PaletteItem) {
	ix := &a.syms
	it.Hits = nil
	ix.recent = slices.DeleteFunc(ix.recent, func(o PaletteItem) bool {
		return o.repo == it.repo && o.path == it.path && o.jump == it.jump && o.Text == it.Text
	})
	ix.recent = slices.Insert(ix.recent, 0, it)
	ix.recent = ix.recent[:min(len(ix.recent), maxRecentSyms)]
	ix.ver++
}
