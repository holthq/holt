package ui

import (
	"bytes"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 文件与编辑器的小功能：树里拖动、访达拖入、被忽略的行、编码条、大纲、保存整理。都在临时仓库里。

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// filesUI 开好文件树：root 是仓库，等到 want 出现在树里。
func filesUI(t *testing.T, ws string, want string) (*app.App, *wui.Tester, func(string, func() bool)) {
	t.Helper()
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.ShowSidebar(workspace.ModeFiles)
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return idle(r)() && r.Files.Loaded && tt.HasText(want) })
	return a, tt, wait
}

// chooseRow 点一下树里的 name 选中它（单击会预览），再把标签都关掉，免得标签上的名字比树里的先被找到。
func chooseRow(t *testing.T, a *app.App, tt *wui.Tester, name string) {
	t.Helper()
	mustClickRow(t, tt, name)
	a.Exec("file.closeAll")
	tt.Frame()
}

// 拖选中的文件到目录行上：拖在上面时那行高亮，松手移过去；拖到仓库行是移到仓库根；拖目录先问；
// 从访达拖到目录行上先问再复制；筛选时不能拖（按住拖是拖着选）。
func TestTreeDragMove(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	for _, p := range []string{"lib/x.txt", "docs/n.md"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(p+"\n"), 0o644)
	}
	a, tt, wait := filesUI(t, ws, "docs")
	r := a.Repos[0]

	chooseRow(t, a, tt, "u.txt")
	lib, _ := tt.Find("lib")
	f, _ := tt.Find("u.txt")
	scale := float32(tt.Image().Bounds().Dx()) / 1440
	px := func() color.RGBA { // lib 那行靠右的底色（Find 到的是整行）
		return tt.Image().RGBAAt(int((lib.X+lib.W*0.8)*scale), int((lib.Y+lib.H/2)*scale))
	}
	x0, y0, x1, y1 := f.X+f.W/2, f.Y+f.H/2, lib.X+lib.W/2, lib.Y+lib.H/2
	tt.Move(x1, y1)
	tt.Frame()
	hover := px()
	tt.Press(x0, y0)
	for k := 1; k <= 4; k++ {
		s := float32(k) / 4
		tt.Move(x0+(x1-x0)*s, y0+(y1-y0)*s)
	}
	tt.Frame()
	if over := px(); over == hover {
		t.Errorf("拖在目录行上没有高亮：%v", over)
	}
	tt.Release(x1, y1)
	tt.Frame()
	if a.ConfirmOpen || !exists(filepath.Join(root, "lib", "u.txt")) || exists(filepath.Join(root, "u.txt")) {
		t.Fatalf("一个干净的文件应直接移过去：confirm=%+v", a.Confirm)
	}
	wait("树里 lib 展开、选中移过去的", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return idle(r)() && ok && row.Path == "lib/u.txt" && tt.HasText("x.txt")
	})

	// 拖到仓库行：回到仓库根（它已经选中了）
	tt.Frame()
	dragTo(t, tt, "u.txt", "demo（仓库）", 0.5)
	if !exists(filepath.Join(root, "u.txt")) {
		t.Fatal("拖到仓库行没移到仓库根")
	}
	wait("树里", func() bool { return idle(r)() && r.Files.Has("u.txt") })

	// 目录：先问，点"移动"才移
	chooseRow(t, a, tt, "docs")
	dragTo(t, tt, "docs", "lib", 0.5)
	if !a.ConfirmOpen || a.Confirm.Title != "把 docs 移到 lib？" || !exists(filepath.Join(root, "docs")) {
		t.Fatalf("拖目录没先问：%+v", a.Confirm)
	}
	mustClickRow(t, tt, "移动")
	if a.ConfirmOpen || !exists(filepath.Join(root, "lib", "docs", "n.md")) {
		t.Fatal("点了移动没移")
	}
	wait("树里", func() bool { return idle(r)() && r.Files.Has("lib/docs/n.md") })

	// 从访达拖到目录行上：先问，点"复制"才复制
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "z.txt"), []byte("z\n"), 0o644)
	lib, _ = tt.Find("lib")
	if !tt.DropFiles(lib.X+lib.W/2, lib.Y+lib.H/2, filepath.Join(out, "z.txt")) {
		t.Fatal("目录行没接访达拖来的文件")
	}
	if !a.ConfirmOpen || a.Confirm.Title != "把 1 项复制到 lib？" {
		t.Fatalf("访达拖来的没先问：%+v", a.Confirm)
	}
	mustClickRow(t, tt, "复制")
	wait("复制进来", func() bool { return idle(r)() && r.Files.Has("lib/z.txt") })
	if !exists(filepath.Join(out, "z.txt")) {
		t.Error("复制把原来的拿走了")
	}

	// 筛选时：没有目录行可放，按住选中的行拖是拖着选，不移动
	a.SetFilter("txt")
	wait("筛选", func() bool { return tt.HasText("z.txt") && !tt.HasText("docs") })
	chooseRow(t, a, tt, "a.txt")
	dragTo(t, tt, "a.txt", "z.txt", 0.5)
	if a.ConfirmOpen || !exists(filepath.Join(root, "a.txt")) || a.FileChosen.Len() < 2 {
		t.Fatalf("筛选时拖了：confirm=%v 选中 %d", a.ConfirmOpen, a.FileChosen.Len())
	}
}

// 显示被忽略的文件：开了灰行出现（目录点箭头读一层），快速打开不含它们；关掉就没了。
func TestIgnoredRows(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\n*.log\n"), 0o644)
	os.WriteFile(filepath.Join(root, "debug.log"), []byte("log\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "build", "obj"), 0o755)
	os.WriteFile(filepath.Join(root, "build", "out.bin"), []byte("bin\n"), 0o644)
	a, tt, wait := filesUI(t, ws, ".gitignore")
	r := a.Repos[0]
	if tt.HasText("debug.log") || tt.HasText("build") {
		t.Fatal("默认不该显示被忽略的")
	}

	a.Exec("files.ignored")
	if !a.St.Settings.ShowIgnored {
		t.Fatal("开关没开")
	}
	wait("灰行", func() bool { return tt.HasText("debug.log") && tt.HasText("build") })
	if tt.HasText("out.bin") {
		t.Fatal("被忽略的目录不该一开始就读")
	}
	mustClickRow(t, tt, "展开 build")
	wait("读一层", func() bool { return tt.HasText("out.bin") && tt.HasText("obj") })

	a.OpenPalette("")
	tt.Frame()
	tt.Type("debug")
	tt.Frame()
	for _, it := range a.Palette.Items {
		if it.Kind == app.ItemFile {
			t.Errorf("快速打开里有被忽略的：%q", it.Text)
		}
	}
	a.ClosePalette()

	a.Exec("files.ignored")
	wait("灰行没了", func() bool { return !tt.HasText("debug.log") && !tt.HasText("out.bin") && r.Files.Loaded })
}

// 编码：GB18030 能编辑、没有条；编不回原样的（单独的 0x80）只读，"转成 UTF-8…"先问（多一句提醒）再转；
// Big5 只读、有"换编码…"，状态栏的编码点开能换：按 UTF-8、GB18030 解不通也打开、条上说明，
// "自动识别编码重新打开"回到 Big5，文件不动。认不出的只有说明。
func TestEncodingBar(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	enc := func(e encoding.Encoding, s string) []byte {
		b, err := e.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	os.WriteFile(filepath.Join(root, "gb.txt"), enc(simplifiedchinese.GB18030, "中文\n"), 0o644)
	os.WriteFile(filepath.Join(root, "lossy.txt"), append(enc(simplifiedchinese.GB18030, "中文"), 0x80, '\n'), 0o644)
	os.WriteFile(filepath.Join(root, "tw.txt"), enc(traditionalchinese.Big5, "設定檔讀取失敗時，程式會回傳錯誤並記錄日誌。\n"), 0o644)
	os.WriteFile(filepath.Join(root, "bad.txt"), []byte{0x80, 0x80, 0xFF, '\n'}, 0o644)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]

	g := a.OpenFile(r, "gb.txt", true)
	wait("读好", func() bool { return g.Buf != nil })
	tt.Frame()
	if _, ok := tt.Find("编码 GB18030"); !ok || g.St.ReadOnly || !g.Editable() || tt.HasText("换编码…") {
		t.Fatalf("GB18030 应能编辑、没有条：只读 %v", g.St.ReadOnly)
	}

	d := a.OpenFile(r, "lossy.txt", true)
	wait("读好", func() bool { return d.Buf != nil })
	tt.Frame()
	mustText(t, tt, "按 GB18030 解码显示，有编回去和原来不一样的字节，只读")
	if !d.St.ReadOnly || !d.Lossy {
		t.Fatalf("编不回原样的应只读：%v %v", d.St.ReadOnly, d.Lossy)
	}
	mustClickRow(t, tt, "转成 UTF-8…")
	if !a.ConfirmOpen || a.Confirm.Title != "把 lossy.txt 转成 UTF-8 保存？" || !strings.Contains(a.Confirm.Message, "0x80") {
		t.Fatalf("没先问，或没提醒字节会变：%+v", a.Confirm)
	}
	mustClickRow(t, tt, "转换")
	if got, _ := os.ReadFile(filepath.Join(root, "lossy.txt")); string(got) != "中文€\n" || d.Enc != docs.UTF8 || d.St.ReadOnly {
		t.Fatalf("转完：%q %q", got, d.Enc)
	}
	tt.Frame()
	if tt.HasText("存不回原样") || !tt.HasText("lossy.txt 已转成 UTF-8") {
		t.Errorf("转完条还在，或没有提示：%q", a.Toast)
	}
	if _, ok := tt.Find("编码 UTF-8"); !ok {
		t.Error("状态栏没换成 UTF-8")
	}

	w := a.OpenFile(r, "tw.txt", true)
	wait("读好", func() bool { return w.Buf != nil })
	tt.Frame()
	if tt.HasText("换编码…") || w.St.ReadOnly || !w.Editable() { // 编得回原样的 Big5 能编辑，没有只读条
		t.Fatalf("Big5 应能编辑、没有条：只读 %v", w.St.ReadOnly)
	}
	mustClickRow(t, tt, "编码 Big5")
	if m := tt.Menu(); !slices.Contains(m, "用别的编码重新打开") || !slices.Contains(m, "Shift_JIS") || !slices.Contains(m, "转成 UTF-8…") || slices.Contains(m, "自动识别编码重新打开") {
		t.Fatalf("状态栏编码的菜单：%q", m)
	}
	if err := tt.ChooseMenuItem("UTF-8"); err != nil {
		t.Fatal(err)
	}
	wait("按 UTF-8 重开", func() bool {
		return w.Enc == docs.UTF8 && tt.HasText("按 UTF-8 解码有解不出来的字节，只读")
	})
	if !w.St.ReadOnly || w.Convertible() || tt.HasText("转成 UTF-8…") {
		t.Error("解不通的应只读、不能转")
	}
	mustClickRow(t, tt, "换编码…")
	if err := tt.ChooseMenuItem("GB18030"); err != nil {
		t.Fatal(err)
	}
	wait("按 GB18030 重开", func() bool {
		return w.Enc == docs.GB18030 && tt.HasText("按 GB18030 解码有解不出来的字节，只读")
	})
	if !w.St.ReadOnly || w.ForcedEnc != docs.GB18030 {
		t.Errorf("Big5 的“，”（A1 41）这些落在 GB18030 的用户区，应只读：只读 %v，%q", w.St.ReadOnly, w.ForcedEnc)
	}
	mustClickRow(t, tt, "编码 GB18030")
	if err := tt.ChooseMenuItem("自动识别编码重新打开"); err != nil {
		t.Fatal(err)
	}
	wait("自动识别", func() bool {
		return w.Enc == docs.Big5 && w.ForcedEnc == "" && !w.St.ReadOnly && !tt.HasText("换编码…")
	})
	if got, _ := os.ReadFile(filepath.Join(root, "tw.txt")); !bytes.Equal(got, enc(traditionalchinese.Big5, "設定檔讀取失敗時，程式會回傳錯誤並記錄日誌。\n")) {
		t.Error("换编码把文件改了")
	}

	b := a.OpenFile(r, "bad.txt", true)
	wait("读好", func() bool { return b.Buf != nil && tt.HasText("编码不明") })
	if tt.HasText("转成 UTF-8…") || !b.St.ReadOnly {
		t.Error("认不出的文件不该能转、应只读")
	}
}

// 大纲：侧栏切到大纲，Markdown 的标题按级嵌套；单击跳到那一行；光标挪到哪个符号它就选中哪个
// （收起来的选最近的展开着的祖先）；@ 面板找符号回车跳过去；不支持的文件、没打开文件的有说明。
func TestOutlinePanel(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "doc.md"), []byte("# 标题\n\n## 第一节\n\n正文\n\n## 第二节\n\n### 小节\n\n结尾\n"), 0o644)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.Exec("view.outline")
	tt.Frame()
	mustText(t, tt, "没有打开的文件")

	d := a.OpenFile(r, "doc.md", true)
	wait("大纲", func() bool { return d.Buf != nil && tt.HasText("小节") })
	st := &d.OutlineSt
	var rows []string
	for i := range st.Rows() {
		rows = append(rows, d.OutlineSyms.Flat[d.OutlineSyms.ByKey[st.Item(i)]].Path)
	}
	if len(rows) != 4 || rows[3] != "标题 › 第二节 › 小节" {
		t.Fatalf("大纲的行：%q", rows)
	}

	mustClickRow(t, tt, "第二节")
	if p := d.St.CursorPos(); p.Line != 6 || d.OutlineSel != 2 {
		t.Fatalf("单击没跳过去：行 %d，选中 %d", p.Line+1, d.OutlineSel)
	}

	// 光标跟随：挪到"小节"下面那段正文里，选中"小节"；收起"第二节"后再挪回来，选中"第二节"
	end := d.Buf.Len()
	d.St.Select(end, end)
	tt.Frame()
	tt.Frame()
	if d.OutlineSel != 3 {
		t.Fatalf("光标在小节里，选中 %d", d.OutlineSel)
	}
	st.Open.Remove(d.OutlineSyms.Flat[2].Key)
	d.St.Select(0, 0)
	tt.Frame()
	d.St.Select(end, end)
	tt.Frame()
	tt.Frame()
	if d.OutlineSel != 2 || tt.HasText("小节") {
		t.Fatalf("收起后光标在小节里，选中 %d", d.OutlineSel)
	}

	// @ 面板
	a.OpenPalette("@")
	tt.Frame()
	tt.Type("第一")
	tt.Frame()
	if len(a.Palette.Items) != 1 || a.Palette.Items[0].Kind != app.ItemSymbol {
		t.Fatalf("@第一：%+v", a.Palette.Items)
	}
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if p := d.St.CursorPos(); a.Palette.Open || p.Line != 2 {
		t.Fatalf("@ 回车没跳过去：行 %d", p.Line+1)
	}

	a.OpenFile(r, "a.txt", true)
	wait("a.txt", func() bool { return tt.HasText("a.txt 没有大纲") })
}

// ⌘S 按设置整理：去掉行尾空白（光标所在的行不动），toast 说整理了几行。
func TestTidySave(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "t.txt"), []byte("a  \nb  \nc"), 0o644)
	a, tt, wait := newUI(t, ws, "")
	a.St.Settings.TrimTrailing, a.St.Settings.FinalNewline = true, true
	r := a.Repos[0]
	d := a.OpenFile(r, "t.txt", true)
	wait("读好", func() bool { return d.Buf != nil })
	mustClickRow(t, tt, "编辑 t.txt")
	d.St.Select(5, 5) // 第二行的 b 后面
	tt.Type("!")
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyS)
	tt.Frame()
	if got, _ := os.ReadFile(filepath.Join(root, "t.txt")); string(got) != "a\nb!  \nc\n" || d.Modified() {
		t.Fatalf("⌘S 整理后：%q", got)
	}
	mustText(t, tt, "已保存 t.txt · 整理了 2 行")
	wait("保存后", idle(r))
}
