// Package app 是应用状态机：工作区、当前仓库、三个区域的开关、命令注册表（菜单栏、
// 窗口快捷键、工具提示里的快捷键都从它生成）、持久化和后台任务。
//
// 界面（internal/ui）只读这里的状态、通过这里的方法和命令改状态；后台 goroutine
// 只经 App.Update 回主线程。读仓库状态都是只读的 git 命令；会改仓库的操作和保存文件
// 只在用户明确点了按钮、菜单或按了快捷键后才做。
package app

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/globs"
	"github.com/lemoeade/grove/internal/history"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/render"
	"github.com/lemoeade/grove/internal/snippets"
	"github.com/lemoeade/grove/internal/tasks"
	"github.com/lemoeade/grove/internal/watch"
	"github.com/lemoeade/grove/internal/workspace"
)

// StaleAfter 之后再切到一个没在监听的仓库，就顺手在后台重读一次它的状态。
const StaleAfter = 5 * time.Second

// Repo 是工作区里的一个仓库：它的 git 状态、历史、文件树和它的终端。
type Repo struct {
	Name string
	Root string

	Status   git.Status
	Branches []git.Branch
	Tags     []git.Tag // 标签，新的在前，最多 git.MaxTags 个（和分支一起读）
	Err      error
	Loading  bool
	Loaded   bool
	LoadedAt time.Time
	Plain    bool // 不在 Git 仓库里的普通文件夹：没有 git 状态、历史和提交，文件树直接列目录。读状态时判断，不保存

	GitFold      int             // Git 栏里这个仓库段的折叠：FoldAuto / FoldOpen / FoldClosed
	GroupToggled map[string]bool // Git 栏里这个仓库段点过、和默认（贮藏收着，别的展开）反过来的分组（GroupKey）
	CommitOpen   bool            // 用户点开了段底的"提交…"
	FocusCommit  bool            // 下一帧把焦点给这个仓库的提交信息框

	Hist     History
	Files    files.Tree
	Expanded bool // 侧栏文件树里这个仓库展开着

	Tasks     []tasks.Task // 仓库根的 package.json / Makefile / justfile / Cargo.toml / go.mod 认出来的任务（读状态时后台扫，那几个文件变了才重扫）
	taskStamp string       // Tasks 是按那几个文件的哪个样子扫的（tasks.Stamp）

	// Git 工作树（worktrees.go）
	Worktrees  []Worktree          // 这个仓库的别的工作树（不含自己）：主的在最前，别的按提交时间倒序
	Linked     bool                // 自己是链接的工作树（git worktree add 出来的）
	MainRoot   string              // 链接的工作树：主工作树的目录（主仓库是 bare 时为空）
	WtOpen     bool                // Git 栏里"工作树"块展开着（记在会话的 wtOpen）
	set        *workspace.Settings // 所在窗口的设置（App.newRepo 给的 &a.St.Settings）：ShowWts / ShowStashes 读它；nil 当默认
	wtAuto     bool                // 有工作树在等你、在跑或可合并：组收着也接着算状态、盯着目录（SyncWtAuto 每帧对）
	WtInfo     map[string]*WtInfo  // 工作树路径 → 懒算的改动数、领先 / 落后；nil 为还没算（块展开时算，重读状态时重算）
	WtLoading  bool                // 正在算 WtInfo
	WtErr      error               // 上一次读工作树列表失败的原因
	wtGen      int                 // 每次算 WtInfo 递增，旧的结果作废
	real       string              // Root 解开符号链接（认"已打开"用；第一次读完状态才有）
	nested     []string            // 套在 Root 下面的别的工作树（仓库内路径），文件树不显示
	Excluded   int                 // 按 files.exclude 藏掉的文件和目录数（整棵读时数的，exclude.go）
	exclude    globs.Set           // 文件树最近一次整棵读时按的 files.exclude
	excludeKey string              // exclude 是哪些模式（比较变了没有）
	emptyRead  map[string]int      // 大仓库里读过一层找空目录的目录 → 那次文件列表的 Gen（emptylazy.go）
	emptyAt    [2]int              // lazyEmpty 上次看的时候 Files 的 Gen、Version

	// Git 栏里展开看文件的贮藏（stash.go，不保存）：一个仓库同时只展开一条
	StashOpen     string       // 展开着的那条（Stash.Hash），空为都收着
	StashFiles    []git.Change // 它改了的文件（StashFilesFor 那条的）
	StashFilesFor string       // StashFiles 是哪条贮藏的；和 StashOpen 不一样是还在读
	StashLoading  bool         // 正在读 StashOpen 的文件

	DirOpen map[string]bool // Git 栏里展开着的未跟踪目录（Change.Path，untracked.go），不保存：换了工作区仓库是新的，自然清掉

	CommitMsg string
	Busy      string    // 正在跑的 git 写命令的名字，如 "推送"；空为没有（排着的在 Queue）
	Queue     []*Op     // 排着队、还没跑的 git 写命令（queue.go）
	running   *Op       // 正在跑的那条
	Extra     git.Extra // 状态以外的几样：进行到一半的合并、HEAD、没推送的提交
	Amend     bool      // 提交框里勾着"修改上一次提交"
	amendKeep string    // 勾上之前框里写着的（取消勾选时放回去）
	amendMsg  string    // 勾上时填进去的上一次提交的信息
	msgPos    int       // 提交框里 ↑ 翻到提交信息历史的第几条（从 1 起），0 为没在翻

	rebase *docs.RebasePlan // 跑着 / 停着的交互式变基（rebase.go）：向导关了也跟着它，变基结束时删临时文件

	watcher  *watch.Watcher
	watching bool                  // 监听正在启动或在跑
	wtWatch  map[string]*wtWatcher // "工作树"块展开时链接工作树目录的监听（wtwatch.go），键是 Worktree.Path
	WatchErr error                 // 监听停了的原因（这时切回窗口会重读，⌘R 也行）
	LastWhy  string                // 最近一次重读的原因（RefreshRepo 的 why）
	again    string                // 正在读的时候又要重读：读完以这个原因再读一遍
	hints    fileHints             // 下一次重读时文件树要看的地方（增量刷新，treediff.go）

	Terms      []*TermTab       // 底栏里这个仓库的终端标签（第一次要显示时才建，见 terminals.go）
	EdTerms    []*TermTab       // 开在编辑器区的终端标签：各是一个 docs.Term 标签，底栏的标签条、TermSel 不算它们
	TermSel    int              // 当前的终端标签
	TermStrip  ui.ScrollState   // 底栏终端标签条的横向滚动（界面按它画底边的细滚动条）
	TermShown  int              // 上次滚到看得见的终端标签的 Num：换了当前标签才再滚一次（记编号不记指针，关掉的标签连同回看不被留着）
	termNum    int              // 开过几个终端标签（默认名"终端 n"用）
	termsInit  bool             // Terms 建过了
	savedTerms *workspace.Terms // 会话里记着的终端标签，initTerms 用

	ctx  context.Context // 这个仓库所有 git 命令的 ctx：移除仓库、切工作区、关窗口时取消（close）
	stop context.CancelFunc
}

// NewRepo 是一个还没读状态的仓库。
func NewRepo(root string) *Repo {
	r := &Repo{Name: filepath.Base(root), Root: root, Hist: History{Sel: -1, PrevSel: -1}}
	r.ctx, r.stop = context.WithCancel(context.Background())
	return r
}

// newRepo 是这个窗口里一个还没读状态的仓库：NewRepo，再接上窗口的设置（ShowWts / ShowStashes 读它）。
func (a *App) newRepo(root string) *Repo {
	r := NewRepo(root)
	r.set = &a.St.Settings
	return r
}

// ShowWts 是设置里开了"在 Git 栏显示工作树"：关着时没有工作树组、"需要你"条和段头的工作树数，
// 也不算各工作树的状态、不盯它们的目录（WtShown、WtTracked 都是假）。
func (r *Repo) ShowWts() bool { return r.set != nil && r.set.ShowWorktrees }

// ShowStashes 是设置里开了"在 Git 栏显示贮藏"：关着时没有贮藏组和段头的贮藏数（贮藏照常读、照常能贮藏 / 弹出）。
func (r *Repo) ShowStashes() bool { return r.set != nil && r.set.ShowStashes }

// ConfirmReq 是等用户确认的一步会改仓库或丢数据的操作。
type ConfirmReq struct {
	Title, Message, Button string
	Danger                 bool // 删掉、丢掉、改写别人看得到的东西：红的警告图标和红按钮（AskDanger）
	Fn                     func()
}

// BranchDialog 是"新建分支"对话框的状态。
type BranchDialog struct {
	Open bool
	Name string
	From string // 新分支的起点（空 = 当前 HEAD；历史里"从这里新建分支"会填短哈希）
	Repo *Repo  // 建在哪个仓库（历史里从某个提交新建时是那个提交的仓库）；nil = 当前仓库
}

// 底栏的标签。
const (
	BottomTerminal = iota
	BottomOutput
	BottomProblems // 检查报的问题（lint.go）
)

// App 是整个应用的状态。
type App struct {
	// 后台 goroutine、计时器、终端回调别读 a.Update / a.Win：换工作区时 switchTo 会整个重写 *a。
	// 在主线程上先取出来（up := a.Update）再带进去。
	Win     *mygo.Window
	Update  func(fn func()) // 在主线程上跑 fn 并重绘；窗口建好后是 Win.Update，测试里由测试驱动
	HasMenu bool            // 有菜单栏（测试里没有）
	Windows *Windows        // 开着的所有窗口（windows.go）；测试里多半没有，只有这一个
	Slot    int             // 窗口的槽位（1 起，见 StateKey）；没有注册表时是 0
	LastBar ui.TitleBar     // 上一帧的 c.TitleBar()（界面记日志用，按窗口记）

	// Mermaid / 公式的渲染器（render.go，跟着窗口走）；RenderOpen 是建它后端的，测试里换成假的（没有就用 Win 建 WebView）
	Rend       *render.Renderer
	RenderOpen func() (render.Backend, error)

	WsPath   string
	StateDir string   // state.json 在这里；空则不保存
	WsRoots  []string // 工作区文件里的仓库
	Repos    []*Repo
	Selected int
	St       workspace.State // 随 state.json 保存的布局和设置

	// 主区的标签：有键盘的那组的（别的组的在组里，见 groups.go）
	Docs   []*docs.Doc
	DocSel int     // 当前标签，-1 为没有
	Ed     *EdNode // 编辑器组的拆分树（Editors 取，没分屏时是一个叶子）
	Grp    *Group  // 有键盘的那组
	closed []closedDoc
	nav    []navPoint // 看过的标签和位置，‹ › 在里面走
	navPos int
	tabMRU []docs.Key // 切到过的标签，最近的在前（⌃Tab 按它走，tabswitch.go）

	olCache   olCache       // 快速打开"名字@符号"读的没开着的文件的大纲（palettesym.go）
	syms      symIndex      // 工作区符号的索引（"#"，symindex.go）
	snips     *snippets.Set // 读好的代码片段（snippets.go）：第一次用时读
	snipCheck time.Time     // 上次看片段目录变了没有
	taskVer   int           // 哪个仓库的 Tasks 重扫了就加一（面板 "!" 跟着重算）

	// 列表
	ChangeList  ui.ListState // Git 栏：所有仓库的变更，一个仓库一段
	GitSel      int          // Git 栏里选中的行，-1 为无
	gitPrev     int          // 上一次选中的行，用来判断键盘是往上还是往下走
	gitWt       string       // Git 栏的选中停在这个工作树上（路径），空为没有
	gitWtDoc    *docs.Doc    // 停上去时的当前标签：换了标签选中就跟着标签走
	gitStash    string       // Git 栏的选中停在这条贮藏上（Stash.Hash），空为没有；和 gitWt 一样跟着 gitStashDoc
	gitStashDoc *docs.Doc
	gitDir      ChangeKey // Git 栏的选中停在这个未跟踪目录上（它那行的 Key），空为没有；和 gitStash 一样跟着 gitDirDoc
	gitDirDoc   *docs.Doc
	GitHover    int                     // 这一帧指针所在的行，用来区分点击和键盘移动
	WtExp       *WtExpand               // Git 栏里展开着的工作树（wtstate.go），nil 为没有
	GitChosen   ui.Selection[ChangeKey] // Git 栏里选中的行（⌘点、⇧点、⌘A 多选），见 ChosenChanges；单选时就是 GitSel 那行
	Peek        Peek                    // 编辑器里就地看的那块改动（peek.go），一次只开一张
	gitMulti    bool                    // 选着好几行（点击不预览），只要当前标签还是 gitMultiDoc 就留着，见 syncChangeSel
	gitMultiDoc *docs.Doc
	gitAfter    gitAfter // 暂存 / 取消暂存 / 丢弃了选中的行之后，那行没了时选中落到哪（gitchosen.go）
	HistList    ui.ListState
	AllHist     ui.ListState // 右栏"所有仓库"视图
	HistAllSel  int          // "所有仓库"视图里选中的行，-1 为无
	FileList    ui.ListState
	OutList     ui.ListState // 底栏的输出，跟着最新一条

	// 右栏的本地历史路径模式（localhist.go）：仓库外、普通文件夹里、未跟踪的文件"查看本地历史"时，窗口级，不按仓库
	LocalHistPath string
	LocalRows     []history.Entry // LocalHistPath 的条目（旧的在前，界面倒着列）
	LocalList     ui.ListState
	LocalSel      int        // 选中的行（倒序后的下标），-1 为无
	localUsage    localUsage // 设置页那行的占用（后台算）

	// 侧栏文件树
	Filter       string
	FileSel      int                   // 文件树的光标行（键盘从它走、新建放在它的目录），-1 为无
	FileChosen   ui.Selection[FileKey] // 文件树里选中的行（⌘点、⇧点、⌘A 多选），见 ChosenRows
	FileHover    int                   // 这一帧指针所在的行，用来区分点击和键盘移动
	treeClick    treeClick             // 文件树里选中的（可拖的）行自己接到的点击，列表建好之后补做（ApplyTreeClick）
	ignoredOn    bool                  // 文件树最近一次读的时候开没开"显示被忽略的文件"（设置改了对不上就全部重读）
	ready        *readyLog             // 启动计时还在等的几步（ready.go），打过就是 nil
	excludeOn    string                // 文件树最近按的设置里的"隐藏的文件"（exclude.go）
	CompareSel   *CompareItem          // 文件树右键"选择以进行比较"记下的文件（filecompare.go）
	FileUndo     []FileOp              // 文件树能撤销的操作，最新的在后（fileundo.go）
	excludeTimer *time.Timer           // 设置里的"隐藏的文件"停手之后重读
	wsExclude    []string              // 工作区文件 settings 的 files.exclude（exclude.go）
	wsMod        time.Time             // 读 wsExclude 时工作区文件的修改时间
	tree         treeCache
	treeSel      *int // 应用自己选的行，界面画完列表后再设一次（见 setTreeSel）
	revealLater  *revealReq
	follow       follow                        // 文件树跟随当前文件（FollowCurrent）
	TreeDragging bool                          // 上一帧文件树里有行在拖（界面写，FollowCurrent 读：拖的时候树不跟着动）
	TreeDropCopy bool                          // 这一帧拖着停在一个松手会复制的目标上（别的仓库来的、按着 ⌥）（界面写，EndTreeDrop 挪进 TreeCopying）
	TreeCopying  bool                          // 上一帧的 TreeDropCopy：拖着的行画复制光标
	TreeDropNone bool                          // 这一帧拖着停在一个不接的地方（只有目录拖到编辑器上）（界面写，EndTreeDrop 挪进 TreeNoDrop）
	TreeNoDrop   bool                          // 上一帧的 TreeDropNone：拖着的行画不可放的光标
	dwell        treeDwell                     // 拖着停在哪个收起的目录行 / 仓库行上（DwellTree）
	TreeSpans    map[int]ui.Rect               // 这一帧建了的行上一帧画在哪（窗口坐标），按这一帧的行号：吸顶栈看哪行在顶上、谁被顶出去、光标那行被盖住没有（界面每帧清空重写）
	SideScrolls  map[*ui.ListState]*SideScroll // 侧栏列表（文件树、Git 栏、搜索结果）的滚轮状态（sideList）
	TreeRootH    float32                       // 上一帧仓库行的高
	TreeRowH     float32                       // 上一帧目录 / 文件行的高
	TreeClip     []string                      // 文件树里 ⌘C 复制的文件 / 目录（绝对路径），⌘V 粘贴它们；应用里记着，不读系统剪贴板
	rowTips      map[FileKey]rowTip            // 文件树行的悬停提示，仓库刷新之后重算（RowTip）
	TreeTip      TreeTip                       // 文件树行的悬停提示停了多久（treetip.go；界面写）
	Menu         PopMenu                       // Grove 自己画的菜单（文件树和 Git 栏的右键、分支菜单，popmenu.go；界面喂事件）
	MenuShown    int                           // 菜单开着画了几帧（界面用：画过两帧后接收层没了焦点就是窗口失去了焦点）
	MenuByKey    bool                          // 菜单是 ⇧F10 / 菜单键开的：没选就关了时键盘回到文件树
	dirInfos     map[FileKey]*dirInfo          // 目录行悬停提示的大小、最近改动（dirtip.go）
	Edit         *TreeEdit                     // 文件树里正在输入的名字（新建 / 重命名），nil 为没有
	TreeFocused  bool                          // 这一帧文件树有焦点（界面每帧写；⌘N 据此决定建在哪）
	FocusTree    bool                          // 下一帧把焦点还给文件树（输入框收起之后）

	Trash    func(path string) error // 移到废纸篓；默认 mygo.Shell.TrashItem，测试里换掉
	InFinder func(path string)       // 在访达里显示；默认 mygo.Shell.ShowItemInFolder，测试里换掉
	OpenURL  func(url string) error  // 用系统浏览器 / 邮件开网址；默认 mygo.Shell.OpenExternal，测试里换掉

	OpenDialog func(mygo.OpenDialogOptions) ([]string, error) // 系统的打开面板；默认 mygo.Dialog.Open，测试里换掉
	SaveDialog func(mygo.SaveDialogOptions) (string, error)   // 系统的存储面板（导出设置）；默认 mygo.Dialog.Save，测试里换掉

	OnSettings    func(s *workspace.Settings) // 外观和语言交给窗口（main 里给；测试里没有）
	wtShowOn      bool                        // 上次套上的"在 Git 栏显示工作树"（syncShownGroups 比它认出开关变了）
	stashShowOn   bool                        // 上次套上的"在 Git 栏显示贮藏"
	OpenSettings  func()                      // 打开设置窗口（⌘,；main 里给）
	OpenAbout     func()                      // 打开关于窗口（main 里给）
	SettingsWin   *mygo.Window                // 开着的设置窗口（main 里给，导出 / 导入的面板挂在它上面）；没开、测试里为 nil
	ShowPath      func(path string) error     // 用访达 / 默认程序打开目录或文件（关于窗口的日志目录、许可）；默认 mygo.Shell.OpenPath，测试里换掉
	OnRecent      func(path string)           // 记进系统的"最近使用"（main 里给；用另一份 state.json 时和测试里没有）
	OnClearRecent func()                      // 清掉系统的"最近使用"（同上）
	LogDir        string                      // 崩溃和错误日志的目录（main 里给；测试里空）
	LastCrash     string                      // 上次崩溃留下的日志（main 里给），没有为空
	autoVer       map[*docs.Doc]autoSave      // "停手后自动保存"：每个改过的文件最后一次改动

	// 侧栏搜索页；快速打开和命令面板
	Search  SearchState
	Palette PaletteState

	// 右栏历史的搜索框（不记；所有分支的开关在 St 里，文件历史在各仓库的 Hist 里）
	HistQuery string
	HistAgent bool      // 右栏历史只看 agent 写的提交（头部的四角星开关），见 ToggleHistAgent
	HistFind  bool      // 右栏历史头部的搜索框展开着（点放大镜开，Esc 收起）
	HistField ui.Handle // 右栏历史的搜索框（展开时把焦点给它）
	HistFocus bool      // 下一帧把焦点给右栏历史的搜索框（刚点了放大镜）
	histTimer *time.Timer
	histTick  int

	FocusDoc  bool     // 下一帧把焦点给当前标签的编辑器（从面板打开文件、跳行之后）
	FocusTerm bool     // 下一帧把焦点给当前仓库当前终端标签里有键盘的那一格（新建、分屏之后，量内存时的 WORKBENCH_FOCUS=terminal）
	FocusTab  *TermTab // FocusTerm 给的是哪个终端标签（编辑器区的、或指名的底栏标签）；nil 为底栏当前的

	BottomMax bool // 底栏最大化着（bottommax.go，不持久；底栏关着时不算，看 BottomMaxed）

	// 禅模式（zen.go）
	Zen    bool
	zenWas [3]bool   // 进禅模式时侧栏、右栏、底栏的开关
	zenEsc time.Time // 禅模式里上一下没人接的 Esc

	// 底栏的终端（terminals.go）
	TermFocused bool                       // 这一帧键盘在终端里（底栏或编辑器区；View 的根上清，终端画出来时写）；终端的快捷键看它
	agents      *agentPoller               // 后台看终端前台的 agent（agents.go）
	clock       func() time.Time           // 测试换的钟；nil 为 time.Now
	fgJobs      func([]int) map[int]string // 测试换的前台命令；nil 为 ps
	edTab       *TermTab                   // 键盘最近在的编辑器区终端标签（edRepo 的）；在底栏的格里为 nil。TermFocused 时 ActivePane 是它
	edRepo      *Repo
	Rename      TermRename // 正在改名的终端标签
	paneSeq     int
	pidWait     []*TermPane // 启动了、还没认领到 shell 进程号的格，按启动先后
	pidPolling  bool
	home        *Repo // 没有仓库时（空工作区）终端所在的家目录，不在 Repos 里，见 TermRepo

	// 对话框和提示
	Confirm     *ConfirmReq
	ConfirmOpen bool
	AlertOpen   bool
	AlertErr    bool // 出错（Fail）：对话框头是红的 ✕，否则是提示
	AlertTitle  string
	AlertMsg    string
	AlertDetail string    // git.Explain 认出来时 git 原话的最后几行（等宽小字，在说明下面），否则空
	CloseReq    *docs.Doc // 等用户决定存不存的要关的标签
	CloseOpen   bool
	Leave       *Leave // 退出或换工作区之前在问的（workspaces.go），nil 为没有
	ForceClose  bool   // 问完了要退出，这次关窗口不再拦
	Branch      BranchDialog
	WtDialog    WorktreeDialog // 新建 / 锁定 / 移除工作树（worktrees.go）
	RefDlg      RefDialog      // 分支改名、新建 / 删除标签、远程改名 / 添加（gitrefs.go）
	Toast       string         // 下一帧要显示的 toast
	Batch       *Batch         // 正在跑的全部抓取 / 全部拉取，nil 为没有
	Cred        []*CredReq     // 等用户回答的凭据提示，框一次一个（askpass.go）
	credCache   map[string]credPass
	Clones      []*Clone    // 正在跑的克隆（clone.go），侧栏顶上一个一行
	CloneDlg    CloneDialog // "克隆仓库"对话框

	Output    []LogEntry
	BottomTab int
	Probs     Problems     // 检查报的问题（lint.go）：所有仓库一起，换工作区时不带过去
	ProbList  ui.ListState // 底栏的问题列表
	ProbSel   int          // 问题列表里选中的行，-1 为无

	LastRefresh time.Time
	watching    bool      // StartWatching 之后、Close 之前：新加的仓库也开监听
	RemoveReq   *Repo     // 右键选了"从列表移除"，这一帧列表画完后再处理
	MoveReq     *RepoMove // 拖仓库行松手、菜单的上移 / 下移，这一帧列表画完后再挪
	RevealReq   *docs.Doc // 标签菜单选了"在文件树里显示"，这一帧画完再做

	menuSig  string
	ownStore *workspace.Store // 没有注册表时自己的 Store（store）
}

// New 读取自己的 state.json 和工作区（.code-workspace 或目录，见 workspaceRoots），建好仓库列表；
// wsPath 为空时开最近的工作区，没有（第一次用）或都打不开是空工作区。窗口、终端和状态读取由调用方启动。
// stateDir 为空就不保存。
func New(wsPath, stateDir string) (*App, error) {
	st := workspace.Default()
	if stateDir != "" {
		var err error
		if st, err = workspace.LoadState(stateDir); err != nil {
			log.Printf("读 %s: %v", workspace.StateFile, err)
		}
	}
	var roots []string
	failed := false
	if wsPath == "" {
		wsPath, roots, failed = startWorkspace(&st)
	} else {
		wsPath = absPath(wsPath)
		var err error
		if roots, err = workspaceRoots(wsPath); err != nil {
			return nil, err
		}
	}
	a := newApp(wsPath, roots, st)
	a.StateDir = stateDir
	a.defaults()
	if failed {
		a.Toast = i18n.T("最近的工作区都打不开了")
	}
	return a, nil
}

// defaults 是窗口建好之前的 Update（直接跑）和系统的废纸篓、浏览器、访达、打开 / 存储面板。
func (a *App) defaults() {
	a.Update = func(fn func()) { fn() }
	a.Trash, a.OpenURL, a.ShowPath, a.InFinder = mygo.Shell.TrashItem, mygo.Shell.OpenExternal, mygo.Shell.OpenPath, mygo.Shell.ShowItemInFolder
	a.OpenDialog, a.SaveDialog = mygo.Dialog.Open, mygo.Dialog.Save
}

// newApp 是工作区 wsPath（仓库 roots）刚打开的样子：仓库列表、上次的当前仓库和展开状态、布局。
// 加进来和移除的仓库按工作区记在会话里；会话里没有的（老版本的 state.json）用 st 顶层的。
// wsPath 为空是空工作区：没有仓库，也没有会话。
func newApp(wsPath string, roots []string, st workspace.State) *App {
	a := &App{WsPath: wsPath, WsRoots: roots, St: st, DocSel: -1, FileSel: -1, FileHover: -1, GitSel: -1, gitPrev: -1, GitHover: -1, HistAllSel: -1, LocalSel: -1, ProbSel: -1}
	a.OutList.FollowEnd = true
	a.Search.Sel, a.Search.Hover, a.Search.EditorLines = -1, -1, 2
	a.Editors()
	a.excludeOn = st.Settings.Exclude
	a.wtShowOn, a.stashShowOn = st.Settings.ShowWorktrees, st.Settings.ShowStashes
	if wsPath == "" {
		return a
	}
	a.loadWsSettings()
	if s := a.St.Sessions[a.sessionKey()]; s != nil && (s.Added != nil || s.Hidden != nil) {
		a.St.Added, a.St.Hidden = slices.Clone(s.Added), slices.Clone(s.Hidden)
	}
	if s := a.St.Sessions[a.sessionKey()]; s != nil && s.Panels != nil {
		a.St.SetPanels(s.Panels) // 这个工作区自己的布局；没记过的沿用 st 顶层的（最近一次的）
	}
	for _, root := range roots {
		if !slices.Contains(a.St.Hidden, root) {
			a.Repos = append(a.Repos, a.newRepo(root))
		}
	}
	for _, root := range a.St.Added {
		if a.RepoFor(root) == nil {
			a.Repos = append(a.Repos, a.newRepo(root))
		}
	}
	if s := a.St.Sessions[a.sessionKey()]; s != nil {
		a.restoreTree(s)
	} else if r := a.Current(); r != nil {
		r.Expanded = true
	}
	return a
}

// Start 重新打开上次的标签并在后台读状态；窗口建好之后调。终端第一次要显示时才启动（terminals.go）。
func (a *App) Start() {
	a.startKeybindings()
	a.startReadyLog()
	a.restoreTabs()
	a.RefreshAll(WhyOpen)
	a.StartWatching()
	for _, r := range a.Repos {
		if r.Expanded {
			a.EnsureFiles(r)
		}
	}
	a.noteWs() // 会存一次：标签放回去之后再存，不然会话里的标签就丢了
	a.startLocalHistory()
}

// Close 关掉所有终端和文件监听、取消还在跑的 git 命令、停掉还没搜完的搜索（窗口关了之后）。
func (a *App) Close() {
	a.stopAgents()
	a.stopWatching()
	a.Search.stop()
	if a.Search.timer != nil {
		a.Search.timer.Stop()
	}
	for _, r := range a.Repos {
		r.close()
	}
	if a.home != nil {
		a.home.close()
	}
	for _, cl := range a.Clones {
		cl.cancel()
	}
}

// WsName 是工作区的名字：工作区文件去掉 .code-workspace，目录就是目录名。
func (a *App) WsName() string { return wsName(a.WsPath) }

// SaveState 把布局、设置、几张最近的表和这个工作区现在的会话（标签、展开状态、仓库增删）写进 state.json。
// 几个窗口共用一个 Store：别的工作区的会话留着，开着的窗口记进 windows；最近的工作区 / 文件、提交信息历史、设置
// 存完抄给别的窗口（syncShared），所以存的这个窗口手里的就是最新的。布局（三栏开关和大小、diff 视图……）
// 也记进会话（按工作区），顶层的照写，是最近存的那个窗口的。
func (a *App) SaveState() {
	if a.StateDir == "" {
		return
	}
	changed, err := a.store().Save(func(st *workspace.State) {
		n := a.St.Clone()
		if a.Zen { // 禅模式不持久：记的是进之前的三个面板
			n.Sidebar, n.RightBar, n.BottomBar = a.zenWas[0], a.zenWas[1], a.zenWas[2]
		}
		n.Sessions, n.Windows = st.Sessions, st.Windows
		if a.WsPath != "" {
			if n.Sessions == nil {
				n.Sessions = map[string]*workspace.Session{}
			}
			n.Sessions[a.sessionKey()] = a.session()
			n.Added, n.Hidden = nil, nil // 记在会话里了（空工作区没有会话，老版本顶层的留着）
		}
		if a.Windows != nil {
			n.Windows = a.Windows.list(n.RecentWs)
		}
		*st = n
	})
	if err != nil {
		log.Printf("写 %s: %v", workspace.StateFile, err)
	}
	if changed && a.Windows != nil {
		a.Windows.syncShared(a)
	}
}

// store 是存 state.json 的 Store：几个窗口共用注册表的；没有注册表（测试）时是自己的，第一次存时从 a.St 建。
func (a *App) store() *workspace.Store {
	if a.Windows != nil {
		return a.Windows.store
	}
	if a.ownStore == nil || a.ownStore.Dir() != a.StateDir {
		a.ownStore = workspace.NewStore(a.StateDir, a.St)
	}
	return a.ownStore
}

// Current 是当前仓库；列表空的时候是 nil。
func (a *App) Current() *Repo {
	if len(a.Repos) == 0 {
		return nil
	}
	if a.Selected < 0 || a.Selected >= len(a.Repos) {
		return a.Repos[0]
	}
	return a.Repos[a.Selected]
}

// RepoFor 是根目录为 root 的仓库，没有为 nil。
func (a *App) RepoFor(root string) *Repo {
	if root == "" {
		return nil
	}
	for _, r := range a.Repos {
		if r.Root == root {
			return r
		}
	}
	return nil
}

// UseRepo 把当前仓库切到 r（终端、历史、标题栏的分支胶囊跟着它）：在 Git 栏、文件树、历史的
// "所有仓库"里点到一个仓库的东西时调。好久没读的仓库顺手重读；右栏开着就读它的历史。
func (a *App) UseRepo(r *Repo) {
	i := slices.Index(a.Repos, r)
	if i < 0 || i == a.Selected && a.Current() == r {
		return
	}
	a.Selected = i
	a.ClearLocalPath() // 本地历史的路径模式：切仓库就退出
	if !r.Loading && (!r.Loaded || r.watcher == nil && time.Since(r.LoadedAt) > StaleAfter) {
		a.RefreshRepo(r, WhyOpen)
	}
	if a.St.RightBar {
		a.EnsureHistory(r)
	}
}

// SelectRepo 切到第 i 个仓库并在文件树里展开它（"仓库"菜单、文件树的仓库行、添加仓库之后）。
func (a *App) SelectRepo(i int) {
	if i < 0 || i >= len(a.Repos) {
		return
	}
	r := a.Repos[i]
	a.UseRepo(r)
	if !r.Expanded {
		r.Expanded = true
		a.tree.ver++
		a.EnsureFiles(r)
	}
}

// JumpRepo 是 ⌘1–9：跳到第 i 个仓库，在文件树和 Git 栏里都展开它、滚到它那一段的开头。
func (a *App) JumpRepo(i int) {
	if i < 0 || i >= len(a.Repos) {
		return
	}
	r := a.Repos[i]
	a.SelectRepo(i)
	if !r.GitOpen() {
		r.GitFold = FoldOpen
	}
	if row := a.GitRowOf(r); row >= 0 {
		a.ChangeList.ScrollTo(row, ui.Start)
	}
	if row := slices.IndexFunc(a.TreeRows(), func(tr TreeRow) bool { return tr.Root && tr.Repo == r }); row >= 0 {
		a.FileList.ScrollTo(row, ui.Start)
	}
}

// AnyLoading 是有没有仓库正在读状态。
func (a *App) AnyLoading() bool {
	return slices.ContainsFunc(a.Repos, func(r *Repo) bool { return r.Loading })
}

// TotalChanges 是所有仓库的变更数。
func (a *App) TotalChanges() int {
	n := 0
	for _, r := range a.Repos {
		n += r.Status.Count
	}
	return n
}

// Ask 让用户确认一步会改仓库或丢东西的操作；点了 button 才跑 fn。
func (a *App) Ask(title, message, button string, fn func()) {
	a.Confirm = &ConfirmReq{Title: title, Message: message, Button: button, Fn: fn}
	a.ConfirmOpen = true
}

// AskDanger 是 Ask 的危险版：删分支 / 标签 / 远程、丢弃改动、重置 --hard、强制推送、移除工作树、中止变基 / 合并、丢弃贮藏这一类，
// 对话框是红的警告和红按钮；保存时盖掉磁盘上别人的改动、换编码丢掉没保存的、撤销 / 修改推送过的提交也是。
func (a *App) AskDanger(title, message, button string, fn func()) {
	a.Ask(title, message, button, fn)
	a.Confirm.Danger = true
}

// Fail 在窗口里弹一个错误提示。
func (a *App) Fail(title string, err error) {
	a.AlertTitle, a.AlertMsg, a.AlertDetail, a.AlertOpen = title, i18n.T(err.Error()), "", true // 包里的固定错误（如 search.ErrEmpty）在表里
	// 认出了 git / ssh 的原话：先一句人话，原话的最后几行放下面；我们自己写的话（humanErr）不认
	var said humanErr
	if why := git.Explain(err.Error()); why != "" && !errors.As(err, &said) {
		a.AlertMsg, a.AlertDetail = why, tailLines(err.Error(), alertTail)
	}
	a.AlertErr = true
}

// humanErr 是已经写给人看的错误（如取消后留下锁文件的那句），Fail 照原样显示、不拿 git.Explain 认。
type humanErr string

func (e humanErr) Error() string { return string(e) }

// alertTail 是出错框里 git 原话留几行。
const alertTail = 8

// tailLines 是 s 去掉空行和 git 的 hint: 行（上面那句人话说的就是它）后的最后 n 行。
func tailLines(s string, n int) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimRight(l, " \r"); strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "hint:") {
			keep = append(keep, l)
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, "\n")
}

// LogEntry 是输出栏里的一条：git 操作、⌘K 命令的结果。
type LogEntry struct {
	Time   time.Time
	Repo   string
	Title  string // 如 "$ git push" 或 "保存 a.txt"
	Text   string // 命令的输出，可以为空
	Failed bool
}

// maxLog 是输出栏最多留的条数。
const maxLog = 500

// Log 往输出栏记一条。
func (a *App) Log(r *Repo, title, text string, failed bool) {
	e := LogEntry{Time: time.Now(), Title: title, Text: text, Failed: failed}
	if r != nil {
		e.Repo = r.Name
	}
	if failed { // 失败的也记进日志文件（crash 包把 log 接到 workbench.log）
		first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
		log.Printf("%s %s: %s", e.Repo, title, first)
	}
	a.Output = append(a.Output, e)
	if len(a.Output) > maxLog {
		a.Output = slices.Delete(a.Output, 0, len(a.Output)-maxLog)
	}
}
