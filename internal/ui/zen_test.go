package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 禅模式：⌃⌘Z 进，三个面板全关、编辑器组在中间（两边留白）；state.json 里记的还是进之前的；再按一下、
// 或 1 s 内连按两下没人接的 Esc 出，面板照原样恢复；隔得久的两下不出，编辑器自己要的 Esc（收起选区）不算；窗口窄了不留白。
func TestZen(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	r := a.Repos[0]
	d := a.OpenFile(r, "a.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	a.St.RightBar = false // 进之前的样子：侧栏、底栏开着，右栏关着
	tt.Frame()
	tabX := func() float32 {
		rc, ok := tt.Find("标签 a.txt")
		if !ok {
			t.Fatalf("找不到标签：%q", tt.Texts())
		}
		return rc.X
	}
	before := tabX()

	tt.Key(wui.Ctrl|wui.Cmd, wui.KeyZ)
	tt.Frame()
	tt.Frame()
	if !a.Zen || a.St.Sidebar || a.St.RightBar || a.St.BottomBar {
		t.Fatalf("进禅模式：zen %v，侧栏 %v 右栏 %v 底栏 %v", a.Zen, a.St.Sidebar, a.St.RightBar, a.St.BottomBar)
	}
	if x := tabX(); x < (1440-app.ZenWidth)/2-1 {
		t.Errorf("编辑器组没居中：标签在 x=%v（进之前 %v）", x, before)
	}
	// 存下来的是进之前的
	a.SaveState()
	var saved workspace.State
	data, _ := os.ReadFile(filepath.Join(stateDir, workspace.StateFile))
	if err := json.Unmarshal(data, &saved); err != nil || !saved.Sidebar || saved.RightBar || !saved.BottomBar {
		t.Errorf("禅模式不该持久：存的 侧栏 %v 右栏 %v 底栏 %v（%v）", saved.Sidebar, saved.RightBar, saved.BottomBar, err)
	}
	// 窗口窄了不留白
	tt.SetSize(1040, 800)
	tt.Frame()
	tt.Frame()
	if x := tabX(); x > 40 {
		t.Errorf("窗口 1040 宽还留白：标签在 x=%v", x)
	}
	tt.SetSize(1440, 880)
	tt.Frame()

	// 再按一下出来，恢复原样
	a.Exec("view.zen")
	tt.Frame()
	if a.Zen || !a.St.Sidebar || a.St.RightBar || !a.St.BottomBar {
		t.Fatalf("出禅模式没恢复：zen %v，侧栏 %v 右栏 %v 底栏 %v", a.Zen, a.St.Sidebar, a.St.RightBar, a.St.BottomBar)
	}

	// Esc：编辑器里有选区时第一下是编辑器的（收起选区），不算；之后 1 s 内两下出
	a.Exec("view.zen")
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	d.St.Select(0, 3)
	tt.Frame()
	esc := func() {
		tt.Key(0, wui.KeyEscape)
		tt.Frame()
		tt.Frame()
	}
	esc()
	if d.St.SelectedText() != "" || !a.Zen {
		t.Fatalf("第一下 Esc 应收起选区、不出：选中 %q，zen %v", d.St.SelectedText(), a.Zen)
	}
	esc()
	if !a.Zen {
		t.Fatal("没东西取消的第一下就出了")
	}
	tt.Advance(300 * time.Millisecond)
	esc()
	if a.Zen || !a.St.Sidebar || !a.St.BottomBar {
		t.Fatalf("1 s 内两下 Esc 没出：zen %v", a.Zen)
	}

	// 隔了 1 s 多的两下不出
	a.Exec("view.zen")
	tt.Frame()
	tt.Frame()
	esc()
	tt.Advance(1100 * time.Millisecond)
	esc()
	if !a.Zen {
		t.Fatal("隔了 1.1 s 的两下 Esc 也出了")
	}
	tt.Advance(200 * time.Millisecond)
	esc()
	if a.Zen {
		t.Fatal("接着 0.2 s 再按一下应该出")
	}
}
