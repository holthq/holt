package ui

import (
	"reflect"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// mygo ui/element.go 里 textStyle.set 的位：setFamily、setSize、setWeight 依次 1<<iota。
const setWeightBit = 1 << 2

// textWeights 返回 tt 这一帧元素树里文字正好是 s 的每个文字元素实际的字重：自己或最近的祖先设过的 FontWeight，
// 都没设是 0（默认字重）。mygo 不公开字重，和 iconOnly 一样用 reflect 读没导出的字段。
func textWeights(t *testing.T, tt *wui.Tester, s string) []int64 {
	t.Helper()
	c := field(t, field(t, reflect.ValueOf(tt).Elem(), "rt").Elem(), "c")
	weight := func(n reflect.Value) int64 {
		for ; !n.IsNil(); n = field(t, n.Elem(), "parent") {
			ts := field(t, n.Elem(), "ts")
			if field(t, ts, "set").Uint()&setWeightBit != 0 {
				return field(t, ts, "weight").Int()
			}
		}
		return 0
	}
	var out []int64
	var walk func(n reflect.Value)
	walk = func(n reflect.Value) {
		for ; !n.IsNil(); n = field(t, n.Elem(), "next") {
			e := n.Elem()
			if field(t, e, "hidden").Bool() || field(t, e, "flags").Uint()&flagInvisible != 0 {
				continue
			}
			if field(t, e, "kind").Uint() == kindText && field(t, e, "text").String() == s {
				out = append(out, weight(n))
			}
			walk(field(t, e, "first"))
		}
	}
	walk(field(t, c, "root"))
	return out
}

// #15：文件树里当前打开的文件不加粗，名字的字重和别的行一样（只靠选中底色区分）。三个仓库都展开，
// 打开 gamma 的 gamma.md（干净、没有状态色），它在树里（和标签上）的名字字重和没打开的 beta.go 一样。
func TestTreeCurrentNotBold(t *testing.T) {
	_, _, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	a.ShowSidebar(workspace.ModeFiles)
	for _, r := range a.Repos {
		if !r.Expanded {
			a.ToggleRepo(r)
		}
		a.RefreshRepo(r, app.WhyManual)
		a.EnsureFiles(r)
	}
	wait("三个仓库的文件", func() bool {
		return loaded(a)() && tt.HasText("notes.md") && tt.HasText("beta.go") && tt.HasText("gamma.md")
	})
	gamma := a.Repos[2]
	if d := a.OpenFile(gamma, "gamma.md", true); d == nil {
		t.Fatal("打不开 gamma.md")
	}
	wait("gamma.md 是当前文件", func() bool {
		d := a.CurrentDoc()
		return d != nil && d.Root == gamma.Root && d.Rel == "gamma.md"
	})
	tt.Frame()

	other := textWeights(t, tt, "beta.go")
	if len(other) == 0 {
		t.Fatal("树里找不到 beta.go")
	}
	cur := textWeights(t, tt, "gamma.md")
	if len(cur) < 2 { // 树里一行、标签上一个
		t.Fatalf("gamma.md 只画了 %d 处，想要树里和标签上都有", len(cur))
	}
	for _, w := range cur {
		if w != other[0] {
			t.Fatalf("当前文件 gamma.md 的字重 %v，别的行 beta.go 是 %d：当前文件不该加粗", cur, other[0])
		}
	}
}
