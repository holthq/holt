package app

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/fuzzy"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/outline"
	"github.com/holthq/holt/internal/snippets"
	"github.com/holthq/holt/internal/tasks"
)

// 这个文件是快速打开和命令面板：同一个浮层，按输入的开头分模式。
//
//	（空）        最近打开的文件（当前的除外）
//	名字          所有仓库的文件按"仓库/路径"模糊匹配，最近打开的靠前；"名字:12[:3]" 打开后跳到第 12 行（第 3 列）
//	:12[:3]       在当前文件里跳到第 12 行
//	@名字         当前文件大纲里的符号（"父 › 子" 模糊匹配），回车跳过去
//	名字@符号     名字打分第一的文件和它大纲里的符号，回车打开文件跳过去（palettesym.go）
//	#名字         所有仓库里名字模糊匹配的符号，回车打开文件跳过去（symindex.go）
//	>名字         命令（菜单里的全部），显示分组和快捷键，现在不能用的灰掉
//	>git 参数     在当前仓库里跑一条 git 命令（替代原来的 ⌘K 对话框），下面列最近跑过的
//	>term 名字    默认的 shell 和设置里的终端配置，回车在当前仓库开一个新终端（"用配置新建终端…"）
//	>snip 前缀    当前语言和所有语言的代码片段，回车插到光标处、开始跳位（"插入片段…"，snippets.go）
//	!名字         所有仓库的任务（npm scripts、make / just 目标……），回车在那个仓库的终端里跑（tasks.go）
//
// ⌘P 打开时输入是空的，⇧⌘P 带着 ">"，⌘K 带着 ">git "，⌘T 带着 "#"，⇧⌘B 带着 "!"。

const (
	maxPaletteItems = 100 // 列表最多列多少项
	maxRecent       = 50  // 记住多少个最近打开的文件（state.json）
	maxGitHistory   = 20  // 记住多少条跑过的 git 命令
	recentBonus     = 30  // 最近打开的文件加的分（最近的一个加满，越早越少）
)

// PaletteKind 是面板里的一项是什么。
type PaletteKind int

const (
	ItemFile       PaletteKind = iota // 仓库里的文件
	ItemLine                          // 当前文件里的一行
	ItemCommand                       // 注册表里的命令
	ItemGit                           // 跑输入的这条 git 命令
	ItemGitHistory                    // 跑过的 git 命令：选它把它填回输入框
	ItemTerm                          // 把输入的这行放进当前仓库的终端（不回车）
	ItemSymbol                        // 当前文件大纲里的一个符号（@）
	ItemProfile                       // 用这个终端配置新建终端（>term ）
	ItemTask                          // 在仓库的终端里跑这个任务（!）
	ItemSnippet                       // 把这条片段插进当前标签（>snip ）
	ItemDeleted                       // 从本地历史恢复这个删掉的文件（>restore ）
)

// PaletteItem 是面板列表的一项。
type PaletteItem struct {
	Kind   PaletteKind
	Text   string       // 匹配和显示的文字：仓库/路径、命令名、git 命令
	Name   int          // Text 里文件名开始的字节下标（"@" 符号是名字、"!" 任务是命令开始的；其他项为 0）
	Hits   []int        // 命中的字在 Text 里的字节下标
	Detail string       // 右边的灰字：命令的分组、git 命令在哪个仓库跑
	Keys   string       // 命令的快捷键
	Recent bool         // 最近打开过的文件、最近跑过的任务
	Off    bool         // 现在不能用的命令
	Sym    outline.Kind // ItemSymbol 的种类（界面画小字）
	Under  bool         // "名字@符号"里列在文件下面的符号（界面缩进一格）
	Loc    string       // "#" 工作区符号在哪：仓库/路径:行号（界面画在右边）

	repo *Repo
	path string
	cmd  *Command
	args string    // git 命令的参数（不带 git）
	jump docs.Jump // ItemLine、ItemSymbol，和带 :行号 的文件
	at   bool      // 有 jump
	task tasks.Task
	snip snippets.Snippet
}

// PaletteState 是面板的状态。
type PaletteState struct {
	Open  bool
	Text  string
	List  ui.ListState
	Sel   int
	Items []PaletteItem
	Note  string // 输入框下面的一行提示（怎么用、没有匹配、不能跳行的原因……）

	History []string // 跑过的 git 命令的参数，最新的在最后
	sig     string   // 上次算 Items 时的输入和文件数，变了才重算
}

// OpenPalette 打开面板，输入框里预先放 prefix（"" 快速打开、">" 命令、">git " git 命令、"@" 当前文件的符号、"#" 所有仓库里的符号），
// 并开始读还没读过的仓库的文件列表。
func (a *App) OpenPalette(prefix string) {
	p := &a.Palette
	p.Open, p.Text, p.Sel, p.sig = true, prefix, 0, "\x00"
	for _, r := range a.Repos {
		a.EnsureFiles(r)
	}
}

// ClosePalette 关掉面板。
func (a *App) ClosePalette() {
	p := &a.Palette
	p.Open, p.Text, p.Items, p.Note, p.sig = false, "", nil, "", ""
}

// UpdatePalette 在输入或文件列表变了之后重算列表；界面每帧画面板前调。
func (a *App) UpdatePalette() {
	p := &a.Palette
	var b strings.Builder
	b.WriteString(p.Text)
	for _, r := range a.Repos {
		fmt.Fprintf(&b, "\x00%s:%v:%d", r.Root, r.Files.Loaded, r.Files.Count)
	}
	if d := a.CurrentDoc(); strings.HasPrefix(p.Text, "@") && d != nil && d.Buf != nil {
		fmt.Fprintf(&b, "\x00@%p:%d", d, d.Buf.Version()) // 符号跟着当前标签和它的内容
	}
	if strings.HasPrefix(p.Text, "#") {
		fmt.Fprintf(&b, "\x00#%d", a.syms.ver) // 索引建着、更新了
	}
	if strings.HasPrefix(p.Text, "!") {
		fmt.Fprintf(&b, "\x00!%d", a.taskVer) // 读完状态才有任务
	}
	if sig := b.String(); sig != p.sig {
		p.sig = sig
		p.Items, p.Note = a.paletteItems(p.Text)
		p.Sel = min(0, len(p.Items)-1)
		p.List.ScrollTo(0, ui.Start)
	}
}

// MovePalette 把选中项上下移 d 项（↑ ↓，到头了绕回另一头）。
func (a *App) MovePalette(d int) {
	p := &a.Palette
	if n := len(p.Items); n > 0 {
		p.Sel = ((p.Sel+d)%n + n) % n
		p.List.ScrollIntoView(p.Sel)
	}
}

// paletteItems 按输入算列表和提示。
func (a *App) paletteItems(text string) ([]PaletteItem, string) {
	if rest, ok := strings.CutPrefix(text, "@"); ok {
		return a.symbolItems(strings.TrimSpace(rest))
	}
	if rest, ok := strings.CutPrefix(text, "#"); ok {
		return a.workspaceSymbolItems(strings.TrimSpace(rest))
	}
	if rest, ok := strings.CutPrefix(text, "!"); ok {
		return a.taskItems(strings.TrimSpace(rest))
	}
	if rest, ok := strings.CutPrefix(text, ">"); ok {
		rest = strings.TrimLeft(rest, " ")
		if args, ok := strings.CutPrefix(rest, "git "); ok {
			return a.gitItems(strings.TrimSpace(args))
		}
		if q, ok := strings.CutPrefix(rest, "term "); ok {
			if items, note := a.profileItems(strings.TrimSpace(q)); len(items) > 0 {
				return items, note
			}
			// 一个配置都对不上：当成找命令（英文界面敲 "term..." 找终端的命令）
		}
		if q, ok := strings.CutPrefix(rest, "snip "); ok {
			return a.snippetItems(strings.TrimSpace(q))
		}
		if q, ok := strings.CutPrefix(rest, "restore "); ok {
			return a.deletedItems(strings.TrimSpace(q))
		}
		return a.commandItems(strings.TrimSpace(rest))
	}
	text = strings.TrimSpace(text)
	if name, sym, ok := splitAt(text); ok {
		return a.fileSymbolItems(text, name, sym)
	}
	name, j, at := splitLine(text)
	switch {
	case name == "" && at:
		return a.lineItems(j)
	case name == "":
		items := a.recentItems()
		if len(items) == 0 {
			return nil, i18n.T("敲文件名在所有仓库里找 · 名字:行号 打开到那一行 · :行号 在当前文件跳行 · @ 当前文件的符号 · 名字@符号 别的文件里的符号 · # 所有仓库里的符号 · > 命令 · >git 运行 git 命令 · ! 运行任务")
		}
		return items, ""
	}
	return a.fileItems(name, j, at)
}

// splitLine 拆开 "名字:行[:列]"（行、列从 1 数）；没有行号时 at 为 false。
func splitLine(s string) (name string, j docs.Jump, at bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return s, j, false
	}
	if i == len(s)-1 {
		return s[:i], j, false // 还没敲行号
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 1 {
		return s, j, false
	}
	if k := strings.LastIndexByte(s[:i], ':'); k >= 0 {
		if l, err := strconv.Atoi(s[k+1 : i]); err == nil && l >= 1 {
			return s[:k], docs.Jump{Line: l - 1, Col: n - 1}, true
		}
	}
	return s[:i], docs.Jump{Line: n - 1}, true
}

func lineText(j docs.Jump) string {
	s := i18n.Tf("第 %d 行", j.Line+1)
	if j.Col > 0 {
		s += i18n.Tf("第 %d 列", j.Col+1)
	}
	return s
}

// lineItems 是 ":行号"：在当前文件里跳。
func (a *App) lineItems(j docs.Jump) ([]PaletteItem, string) {
	d := a.CurrentDoc()
	if d == nil {
		return nil, i18n.T("先打开一个文件再跳行")
	}
	if _, ok := d.Cursor(); !ok {
		return nil, i18n.T("先打开一个文件再跳行")
	}
	it := PaletteItem{Kind: ItemLine, Text: i18n.Tf("跳到%s", lineText(j)), Detail: i18n.Tn("%s · 共 %d 行", d.Title, d.Buf.LineCount()),
		jump: j, at: true}
	return []PaletteItem{it}, ""
}

// symbolItems 是 "@"：当前文件大纲里模糊匹配 q 的符号（匹配"父 › 子"，名字里命中的靠前）；q 为空时按文件里的顺序。
func (a *App) symbolItems(q string) ([]PaletteItem, string) {
	d := a.CurrentDoc()
	if d == nil || d.Kind == docs.Diff || d.Buf == nil {
		return nil, i18n.T("先打开一个文件再找符号")
	}
	tree := d.Outline()
	if !outline.Supported(d.Name()) || d.Buf.Len() > outline.MaxBytes {
		return nil, i18n.Tf("%s 没有大纲", d.Title)
	}
	if len(tree.Flat) == 0 {
		return nil, i18n.T("没有符号")
	}
	items := symbolsOf(tree.Flat, q)
	if len(items) == 0 {
		return nil, i18n.T("没有匹配的符号")
	}
	return items, ""
}

// recentItems 是最近打开过、还在列表里某个仓库下的文件，当前标签的除外。
func (a *App) recentItems() []PaletteItem {
	cur := ""
	if d := a.CurrentDoc(); d != nil && d.Kind == docs.File {
		cur = d.Abs
	}
	var items []PaletteItem
	for _, abs := range a.St.Recent {
		if abs == cur {
			continue
		}
		r, rel := a.repoOfPath(abs)
		if r == nil {
			continue
		}
		text := r.Name + "/" + rel
		items = append(items, PaletteItem{Kind: ItemFile, Text: text, Name: strings.LastIndexByte(text, '/') + 1,
			Recent: true, repo: r, path: rel})
	}
	return items
}

// repoOfPath 是磁盘路径 abs 所在的仓库和它在仓库里的路径。
func (a *App) repoOfPath(abs string) (*Repo, string) {
	for _, r := range a.Repos {
		if rel, err := filepath.Rel(r.Root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return r, filepath.ToSlash(rel)
		}
	}
	return nil, ""
}

// fileItems 是所有仓库里按"仓库/路径"模糊匹配 q 的文件，分高的、最近打开的、短的靠前，最多 maxPaletteItems 个。
func (a *App) fileItems(q string, j docs.Jump, at bool) ([]PaletteItem, string) {
	pat := fuzzy.New(q)
	recent := map[*Repo]map[string]int{}
	for i, abs := range a.St.Recent {
		if r, rel := a.repoOfPath(abs); r != nil {
			if recent[r] == nil {
				recent[r] = map[string]int{}
			}
			recent[r][rel] = i
		}
	}
	type hit struct {
		item  PaletteItem
		score int
	}
	var hs []hit
	loading := false
	for _, r := range a.Repos {
		if !r.Files.Loaded {
			loading = true
			continue
		}
		prefix := r.Name + "/"
		for _, rel := range r.Files.Paths {
			text := prefix + rel
			name := strings.LastIndexByte(text, '/') + 1
			sc, hits, ok := pat.Match(text, name)
			if !ok {
				continue
			}
			it := PaletteItem{Kind: ItemFile, Text: text, Name: name, Hits: hits, repo: r, path: rel, jump: j, at: at}
			if k, ok := recent[r][rel]; ok {
				sc += recentBonus * (maxRecent - k) / maxRecent
				it.Recent = true
			}
			hs = append(hs, hit{it, sc})
		}
	}
	slices.SortFunc(hs, func(x, y hit) int {
		return cmp.Or(y.score-x.score, len(x.item.Text)-len(y.item.Text), strings.Compare(x.item.Text, y.item.Text))
	})
	note := ""
	switch {
	case len(hs) == 0 && loading:
		note = i18n.T("正在读文件列表…")
	case len(hs) == 0:
		note = i18n.T("没有匹配的文件")
	case len(hs) > maxPaletteItems:
		note = i18n.Tf("%d 个文件匹配，只列最好的 %d 个", len(hs), maxPaletteItems)
		hs = hs[:maxPaletteItems]
	}
	if at && len(hs) > 0 {
		note = i18n.Tf("回车打开并跳到%s", lineText(j))
	}
	items := make([]PaletteItem, len(hs))
	for i, h := range hs {
		items[i] = h.item
	}
	return items, note
}

// commandItems 是名字模糊匹配 q 的命令（菜单里的全部）；q 为空时按菜单顺序。
func (a *App) commandItems(q string) ([]PaletteItem, string) {
	pat := fuzzy.New(q)
	type hit struct {
		item  PaletteItem
		score int
	}
	var hs []hit
	for _, c := range Commands {
		if c.Run == nil || (c.Group == "" && c.Context != Editor && c.Context != DiffTab && c.Context != GitList) {
			continue
		}
		title := c.Label()
		if n, ok := strings.CutPrefix(c.ID, "repo."); ok {
			if i, err := strconv.Atoi(n); err == nil {
				if i > len(a.Repos) {
					continue
				}
				title = i18n.Tf("切到仓库 %s", a.Repos[i-1].Name)
			}
		}
		sc, hits, ok := pat.Match(title, 0)
		// 编辑器自带的命令也按 mygo 的英文名搜（"foldAll" 找得到"全部折叠"），取分高的；英文名赢了标题上不标字
		if en, found := strings.CutPrefix(c.ID, "editor."); found && c.Section != "" {
			if esc, _, eok := pat.Match(en, 0); eok && (!ok || esc > sc) {
				sc, hits, ok = esc, nil, true
			}
		}
		if !ok {
			continue
		}
		detail := i18n.T(cmp.Or(c.Group, c.Context.String()))
		if c.Section != "" {
			detail += " · " + i18n.T(c.Section)
		}
		if r := a.Current(); c.ID == "git.openWeb" && r != nil && r.Extra.Web.OK() {
			detail += " · " + a.WebOf(r).Host // 在哪个网站开
		}
		if l := a.FileUndoLabel(); c.ID == "tree.undo" && l != "" {
			detail += " · " + l // 要撤的是什么
		}
		if why := a.WhyNot(c); why != "" {
			detail += " · " + why // 灰着的 Git 操作写上为什么
		}
		hs = append(hs, hit{PaletteItem{Kind: ItemCommand, Text: title, Hits: hits, Detail: detail, Keys: ShownKeys(c),
			Off: !a.CanRun(c), cmd: c}, sc})
	}
	if !pat.Empty() {
		slices.SortStableFunc(hs, func(x, y hit) int { return y.score - x.score })
	}
	items := make([]PaletteItem, len(hs))
	for i, h := range hs {
		items[i] = h.item
	}
	// 敲的不是命令名也行：最后一项把它当 shell 命令放进当前仓库的终端，回不回车自己定。
	if r := a.Current(); q != "" && r != nil {
		items = append(items, PaletteItem{Kind: ItemTerm, Text: i18n.Tf("在终端运行：%s", q),
			Detail: i18n.Tf("放进 %s 的终端，不回车", r.Name), args: q})
	}
	if len(hs) == 0 {
		return items, i18n.T("没有匹配的命令；在当前仓库跑 git 命令请敲 >git 加参数")
	}
	return items, ""
}

// gitItems 是 ">git 参数"：第一项跑这条命令，下面是最近跑过的（匹配参数的）。
func (a *App) gitItems(args string) ([]PaletteItem, string) {
	r := a.Current()
	if r == nil {
		return nil, i18n.T("列表里没有仓库")
	}
	where := r.Name
	if r.Status.Branch != "" {
		where += " · " + r.Status.Branch
	}
	var items []PaletteItem
	why := git.NeedsTerminal(git.SplitArgs(args))
	if args != "" {
		detail := i18n.Tf("在 %s 里运行", where)
		if why != "" {
			detail = i18n.Tf("在 %s 的终端里运行", where)
		}
		items = append(items, PaletteItem{Kind: ItemGit, Text: "git " + args, Detail: detail, args: args})
	}
	pat := fuzzy.New(args)
	for _, h := range slices.Backward(a.Palette.History) {
		if h == args {
			continue
		}
		if _, _, ok := pat.Match(h, 0); ok {
			items = append(items, PaletteItem{Kind: ItemGitHistory, Text: "git " + h, Detail: i18n.T("最近运行过"), args: h})
		}
	}
	if why != "" {
		return items, i18n.Tf("这条会在终端里跑：%s", why)
	}
	return items, i18n.Tf("在 %s 里运行；不弹密码提示、不开编辑器，会改仓库的命令也照跑；输出在底栏的“输出”里", where)
}

// profileItems 是 ">term 名字"：默认的 shell 和每个终端配置（名字模糊匹配），右边是跑的命令。
func (a *App) profileItems(q string) ([]PaletteItem, string) {
	s := &a.St.Settings
	pat := fuzzy.New(q)
	var items []PaletteItem
	add := func(name, text string, cmd []string) {
		if _, hits, ok := pat.Match(text, 0); ok {
			quoted := make([]string, len(cmd))
			for i, w := range cmd {
				quoted[i] = ShellQuote(w)
			}
			items = append(items, PaletteItem{Kind: ItemProfile, Text: text, Hits: hits, Detail: strings.Join(quoted, " "), args: name})
		}
	}
	def := []string{"$SHELL", "-l"}
	if s.Shell != "" {
		def[0] = s.Shell
	}
	add("", i18n.T("默认"), def)
	for _, pr := range s.Profiles() {
		add(pr.Name, pr.Name, pr.Command())
	}
	return items, i18n.Tf("在 %s 里新建终端；配置在设置 → 终端 → 其他配置里加", a.TermRepo().Name)
}

// snippetItems 是 ">snip 前缀"：当前标签能用的片段（它的语言的和所有语言的），前缀或说明模糊匹配，右边是说明。
func (a *App) snippetItems(q string) ([]PaletteItem, string) {
	d := a.CurrentDoc()
	if !snipEditable(d) {
		return nil, i18n.T("当前标签不是能改的文件，插不了片段")
	}
	pat := fuzzy.New(q)
	var items []PaletteItem
	for _, sn := range a.Snippets().For(snipLang(d)) {
		if _, hits, ok := pat.Match(sn.Prefix, 0); ok {
			items = append(items, PaletteItem{Kind: ItemSnippet, Text: sn.Prefix, Hits: hits, Detail: sn.Description(), snip: sn})
		} else if _, _, ok := pat.Match(sn.Description(), 0); ok {
			items = append(items, PaletteItem{Kind: ItemSnippet, Text: sn.Prefix, Detail: sn.Description(), snip: sn})
		}
	}
	if len(items) == 0 {
		return nil, i18n.T("没有对得上的片段；片段在设置 → 片段里看，目录里的 JSON 自己加")
	}
	return items, i18n.T("回车插到光标处，Tab / ⇧Tab 在跳位之间走")
}

// AcceptPalette 执行第 i 项（回车、点击）：打开文件（固定标签）、跳行、跑命令；
// 选最近跑过的 git 命令只是把它填回输入框，再回车才跑。
func (a *App) AcceptPalette(i int) { a.acceptPalette(i, false) }

// AcceptPaletteSide 是面板里的 ⌥回车：文件项在侧边打开（OpenFileSide，带行号的再跳过去），别的项和回车一样。
func (a *App) AcceptPaletteSide(i int) { a.acceptPalette(i, true) }

func (a *App) acceptPalette(i int, side bool) {
	p := &a.Palette
	if i < 0 || i >= len(p.Items) {
		return
	}
	it := p.Items[i]
	kind := it.Kind
	if kind == ItemSymbol && it.repo != nil {
		kind = ItemFile // "名字@符号"、"#"：别的文件里的符号，打开它再跳过去
		if it.Loc != "" {
			a.noteSymbol(it)
		}
	}
	switch kind {
	case ItemFile:
		a.ClosePalette()
		if a.RepoFor(it.repo.Root) == nil {
			a.Toast = i18n.Tf("%s 已经不在列表里了", it.repo.Name)
			return
		}
		switch {
		case side:
			if d := a.OpenFileSide(it.repo, it.path); it.at {
				a.jumpIn(d, it.jump, false)
			}
		case it.at:
			a.OpenAt(it.repo, it.path, it.jump, true)
		default:
			a.OpenFile(it.repo, it.path, true)
		}
		a.FocusDoc = true
	case ItemLine, ItemSymbol:
		a.ClosePalette()
		a.JumpHere(it.jump)
		a.FocusDoc = true
	case ItemCommand:
		if it.Off {
			a.Toast = cmp.Or(a.WhyNot(it.cmd), i18n.Tf("“%s”现在不能用", it.Text))
			return
		}
		a.ClosePalette()
		a.run(it.cmd)
	case ItemGit:
		a.ClosePalette()
		if r := a.Current(); r != nil {
			a.RunCommand(r, it.args)
		}
	case ItemGitHistory:
		p.Text = ">git " + it.args
	case ItemTerm:
		a.ClosePalette()
		a.RunInTerminal(it.args)
	case ItemProfile:
		a.ClosePalette()
		a.NewTerminalProfile(a.TermRepo(), "", it.args)
	case ItemTask:
		a.ClosePalette()
		if !it.repo.gone() {
			a.RunTask(it.repo, it.task)
		}
	case ItemSnippet:
		a.ClosePalette()
		a.InsertSnippet(a.CurrentDoc(), it.snip)
		a.FocusDoc = true
	case ItemDeleted:
		a.ClosePalette()
		a.RestoreDeleted(it.path)
	}
}

// noteRecent 把刚切到的文件标签记到最近打开的最前面（快速打开里靠前，存进 state.json）。
func (a *App) noteRecent(d *docs.Doc) {
	if d.Kind != docs.File || d.Root == "" || d.Abs == "" {
		return
	}
	rs := a.St.Recent
	if len(rs) > 0 && rs[0] == d.Abs {
		return
	}
	if i := slices.Index(rs, d.Abs); i >= 0 {
		rs = slices.Delete(rs, i, i+1)
	}
	rs = slices.Insert(rs, 0, d.Abs)
	a.St.Recent = rs[:min(len(rs), maxRecent)]
	a.SaveState()
}

// RunCommand 在仓库 r 里跑一条用户敲的 git 命令（命令面板的 ">git "，git 可省）：排进仓库的队列（queue.go），
// 跑完输出记进底栏的"输出"并切过去，失败再弹一个 toast。不开编辑器（git.RunOp）；要凭据时弹凭据框（askpass.go）。
func (a *App) RunCommand(r *Repo, line string) {
	args := git.SplitArgs(line)
	if len(args) == 0 {
		return
	}
	if r.Plain && args[0] != "init" {
		a.Toast = i18n.T("当前文件夹不是 Git 仓库") // git init 能跑：跑完重读就成了仓库
		return
	}
	cmd := "git " + strings.Join(args, " ")
	p := &a.Palette
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "git "))
	if i := slices.Index(p.History, line); i >= 0 {
		p.History = slices.Delete(p.History, i, i+1)
	}
	p.History = append(p.History, line)
	if len(p.History) > maxGitHistory {
		p.History = p.History[len(p.History)-maxGitHistory:]
	}
	if why := git.NeedsTerminal(args); why != "" { // 这里不开编辑器、不能一块块选：交给终端
		a.runGitInTerm(r, line)
		a.Toast = i18n.Tf("%s：%s，已在终端里运行", cmd, why)
		return
	}
	a.enqueue(r, &Op{Label: cmd, Cmd: "$ " + cmd, args: args, done: func(out string, err error) {
		text := logText(out, err)
		switch {
		case errors.Is(err, git.ErrCanceled):
			text = canceledText(out, cmd, nil)
		case text == "":
			text = i18n.T("（没有输出）")
		}
		a.Log(r, "$ "+cmd, text, err != nil)
		if r.gone() {
			return
		}
		a.BottomTab = BottomOutput
		if !a.St.BottomBar {
			a.ToggleArea(&a.St.BottomBar)
		}
		if err != nil && !errors.Is(err, git.ErrCanceled) {
			a.Toast = i18n.Tf("%s 失败，输出在底栏", cmd)
		}
	}})
}
