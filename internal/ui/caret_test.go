package ui

import (
	"bytes"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
)

// still 是 550 ms 里画面一点没变（光标闪的话半秒内一定换过一次）。
func still(tt *wui.Tester) bool {
	a := bytes.Clone(tt.Image().Pix)
	tt.Advance(550 * time.Millisecond)
	return bytes.Equal(a, tt.Image().Pix)
}

// 代码编辑器停手 30 秒后光标不再闪，键盘还在它那里：打字照常进去、马上又闪；输入法也照常；点别处再
// 回来重新计时。
func TestCaretRestEditor(t *testing.T) {
	st := &editor.State{Buffer: editor.NewBuffer("hello\nworld\n"), Label: "代码"}
	var r docs.Rest
	first := true
	tt := wui.NewTester(func(c *wui.Context) {
		wui.Column(c).Fill().Children(func() {
			wui.Button(c, "别处")
			ed := restingEditor(c, "ed", st, &r, nil).Grow(1)
			if first {
				first = false
				ed.Focus()
			}
		})
	}, 600, 300)
	tt.Frame()
	tt.Frame()
	if !tt.Focused("代码") || !st.Focused() || still(tt) {
		t.Fatalf("刚拿到键盘应在闪: focused=%v", st.Focused())
	}
	tt.Advance(CaretRest)
	if !r.Resting || st.Focused() || !tt.Focused("代码") {
		t.Fatalf("30 秒后应歇着、键盘还在编辑器: resting=%v ed.focused=%v", r.Resting, st.Focused())
	}
	if !still(tt) || !still(tt) {
		t.Error("歇着的光标还在闪")
	}
	if st.CursorShape != editor.CursorBlock {
		t.Error("歇着时光标应是空心方块")
	}
	// 歇着时排的帧只有到点的：再等 10 秒也没有新帧
	tt.Advance(10 * time.Second)
	if tt.FramePending() {
		t.Error("歇着时还有帧要画")
	}

	// 打字：字进去了，马上醒过来接着闪
	tt.Type("x")
	tt.Key(0, wui.KeyRight)
	tt.Frame()
	if got := st.Buffer.String(); got != "xhello\nworld\n" {
		t.Fatalf("歇着时打的字 %q", got)
	}
	if r.Resting || !st.Focused() || st.CursorShape != editor.CursorBar || still(tt) {
		t.Fatalf("按键后应醒: resting=%v focused=%v", r.Resting, st.Focused())
	}

	// 输入法：歇着时组字、上屏都照常
	tt.Advance(CaretRest)
	if !r.Resting {
		t.Fatal("又过 30 秒应歇着")
	}
	tt.Compose("ni", 2)
	tt.Type("你")
	tt.Frame()
	if got := st.Buffer.Line(0); got != "xh你ello" {
		t.Errorf("歇着时输入法上屏 %q", got)
	}
	if r.Resting {
		t.Error("上屏后应醒")
	}

	// 键盘去了别处：不歇着、不抢键盘；回来重新计时
	tt.Advance(CaretRest)
	mustClick(t, tt, "别处")
	tt.Frame()
	if tt.Focused("代码") || r.Resting {
		t.Fatalf("点了别处键盘应离开编辑器: resting=%v", r.Resting)
	}
	tt.Advance(time.Minute)
	if tt.Focused("代码") {
		t.Fatal("歇着的编辑器把键盘抢回去了")
	}
	mustClick(t, tt, "代码")
	tt.Frame()
	if r.Resting || !st.Focused() || still(tt) {
		t.Error("点回编辑器应醒")
	}
}

// Diff 视图（并排和统一）停手 30 秒后光标不再闪，键盘还在原来那一侧：↑ 照常移光标、醒过来。
func TestCaretRestDiff(t *testing.T) {
	for _, layout := range []editor.DiffLayout{editor.DiffSplit, editor.DiffUnified} {
		d := &editor.DiffState{Old: editor.NewBuffer("a\nb\nc\nd\n"), New: editor.NewBuffer("a\nB\nc\nd\ne\n"), Layout: layout}
		var r docs.Rest
		tt := wui.NewTester(func(c *wui.Context) {
			wui.Column(c).Fill().Children(func() {
				wui.Button(c, "别处")
				restingDiff(c, "diff", d, &r).Grow(1)
			})
		}, 700, 300)
		tt.Frame()
		label := "New text" // mygo 的文案，测试里没换成中文
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("找不到 %q: %q", label, tt.Texts())
		}
		mustClick(t, tt, label)
		tt.Frame()
		if !tt.Focused(label) || still(tt) {
			t.Fatalf("%v: 点进新的一侧应在闪", layout)
		}
		tt.Advance(CaretRest)
		if !r.Resting || !tt.Focused(label) {
			t.Fatalf("%v: 30 秒后应歇着、键盘还在新的一侧: resting=%v", layout, r.Resting)
		}
		if !still(tt) || !still(tt) {
			t.Errorf("%v: 歇着的光标还在闪", layout)
		}
		if tt.HasText("跳到行") || tt.HasText("行号") {
			t.Errorf("%v: 借来的跳到行栏露出来了: %q", layout, tt.Texts())
		}
		before := d.CursorPos()
		tt.Key(0, wui.KeyUp)
		tt.Frame()
		if d.CursorPos().Line != before.Line-1 || r.Resting || still(tt) {
			t.Errorf("%v: ↑ 后光标 %v→%v、应醒: resting=%v", layout, before, d.CursorPos(), r.Resting)
		}
	}
}
