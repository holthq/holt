package ui

import (
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 预览里拿表格并排放的图片（GitHub README 的常见写法）：显示成图，不是 [说明]；两列等宽并排、铺满一栏；
// 全空的表头行不画；同一张表里的字照样排（加粗也在）；不带图的表格照旧。
func TestMarkdownImageTable(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	writePNG(t, root, "a.png", 400, 200, color.RGBA{200, 60, 60, 255})
	writePNG(t, root, "b.png", 400, 200, color.RGBA{60, 60, 200, 255})
	md := "上面一段\n\n| | |\n| --- | --- |\n| ![甲图](a.png) | ![乙图](b.png) |\n| 左边说明 | **右边**说明 |\n\n" +
		"| 名字 | 说明 |\n| --- | --- |\n| 甲 | 纯文字表格 |\n"
	if err := os.WriteFile(root+"/pics.md", []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := newUI(t, ws, "")
	a.OpenFile(a.Repos[0], "pics.md", true)
	d := a.CurrentDoc()
	d.Preview = true
	wait("图片读好", func() bool {
		ra, okA := tt.Find("甲图")
		rb, okB := tt.Find("乙图")
		return okA && okB && ra.H > 0 && rb.H > 0 && ra.W/ra.H > 1.9 && rb.W/rb.H > 1.9 // 400×200；占位是一小块字
	})
	ra, _ := tt.Find("甲图")
	rb, _ := tt.Find("乙图")
	if ra.Y != rb.Y || rb.X <= ra.X+ra.W || abs32(ra.W-rb.W) > 1 {
		t.Fatalf("两张图应等宽并排：甲 %+v 乙 %+v", ra, rb)
	}
	if ra.W < 300 {
		t.Fatalf("图应铺满半栏：%+v", ra)
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, "[甲图]") || strings.Contains(s, "| ---") {
			t.Fatalf("还是源码或 [说明]：%q", s)
		}
	}
	mustText(t, tt, "左边说明")
	mustText(t, tt, "右边")
	mustText(t, tt, "纯文字表格")
	// 全空的表头行不画：图紧跟在上一段下面（段距 + 边框 + 单元格内边距，没有一行空表头的高度）
	above, _ := tt.Find("上面一段")
	if gap := ra.Y - (above.Y + above.H); gap > 24 {
		t.Fatalf("图和上一段隔了 %.0f，空表头行画出来了？", gap)
	}
}

func TestCellSource(t *testing.T) {
	for in, want := range map[string]string{
		"  ![图](a.png) ": "![图](a.png)",
		"# 不是标题":         `\# 不是标题`,
		"> 不是引用":         `\> 不是引用`,
		"- 不是列表":         `\- 不是列表`,
		"12. 不是列表":       `12\. 不是列表`,
		"---":            `\---`,
		"* * *":          `\* * *`,
		"```go":          "\\```go",
		"**加粗**":         "**加粗**",
		"*斜体*":           "*斜体*",
		"-1":             "-1",
		"3.14":           "3.14",
		"":               "",
	} {
		if got := cellSource(in); got != want {
			t.Errorf("cellSource(%q) = %q，应为 %q", in, got, want)
		}
	}
}
