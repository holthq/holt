package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 临时仓库里的 Markdown：⇧⌘V 切到预览，表格和本地图片显示出来；点链接——网址交给系统浏览器（测试里换掉）、
// 相对路径的 Markdown 直接开成预览、带 :行 的文件在编辑器里跳过去、#标题 滚到那个标题、
// 找不到的文件和不认识的协议给提示；再按 ⇧⌘V 回到源码、焦点给编辑器。预览状态随会话保存。
func TestMarkdownPreview(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	var b strings.Builder
	b.WriteString("# 指南\n\n[网站](https://example.com/x) · [另一篇](other.md#第二节) · [代码](../a.txt:3) · " +
		"[没有的](nope.md) · [奇怪的](vscode://open) · [跳到结尾](#结尾一节)\n\n" +
		"| 名字 | 说明 |\n| --- | --- |\n| 甲 | 单元格一 |\n| 乙 | 单元格二 |\n\n![示意图](pic.png)\n\n")
	for i := range 80 {
		b.WriteString("第 " + itoa(i+1) + " 段，撑开页面。\n\n")
	}
	b.WriteString("## 结尾一节\n\n")
	for i := range 40 {
		b.WriteString("结尾第 " + itoa(i+1) + " 段。\n\n")
	}
	write := func(rel, s string) {
		p := filepath.Join(root, "docs", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("guide.md", b.String())
	write("other.md", "# 另一篇\n\n另一篇的正文\n\n## 第二节\n\n第二节的正文\n")
	img := image.NewRGBA(image.Rect(0, 0, 300, 30))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(0, 0, color.Black)
	f, err := os.Create(filepath.Join(root, "docs", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	var opened []string
	a.OpenURL = func(u string) error { opened = append(opened, u); return nil }
	r := a.Repos[0]
	a.OpenFile(r, "docs/guide.md", true)
	a.FocusDoc = true
	wait("打开 guide.md", func() bool { return tt.Focused("编辑 guide.md") })
	guide := a.CurrentDoc()
	mustText(t, tt, "空格 2") // 状态栏里的缩进（按语言：Markdown 是 2 个空格）

	tt.Key(wui.Shift|wui.Cmd, wui.KeyV)
	wait("预览", func() bool { return guide.Preview && tt.HasText("单元格二") && tt.HasText("网站") })
	if _, ok := tt.Find("预览 guide.md"); !ok {
		t.Fatal("没有预览区")
	}
	if tt.HasText("| 名字") {
		t.Fatal("表格还是源码")
	}
	wait("图片读好", func() bool {
		rc, ok := tt.Find("示意图")
		return ok && rc.H > 0 && rc.W/rc.H > 8 // 300×30 的图；没读好时是一小块写着说明的占位
	})

	// 网址交给系统浏览器
	mustClick(t, tt, "网站")
	if !slices.Equal(opened, []string{"https://example.com/x"}) {
		t.Fatalf("打开的网址 %q", opened)
	}

	// #标题：滚到结尾那一节
	if rc, ok := tt.Find("结尾一节"); ok && rc.H > 0 {
		t.Fatalf("结尾一节一开始就在窗口里 %+v", rc)
	}
	mustClick(t, tt, "跳到结尾")
	wait("滚到结尾", func() bool {
		rc, ok := tt.Find("结尾一节")
		return ok && rc.H > 0 && rc.Y < 160 && guide.Anchor == "" // 滚到最上面；滚出去的元素框是 0
	})

	// 找不到的文件、不认识的协议：提示，不开
	toTop(tt, wait, "没有的")
	mustClick(t, tt, "没有的")
	wait("找不到的提示", func() bool { return tt.HasText("找不到 nope.md") })
	mustClick(t, tt, "奇怪的")
	wait("协议的提示", func() bool { return tt.HasText("不打开 vscode: 链接") })
	if len(opened) != 1 || len(a.Docs) != 1 {
		t.Fatalf("不该开：网址 %q，标签 %d", opened, len(a.Docs))
	}

	// 相对路径的 Markdown：开成预览，滚到 #第二节
	mustClick(t, tt, "另一篇")
	wait("另一篇的预览", func() bool {
		d := a.CurrentDoc()
		return d != guide && d.Rel == "docs/other.md" && d.Preview && tt.HasText("第二节的正文")
	})

	// 带行号的文件：编辑器里跳过去
	a.SelectDoc(slices.Index(a.Docs, guide))
	wait("回到 guide 的预览", func() bool { return tt.HasText("单元格二") })
	toTop(tt, wait, "代码")
	mustClick(t, tt, "代码")
	wait("a.txt 第 3 行", func() bool {
		d := a.CurrentDoc()
		p, ok := d.Cursor()
		return d.Rel == "a.txt" && ok && p.Line == 2 && tt.Focused("编辑 a.txt")
	})

	// 预览状态随会话保存；⇧⌘V 回到源码，焦点给编辑器
	a.SaveState()
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var previews []string
	for _, x := range st.Sessions[ws].Tabs {
		if x.Preview {
			previews = append(previews, filepath.Base(x.Path))
		}
	}
	if !slices.Equal(previews, []string{"guide.md", "other.md"}) {
		t.Fatalf("存下来的预览 %q", previews)
	}
	a.SelectDoc(slices.Index(a.Docs, guide))
	wait("guide 的预览", func() bool { return tt.HasText("单元格二") })
	tt.Key(wui.Shift|wui.Cmd, wui.KeyV)
	wait("回到源码", func() bool { return !guide.Preview && tt.Focused("编辑 guide.md") })
	if a.Exec("view.markdownPreview"); !guide.Preview {
		t.Fatal("命令没切")
	}
	a.SelectDoc(slices.IndexFunc(a.Docs, func(d *docs.Doc) bool { return d.Rel == "a.txt" }))
	tt.Frame()
	if a.CanRun(app.Find("view.markdownPreview")) {
		t.Fatal("不是 Markdown 也能预览")
	}
}

// 软换行（⌥Z，编辑器有焦点时也能按）和显示空白字符：改了马上套到编辑器上，存进 state.json。
func TestEditorDisplayOptions(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	r := a.Repos[0]
	a.OpenFile(r, "a.txt", true)
	a.FocusDoc = true
	wait("打开 a.txt", func() bool { return tt.Focused("编辑 a.txt") })
	d := a.CurrentDoc()
	text := d.Buf.String()

	tt.Key(wui.Alt, wui.KeyZ)
	wait("软换行", func() bool { return a.St.Settings.SoftWrap && d.St.SoftWrap })
	if d.Buf.String() != text {
		t.Fatal("⌥Z 往文件里打了字")
	}
	a.Exec("view.whitespace")
	tt.Frame()
	if d.St.Whitespace != editor.WhitespaceAll {
		t.Fatalf("空白字符 %v", d.St.Whitespace)
	}
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Settings.SoftWrap || st.Settings.Whitespace != "all" {
		t.Fatalf("没存：%+v", st.Settings)
	}
	tt.Key(wui.Alt, wui.KeyZ)
	wait("关软换行", func() bool { return !a.St.Settings.SoftWrap && !d.St.SoftWrap })

	// 缩进：纯文本按语言是 4 个空格；设置里改成 8 个空格，状态栏和编辑器马上跟上
	mustText(t, tt, "空格 4")
	a.St.Settings.Indent = "8"
	tt.Frame()
	mustText(t, tt, "空格 8")
	if d.St.TabSize != 8 || d.St.Indent != editor.IndentSpaces {
		t.Fatalf("缩进 %d %v", d.St.TabSize, d.St.Indent)
	}
}

// toTop 把预览滚回最上面，等 s 露出来（滚出去的元素框是 0）。
func toTop(tt *wui.Tester, wait func(string, func() bool), s string) {
	tt.Move(700, 400)
	tt.Scroll(700, 400, 0, -100000)
	wait("滚回顶上", func() bool { rc, ok := tt.Find(s); return ok && rc.H > 0 && rc.Y < 300 })
}
