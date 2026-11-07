package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// GitHub 风格的 README：块级 HTML（div align=center、h1、p、strong、包着图片的链接、a）要画成界面，
// 不能把标签当文字显示；本地图片按 width 排，链接点了走 MarkdownLink。
func TestMarkdownHTMLBlocks(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	readme := `<div align="center">
  <img src="pic.png" alt="Logo" width="48" />
  <h1>DownVid</h1>
  <p><strong>把喜欢的视频，轻松保存到本地。</strong></p>
  <p>
    <a href="https://example.com/dl"><img src="pic.png" alt="Downloads" /></a>
    <a href="https://example.com/site">官网</a>
  </p>
</div>

普通段落，<b>加粗</b>和 <code>inline code</code>。
`
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# 标题\n\n"+readme), 0o644); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(0, 0, color.Black)
	f, err := os.Create(filepath.Join(root, "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	a, tt, wait := newUI(t, testutil.Workspace(t, root), t.TempDir())
	r := a.Repos[0]
	a.OpenFile(r, "README.md", true)
	wait("打开 README", func() bool { return a.CurrentDoc() != nil && !a.CurrentDoc().Loading })
	d := a.CurrentDoc()
	a.TogglePreview()
	wait("HTML 块画出来", func() bool {
		return d.Preview && tt.HasText("DownVid") && tt.HasText("官网") && !tt.HasText("<div")
	})
	for _, tag := range []string{"<div", "<h1>", "<p>", "<strong>", "<code>", "<img"} {
		if tt.HasText(tag) {
			t.Fatalf("HTML 标签当文字显示了：%q", tag)
		}
	}
	if !tt.HasText("把喜欢的视频，轻松保存到本地。") {
		t.Fatal("粗体段落的文字没显示")
	}
	// 图片：按 width=48 排，读好以后是 48×48 的位图（没读好时是 alt 的小占位）
	if _, ok := tt.Find("Logo"); !ok {
		t.Fatal("Logo 图片没出现")
	}
	wait("图片读好", func() bool {
		rc, ok := tt.Find("Logo")
		return ok && rc.W >= 40 && rc.H >= 40
	})
	// 链接是 mygo 的 Link（点了走系统浏览器，和 Markdown 的 http 链接一样）；这里只确认它画出来了
	if rc, ok := tt.Find("官网"); !ok || rc.W <= 0 {
		t.Fatal("没找到官网链接")
	}
}
