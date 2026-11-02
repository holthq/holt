package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 临时仓库上走一遍侧栏文件树和主区编辑器：仓库下的文件树、目录展开收起、单击预览 / 回车固定、
// 筛选框敲字回车固定打开、编辑后 ⌘S 写盘、外部改动后 ⌘R 重读、冲突条、脏标签 ⌘W 的确认、
// 历史里的只读版本、⌘B 开关侧栏并记进 state.json。
func TestFilesAndEditor(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("# 指南\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	r := a.Repos[0]
	if a.St.SidebarMode != workspace.ModeFiles {
		t.Fatalf("侧栏默认应是文件: %q", a.St.SidebarMode)
	}
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("文件列表", func() bool { return idle(r)() && r.Files.Loaded })

	paths := func() []string {
		var out []string
		for _, tr := range a.TreeRows() {
			if !tr.Root && tr.Note == "" {
				out = append(out, tr.Path)
			}
		}
		return out
	}
	if got := paths(); !slices.Equal(got, []string{"docs", "a.txt", "README.md", "u.txt"}) {
		t.Fatalf("文件树的行不对: %v", got)
	}
	rows := a.TreeRows()
	if !rows[0].Root || rows[0].Repo != r || rows[2].Code != 'M' || rows[4].Code != '?' || !rows[1].Mark {
		t.Errorf("仓库根或状态标记不对: %+v", rows)
	}
	mustText(t, tt, "demo（仓库）")
	mustText(t, tt, "README.md")

	// 点目录展开，← 收起
	mustClick(t, tt, "docs")
	if got := paths(); !slices.Equal(got, []string{"docs", "docs/guide.md", "a.txt", "README.md", "u.txt"}) {
		t.Fatalf("点 docs 后没展开: %v", got)
	}
	tt.Key(0, wui.KeyLeft)
	tt.Frame()
	if len(paths()) != 4 {
		t.Errorf("← 没收起目录: %v", paths())
	}

	// 单击 u.txt → 主区开预览标签；再单击 README.md 顶掉它
	mustClick(t, tt, "u.txt")
	wait("u.txt 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == "u.txt" && d.Buf != nil })
	if a.Docs[0].Pinned || a.Docs[0].Buf.String() != "未跟踪\n" {
		t.Errorf("单击应打开预览标签: pinned=%v", a.Docs[0].Pinned)
	}
	if a.Docs[0].Base != nil {
		t.Error("未跟踪文件不该有 HEAD 版本")
	}
	mustClick(t, tt, "README.md")
	wait("README 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == "README.md" && d.Buf != nil })
	if len(a.Docs) != 1 {
		t.Errorf("预览标签应被顶掉，现在有 %d 个", len(a.Docs))
	}
	mustText(t, tt, "Markdown")          // 状态栏的语言
	mustText(t, tt, "位置 demo/README.md") // 面包屑

	// 侧栏底部的筛选框：输入后回车固定打开
	mustClick(t, tt, "筛选文件")
	tt.Type("a.tx")
	tt.Frame()
	if got := paths(); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("筛选结果不对: %v", got)
	}
	tt.Key(0, wui.KeyEnter)
	wait("a.txt 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == "a.txt" && d.Buf != nil })
	d := a.CurrentDoc()
	if !d.Pinned || len(a.Docs) != 2 {
		t.Errorf("回车应固定打开: pinned=%v docs=%d", d.Pinned, len(a.Docs))
	}
	if d.Base == nil || !strings.Contains(d.Base.String(), "第 20 行\n") {
		t.Error("应读到 HEAD 里的版本做对比基准")
	}

	// 编辑并 ⌘S
	mustClick(t, tt, "编辑 a.txt")
	d.St.Select(0, 0)
	tt.Type("新")
	tt.Frame()
	if !d.Modified() || !tt.HasText("未保存 · ⌘S") {
		t.Fatalf("输入后应标为未保存: modified=%v", d.Modified())
	}
	tt.Key(wui.Cmd, wui.KeyS)
	tt.Frame()
	data, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if !strings.HasPrefix(string(data), "新第 1 行\n") || d.Modified() {
		t.Fatalf("⌘S 没写盘: modified=%v 文件开头 %q", d.Modified(), string(data[:min(20, len(data))]))
	}
	wait("保存后重读状态", func() bool { return idle(r)() && r.Status.Unstaged == 1 })

	// 别处改了没改过的 README → ⌘R 后重读
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n\n别处改的\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readme := a.Docs[0]
	if readme.Rel != "README.md" {
		t.Fatalf("第一个标签应是 README: %q", readme.Rel)
	}
	tt.Key(wui.Cmd, wui.KeyR)
	wait("README 重读", func() bool { return strings.Contains(readme.Buf.String(), "别处改的") })

	// 这里也改了、磁盘又变了 → 冲突条；"保留我的"后 ⌘S 直接覆盖
	mustClick(t, tt, "标签 README.md")
	mustClick(t, tt, "编辑 README.md")
	readme.St.Select(0, 0)
	tt.Type("本地 ")
	tt.Frame()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n\n又改了\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tt.Key(wui.Cmd, wui.KeyR)
	wait("冲突条", func() bool { return readme.Conflict })
	if !tt.HasText("保留我的") || strings.Contains(readme.Buf.String(), "又改了") {
		t.Fatalf("冲突时不该覆盖本地改动: %q", tt.Texts())
	}
	mustClick(t, tt, "保留我的")
	tt.Key(wui.Cmd, wui.KeyS)
	tt.Frame()
	data, _ = os.ReadFile(filepath.Join(root, "README.md"))
	if string(data) != "本地 # demo\n\n别处改的\n" || readme.Conflict || a.ConfirmOpen {
		t.Errorf("保留我的之后 ⌘S 应直接覆盖: %q confirm=%v", data, a.ConfirmOpen)
	}

	// 脏标签 ⌘W → 确认框，选不保存
	mustClick(t, tt, "标签 a.txt")
	mustClick(t, tt, "编辑 a.txt")
	d.St.Select(0, 0)
	tt.Type("y")
	tt.Key(wui.Cmd, wui.KeyW)
	tt.Frame()
	if !a.CloseOpen || !tt.HasText("保存对 a.txt 的修改？") {
		t.Fatalf("关脏标签应先问: %q", tt.Texts())
	}
	mustClick(t, tt, "不保存")
	data, _ = os.ReadFile(filepath.Join(root, "a.txt"))
	if len(a.Docs) != 1 || a.Docs[0] != readme || strings.HasPrefix(string(data), "y") {
		t.Errorf("不保存应关掉标签且不写盘: docs=%d", len(a.Docs))
	}

	// 历史里的版本只读
	commits, err := git.Log(context.Background(), root, 0, 10, git.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	ci := slices.IndexFunc(commits, func(c git.Commit) bool { return c.Subject == "加 a.txt" })
	cfiles, err := git.CommitFiles(context.Background(), root, commits[ci])
	if err != nil || len(cfiles) != 1 {
		t.Fatalf("提交的文件: %v %v", cfiles, err)
	}
	v := a.OpenVersion(r, cfiles[0])
	wait("旧版本", func() bool { return v.Buf != nil || v.Info != "" })
	if v.Info != "" || !v.St.ReadOnly || strings.Contains(v.Buf.String(), "改过") || !strings.Contains(v.Buf.String(), "第 20 行\n") {
		t.Errorf("旧版本不对: info=%q readonly=%v", v.Info, v.St.ReadOnly)
	}
	tt.Frame()
	short := git.Short(commits[ci].Hash)
	if !tt.HasText("只读 · 提交 "+short) || !tt.HasText("标签 a.txt @ "+short) {
		t.Errorf("旧版本的标签和状态栏: %q", tt.Texts())
	}

	// ⌘B 关侧栏，记进 state.json
	tt.Key(wui.Cmd, wui.KeyB)
	tt.Frame()
	if a.St.Sidebar || tt.HasText("筛选文件") {
		t.Error("⌘B 没关掉侧栏")
	}
	st, err := workspace.LoadState(stateDir)
	if err != nil || st.Sidebar || !st.RightBar || !st.BottomBar {
		t.Errorf("state.json 里的区域开关不对 (%v): %+v", err, st)
	}
}
