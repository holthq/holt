package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 键盘找字：文件树有键盘时打字母跳到名字以它开头的行（mygo 列表的 Label，大小写不分）：
// 一秒内接着打的连起来，同一个字母再打跳到下一个；仓库行念"proj（仓库）"、名字在前，打仓库名跳到它；
// 合并行从第一段找起；收起的目录里面的不找；只选中、不预览；多选收成那一行。
func TestTreeTypeToFind(t *testing.T) {
	proj := testutil.NewRepo(t, "proj")
	for _, p := range []string{"alpha.go", "beta.txt", "bravo.md", "docs/charlie.md", "internal/ui/view.go", "README.md", "main.go", "tree.go", "test.go"} {
		proj.Write(p, "x\n")
	}
	proj.Commit("init")
	other := testutil.NewRepo(t, "other")
	other.Write("omega.txt", "o\n")
	other.Commit("init")
	k := newTreeKit(t, proj.Root, other.Root)
	a, tt := k.a, k.tt

	k.click(0, "alpha.go")
	a.Exec("file.closeAll")
	tt.Frame()
	typed := func(want, rel string, keys ...wui.Key) {
		t.Helper()
		for _, key := range keys {
			tt.Key(0, key)
			tt.Frame()
		}
		tt.Frame()
		if got := k.sel(); got != rel {
			t.Fatalf("%s：光标在 %s，想要 %s", want, got, rel)
		}
		tt.Advance(2 * time.Second) // 下一次重新打
	}
	typed("打 b b（一秒内）", "bravo.md", wui.KeyB, wui.KeyB)
	if a.CurrentDoc() != nil || a.FileChosen.Len() != 1 {
		t.Fatalf("找字只选中、不预览：标签 %v，选中 %d 项", a.CurrentDoc(), a.FileChosen.Len())
	}
	typed("过了一秒再打 b（从光标往下，绕回来）", "beta.txt", wui.KeyB)
	typed("打 br", "bravo.md", wui.KeyB, wui.KeyR)
	typed("打 RE（大小写不分）", "README.md", wui.KeyR, wui.KeyE)
	// 例子：tr 是一次（不是先 t 到 test.go 再 r 到 README）；1.2 s 后打 m 重来
	typed("打 tr", "tree.go", wui.KeyT, wui.KeyR)
	tt.Key(0, wui.KeyT)
	tt.Frame()
	tt.Advance(1200 * time.Millisecond)
	typed("1.2 s 后打 m", "main.go", wui.KeyM)
	typed("打 in（合并行 internal / ui）", "internal/ui", wui.KeyI, wui.KeyN)

	// 收起的 docs 里的 charlie.md 不找：光标不动
	typed("打 c", "internal/ui", wui.KeyC)

	// 仓库行：打 o 到 other 仓库、打 p 回 proj 仓库（行不展开也不收起）
	typed("打 o", "仓库", wui.KeyO)
	if row, _ := a.TreeRowAt(a.FileSel); row.Repo.Name != "other" || !row.Repo.Expanded {
		t.Fatalf("打 o 该到 other 仓库行（还展开着）：%+v", row)
	}
	typed("打 om", "omega.txt", wui.KeyO, wui.KeyM)
	typed("打 p", "仓库", wui.KeyP)
	if row, _ := a.TreeRowAt(a.FileSel); row.Repo.Name != "proj" {
		t.Fatalf("打 p 该到 proj 仓库行：%+v", row)
	}

	// 多选时打字：收成跳到的那一行
	k.click(0, "alpha.go")
	k.click(wui.Cmd, "beta.txt")
	a.Exec("file.closeAll")
	tt.Frame()
	typed("多选时打 r", "README.md", wui.KeyR)
	if rows := a.ChosenRows(); len(rows) != 1 || rows[0].Path != "README.md" {
		t.Fatalf("多选时找字之后选中 %d 项", len(rows))
	}

	// 筛选框有键盘时字进筛选框，不找字
	mustClickRow(t, tt, "筛选文件")
	tt.Type("bra")
	tt.Frame()
	if a.Filter != "bra" {
		t.Fatalf("筛选框里打字：筛选 %q", a.Filter)
	}
}

// 拖放悬停自动展开：拖着停在收起的目录行 / 仓库行上满半秒展开它（合并行整条链），没停够挪走不展开，
// 文件行上不展开，展开了挪走也不收回。拖到别的仓库上光标是复制的，松手先问复制（原来的不动）；按着 ⌥ 也是复制光标。
// （⌥ 松手直接复制在 app 的 TestDropTreeCopy：Tester 松手不带修饰键。）
func TestTreeDragDwell(t *testing.T) {
	proj := testutil.NewRepo(t, "proj")
	for _, p := range []string{"u.txt", "docs/n.md", "lib/x.txt", "internal/ui/view.go"} {
		proj.Write(p, p+"\n")
	}
	proj.Commit("init")
	other := testutil.NewRepo(t, "other")
	other.Write("o.txt", "o\n")
	other.Commit("init")
	k := newTreeKit(t, proj.Root, other.Root)
	a, tt := k.a, k.tt
	pr, or := k.repo("proj"), k.repo("other")
	a.ToggleRepo(or) // other 收起
	k.click(0, "u.txt")
	a.Exec("file.closeAll")
	tt.Frame()

	var x, y float32
	at := func(name string) {
		t.Helper()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q；texts=%q", name, tt.Texts())
		}
		x, y = b.X+b.W/2, b.Y+b.H/2
	}
	glide := func(name string) { // 按着从现在的位置分几步挪到 name 的中间
		t.Helper()
		x0, y0 := x, y
		at(name)
		for i := 1; i <= 4; i++ {
			s := float32(i) / 4
			tt.Move(x0+(x-x0)*s, y0+(y-y0)*s)
		}
		tt.Frame()
	}
	at("u.txt")
	tt.Press(x, y)
	glide("docs")
	tt.Advance(300 * time.Millisecond)
	if tt.Cursor() == wui.CursorCopy {
		t.Error("同一个仓库里不按 ⌥ 拖，光标成了复制的")
	}
	glide("u.txt")
	tt.Advance(600 * time.Millisecond)
	if pr.Files.Open["docs"] {
		t.Fatal("在 docs 上停了 300 ms 就挪走（停在文件行上），docs 展开了")
	}
	glide("docs")
	tt.Advance(600 * time.Millisecond)
	if !pr.Files.Open["docs"] || !tt.HasText("n.md") {
		t.Fatalf("在 docs 上停了 600 ms 没展开：open=%v n.md=%v", pr.Files.Open["docs"], tt.HasText("n.md"))
	}
	glide("u.txt")
	tt.Advance(100 * time.Millisecond)
	if !pr.Files.Open["docs"] {
		t.Fatal("挪走之后 docs 又收起了")
	}
	glide("internal") // 合并行 internal / ui
	tt.Advance(600 * time.Millisecond)
	if !pr.Files.Open["internal"] || !pr.Files.Open["internal/ui"] || !tt.HasText("view.go") {
		t.Fatalf("合并行没整条展开：%v", pr.Files.Open)
	}
	tt.HoldModifiers(wui.Alt)
	tt.Frame()
	tt.Frame()
	if tt.Cursor() != wui.CursorCopy {
		t.Errorf("按着 ⌥ 拖在目录上，光标 %v 不是复制的", tt.Cursor())
	}
	tt.HoldModifiers(0)
	glide("other（仓库）")
	tt.Advance(600 * time.Millisecond)
	if !or.Expanded || !tt.HasText("o.txt") {
		t.Fatal("在收起的仓库行上停了 600 ms 没展开")
	}
	tt.Frame()
	if tt.Cursor() != wui.CursorCopy {
		t.Errorf("拖到别的仓库上，光标 %v 不是复制的", tt.Cursor())
	}
	tt.Release(x, y)
	tt.Frame()
	if !a.ConfirmOpen || a.Confirm.Title != "把 u.txt 复制到 other？" || !strings.Contains(a.Confirm.Message, "proj 里原来的不动") {
		t.Fatalf("跨仓库松手没先问复制：%+v", a.Confirm)
	}
	mustClickRow(t, tt, "复制")
	k.wait("复制到 other", func() bool { return idle(or)() && or.Files.Has("u.txt") })
	if _, err := os.Stat(filepath.Join(proj.Root, "u.txt")); err != nil {
		t.Fatal("跨仓库复制把原来的拿走了")
	}
	if row, ok := a.TreeRowAt(a.FileSel); !ok || row.Repo != or || row.Path != "u.txt" {
		t.Errorf("没选中复制过去的：%+v", row)
	}
}

// 按 Git 状态给文件名着色（默认开）：开着时改了的名字发黄、没跟踪的发绿，
// 里面有改动的目录名也发黄；关掉以后名字是正文色。量的是行的左边六成（含名字，不含右边的字母；目录的圆点在里面，
// 所以目录比的是开关前后差多少）里偏黄 / 偏绿的点。
func TestTreeStatusColors(t *testing.T) {
	proj := testutil.NewRepo(t, "proj")
	for _, p := range []string{"a.txt", "m.txt", "src/s.txt", "doc/d.txt"} {
		proj.Write(p, "x\n")
	}
	proj.Commit("init")
	proj.Write("m.txt", "changed\n")
	proj.Write("n.txt", "new\n")
	proj.Write("src/s.txt", "changed\n")
	k := newTreeKit(t, proj.Root)
	a, tt := k.a, k.tt
	if a.St.Settings.TreeNoStatusColors {
		t.Fatal("着色默认该开着")
	}
	k.click(0, "a.txt")
	a.Exec("file.closeAll")
	k.wait("状态", func() bool { return idle(k.repo("proj"))() && tt.HasText("M") })
	scale := float32(tt.Image().Bounds().Dx()) / 1440
	count := func(name string, hue func(c color.RGBA) bool) int {
		t.Helper()
		tt.Frame()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %s", name)
		}
		img, n := tt.Image(), 0
		for y := int(b.Y * scale); y < int((b.Y+b.H)*scale); y++ {
			for x := int(b.X * scale); x < int((b.X+b.W*0.6)*scale); x++ {
				if hue(img.RGBAAt(x, y)) {
					n++
				}
			}
		}
		return n
	}
	yellow := func(c color.RGBA) bool { return int(c.R) > int(c.B)+70 && int(c.G) > int(c.B)+40 }
	green := func(c color.RGBA) bool { return int(c.G) > int(c.R)+40 && int(c.G) > int(c.B)+20 }
	m1, n1, s1, d1 := count("m.txt", yellow), count("n.txt", green), count("src", yellow), count("doc", yellow)
	a.St.Settings.TreeNoStatusColors = true
	m0, n0, s0 := count("m.txt", yellow), count("n.txt", green), count("src", yellow)
	t.Logf("偏黄 %d → %d，偏绿 %d → %d，目录 src 偏黄 %d → %d（doc %d）", m0, m1, n0, n1, s0, s1, d1)
	if m1 < m0+15 || n1 < n0+15 {
		t.Fatalf("开着时名字没变色：改了的偏黄点 %d / 关了 %d，没跟踪的偏绿点 %d / 关了 %d", m1, m0, n1, n0)
	}
	if s1 < s0+10 || d1 > s0 {
		t.Errorf("有改动的目录 src 名字没变黄（%d，关着时 %d），没改动的 doc %d", s1, s0, d1)
	}
	a.St.Settings.TreeNoStatusColors = false
	if m2 := count("m.txt", yellow); m2 != m1 {
		t.Errorf("再开回来不一样：%d（原来 %d）", m2, m1)
	}
}

// 拖仓库行调顺序：选中的仓库行拖到别的仓库行的上半 / 下半，停着时那条边上画一条插入线（放回原位的不画），
// 松手挪过去、写回工作区文件的 folders；仓库菜单的"上移"在最上面那个仓库上灰着，"下移"挪一格。
func TestTreeRepoReorder(t *testing.T) {
	var roots []string
	for _, n := range []string{"proj", "other", "third"} {
		r := testutil.NewRepo(t, n)
		r.Write(n+".txt", "x\n")
		r.Commit("init")
		roots = append(roots, r.Root)
	}
	k := newTreeKit(t, roots...)
	a, tt := k.a, k.tt
	names := func() string {
		var s []string
		for _, r := range a.Repos {
			s = append(s, r.Name)
		}
		return strings.Join(s, " ")
	}
	saved := func() string {
		got, err := workspace.Load(a.WsPath)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, p := range got {
			s = append(s, filepath.Base(p))
		}
		return strings.Join(s, " ")
	}
	if err := rightClickRow(tt, "proj（仓库）"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(t, tt, "更多", "上移"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("最上面的仓库的上移没灰着：%v；菜单=%q", err, shownMenu(t, tt))
	}
	closeMenu(t, tt)

	scale := float32(tt.Image().Bounds().Dx()) / 1440
	edge := func(name string, lower bool) []color.RGBA { // name 那行上边 / 下边那条线上的点
		t.Helper()
		tt.Frame()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %s", name)
		}
		y, img := b.Y, tt.Image()
		if lower {
			y = b.Y + b.H - 0.5
		}
		var px []color.RGBA
		for x := int((b.X + 8) * scale); x < int((b.X+b.W-8)*scale); x++ {
			px = append(px, img.RGBAAt(x, int(y*scale)))
		}
		return px
	}
	changed := func(p, q []color.RGBA) int {
		n := 0
		for i := range min(len(p), len(q)) {
			if p[i] != q[i] {
				n++
			}
		}
		return n
	}
	var x, y float32
	to := func(name string, frac float32) { // 按着分几步挪到 name 那行的 frac 高度
		t.Helper()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q", name)
		}
		x0, y0 := x, y
		x, y = b.X+b.W/2, b.Y+b.H*frac
		for i := 1; i <= 4; i++ {
			s := float32(i) / 4
			tt.Move(x0+(x-x0)*s, y0+(y-y0)*s)
		}
		tt.Frame()
	}

	k.click(0, "third（仓库）")
	projTop, otherLow := edge("proj（仓库）", false), edge("other（仓库）", true)
	b, _ := tt.Find("third（仓库）")
	x, y = b.X+b.W/2, b.Y+b.H/2
	tt.Press(x, y)
	to("other（仓库）", 0.8) // other 下半 = third 原来的位置：不画线
	if n := changed(otherLow, edge("other（仓库）", true)); n > len(otherLow)/4 {
		t.Errorf("放回原位也画了插入线（%d 个点变了）", n)
	}
	to("proj（仓库）", 0.25)
	if n := changed(projTop, edge("proj（仓库）", false)); n < len(projTop)/2 {
		t.Fatalf("停在 proj 上半没画插入线：%d / %d 个点变了", n, len(projTop))
	}
	if names() != "proj other third" {
		t.Fatal("没松手就挪了")
	}
	tt.Release(x, y)
	tt.Frame()
	if names() != "third proj other" || saved() != "third proj other" {
		t.Fatalf("third 拖到 proj 上面：Repos %s，工作区文件 %s", names(), saved())
	}
	if row, _ := a.TreeRowAt(a.FileSel); !row.Root || row.Repo.Name != "third" {
		t.Errorf("光标没跟着 third：%+v", row)
	}

	mustRightClickRow(t, tt, "proj（仓库）", "更多", "下移")
	if names() != "third other proj" || saved() != "third other proj" {
		t.Fatalf("proj 下移：Repos %s，工作区文件 %s", names(), saved())
	}
}

// 符号链接：链接行名字后面一个链接图标，悬停提示"→ 目标"（相对的照写，断了的说"目标不存在"），
// 已跟踪的、未跟踪的、指向目录的（是能展开的目录行，提示多"（目录）"）都有；普通文件没有。
func TestTreeSymlinks(t *testing.T) {
	proj := testutil.NewRepo(t, "proj")
	proj.Write("a.txt", "a\n")
	proj.Write("sub/b.txt", "b\n")
	link := func(target, p string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(proj.Root, p)); err != nil {
			t.Fatal(err)
		}
	}
	link("a.txt", "link-a")
	link("sub", "dirlink")
	link("nope", "broken")
	proj.Commit("init")
	link("sub/b.txt", "ulink")
	k := newTreeKit(t, proj.Root)
	tt := k.tt
	k.wait("链接", func() bool { return tt.HasText("ulink") })
	type tip struct {
		desc string
		x, y float32
	}
	// 元素树里带提示（mygo 记成 description）的图标
	var tips []tip
	var walk func(n reflect.Value)
	walk = func(n reflect.Value) {
		for ; !n.IsNil(); n = field(t, n.Elem(), "next") {
			e := n.Elem()
			if d := field(t, e, "description").String(); field(t, e, "kind").Uint() == kindIcon && strings.HasPrefix(d, "→ ") {
				tips = append(tips, tip{d, float32(field(t, e, "x").Float()), float32(field(t, e, "y").Float())})
			}
			walk(field(t, e, "first"))
		}
	}
	tt.Frame()
	walk(field(t, field(t, field(t, reflect.ValueOf(tt).Elem(), "rt").Elem(), "c"), "root"))
	var got []string
	for _, tp := range tips {
		got = append(got, tp.desc)
	}
	slices.Sort(got)
	if want := []string{"→ a.txt", "→ nope（目标不存在）", "→ sub/b.txt", "→ sub（目录）"}; !slices.Equal(got, want) {
		t.Fatalf("链接图标的提示 %q，要 %q", got, want)
	}
	row, _ := tt.Find("dirlink") // 整行（列表行的 Label 是名字）
	i := slices.IndexFunc(tips, func(tp tip) bool { return tp.desc == "→ sub（目录）" })
	if tp := tips[i]; tp.x < row.X+60 || tp.x > row.X+row.W/2 || tp.y > row.Y+row.H || tp.y+iconRow < row.Y {
		t.Errorf("dirlink 的链接图标 @(%.0f,%.0f) 不在那一行 %+v 的名字后面", tp.x, tp.y, row)
	}
	if row, ok := k.a.TreeRowAt(slices.IndexFunc(k.a.TreeRows(), func(r app.TreeRow) bool { return r.Path == "dirlink" })); !ok || !row.Dir || !row.DirLink() {
		t.Error("指向目录的链接该是能展开的目录行")
	}
}
