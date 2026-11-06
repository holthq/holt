package workspace

import (
	"cmp"
	"slices"
	"strings"
)

// Settings 是设置窗口（⌘,）里的那些，随 state.json 存在 "settings" 下。零值就是默认，
// 所以旧的 state.json 读进来什么都不用迁移；读不懂的值在 Clamp 里换回默认。
type Settings struct {
	// 外观
	Theme          string `json:"theme,omitempty"`          // "" 跟随系统、"light"、"dark"
	Backdrop       string `json:"backdrop,omitempty"`       // 窗口底："" 柔和（纯色底上几团淡淡的颜色）、"flat" 纯色、"vibrancy" 通透（透出桌面的系统材质）
	Material       string `json:"material,omitempty"`       // 通透时用的系统材质："" 侧栏，或 Materials 里别的（mygo.Vibrancy 的值）
	Accent         string `json:"accent,omitempty"`         // 强调色："" 是默认的那个（DefaultAccent），或 Accents 里别的
	SolidSelection bool   `json:"solidSelection,omitempty"` // 列表的选中行用强调色的实心块；默认（关着）是淡底：强调色淡淡的一层、左边一道竖条
	Zoom           int    `json:"zoom,omitempty"`           // 界面缩放，百分数（⌘= / ⌘- / ⌘0，所有窗口一起）；0 为 100
	// 默认开，存反面
	NoDiagrams bool `json:"noDiagrams,omitempty"` // Markdown 预览里不渲染 Mermaid 图和公式（默认渲染：看不见的 WebView 画成位图，internal/render）

	// 编辑器
	EditorFont   string  `json:"editorFont,omitempty"`   // "" 为内置的等宽字体
	EditorSize   float32 `json:"editorSize,omitempty"`   // 0 为 12.5
	Indent       string  `json:"indent,omitempty"`       // "" 按语言、"tab"、"2"、"4"、"8"（空格）
	SoftWrap     bool    `json:"softWrap,omitempty"`     // 软换行（⌥Z）
	Whitespace   string  `json:"whitespace,omitempty"`   // 显示空白字符："" 只在选中的文字里、"all"、"trailing"、"none"
	AutoSave     string  `json:"autoSave,omitempty"`     // "" 不自动保存、"blur"（窗口切走时）、"delay"（停手 1 秒后）
	TrimTrailing bool    `json:"trimTrailing,omitempty"` // 保存时去掉行尾空白（光标所在的行、Markdown 的两空格硬换行不动）
	FinalNewline bool    `json:"finalNewline,omitempty"` // 保存时补上末尾换行
	// 默认开，存反面
	NoStickyScroll bool `json:"noStickyScroll,omitempty"` // 不粘性滚动（默认开：滚动时把当前函数 / 类 / 标题吸在编辑器顶上，ui/sticky.go）
	ShowIgnored    bool `json:"showIgnored,omitempty"`    // 文件树里灰显被 .gitignore 忽略的文件和目录（放在设置的"文件树"页）
	// 本地历史（默认开，存反面；app/localhist.go）
	NoLocalHistory bool `json:"noLocalHistory,omitempty"` // 不记本地历史（默认每次保存在状态目录的 history 里留一份）
	HistoryDays    int  `json:"historyDays,omitempty"`    // 本地历史留几天：0 为 30，或 HistoryDayOpts 里别的

	// 文件树（三项默认开着，存的是反面，零值 = 默认）
	TreeNoFollow  bool `json:"treeNoFollow,omitempty"`  // 不跟随当前文件（默认跟随：切到一个文件，文件树展开到它、选中、滚到）
	TreeNoCompact bool `json:"treeNoCompact,omitempty"` // 不合并只有一个子目录的目录（默认合并成 "internal / ui" 一行）
	TreeNoGuides  bool `json:"treeNoGuides,omitempty"`  // 不画缩进参考线
	// 默认开，存反面（老版本存的是正面的 treeStatusColors、默认关，LoadState 认）
	TreeNoStatusColors bool `json:"treeNoStatusColors,omitempty"` // 不按 git 状态给文件名着色（默认改了黄、新的绿、删了灰加删除线、冲突红，有改动的目录名也黄）
	// 默认彩色底块，存反面
	TreeIconsPlain bool   `json:"treeIconsPlain,omitempty"` // 图标风格"线形"：单色图标、线框的目录（默认是彩色底块、按名字着色的实心目录）
	Exclude        string `json:"exclude,omitempty"`        // 隐藏的文件（glob，逗号分开）：和工作区文件、仓库 .vscode/settings.json 的 files.exclude 并在一起（app/exclude.go）
	// 默认关，存正面
	ShowOpenEditors bool `json:"showOpenEditors,omitempty"` // 侧栏文件模式上面一段“打开的编辑器”：所有组的标签各一行（app/openeditors.go）

	// 终端（改了之后新开的终端生效，已经开着的按"重新启动终端"）
	Shell            string        `json:"shell,omitempty"`            // "" 为 $SHELL
	TermFont         string        `json:"termFont,omitempty"`         // "" 跟编辑器一样
	TermSize         float32       `json:"termSize,omitempty"`         // 0 为 12.5
	TermTheme        string        `json:"termTheme,omitempty"`        // "" 跟着外观、"light"、"dark"，或 Ghostty 的主题名（"Dracula"…）
	Scrollback       int           `json:"scrollback,omitempty"`       // 回滚保留的输出，MB；0 为 10
	TermBlink        bool          `json:"termBlink,omitempty"`        // 光标闪烁，默认不闪（闪的光标让窗口一直在画，多占内存）
	TermDock         string        `json:"termDock,omitempty"`         // 终端放在哪："" 底栏、"right" 右栏
	TermDaemon       bool          `json:"termDaemon,omitempty"`       // 关掉窗口后终端继续在后台运行（shell 跑在 termd 里），默认关
	TermOptionAsAlt  bool          `json:"termOptionAsAlt,omitempty"`  // ⌥ 作为 Meta 键（新开的终端生效）
	TermCopyOnSelect bool          `json:"termCopyOnSelect,omitempty"` // 终端里选中即复制到剪贴板（开着的终端马上生效）
	TermProfiles     []TermProfile `json:"termProfiles,omitempty"`
	AgentNames       string        `json:"agentNames,omitempty"` // 另外认作 agent 的前台命令，逗号分开（默认的 claude、codex… 不用写）     // "其他配置"表：底栏「+」右键、"用配置新建终端…"里挑（上面的 Shell 是默认）

	// Git
	PullMode string `json:"pullMode,omitempty"` // 拉取方式："" 只快进（--ff-only）、"rebase"、"merge"
	AutoPush bool   `json:"autoPush,omitempty"` // 提交后自动推送，默认关
	// Git 栏里显示工作树 / 贮藏，默认都关（app.Repo.ShowWts / ShowStashes）
	ShowWorktrees bool      `json:"showWorktrees,omitempty"` // 工作树组、"需要你"条、段头的工作树数；关着时也不算各工作树的状态、不盯它们的目录
	ShowStashes   bool      `json:"showStashes,omitempty"`   // 贮藏组和段头的贮藏数；关着时贮藏、弹出贮藏照常能用
	AuthorName    string    `json:"authorName,omitempty"`    // 提交时改用的作者，空为用 git 自己的配置（默认）
	AuthorEmail   string    `json:"authorEmail,omitempty"`   //
	InlineBlame   bool      `json:"inlineBlame,omitempty"`   // 行内 blame：光标所在行的行尾显示作者、时间和提交摘要，默认关
	WebHosts      []WebHost `json:"webHosts,omitempty"`      // "在网页打开"按哪种风格拼地址：主机名不带关键字的自建 GitLab / Gitea 在这里指

	// 格式化（app/format.go）
	FormatOnSave bool              `json:"formatOnSave,omitempty"` // 保存时先跑外部格式化命令，默认关
	Formatters   map[string]string `json:"formatters,omitempty"`   // 每种语言（editor.Language.Name）的格式化命令：没有或 "" 是内置默认，"-" 是不格式化

	// 检查（app/lint.go）
	LintOnSave bool              `json:"lintOnSave,omitempty"` // 保存后跑外部检查命令，问题标进编辑器、列进问题面板，默认关
	Linters    map[string]string `json:"linters,omitempty"`    // 每种语言的检查命令：没有或 "" 是内置默认，"-" 是不检查

	// 语言
	Lang string `json:"lang,omitempty"` // "" 中文、"en" 英文
}

// WebHost 是设置里"网页风格"表的一行：这台主机（远程地址里的主机名，https 的带端口）的网页按 Kind 拼。
type WebHost struct {
	Host string `json:"host"`
	Kind string `json:"kind"` // WebKinds 之一
}

// TermProfile 是终端的一种配置（设置 → 终端 → 其他配置）：用别的 shell、带参数、加环境变量开终端。
type TermProfile struct {
	Name  string   `json:"name"`
	Shell string   `json:"shell"`
	Args  []string `json:"args,omitempty"` // 空为 -l（和默认的一样当登录 shell）
	Env   []string `json:"env,omitempty"`  // "K=V"，盖过应用自己的环境
}

// MaxTermProfiles 是最多几个终端配置。
const MaxTermProfiles = 10

// Command 是这个配置开终端跑的命令：Shell 加参数，没有参数是 [Shell, -l]。
func (p *TermProfile) Command() []string {
	if len(p.Args) == 0 {
		return []string{p.Shell, "-l"}
	}
	return append([]string{p.Shell}, p.Args...)
}

// Profiles 是能用的终端配置：名字非空、不和前面的重、Shell 非空、每个参数不超过 200 字节，最多 MaxTermProfiles 个；
// 环境里不是 K=V 的行不算。设置窗口里正在填的（名字、命令还空着）不在里面，读 state.json 时 Clamp 把它们丢掉。
func (s *Settings) Profiles() []TermProfile {
	var out []TermProfile
	seen := map[string]bool{}
	for _, p := range s.TermProfiles {
		p.Name, p.Shell = strings.TrimSpace(p.Name), strings.TrimSpace(p.Shell)
		bad := p.Name == "" || len(p.Name) > 100 || seen[p.Name] || p.Shell == "" || len(p.Shell) > 1024 || strings.ContainsAny(p.Name+p.Shell, "\x00\n") ||
			slices.ContainsFunc(p.Args, func(a string) bool { return len(a) > 200 || strings.Contains(a, "\x00") })
		if bad {
			continue
		}
		p.Args = slices.Clone(p.Args)
		p.Env = slices.DeleteFunc(slices.Clone(p.Env), func(kv string) bool {
			k, _, ok := strings.Cut(kv, "=")
			return !ok || k == "" || strings.ContainsAny(k, " \t") || strings.ContainsAny(kv, "\x00\n")
		})
		seen[p.Name] = true
		out = append(out, p)
		if len(out) == MaxTermProfiles {
			break
		}
	}
	return out
}

// Profile 是名字叫 name 的终端配置（能用的里找）；"" 或没有为 nil。
func (s *Settings) Profile(name string) *TermProfile {
	if name == "" {
		return nil
	}
	for _, p := range s.Profiles() {
		if p.Name == name {
			return &p
		}
	}
	return nil
}

// WebKinds 是"网页风格"可选的值，和 git.WebKinds 一一对应（这个包不引 git）。
var WebKinds = []string{"github", "gitlab", "bitbucket", "gitea", "generic"}

// WebKindFor 是设置里给 host 指定的风格，没指定为空。主机名不分大小写。
func (s *Settings) WebKindFor(host string) string {
	for _, h := range s.WebHosts {
		if strings.EqualFold(h.Host, host) {
			return h.Kind
		}
	}
	return ""
}

// 设置里各项可选的值（第一个是默认，存成 ""）。
var (
	Themes      = []string{"", "light", "dark"}
	Backdrops   = []string{"", "flat", "vibrancy"}                         // 以前的 "opaque" 键不认了（读进来忽略），开没开通透的都成柔和
	Materials   = []string{"", "under-window", "popover", "menu", "glass"} // "" 是 sidebar；hud、glass-clear 去掉了（HUD 把侧栏的字洗淡），存过的读回来是空
	Accents     = accents()
	Indents     = []string{"", "tab", "2", "4", "8"}
	Whitespaces = []string{"", "all", "trailing", "none"}
	AutoSaves   = []string{"", "blur", "delay"}
	TermThemes  = []string{"", "light", "dark"} // 后面还可以是 Ghostty 的主题名，Clamp 不查
	TermDocks   = []string{"", "right"}
	PullModes   = []string{"", "rebase", "merge"}
	Langs       = []string{"", "en"}
)

// DefaultAccent 是 Accent 存成 "" 时的强调色："steel" 钢蓝（以前是 "system" 跟随系统，mygo 跟着系统设置里的强调色）。
// 换默认只改这一行：Accents 里它存成 ""，存过的这个值读回来也成 ""；别的（包括跟随系统）存实名，换默认时不跟着变。
// 以前选“跟随系统”存的也是 ""，和没选过分不开，所以那些都成了钢蓝。
const DefaultAccent = "steel"

// AccentKeys 是所有的强调色，设置下拉里就是这个顺序。
var AccentKeys = []string{"system", "steel", "ink", "graphite", "teal", "indigo"}

// accents 是设置里强调色可选的值：AccentKeys 的顺序，默认的那个存成 ""。
func accents() []string {
	out := make([]string, len(AccentKeys))
	for i, k := range AccentKeys {
		if k != DefaultAccent {
			out[i] = k
		}
	}
	return out
}

// AccentOf 是设置的值 v 实际是哪个强调色（"" 换成 DefaultAccent）。
func AccentOf(v string) string { return cmp.Or(v, DefaultAccent) }

// ZoomSteps 是 ⌘= / ⌘- 一步一步走的界面缩放（百分数），状态栏缩放菜单里也是这几档。
var ZoomSteps = []int{50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300}

// DefaultHistoryDays 是本地历史默认留的天数（HistoryDays 为 0）；HistoryDayOpts 是设置里能选的。
const DefaultHistoryDays = 30

var HistoryDayOpts = []int{7, 30, 90, 365}

// LocalHistoryDays 是本地历史留几天。
func (s Settings) LocalHistoryDays() int { return cmp.Or(s.HistoryDays, DefaultHistoryDays) }

// ZoomPercent 是界面缩放的百分数（存的 0 是 100）。
func (s *Settings) ZoomPercent() int { return cmp.Or(s.Zoom, 100) }

// Clamp 把读不懂的值换回默认（读 state.json、设置文档保存、导入时）。
func (s *Settings) Clamp() {
	if s.Accent == DefaultAccent {
		s.Accent = ""
	}
	for _, f := range []struct {
		v  *string
		ok []string
	}{
		{&s.Theme, Themes}, {&s.Backdrop, Backdrops}, {&s.Material, Materials}, {&s.Accent, Accents}, {&s.Indent, Indents}, {&s.Whitespace, Whitespaces}, {&s.AutoSave, AutoSaves},
		{&s.TermDock, TermDocks}, {&s.PullMode, PullModes}, {&s.Lang, Langs},
	} {
		if !contains(f.ok, *f.v) {
			*f.v = ""
		}
	}
	if len(s.TermTheme) > 200 || strings.ContainsAny(s.TermTheme, "\x00\n") {
		s.TermTheme = ""
	}
	if s.EditorSize != 0 && (s.EditorSize < 9 || s.EditorSize > 32) {
		s.EditorSize = 0
	}
	if s.TermSize != 0 && (s.TermSize < 9 || s.TermSize > 32) {
		s.TermSize = 0
	}
	if s.Scrollback < 0 || s.Scrollback > 1000 {
		s.Scrollback = 0
	}
	if s.Zoom < 50 || s.Zoom > 300 || s.Zoom == 100 {
		s.Zoom = 0
	}
	if s.HistoryDays == DefaultHistoryDays || !slices.Contains(HistoryDayOpts, s.HistoryDays) {
		s.HistoryDays = 0
	}
	s.TermProfiles = s.Profiles()
	s.WebHosts = slices.DeleteFunc(s.WebHosts, func(h WebHost) bool { return !contains(WebKinds, h.Kind) || len(h.Host) > 255 })
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
