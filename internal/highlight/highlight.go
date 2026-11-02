// Package highlight 是大文件（docs.Doc.Big）的语法高亮：一个 goroutine 从头往下一行一行地分词，记下每行开头的状态，
// 主线程只按记好的状态分看得见的那几行。mygo 自带的高亮器在主线程上每帧追 4 ms，64 MB 的文件要追两三秒。
package highlight

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui/editor"
)

// maxTokenize 是主线程上分词的最长一行，更长的不上色（和 mygo 的 tokenHighlighter 一样）。
const maxTokenize = 10000

var (
	mu    sync.Mutex
	langs = map[*editor.Language]*editor.Language{}

	running atomic.Int64 // 还在算的 goroutine（测试看它们都退出了）
)

// Running 是还在后台算的有几个。
func Running() int { return int(running.Load()) }

// For 是 name 的语言换成后台高亮的一份（其余照 mygo 的，Name、缩进、注释、括号都一样）；mygo 不认识的为 nil，
// 没有 Tokenizer 或自带 Highlighter 的原样返回。同一种语言每次返回同一个指针（Buffer 按指针认语言换没换）。
func For(name string) *editor.Language { return Wrap(editor.LanguageFor(name)) }

// Wrap 和 For 一样，换的是 base 这种语言（状态栏手动选的）；nil 为 nil。
func Wrap(base *editor.Language) *editor.Language {
	if base == nil || base.Tokenizer == nil || base.Highlighter != nil {
		return base
	}
	mu.Lock()
	defer mu.Unlock()
	if l := langs[base]; l != nil {
		return l
	}
	l := *base
	t := base.Tokenizer
	l.Highlighter = func(text editor.Snapshot, invalidate func()) editor.Highlighter {
		return New(t, text, invalidate)
	}
	langs[base] = &l
	return &l
}

// Highlighter 是一份文字的后台高亮器。方法都在主线程上调（mygo 的规矩），后台的 run 只写自己的 states。
type Highlighter struct {
	t   editor.Tokenizer
	inv func()
	run *run
}

// run 是一次从某行往下算到底：states[i] 是第 i 行开头的状态，前 done 行的是算好的（done 用原子量发布，
// 后台先写 states 再 Store，主线程先 Load 再读，前 done 个后台不会再写）。
type run struct {
	text   editor.Snapshot
	states []uint64
	done   atomic.Int64
	want   atomic.Int64 // 编辑器最近要到的行：后台越过它时 invalidate 一次
	stop   atomic.Bool  // Edit 换了新的 run，旧的停下
	took   atomic.Int64 // 算完了：花了多少纳秒（至少 1）
}

// New 在后台从头分 text 的词，算完自己退出；invalidate 在越过编辑器要的行和算完时各调一次（从后台）。
func New(t editor.Tokenizer, text editor.Snapshot, invalidate func()) *Highlighter {
	h := &Highlighter{t: t, inv: invalidate}
	h.start(text, nil, 0)
	return h
}

// start 开一次新的 run：从第 from 行往下算，前 from+1 行的状态照 keep。
func (h *Highlighter) start(text editor.Snapshot, keep []uint64, from int) {
	n := max(1, text.LineCount())
	r := &run{text: text, states: make([]uint64, n)}
	from = min(from, n-1, max(0, len(keep)-1))
	copy(r.states, keep[:min(len(keep), from+1)])
	r.done.Store(int64(from + 1))
	r.want.Store(-1)
	if h.run != nil {
		r.want.Store(h.run.want.Load())
	}
	h.run = r
	running.Add(1)
	go r.work(h.t, from, h.inv)
}

// work 从第 from 行分到最后一行。每 64 行发布一次算到哪了。
func (r *run) work(t editor.Tokenizer, from int, inv func()) {
	defer running.Add(-1)
	began := time.Now()
	n := len(r.states)
	i := from // 正在分的行
	state := r.states[from]
	fired := int64(-1)
	var toks []editor.Token
	var carry []byte // 跨了两块的那一行的前半截
	line := func(s string) bool {
		toks, state = t.Tokenize(s, state, toks[:0])
		i++
		if i >= n {
			return false
		}
		r.states[i] = state
		if i&63 == 0 {
			r.done.Store(int64(i + 1))
			if w := r.want.Load(); w >= 0 && w != fired && int64(i) > w {
				fired = w
				inv()
			}
			return !r.stop.Load()
		}
		return true
	}
	r.text.Chunks(r.text.LineStart(from), r.text.Len(), func(s string) bool {
		for {
			j := strings.IndexByte(s, '\n')
			if j < 0 {
				carry = append(carry, s...)
				return true
			}
			l := s[:j]
			if len(carry) > 0 {
				l = string(append(carry, l...))
				carry = carry[:0]
			}
			if !line(l) {
				return false
			}
			s = s[j+1:]
		}
	})
	if r.stop.Load() {
		return
	}
	r.done.Store(int64(n))
	r.took.Store(max(1, int64(time.Since(began))))
	inv()
}

// Edit 跟上一次编辑（大文件只读，基本不会有）：停下在算的，从改动的第一行接着算。
func (h *Highlighter) Edit(old, new editor.Snapshot, changes []editor.Change) {
	if len(changes) == 0 {
		return
	}
	from := new.LineOf(changes[0].NewStart)
	for _, c := range changes[1:] {
		from = min(from, new.LineOf(c.NewStart))
	}
	r := h.run
	r.stop.Store(true)
	done := int(r.done.Load())
	h.start(new, r.states[:done], min(from, done-1))
}

// Highlight 只记下编辑器要到哪一行，总是返回 true：返回 false 编辑器会每帧再来、当成还在算；
// 没算到的行由 Tokens 返回 final 为假，编辑器自己每帧重问看得见的那几行，算到了就换上颜色。
func (h *Highlighter) Highlight(last int, budget time.Duration) bool {
	h.run.want.Store(int64(last))
	return true
}

// Tokens 分第 i 行：算到了的按记好的状态分、final 为真；没算到的先按行首是代码（状态 0）猜一份、final 为假。
func (h *Highlighter) Tokens(i int, text string, dst []editor.Token) ([]editor.Token, bool) {
	r := h.run
	done := i < int(r.done.Load())
	if len(text) > maxTokenize || i >= len(r.states) {
		return dst, done
	}
	var state uint64
	if done {
		state = r.states[i]
	}
	dst, _ = h.t.Tokenize(text, state, dst)
	return dst, done
}

// Done 是后台算完了没有、算了多久（测试和量用）。
func (h *Highlighter) Done() (bool, time.Duration) {
	took := h.run.took.Load()
	return took > 0, time.Duration(took)
}
