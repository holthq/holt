package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/lsp"
	"github.com/holthq/holt/internal/version"
)

// 语言服务器（LSP）：每个仓库根 + 每种语言一个进程，由设置里的命令（或内置默认）经 login shell 起。
// 文档同步用全文；编辑器要补全、悬停、跳转定义时问服务端，诊断由服务端推过来。
// 协议和进程都在 internal/lsp，这里只管把它接到标签和编辑器上。没有服务端时一切照旧（片段补全、外部检查）。

const (
	// langSyncMax 是交给语言服务器的文件上限：全文同步，再大每次敲字都要复制几 MB，不划算。
	langSyncMax = 2 << 20
	// langDebounce 是打字时两次 didChange 至少隔多久；补全、悬停、定义和保存会强制同步一次。
	langDebounce = 120 * time.Millisecond
	// langTimeout 是补全 / 定义这类请求最多等多久。
	langTimeout = 10 * time.Second
	// langHoverTimeout 是悬停请求最多等多久，短一点免得停在那儿等。
	langHoverTimeout = 6 * time.Second
	// langSnaps 是留几份最近的文字快照：服务端的诊断可能晚一两版才来，要按当时那一版算位置。
	langSnaps = 2
)

// DefaultLanguageServers 是有内置默认命令的语言（键是 editor.Language 的 Name）。命令经 login shell
// （shell -lc）跑，所以 Homebrew、cargo 装的服务器都在 PATH 上；"-" 表示不用。
var DefaultLanguageServers = map[string]string{
	"Go":         "gopls",
	"Rust":       "rust-analyzer",
	"C":          "clangd",
	"C++":        "clangd",
	"TypeScript": "typescript-language-server --stdio",
	"JavaScript": "typescript-language-server --stdio",
	"Python":     "pyright-langserver --stdio",
	"Lua":        "lua-language-server",
}

// LanguageServerLangs 是设置里语言服务器表先列出来的语言（都有内置默认），按这个顺序。
var LanguageServerLangs = []string{"Go", "Rust", "C", "C++", "TypeScript", "JavaScript", "Python", "Lua"}

// lspLanguageIDs 是 mygo 的语言名对应的 LSP languageId。
var lspLanguageIDs = map[string]string{
	"Go": "go", "Rust": "rust", "C": "c", "C++": "cpp", "Objective-C": "objective-c",
	"TypeScript": "typescript", "JavaScript": "javascript", "Python": "python", "Lua": "lua",
	"Ruby": "ruby", "PHP": "php", "Java": "java", "Kotlin": "kotlin", "Swift": "swift",
	"C#": "csharp", "Shell": "shellscript", "JSON": "json", "YAML": "yaml", "TOML": "toml",
	"HTML": "html", "CSS": "css", "SQL": "sql", "XML": "xml", "Markdown": "markdown",
	"Dockerfile": "dockerfile", "Makefile": "makefile", "Protobuf": "protobuf", "GraphQL": "graphql",
	"Zig": "zig", "Dart": "dart", "Scala": "scala", "R": "r",
}

// langState 是一个窗口的语言服务器：进程按仓库根 + 语言分，文档按缓冲区分（镜像共用一个缓冲区）。
type langState struct {
	servers map[string]*langServer      // 仓库根 + 语言 ID
	docs    map[*editor.Buffer]*langDoc // 打开的缓冲区
	byURI   map[string]*langDoc         // 服务端推诊断时按 URI 找
	diags   map[*editor.Buffer]*langDiag
	gen     int    // 每次换工作区 / 重启加一，旧的后台回调据此作废
	rev     int    // 诊断来一批加一
	cmdKey  string // 上次生效的语言服务器命令（设置里改了才重启）
}

type langServer struct {
	key    string
	root   string
	name   string // mygo 的语言名，提示里用
	cmd    string
	cl     *lsp.Client
	ready  bool
	cancel context.CancelFunc
}

type langSnap struct {
	ver  uint64
	text *lsp.Text
}

type langDoc struct {
	srv     *langServer
	buf     *editor.Buffer
	uri     string
	abs     string
	langID  string
	version uint64 // 最后一次交给服务端的那一版
	text    string
	last    time.Time
	snaps   []langSnap
	open    bool
}

type langDiag struct {
	ds  []editor.Diagnostic
	ver uint64
	rev int
}

// LanguageServerCommand 是某种语言现在的语言服务器命令：设置里改过的、内置默认；"" 为不用。
func (a *App) LanguageServerCommand(lang string) string {
	if cmd, ok := a.St.Settings.LanguageServers[lang]; ok {
		if cmd = strings.TrimSpace(cmd); cmd == "-" {
			return ""
		} else if cmd != "" {
			return cmd
		}
	}
	return DefaultLanguageServers[lang]
}

// ExtraLanguageServerLangs 是设置里另加了一行的语言（不在 LanguageServerLangs 里），按名字排。
func (a *App) ExtraLanguageServerLangs() []string {
	var out []string
	for l := range a.St.Settings.LanguageServers {
		if !slices.Contains(LanguageServerLangs, l) {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

// HasLangServers 是现在有没有语言服务器在跑（命令面板里"重启"能不能用）。
func (a *App) HasLangServers() bool { return len(a.lang.servers) > 0 }

// RestartLangServers 关掉所有语言服务器；开着的文档下一帧会按现在的设置重新起、重新打开。
func (a *App) RestartLangServers() {
	n := len(a.lang.servers)
	a.stopLangs()
	a.lang.cmdKey = a.langCommandKey()
	if n == 0 {
		a.Toast = i18n.T("没有正在运行的语言服务器")
		return
	}
	a.Toast = i18n.T("已重启语言服务器")
}

// stopLangs 关掉所有服务器；进行中的 initialize 取消，晚回来的结果按 gen 丢掉。
func (a *App) stopLangs() {
	a.lang.gen++
	for _, s := range a.lang.servers {
		if s.cancel != nil {
			s.cancel()
		}
		if s.cl != nil {
			s.cl.Close()
		}
	}
	a.lang = langState{gen: a.lang.gen, rev: a.lang.rev, cmdKey: a.lang.cmdKey}
}

// langCommandKey 是设置里的语言服务器命令，用来认设置改没改。
func (a *App) langCommandKey() string {
	b, _ := json.Marshal(a.St.Settings.LanguageServers)
	return string(b)
}

// LangTick 每帧跟上 d：打开的文档第一次交给服务端，文字变了发 didChange。
// 只对当前画着的那一组标签调用；没在画的标签没有人改它，不用同步。
func (a *App) LangTick(d *docs.Doc) {
	a.syncLangDoc(d, false)
}

// LangSaved 是文件写盘之后：先补上还没发的 didChange，再发 didSave。没起过语言服务器的文件（保存的测试、
// 从来没看过代码）不因为保存去起一个。
func (a *App) LangSaved(d *docs.Doc) {
	if d == nil || d.Buf == nil {
		return
	}
	if _, ok := a.lang.docs[d.Buf]; !ok {
		return
	}
	st := a.syncLangDoc(d, true)
	if st == nil || !st.open || st.srv == nil || !st.srv.ready || st.srv.cl == nil {
		return
	}
	st.srv.cl.DidSave(st.uri)
}

// LangClosed 是标签关掉之后：同一个缓冲区没有别的标签了才 didClose，并扔掉它的诊断。
func (a *App) LangClosed(d *docs.Doc) {
	if d == nil || d.Buf == nil {
		return
	}
	st := a.lang.docs[d.Buf]
	if st == nil {
		return
	}
	for _, o := range a.AllDocs() {
		if o != d && o.Buf == d.Buf {
			return
		}
	}
	a.dropLangDoc(st)
}

// syncLangDoc 保证 d 的文字已经交给服务端（必要时全文发一次 didChange）；force 是补全、悬停、
// 定义和保存要立刻用最新文字。服务端在起、没有、太大、发不出去时返回的状态可能还是旧的。
func (a *App) syncLangDoc(d *docs.Doc, force bool) *langDoc {
	st := a.langDocFor(d)
	if st == nil || st.srv == nil || !st.srv.ready || st.srv.cl == nil {
		return st
	}
	srv := st.srv
	if !st.open {
		a.openLangDoc(st)
		return st
	}
	v := st.buf.Version()
	if v == st.version {
		return st
	}
	if !force && time.Since(st.last) < langDebounce {
		return st
	}
	text := st.buf.String()
	if len(text) > langSyncMax {
		return st
	}
	if srv.cl.DidChange(st.uri, int(v), text) {
		st.version, st.text, st.last = v, text, time.Now()
		st.addSnap(v, text)
	}
	return st
}

// langDocFor 是 d 对应的文档状态；没有就按语言和命令起服务器、建一个。
func (a *App) langDocFor(d *docs.Doc) *langDoc {
	if a.LangOff {
		return nil
	}
	if d == nil || d.Buf == nil || d.Kind != docs.File || d.Loading || d.Big || d.Hex || d.Abs == "" {
		return nil
	}
	if st := a.lang.docs[d.Buf]; st != nil {
		return st
	}
	lang := d.Buf.Language()
	if lang == nil {
		lang = editor.LanguageFor(d.Abs)
	}
	if lang == nil {
		return nil
	}
	id := lspLanguageIDs[lang.Name]
	if id == "" {
		id = strings.ToLower(lang.Name)
	}
	cmd := a.LanguageServerCommand(lang.Name)
	if cmd == "" {
		return nil
	}
	root := d.Root
	if root == "" {
		root = filepath.Dir(d.Abs)
	}
	if a.lang.docs == nil {
		a.lang.docs = map[*editor.Buffer]*langDoc{}
	}
	if a.lang.byURI == nil {
		a.lang.byURI = map[string]*langDoc{}
	}
	uri := lsp.PathToURI(git.RealPath(d.Abs))
	if old := a.lang.byURI[uri]; old != nil && old.buf != d.Buf {
		a.dropLangDoc(old)
	}
	srv := a.langServer(root, lang.Name, id, cmd)
	st := &langDoc{srv: srv, buf: d.Buf, uri: uri, abs: d.Abs, langID: id}
	a.lang.docs[d.Buf] = st
	a.lang.byURI[uri] = st
	return st
}

// langServer 是 root 这个仓库里 id 这种语言的服务器；没有就起一个。
func (a *App) langServer(root, name, id, cmd string) *langServer {
	key := root + "\x00" + id
	if s := a.lang.servers[key]; s != nil {
		return s
	}
	if a.lang.servers == nil {
		a.lang.servers = map[string]*langServer{}
	}
	s := &langServer{key: key, root: root, name: name, cmd: cmd}
	a.lang.servers[key] = s
	a.startLangServer(s)
	return s
}

// startLangServer 在后台起进程、initialize；好了以后主线程把已经等着的文档 didOpen。
func (a *App) startLangServer(srv *langServer) {
	up, gen := a.Update, a.lang.gen
	ctx, cancel := context.WithCancel(context.Background())
	srv.cancel = cancel
	root := git.RealPath(srv.root)
	rootURI := lsp.PathToURI(root)
	opts := lsp.Options{
		Command:       srv.cmd,
		Shell:         a.shell(),
		Dir:           root,
		RootURI:       rootURI,
		Folders:       []lsp.WorkspaceFolder{{URI: rootURI, Name: filepath.Base(root)}},
		ClientName:    "Holt",
		ClientVersion: version.App,
		Logf:          log.Printf,
		OnDiagnostics: func(p lsp.PublishDiagnosticsParams) {
			up(func() {
				if a.lang.gen != gen || a.lang.servers[srv.key] != srv {
					return
				}
				a.langPublish(srv, p)
			})
		},
		OnExit: func(err error) {
			if err == nil || errors.Is(err, lsp.ErrClosed) {
				return
			}
			up(func() {
				if a.lang.gen != gen || a.lang.servers[srv.key] != srv {
					return
				}
				log.Printf("语言服务器 %s 退出了：%v", srv.cmd, err)
			})
		},
	}
	go func() {
		cl, err := lsp.Start(ctx, opts)
		up(func() {
			if a.lang.gen != gen || a.lang.servers[srv.key] != srv {
				if cl != nil {
					cl.Close()
				}
				return
			}
			if err != nil {
				log.Printf("语言服务器 %s 起不来：%v", srv.cmd, err)
				if a.langDocUses(srv) {
					a.Toast = i18n.Tf("%s 的语言服务器起不来（检查设置里的命令）", srv.name)
				}
				return
			}
			srv.cl, srv.ready = cl, true
			a.openLangDocs(srv)
		})
	}()
}

// langDocUses 是有没有打开的文档等着这个服务器（起不来时才提示）。
func (a *App) langDocUses(srv *langServer) bool {
	for _, st := range a.lang.docs {
		if st.srv == srv {
			return true
		}
	}
	return false
}

// openLangDocs 把等着的文档都 didOpen。
func (a *App) openLangDocs(srv *langServer) {
	for _, st := range a.lang.docs {
		if st.srv == srv && !st.open {
			a.openLangDoc(st)
		}
	}
}

func (a *App) openLangDoc(st *langDoc) {
	if st.open || st.srv == nil || st.srv.cl == nil || !st.srv.ready {
		return
	}
	text := st.buf.String()
	if len(text) > langSyncMax {
		return
	}
	v := st.buf.Version()
	if st.srv.cl.DidOpen(st.uri, st.langID, int(v), text) {
		st.open, st.version, st.text, st.last = true, v, text, time.Now()
		st.addSnap(v, text)
	}
}

// dropLangDoc 关掉一个文档：didClose 之后就没人再用它了。
func (a *App) dropLangDoc(st *langDoc) {
	if st.open && st.srv != nil && st.srv.ready && st.srv.cl != nil {
		st.srv.cl.DidClose(st.uri)
	}
	if a.lang.docs[st.buf] == st {
		delete(a.lang.docs, st.buf)
	}
	if a.lang.byURI[st.uri] == st {
		delete(a.lang.byURI, st.uri)
	}
	delete(a.lang.diags, st.buf)
}

func (st *langDoc) addSnap(ver uint64, text string) {
	st.snaps = append(st.snaps, langSnap{ver: ver, text: lsp.NewTextEncoding(text, st.srv.readyEncoding())})
	if len(st.snaps) > langSnaps {
		st.snaps = st.snaps[len(st.snaps)-langSnaps:]
	}
}

// readyEncoding 是服务端协商的位置单位；没有服务端时按协议默认。
func (srv *langServer) readyEncoding() lsp.Encoding {
	if srv == nil || srv.cl == nil {
		return lsp.UTF16
	}
	return srv.cl.PositionEncoding()
}

// snapshot 是版本 v 的文字；没有请求的版本（null）时给最后一份。
func (st *langDoc) snapshot(v *int) (*lsp.Text, uint64) {
	if v != nil {
		for i := len(st.snaps) - 1; i >= 0; i-- {
			if st.snaps[i].ver == uint64(*v) {
				return st.snaps[i].text, st.snaps[i].ver
			}
		}
	}
	if len(st.snaps) > 0 {
		s := st.snaps[len(st.snaps)-1]
		return s.text, s.ver
	}
	return nil, 0
}

// langPublish 是服务端推来的诊断：按它算的那一版文字换成字节偏移，交给 SyncProblems 和外部检查合在一起。
func (a *App) langPublish(srv *langServer, p lsp.PublishDiagnosticsParams) {
	st := a.lang.byURI[p.URI]
	if st == nil || st.srv != srv || st.buf == nil {
		return
	}
	text, ver := st.snapshot(p.Version)
	if text == nil {
		text = lsp.NewTextEncoding(st.buf.String(), srv.readyEncoding())
		ver = st.buf.Version()
	}
	ds := make([]editor.Diagnostic, 0, len(p.Diagnostics))
	for _, q := range p.Diagnostics {
		start, end := text.RangeOffsets(q.Range)
		if end <= start {
			end = min(start+1, len(text.String()))
		}
		ds = append(ds, editor.Diagnostic{
			Start: start, End: end, Severity: langSeverity(q.Severity), Message: q.Message, Source: q.Source,
		})
	}
	if a.lang.diags == nil {
		a.lang.diags = map[*editor.Buffer]*langDiag{}
	}
	a.lang.rev++
	a.lang.diags[st.buf] = &langDiag{ds: ds, ver: ver, rev: a.lang.rev}
}

func langSeverity(s int) editor.Severity {
	switch s {
	case 2:
		return editor.SeverityWarning
	case 3:
		return editor.SeverityInfo
	case 4:
		return editor.SeverityHint
	default:
		return editor.SeverityError
	}
}

// Completion 是编辑器要补全时调的：先给代码片段，再问语言服务器，回答回来后两批合在一起。
// 服务端还没好、没有补全能力、文件太大或文字没同步上时只给片段。
func (a *App) Completion(d *docs.Doc, req editor.CompletionRequest) {
	start, items, _ := a.snippetCompletionItems(d, req)
	if len(items) > 0 {
		d.St.ShowCompletions(start, items)
	}
	if d.Snip != nil {
		return
	}
	st := a.syncLangDoc(d, true)
	if st == nil || st.srv == nil || !st.srv.ready || st.srv.cl == nil || !st.open ||
		!st.srv.cl.Supports("completion") || st.version != d.Buf.Version() {
		return
	}
	cl := st.srv.cl
	uri, text, ver := st.uri, st.text, st.version
	pos := lsp.NewTextEncoding(text, cl.PositionEncoding()).Position(req.Offset)
	buf := d.Buf
	up, gen := a.Update, a.lang.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), langTimeout)
		defer cancel()
		its, err := cl.Completion(ctx, uri, pos)
		if err != nil || len(its) == 0 {
			return
		}
		up(func() {
			if a.lang.gen != gen {
				return
			}
			cur := a.lang.docs[buf]
			if cur == nil || cur != st || buf.Version() != ver || d.Snip != nil {
				return
			}
			out := a.mapCompletions(cur, its)
			if len(out) == 0 {
				return
			}
			d.St.ShowCompletions(start, append(slices.Clone(items), out...))
		})
	}()
}

// mapCompletions 把 LSP 的补全换成编辑器的：片段格式交给 mygo 的 ParseSnippet，解不开就当普通文字。
func (a *App) mapCompletions(st *langDoc, items []lsp.CompletionItem) []editor.Completion {
	out := make([]editor.Completion, 0, len(items))
	for _, it := range items {
		label := strings.TrimSpace(it.Label)
		if label == "" {
			continue
		}
		ins := it.InsertText
		if it.TextEdit != nil && it.TextEdit.NewText != "" {
			ins = it.TextEdit.NewText
		}
		if ins == "" {
			ins = label
		}
		c := editor.Completion{
			Label:         label,
			Detail:        strings.TrimSpace(it.Detail),
			Kind:          completionKind(it.Kind),
			Insert:        ins,
			FilterText:    it.FilterText,
			Documentation: lsp.Documentation(it.Documentation),
		}
		if it.InsertTextFormat == 2 {
			if sn, err := editor.ParseSnippet(ins); err == nil {
				c.Snippet = sn
				c.Insert = ""
			}
		}
		out = append(out, c)
	}
	return out
}

func completionKind(k int) editor.CompletionKind {
	switch k {
	case 2:
		return editor.CompletionMethod
	case 3, 4:
		return editor.CompletionFunction
	case 5, 10:
		return editor.CompletionField
	case 6:
		return editor.CompletionVariable
	case 9:
		return editor.CompletionModule
	case 7, 8, 22:
		return editor.CompletionType
	case 13, 14:
		return editor.CompletionKeyword
	case 15:
		return editor.CompletionSnippet
	case 12, 20, 21:
		return editor.CompletionConstant
	default:
		return editor.CompletionText
	}
}

// Hover 是编辑器问悬停时调的：服务端有回答就显示在悬停卡里（纯文字）。
func (a *App) Hover(d *docs.Doc, req editor.HoverRequest) {
	st := a.syncLangDoc(d, true)
	if st == nil || st.srv == nil || !st.srv.ready || st.srv.cl == nil || !st.open ||
		!st.srv.cl.Supports("hover") || st.version != d.Buf.Version() {
		return
	}
	cl := st.srv.cl
	uri, text, ver := st.uri, st.text, st.version
	pos := lsp.NewTextEncoding(text, cl.PositionEncoding()).Position(req.Offset)
	buf := d.Buf
	up, gen := a.Update, a.lang.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), langHoverTimeout)
		defer cancel()
		h, err := cl.Hover(ctx, uri, pos)
		if err != nil || h == nil {
			return
		}
		body := lsp.HoverText(h.Contents)
		if body == "" {
			return
		}
		up(func() {
			if a.lang.gen != gen || buf.Version() != ver || a.lang.docs[buf] != st {
				return
			}
			start, end := 0, 0
			if h.Range != nil {
				start, end = lsp.NewTextEncoding(text, cl.PositionEncoding()).RangeOffsets(*h.Range)
			}
			if start == end {
				start, end = lsp.NewText(text).WordAt(req.Offset)
			}
			d.St.ShowHover(start, end, body, nil)
		})
	}()
}

// Definition 是 ⌘点或 F12：问服务端，第一个结果在同一个文件里跳，别的文件打开到那一行。
// 服务端没有回答时什么也不做（不是每个词都有定义）。
func (a *App) Definition(d *docs.Doc, offset int) {
	st := a.syncLangDoc(d, true)
	if st == nil || st.srv == nil || !st.srv.ready || st.srv.cl == nil || !st.open ||
		!st.srv.cl.Supports("definition") || st.version != d.Buf.Version() {
		return
	}
	cl := st.srv.cl
	uri, text, ver := st.uri, st.text, st.version
	pos := lsp.NewTextEncoding(text, cl.PositionEncoding()).Position(offset)
	buf := d.Buf
	up, gen := a.Update, a.lang.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), langTimeout)
		defer cancel()
		locs, err := cl.Definition(ctx, uri, pos)
		if err != nil || len(locs) == 0 {
			return
		}
		up(func() {
			if a.lang.gen != gen || buf.Version() != ver || a.lang.docs[buf] != st {
				return
			}
			a.goToDefinition(st, locs[0])
		})
	}()
}

// goToDefinition 打开 loc 指的文件并跳到它的位置。跳之前把光标位置记进 ‹ ›。
func (a *App) goToDefinition(from *langDoc, loc lsp.Location) {
	path := lsp.URIToPath(loc.URI)
	if path == "" {
		return
	}
	j := a.jumpForLocation(from, path, loc.Range.Start, loc.Range.End)
	if r, rel := a.repoFile(path); r != nil {
		a.OpenAt(r, rel, j, true)
		return
	}
	d := a.OpenPath(path)
	if d != nil {
		d.JumpTo(j)
	}
}

// repoFile 是 abs 在哪个仓库里（仓库内路径）；不在任何仓库里时是 nil。
func (a *App) repoFile(abs string) (*Repo, string) {
	for _, r := range a.Repos {
		if rel := git.Under(r.Root, abs); rel != "" {
			return r, rel
		}
	}
	return nil, ""
}

// jumpForLocation 把 LSP 位置换成字节行列：目标文件开着就用缓冲区里的文字，否则读盘（最多 langSyncMax）。
func (a *App) jumpForLocation(from *langDoc, path string, start, end lsp.Position) docs.Jump {
	j := docs.Jump{Line: start.Line, Col: start.Character}
	var text *lsp.Text
	enc := lsp.UTF16
	if from != nil {
		enc = from.srv.readyEncoding()
	}
	for _, o := range a.AllDocs() {
		if o.Buf != nil && o.Abs != "" && git.SamePath(o.Abs, path) {
			text = lsp.NewTextEncoding(o.Buf.String(), enc)
			break
		}
	}
	if text == nil {
		if data, err := os.ReadFile(path); err == nil && len(data) <= langSyncMax {
			text = lsp.NewTextEncoding(string(data), enc)
		}
	}
	if text == nil {
		return j
	}
	s := text.Offset(start)
	e := text.Offset(end)
	j.Line = start.Line
	j.Col = max(0, s-text.LineStart(start.Line))
	if e > s {
		j.Len = e - s
	}
	return j
}
