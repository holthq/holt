package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
)

// 打字时不在主线程上每个键都整篇重算（issue #11）：开着粘性滚动、文件有改动块，连敲 N 个键，
// 大纲只在停手 StickyPause 后解析一次（StickySlow 改成 0，当它解析得慢）；改动块一直用同一个 DiffState 增量算，不从头整篇 diff。
// 停手后粘性滚动的标题、改动块都和从头算的一样。用计数不用计时（CI 负载下计时不稳）。
func TestTypingNoFullRecompute(t *testing.T) {
	keep := docs.StickySlow
	docs.StickySlow = 0
	t.Cleanup(func() { docs.StickySlow = keep })

	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := range 40 {
		fmt.Fprintf(&b, "func F%d() {\n", i)
		for range 40 {
			b.WriteString("\tprintln(1)\n")
		}
		b.WriteString("}\n\n")
	}
	src := b.String()
	repo := testutil.NewRepo(t, "typing")
	repo.Write("big.go", src)
	repo.Commit("base")
	repo.Write("big.go", strings.Replace(src, "func F3() {\n\tprintln(1)", "func F3() {\n\tprintln(3)", 1))
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	d := a.OpenFile(a.Repos[0], "big.go", true)
	wait("读好", func() bool { return d.Buf != nil && d.Base != nil && !d.Loading })
	a.FocusDoc = true
	for range 3 {
		tt.Frame()
	}
	if d.St.OnGutterClick == nil {
		t.Fatal("有改动块时该接管装订线的点击")
	}
	st := &d.St
	// 光标放进 F20 里面、F20 那段滚到中间：粘性滚动吸着 func F20() {
	line := 2 + 20*43 + 10
	st.GoTo(editor.Pos{Line: line, Col: 1})
	st.SetScrollY(st.LineTop(line - 5))
	tt.Frame()
	tt.Frame()
	if !slices.Contains(tt.Texts(), "func F20() {") {
		t.Fatalf("该吸着 func F20() {：%q", tt.Texts())
	}

	parses, rebuilds := docs.OutlineParses(), app.HunkRebuilds()
	const n = 20
	for range n {
		tt.Type("x")
		tt.Frame()
	}
	if !strings.Contains(d.Buf.Line(line), strings.Repeat("x", n)) {
		t.Fatalf("没打进去：%q", d.Buf.Line(line))
	}
	if got := docs.OutlineParses() - parses; got > 1 {
		t.Errorf("连敲 %d 个键大纲解析了 %d 次，应在停手前不解析（至多 1 次）", n, got)
	}
	if got := app.HunkRebuilds() - rebuilds; got != 0 {
		t.Errorf("连敲 %d 个键改动块从头算了 %d 次，应一直增量", n, got)
	}

	// 停手：解析一次，吸着的标题照旧
	before := docs.OutlineParses()
	tt.Advance(docs.StickyPause + 50e6)
	tt.Frame()
	tt.Frame()
	if got := docs.OutlineParses() - before; got != 1 {
		t.Errorf("停手后该解析一次，解析了 %d 次", got)
	}
	if !slices.Contains(tt.Texts(), "func F20() {") {
		t.Errorf("停手后该吸着 func F20() {：%q", tt.Texts())
	}

	// 增量算的块和从头算的一样：F3 里改的一处 + 这里打字的一处
	fresh := (&editor.DiffState{Old: d.Base, New: d.Buf}).Hunks()
	if got := app.DocHunks(d); !slices.Equal(got, fresh) || len(got) != 2 {
		t.Errorf("块 %+v，从头算是 %+v", got, fresh)
	}
	d.St.OnGutterClick(line)
	tt.Frame()
	tt.Frame()
	if !shown(tt, "第 2 / 2 处改动") {
		t.Errorf("点打字那一行该开第 2 块的卡：%q", tt.Texts())
	}
}
