package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// StateFile 记着用户加进来 / 从列表移除的仓库、界面设置（三个区域开没开、侧栏模式、
// 区域大小、diff 布局）和每个工作区上次打开的标签、展开的目录，放在应用自己的数据目录，
// 不碰用户的 .code-workspace 文件。
const StateFile = "state.json"

// Version 是 state.json 当前的版本。没有 version 的是原型时期的（面板表或四个布尔），要迁移；
// 从 2 起字段只加不改，旧版本的文件直接读（3 加了 sessions 和 settings；4 起会话里的 groups 记的是
// 和默认反过来的分组，贮藏默认收着，3 的文件里收起的贮藏就是默认，读的时候去掉）。
const Version = 4

// firstPlain 是不用迁移、直接读的最早版本。
const firstPlain = 2

// firstToggled 是会话里的 groups 记"和默认反过来的分组"（而不是"收起的分组"）的第一个版本。
const firstToggled = 4

// 侧栏的四种模式。
const (
	ModeFiles   = "files"
	ModeSearch  = "search"
	ModeGit     = "git"
	ModeOutline = "outline"
)

// State 是 state.json 的内容。
type State struct {
	Version    int    `json:"version"`
	AppVersion string `json:"appVersion,omitempty"` // 上次用的应用版本（换了版本启动时状态栏提示一下）
	// Added、Hidden 在内存里是当前工作区的：用"添加仓库"加进来的仓库根、从列表移除的工作区仓库。
	// 按工作区记在 Session 里，存盘时这两个字段是空的；读到老版本的（那时不分工作区）就归给第一个打开的工作区。
	Added  []string `json:"added,omitempty"`
	Hidden []string `json:"hidden,omitempty"`

	RecentWs []string `json:"recentWs,omitempty"` // 最近打开的工作区（.code-workspace 或目录，最近的在前，最多 MaxRecentWs 个）
	CloneDir string   `json:"cloneDir,omitempty"` // 上次克隆到的父目录，克隆对话框默认放这里

	Sidebar      bool `json:"sidebar"`                // 左侧栏
	RightBar     bool `json:"rightBar"`               // 右栏（历史）
	HistAll      bool `json:"histAll,omitempty"`      // 右栏显示所有仓库合并的历史（否则只当前仓库）
	HistBranches bool `json:"histBranches,omitempty"` // 右栏历史读所有分支（本地、远程和标签），否则只 HEAD
	HistNoGraph  bool `json:"histNoGraph,omitempty"`  // 右栏历史不画提交图（默认画，所以存反过来的，旧文件不用迁移）
	// 侧栏“打开的编辑器”那一段收起了（开不开在设置里，showOpenEditors）
	OpenEditorsFolded bool    `json:"openEditorsFolded,omitempty"`
	BottomBar         bool    `json:"bottomBar"` // 底栏（终端、输出）
	SidebarMode       string  `json:"sidebarMode,omitempty"`
	SidebarW          float32 `json:"sidebarW,omitempty"`
	RightW            float32 `json:"rightW,omitempty"`
	BottomH           float32 `json:"bottomH,omitempty"`
	TermW             float32 `json:"termW,omitempty"` // 设置里终端放在右边时它的宽

	Unified  bool `json:"unified,omitempty"`  // diff 统一视图
	FullDiff bool `json:"fullDiff,omitempty"` // diff 显示全文

	Recent []string `json:"recent,omitempty"` // 最近打开的文件（磁盘路径，最近的在前），快速打开里靠前

	CommitMsgs []string `json:"commitMsgs,omitempty"` // 提交成功过的信息（所有仓库共用，最近的在前），提交框里 ↑ 翻
	// SearchHistory 是最近搜索（侧栏搜索页回车、打开过结果的，最近的在前），搜索框里 ↑ 翻；连同当时的开关和包含 / 排除
	// （老版本只记字、叫 "searches"，读的时候迁过来，不再写回）。
	SearchHistory []SearchEntry `json:"searchHistory,omitempty"`
	// TaskHistory 是最近跑过的任务（面板 "!" 回车的，最近的在前，最多 MaxTaskHistory 条），面板里靠前。
	TaskHistory []TaskRun `json:"taskHistory,omitempty"`

	Settings Settings `json:"settings"` // 设置窗口里的那些

	// Sessions 按工作区文件的路径记着上次关窗口（或切走）时的样子。
	Sessions map[string]*Session `json:"sessions,omitempty"`

	// Windows 是退出时开着的窗口的工作区（按槽位，空工作区的窗口是 ""），下次启动一个个开回来。
	// 只有一个窗口、开的就是最近的工作区（或空工作区）时不记，和以前的文件一样。
	Windows []string `json:"windows,omitempty"`
}

// Session 是一个工作区上次的样子：打开的文件标签、当前仓库、文件树里展开的仓库和目录、
// Git 栏里点过箭头的仓库段。
type Session struct {
	Added  []string            `json:"added,omitempty"`  // 用"添加仓库"加进这个工作区的仓库根
	Hidden []string            `json:"hidden,omitempty"` // 从列表移除的这个工作区的仓库
	Tabs   []Tab               `json:"tabs,omitempty"`
	Active int                 `json:"active"`           // 当前标签在 Tabs 里的下标，-1 为没有（或当前是 Diff 等不记的标签）
	Repo   string              `json:"repo,omitempty"`   // 当前仓库的根
	Open   []string            `json:"open,omitempty"`   // 展开着的仓库根
	Dirs   map[string][]string `json:"dirs,omitempty"`   // 仓库根 → 展开着的目录（仓库内路径）
	Git    map[string]string   `json:"git,omitempty"`    // 仓库根 → Git 栏里这一段 "open" / "closed"；没点过的（按有没有改动）不记
	Groups map[string][]string `json:"groups,omitempty"` // 仓库根 → Git 栏里这一段点过、和默认反过来的分组（conflict / staged / unstaged / untracked 默认展开，stash 默认收起）
	Terms  map[string]*Terms   `json:"terms,omitempty"`  // 仓库根 → 底栏里这个仓库的终端标签；只有默认那一个的不记
	WtOpen []string            `json:"wtOpen,omitempty"` // Git 栏里"工作树"块展开着的仓库根（默认收着，只记展开的）
	Layout *Layout             `json:"layout,omitempty"` // 编辑器分屏；没有 = 一组（老会话照旧）
	Panels *Panels             `json:"panels,omitempty"` // 三栏和 diff、历史的看法；没有 = 用顶层的（最近一次的）
}

// Panels 是一个工作区的布局：三栏开关和大小、侧栏的页、diff 和右栏历史的看法。存会话时从 State 顶层抄（State.Panels），
// 打开 / 切到这个工作区时灌回顶层（SetPanels）；顶层照写，是最近一次的，给没记过的工作区和空工作区用。
type Panels struct {
	Sidebar           bool    `json:"sidebar"`
	RightBar          bool    `json:"rightBar"`
	BottomBar         bool    `json:"bottomBar"`
	SidebarMode       string  `json:"sidebarMode,omitempty"`
	SidebarW          float32 `json:"sidebarW,omitempty"`
	RightW            float32 `json:"rightW,omitempty"`
	BottomH           float32 `json:"bottomH,omitempty"`
	TermW             float32 `json:"termW,omitempty"`
	Unified           bool    `json:"unified,omitempty"`
	FullDiff          bool    `json:"fullDiff,omitempty"`
	HistAll           bool    `json:"histAll,omitempty"`
	HistBranches      bool    `json:"histBranches,omitempty"`
	HistNoGraph       bool    `json:"histNoGraph,omitempty"`
	OpenEditorsFolded bool    `json:"openEditorsFolded,omitempty"`
}

// Panels 是 st 现在的布局（新的一份）。
func (st *State) Panels() *Panels {
	return &Panels{Sidebar: st.Sidebar, RightBar: st.RightBar, BottomBar: st.BottomBar, SidebarMode: st.SidebarMode,
		SidebarW: st.SidebarW, RightW: st.RightW, BottomH: st.BottomH, TermW: st.TermW, Unified: st.Unified, FullDiff: st.FullDiff,
		HistAll: st.HistAll, HistBranches: st.HistBranches, HistNoGraph: st.HistNoGraph, OpenEditorsFolded: st.OpenEditorsFolded}
}

// SetPanels 把一个工作区记的布局 p 灌回 st，不合理的值换回默认。
func (st *State) SetPanels(p *Panels) {
	st.Sidebar, st.RightBar, st.BottomBar, st.SidebarMode = p.Sidebar, p.RightBar, p.BottomBar, p.SidebarMode
	st.SidebarW, st.RightW, st.BottomH, st.TermW = p.SidebarW, p.RightW, p.BottomH, p.TermW
	st.Unified, st.FullDiff, st.HistAll, st.HistBranches, st.HistNoGraph = p.Unified, p.FullDiff, p.HistAll, p.HistBranches, p.HistNoGraph
	st.OpenEditorsFolded = p.OpenEditorsFolded
	st.clampPanels()
}

// Layout 是编辑器组的拆分树：叶子是一组，记它的标签在 Session.Tabs 里的下标、当前是其中第几个、
// 有没有键盘；否则是左右（Row）或上下分的两半，A 占 Ratio。只有一组时不记。
type Layout struct {
	Tabs  []int   `json:"tabs,omitempty"`
	Sel   int     `json:"sel,omitempty"`
	Focus bool    `json:"focus,omitempty"`
	Row   bool    `json:"row,omitempty"`
	Ratio float32 `json:"ratio,omitempty"`
	A     *Layout `json:"a,omitempty"`
	B     *Layout `json:"b,omitempty"`
}

// Terms 是一个仓库的终端标签：名字、顺序和工作目录（不记内容）。
type Terms struct {
	Tabs   []TermTab `json:"tabs"`
	Active int       `json:"active,omitempty"`
}

// TermTab 是一个终端标签：用户起的名字（空为"终端 n"）和关窗口时 shell 在的目录（空为仓库根）。
// Sids 是 shell 跑在终端后台进程（termd）里时，标签里各格的会话，从左到右、从上到下；再打开时接回去。
// Split 是分屏的样子和每格自己的目录、会话、上次的输出；有它就按它还原，Sids 照写给老版本用。
// InEditor、Num 是开在编辑器区的标签：排在底栏的后面，会话里 Kind 为 TabTerm 的标签按 Num 对上它。
type TermTab struct {
	Name     string     `json:"name,omitempty"`
	Dir      string     `json:"dir,omitempty"`
	Sids     []string   `json:"sids,omitempty"`
	Split    *TermSplit `json:"split,omitempty"`
	InEditor bool       `json:"inEditor,omitempty"`
	Num      int        `json:"num,omitempty"`
}

// TermSplit 是终端标签里的分屏树：叶子是一格，记它 shell 的目录（空为仓库根）、termd 里的会话（只记没退出的）
// 和上次的输出存在 termhist 里的文件名（Hist，只有不在后台的格）；否则是左右（Row）或上下分的两半，A 占 Ratio。
type TermSplit struct {
	Row   bool       `json:"row,omitempty"`
	Ratio float32    `json:"ratio,omitempty"`
	A     *TermSplit `json:"a,omitempty"`
	B     *TermSplit `json:"b,omitempty"`
	Dir   string     `json:"dir,omitempty"`
	Sid   string     `json:"sid,omitempty"`
	Hist  string     `json:"hist,omitempty"`
	// 这一格用的终端配置（Settings.TermProfiles 的名字），空为默认
	Profile string `json:"profile,omitempty"`
}

// MaxTermPanes 是一个终端标签最多几格（和应用里分屏的上限一样）。
const MaxTermPanes = 8

// Leaves 是树里的格，从左到右、从上到下。
func (s *TermSplit) Leaves() []*TermSplit {
	if s == nil {
		return nil
	}
	if s.A == nil {
		return []*TermSplit{s}
	}
	return append(s.A.Leaves(), s.B.Leaves()...)
}

// clampSplit 是改过的 s：两半缺一半的、格太多的整棵不要（退回 Sids），比例不对的换成一半，目录不是绝对路径的、
// Hist 不是一个普通文件名的清掉。
func clampSplit(s *TermSplit) *TermSplit {
	var ok func(s *TermSplit, depth int) bool
	ok = func(s *TermSplit, depth int) bool {
		if s.A == nil && s.B == nil {
			return true
		}
		return s.A != nil && s.B != nil && depth < MaxTermPanes && ok(s.A, depth+1) && ok(s.B, depth+1)
	}
	if s == nil || !ok(s, 0) || len(s.Leaves()) > MaxTermPanes {
		return nil
	}
	var fix func(s *TermSplit)
	fix = func(s *TermSplit) {
		if s.A == nil {
			s.Row, s.Ratio = false, 0
			if s.Dir != "" && !filepath.IsAbs(s.Dir) {
				s.Dir = ""
			}
			if s.Hist != "" && (s.Hist != filepath.Base(s.Hist) || s.Hist[0] == '.' || filepath.Ext(s.Hist) != ".vt") {
				s.Hist = ""
			}
			if len(s.Profile) > 100 {
				s.Profile = ""
			}
			return
		}
		s.Dir, s.Sid, s.Hist, s.Profile = "", "", "", ""
		if !(s.Ratio > 0 && s.Ratio < 1) {
			s.Ratio = 0.5
		}
		fix(s.A)
		fix(s.B)
	}
	fix(s)
	return s
}

// MaxTermTabs 是一个仓库最多记的终端标签数。
const MaxTermTabs = 20

// Tab 是一个标签：磁盘路径、固定了没有、光标（行、列从 0 起，列按字节）、滚到最上面的那一行。
// 历史版本（Kind 为 TabVersion）和 Diff（TabDiff）标签也记：Repo 是仓库根，Change 是哪项改动，
// Diff 不记光标和滚动。它们的 Path 也写仓库根：老版本读到不是绝对路径的标签会删掉它，后面的下标（Active、
// Layout）全错位；写成仓库根，老版本 os.Stat 看到是目录就跳过，下标不动。
type Tab struct {
	Path    string     `json:"path"`
	Kind    string     `json:"kind,omitempty"`
	Repo    string     `json:"repo,omitempty"`
	Change  *TabChange `json:"change,omitempty"`
	Pinned  bool       `json:"pinned,omitempty"`
	Preview bool       `json:"preview,omitempty"` // Markdown、SVG显示的是预览
	Line    int        `json:"line,omitempty"`
	Col     int        `json:"col,omitempty"`
	Top     int        `json:"top,omitempty"`
	Enc     string     `json:"enc,omitempty"` // 文件、历史版本手动选的编码（docs.Encoding，"" 为自动认），重开按它解
	// 状态栏手动选的语言（docs.Doc.Lang）和缩进（docs.Doc.Indent），只文件标签记
	Lang   string `json:"lang,omitempty"`
	Indent string `json:"indent,omitempty"`
	// 开在编辑器区的终端标签（Kind 为 TabTerm）是仓库 Repo 的 Terms 里 Num 为 TermNum 的那个；Path 也写仓库根
	TermNum int `json:"termNum,omitempty"`
	// 文件、历史版本按十六进制看（docs.Doc.Hex）
	Hex bool `json:"hex,omitempty"`
	// 同一个文件在另一组里再开的第几份（docs.Doc.Copy，镜像）；重开时同一个路径第二次出现就挂成前一份的镜像
	Copy int `json:"copy,omitempty"`
}

// 会话里 Tab.Kind 的写法；空是文件标签。
const (
	TabVersion = "version"
	TabDiff    = "diff"
	TabTerm    = "term"
)

// TabChange 是历史版本和 Diff 标签的那项改动（git.Change 里重开要用的几样）。Kind 存名字（staged、unstaged、
// untracked、commit、conflict），不存枚举的数；Code 是一个字母（M A D R C T U ?，两边的冲突是 S）；
// Commit / Parent 都是完整的 SHA（不存分支名，分支挪了重开的还是上次看到的那份），Range 是比较两个提交里的。
type TabChange struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Orig   string `json:"orig,omitempty"`
	Code   string `json:"code,omitempty"`
	Sub    string `json:"sub,omitempty"`
	Commit string `json:"commit,omitempty"`
	Parent string `json:"parent,omitempty"`
	Range  bool   `json:"range,omitempty"`
}

// MaxRecentWs 是最多记几个最近的工作区。
const MaxRecentWs = 10

// MaxTabs 是一个工作区最多记的标签数。
const MaxTabs = 50

// Default 是第一次打开时的样子：三个区域都开着，侧栏是文件树。
func Default() State {
	return State{Version: Version, Sidebar: true, RightBar: true, BottomBar: true, SidebarMode: ModeFiles}
}

// TaskRun 是一条跑过的任务：在哪个仓库（根目录）跑的哪条命令。
type TaskRun struct {
	Repo string `json:"repo"`
	Cmd  string `json:"cmd"`
}

// MaxTaskHistory 是最多记几条跑过的任务。
const MaxTaskHistory = 20

// SearchEntry 是一条最近搜索：搜的字和当时的开关、包含 / 排除。
type SearchEntry struct {
	Text    string `json:"text"`
	Case    bool   `json:"case,omitempty"`   // 区分大小写
	Word    bool   `json:"word,omitempty"`   // 全字匹配
	Regexp  bool   `json:"regexp,omitempty"` // 正则表达式
	Include string `json:"include,omitempty"`
	Exclude string `json:"exclude,omitempty"`
	Ignored bool   `json:"ignored,omitempty"` // 也搜被忽略的文件
	Big     bool   `json:"big,omitempty"`     // 也搜 2 MB 以上的大文件
}

// legacy 是原型时期 state.json 里新版不再用的字段。
type legacy struct {
	Version int             `json:"version"`
	Panels  json.RawMessage `json:"panels"`
}

// LoadState 读 dir 里的 state.json（上一版只记仓库的叫 repos.json），没有或读不懂时
// 返回 Default 和读的错误（文件不存在不算错）。旧版的面板开关按下面的规则迁移：
//
//   - {"files": …, "changes": …, "history": …, "terminal": …}：侧栏 = 文件或变更开着；
//     文件关着、变更开着时侧栏是 Git 模式；右栏 = 历史；底栏 = 终端。
//   - [变更, 历史, Diff, 终端]：侧栏开（那一版的仓库列表总是在）；右栏 = 历史；底栏 = 终端。
func LoadState(dir string) (State, error) {
	st := Default()
	if dir == "" {
		return st, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		if data, err = os.ReadFile(filepath.Join(dir, "repos.json")); err != nil {
			if os.IsNotExist(err) {
				err = nil
			}
			return st, err
		}
	}
	var old legacy
	if err := json.Unmarshal(data, &old); err != nil {
		return st, err
	}
	if old.Version >= firstPlain {
		if err := json.Unmarshal(data, &st); err != nil {
			return Default(), err
		}
		if old.Version < firstToggled {
			dropFoldedStash(&st)
		}
		legacyTreeColors(data, &st)
		legacySearches(data, &st)
	} else {
		// 原型时期的文件：只认仓库列表、diff 设置和侧栏宽度，面板按规则迁移。
		var v1 struct {
			Added, Hidden     []string
			Unified, FullDiff bool
			SidebarW          float32 `json:"sidebarW"`
		}
		_ = json.Unmarshal(data, &v1)
		st.Added, st.Hidden, st.Unified, st.FullDiff, st.SidebarW = v1.Added, v1.Hidden, v1.Unified, v1.FullDiff, v1.SidebarW
		migratePanels(&st, old.Panels)
	}
	st.Version = Version
	st.clamp()
	return st, nil
}

// legacyTreeColors 认老版本的 treeStatusColors（存正面、默认关）：现在默认开、存反面 treeNoStatusColors。
// 写着 false 的当关掉（omitempty 平常写不出 false，手改的才有）；写着 true 的和没写的照新的默认开着。
func legacyTreeColors(data []byte, st *State) {
	var v struct {
		Settings struct {
			TreeStatusColors *bool `json:"treeStatusColors"`
		} `json:"settings"`
	}
	if json.Unmarshal(data, &v) == nil && v.Settings.TreeStatusColors != nil && !*v.Settings.TreeStatusColors {
		st.Settings.TreeNoStatusColors = true
	}
}

// legacySearches 认老版本的 "searches"（只记了字）：新的 searchHistory 空着时迁过来，开关都是关的。
func legacySearches(data []byte, st *State) {
	if len(st.SearchHistory) > 0 {
		return
	}
	var v struct {
		Searches []string `json:"searches"`
	}
	if json.Unmarshal(data, &v) != nil {
		return
	}
	for _, q := range v.Searches {
		if q != "" {
			st.SearchHistory = append(st.SearchHistory, SearchEntry{Text: q})
		}
	}
}

// dropFoldedStash 去掉第 3 版会话里收起的贮藏分组：那时 groups 记的是收起的组，4 起贮藏默认就收着、
// groups 里的 "stash" 意思是点开了。
func dropFoldedStash(st *State) {
	for _, s := range st.Sessions {
		if s == nil {
			continue
		}
		for root, gs := range s.Groups {
			if gs = slices.DeleteFunc(gs, func(g string) bool { return g == "stash" }); len(gs) > 0 {
				s.Groups[root] = gs
			} else {
				delete(s.Groups, root)
			}
		}
		if len(s.Groups) == 0 {
			s.Groups = nil
		}
	}
}

func migratePanels(st *State, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var m map[string]bool
	if json.Unmarshal(raw, &m) == nil {
		on := func(id string) bool {
			v, ok := m[id]
			return !ok || v // 表里没有的面板按默认（开着）算
		}
		files, changes := on("files"), on("changes")
		st.Sidebar = files || changes
		if !files && changes {
			st.SidebarMode = ModeGit
		}
		st.RightBar, st.BottomBar = on("history"), on("terminal")
		return
	}
	var arr [4]bool
	if json.Unmarshal(raw, &arr) == nil {
		st.Sidebar, st.RightBar, st.BottomBar = true, arr[1], arr[3]
	}
}

// clamp 把不合理的值换回默认。
func (st *State) clamp() {
	st.clampPanels()
	st.Settings.Clamp()
	seen := map[string]bool{}
	st.RecentWs = slices.DeleteFunc(st.RecentWs, func(p string) bool {
		drop := !filepath.IsAbs(p) || seen[p]
		seen[p] = true
		return drop
	})
	if len(st.RecentWs) > MaxRecentWs {
		st.RecentWs = st.RecentWs[:MaxRecentWs]
	}
	clear(seen)
	st.TaskHistory = slices.DeleteFunc(st.TaskHistory, func(r TaskRun) bool {
		k := r.Repo + "\x00" + r.Cmd
		drop := !filepath.IsAbs(r.Repo) || r.Cmd == "" || strings.ContainsAny(r.Cmd, "\n\r") || seen[k]
		seen[k] = true
		return drop
	})
	if len(st.TaskHistory) > MaxTaskHistory {
		st.TaskHistory = st.TaskHistory[:MaxTaskHistory]
	}
	for k, s := range st.Sessions {
		if s == nil || k == "" {
			delete(st.Sessions, k)
			continue
		}
		s.Tabs = slices.DeleteFunc(s.Tabs, func(t Tab) bool {
			return !filepath.IsAbs(t.Path) || t.Line < 0 || t.Col < 0 || t.Top < 0 || t.TermNum < 0
		})
		if len(s.Tabs) > MaxTabs {
			s.Tabs = s.Tabs[:MaxTabs]
		}
		if s.Active < -1 || s.Active >= len(s.Tabs) {
			s.Active = -1
		}
		rel := func(p string) bool { return !filepath.IsAbs(p) }
		s.Added, s.Hidden = slices.DeleteFunc(s.Added, rel), slices.DeleteFunc(s.Hidden, rel)
		for root, ts := range s.Terms {
			if ts == nil || !filepath.IsAbs(root) {
				delete(s.Terms, root)
				continue
			}
			ts.Tabs = slices.DeleteFunc(ts.Tabs, func(t TermTab) bool { return t.Dir != "" && !filepath.IsAbs(t.Dir) })
			if len(ts.Tabs) > MaxTermTabs {
				ts.Tabs = ts.Tabs[:MaxTermTabs]
			}
			for i := range ts.Tabs {
				ts.Tabs[i].Split = clampSplit(ts.Tabs[i].Split)
			}
			if ts.Active < 0 || ts.Active >= len(ts.Tabs) {
				ts.Active = 0
			}
		}
	}
}

// clampPanels 把不合理的布局值换回默认。
func (st *State) clampPanels() {
	switch st.SidebarMode {
	case ModeFiles, ModeSearch, ModeGit, ModeOutline:
	default:
		st.SidebarMode = ModeFiles
	}
	if st.SidebarW < 160 || st.SidebarW > 800 {
		st.SidebarW = 0
	}
	if st.RightW < 200 || st.RightW > 900 {
		st.RightW = 0
	}
	if st.BottomH < 80 || st.BottomH > 1200 {
		st.BottomH = 0
	}
	if st.TermW < 240 || st.TermW > 1200 {
		st.TermW = 0
	}
}

// SaveState 把 st 写进 dir/state.json（缩进的 JSON，末尾换行）。
func SaveState(dir string, st State) error {
	if dir == "" {
		return nil
	}
	return write(dir, encode(st))
}

// encode 是 st 存盘的样子。
func encode(st State) []byte {
	st.Version = Version
	data, _ := json.MarshalIndent(st, "", "  ")
	return append(data, '\n')
}

func write(dir string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, StateFile), data, 0o644)
}
