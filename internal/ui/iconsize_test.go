package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sizedIcons 是把图标大小当参数往下传的函数：调用时在第几个参数（从 0 数）、一共几个参数。
// 函数里 FontSize(参数) 和按它算出来的（彩色底块上的图形 size × 0.72）不查，只要不是数字；
// 调用的地方查传进去的（外框）是不是三档之一。
var sizedIcons = map[string]struct {
	arg, nargs int
}{
	"squareButton":  {5, 8}, // squareButton(c, ic, label, tip, size, iconSize, disabled, fn)
	"fileGlyphSize": {5, 6}, // fileGlyphSize(a, c, name, on, dim, size)
	"plainGlyph":    {3, 4}, // plainGlyph(c, kind, color, size)
	"chevron":       {1, 6}, // chevron(c, size, open, name, color, toggle)
}

// TestIconSizes 扫 internal/ui 的源码：图标只有三档（iconInline / iconRow / iconTool，空态 iconEmpty），
// Icon(...).FontSize 不能写数字，squareButton / fileGlyphSize / chevron 的图标大小也只能是这几个常量。
func TestIconSizes(t *testing.T) {
	if iconInline != 11 || iconRow != 13 || iconTool != 16 {
		t.Errorf("三档是 11 / 13 / 16，现在 %v / %v / %v", iconInline, iconRow, iconTool)
	}
	tiers := map[string]bool{"iconInline": true, "iconRow": true, "iconTool": true, "iconEmpty": true}
	paths, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	n := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			var arg ast.Expr
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr: // wui.Icon(c, ic)....FontSize(x)
				if fn.Sel.Name != "FontSize" || !onIcon(fn.X) || len(call.Args) != 1 {
					return true
				}
				arg = call.Args[0]
			case *ast.Ident:
				s, ok := sizedIcons[fn.Name]
				if !ok || len(call.Args) != s.nargs {
					return true
				}
				arg = call.Args[s.arg]
			default:
				return true
			}
			if _, lit := arg.(*ast.BasicLit); !lit {
				for name := range sizedIcons {
					if inFunc(f, call, name) {
						return true // 那个函数自己的参数或按它算出来的（往下传给 plainGlyph 的也是），调用的地方另外查
					}
				}
			}
			n++
			if id, ok := arg.(*ast.Ident); !ok || !tiers[id.Name] {
				t.Errorf("%s：图标大小 %s 不是三档之一（iconInline / iconRow / iconTool，空态 iconEmpty）",
					fset.Position(arg.Pos()), exprString(arg))
			}
			return true
		})
	}
	if n < 40 {
		t.Errorf("只扫到 %d 处图标大小，扫漏了？", n)
	}
}

// onIcon 报 e 这条链是不是从 wui.Icon(...) 开始的。
func onIcon(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "wui" && sel.Sel.Name == "Icon" {
					return true
				}
				e = sel.X
				continue
			}
			return false
		case *ast.SelectorExpr:
			e = x.X
		default:
			return false
		}
	}
}

// inFunc 报 node 是不是在函数 name 里面。
func inFunc(f *ast.File, node ast.Node, name string) bool {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name && fd.Pos() <= node.Pos() && node.End() <= fd.End() {
			return true
		}
	}
	return false
}

// exprString 是报错里显示的表达式：字面量原样，别的写个大概。
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		return x.Value
	case *ast.Ident:
		return x.Name
	}
	return "（表达式）"
}

// icons/ 里每个文件都有 icon("名字") 用着：换图标时旧文件别留下，打进包里白占地方。
func TestIconFilesUsed(t *testing.T) {
	var src strings.Builder
	for _, name := range []string{"icons.go", "fileicons.go", "agents.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	files, err := iconFS.ReadDir("icons")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(f.Name(), ".svg")
		if !strings.Contains(src.String(), `icon("`+name+`")`) {
			t.Errorf("icons/%s 没人用", f.Name())
		}
	}
}
