package highlight

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"
)

// source 是至少 n 字节的 Go 代码，有跨行的块注释和原始字符串（行首的状态不是 0），还有一行比 maxTokenize 长的。
func source(n int) string {
	var b strings.Builder
	b.WriteString("package p\n\n")
	for i := 0; b.Len() < n; i++ {
		switch i % 40 {
		case 3:
			b.WriteString("/* 块注释\n   第二行 \"不是字符串\n*/ x := 1\n")
		case 11:
			b.WriteString("var raw = `第一行\n第二行 // 不是注释\n`\n")
		case 23:
			fmt.Fprintf(&b, "var long = %q\n", strings.Repeat("x", maxTokenize))
		default:
			fmt.Fprintf(&b, "func f%d() int { return %d } // 注释\n", i, i)
		}
	}
	return b.String()
}

func goTokenizer(t *testing.T) editor.Tokenizer {
	t.Helper()
	l := editor.LanguageFor("a.go")
	if l == nil || l.Tokenizer == nil {
		t.Fatal("mygo 不认识 .go")
	}
	return l.Tokenizer
}

func waitDone(t *testing.T, h *Highlighter) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if done, _ := h.Done(); done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("后台没算完")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for Running() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("还有 %d 个 goroutine 没退出", Running())
		}
		time.Sleep(time.Millisecond)
	}
}

// same 比较两个高亮器每一行的词和 final。
func same(t *testing.T, text editor.Snapshot, got, want editor.Highlighter) {
	t.Helper()
	var a, b []editor.Token
	for i := range text.LineCount() {
		line := text.Line(i)
		var fa, fb bool
		a, fa = got.Tokens(i, line, a[:0])
		b, fb = want.Tokens(i, line, b[:0])
		if fa != fb || !slices.Equal(a, b) {
			t.Fatalf("第 %d 行 %.40q：final %v / %v，词 %v / %v", i, line, fa, fb, a, b)
		}
	}
}

func reference(t editor.Tokenizer, text editor.Snapshot) editor.Highlighter {
	h := editor.NewTokenHighlighter(t, text)
	for !h.Highlight(text.LineCount()-1, time.Second) {
	}
	return h
}

// 后台算完的每行状态和 mygo 自带的（主线程上追的）一样：块注释、原始字符串跨行，行跨了 rope 的块，过长的行不上色；
// 没算到的行 final 为假；Highlight 总是 true；算完 invalidate 至少一次，goroutine 退出。
func TestMatchesTokenHighlighter(t *testing.T) {
	tok := goTokenizer(t)
	text := editor.NewBuffer(source(300 << 10)).Snapshot()
	var inv atomic.Int64
	h := New(tok, text, func() { inv.Add(1) })
	if !h.Highlight(text.LineCount()-1, 0) {
		t.Error("Highlight 应总是返回 true")
	}
	waitDone(t, h)
	waitIdle(t)
	if inv.Load() == 0 {
		t.Error("算完应 invalidate")
	}
	same(t, text, h, reference(tok, text))

	// 空的、只有一行没换行的
	for _, s := range []string{"", "package p", "/* 没关上"} {
		snap := editor.NewBuffer(s).Snapshot()
		h := New(tok, snap, func() {})
		waitDone(t, h)
		same(t, snap, h, reference(tok, snap))
	}
	waitIdle(t)
}

// 没算到的行：按状态 0 猜一份词（和 mygo 的一样），final 为假。
func TestTokensBeforeDone(t *testing.T) {
	tok := goTokenizer(t)
	text := editor.NewBuffer(source(64 << 10)).Snapshot()
	h := &Highlighter{t: tok, inv: func() {}}
	r := &run{text: text, states: make([]uint64, text.LineCount())}
	r.done.Store(1)
	h.run = r // 不开 goroutine：只看第 0 行算到了
	if _, final := h.Tokens(0, text.Line(0), nil); !final {
		t.Error("第 0 行总是算到了的")
	}
	toks, final := h.Tokens(5, text.Line(5), nil)
	if final || len(toks) == 0 {
		t.Errorf("没算到的行应先猜一份词、final 为假：%v %v", toks, final)
	}
}

// Edit：停下旧的、从改动那行接着算，结果和整份重算的一样；改得很快（旧的还没算完）也一样；旧的 goroutine 都退出。
func TestEdit(t *testing.T) {
	tok := goTokenizer(t)
	buf := editor.NewBuffer(source(2 << 20))
	text := buf.Snapshot()
	h := New(tok, text, func() {})
	edit := func(start, end int, s string) {
		old := buf.Snapshot()
		buf.Edit(editor.Edit{Start: start, End: end, Text: s})
		h.Edit(old, buf.Snapshot(), []editor.Change{{OldStart: start, OldEnd: end, NewStart: start, NewEnd: start + len(s)}})
	}
	// 马上改（旧的多半还在算）：在第 100 行开头开一个块注释，后面全成了注释
	at := text.LineStart(100)
	edit(at, at, "/* ")
	// 再在后面关上
	at2 := buf.Snapshot().LineStart(5000)
	edit(at2, at2, "*/")
	waitDone(t, h)
	waitIdle(t)
	now := buf.Snapshot()
	same(t, now, h, reference(tok, now))

	// 算完之后再改：删掉开头那个块注释
	edit(at, at+3, "")
	waitDone(t, h)
	waitIdle(t)
	now = buf.Snapshot()
	same(t, now, h, reference(tok, now))
}

// For：同一种语言每次同一个指针，名字、缩进照 mygo 的；不认识的为 nil，没有 Tokenizer 的原样。
func TestFor(t *testing.T) {
	a, b := For("a.go"), For("b.go")
	base := editor.LanguageFor("a.go")
	if a == nil || a != b || a == base || a.Highlighter == nil || a.Tokenizer == nil {
		t.Fatalf("For(.go)：%p %p base %p", a, b, base)
	}
	if a.Name != base.Name || a.TabSize != base.TabSize || a.HardTabs != base.HardTabs {
		t.Error("名字、缩进应照 mygo 的")
	}
	if base.Highlighter != nil {
		t.Error("不该改 mygo 的那份")
	}
	if For("a.unknown-ext") != nil {
		t.Error("不认识的应为 nil")
	}
}
