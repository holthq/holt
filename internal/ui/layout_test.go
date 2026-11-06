package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 三个区域和侧栏三种模式：快捷键、标题栏按钮都能切，界面跟着变。
func TestLayoutShortcuts(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))

	// 默认：侧栏（文件）、右栏、底栏都开着
	if !a.St.Sidebar || !a.St.RightBar || !a.St.BottomBar || a.St.SidebarMode != workspace.ModeFiles {
		t.Fatalf("默认布局不对: %+v", a.St)
	}
	for _, s := range []string{"筛选文件", "历史", "终端 · demo", "输出", "快速打开", "工作区 demo", "分支 main"} {
		mustText(t, tt, s)
	}

	for _, k := range []struct {
		mods wui.Modifiers
		key  wui.Key
		mode string
		text string
	}{
		{wui.Cmd | wui.Shift, wui.KeyG, workspace.ModeGit, commitPlaceholder()},
		{wui.Cmd | wui.Shift, wui.KeyF, workspace.ModeSearch, "在所有仓库里搜文字"},
		{wui.Cmd | wui.Shift, wui.KeyE, workspace.ModeFiles, "筛选文件"},
	} {
		tt.Key(k.mods, k.key)
		tt.Frame()
		if a.St.SidebarMode != k.mode || !tt.HasText(k.text) {
			t.Errorf("切到 %s 后 mode=%q，界面上没有 %q", k.mode, a.St.SidebarMode, k.text)
		}
	}
	// ⇧⌘F 不像 ⇧⌘E / ⇧⌘G 那样开关：已经在搜索页也只是把焦点给搜索框
	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	if !a.St.Sidebar || a.St.SidebarMode != workspace.ModeSearch || !tt.Focused("搜索文字") {
		t.Errorf("再按 ⇧⌘F 应留在搜索页并聚焦搜索框: mode=%q", a.St.SidebarMode)
	}
	// 分段控件也能切
	mustClick(t, tt, "Git")
	if a.St.SidebarMode != workspace.ModeGit {
		t.Errorf("点 Git 分段后 mode=%q", a.St.SidebarMode)
	}

	for _, k := range []struct {
		name string
		mods wui.Modifiers
		key  wui.Key
		on   func() bool
		gone string // 关掉后界面上不该有的
	}{
		{"侧栏", wui.Cmd, wui.KeyB, func() bool { return a.St.Sidebar }, commitPlaceholder()},
		{"底栏", wui.Cmd, wui.KeyJ, func() bool { return a.St.BottomBar }, "终端 · demo"},
		{"右栏", wui.Cmd | wui.Alt, wui.KeyB, func() bool { return a.St.RightBar }, app.Tip("关闭右栏", "view.right")},
	} {
		tt.Key(k.mods, k.key)
		tt.Frame()
		if k.on() || tt.HasText(k.gone) {
			t.Errorf("快捷键没关掉%s", k.name)
		}
		tt.Key(k.mods, k.key)
		tt.Frame()
		if !k.on() || !tt.HasText(k.gone) {
			t.Errorf("快捷键没重新打开%s", k.name)
		}
	}

	// 标题栏的三个开关按钮
	for _, b := range []struct {
		label string
		on    func() bool
	}{
		{app.Tip("侧栏", "view.sidebar"), func() bool { return a.St.Sidebar }},
		{app.Tip("右栏（历史）", "view.right"), func() bool { return a.St.RightBar }},
		{app.Tip("底栏（终端 / 输出）", "view.bottom"), func() bool { return a.St.BottomBar }},
	} {
		mustClick(t, tt, b.label)
		if b.on() {
			t.Errorf("点 %q 没关掉", b.label)
		}
		mustClick(t, tt, b.label)
		if !b.on() {
			t.Errorf("再点 %q 没打开", b.label)
		}
	}

	// 隐藏的侧栏用 ⇧⌘G 叫出来，直接是 Git 栏
	tt.Key(wui.Cmd, wui.KeyB)
	tt.Frame()
	tt.Key(wui.Cmd|wui.Shift, wui.KeyE)
	tt.Frame()
	if !a.St.Sidebar || a.St.SidebarMode != workspace.ModeFiles {
		t.Errorf("侧栏关着时 ⇧⌘E 应打开它并切到文件: %+v", a.St)
	}
}

// 旧版 state.json（面板表 / 四元数组）在新界面里打开：换算成三个区域，存一次就是新格式。
func TestStateMigrationUI(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	for _, c := range []struct {
		name, old                  string
		mode                       string
		right, bottom, commitShown bool
	}{
		{"面板表", `{"version":1,"panels":{"files":false,"changes":true,"history":false,"terminal":true},"unified":true}`,
			workspace.ModeGit, false, true, true},
		{"四元数组", `{"panels":[true,false,true,false]}`, workspace.ModeFiles, false, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, workspace.StateFile)
			if err := os.WriteFile(path, []byte(c.old), 0o644); err != nil {
				t.Fatal(err)
			}
			a, tt, wait := newUI(t, ws, dir)
			r := a.Repos[0]
			a.RefreshRepo(r, app.WhyManual)
			wait("git status", idle(r))
			if !a.St.Sidebar || a.St.SidebarMode != c.mode || a.St.RightBar != c.right || a.St.BottomBar != c.bottom {
				t.Fatalf("迁移后 %+v", a.St)
			}
			if tt.HasText(commitPlaceholder()) != c.commitShown || tt.HasText(app.Tip("关闭右栏", "view.right")) != c.right ||
				tt.HasText("终端 · demo") != c.bottom {
				t.Errorf("界面和迁移后的开关对不上: %q", tt.Texts())
			}
			if c.name == "面板表" && !a.St.Unified {
				t.Error("旧的统一视图设置丢了")
			}
			a.SaveState()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if _, ok := m["panels"]; ok || m["version"] != float64(workspace.Version) || m["sidebar"] != true {
				t.Errorf("存回去的不是新格式: %s", data)
			}
		})
	}
}
