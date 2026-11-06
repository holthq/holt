package ui

import (
	"fmt"
	"image/color"
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 合并目录、缩进参考线：只有一个子目录的目录一行显示 "internal / ui"，每段右键对那一段；
// 箭头展开每一层；参考线在上一级箭头的正中，关掉设置就没了。
func TestTreeCompactGuides(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("internal/ui/view.go", "package ui\n")
	repo.Write("internal/ui/list.go", "package ui\n")
	repo.Write("lib/a.go", "package lib\n")
	repo.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	var copied string
	keep := copyText
	copyText = func(s string) { copied = s }
	t.Cleanup(func() { copyText = keep })
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Current()
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("internal") && tt.HasText("lib") })
	// 分隔的 "/" 两边空得一样（以前画成过 "internal /ui"：" / " 末尾的空格量宽时不算）
	seg, ok0 := tt.Find("internal")
	sl, ok1 := tt.Find("/")
	ur, ok2 := tt.Find("ui")
	if !ok0 || !ok1 || !ok2 || sl.X < seg.X+seg.W || ur.X < sl.X+sl.W {
		t.Fatalf("合并行没画成 internal / ui：%v %v %v %q", seg, sl, ur, tt.Texts())
	}
	tt.Move(700, 400)
	tt.Frame()
	img := tt.Image()
	bg := img.RGBAAt(int(ur.X+ur.W+4), int(seg.Y+seg.H/2))
	runs := inkRuns(img, wui.Rect{X: seg.X, Y: seg.Y + 1, W: ur.X + ur.W - seg.X, H: seg.H - 2}, 1, bg, 2)
	if len(runs) != 3 {
		t.Fatalf("合并行的墨迹不是 internal、/、ui 三块：%v", runs)
	}
	left, right := runs[1].x0-runs[0].x1-1, runs[2].x0-runs[1].x1-1
	if left < 2 || right < 2 || left-right > 1 || right-left > 1 {
		t.Errorf("\"/\" 左边空 %d、右边空 %d", left, right)
	}

	// 每段的右键菜单对那一段
	mustRightClick(t, tt, "internal", "复制仓库内路径")
	if copied != "internal" {
		t.Fatalf("右键 internal 复制的是 %q", copied)
	}
	mustRightClick(t, tt, "ui", "复制仓库内路径")
	if copied != "internal/ui" {
		t.Fatalf("右键 ui 复制的是 %q", copied)
	}

	// 箭头：每一层都展开
	if err := tt.Click("展开 internal / ui"); err != nil {
		t.Fatal(err)
	}
	wait("展开合并行", func() bool { return tt.HasText("view.go") })
	if !r.Files.Open["internal"] || !r.Files.Open["internal/ui"] {
		t.Fatalf("展开合并行：%v", r.Files.Open)
	}

	// 参考线：view.go 那行在上一级（合并行）箭头的正中、仓库行箭头的正中各有一条竖线
	tt.Move(700, 400) // 指针挪开，不悬停在行上
	tt.Frame()
	px := func(x, y float32) color.RGBA { return tt.Image().RGBAAt(int(x), int(y)) }
	// line 是 x 处比旁边 4 pt 深一截（软背景是渐变：隔 4 pt 本来就差 1、2）
	line := func(x, y float32) bool { return inkDist(px(x, y), px(x+4, y)) > 12 }
	view, ok := tt.Find("view.go")
	if !ok {
		t.Fatal("找不到 view.go")
	}
	y := view.Y + view.H/2
	for _, chev := range []string{"收起 internal / ui", "收起 proj"} {
		b, ok := tt.Find(chev)
		if !ok {
			t.Fatalf("找不到 %s", chev)
		}
		x := b.X + b.W/2
		if !line(x, y) {
			t.Errorf("%s 正下方 view.go 那行没有参考线：%v / 旁边 %v", chev, px(x, y), px(x+4, y))
		}
	}
	a.St.Settings.TreeNoGuides = true
	a.SettingsChanged()
	tt.Frame()
	tt.Frame()
	b, _ := tt.Find("收起 internal / ui")
	if x := b.X + b.W/2; line(x, y) {
		t.Error("关掉参考线还在画")
	}
}

// 吸顶栈（原来只钉仓库行）：第二个仓库滚到中间时它的仓库行钉在顶上，点它滚回真的仓库行；滚回顶上没有钉着的。
// 第三个仓库 deep：滚到 src/main/java（合并行）的文件深处，栈是 [deep, src / main / java]；滚到 d1/d2/d3/d4 的文件里，
// 栈只留仓库 + 最近的两级 [deep, d3, d4]；点 d4 那层滚到真的 d4 那行（露在栈底下），d4 那层就不钉了。
func TestTreeStickyStack(t *testing.T) {
	small := testutil.NewRepo(t, "small")
	small.Write("a.txt", "a\n")
	small.Commit("init")
	big := testutil.NewRepo(t, "big")
	for i := range 90 {
		big.Write(fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	big.Commit("init")
	deep := testutil.NewRepo(t, "deep")
	deep.Write("src/main/java/App.java", "class App {}\n")
	for i := range 40 {
		deep.Write(fmt.Sprintf("src/main/java/F%02d.java", i), "class F {}\n")
		deep.Write(fmt.Sprintf("d1/d2/d3/d4/g%02d.txt", i), "g\n")
	}
	for _, d := range []string{"d1", "d1/d2", "d1/d2/d3"} {
		deep.Write(d+"/x.txt", "x\n")
	}
	deep.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, small.Root, big.Root, deep.Root), "")
	a.ShowSidebar(workspace.ModeFiles)
	for _, r := range a.Repos {
		if !r.Expanded {
			a.ToggleRepo(r)
		}
		a.EnsureFiles(r)
	}
	rb, rd := a.Repos[1], a.Repos[2]
	wait("三个仓库的文件", func() bool { return rb.Files.Loaded && rd.Files.Loaded && tt.HasText("a.txt") })
	rd.Files.Reveal("src/main/java/App.java")
	rd.Files.Reveal("d1/d2/d3/d4/g00.txt")
	tt.Frame()
	rows := a.TreeRows()
	root := -1
	at := func(r *app.Repo, p string) int {
		for i, row := range rows {
			if row.Repo == r && !row.Root && row.Path == p {
				return i
			}
		}
		t.Fatalf("%s 里没有 %s 那行", r.Name, p)
		return -1
	}
	for i, row := range rows {
		if row.Root && row.Repo == rb {
			root = i
		}
	}
	pinned := "big（仓库，钉在顶上）"
	scroll := func(i int) {
		a.FileList.ScrollTo(i, wui.Start)
		for range 4 {
			tt.Frame()
		}
	}
	// pins 是看得见的吸顶层（从上到下）
	pins := func() []string {
		var out []string
		var ys []float32
		for _, name := range []string{"small（仓库，钉在顶上）", pinned, "deep（仓库，钉在顶上）", "src / main / java（钉在顶上）",
			"d1（钉在顶上）", "d2（钉在顶上）", "d3（钉在顶上）", "d4（钉在顶上）"} {
			if b, ok := tt.Find(name); ok {
				k := len(ys)
				for k > 0 && ys[k-1] > b.Y {
					k--
				}
				out, ys = slices.Insert(out, k, name), slices.Insert(ys, k, b.Y)
			}
		}
		return out
	}
	scroll(root + 40)
	if got := pins(); !slices.Equal(got, []string{pinned}) {
		t.Fatalf("big 滚到中间，钉着的是 %q，应该只有它的仓库行", got)
	}
	// 光标那行在钉着的仓库行底下（↑ 走到顶时滚成这样）：不钉，先让它看得见；光标往下挪开又钉上
	a.FileSel = root + 40
	for range 3 {
		tt.Frame()
	}
	if _, ok := tt.Find(pinned); ok {
		t.Fatal("光标那行被钉着的仓库行盖住了")
	}
	a.FileSel = root + 45
	for range 3 {
		tt.Frame()
	}
	if _, ok := tt.Find(pinned); !ok {
		t.Fatal("光标挪开后应该又钉上")
	}
	// 右键是仓库菜单
	if err := tt.RightClick(pinned); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !hasItem(shownMenu(t, tt), "从列表移除") {
		t.Fatalf("钉着的仓库行右键菜单 %q", shownMenu(t, tt))
	}
	closeMenu(t, tt)
	tt.Frame()
	// 点它滚回真的仓库行
	pb, _ := tt.Find("收起 big") // 钉着的那行的箭头（真的仓库行没画）
	if err := tt.Click(pinned); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		tt.Frame()
	}
	// Visible 把露在列表留白里的上一行也算上，所以看真的仓库行画在哪儿：在列表顶上（留白 2）
	if _, ok := tt.Find(pinned); ok {
		t.Fatal("真的仓库行在顶上了，不该再钉着")
	}
	if b, ok := tt.Find("收起 big"); !ok || b.Y < pb.Y || b.Y > pb.Y+4 {
		t.Fatalf("点钉着的仓库行后 big 的仓库行箭头在 %v，钉着时在 %v", b, pb)
	}

	// 合并行的文件深处：[deep, src / main / java]
	a.FileSel = -1
	scroll(at(rd, "src/main/java/F20.java"))
	if got := pins(); !slices.Equal(got, []string{"deep（仓库，钉在顶上）", "src / main / java（钉在顶上）"}) {
		t.Errorf("滚到 src/main/java 的文件深处，钉着的是 %q", got)
	}
	// 四级目录的文件里：只留最近的两级
	scroll(at(rd, "d1/d2/d3/d4/g20.txt"))
	if got := pins(); !slices.Equal(got, []string{"deep（仓库，钉在顶上）", "d3（钉在顶上）", "d4（钉在顶上）"}) {
		t.Fatalf("滚到 d4 的文件里，钉着的是 %q", got)
	}
	d3, _ := tt.Find("d3（钉在顶上）")
	if err := tt.Click("d4（钉在顶上）"); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		tt.Frame()
	}
	got := pins()
	if slices.Contains(got, "d4（钉在顶上）") {
		t.Fatalf("点了 d4 那层还钉着它：%q", got)
	}
	d4, ok := a.TreeSpans[at(rd, "d1/d2/d3/d4")]
	if !ok || len(got) == 0 {
		t.Fatalf("点了 d4 那层，真的 d4 那行没画出来（%v）或栈空了：%q", d4, got)
	}
	last, _ := tt.Find(got[len(got)-1])
	if d4.Y < last.Y+last.H-0.5 || d4.Y > d3.Y+d3.H+30 {
		t.Errorf("点了 d4 那层，真的 d4 那行在 %.1f，栈底在 %.1f，原来钉着的 d4 在 %.1f", d4.Y, last.Y+last.H, d3.Y+d3.H)
	}

	scroll(0)
	if got := pins(); len(got) > 0 {
		t.Fatalf("滚回顶上不该钉着：%q", got)
	}
}

func hasItem(items []string, s string) bool {
	for _, it := range items {
		if it == s {
			return true
		}
	}
	return false
}
