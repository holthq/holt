package ui

import (
	"embed"

	wui "github.com/egoist/mygo/ui"
)

// icons/ 是界面的图标：Phosphor 2.1.1（MIT）的 Regular，实心的文件夹是 Fill，填 currentColor，跟随文字颜色。
// lang-*.svg 是语言的单色图标：Go、C#、Objective-C、Vue、SQL、proto 是自己画的，别的来自 Simple Icons 13.21.0，
// 四周加了和 Phosphor 差不多的空；各自的许可见 resources/licenses/simple-icons.txt，加新的要先查那个 logo 自己的
// 许可（Vue 的不许商用，所以是自己画的 V，不是官方那个）。
// 别的文件名就是 Phosphor 的图标名。不是 Phosphor 原样的只有三个：sidebar-right / sidebar-bottom 是 sidebar-simple
// 翻过去、转过去的（三个区域开关要一样的画法），spinner-ring 是运行中的转圈，线宽和别的图标一样。
//
//go:embed icons/*.svg
var iconFS embed.FS

// icon 读 icons/name.svg。
func icon(name string) *wui.SVG {
	b, err := iconFS.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic("ui: " + err.Error())
	}
	return wui.MustParseSVG(b)
}

var (
	iconBranch      = icon("git-branch")
	iconRefresh     = icon("arrow-clockwise")
	iconTerminal    = icon("terminal")
	iconFolder      = icon("folder")
	iconCheckSquare = icon("check-square")
	iconHome        = icon("house")
	iconMerge       = icon("git-merge")
	iconCheck       = icon("check-circle")
	iconLayers      = icon("stack")
	iconSparkle     = icon("sparkle")
	iconCloud       = icon("cloud")
	iconAlert       = icon("warning-circle")
)

var (
	iconMaximize = icon("arrows-out")
	iconMinimize = icon("arrows-in")
)

var (
	iconPlus     = icon("plus")
	iconExternal = icon("arrow-square-out")
)

var (
	iconClose     = icon("x")
	iconReplace   = icon("swap")
	iconHistory   = icon("clock-counter-clockwise")
	iconCommit    = icon("git-commit")
	iconFetch     = icon("cloud-arrow-down")
	iconPull      = icon("arrow-line-down")
	iconPush      = icon("arrow-line-up")
	iconMore      = icon("dots-three")
	iconChevron   = icon("caret-down")
	iconChevronUp = icon("caret-up")
	iconColumns   = icon("columns")
	iconTag       = icon("tag")
	iconArchive   = icon("archive")
	iconUser      = icon("user")
	iconClock     = icon("clock")
	iconUndo      = icon("arrow-u-up-left")
	iconTrash     = icon("trash")
)

// 文件和编辑器面板用的图标。
var (
	iconFile         = icon("file")
	iconFolderOpen   = icon("folder-open")
	iconFilePen      = icon("note-pencil")
	iconSave         = icon("floppy-disk")
	iconPencil       = icon("pencil-simple")
	iconEye          = icon("eye")
	iconCode         = icon("code")
	iconImage        = icon("image")
	iconChevronRight = icon("caret-right")
	iconSearch       = icon("magnifying-glass")
	iconFilePlus     = icon("file-plus")
	iconFolderPlus   = icon("folder-plus")
)

// 新布局用的图标：标题栏的区域开关、前进后退。
var (
	iconSidebar     = icon("sidebar-simple")
	iconRightbar    = icon("sidebar-right")
	iconBottombar   = icon("sidebar-bottom")
	iconChevronLeft = icon("caret-left")
	iconOutput      = icon("text-align-left")
	iconDiff        = icon("git-diff") // ±：Diff 标签，和文件标签区分开
)

// 终端标签条和小标记用的图标。
var (
	iconRows    = icon("rows")     // 上下分屏（左右分屏用 iconColumns）
	iconArrowUp = icon("arrow-up") // 历史里"未推送"：小字号下没有顶上那一横才不像"不"
)

// Git 栏分组标题、贮藏、终端分屏、侧栏分段用的图标。
var (
	iconMinus       = icon("minus")
	iconCircleX     = icon("x-circle")        // 中止合并 / 变基
	iconCollapseAll = icon("stack-minus")     // 收起所有
	iconLocate      = icon("crosshair")       // 定位当前文件
	iconExpandAll   = icon("stack-plus")      // 展开所有
	iconStashApply  = icon("arrow-line-down") // 应用贮藏：放到工作区，贮藏留着
	iconStashPop    = icon("box-arrow-up")    // 弹出贮藏
	iconPaneClose   = icon("x-square")        // 关闭这一格分屏
	iconWorktree    = icon("git-fork")        // Git 工作树
	iconOutline     = icon("tree-view")       // 侧栏的大纲
	iconSubmodule   = icon("folders")
	iconFiles       = icon("files")
	iconLink        = icon("link")           // 文件树里的符号链接
	iconRebase      = icon("arrows-down-up") // 交互式变基向导（排顺序）
)

// 界面打磨：对话框头、复制、提交图、Diff 的局部 / 全文、搜索的包含 / 排除。
var (
	iconInfo      = icon("info")                    // 对话框：提示
	iconQuestion  = icon("question")                // 对话框：确认
	iconCopy      = icon("copy")                    // 复制全部
	iconCloseAll  = icon("x-square")                // copy-x：打开的编辑器的关闭全部
	iconFold      = icon("arrows-in-line-vertical") // 只看改动附近
	iconFilter    = icon("funnel-simple")           // 包含 / 排除的文件
	iconSearchDoc = icon("file-search")             // 搜索编辑器
	iconPlay      = icon("play")                    // 运行任务
)

// 面板里的动作按钮改成只有图标（#56）：冲突条、进行中横幅、提交框的 amend 开关。
var (
	iconAmend       = icon("pencil-simple-line") // 修改上一次提交
	iconTakeOurs    = icon("arrow-line-up")      // 用我的
	iconTakeTheirs  = icon("arrow-line-down")    // 用他们的
	iconTakeBoth    = icon("arrows-merge")       // 两个都要
	iconChevronsUp  = icon("caret-double-up")    // 全部用我的
	iconChevronsDn  = icon("caret-double-down")  // 全部用他们的
	iconSkipForward = icon("skip-forward")       // 跳过
)
