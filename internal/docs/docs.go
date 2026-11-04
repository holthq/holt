// Package docs 是主区标签里打开的东西：工作区文件（可编辑）、历史里的版本（只读）、
// 一项变更的 diff 和终端输出的快照（只读）。这里只有数据和同步的读写；Read* 可以在后台 goroutine 里调用
// （只用传进去的值），Apply* 和其他方法只在主线程上用。
package docs

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/highlight"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/outline"
	"github.com/lemoeade/grove/internal/snippets"
)

// MaxEditBytes 是能编辑的最大文件；更大的文本只读打开（Doc.Big），图片不打开。
const MaxEditBytes = 8 << 20

// MaxViewBytes 是编辑器肯打开的最大文本文件（只读）；更大的只给一句说明。
const MaxViewBytes = 64 << 20

// MaxDiffBaseBytes 是和 HEAD 对比（改动条）的最大文件：mygo 在主线程上算这份 diff，2 MB 改百分之一的行第一帧要 270 ms。
// 文件或 HEAD 里的那份更大的不设 DiffBase，标签上面淡淡写一句（NoBase）。
const MaxDiffBaseBytes = 2 << 20

// Kind 是标签的种类。
type Kind int

const (
	File           Kind = iota // 磁盘上的文件：仓库里的或仓库外的
	Version                    // 历史里某次提交的一个文件，只读
	Diff                       // 一项变更（已暂存 / 未暂存 / 未跟踪 / 某次提交里的）的 diff
	TermText                   // 一格终端的内容（屏幕和回滚）的快照（⇧⌘C），只读；不记进会话、最近打开和 ⇧⌘T
	Rebase                     // 交互式变基向导（Doc.Rebase）：只在内存里，不记进会话、最近打开和 ⇧⌘T
	ReplacePreview             // 搜索页替换之前看的 diff（app.PreviewReplace）：只读，只在内存里，不记进会话、最近打开和 ⇧⌘T
	SearchDoc                  // 搜索结果开成的纯文本（app.OpenSearchEditor，Doc.Hits）：只读，只在内存里，不记进会话、最近打开和 ⇧⌘T
	Compare                    // 比较两个文件（app/filecompare.go）：左边 Abs，右边 Right 或剪贴板（RightText）；只读，只在内存里，不记进会话、最近打开和 ⇧⌘T
	Term                       // 开在编辑器区的终端标签（app 的 Repo.EdTerms 里 Num 为 TermNum 的那个）：记进会话，不记最近打开和 ⇧⌘T
	SettingsDoc                // 设置的 JSON（app.OpenSettingsDoc）：可编辑，⌘S 是套用设置、不写盘；只在内存里，不记进会话、最近打开和 ⇧⌘T
)

// Doc 是一个标签。
type Doc struct {
	Kind   Kind
	Root   string // 所属仓库的根；仓库外的文件为空
	Rel    string // 仓库内路径；仓库外的文件为空
	Abs    string // 磁盘路径；历史版本为空
	Commit string // 历史版本、提交里的 diff：提交哈希
	Title  string
	Pinned bool // 固定的标签；没固定的"预览"标签会被下一个单击打开的顶掉
	// Copy 是同一个文件开的第几份（view.openInOtherGroup）：0 是正本，1、2… 是镜像。镜像和正本共用缓冲区、
	// 对比基准和从磁盘读来的状态（Share），光标、滚动、折叠、查找、预览、图片缩放各自的；Key 带着它，所以各算各的标签
	Copy int

	Buf    *editor.Buffer
	St     editor.State
	Base   *editor.Buffer // HEAD（历史版本：父提交）里的版本，编辑器槽里标出改动
	Bitmap *ui.Bitmap     // 图片
	Img    ImgView        // 图片视图（图片、SVG 预览、图片 diff）的缩放和滚动
	// ImgDiff 是 Diff 标签两边都是图片（或一边没有）时的两张：旧、新
	ImgDiff *[2]ImgSide
	// Hex 是按十六进制看：缓冲区里是 HexDump 的 dump（只读），HexTotal 是文件一共多少字节（dump 只有前 HexMax）
	Hex      bool
	HexTotal int64
	Info     string // 不能显示时的说明（二进制、过大、读失败）
	Loading  bool
	// Gone 是要看的提交已经不在仓库里了（被变基后清理掉了、贮藏丢掉后 gc）：Info 写着 CommitGoneNote，下次会话不记
	Gone bool
	// Submodule 是 Diff 标签里的那一项是子模块：Info 写着两个提交（git.DiffContent.Submodule），下面有打开它的按钮
	Submodule bool
	// Rebase 是交互式变基向导的计划（Kind == Rebase）
	Rebase *RebasePlan
	// ReplaceVer、ReplaceText 是替换预览（Kind == ReplacePreview）按哪一版搜索结果（app 的搜索 ver）、哪个替换词生成的：对不上就是旧的
	ReplaceVer  int
	ReplaceText string
	// ReplaceHunk 是替换预览从一处进来时要滚到的那块改动（从 1 起，0 是不指定）：界面在 ToHunk 时用掉
	ReplaceHunk int
	// HunkLine 是从编辑器的改动卡“在 Diff 中打开”进来时要滚到的那块：新文件第 HunkLine-1 行所在（或它后面）的那块，0 是不指定
	HunkLine int
	// Hits 是搜索编辑器（Kind == SearchDoc）每一行指向的地方，下标是行号；SearchSrc 是 app 记的怎么重新生成
	// （查询、范围、结果：docs 不认 search 的类型），SearchLines 是生成时的上下文行数
	Hits        []SearchHit
	SearchSrc   any
	SearchLines int
	// Right 是比较标签（Kind == Compare）右边的磁盘路径，"" 是剪贴板：内容在 RightText（左边是 Abs）
	Right     string
	RightText []byte
	// Snapshot 是本地历史的标签：Compare 的左边、File 的 Abs 是那一条的内容文件（Compare 的右边是工作区的文件），
	// 值是条目的 T（unix 毫秒）；0 为不是。File 的只读，不记进会话、最近打开和 ⇧⌘T
	Snapshot int64
	// 磁盘上的文件在别处被改了，而这里也改了没存：标签上面显示一条，让用户选
	Conflict bool

	Mtime time.Time
	Size  int64
	Mode  os.FileMode
	Enc   Encoding // 文件的编码（自动认的或 ForcedEnc 解出来的）；保存按它写回，Writable 的才能编辑
	Gen   int      // 每次（重）读递增，旧的结果作废
	// ForcedEnc 是"用别的编码重新打开"选的编码（文件、历史版本），"" 为自动认；会话里记着（Tab.Enc），重读都按它
	ForcedEnc Encoding
	// Lossy 是按这个编码编回去和原来的字节不一样（GB18030、Shift_JIS 里单独的 0x80 之类），Broken 是选的编码有解不出来的字节：都只读
	Lossy, Broken bool
	// Lang 是状态栏手动选的语言（editor.Language 的 Name，PlainLang 是纯文本），"" 为按文件名认；文件标签的记进会话（Tab.Lang）
	Lang string
	// Indent 是状态栏手动选的这个文件的缩进（IndentAuto 按语言、"tab"、"2"、"4"、"8"），"" 为跟设置；文件标签的记进会话（Tab.Indent）
	Indent string
	// savedEOL 是状态栏换了换行（SetEOL）之前磁盘上那份的换行，"" 为没换过：SetLineEnding 不动内容版本，Modified 看它
	savedEOL string
	cursor   cursorInfo // CursorInfo 上次算的
	// 文字改没改（Modified 每帧都问）：缓冲区、内容版本、存盘次数都没变就用上次的结果。
	// mygo 的 Buffer.Modified 在改过之后每次都把当前文本和存盘文本各拼成整串比较，大文件每帧几 MB 分配。
	// MarkSaved 不动内容版本，所以自己数存盘次数（markSaved，全局的：镜像标签共用一个缓冲区）；Reset、换缓冲区分别动版本和指针。
	modBuf    *editor.Buffer
	modVer    uint64
	modSaves  uint64
	modVal    bool
	modChecks int // 真去问了几次 Buffer.Modified（ModifiedChecks）
	// Big 是超过 MaxEditBytes 的文本（文件、历史版本）：只读，不和 HEAD 对比，没有大纲、冲突检查和 blame，
	// 重读最多每 2 s 一次（app.syncFile）
	Big bool
	// BaseSum 是 Base 那份原始字节的摘要：syncFile 在后台拿新读的 HEAD 版本和它比，一样就不回主线程
	BaseSum Sum
	// NoBase 是 HEAD 里有这个文件、可它或文件超过 MaxDiffBaseBytes，没设对比基准（BaseSum 照记）
	NoBase bool
	// ReloadAt 是上次开始（重）读的时候，ReloadQueued 是大文件已经排了一次到点的重读（app.syncFile 节流用）
	ReloadAt     time.Time
	ReloadQueued bool

	Pending *Jump // 还没读完时要跳去的地方，读完再跳
	Top     int   // 恢复上次的标签时要滚到最上面的行；编辑器排过一次版后界面滚过去并清零

	// Markdown 预览（⇧⌘V）
	Preview bool
	MD      *markdown.Options // 预览的选项和读好的图片；第一次预览时建，改名后重建
	Anchor  string            // 预览里点了 #标题 的链接，下一帧滚到那个标题
	mdText  string
	mdVer   uint64
	mdOK    bool
	// SVG 预览（SVGPreview）：缓冲区变了才重新解析，svgImg 是最近一次成功的
	svgImg     *ui.SVG
	svgErr     string
	svgMissing []string
	svgVer     uint64
	svgOK      bool

	// 冲突标记块（ConflictBlocks）：缓冲区变了才重新扫
	cfBlocks []ConflictBlock
	cfVer    uint64
	cfOK     bool

	// 大纲（Outline）：只在侧栏的大纲看得见、@ 面板开着、或粘性滚动画着时才算，缓冲区变了才重新解析
	olTree *outline.Tree
	olVer  uint64
	olOK   bool
	olSeen uint64        // 粘性滚动（StickyOutline）见过的最新版本
	olEdit time.Time     // 和它第一次见到的时间
	olCost time.Duration // 上一次解析花了多久：超过 StickySlow 的，粘性滚动停手了再重新解析
	// 侧栏大纲的展开和选中，按标签记（键是 outline.Flat.Key）；OutlineSeen 是见过的有子项的符号（新出现的默认展开），
	// OutlineSyms 是展开状态对过的那棵树，OutlineCur 是上一帧光标所在的符号（变了才跟着选中、滚到）
	OutlineSt   ui.OutlineState[string]
	OutlineSeen map[string]bool
	OutlineSyms *outline.Tree
	OutlineCur  string
	OutlineSel  int // 侧栏大纲选中的行（-1 没选）；OutlineSeen 为 nil 时还没建过，从 -1 起
	// StickyCur 是粘性滚动上一帧看到的光标：变了、光标那一行落在吸着的行底下，就往上滚到露出来；-1 是下一帧再看一遍
	StickyCur int

	// Diff 标签
	Change git.Change
	DiffSt *editor.DiffState
	Raw    [2][]byte // 比较的两边原样的内容（旧、新）：DiffSt 的缓冲区把 CRLF 换成了 \n，块级操作的补丁要用原样的
	ToHunk bool      // 全文 diff 刚读出来、或刚切了局部 / 全文：界面下一帧把光标所在（还没动过就是第一处）那块改动滚进视图，并清掉

	Rest Rest // 编辑器 / Diff 视图的光标停手多久了（ui 的 restingEditor / restingDiff）

	// blame 装订线（⌃⌘B，app/blame.go）：按标签记，不进 state.json
	ShowBlame    bool
	Blame        *git.Blame // 读好的；nil 为还没读或读不了
	BlameErr     string     // 读不了的说明（太大、还没提交过、git 报的错）
	BlameFor     BlameKey   // Blame / BlameErr 是哪一版内容的；和现在的不一样、又没有改了没存时重读
	BlameLoading bool
	BlameInlay   editor.Inlay // 行内 blame现在交给编辑器的那一条；Text 为空 = 没放

	// 外部格式化（app/format.go）
	Formatting   bool // 正在后台跑：状态栏"正在格式化…"，再按一次不另起
	FormatWarned bool // 保存时格式化失败已经提示过了：这个标签格式化成功之前不再提示
	EatText      bool // 编辑器刚接了 ⇧⌥F：macOS 随后送进来的、⌥ 打出的那段字（Ï）吞掉（ui.docKeys）

	// 检查（app/lint.go）：交给编辑器的诊断是哪个缓冲区、这个文件的问题第几版（app.SyncProblems 对着它认要不要重设）
	ProbBuf *editor.Buffer
	ProbGen int

	// 代码片段（app/snippets.go）：Snip 是插进去的片段在跳位之间走（Tab / ⇧Tab，状态栏“片段 2/3”）；SnipOffer 是给编辑器
	// 补全的那批里的片段（选没选、选了哪一条事后对着缓冲区认）。按标签记，镜像不共用
	Snip      *snippets.Session
	SnipOffer *snippets.Offer

	// 终端输出标签：哪一格（TermPane.ID，窗口里唯一）、快照有几行、什么时候取的
	Pane  int
	Lines int
	Snap  time.Time

	// TermNum 是编辑器区的终端标签（Kind == Term）在仓库里的号（app.TermTab.Num）：docs 不认 app 的类型，app.TermTabOf 拿它找回来
	TermNum int
}

// BlameKey 是 blame 算的是哪一版：第几次（重）读、缓冲区的版本（保存后变了要重算）、仓库的 HEAD（提交、切分支后重算）。
type BlameKey struct {
	Gen  int
	Ver  uint64
	Head string
}

// Rest 是一个编辑器的光标"停手不闪"要记的东西：界面每帧读写，别处不用。
type Rest struct {
	H       ui.Handle // 绑在编辑器（Diff 视图是它的外框）上，建它之前就能问键盘在不在它那里
	At      time.Time // 最后一次按键、点击、改动、选区变化或拿到键盘
	Focused bool      // 上一帧键盘在不在它那里
	Resting bool      // 歇着：光标不闪
	Woke    bool      // 歇着的时候来了输入，下一帧醒
	Ver     uint64    // 上一帧的内容版本、主选区和选区个数
	Sel     editor.Selection
	NSel    int
	Side    editor.DiffSide
	Keyed   bool // 上一帧之后编辑器收到的最后一个输入是键盘（按键、文字、命令），不是指针：光标因它而动时多留几行余量（caretMargin），每帧清掉
}

// Jump 是要跳去的地方：行、列（字节）从 0 起；Len > 0 时选中这么多字节（搜索结果）。
type Jump struct{ Line, Col, Len int }

// JumpTo 把光标放到 j，那一行不在眼前就滚到中间；内容还没读完就记下，读完再跳。
func (d *Doc) JumpTo(j Jump) {
	if d.Buf == nil {
		d.Pending = &j
		return
	}
	d.Pending = nil
	p := editor.Pos{Line: j.Line, Col: j.Col}
	d.St.GoTo(p)
	if j.Len > 0 {
		start := d.Buf.Offset(p)
		d.St.Select(start, min(start+j.Len, d.Buf.Len()))
	}
}

// Cursor 是文件 / 历史版本标签的光标位置；没有编辑器（Diff、图片、还在读）时 ok 为 false。
func (d *Doc) Cursor() (p editor.Pos, ok bool) {
	if d.Buf == nil || d.Kind == Diff {
		return p, false
	}
	return d.St.CursorPos(), true
}

// Key 认出"同一个标签"：同一个文件、同一个版本、同一项变更。
type Key struct {
	Kind       Kind
	Root, Path string
	Commit     string
	Parent     string // InCommit 的 diff 和哪个版本比：比较两个提交的文件和提交自己的同一个文件是两个标签
	Change     git.Kind
	Code       byte // 冲突的 diff：'S' 是两边（:2 ↔ :3），和 HEAD ↔ 工作区那个分开
	Copy       int  // 同一个文件的第几份（Doc.Copy）：打开文件找的是 0（正本）
}

// Key 是这个标签的 Key。
func (d *Doc) Key() Key {
	k := Key{Kind: d.Kind, Root: d.Root, Path: d.Rel, Commit: d.Commit, Copy: d.Copy}
	switch d.Kind {
	case File:
		if d.Root == "" {
			k.Path = d.Abs
		}
	case TermText:
		k.Path = fmt.Sprintf("pane:%d", d.Pane)
	case Term:
		k.Path = fmt.Sprintf("term:%d", d.TermNum)
	case Rebase: // 一个仓库一个向导
		k.Path = ""
	case SearchDoc: // 同一个查询一个标签
		k.Path = d.Title
	case Compare: // 同样两边一个标签；和剪贴板比的，左边一样就是同一个
		k.Path = d.Abs + "\x00" + d.Right
	case Diff:
		k.Change, k.Path, k.Commit = d.Change.Kind, d.Change.Path, d.Change.Commit
		switch d.Change.Kind {
		case git.Conflict:
			k.Code = d.Change.Code
		case git.InCommit:
			k.Parent = d.Change.Parent
		}
	}
	return k
}

// NewFile 是仓库里一个文件的标签（还没读）。
func NewFile(root, rel string) *Doc {
	return &Doc{Kind: File, Root: root, Rel: rel, Abs: filepath.Join(root, filepath.FromSlash(rel)), Title: path.Base(rel)}
}

// NewPath 是仓库外一个文件的标签。
func NewPath(abs string) *Doc {
	return &Doc{Kind: File, Abs: abs, Title: filepath.Base(abs)}
}

// NewVersion 是历史里某次提交的一个文件的标签，标题如 a.txt @ 3818438。
func NewVersion(root string, ch git.Change) *Doc {
	return &Doc{Kind: Version, Root: root, Rel: ch.Path, Commit: ch.Commit, Change: ch,
		Title: path.Base(ch.Path) + " @ " + git.Short(ch.Commit)}
}

// NewDiff 是一项变更的 diff 标签，标题如 diff: a.txt。
func NewDiff(root string, ch git.Change) *Doc {
	d := &Doc{Kind: Diff, Root: root, Rel: ch.Path, Commit: ch.Commit, Change: ch, Title: "diff: " + path.Base(ch.Path)}
	if ch.Kind == git.Conflict && ch.Code == 'S' {
		d.Title = i18n.Tf("%s（两边）", path.Base(ch.Path))
	}
	if ch.Kind != git.InCommit {
		d.Abs = filepath.Join(root, filepath.FromSlash(ch.Path))
	}
	return d
}

// NewCompare 是比较两个文件的标签：left、right 是磁盘路径，right 为 "" 时右边是剪贴板的文字 clip。
// 标题如 a.go ↔ b.go，两边同名时带上一级目录；Root、Rel（左边所在的仓库）由调用方填。
func NewCompare(left, right string, clip []byte) *Doc {
	l, r := filepath.Base(left), filepath.Base(right)
	switch {
	case right == "":
		r = i18n.T("剪贴板")
	case l == r:
		l = filepath.Base(filepath.Dir(left)) + "/" + l
		r = filepath.Base(filepath.Dir(right)) + "/" + r
	}
	return &Doc{Kind: Compare, Abs: left, Right: right, RightText: clip, Title: l + " ↔ " + r}
}

// ReadCompare 在后台读比较标签的两边（右边 right 为 "" 时用 clip）：超过 git.MaxDiffBytes 的 TooLarge、有 NUL 的 Binary，
// 文字按编辑器认的编码解成 UTF-8。语言按两边的文件名认（剪贴板跟左边）。
func ReadCompare(left, right string, clip []byte) (git.DiffContent, error) {
	dc := git.DiffContent{OldName: left, NewName: right}
	read := func(p string) ([]byte, error) {
		fi, err := os.Stat(p)
		switch {
		case err != nil:
			return nil, err
		case fi.IsDir():
			return nil, fmt.Errorf(i18n.T("%s 是目录"), p)
		case fi.Size() > git.MaxDiffBytes:
			dc.TooLarge = true
			return nil, nil
		}
		return os.ReadFile(p)
	}
	old, err := read(left)
	if err != nil {
		return dc, err
	}
	nw := clip
	if right == "" {
		dc.NewName = left
		dc.TooLarge = dc.TooLarge || len(clip) > git.MaxDiffBytes
	} else if nw, err = read(right); err != nil {
		return dc, err
	}
	if dc.TooLarge {
		return dc, nil
	}
	if git.IsBinary(old) || git.IsBinary(nw) {
		dc.Binary = true
		return dc, nil
	}
	o, _ := Sniff(old)
	n, _ := Sniff(nw)
	dc.Old, dc.New = []byte(o), []byte(n)
	return dc, nil
}

// IsDiffView 是这个标签显示的是 diff（Diff、替换预览、比较）：J / K、上 / 下一处改动、没有大纲。
func (d *Doc) IsDiffView() bool {
	return d.Kind == Diff || d.Kind == ReplacePreview || d.Kind == Compare
}

// NewTerm 是仓库 root 里号为 num 的终端标签开在编辑器区的样子，title 是标签名（app.Repo.TermLabel）。
func NewTerm(root string, num int, title string) *Doc {
	return &Doc{Kind: Term, Root: root, TermNum: num, Title: title, Pinned: true}
}

// NewTermText 是一格终端（pane 是 TermPane.ID）的输出标签，标题如 终端 · 仓库 · 终端 1；内容由 SetTermText 放。
func NewTermText(root string, pane int, title string) *Doc {
	return &Doc{Kind: TermText, Root: root, Pane: pane, Title: title}
}

// NewReplacePreview 是仓库 root 里 rel 的替换预览标签（两边由 SetReplacePreview 放进来）。
func NewReplacePreview(root, rel string) *Doc {
	return &Doc{Kind: ReplacePreview, Root: root, Rel: rel, Title: i18n.Tf("替换预览: %s", path.Base(rel)), Pinned: true}
}

// SetReplacePreview 放进替换预览的两边：old 是现在的文字，nw 是换好的；ver、repl 是按哪一版结果、哪个替换词生成的。
// 超过 git.MaxDiffBytes 的和 Diff 标签一样不画。展开全文、布局和字体留着上一份的。
func (d *Doc) SetReplacePreview(old, nw []byte, ver int, repl string, expand bool) {
	d.Gen++
	d.Loading, d.Info = false, ""
	d.ReplaceVer, d.ReplaceText = ver, repl
	d.Size = int64(len(nw))
	if len(old) > git.MaxDiffBytes || len(nw) > git.MaxDiffBytes {
		d.Info, d.DiffSt = i18n.T("文件超过 2 MB，不显示 diff"), nil
		return
	}
	d.Raw = [2][]byte{old, nw}
	ob, nb := editor.NewBuffer(string(old)), editor.NewBuffer(string(nw))
	if l := editor.LanguageFor(d.Rel); l != nil {
		ob.SetLanguage(l)
		nb.SetLanguage(l)
	}
	st := &editor.DiffState{Old: ob, New: nb, ExpandUnchanged: expand}
	if d.DiffSt != nil {
		st.Layout, st.Font = d.DiffSt.Layout, d.DiffSt.Font
	}
	d.DiffSt, d.ToHunk = st, true
}

// SearchHit 是搜索编辑器的一行指向的地方：文件（Root、Path）里第 Line 行（从 0 起）。匹配行的 Marks 是这一行上的每一处，
// 上下文行、文件名那行为空；表头、空行、⋯ 的 Path 为空。
type SearchHit struct {
	Root, Path string
	Line       int
	Marks      []SearchMark
}

// SearchMark 是搜索编辑器里的一处匹配：Start、End 是在标签文字里的位置（字节，画高亮），Col 是在原文那一行里的列、
// Len 是打开时选中的长度（字节，缓冲区里的：跨行的连换行）。
type SearchMark struct {
	Start, End int
	Col, Len   int
}

// NewSearchDoc 是搜索编辑器的标签（内容由 SetSearchText 放进来）。
func NewSearchDoc(title string) *Doc {
	return &Doc{Kind: SearchDoc, Title: title, Pinned: true}
}

// SettingsDocTitle 是设置文档的标签名：.json 结尾，按 JSON 高亮。
const SettingsDocTitle = "settings.json"

// NewSettingsDoc 是设置文档的标签，内容是 text（workspace.SettingsJSON），算已保存。
func NewSettingsDoc(text string) *Doc {
	d := &Doc{Kind: SettingsDoc, Title: SettingsDocTitle, Pinned: true}
	d.SetSettingsText(text)
	return d
}

// SetSettingsText 把设置文档的内容换成 text（新生成的、规范格式的），算已保存；光标留在原来那行（超出的到最后一行）。
// 内容本来就是 text（保存的就是规范格式）时只算已保存，撤销历史留着。
func (d *Doc) SetSettingsText(text string) {
	d.Gen++
	line := 0
	switch {
	case d.Buf == nil:
		d.Buf = editor.NewBuffer(text)
		d.Buf.SetLanguage(d.language())
	case d.Buf.String() == text:
		d.markSaved()
		return
	default:
		line = d.St.CursorPos().Line
		d.Buf.Reset(text)
	}
	d.St.Buffer, d.St.Label = d.Buf, d.Title
	d.Size, d.Enc = int64(len(text)), UTF8
	d.Lines = CountLines(text)
	if line > 0 {
		d.JumpTo(Jump{Line: min(line, max(0, d.Lines-1))})
	}
}

// SetSearchText 放进搜索编辑器的文字和每行指向的地方：只读、UTF-8；重写时光标留在原来那行（超出的到最后一行）。
// 高亮（St.TextBackgrounds）清掉，界面按 Hits 重画。
func (d *Doc) SetSearchText(text string, hits []SearchHit) {
	d.Gen++
	d.Loading, d.Info = false, ""
	line := 0
	if d.Buf == nil {
		d.Buf = editor.NewBuffer(text)
	} else {
		line = d.St.CursorPos().Line
		d.Buf.Reset(text)
	}
	d.Hits = hits
	d.St.Buffer, d.St.Label, d.St.ReadOnly, d.St.TextBackgrounds = d.Buf, d.Title, true, nil
	d.Size, d.Enc = int64(len(text)), UTF8
	d.Lines = CountLines(text)
	d.JumpTo(Jump{Line: min(line, max(0, d.Lines-1))})
}

// HitAt 是搜索编辑器里 offset（字节）所在那行指向的地方，和落在哪一处上（没落在哪一处上是这一行的第一处；上下文行为 nil）。
// 那一行不指向文件时 ok 为假。
func (d *Doc) HitAt(offset int) (h SearchHit, m *SearchMark, ok bool) {
	if d.Buf == nil {
		return h, nil, false
	}
	line := d.Buf.Pos(offset).Line
	if line < 0 || line >= len(d.Hits) || d.Hits[line].Path == "" {
		return h, nil, false
	}
	h = d.Hits[line]
	for i := range h.Marks {
		if h.Marks[i].Start <= offset && offset <= h.Marks[i].End {
			return h, &h.Marks[i], true
		}
	}
	if len(h.Marks) > 0 {
		return h, &h.Marks[0], true
	}
	return h, nil, true
}

// NewRebase 是仓库 root 的交互式变基向导标签。
func NewRebase(root, title string, plan *RebasePlan) *Doc {
	return &Doc{Kind: Rebase, Root: root, Title: title, Rebase: plan}
}

// SetTermText 换上终端内容的快照 text（末尾的空行已去掉），at 是取的时候；光标放到最后一行、滚到那里。
func (d *Doc) SetTermText(text string, at time.Time) {
	d.Gen++
	d.Loading, d.Info = false, ""
	if d.Buf == nil {
		d.Buf = editor.NewBuffer(text)
	} else {
		d.Buf.Reset(text)
	}
	d.St.Buffer, d.St.Label, d.St.ReadOnly = d.Buf, d.Title, true
	d.Size, d.Enc = int64(len(text)), UTF8
	d.Lines, d.Snap = CountLines(text), at
	d.JumpTo(Jump{Line: max(0, d.Lines-1)})
}

// CountLines 是 text 有几行（空串是 0 行，末尾没有换行的最后一段也算一行）。
func CountLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

// Moved 是仓库里的文件在应用里改了名（或所在的目录改了名）：换路径和标题，语言按新名字重新认，
// 没保存的改动留着，之后存到新路径。
func (d *Doc) Moved(rel string) {
	d.Rel, d.Abs, d.Title = rel, filepath.Join(d.Root, filepath.FromSlash(rel)), path.Base(rel)
	if d.Buf != nil {
		d.Buf.SetLanguage(d.language())
		d.St.Label = i18n.Tf("编辑 %s", d.Title)
	}
	d.MD = nil     // 图片相对的目录变了
	d.olOK = false // 语言可能换了
}

// Share 让 d（同一个文件的另一份）跟上 o：共用 o 的缓冲区和对比基准，抄 o 从磁盘读来、保存时改的状态
// （时间、大小、编码、冲突、换行、语言、缩进、路径）；d 自己的光标、滚动、折叠、查找、预览、图片缩放不动。
// 缓冲区换了（o 重读建了新的）光标留在原来那行，还没读完时记下的跳转这时跳。
func (d *Doc) Share(o *Doc) {
	if d.Buf != o.Buf {
		swap := d.Buf != nil && o.Buf != nil
		var at editor.Pos
		var y float64
		if swap {
			at, y = d.St.CursorPos(), d.St.ScrollY()
			if at.Line >= o.Buf.LineCount()-1 {
				at = editor.Pos{Line: o.Buf.LineCount() - 1}
			}
		}
		d.Buf, d.St.Buffer = o.Buf, o.Buf
		d.mdOK, d.svgOK, d.cfOK, d.olOK = false, false, false, false
		if swap {
			d.St.GoTo(at)
			d.St.SetScrollY(y)
		}
		if d.Pending != nil && d.Buf != nil {
			d.JumpTo(*d.Pending)
		}
	}
	if d.Rel != o.Rel || d.Abs != o.Abs {
		d.MD = nil
	}
	d.Root, d.Rel, d.Abs, d.Title = o.Root, o.Rel, o.Abs, o.Title
	d.Bitmap, d.Info, d.Loading, d.Conflict, d.Hex, d.HexTotal = o.Bitmap, o.Info, o.Loading, o.Conflict, o.Hex, o.HexTotal
	d.Mtime, d.Size, d.Mode, d.Enc, d.ForcedEnc = o.Mtime, o.Size, o.Mode, o.Enc, o.ForcedEnc
	d.Lossy, d.Broken, d.Big, d.savedEOL, d.Lang, d.Indent = o.Lossy, o.Broken, o.Big, o.savedEOL, o.Lang, o.Indent
	d.Base, d.BaseSum, d.NoBase = o.Base, o.BaseSum, o.NoBase
	d.St.DiffBase, d.St.ReadOnly, d.St.Label = o.St.DiffBase, o.St.ReadOnly, o.St.Label
}

// IsMarkdown 是不是 Markdown 文件（能预览）。
func IsMarkdown(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// CanPreview 是能不能显示预览（⇧⌘V）：读好了的 Markdown 或 SVG文件或历史版本（大文件、十六进制不行）。
func (d *Doc) CanPreview() bool {
	return d.Kind != Diff && d.Buf != nil && !d.Big && !d.Hex && (IsMarkdown(d.Name()) || IsSVG(d.Name()))
}

// Name 是文件名（历史版本的标题带着提交号，这里不带）。
func (d *Doc) Name() string {
	if d.Rel != "" {
		return path.Base(d.Rel)
	}
	return filepath.Base(d.Abs)
}

// Dir 是文件所在的目录（磁盘路径）：预览里相对路径的链接和图片从这里找。历史版本用工作区里的同一个目录。
func (d *Doc) Dir() string {
	if d.Abs != "" {
		return filepath.Dir(d.Abs)
	}
	return filepath.Join(d.Root, filepath.FromSlash(path.Dir(d.Rel)))
}

// MarkdownSource 是预览要显示的文字：缓冲区变了才重新取一次。大文件为空（不为它拼整个缓冲区）。
func (d *Doc) MarkdownSource() string {
	if d.Big || d.Buf == nil {
		return ""
	}
	if v := d.Buf.Version(); !d.mdOK || v != d.mdVer {
		d.mdText, d.mdVer, d.mdOK = d.Buf.String(), v, true
	}
	return d.mdText
}

// ConflictBlocks 是文件标签里的冲突标记块：缓冲区变了才重新扫一次（每帧都问）。没读好、大文件、历史版本、Diff 标签为 nil。
func (d *Doc) ConflictBlocks() []ConflictBlock {
	if d.Buf == nil || d.Kind != File || d.Big {
		return nil
	}
	if v := d.Buf.Version(); !d.cfOK || v != d.cfVer {
		d.cfBlocks, d.cfVer, d.cfOK = ParseConflicts(d.Buf.String()), v, true
	}
	return d.cfBlocks
}

// Outline 是文件 / 历史版本标签的大纲（outline.Parse 再 Build）：缓冲区变了才重新解析。没读好、Diff、大文件、
// 没有大纲的语言、超过 outline.MaxBytes 的为 nil（先看这几样，不为它们把整个缓冲区拼成字符串）。
func (d *Doc) Outline() *outline.Tree {
	name := d.outlineName()
	if d.Kind == Diff || d.Kind == TermText || d.Kind == SearchDoc || d.Buf == nil || d.Big || d.Hex || !outline.Supported(name) || d.Buf.Len() > outline.MaxBytes {
		return nil
	}
	if v := d.Buf.Version(); !d.olOK || v != d.olVer {
		t0 := time.Now()
		d.olTree, d.olVer, d.olOK = outline.Build(outline.Parse(name, d.Buf.String())), v, true
		d.olCost = time.Since(t0)
		outlineParses.Add(1)
	}
	return d.olTree
}

// outlineParses 数 Outline 真解析了几次（测试看打字时是不是每个键都整篇解析）。
var outlineParses atomic.Int64

// OutlineParses 是到现在为止大纲整篇解析的次数（只给测试用）。
func OutlineParses() int64 { return outlineParses.Load() }

// 粘性滚动用的大纲：大于 StickyQuick 的文件，或上次解析超过 StickySlow 的，敲字的时候先用旧的，停手 StickyPause 再重新解析
// （900 KB 的 Go 文件解析一遍 20 多 ms，200 KB 的也要几 ms，每敲一个字在主线程上来一遍会掉帧）。几百行的小文件解析远不到 1 ms，照旧每次都算。
const (
	StickyQuick = 256 << 10
	StickyPause = 500 * time.Millisecond
)

// StickySlow 是解析多慢就算“慢”（120 Hz 一帧 8.3 ms，留给光栅化）。变量只为测试能改小。
var StickySlow = time.Millisecond

// StickyOutline 是粘性滚动用的大纲（ui/sticky.go）：小文件和 Outline 一样；大的、解析慢的改着的时候先给旧的那棵（行号可能差几行），
// wait > 0 时过这么久再来问。
func (d *Doc) StickyOutline(now time.Time) (t *outline.Tree, wait time.Duration) {
	if !d.olOK || d.Buf == nil || d.Buf.Len() <= StickyQuick && d.olCost < StickySlow {
		return d.Outline(), 0
	}
	v := d.Buf.Version()
	if v == d.olVer {
		return d.olTree, 0
	}
	if v != d.olSeen {
		d.olSeen, d.olEdit = v, now
	}
	if wait := d.olEdit.Add(StickyPause).Sub(now); wait > 0 {
		return d.olTree, wait
	}
	return d.Outline(), 0
}

// outlineName 是认大纲用的文件名：手动选了语言时换成那种语言的扩展名（纯文本没有大纲）。
func (d *Doc) outlineName() string {
	switch d.Lang {
	case "":
		return d.Name()
	case PlainLang:
		return ""
	}
	return "x" + outline.Ext(d.Lang)
}

// Modified 是有没保存的改动：文字改了，或状态栏换了换行。每帧都问（标签的圆点、状态栏），
// 文字那半按缓冲区、内容版本、存盘次数缓存（textModified）。
func (d *Doc) Modified() bool {
	return (d.Kind == File || d.Kind == SettingsDoc) && d.Buf != nil && !d.Hex && (d.textModified() || d.savedEOL != "" && d.Buf.LineEnding() != d.savedEOL)
}

// ModifiedChecks 是 textModified 真去问了几次 Buffer.Modified（测试用：画 N 帧只该问常数次）。
func (d *Doc) ModifiedChecks() int { return d.modChecks }

// textModified 是 d.Buf.Modified()，缓冲区、版本、存盘次数都和上次一样时直接用上次的结果。
// 版本只增不减，撤回到和存盘一样也是新版本，会重新比一次，圆点照样消失。
func (d *Doc) textModified() bool {
	v := d.Buf.Version()
	if d.modBuf != d.Buf || d.modVer != v || d.modSaves != saves.Load() {
		d.modChecks++
		d.modBuf, d.modVer, d.modSaves, d.modVal = d.Buf, v, saves.Load(), d.Buf.Modified()
	}
	return d.modVal
}

// saves 是调过几次 markSaved。按全局数而不按 Doc 数：镜像标签（分屏里同一个文件的两份）共用一个缓冲区，
// 一份存了另一份的缓存也得失效。存盘很少，每次让所有标签各重新比一次不碍事。
var saves atomic.Uint64

// markSaved 把当前文字记成存盘的那份（Buffer.MarkSaved 不动内容版本，这里让 Modified 的缓存失效）。
func (d *Doc) markSaved() {
	d.Buf.MarkSaved()
	saves.Add(1)
}

// SetEOL 把换行换成 eol（"\n" / "\r\n"，状态栏换行那项选的）：内容没变，保存前算改过。
func (d *Doc) SetEOL(eol string) {
	if d.Buf == nil || d.Buf.LineEnding() == eol {
		return
	}
	if d.savedEOL == "" {
		d.savedEOL = d.Buf.LineEnding()
	}
	d.Buf.SetLineEnding(eol)
}

// PlainLang 是 Doc.Lang 里"纯文本"的写法，IndentAuto 是 Doc.Indent 里"按语言"的（设置不是按语言时这个文件按语言）。
const (
	PlainLang  = "plaintext"
	IndentAuto = "auto"
)

// SetLang 换语言（状态栏语言那项选的；"" 为按文件名认）：高亮、注释、缩进、大纲跟着换。
func (d *Doc) SetLang(name string) {
	if name != "" && name != PlainLang && editor.LanguageNamed(name) == nil {
		name = "" // 会话里存的、这个版本不认识的语言：照文件名认
	}
	d.Lang, d.olOK = name, false
	if d.Buf != nil {
		d.Buf.SetLanguage(d.language())
	}
}

// Editable 是能不能保存：磁盘上的文本文件（不是大文件），编码能原样写回；设置文档（保存是套用设置，app.WriteDoc 里分开走）。
func (d *Doc) Editable() bool {
	return d.Kind == SettingsDoc && d.Buf != nil || d.Kind == File && d.Buf != nil && d.Abs != "" && !d.Big && !d.Hex && d.Enc.Writable() && !d.Lossy && !d.Broken && d.Snapshot == 0
}

// Convertible 是只读条和编码菜单里能不能"转成 UTF-8…"：磁盘上不大的文件，不是 UTF-8 的文本编码，解得通。
func (d *Doc) Convertible() bool {
	return d.Kind == File && d.Buf != nil && d.Abs != "" && !d.Big && !d.Hex && d.Enc.Convertible() && !d.Broken && d.Snapshot == 0
}

// Where 是状态栏和提示里显示的路径。
func (d *Doc) Where() string {
	switch {
	case d.Kind == TermText || d.Kind == Rebase || d.Kind == SearchDoc || d.Kind == Compare || d.Kind == Term || d.Kind == SettingsDoc:
		return d.Title
	case d.Kind == Version:
		return d.Rel + " @ " + git.Short(d.Commit)
	case d.Kind == Diff && d.Change.Range:
		return d.Rel + " @ " + git.Short(d.Change.Parent) + " → " + git.Short(d.Commit)
	case d.Kind == Diff && d.Change.Kind == git.InCommit:
		return d.Rel + " @ " + git.Short(d.Commit)
	case d.Rel != "":
		return d.Rel
	}
	return d.Abs
}

// Loaded 是后台读好的东西，交给 Apply* 放进标签：解码、建缓冲区、读 HEAD 版本都在后台做完，主线程上只换指针。
type Loaded struct {
	Info     os.FileInfo
	Note     string // 不打开的说明（过大），原样放进 Info
	Image    bool   // 图片：Data 是原始字节，主线程上解码
	Hex      bool   // 十六进制 dump（二进制文件、以十六进制查看）：Buf 是建好的 dump，Total 是一共多少字节
	Total    int64
	Data     []byte // 只有图片留着；文本解码完就不要了
	Size     int64  // 读到的字节数
	Text     string // 解码好的文字（二进制为空）
	Enc      Encoding
	Editable bool
	Lossy    bool           // 见 Doc.Lossy
	Broken   bool           // 见 Doc.Broken
	Big      bool           // 超过 MaxEditBytes：只读，没读 HEAD 里的版本
	Buf      *editor.Buffer // fresh 时在后台建好的缓冲区
	Base     *editor.Buffer // HEAD（历史版本：父提交）里的版本，建好的；nil 为没有（或超过 MaxDiffBaseBytes）
	BaseSum  Sum            // HEAD 里那份原始字节的摘要（超过 MaxDiffBaseBytes 没建 Base 也记）
	Gone     string         // 历史版本：读不出来是因为这个提交不在了（GoneCommit）
	Err      error
}

// sizeNote 是 n 字节的文件不打开时的说明，开得了为空：文本最大 MaxViewBytes，图片最大 MaxEditBytes；
// onDisk 是磁盘上的文件（能用默认应用打开），不是的话（历史版本）只说不打开。
func sizeNote(n int64, image, onDisk bool) string {
	switch {
	case image && n > MaxEditBytes:
		return i18n.T("文件超过 8 MB，不打开")
	case n > MaxViewBytes && onDisk:
		return i18n.T("文件超过 64 MB，用默认应用打开")
	case n > MaxViewBytes:
		return i18n.T("文件超过 64 MB，不打开")
	}
	return ""
}

// decode 在后台按 enc（"" 为自动认）解码读到的 data 并拿走它（UTF-8 的文字直接借用这块内存，见 encoding.go 的 decode）；
// 不大的老编码（GB18030、Big5 等）编回去比一遍（Lossy）。fresh 时顺手建好缓冲区，大文件总是建（能编辑的文件变大成了 Big，也换新的缓冲区，不走 Reset）。
func (l *Loaded) decode(data []byte, fresh bool, enc Encoding) {
	l.Size, l.Big = int64(len(data)), len(data) > MaxEditBytes
	r := decode(data, enc, true, !l.Big)
	l.Text, l.Enc, l.Lossy, l.Broken = r.Text, r.Enc, r.Lossy, r.Broken
	l.Editable = r.Editable && !l.Big
	if l.Enc == Binary {
		l.hexify(data) // 二进制按十六进制看
		return
	}
	if fresh || l.Big {
		l.Buf = editor.NewBuffer(l.Text)
	}
}

// readBase 读 HEAD（历史版本：父提交）里的那份：按文件的编码建成对比基准；文件和那份有一个超过 MaxDiffBaseBytes 的只记摘要。
func (l *Loaded) readBase(root, rev, rel string) {
	blob := git.Blob(root, rev, rel)
	if l.BaseSum = SumOf(blob); DiffBaseFits(l.Size, len(blob)) {
		l.Base = BaseBufferAs(blob, l.Enc)
	}
}

// DiffBaseFits 是 size 字节的文件和 base 字节的 HEAD 版本能不能对比：两份都不超过 MaxDiffBaseBytes
// （全删了、全换了的，diff 的代价看两份里大的那份）。
func DiffBaseFits(size int64, base int) bool {
	return size <= MaxDiffBaseBytes && base <= MaxDiffBaseBytes
}

// ReadFile 读磁盘上的文件并解码（过大的不读），仓库里的不大的文本同时读 HEAD 里的版本；fresh 是标签还没有缓冲区
// （或是大文件，重读也换一个新的）：在这里建好，主线程上只换指针。可以在后台调用。
func ReadFile(abs, root, rel string, fresh bool) Loaded { return ReadFileAs(abs, root, rel, fresh, "") }

// ReadFileAs 和 ReadFile 一样，但按 enc 解码（Doc.ForcedEnc；"" 为自动认）。
func ReadFileAs(abs, root, rel string, fresh bool, enc Encoding) Loaded {
	var l Loaded
	l.Info, l.Err = os.Stat(abs)
	if l.Err != nil {
		return l
	}
	l.Image = IsImage(abs)
	if l.Note = sizeNote(l.Info.Size(), l.Image, true); l.Note != "" {
		return l
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		l.Err = err
		return l
	}
	if l.Note = sizeNote(int64(len(data)), l.Image, true); l.Note != "" { // Stat 之后又变大了
		return l
	}
	if l.Image {
		l.Data, l.Size = data, int64(len(data))
		return l
	}
	l.decode(data, fresh, enc)
	if !l.Big && l.Enc != Binary && root != "" {
		l.readBase(root, "HEAD", rel)
	}
	return l
}

// Start 开始一次（重）读，返回这次的编号。
func (d *Doc) Start() int {
	d.Gen++
	d.Loading = d.Buf == nil && d.Bitmap == nil && d.DiffSt == nil && d.ImgDiff == nil
	d.Info = ""
	return d.Gen
}

// ApplyFile 把 ReadFile 的结果放进标签：文本进编辑器（已经开着的话保留撤销之外的状态），
// 图片解码，二进制给一句说明。gen 过期时什么都不做并返回 false。
func (d *Doc) ApplyFile(gen int, l Loaded) bool {
	if d.Gen != gen {
		return false
	}
	d.Loading, d.Conflict = false, false
	switch {
	case l.Err != nil:
		d.Info = i18n.Tf("读取失败：%s", l.Err.Error())
		return true
	case l.Note != "":
		d.Info = l.Note
		return true
	}
	d.Mtime, d.Size, d.Mode = l.Info.ModTime(), l.Info.Size(), l.Info.Mode().Perm()
	switch {
	case l.Hex:
		d.applyHex(l)
	case l.Image:
		bm, err := ui.DecodeBitmap(l.Data)
		if err != nil {
			d.Info = i18n.Tf("图片解码失败：%s", err.Error())
			return true
		}
		d.Bitmap, d.Buf, d.Base, d.Big, d.Hex = bm, nil, nil, false, false
	default:
		d.Hex = false
		d.Bitmap, d.Enc, d.Big, d.Lossy, d.Broken = nil, l.Enc, l.Big, l.Lossy, l.Broken
		d.setText(l)
		d.St.ReadOnly = !l.Editable || d.Snapshot != 0 // 本地历史的那一份只读
	}
	return true
}

// ReadVersion 读某次提交里的文件并解码，不大的同时读它在父提交里的样子（新加的文件没有）；fresh 同 ReadFile。
// 可以在后台调用。
func ReadVersion(root string, ch git.Change, fresh bool) Loaded {
	return ReadVersionAs(root, ch, fresh, "")
}

// ReadVersionAs 和 ReadVersion 一样，但按 enc 解码（"" 为自动认）。
func ReadVersionAs(root string, ch git.Change, fresh bool, enc Encoding) Loaded {
	var l Loaded
	data, err := git.Run(root, "cat-file", "blob", ch.Commit+":"+ch.Path)
	if err != nil {
		l.Err, l.Gone = err, GoneCommit(root, ch.Commit)
		return l
	}
	l.Image = IsImage(ch.Path)
	if l.Note = sizeNote(int64(len(data)), l.Image, false); l.Note != "" {
		return l
	}
	if l.Image { // 历史里的图片也能看
		l.Data, l.Size = data, int64(len(data))
		return l
	}
	l.decode(data, fresh, enc)
	if !l.Big && l.Enc != Binary && ch.Parent != "" && ch.Code != 'A' {
		l.readBase(root, ch.Parent, ch.Path)
	}
	return l
}

// GoneCommit 是 shas 里已经不在仓库里的第一个提交（空的不算），都在、或 root 根本不是仓库时为 ""。
// 读提交里的东西失败之后才调，分清"提交没了"和别的错；可以在后台调用。
func GoneCommit(root string, shas ...string) string {
	if _, err := git.Run(root, "rev-parse", "--git-dir"); err != nil {
		return ""
	}
	for _, sha := range shas {
		if sha == "" {
			continue
		}
		if _, err := git.Run(root, "cat-file", "-e", sha+"^{commit}"); err != nil {
			return sha
		}
	}
	return ""
}

// CommitGoneNote 是提交 sha 不在了时标签上的说明（历史版本、提交里的 Diff，重开会话和 ⇧⌘T 时最常见）。
func CommitGoneNote(sha string) string {
	return i18n.Tf("提交 %s 已经不在了（可能被变基或清理掉了）", git.Short(sha))
}

// ApplyVersion 把 ReadVersion 的结果放进只读的编辑器。
func (d *Doc) ApplyVersion(gen int, l Loaded) bool {
	if d.Gen != gen {
		return false
	}
	d.Loading, d.Gone = false, l.Gone != ""
	switch {
	case d.Gone:
		d.Info = CommitGoneNote(l.Gone)
	case l.Err != nil:
		d.Info = i18n.Tf("读取失败：%s", l.Err.Error())
	case l.Note != "":
		d.Info = l.Note
	case l.Hex:
		d.Size = l.Size
		d.applyHex(l)
	case l.Image:
		bm, err := ui.DecodeBitmap(l.Data)
		if err != nil {
			d.Info = i18n.Tf("图片解码失败：%s", err.Error())
			return true
		}
		d.Size, d.Bitmap, d.Buf, d.Hex = l.Size, bm, nil, false
	default:
		d.Size, d.Enc, d.Big, d.Lossy, d.Broken, d.Hex = l.Size, l.Enc, l.Big, l.Lossy, l.Broken, false
		d.setText(l)
		d.St.ReadOnly = true
	}
	return true
}

// ApplyDiff 把 git.LoadDiffContent 的结果放进 diff 视图；expand 为显示全文。
func (d *Doc) ApplyDiff(gen int, dc git.DiffContent, err error, expand bool) bool {
	if d.Gen != gen {
		return false
	}
	d.Loading = false
	d.Submodule = err == nil && dc.Submodule
	d.ImgDiff = nil
	switch {
	case err != nil:
		d.Info, d.DiffSt = i18n.Tf("读取失败：%s", err.Error()), nil
	case dc.Note != "":
		d.Info, d.DiffSt = dc.Note, nil
	case dc.TooLarge:
		d.Info, d.DiffSt = i18n.T("文件超过 2 MB，不显示 diff"), nil
	case d.Kind == Diff && IsImage(dc.NewName): // 图片并排看两边；读的时候按 MaxEditBytes 放宽（git.LoadDiffContentMax）
		d.Size, d.DiffSt, d.ImgDiff = int64(len(dc.New)), nil, imgSides(dc)
	case dc.Binary:
		d.Info, d.DiffSt = i18n.T("二进制文件，不显示 diff"), nil
	default:
		d.Size = int64(len(dc.New))
		d.Raw = [2][]byte{dc.Old, dc.New}
		if d.DiffSt != nil && sameText(d.DiffSt.Old, dc.Old) && sameText(d.DiffSt.New, dc.New) {
			return true // 没变：保留滚动位置和展开状态
		}
		old := editor.NewBuffer(string(dc.Old))
		if l := editor.LanguageFor(dc.OldName); l != nil {
			old.SetLanguage(l)
		}
		nw := editor.NewBuffer(string(dc.New))
		if l := editor.LanguageFor(dc.NewName); l != nil {
			nw.SetLanguage(l)
		}
		st := &editor.DiffState{Old: old, New: nw, ExpandUnchanged: expand}
		if d.DiffSt != nil {
			st.Layout, st.Font = d.DiffSt.Layout, d.DiffSt.Font
		}
		d.DiffSt, d.ToHunk = st, expand
	}
	return true
}

// setText 把读到的内容放进标签：后台建好了缓冲区的（新标签、大文件重读）直接换上，否则重设原来的；
// 再换上 HEAD 里的版本。大文件重读换了缓冲区，光标留在原来那行（原来在最后一行的跟到新的最后一行）。
func (d *Doc) setText(l Loaded) {
	swap := l.Buf != nil && d.Buf != nil
	var at editor.Pos
	var y float64
	if swap {
		at, y = d.St.CursorPos(), d.St.ScrollY()
		if at.Line >= d.Buf.LineCount()-1 {
			at = editor.Pos{Line: l.Buf.LineCount() - 1}
		}
	}
	fresh := d.Buf == nil || l.Buf != nil
	switch {
	case l.Buf != nil:
		d.Buf = l.Buf
	case d.Buf == nil:
		d.Buf = editor.NewBuffer(l.Text)
	default:
		d.Buf.Reset(l.Text)
	}
	if fresh {
		if lang := d.language(); lang != nil {
			d.Buf.SetLanguage(lang)
		}
	}
	d.savedEOL = "" // 重读了：换行是文件里的
	d.St.Buffer, d.St.Label = d.Buf, i18n.Tf("编辑 %s", d.Title)
	d.SetBase(l.Base, l.BaseSum)
	if swap {
		d.St.GoTo(at)
		d.St.SetScrollY(y)
	}
	if d.Pending != nil {
		d.JumpTo(*d.Pending)
	}
}

// SetBase 换掉槽里的对比基准（BaseBuffer 建好的 HEAD 版本）和它的摘要；nil 为没有（新文件、仓库外、大文件），
// 有摘要没有基准的是超过 MaxDiffBaseBytes 没建（NoBase）。
func (d *Doc) SetBase(base *editor.Buffer, sum Sum) {
	d.Base, d.BaseSum = base, sum
	d.NoBase = base == nil && sum.OK
	d.St.DiffBase = base
}

// language 是按标题认的语言（手动选了的用选的）；大文件的高亮在后台算（highlight.Wrap），别的照 mygo 的在主线程上每帧追一点。
func (d *Doc) language() *editor.Language {
	if d.Hex {
		return nil // dump 不高亮
	}
	var l *editor.Language
	switch d.Lang {
	case "":
		l = editor.LanguageFor(d.Title)
	case PlainLang:
		return nil
	default:
		l = editor.LanguageNamed(d.Lang)
	}
	if d.Big {
		return highlight.Wrap(l)
	}
	return l
}

// BaseBuffer 把 HEAD 里的版本（git.Blob 读到的原始字节，拿走它）和文件一样解码、建成缓冲区；nil 为没有。可以在后台调用。
func BaseBuffer(data []byte) *editor.Buffer { return BaseBufferAs(data, "") }

// BaseBufferAs 和 BaseBuffer 一样，但先按文件的编码 enc 解（解得通才用，不然自动认）：短的 HEAD 版本自己认可能认成别的，
// 手动换的编码也跟着换。
func BaseBufferAs(data []byte, enc Encoding) *editor.Buffer {
	if data == nil {
		return nil
	}
	r := Decoded{Enc: Binary}
	if enc != "" && enc != Unknown && enc != Binary {
		r = decode(data, enc, true, false)
	}
	if r.Broken || r.Enc == Binary {
		r = decode(data, "", true, false)
	}
	if r.Enc == Binary {
		r.Text = string(data)
	}
	return editor.NewBuffer(r.Text)
}

// Sum 是一份原始字节的摘要（长度 + SHA-256），零值是"没有这份"（新文件、仓库外、大文件不读）。
type Sum struct {
	OK   bool
	Len  int
	Hash [sha256.Size]byte
}

// SumOf 是 data 的摘要；nil 为零值。可以在后台调用。
func SumOf(data []byte) Sum {
	if data == nil {
		return Sum{}
	}
	return Sum{OK: true, Len: len(data), Hash: sha256.Sum256(data)}
}

func sameText(b *editor.Buffer, data []byte) bool {
	if b == nil || data == nil {
		return b == nil && data == nil
	}
	text := decodeBase(data)
	return b.Len() == len(text) && b.String() == text
}

// Stat 是磁盘上文件现在的修改时间和大小。可以在后台调用。
func Stat(abs string) (time.Time, int64, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return time.Time{}, 0, err
	}
	return fi.ModTime(), fi.Size(), nil
}

// ChangedOnDisk 是磁盘上的文件和上次读 / 写时不一样了。
func (d *Doc) ChangedOnDisk(mtime time.Time, size int64) bool {
	return !mtime.Equal(d.Mtime) || size != d.Size
}

// NeedsOverwrite 是保存前磁盘上的文件已经被别处改过（还没在冲突条里选"保留我的"）。
func (d *Doc) NeedsOverwrite() bool {
	if d.Conflict || d.Mtime.IsZero() {
		return false
	}
	mt, _, err := Stat(d.Abs)
	return err == nil && !mt.Equal(d.Mtime)
}

// Write 把缓冲区写回磁盘（保留原来的权限），成功后算已保存。
func (d *Doc) Write() error {
	if !d.Editable() || d.Kind != File {
		return errors.New(i18n.T("这个标签不能保存"))
	}
	return d.write(d.Enc)
}

// ConvertUTF8 把不是 UTF-8 的文件按 UTF-8 重写（只读条和编码菜单里的"转成 UTF-8…"，问过之后），之后可编辑。
func (d *Doc) ConvertUTF8() error {
	if !d.Convertible() {
		return errors.New(i18n.T("这个标签不能保存"))
	}
	if err := d.write(UTF8); err != nil {
		return err
	}
	d.Enc, d.ForcedEnc, d.Lossy, d.St.ReadOnly = UTF8, "", false, false
	return nil
}

// write 把缓冲区按 enc 编码（换行照原来的）写回 d.Abs，成功后算已保存。
func (d *Doc) write(enc Encoding) error {
	var b bytes.Buffer
	if _, err := d.Buf.WriteTo(&b); err != nil {
		return err
	}
	data := Encode(b.String(), enc)
	if data == nil {
		if n := Unencodable(b.String(), enc); n > 0 {
			return &UnencodableError{Enc: enc, N: n}
		}
		return fmt.Errorf(i18n.T("不能按 %s 保存"), enc.Label())
	}
	mode := d.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(d.Abs, data, mode); err != nil {
		return err
	}
	d.markSaved()
	d.Conflict, d.savedEOL = false, ""
	if mt, size, err := Stat(d.Abs); err == nil {
		d.Mtime, d.Size = mt, size
	}
	return nil
}

// UnencodableError 是保存时文字里有 N 个字按 Enc 写不进去（老编码，如 Shift_JIS 里的 emoji）：WriteDoc 问要不要转成 UTF-8 保存。
type UnencodableError struct {
	Enc Encoding
	N   int
}

func (e *UnencodableError) Error() string {
	return i18n.Tn("有 %d 个字 %s 写不了", e.N, e.Enc.Label())
}

// KeepMine 是冲突时选"保留我的"：下次保存直接覆盖，不再问。
func (d *Doc) KeepMine() {
	if mt, size, err := Stat(d.Abs); err == nil {
		d.Mtime, d.Size = mt, size
	}
	d.Conflict = false
}

// IsImage 按扩展名判断是不是能显示的图片。
func IsImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// HumanSize 是 1.5 KB 这样的大小。
func HumanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
