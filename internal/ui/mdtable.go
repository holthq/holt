package ui

import (
	"strings"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// 这个文件是 Markdown 预览里带图片的表格。mygo 的表格单元格只排字，图片显示成 [说明]；
// README 常拿表格并排放截图（GitHub 上显示得出来），所以这种表格自己搭：列等宽、铺满一栏，
// 每个单元格是一个小的 markdown.View（只有图片的单元格就显示图片）。不带图片的表格照旧给 mygo。

// mdHasTable 粗看源码里可能有带图片的表格：没有就不挂 Block 钩子（挂了每个块外面都多包一层）。
func mdHasTable(src string) bool {
	return strings.Contains(src, "|") && strings.Contains(src, "![")
}

// mdTableKey 是带图表格上留着的单元格选项（里面有图片缓存，每帧新建就会一直重读图片）。
type mdTableKey struct{}

// mdImageTable 在第 i 块是带图片的表格时画出来，返回 true；不是返回 false，交给 mygo。
// 全空的表头行（并排放图时常写 | | |）不画。
func mdImageTable(c *wui.Context, o *markdown.Options, doc *parser.Document, i int) bool {
	b := doc.Block(i)
	if b.Kind != parser.Table || !tableHasImage(doc, i) {
		return false
	}
	th := markdown.DefaultTheme(c.Theme())
	if o.Theme != nil {
		th = o.Theme
	}
	em := func(v float32) float32 { return v * th.FontSize }
	padX, padY := em(th.TableCellPadding[0]), em(th.TableCellPadding[1])
	src := doc.Source()
	cols := len(b.Align)
	grid := wui.Column(c).AlignSelf(wui.Stretch).Border(1, th.TableBorder).Radius(em(0.3)).Clip().Role(wui.RoleTable)
	co := *wui.Local(grid, mdTableKey{}, func() *markdown.Options { return &markdown.Options{ShareSelection: true} })
	co.Theme, co.Extensions, co.LoadImage, co.OnLink, co.InlineMath = o.Theme, o.Extensions, o.LoadImage, o.OnLink, o.InlineMath
	grid.Children(func() {
		n := 0
		for r := i + 1; r < b.Next; r = doc.Block(r).Next {
			row := doc.Block(r)
			if row.Header && rowEmpty(doc, r) {
				continue
			}
			rowEl := wui.Row(c).Role(wui.RoleRow).AlignItems(wui.Stretch)
			switch {
			case row.Header:
				rowEl.Background(th.TableHeader)
			case n%2 == 1 && th.TableStripe.A > 0:
				rowEl.Background(th.TableStripe)
			}
			if n > 0 {
				rowEl.BorderWidth(1, 0, 0, 0).BorderColor(th.TableBorder)
			}
			rowEl.Children(func() {
				k := 0
				for cell := r + 1; cell < row.Next && k < cols; cell = doc.Block(cell).Next {
					role := wui.RoleCell
					box := wui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(padY, padX)
					if row.Header {
						role = wui.RoleColumnHeader
						box.FontWeight(th.StrongWeight)
					}
					box.Role(role)
					if k > 0 {
						box.BorderWidth(0, 0, 0, 1).BorderColor(th.TableBorder)
					}
					switch b.Align[k] {
					case parser.AlignCenter:
						box.TextAlign(wui.Center).AlignItems(wui.Center)
					case parser.AlignRight:
						box.TextAlign(wui.End).AlignItems(wui.End)
					}
					cb := doc.Block(cell)
					text := cellSource(src[cb.Start:cb.End])
					box.Children(func() { markdown.View(c, text, co) })
					k++
				}
			})
			n++
		}
	})
	return true
}

// tableHasImage 是表格 i 的某个单元格里有图片。
func tableHasImage(doc *parser.Document, i int) bool {
	for r := i + 1; r < doc.Block(i).Next; r = doc.Block(r).Next {
		for cell := r + 1; cell < doc.Block(r).Next; cell = doc.Block(cell).Next {
			for _, in := range doc.Inlines(cell) {
				if in.Kind == parser.Image {
					return true
				}
			}
		}
	}
	return false
}

// rowEmpty 是第 r 行的单元格全是空的。
func rowEmpty(doc *parser.Document, r int) bool {
	for cell := r + 1; cell < doc.Block(r).Next; cell = doc.Block(cell).Next {
		if len(doc.Inlines(cell)) > 0 {
			return false
		}
	}
	return true
}

// cellSource 把单元格的源码变成单独解析也还是一段字的 Markdown：开头像块的标记（# 标题、> 引用、- 列表、
// 1. 列表、--- 分隔线、``` 代码块）前面加反斜杠，表格里它们本来就只是字。
func cellSource(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	c0 := s[0]
	spaceAfter := func(j int) bool { return j >= len(s) || s[j] == ' ' || s[j] == '\t' }
	j := 0
	for j < len(s) && j < 9 && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	switch {
	case j > 0 && j < len(s) && (s[j] == '.' || s[j] == ')') && spaceAfter(j+1):
		return s[:j] + `\` + s[j:]
	case c0 == '#' || c0 == '>':
		return `\` + s
	case (c0 == '-' || c0 == '+' || c0 == '*') && spaceAfter(1):
		return `\` + s
	case strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~"):
		return `\` + s
	case strings.ContainsRune("-*_", rune(c0)) && strings.Count(s, s[:1]) >= 3 && strings.Trim(s, s[:1]+" \t") == "":
		return `\` + s
	}
	return s
}
