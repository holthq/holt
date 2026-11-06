package ui

import (
	"os"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 设置 → 快捷键：搜到命令面板，选中点“改…”开录键卡片；按 ⌘K 提示和“运行 git 命令…”冲突，回车写进 keybindings.json，
// 行上换成 ⌘K、有“自定义”，git.command 那行写着被谁占了；录着的时候菜单栏不带键。再录 ⌫ 是去掉、Esc 不改、
// 菜单栏“编辑”的键（⌘C 作为命令送来）也录得到；“重置”回到默认。设置的搜索框里也搜得到命令。
func TestKeyRecorder(t *testing.T) {
	r0 := testutil.NewRepo(t, "keys")
	r0.Write("a.txt", "a\n")
	r0.Commit("初始提交")
	a, _, _ := newUI(t, testutil.Workspace(t, r0.Root), "")
	p := a.KeybindingsPath()
	t.Cleanup(func() {
		os.Remove(p)
		a.LoadKeybindings()
	})
	a.LoadKeybindings()
	file := func() string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	st := wui.NewTester(SettingsView(a), 760, 520)
	st.Frame()
	mustClick(t, st, "快捷键")
	if !st.HasText("命令面板…") || !st.HasText("⇧⌘P") {
		t.Fatalf("快捷键页：%q", st.Texts())
	}
	mustClick(t, st, "搜索快捷键")
	st.Type("palette.commands")
	st.Frame()
	if st.HasText("快速打开…") || !st.HasText("命令面板…") {
		t.Fatalf("搜 palette.commands：%q", st.Texts())
	}
	mustClick(t, st, "命令面板…")
	mustClick(t, st, "改…")
	if !st.HasText("按下新的快捷键") || !st.HasText("等你按…") || !app.KeyRecording {
		t.Fatalf("录键卡片没开（录着 %v）：%q", app.KeyRecording, st.Texts())
	}
	st.Key(wui.Cmd, wui.KeyK)
	st.Frame()
	if !st.HasText("⌘K") || !st.HasText("⌘K — 已被 运行 git 命令… 用着，保存后它没有快捷键") {
		t.Fatalf("按了 ⌘K：%q", st.Texts())
	}
	st.Key(0, wui.KeyEnter)
	st.Frame()
	st.Frame()
	if st.HasText("等你按…") || st.HasText("按下新的快捷键") || app.KeyRecording {
		t.Fatalf("回车该保存并关掉（录着 %v）：%q", app.KeyRecording, st.Texts())
	}
	if !strings.Contains(file(), `{"command": "palette.commands", "key": "cmd+k"}`) {
		t.Fatalf("keybindings.json：%q", file())
	}
	if !st.HasText("⌘K") || !st.HasText("自定义") {
		t.Errorf("改了的行：%q", st.Texts())
	}
	if c := app.Find("git.command"); c.Key != 0 || c.Shadowed != "palette.commands" {
		t.Errorf("git.command 该被顶掉：%v %q", c.Key, c.Shadowed)
	}

	// 再录：Esc 不改
	mustClick(t, st, "改…")
	st.Key(wui.Cmd, wui.KeyJ)
	st.Key(0, wui.KeyEscape)
	st.Frame()
	if st.HasText("等你按…") || app.Find("palette.commands").Key != wui.KeyK {
		t.Fatalf("Esc 该不改就关：%q", st.Texts())
	}
	// 菜单栏“编辑”的 ⌘C 是作为命令送来的
	mustClick(t, st, "改…")
	st.Command("copy")
	st.Frame()
	if !st.HasText("⌘C") || !st.HasText("没有冲突") {
		t.Fatalf("⌘C 作为命令送来：%q", st.Texts())
	}
	// ⌫ 是不要快捷键，点确定
	st.Key(0, wui.KeyBackspace)
	st.Frame()
	if !st.HasText("不要快捷键") || !st.HasText("保存后这条命令没有快捷键") {
		t.Fatalf("按了 ⌫：%q", st.Texts())
	}
	mustClick(t, st, "确定")
	st.Frame()
	if c := app.Find("palette.commands"); c.Key != 0 || !strings.Contains(file(), `{"command": "palette.commands", "key": ""}`) {
		t.Fatalf("去掉键：%v %q", c.Key, file())
	}
	if c := app.Find("git.command"); c.Key != wui.KeyK {
		t.Errorf("命令面板不占 ⌘K 了，git.command 该回来：%v", c.Key)
	}
	mustClick(t, st, "重置")
	if c := app.Find("palette.commands"); c.Key != wui.KeyP || c.Custom || file() != "[]\n" {
		t.Errorf("重置：%v custom=%v %q", c.Key, c.Custom, file())
	}

	// 设置的搜索框：搜 cmd+shift+p 出命令面板那行
	st2 := wui.NewTester(SettingsView(a), 760, 520)
	st2.Frame()
	mustClick(t, st2, "搜索设置")
	st2.Type("cmd+shift+p")
	st2.Frame()
	if !st2.HasText("快捷键") || !st2.HasText("命令面板…") || !st2.HasText("⇧⌘P") {
		t.Errorf("设置搜索里的快捷键：%q", st2.Texts())
	}
}

// 列表、输入框自己接的键跟着改：提交键换成 ⌥↩ 以后提交框里 ⌘↩ 不提交、⌥↩ 提交（临时仓库）。
func TestKeysFollowCustom(t *testing.T) {
	r0 := testutil.NewRepo(t, "follow")
	r0.Write("a.txt", "a\n")
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	p := a.KeybindingsPath()
	t.Cleanup(func() {
		os.Remove(p)
		a.LoadKeybindings()
	})
	r := a.Repos[0]
	r0.Write("a.txt", "b\n")
	r0.Git("add", "a.txt")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", func() bool { return idle(r)() && r.Status.Staged == 1 })
	a.ShowSidebar(workspace.ModeGit)
	a.OpenCommit(r)
	tt.Frame()
	mustClick(t, tt, "提交信息")
	tt.Type("改 a")
	a.SetKeybinding(app.Find("commit"), wui.Alt, wui.KeyEnter)
	tt.Frame()
	subject := func() string { return strings.TrimSpace(r0.Git("log", "-1", "--format=%s")) }
	tt.Key(wui.Cmd, wui.KeyEnter)
	tt.Frame()
	wait("队列", idle(r))
	if s := subject(); s != "初始提交" {
		t.Fatalf("改了提交键以后 ⌘↩ 不该提交：%q", s)
	}
	tt.Key(wui.Alt, wui.KeyEnter)
	wait("提交", func() bool { tt.Frame(); return idle(r)() && subject() != "初始提交" })
	if s := subject(); !strings.HasPrefix(s, "改 a") {
		t.Errorf("⌥↩ 提交的信息：%q", s)
	}
}
