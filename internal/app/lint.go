package app

import (
	"cmp"
	"context"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
)

// 检查：保存时（设置“格式化”页的“保存时检查”，默认关）或手动（“检查当前文件”、编辑器右键）跑一条外部命令
// （go vet、tsc、eslint 那些），把输出里的 路径:行:列: 信息 认出来，标进编辑器（mygo 编辑器自带的诊断：波浪线、悬停、
// 滚动条上的标记）、列进底栏的“问题”页（所有仓库一起）。不是 LSP：只在保存和手动检查时跑一次，没有补全、跳转。
// 命令和格式化一样在 login shell、仓库根（仓库外的文件：文件所在的目录）跑，读磁盘上的文件（不走 stdin），LintTimeout 超时。

// DefaultLinters 是有内置默认检查命令的语言，key 是 editor.Language.Name。{file} 换成文件的绝对路径、{dir} 换成文件所在的
// 目录（仓库内路径，根是 .），都 ShellQuote 过。
var DefaultLinters = map[string]string{
	"Go":         "go vet ./{dir}",
	"TypeScript": "tsc --noEmit --pretty false",
	"JavaScript": "eslint {file}",
	"Python":     "ruff check --output-format concise {file}",
	"Rust":       "cargo check --message-format short",
	"Shell":      "shellcheck -f gcc {file}",
}

// LinterLangs 是设置里检查表先列出来的语言（都有内置默认），按这个顺序；设置里加过的别的语言排在后面。
var LinterLangs = []string{"Go", "TypeScript", "JavaScript", "Python", "Rust", "Shell"}

// LintTimeout 是一次检查最多等多久（cargo check、tsc 慢）；测试里调短。
var LintTimeout = 60 * time.Second

// LinterOf 是语言 lang 现在的检查命令（设置里改过的，没改是内置默认；占位符还没换）；"" 为没有或关掉了。
func (a *App) LinterOf(lang string) string {
	cmd := strings.TrimSpace(a.St.Settings.Linters[lang])
	if cmd == "" {
		cmd = DefaultLinters[lang]
	}
	if cmd == FormatOff {
		return ""
	}
	return cmd
}

// ExtraLinterLangs 是设置里另加了一行的语言（不在 LinterLangs 里），按名字排。
func (a *App) ExtraLinterLangs() []string {
	var out []string
	for l := range a.St.Settings.Linters {
		if !slices.Contains(LinterLangs, l) {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

// CanLint 是 d 能不能检查：读完了的磁盘文件（检查读的是磁盘上的，改了没存的也能查，查的是存过的那份）。
func CanLint(d *docs.Doc) bool {
	return d != nil && d.Kind == docs.File && d.Buf != nil && !d.Loading && d.Abs != ""
}

// Problem 是检查命令报的一条问题。
type Problem struct {
	Root, Rel string // 仓库根和仓库内路径（/ 分隔）；仓库外的文件 Root 为空、Rel 是绝对路径
	Line, Col int    // 从 1 起；Col 为 0 是命令没给列
	Sev       editor.Severity
	Msg       string
	Source    string // 命令的第一个词（去掉目录）：go、tsc、eslint……
}

// Abs 是问题所在文件的绝对路径。
func (p Problem) Abs() string { return filepath.Join(p.Root, filepath.FromSlash(p.Rel)) }

// lintScope 是一次检查管哪些问题：跑完换掉的就是这一片里上次的。
type lintScope int

const (
	scopeRepo lintScope = iota // 命令不带 {file}、{dir}（tsc、cargo）：这个仓库这种语言的全部
	scopeDir                   // 带 {dir}（go vet）：这个目录里的（不含子目录）
	scopeFile                  // 带 {file}：这个文件的
)

// lintKey 是一次检查是谁：同一个 key 跑着时又要跑，记下来跑完再跑一次，不并发。
type lintKey struct {
	root, lang string // 仓库根（仓库外的文件为空）、语言
	scope      lintScope
	path       string // scopeFile：Rel；scopeDir：目录（仓库内路径，根是 .；仓库外的是绝对路径）
}

// lintSet 是问题按 仓库 + 语言 分的一份。
type lintSet struct{ root, lang string }

// lintJob 是一个在跑的检查。
type lintJob struct {
	src   string    // 命令的第一个词（“正在检查 go…”）
	again *lintSnap // 跑着时又存了一次：跑完照它再跑一次
}

// lintSnap 是要检查那一刻的标签：存过的文字（磁盘上的就是它）和版本，回来时按它算位置，之后的编辑由编辑器挪。
type lintSnap struct {
	d    *docs.Doc
	buf  *editor.Buffer
	ver  uint64
	text string // 改了没存时为空：磁盘上的不是这份，回来时按那时的文字算
}

func snapOf(d *docs.Doc) lintSnap {
	s := lintSnap{d: d, buf: d.Buf, ver: d.Buf.Version()}
	if !d.Modified() {
		s.text = d.Buf.String()
	}
	return s
}

// lintAnchor 是交给编辑器的诊断按哪一版文字算的：编辑器沿着之后的编辑自己挪。
type lintAnchor struct {
	ver uint64
	ds  []editor.Diagnostic
}

// Problems 是问题面板的全部（App.Probs）：按 仓库 + 语言 存，Gen 每变一次加一。
type Problems struct {
	Gen     int
	sets    map[lintSet][]Problem
	jobs    map[lintKey]*lintJob
	warned  map[string]bool               // 保存时检查失败提示过的语言：成功之前不再提示
	absGen  map[string]int                // 每个文件的问题最后一次变时的 Gen：编辑器只在自己那份变了时重设
	anchors map[*editor.Buffer]lintAnchor // 每个开着的缓冲区交给编辑器的那份
	list    []Problem                     // 排好的（面板），listGen、listOrd 是排的时候的 Gen 和仓库顺序
	listGen int
	listOrd []string // 侧栏拖动仓库以后面板马上按新顺序排
	errs    int      // list 里的错误、警告数
	warns   int
}

// ProblemList 是所有问题，按 仓库（列表里的顺序，仓库外的最后）→ 文件 → 行 → 列 排；errs、warns 是错误、警告数。
func (a *App) ProblemList() (list []Problem, errs, warns int) {
	p := &a.Probs
	same := slices.EqualFunc(p.listOrd, a.Repos, func(root string, r *Repo) bool { return root == r.Root })
	if p.listGen == p.Gen && same && (p.list != nil || p.Gen == 0) {
		return p.list, p.errs, p.warns
	}
	order := map[string]int{}
	for i, r := range a.Repos {
		order[r.Root] = i
	}
	rank := func(root string) int {
		if i, ok := order[root]; ok {
			return i
		}
		return len(a.Repos)
	}
	list = []Problem{}
	errs, warns = 0, 0
	for _, ps := range p.sets {
		list = append(list, ps...)
		for _, q := range ps {
			switch q.Sev {
			case editor.SeverityError:
				errs++
			case editor.SeverityWarning:
				warns++
			}
		}
	}
	slices.SortStableFunc(list, func(x, y Problem) int {
		return cmp.Or(cmp.Compare(rank(x.Root), rank(y.Root)), cmp.Compare(x.Root, y.Root), cmp.Compare(x.Rel, y.Rel),
			cmp.Compare(x.Line, y.Line), cmp.Compare(x.Col, y.Col), cmp.Compare(x.Msg, y.Msg))
	})
	p.list, p.listGen, p.errs, p.warns = list, p.Gen, errs, warns
	p.listOrd = p.listOrd[:0]
	for _, r := range a.Repos {
		p.listOrd = append(p.listOrd, r.Root)
	}
	return list, errs, warns
}

// LintRunning 是在跑的检查的命令名（去重，按名字排）：问题面板头上“正在检查 go…”。
func (a *App) LintRunning() []string {
	var out []string
	for _, j := range a.Probs.jobs {
		if !slices.Contains(out, j.src) {
			out = append(out, j.src)
		}
	}
	slices.Sort(out)
	return out
}

// Linting 是 d 那个 key 的检查在跑（编辑器右键的“检查”灰着）。
func (a *App) Linting(d *docs.Doc) bool {
	if !CanLint(d) {
		return false
	}
	lang := langName(d)
	cmd := a.LinterOf(lang)
	return cmd != "" && a.Probs.jobs[a.lintKeyOf(d, lang, cmd)] != nil
}

// langName 是 d 的语言名（设置里的 key）；没有语言是“纯文本”。
func langName(d *docs.Doc) string {
	if l := d.Buf.Language(); l != nil {
		return l.Name
	}
	return i18n.T("纯文本")
}

// lintKeyOf 是给 d 跑 cmd（占位符还没换）的 key。
func (a *App) lintKeyOf(d *docs.Doc, lang, cmd string) lintKey {
	k := lintKey{root: d.Root, lang: lang}
	switch {
	case strings.Contains(cmd, "{file}"):
		k.scope, k.path = scopeFile, d.Rel
		if d.Root == "" {
			k.path = filepath.ToSlash(d.Abs)
		}
	case strings.Contains(cmd, "{dir}") || d.Root == "": // 仓库外的文件没有“整个仓库”：按它所在的目录
		k.scope, k.path = scopeDir, path.Dir(d.Rel)
		if d.Root == "" {
			k.path = filepath.ToSlash(filepath.Dir(d.Abs))
		}
	}
	return k
}

// Lint 是“检查当前文件”（命令面板、编辑器右键、问题面板头上）：不看“保存时检查”开没开，没有命令时提示里说。
func (a *App) Lint(d *docs.Doc) { a.lint(d, true) }

// lintSaved 是存好了 d（refreshSaved）：开着“保存时检查”、这种语言有命令就检查。
func (a *App) lintSaved(d *docs.Doc) {
	if a.St.Settings.LintOnSave {
		a.lint(d, false)
	}
}

// lint 在后台给 d 跑这种语言的检查命令；manual 是手动的（没有命令、失败都提示）。同一个 key 在跑时只记下“跑完再跑一次”。
func (a *App) lint(d *docs.Doc, manual bool) {
	if CanLint(d) {
		a.lintFrom(snapOf(d), manual)
	}
}

func (a *App) lintFrom(sn lintSnap, manual bool) {
	d := sn.d
	lang := langName(d)
	cmd := a.LinterOf(lang)
	if cmd == "" {
		if manual {
			if strings.TrimSpace(a.St.Settings.Linters[lang]) == FormatOff {
				a.Toast = i18n.Tf("%s 的检查在设置里关掉了", lang)
			} else {
				a.Toast = i18n.Tf("没有 %s 的检查命令（设置里加）", lang)
			}
		}
		return
	}
	k := a.lintKeyOf(d, lang, cmd)
	p := &a.Probs
	if j := p.jobs[k]; j != nil {
		j.again = &sn
		return
	}
	if p.jobs == nil {
		p.jobs = map[lintKey]*lintJob{}
	}
	src, _, _ := strings.Cut(strings.TrimSpace(cmd), " ")
	src = filepath.Base(src) // /usr/local/bin/eslint → eslint
	job := &lintJob{src: src}
	p.jobs[k] = job

	base, dir := d.Root, path.Dir(d.Rel)
	if base == "" {
		base, dir = filepath.Dir(d.Abs), "."
	}
	run := strings.ReplaceAll(cmd, "{file}", ShellQuote(d.Abs))
	run = strings.ReplaceAll(run, "{dir}", ShellQuote(dir))
	r := a.RepoOf(d)
	ctx := context.Background()
	if r != nil {
		ctx = r.ctx
	}
	shell, title, root := a.shell(), i18n.Tf("检查 %s", d.Title)+" · $ "+run, d.Root
	timeout := LintTimeout // 包级开关在主线程上读好（测试会改它，后台 goroutine 不碰）
	up := a.Update
	go func() {
		res := runTool(ctx, shell, base, run, "", timeout)
		ps := ParseProblems(res.stdout+"\n"+res.stderr, base)
		ps = slices.DeleteFunc(ps, func(q Problem) bool { // 认错了的行（“zsh:1: …”这种）：文件不在就不算
			fi, err := os.Stat(q.Abs())
			return err != nil || fi.IsDir()
		})
		for i := range ps {
			ps[i].Source = src
			if root == "" { // 仓库外的：Rel 换成绝对路径
				ps[i].Root, ps[i].Rel = "", filepath.ToSlash(ps[i].Abs())
			}
		}
		up(func() {
			delete(p.jobs, k)
			if res.canceled || k.root != "" && a.RepoFor(k.root) == nil { // 跑的时候仓库移出了列表、换了工作区
				return
			}
			if msg := lintFailure(res, ps); msg != "" {
				if manual || !p.warned[lang] {
					if p.warned == nil {
						p.warned = map[string]bool{}
					}
					p.warned[lang] = true
					detail := res.stderr
					if detail == "" {
						detail = strings.TrimSpace(res.stdout)
					}
					a.Log(r, title, detail, true)
					a.Toast = msg
				}
			} else {
				delete(p.warned, lang)
				a.setProblems(k, ps, sn)
			}
			if j := job.again; j != nil && CanLint(j.d) {
				a.lintFrom(*j, false)
			}
		})
	}()
}

// lintFailure 是一次检查算不算没跑成：给人看的一句；跑成了（有问题行，或退出 0）为空。退出码不是 0 但认出了问题行算跑成了。
func lintFailure(res toolRun, ps []Problem) string {
	first := func() string {
		s := res.stderr
		if s == "" {
			s = strings.TrimSpace(res.stdout)
		}
		f, _, _ := strings.Cut(s, "\n")
		return f
	}
	switch {
	case res.canceled:
		return ""
	case res.timeout:
		return i18n.Tf("检查失败：%d 秒还没跑完", int(math.Ceil(LintTimeout.Seconds())))
	case len(ps) > 0:
		return ""
	case res.missing != "":
		return i18n.Tf("没找到 %s：装上，或在设置里改命令", res.missing)
	case res.err != nil:
		return i18n.Tf("检查失败：%s", res.err.Error())
	case res.exit != 0:
		if f := first(); f != "" {
			return i18n.Tf("检查失败：%s", f)
		}
		return i18n.Tf("检查失败：退出码 %d", res.exit)
	}
	return ""
}

// inScope 是问题 q 在 k 管的那一片里（跑完要换掉的）。
func (k lintKey) inScope(q Problem) bool {
	switch k.scope {
	case scopeFile:
		return q.Rel == k.path
	case scopeDir:
		return path.Dir(q.Rel) == k.path
	}
	return true
}

// setProblems 用 k 这次跑出来的 ps 换掉它那一片上次的；开着的标签里的诊断回头在 SyncProblems 里重设。
// sn 是照着跑的标签那一刻的样子。
func (a *App) setProblems(k lintKey, ps []Problem, sn lintSnap) {
	p := &a.Probs
	set := lintSet{k.root, k.lang}
	touched := map[string]bool{}
	var keep []Problem
	for _, q := range p.sets[set] {
		if k.inScope(q) {
			touched[q.Abs()] = true
		} else {
			keep = append(keep, q)
		}
	}
	for _, q := range ps {
		touched[q.Abs()] = true
	}
	if p.sets == nil {
		p.sets = map[lintSet][]Problem{}
	}
	if all := append(keep, ps...); len(all) > 0 {
		p.sets[set] = all
	} else {
		delete(p.sets, set)
	}
	a.touchProblems(touched)
	// 照着跑的那个标签：按跑那一刻的文字算位置，交给编辑器从那一版挪过来（跑的时候又打了字也对得上）
	if d := sn.d; sn.text != "" && d.Buf == sn.buf && touched[d.Abs] {
		p.anchors[sn.buf] = lintAnchor{sn.ver, diagnosticsIn(sn.text, a.problemsOf(d.Abs))}
	}
}

// touchProblems 是这几个文件的问题变了：Gen 加一，它们的标签重设诊断；顺手丢掉已经没有标签的缓冲区记的那份。
func (a *App) touchProblems(abs map[string]bool) {
	p := &a.Probs
	p.Gen++
	if p.absGen == nil {
		p.absGen = map[string]int{}
	}
	for f := range abs {
		p.absGen[f] = p.Gen
	}
	live := map[*editor.Buffer]bool{}
	for _, d := range a.AllDocs() {
		if d.Buf != nil {
			live[d.Buf] = true
		}
	}
	for b := range p.anchors {
		if !live[b] || abs[a.bufAbs(b)] {
			delete(p.anchors, b)
		}
	}
	if p.anchors == nil {
		p.anchors = map[*editor.Buffer]lintAnchor{}
	}
}

// bufAbs 是开着缓冲区 b 的那个文件标签的路径；没有为空。
func (a *App) bufAbs(b *editor.Buffer) string {
	for _, d := range a.AllDocs() {
		if d.Buf == b && d.Kind == docs.File {
			return d.Abs
		}
	}
	return ""
}

// problemsOf 是文件 abs 的问题，按行列排。
func (a *App) problemsOf(abs string) []Problem {
	list, _, _ := a.ProblemList()
	var out []Problem
	for _, q := range list {
		if q.Abs() == abs {
			out = append(out, q)
		}
	}
	return out
}

// ClearProblems 是问题面板头上的“清空”：所有仓库的问题都拿掉（在跑的照样跑完，回来照常列）。
func (a *App) ClearProblems() {
	p := &a.Probs
	abs := map[string]bool{}
	for _, ps := range p.sets {
		for _, q := range ps {
			abs[q.Abs()] = true
		}
	}
	p.sets = nil
	a.touchProblems(abs)
}

// OpenProblem 开到问题 q 那一行（问题面板单击：预览标签；双击、回车 pin 固定）。
func (a *App) OpenProblem(q Problem, pin bool) {
	j := docs.Jump{Line: q.Line - 1, Col: max(q.Col-1, 0)}
	if r := a.RepoFor(q.Root); r != nil && q.Root != "" {
		a.OpenAt(r, q.Rel, j, pin)
		return
	}
	if d := a.OpenPath(q.Abs()); d != nil {
		a.jumpIn(d, j, false)
	}
}

// ProblemPlace 是问题在哪：“仓库 · 路径:行:列”（仓库外的是 ~/… 路径:行:列）。
func (a *App) ProblemPlace(q Problem) string {
	at := strconv.Itoa(q.Line)
	if q.Col > 0 {
		at += ":" + strconv.Itoa(q.Col)
	}
	if r := a.RepoFor(q.Root); r != nil && q.Root != "" {
		return r.Name + " · " + q.Rel + ":" + at
	}
	return tildePath(q.Abs()) + ":" + at
}

// HasProblems 是有问题（“清空问题”亮着）。
func (a *App) HasProblems() bool { return len(a.Probs.sets) > 0 }

// ProblemsShown 是底栏开着、在问题页（视图菜单“问题面板”的勾）。
func (a *App) ProblemsShown() bool { return a.St.BottomBar && a.BottomTab == BottomProblems }

// ToggleProblems 是“问题面板”（⇧⌘M）：开在问题页就关掉底栏，不然开底栏、换到问题页。
func (a *App) ToggleProblems() {
	if a.ProblemsShown() {
		a.St.BottomBar, a.BottomMax = false, false
	} else {
		a.St.BottomBar, a.BottomTab = true, BottomProblems
	}
	a.SaveState()
}

// dropProblems 是仓库移出了列表：它的问题拿掉。
func (a *App) dropProblems(root string) {
	p := &a.Probs
	abs := map[string]bool{}
	for s, ps := range p.sets {
		if s.root == root {
			for _, q := range ps {
				abs[q.Abs()] = true
			}
			delete(p.sets, s)
		}
	}
	if len(abs) > 0 {
		a.touchProblems(abs)
	}
}

// SyncProblems 把 d 的问题交给编辑器（每帧，在编辑器画之前）：这个文件的问题变了、语言服务器来了新诊断、
// 或换了缓冲区（开晚了、重读了）才重设。有记下的那份（跑那一刻的文字、版本）就照它让编辑器沿编辑挪；
// 缓冲区没留那段历史、或没记的，按现在的文字算。外部检查和语言服务器两批合在一起交给编辑器。
func (a *App) SyncProblems(d *docs.Doc) {
	if d.Kind != docs.File || d.Buf == nil || d.Loading {
		return
	}
	p := &a.Probs
	gen := p.absGen[d.Abs]
	lrev := 0
	if ld := a.lang.diags[d.Buf]; ld != nil {
		lrev = ld.rev
	}
	if d.ProbBuf == d.Buf && d.ProbGen == gen && d.ProbLang == lrev {
		return
	}
	first := d.ProbBuf == nil
	d.ProbBuf, d.ProbGen, d.ProbLang = d.Buf, gen, lrev
	ps := a.problemsOf(d.Abs)
	ld := a.lang.diags[d.Buf]
	if first && len(ps) == 0 && (ld == nil || len(ld.ds) == 0) {
		return
	}
	cur := d.Buf.Version()
	d.St.SetDiagnostics(cur, nil)
	var lintDs []editor.Diagnostic
	if len(ps) > 0 {
		if an, ok := p.anchors[d.Buf]; ok {
			d.St.SetDiagnostics(an.ver, an.ds)
			lintDs = d.St.Diagnostics()
		}
		if len(lintDs) == 0 {
			lintDs = diagnosticsIn(d.Buf.String(), ps)
			if p.anchors == nil {
				p.anchors = map[*editor.Buffer]lintAnchor{}
			}
			p.anchors[d.Buf] = lintAnchor{cur, lintDs}
		}
	}
	var lspDs []editor.Diagnostic
	if ld != nil && len(ld.ds) > 0 {
		d.St.SetDiagnostics(cur, nil) // 映射不上时别把上面那批留下当语言服务器的
		d.St.SetDiagnostics(ld.ver, ld.ds)
		lspDs = d.St.Diagnostics()
	}
	d.St.SetDiagnostics(cur, slices.Concat(lintDs, lspDs))
}

// diagnosticsIn 把 ps 换成文字 text 里的诊断：行列（从 1 起，列按字节）换成偏移，超出的夹到行尾；End 是从 Start 起的
// 标识符尾（字母数字下划线），至少一个字。
func diagnosticsIn(text string, ps []Problem) []editor.Diagnostic {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	out := make([]editor.Diagnostic, 0, len(ps))
	for _, q := range ps {
		line := min(max(q.Line, 1), len(starts)) - 1
		ls, le := starts[line], len(text)
		if line+1 < len(starts) {
			le = starts[line+1] - 1
		}
		s := min(ls+max(q.Col-1, 0), le)
		e := s
		for e < le && (text[e] == '_' || text[e] >= '0' && text[e] <= '9' || text[e]|0x20 >= 'a' && text[e]|0x20 <= 'z' || text[e] >= 0x80) {
			e++
		}
		e = min(max(e, s+1), len(text))
		out = append(out, editor.Diagnostic{Start: s, End: e, Severity: q.Sev, Message: q.Msg, Source: q.Source})
	}
	return out
}

// 认的几种输出行（ParseProblems）。
var (
	// tsc：路径(行,列): error TS1234: 信息
	tscLine = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): *(error|warning|info|message|suggestion) +(TS\d+): *(.*)$`)
	// go vet、eslint -f unix、ruff concise、shellcheck -f gcc、cargo short、zig、luacheck：路径:行[:列]: 信息
	gccLine = regexp.MustCompile(`^([^\s(][^(]*?):(\d+)(?::(\d+))?: *(.*)$`)
	// 信息开头的级别（error[E0425]:、warning:、error TS1234: ……）
	levelWord = regexp.MustCompile(`(?i)^(error|warning|warn|note|info|hint)(?:\[([^\]]*)\]| +([A-Za-z]*\d+))?: *`)
	// eslint -f unix 行尾的 [Error/规则]
	eslintTag = regexp.MustCompile(`\s*\[(Error|Warning|Info)/([^\]]*)\]$`)
	// eslint 默认的 stylish：一行绝对路径，下面缩进的“  行:列  error  信息  规则”
	stylishLine = regexp.MustCompile(`^\s+(\d+):(\d+)\s+(error|warning)\s+(.*?)(?:\s{2,}(\S+))?$`)
)

// sevOf 是级别词对应的严重程度；没写级别算错误。
func sevOf(w string) editor.Severity {
	switch strings.ToLower(w) {
	case "warning", "warn":
		return editor.SeverityWarning
	case "note", "info", "message", "suggestion":
		return editor.SeverityInfo
	case "hint":
		return editor.SeverityHint
	}
	return editor.SeverityError
}

// ParseProblems 从检查命令的输出（stdout、stderr 一起）里认出问题行：路径相对 root 或绝对，统一成 root 下的路径（/ 分隔），
// root 外的丢掉；go 的“# 包名”行跳过、行首的“vet: ”去掉。Root 填 root，Source 留给调用的填。
func ParseProblems(out, root string) []Problem {
	real, _ := filepath.EvalSymlinks(root) // 临时目录 /var → /private/var：工具报的绝对路径可能是解开的
	rel := func(p string) (string, bool) {
		if filepath.IsAbs(p) {
			for _, r := range []string{root, real} {
				if r == "" {
					continue
				}
				if q, err := filepath.Rel(r, p); err == nil && q != "." && q != ".." && !strings.HasPrefix(q, "../") {
					return filepath.ToSlash(q), true
				}
			}
			return "", false
		}
		p = filepath.Clean(p)
		if p == "." || p == ".." || strings.HasPrefix(p, "../") {
			return "", false
		}
		return filepath.ToSlash(p), true
	}
	var ps []Problem
	stylish := "" // stylish 格式里当前的文件
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "# ") {
			stylish = ""
			continue
		}
		if m := stylishLine.FindStringSubmatch(line); m != nil && stylish != "" {
			if f, ok := rel(stylish); ok {
				q := Problem{Root: root, Rel: f, Sev: sevOf(m[3]), Msg: m[4]}
				q.Line, _ = strconv.Atoi(m[1])
				q.Col, _ = strconv.Atoi(m[2])
				if m[5] != "" {
					q.Msg += " (" + m[5] + ")"
				}
				ps = append(ps, q)
			}
			continue
		}
		if filepath.IsAbs(line) && !strings.Contains(line, ":") {
			stylish = line
			continue
		}
		line = strings.TrimPrefix(line, "vet: ")
		if m := tscLine.FindStringSubmatch(line); m != nil {
			if f, ok := rel(m[1]); ok {
				q := Problem{Root: root, Rel: f, Sev: sevOf(m[4]), Msg: m[6] + " (" + m[5] + ")"}
				q.Line, _ = strconv.Atoi(m[2])
				q.Col, _ = strconv.Atoi(m[3])
				ps = append(ps, q)
			}
			continue
		}
		m := gccLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		f, ok := rel(m[1])
		if !ok {
			continue
		}
		q := Problem{Root: root, Rel: f, Sev: editor.SeverityError}
		q.Line, _ = strconv.Atoi(m[2])
		q.Col, _ = strconv.Atoi(m[3])
		msg, code := m[4], ""
		if l := levelWord.FindStringSubmatch(msg); l != nil {
			q.Sev, code = sevOf(l[1]), l[2]+l[3]
			msg = msg[len(l[0]):]
		}
		if t := eslintTag.FindStringSubmatch(msg); t != nil {
			q.Sev, code = sevOf(t[1]), t[2]
			msg = msg[:len(msg)-len(t[0])]
		}
		if code != "" {
			msg += " (" + code + ")"
		}
		if q.Line < 1 {
			continue
		}
		q.Msg = strings.TrimSpace(msg)
		ps = append(ps, q)
	}
	return ps
}
