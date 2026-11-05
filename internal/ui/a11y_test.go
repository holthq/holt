package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unsafe"

	wui "github.com/egoist/mygo/ui"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 无障碍标签的自动检查（AGENTS 第 6 条）：画出来的每个按钮都要有读屏念得出来的名字——
// Label、Tooltip，或者按钮里的文字。只有图标的按钮两样都没给就会念成"按钮"，或者干脆读不到。
//
// mygo 的 Tester 不公开这些，这里用 reflect 读它没导出的两样东西（mygo 锁了版本；字段名、编号变了
// field 会直接 Fatal，TestAccessProbe 核对编号，不会悄悄什么都不查）：
//   - 元素树（engine.c.root 和浮层 overlay）：只 Clicked() 的 Box 不可聚焦，mygo 不把它当按钮，没有 Label 时
//     根本不进无障碍树，所以要在元素树里找"可点、里面只有图标、没 Label 没 Tooltip"的元素；
//   - 无障碍树：引擎收到 AccessibilityOn（有读屏的时候）才建，headless 宿主存在 h.access 里；打开开关（rt.access）
//     画一帧再读，查按钮、标签、开关、输入框这些角色有没有名字。

// accNode 是无障碍树里的一个元素。
type accNode struct {
	Role               int
	Label, Desc, Place string
	Parent             int
	States             uint64
	X, Y, W, H         float64
}

// mygo internal/platform 里 AccessRole 的编号（RoleGroup = 0 起的 iota）。roleButton 由 accessCheck 先用一个探针核对。
const (
	roleButton       = 2
	roleLink         = 3
	roleCheckBox     = 4
	roleRadio        = 5
	roleSwitch       = 6
	roleTextField    = 9
	rolePopUpButton  = 16
	roleTab          = 18
	roleMenuButton   = 28
	roleToggleButton = 31
	roleComboBox     = 32
	roleDisclosure   = 33
)

// namedRoles 是读屏要念名字才知道是干什么的角色。
var namedRoles = map[int]string{
	roleButton: "按钮", roleLink: "链接", roleCheckBox: "复选框", roleRadio: "单选", roleSwitch: "开关",
	rolePopUpButton: "弹出按钮", roleTab: "标签", roleMenuButton: "菜单按钮", roleToggleButton: "切换按钮", roleDisclosure: "展开按钮",
	roleTextField: "输入框", roleComboBox: "组合框",
}

// field 取 v 里名叫 name 的字段（没导出的也行），没有就 Fatal。
func field(t *testing.T, v reflect.Value, name string) reflect.Value {
	t.Helper()
	f := v.FieldByName(name)
	if !f.IsValid() {
		t.Fatalf("mygo 的 %s 没有字段 %s 了：无障碍检查要跟着改", v.Type(), name)
	}
	return f
}

// accessTree 打开 tt 的无障碍树、画一帧，返回这一帧的元素。
func accessTree(t *testing.T, tt *wui.Tester) []accNode {
	t.Helper()
	tv := reflect.ValueOf(tt).Elem()
	on := field(t, field(t, tv, "rt").Elem(), "access")
	if on.Kind() != reflect.Bool {
		t.Fatalf("mygo 的 engine.access 不是 bool 了（%s）", on.Kind())
	}
	reflect.NewAt(on.Type(), unsafe.Pointer(on.UnsafeAddr())).Elem().SetBool(true)
	tt.Frame()
	tree := field(t, field(t, tv, "h").Elem(), "access")
	if tree.IsNil() {
		t.Fatal("打开无障碍以后 headless 还是没有树")
	}
	nodes := field(t, tree.Elem(), "Nodes")
	out := make([]accNode, nodes.Len())
	for i := range out {
		n := nodes.Index(i)
		b := field(t, n, "Bounds")
		out[i] = accNode{
			Role: int(field(t, n, "Role").Uint()), Label: field(t, n, "Label").String(),
			Desc: field(t, n, "Description").String(), Place: field(t, n, "Placeholder").String(),
			Parent: int(field(t, n, "Parent").Int()), States: field(t, n, "States").Uint(),
			X: field(t, b, "X").Float(), Y: field(t, b, "Y").Float(), W: field(t, b, "W").Float(), H: field(t, b, "H").Float(),
		}
	}
	return out
}

// unnamed 列出 nodes 里没有名字的按钮（和输入框），每条带上它在哪、外面几层的名字，方便找到代码。
func unnamed(nodes []accNode) []string {
	var bad []string
	for _, n := range nodes {
		kind, ok := namedRoles[n.Role]
		if !ok || strings.TrimSpace(n.Label) != "" || strings.TrimSpace(n.Desc) != "" {
			continue
		}
		if (n.Role == roleTextField || n.Role == roleComboBox) && n.Place != "" {
			continue
		}
		var path []string
		for p := n.Parent; p >= 0 && len(path) < 3; p = nodes[p].Parent {
			if l := nodes[p].Label; l != "" {
				path = append(path, l)
			}
		}
		bad = append(bad, fmt.Sprintf("%s @(%.0f,%.0f %.0f×%.0f) 在 %q 里", kind, n.X, n.Y, n.W, n.H, path))
	}
	return bad
}

// mygo ui/element.go 里的编号：flags 的位和元素的种类。
const (
	flagClickable = 1 << 0
	flagInvisible = 1 << 18
	kindText      = 1
	kindIcon      = 4
)

// iconOnly 列出 tt 这一帧元素树里可点、里面只有图标、没有 Label / Tooltip 的元素。
func iconOnly(t *testing.T, tt *wui.Tester) []string {
	t.Helper()
	c := field(t, field(t, reflect.ValueOf(tt).Elem(), "rt").Elem(), "c")
	// texts 是元素和里面的文字（mygo innerText 的算法），icon 说里面有没有图标。
	var texts func(n reflect.Value) (string, bool)
	texts = func(n reflect.Value) (string, bool) {
		e := n.Elem()
		if field(t, e, "hidden").Bool() || field(t, e, "flags").Uint()&flagInvisible != 0 {
			return "", false
		}
		switch field(t, e, "kind").Uint() {
		case kindText:
			return field(t, e, "text").String(), false
		case kindIcon:
			return "", true
		}
		var b strings.Builder
		icon := false
		for ch := field(t, e, "first"); !ch.IsNil(); ch = field(t, ch.Elem(), "next") {
			s, i := texts(ch)
			b.WriteString(s)
			icon = icon || i
		}
		return b.String(), icon
	}
	// name 是元素自己的名字：Label、Tooltip（mygo 记成 description），或里面的文字。
	name := func(n reflect.Value) string {
		e := n.Elem()
		if l := field(t, e, "label").String(); l != "" {
			return l
		}
		if d := field(t, e, "description").String(); d != "" {
			return d
		}
		s, _ := texts(n)
		return s
	}
	var bad []string
	var walk func(n reflect.Value)
	walk = func(n reflect.Value) {
		for ; !n.IsNil(); n = field(t, n.Elem(), "next") {
			e := n.Elem()
			if field(t, e, "hidden").Bool() || field(t, e, "flags").Uint()&flagInvisible != 0 {
				continue
			}
			if field(t, e, "flags").Uint()&flagClickable != 0 {
				if s, icon := texts(n); icon && strings.TrimSpace(s) == "" && field(t, e, "label").String() == "" && field(t, e, "description").String() == "" {
					var path []string
					for p := field(t, e, "parent"); !p.IsNil() && len(path) < 2; p = field(t, p.Elem(), "parent") {
						if s := strings.TrimSpace(name(p)); s != "" {
							path = append(path, s)
						}
					}
					bad = append(bad, fmt.Sprintf("图标按钮 @(%.0f,%.0f) 在 %q 里", field(t, e, "x").Float(), field(t, e, "y").Float(), path))
				}
			}
			walk(field(t, e, "first"))
		}
	}
	walk(field(t, c, "root"))
	walk(field(t, c, "overlay"))
	return bad
}

// accessCheck 检查 tt 当前这一帧：what 说明是哪个界面状态，出错时带上。
func accessCheck(t *testing.T, tt *wui.Tester, what string) {
	t.Helper()
	nodes := accessTree(t, tt)
	if len(nodes) == 0 {
		t.Fatalf("%s：无障碍树是空的", what)
	}
	bad := append(unnamed(nodes), iconOnly(t, tt)...)
	if len(bad) > 0 {
		t.Errorf("%s：%d 个没有名字的元素（给它 Label 或 Tooltip）：\n  %s", what, len(bad), strings.Join(bad, "\n  "))
	}
}

// 先核对读树的办法本身：一个有字的按钮、一个只有 Tooltip 的图标按钮、一个什么都没有的图标按钮。
func TestAccessProbe(t *testing.T) {
	view := func(c *wui.Context) {
		wui.Column(c).Children(func() {
			wui.Button(c, "探针")
			iconButton(c, iconClose, "关闭探针", func() {})
			b := wui.Box(c).Padding(4).Cursor(wui.CursorPointer)
			b.Children(func() { wui.Icon(c, iconClose).FontSize(14) })
			b.Clicked()
		})
	}
	tt := wui.NewTester(view, 300, 200)
	nodes := accessTree(t, tt)
	var probe bool
	for _, n := range nodes {
		if n.Label == "探针" && n.Role == roleButton {
			probe = true
		}
	}
	if !probe {
		t.Fatalf("没找到角色 %d、名字\"探针\"的按钮：mygo 的角色编号变了？%+v", roleButton, nodes)
	}
	if bad := unnamed(nodes); len(bad) != 0 {
		t.Fatalf("探针的无障碍树里不该有没名字的按钮（只点得到的 Box 不进树）：%q", bad)
	}
	if bad := iconOnly(t, tt); len(bad) != 1 {
		t.Fatalf("探针里应该正好一个没名字的图标按钮，查出 %q", bad)
	}
}

// TestAccessLabels 把主窗口和设置窗口走一遍常见的样子，每一步都查一遍没有名字的按钮：
// 空的主区、文件树 + 编辑器 + Markdown 预览 + 历史、终端（两个标签、分屏、查找条、退出条）、输出、
// Git 栏 + Diff + 贮藏、git 队列和全部抓取的进度、退出前的确认、展开的提交、所有仓库的历史、搜索结果、快速打开 / 命令面板、确认框和新建分支框、大纲和编码只读条、设置的每一页。
// 都在临时仓库里；终端用 /bin/sh。
func TestAccessLabels(t *testing.T) {
	work := testutil.NewRepo(t, "work")
	work.Write("README.md", "# 说明\n\n- 一\n- 二\n")
	work.Write("src/a.go", "package src\n\n// TODO 以后再说\nfunc A() {}\n")
	work.Commit("初始提交")
	work.Write("src/a.go", "package src\n\n// TODO 以后再说\nfunc A() int { return 1 }\n")
	work.Commit("A 返回 1")
	work.Write("README.md", "# 说明\n\n- 一\n- 二\n- 三\n")
	work.Git("stash", "-q")
	work.Write("README.md", "# 说明\n\n改了\n")
	work.Write("src/b.go", "package src\n")
	work.Git("add", "src/b.go")
	work.Write("notes.txt", "没跟踪\n")
	clean := testutil.NewRepo(t, "clean")
	clean.Write("a.txt", "一\n")
	clean.Commit("初始提交")

	a, tt, wait := termUI(t, testutil.Workspace(t, work.Root, clean.Root), termState(t, true))
	wait("两个仓库的状态", loaded(a))
	r := a.Repos[0]
	a.UseRepo(r)
	wait("历史", func() bool { return r.Hist.Loaded })
	accessCheck(t, tt, "刚打开（主区没有标签）")

	a.ShowSidebar(workspace.ModeFiles)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded })
	a.ToggleDir(r, "src")
	d := a.OpenFile(r, "README.md", true)
	wait("读 README.md", func() bool { return !d.Loading })
	a.Exec("view.markdownPreview")
	p := readyPane(t, a, tt)
	accessCheck(t, tt, "文件树 + 编辑器 + Markdown 预览 + 历史 + 终端")

	tab := a.CurTermTab(r)
	tt.Key(wui.Cmd, wui.KeyD)
	wait("左右分屏", func() bool { return len(tab.Panes()) == 2 })
	readyPane(t, a, tt)
	tt.Key(wui.Cmd, wui.KeyF)
	tt.Frame()
	a.Exec("term.new")
	readyPane(t, a, tt)
	accessCheck(t, tt, "终端：两个标签、分屏、查找条")
	a.Exec("term.prevTab")
	tt.Frame()
	typeLine(t, tt, p, "exit 3", "")
	wait("退出条", func() bool { return tt.HasText("重新启动") })
	accessCheck(t, tt, "终端退出条")
	a.BottomTab = app.BottomOutput
	a.Log(r, "$ git fetch", "完成", false)
	accessCheck(t, tt, "输出")

	a.Exec("view.git")
	ch := r.Status.Changes[0]
	dd := a.OpenDiff(r, ch, true)
	wait("diff", func() bool { return !dd.Loading })
	accessCheck(t, tt, "Git 栏 + Diff + 贮藏")

	slowOp(a, r)
	a.FetchAll() // 排在慢命令后面
	accessCheck(t, tt, "git 队列：队列按钮、全部抓取的进度和停止")
	a.RequestLeave("")
	accessCheck(t, tt, "退出前问在跑的 git 命令")
	a.LeaveAnswer(0)
	a.StopBatch()
	a.CancelOp(r)
	wait("取消", idle(r))

	mustClick(t, tt, "A 返回 1")
	wait("展开的提交", func() bool { return r.Hist.Expanded != "" && !r.Hist.FilesLoading })
	accessCheck(t, tt, "展开的提交")
	// 头部的图标开关是切换按钮（按下 = 开）
	for _, name := range []string{"搜索历史", "所有分支", "只看 agent 写的", "所有仓库"} {
		if !slices.ContainsFunc(accessTree(t, tt), func(n accNode) bool { return n.Role == roleToggleButton && n.Label == name }) {
			t.Errorf("历史头部没有叫 %q 的开关", name)
		}
	}
	a.SetHistAll(true)
	wait("所有仓库的历史", func() bool { return a.AllHistDone() && !a.AllHistLoading() })
	accessCheck(t, tt, "所有仓库的历史")

	a.Exec("view.search")
	a.Search.Query.Text = "TODO"
	a.SearchNow()
	wait("搜索结果", func() bool { text, _ := a.SearchSummary(); return !a.Search.Running && strings.Contains(text, "1") })
	accessCheck(t, tt, "搜索结果")

	a.OpenPalette("")
	tt.Frame()
	accessCheck(t, tt, "快速打开")
	a.OpenPalette(">")
	tt.Frame()
	accessCheck(t, tt, "命令面板")
	a.ClosePalette()

	a.Ask("丢弃改动？", "README.md 的改动会丢掉。", "丢弃", func() {})
	accessCheck(t, tt, "确认框")
	a.ConfirmOpen = false
	a.Exec("git.newBranch")
	accessCheck(t, tt, "新建分支框")
	a.Branch = app.BranchDialog{}

	// 侧栏的大纲、编不回原样的 GB18030 文件的只读条（Big5 能编辑，没有条了）
	a.UseRepo(r)
	md := a.OpenFile(r, "README.md", true)
	wait("读 README.md", func() bool { return md.Buf != nil })
	a.ShowSidebar(workspace.ModeOutline)
	wait("大纲", func() bool { return tt.HasText("说明") })
	accessCheck(t, tt, "大纲")
	gb, _ := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文"))
	os.WriteFile(filepath.Join(work.Root, "lossy.txt"), append(gb, 0x80, '\n'), 0o644)
	gd := a.OpenFile(r, "lossy.txt", true)
	wait("读 lossy.txt", func() bool {
		return gd.Buf != nil && tt.HasText(i18n.T("转成 UTF-8…")) && tt.HasText(i18n.T("换编码…"))
	})
	accessCheck(t, tt, "编码只读条")

	// 普通文件夹：标题栏的"无 Git"和灰掉的按钮、Git 栏的仓库头、历史的提示
	plain := t.TempDir()
	os.WriteFile(filepath.Join(plain, "a.txt"), []byte("一\n"), 0o644)
	if err := a.AddRepoDir(plain); err != nil {
		t.Fatal(err)
	}
	pr := a.Repos[len(a.Repos)-1]
	wait("普通文件夹", func() bool { return pr.Loaded && pr.Plain && !pr.Loading })
	a.UseRepo(pr)
	a.SetHistAll(false)
	a.Exec("view.git")
	accessCheck(t, tt, "普通文件夹 + Git 栏")
	if !tt.HasText(i18n.T("这个文件夹不是 Git 仓库")) || !tt.HasText(i18n.T("无 Git")) {
		t.Error("普通文件夹：历史没有提示、标题栏没有\"无 Git\"")
	}

	st := wui.NewTester(SettingsView(a), 600, 480)
	st.SetLocale(i18n.Locale())
	for i, page := range settingsPages {
		if i > 0 {
			mustClick(t, st, i18n.T(page))
		}
		accessCheck(t, st, "设置 · "+page)
	}
}
