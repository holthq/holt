package outline

// StickyMax 是粘性滚动最多吸几行。
const StickyMax = 5

// Sticky 是粘性滚动吸在编辑器顶上的那几个符号（在 Flat 里的下标，从外到内）。rowLine(k) 是编辑器从上往下第 k 行
// 显示的是文件的第几行（0 是最上面那行，折叠藏着的行不算）；folded 是折叠着的符号头所在的行，折着的不吸。
//
// 一行一行往下定：第 k 行吸的是包着 rowLine(k)（头在它之前、End 在它之后）、又在第 k-1 行那个符号里面的符号中最外层的一个，
// 没有就停；最多 max 行，多的是更里层的，不要。包不包按行号算，不按 Parent：Go 的方法挂在类型下面，但不在类型的 {} 里。
func (t *Tree) Sticky(rowLine func(row int) int, max int, folded map[int]bool) []int {
	if t == nil {
		return nil
	}
	end := func(f *Flat) int {
		if folded[f.Line] {
			return f.Line + 1
		}
		return f.End
	}
	var chain []int
	for len(chain) < max {
		l := rowLine(len(chain))
		pick := -1
		for i := range t.Flat {
			f := &t.Flat[i]
			if f.Line >= l || end(f) <= l {
				continue
			}
			if n := len(chain); n > 0 {
				o := &t.Flat[chain[n-1]]
				if i == chain[n-1] || f.Line < o.Line || f.Line == o.Line && f.Depth <= o.Depth || end(f) > end(o) {
					continue
				}
			}
			if pick < 0 || f.Line < t.Flat[pick].Line || f.Line == t.Flat[pick].Line && f.Depth < t.Flat[pick].Depth {
				pick = i
			}
		}
		if pick < 0 {
			break
		}
		chain = append(chain, pick)
	}
	return chain
}
