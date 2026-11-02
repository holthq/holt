package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/version"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestOpenArgs：外面交进来的路径（命令行、第二个实例、open -a、拖到 Dock）——文件开标签，
// 列表里仓库的目录跳到那一段，不在列表里的仓库先问、点了"添加"才加（只记进 state.json，工作区文件不动），
// 不是仓库的目录、找不到的路径只给提示，别的工作区文件换过去（打不开的报错）。都在临时仓库上。
func TestOpenArgs(t *testing.T) {
	work := testutil.NewRepo(t, "work")
	work.Write("README.md", "# work\n")
	work.Write("src/a.go", "package src\n")
	work.Commit("初始提交")
	other := testutil.NewRepo(t, "other")
	other.Write("x.txt", "x\n")
	other.Commit("初始提交")
	otherRoot, _ := filepath.EvalSymlinks(other.Root) // git 报的根解开了符号链接（/var → /private/var）
	plain := t.TempDir()
	outside := filepath.Join(plain, "notes.txt")
	os.WriteFile(outside, []byte("外面的文件\n"), 0o644)
	ws := testutil.Workspace(t, work.Root)
	wsBefore, _ := os.ReadFile(ws)

	stateDir := termState(t, false) // 底栏关着，不起 shell
	a, tt, wait := newUI(t, ws, stateDir)
	a.RefreshAll(app.WhyOpen)
	wait("仓库状态", loaded(a))

	if got := app.Args([]string{"-psn_0_12345", "", "src", "/tmp/x"}); !slices.Equal(got, []string{"src", "/tmp/x"}) {
		t.Errorf("Args = %q", got)
	}

	// 相对路径按第二个实例的工作目录算
	a.OpenArgs([]string{"README.md"}, work.Root)
	tt.Frame()
	if d := a.CurrentDoc(); d == nil || d.Rel != "README.md" || d.Root != work.Root {
		t.Fatalf("README.md 没开成仓库里的标签：%+v", d)
	}
	a.OpenArgs([]string{outside}, "")
	tt.Frame()
	if d := a.CurrentDoc(); d == nil || d.Abs != outside {
		t.Fatalf("仓库外的文件没开：%+v", d)
	}

	// 列表里的仓库（子目录也算）：跳过去，不问
	a.SelectRepo(-1)
	a.OpenArgs([]string{filepath.Join(work.Root, "src")}, "")
	if a.Current() == nil || a.Current().Root != work.Root || a.ConfirmOpen {
		t.Fatalf("work/src 应该跳到 work、不问：current=%v confirm=%v", a.Current(), a.ConfirmOpen)
	}

	// 不在列表里的仓库：先问；取消就不加
	a.OpenArg(other.Root)
	tt.Frame()
	if !a.ConfirmOpen || a.Confirm == nil || !strings.Contains(a.Confirm.Message, otherRoot) || !tt.HasText(i18n.T("添加仓库")) {
		t.Fatalf("应该问要不要加 other：%+v", a.Confirm)
	}
	mustClick(t, tt, i18n.T("取消"))
	if len(a.Repos) != 1 || len(a.St.Added) != 0 {
		t.Fatalf("取消了还是加了：%d %v", len(a.Repos), a.St.Added)
	}
	a.OpenArg(filepath.Join(other.Root, "x.txt")) // 文件：直接按外部文件开，不问
	tt.Frame()
	if a.ConfirmOpen || a.CurrentDoc() == nil || a.CurrentDoc().Abs != filepath.Join(other.Root, "x.txt") {
		t.Fatalf("other/x.txt：confirm=%v doc=%+v", a.ConfirmOpen, a.CurrentDoc())
	}
	a.OpenArg(other.Root)
	tt.Frame()
	mustClick(t, tt, i18n.T("添加"))
	wait("other 加进来、读完状态", func() bool { return len(a.Repos) == 2 && loaded(a)() })
	if a.Current().Root != otherRoot || !slices.Contains(a.St.Added, otherRoot) {
		t.Errorf("加完应该在 other、记进 added：%s %v", a.Current().Root, a.St.Added)
	}
	st, err := workspace.LoadState(stateDir)
	if s := st.Sessions[ws]; err != nil || s == nil || !slices.Contains(s.Added, otherRoot) {
		t.Errorf("state.json 里这个工作区的会话没记 other：%+v %v", s, err)
	}
	if after, _ := os.ReadFile(ws); string(after) != string(wsBefore) {
		t.Errorf("工作区文件被改了：%s", after)
	}
	a.OpenArg(other.Root) // 再来一次：已经在列表里，不问
	if a.ConfirmOpen {
		t.Error("已经在列表里的仓库又问了一遍")
	}

	// 不是仓库的目录：当普通文件夹，一样先问；加进来之后再给它或它的子目录就是跳过去
	os.MkdirAll(filepath.Join(plain, "sub"), 0o755)
	a.OpenArg(plain)
	tt.Frame()
	if !a.ConfirmOpen || a.Confirm == nil || !strings.Contains(a.Confirm.Message, plain) {
		t.Fatalf("应该问要不要加 %s：%+v", plain, a.Confirm)
	}
	mustClick(t, tt, i18n.T("添加"))
	if added := i18n.Tf("已添加 %s（不是 Git 仓库）", filepath.Base(plain)); a.Toast != added && !tt.HasText(added) {
		t.Errorf("加普通文件夹的提示：%q", a.Toast)
	}
	wait("普通文件夹读完状态", func() bool { return len(a.Repos) == 3 && loaded(a)() })
	if r := a.Current(); r.Root != plain || !r.Plain || r.Err != nil {
		t.Errorf("加完应该在 %s、是普通文件夹：%s plain=%v err=%v", plain, r.Root, r.Plain, r.Err)
	}
	a.SelectRepo(0)
	a.OpenArg(filepath.Join(plain, "sub"))
	if a.ConfirmOpen || a.Current().Root != plain {
		t.Errorf("普通文件夹的子目录应该跳过去、不问：confirm=%v current=%s", a.ConfirmOpen, a.Current().Root)
	}

	// 只给提示的
	for _, c := range []struct{ path, want string }{
		{filepath.Join(plain, "没有这个"), i18n.Tf("找不到 %s", filepath.Join(plain, "没有这个"))},
		{ws, ""}, // 就是开着的这个工作区：什么都不说
	} {
		a.Toast = ""
		a.OpenArg(c.path)
		if a.Toast != c.want || a.ConfirmOpen {
			t.Errorf("%s：toast %q，要 %q（confirm=%v）", c.path, a.Toast, c.want, a.ConfirmOpen)
		}
	}
	otherWs := filepath.Join(plain, "别的.code-workspace")
	os.WriteFile(otherWs, []byte(`{"folders":[]}`), 0o644)
	a.OpenArg(otherWs) // 别的工作区文件：换过去；打不开（没有 folders）就报错，还在这个工作区
	tt.Frame()
	if !a.AlertOpen || !strings.Contains(a.AlertMsg, "没有 folders") || a.WsPath != ws || len(a.Docs) != 3 || !tt.HasText(i18n.T("打不开工作区")) {
		t.Errorf("打不开的工作区文件：%v %q，%d 个标签", a.AlertOpen, a.AlertMsg, len(a.Docs))
	}
}

// TestGreet：启动时状态栏报版本号——第一次用"欢迎"、换了版本"已更新"、同一版本不说；上次崩了先说崩溃日志。
func TestGreet(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := termState(t, false)
	a, tt, _ := newUI(t, ws, stateDir)
	a.Greet("0.1.0")
	welcome := i18n.Tf("欢迎使用 Grove %s", "0.1.0")
	if a.Toast != welcome {
		t.Errorf("第一次：%q", a.Toast)
	}
	tt.Frame() // 下一帧交给 mygo 的提示条（窗口底部，亮 4 秒）
	if a.Toast != "" || !tt.HasText(welcome) {
		t.Errorf("提示条上没有 %q：%q", welcome, tt.Texts())
	}
	if st, _ := workspace.LoadState(stateDir); st.AppVersion != "0.1.0" {
		t.Errorf("state.json 的 appVersion = %q", st.AppVersion)
	}
	a.Toast = ""
	a.Greet("0.1.0")
	if a.Toast != "" {
		t.Errorf("同一版本也说了：%q", a.Toast)
	}
	a.Greet("0.2.0")
	if a.Toast != i18n.Tf("已更新到 Grove %s", "0.2.0") {
		t.Errorf("换版本：%q", a.Toast)
	}
	a.LastCrash = "/x/crash-20261029-120000.log"
	a.Greet("0.2.0")
	if !strings.Contains(a.Toast, "crash-20261029-120000.log") {
		t.Errorf("上次崩了：%q", a.Toast)
	}
}

// TestAboutView：关于窗口有版本、mygo / Go 版本、日志目录；三个按钮都经 a.ShowPath（测试里只记不开），
// 没有日志目录、没有许可目录时按钮灰掉；没有上次的崩溃日志就没有那个按钮。无障碍检查也走一遍。
func TestAboutView(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, err := app.New(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	var shown []string
	a.ShowPath = func(p string) error { shown = append(shown, p); return nil }
	a.LogDir = t.TempDir()
	a.LastCrash = filepath.Join(a.LogDir, "crash-20261029-120000.log")
	licenses, _ := filepath.Abs("../../resources/licenses")

	tt := wui.NewTester(AboutView(a, licenses), 460, 600)
	tt.SetLocale(i18n.Locale())
	for _, s := range []string{"Grove", i18n.Tf("版本 %s", version.App), version.Mygo(), a.LogDir, "mygo", "purego", "SIL OFL 1.1"} {
		if !tt.HasText(s) {
			t.Errorf("关于窗口里没有 %q：%q", s, tt.Texts())
		}
	}
	accessCheck(t, tt, "关于窗口")
	mustClick(t, tt, i18n.T("打开日志目录"))
	mustClick(t, tt, i18n.T("上次的崩溃日志"))
	mustClick(t, tt, i18n.T("查看许可证全文"))
	if !slices.Equal(shown, []string{a.LogDir, a.LastCrash, licenses}) {
		t.Errorf("打开的是 %q", shown)
	}
	for _, f := range []string{"README.txt", "mygo.txt", "ghostty.txt", "go.txt", "go-text-typesetting.txt", "purego.txt", "regexp2.txt", "mermaid.txt", "mathjax.txt", "jetbrains-mono-nerd-font.txt"} {
		if _, err := os.Stat(filepath.Join(licenses, f)); err != nil {
			t.Errorf("许可目录里少了 %s", f)
		}
	}

	// 测试里（go run 也一样）没有日志目录和许可目录：按钮灰掉，点了不开
	a.LogDir, a.LastCrash, shown = "", "", nil
	tt2 := wui.NewTester(AboutView(a, ""), 460, 600)
	tt2.SetLocale(i18n.Locale())
	tt2.Click(i18n.T("打开日志目录"))
	tt2.Click(i18n.T("查看许可证全文"))
	tt2.Frame()
	if len(shown) != 0 || tt2.HasText(i18n.T("上次的崩溃日志")) {
		t.Errorf("灰掉的按钮也开了：%q", shown)
	}

	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" {
		a.LogDir = "/Users/me/Library/Logs/Grove"
		tt.SetScale(2)
		writeShot(t, tt, dir, "about.png")
	}
}
