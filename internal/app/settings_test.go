package app

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

func TestPullArgs(t *testing.T) {
	for mode, want := range map[string]string{"": "pull --ff-only", "rebase": "pull --rebase", "merge": "pull --no-rebase", "乱写": "pull --ff-only"} {
		if got := strings.Join(PullArgs(mode), " "); got != want {
			t.Errorf("PullArgs(%q) = %q，要 %q", mode, got, want)
		}
	}
}

// 设置改了：交给窗口（OnSettings）、换语言、存进 state.json，下次启动读回来；⌘, 的命令打开设置窗口。
func TestSettingsApplyAndPersist(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, _ := newTestAppIn(t, ws, stateDir)
	var got []workspace.Settings
	a.OnSettings = func(s *workspace.Settings) { got = append(got, *s) }
	opened := 0
	a.OpenSettings = func() { opened++ }

	s := &a.St.Settings
	s.Theme, s.EditorSize, s.Indent, s.SoftWrap, s.TermDock, s.PullMode, s.Lang = "dark", 15, "4", true, "right", "rebase", "en"
	a.SettingsChanged()
	if len(got) != 1 || got[0].Theme != "dark" {
		t.Fatalf("OnSettings 收到 %+v", got)
	}
	if i18n.Lang() != "en" || i18n.T("设置") != "Settings" {
		t.Fatalf("语言没换：%q", i18n.Lang())
	}
	if f := a.EditorFont(); f.Size != 15 {
		t.Fatalf("编辑器字号 %v", f.Size)
	}
	if a.Exec("app.settings"); opened != 1 {
		t.Fatalf("app.settings 没打开设置窗口（%d）", opened)
	}

	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.Settings, *s) {
		t.Fatalf("存下的设置 %+v，要 %+v", st.Settings, *s)
	}
	i18n.Set("")
	b, _ := newTestAppIn(t, ws, stateDir)
	b.ApplySettings()
	if !reflect.DeepEqual(b.St.Settings, *s) || i18n.Lang() != "en" {
		t.Fatalf("重启后设置 %+v 语言 %q", b.St.Settings, i18n.Lang())
	}
}

// 终端的字体和配色：没设跟编辑器 / 跟外观，设了用设的；shell 和回滚大小进新终端的选项。
func TestTermSettings(t *testing.T) {
	a := &App{}
	a.St.Settings.EditorFont = "Menlo"
	if f := a.TermFont(); f.Family != "Menlo" || f.Size != 12.5 {
		t.Fatalf("默认终端字体 %+v", f)
	}
	if l, d := a.TermTheme(); l != termLight || d != termDark || l.Background != wui.Hex("#ffffff") || d.Background != CardDark {
		t.Fatal("默认终端配色应跟着外观，底色和卡片一样（浅色白、深色 #202023）")
	}
	s := &a.St.Settings
	s.TermFont, s.TermSize, s.TermTheme, s.Shell, s.Scrollback = "Monaco", 14, "dark", "/bin/zsh", 50
	if f := a.TermFont(); f.Family != "Monaco" || f.Size != 14 {
		t.Fatalf("终端字体 %+v", f)
	}
	if l, d := a.TermTheme(); l == nil || l != d {
		t.Fatal("深色终端配色应浅色、深色外观都用深色")
	}
	var o terminal.Options
	a.termOptions(&o, "")
	if !slices.Equal(o.Command, []string{"/bin/zsh", "-l"}) || o.Scrollback != 50<<20 || o.Font.Family != "Monaco" {
		t.Fatalf("新终端选项 %+v", o)
	}
	if !o.NoBlink {
		t.Error("终端光标默认不该闪")
	}
	s.TermBlink = true
	a.termOptions(&o, "")
	if o.NoBlink {
		t.Error("设置里打开了光标闪烁")
	}
	// ⌥ 作为 Meta 键、选中即复制默认关，设置里打开后交给新终端
	if o.OptionAsAlt || o.CopyOnSelect != terminal.CopyOnSelectNone {
		t.Errorf("⌥ 作 Meta、选中即复制默认应关：%v %v", o.OptionAsAlt, o.CopyOnSelect)
	}
	s.TermOptionAsAlt, s.TermCopyOnSelect = true, true
	a.termOptions(&o, "")
	if !o.OptionAsAlt || o.CopyOnSelect != terminal.CopyOnSelectClipboard {
		t.Errorf("设置里打开了 ⌥ 作 Meta、选中即复制：%v %v", o.OptionAsAlt, o.CopyOnSelect)
	}
	// Ghostty 的主题按名字用；找不到的名字（主题文件没了）跟着外观。
	names := terminal.GhosttyThemes()
	if len(names) == 0 {
		t.Fatal("没有 Ghostty 主题")
	}
	s.TermTheme = names[0]
	if l, d := a.TermTheme(); l == nil || l != d {
		t.Fatalf("Ghostty 主题 %q 没用上", names[0])
	}
	s.TermTheme = "没有这个主题"
	if l, d := a.TermTheme(); l != termLight || d != termDark {
		t.Fatal("找不到的主题应跟着外观")
	}
}

// 提交用设置里的作者（名字、邮箱都填了才用），提交后推送打开时提交完马上推到上游；默认两样都不做。
func TestCommitAuthorAndAutoPush(t *testing.T) {
	r0 := testutil.NewRepo(t, "work")
	r0.Write("a.txt", "一\n")
	r0.Commit("初始提交")
	remote := filepath.Join(t.TempDir(), "remote.git")
	r0.Git("init", "-q", "--bare", remote)
	r0.Git("remote", "add", "origin", remote)
	r0.Git("push", "-q", "-u", "origin", "main")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	commit := func(text, msg string) {
		t.Helper()
		r0.Write("a.txt", text)
		r0.Git("add", "a.txt")
		a.RefreshRepo(r, WhyManual)
		wait("读状态", idle(r))
		r.CommitMsg = msg
		a.Commit(r)
		wait("提交", func() bool { return idle(r)() && r.CommitMsg == "" })
		wait("推送（如果有）", idle(r))
	}

	commit("二\n", "默认")
	if got := testutil.GitOut(t, r0.Root, "log", "-1", "--format=%an <%ae>"); got != "测试 <t@example.com>" {
		t.Fatalf("默认作者 %q", got)
	}
	if testutil.GitOut(t, remote, "rev-parse", "main") == testutil.GitOut(t, r0.Root, "rev-parse", "HEAD") {
		t.Fatal("默认不该推送")
	}

	s := &a.St.Settings
	s.AuthorName, s.AutoPush = "只有名字", true
	commit("三\n", "只填了名字")
	if got := testutil.GitOut(t, r0.Root, "log", "-1", "--format=%an"); got != "测试" {
		t.Fatalf("只填名字不该换作者：%q", got)
	}
	s.AuthorEmail = "other@example.com"
	commit("四\n", "换作者并推送")
	if got := testutil.GitOut(t, r0.Root, "log", "-1", "--format=%an <%ae>|%cn"); got != "只有名字 <other@example.com>|测试" {
		t.Fatalf("作者 / 提交者 %q", got)
	}
	wait("推到远程", func() bool {
		return testutil.GitOut(t, remote, "rev-parse", "main") == testutil.GitOut(t, r0.Root, "rev-parse", "HEAD")
	})
}

// 自动保存：默认不保存；"停手后"在最后一次改动 AutoSaveDelay 之后写盘；"切走时"在窗口切走时写盘；
// 磁盘上被别处改过的不自动覆盖。
func TestAutoSave(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	d := a.OpenFile(r, "README.md", true)
	wait("读文件", func() bool { return !d.Loading })
	disk := func() string { b, _ := os.ReadFile(filepath.Join(root, "README.md")); return string(b) }

	d.Buf.Set("改一\n")
	a.AutoSaveTick()
	a.OnBlur()
	time.Sleep(AutoSaveDelay + 100*time.Millisecond)
	a.AutoSaveTick()
	if disk() == "改一\n" {
		t.Fatal("默认不该自动保存")
	}

	a.St.Settings.AutoSave = "delay"
	a.AutoSaveTick()
	if disk() == "改一\n" {
		t.Fatal("还没停手就保存了")
	}
	d.Buf.Set("改二\n") // 又改了：重新计时
	a.AutoSaveTick()
	start := time.Now()
	wait("停手后自动保存", func() bool { a.AutoSaveTick(); return disk() == "改二\n" })
	if el := time.Since(start); el < AutoSaveDelay-50*time.Millisecond {
		t.Fatalf("%v 就保存了", el)
	}
	if d.Modified() || a.AlertOpen || a.Toast != "" {
		t.Fatalf("自动保存后 modified=%v alert=%v toast=%q", d.Modified(), a.AlertOpen, a.Toast)
	}

	a.St.Settings.AutoSave = "blur"
	d.Buf.Set("改三\n")
	a.AutoSaveTick()
	time.Sleep(AutoSaveDelay + 100*time.Millisecond)
	a.AutoSaveTick()
	if disk() != "改二\n" {
		t.Fatal("切走时保存的设置下不该按时间保存")
	}
	a.OnBlur()
	if disk() != "改三\n" || d.Modified() {
		t.Fatal("窗口切走时没保存")
	}

	// 磁盘上被别处改了：不自动覆盖
	d.Buf.Set("改四\n")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("外面改的\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.RefreshRepo(r, WhyWatchFiles)
	wait("发现磁盘上改了", func() bool { return d.NeedsOverwrite() })
	a.OnBlur()
	if disk() != "外面改的\n" {
		t.Fatal("磁盘上被改过的文件不该自动覆盖")
	}
}
