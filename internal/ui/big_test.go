package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/highlight"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// bigSource 是至少 n 字节、像 Go 代码的文字：多是短行，每 16 行一条 240 字左右的长行（换行开着时会折）。
func bigSource(n int) string {
	var b strings.Builder
	b.Grow(n + 512)
	b.WriteString("package big\n\n")
	for i := 0; b.Len() < n; i++ {
		switch i % 16 {
		case 0:
			fmt.Fprintf(&b, "// 第 %d 段：一行注释\n", i/16)
		case 7:
			fmt.Fprintf(&b, "var msg%d = %q\n", i, strings.Repeat("a long line that wraps ", 10))
		case 15:
			b.WriteString("}\n\n")
		default:
			fmt.Fprintf(&b, "\tx%d := f(%d) + g(\"s\")\n", i%16, i)
		}
	}
	return b.String()
}

// 9 MB 的文件：编辑区上面一条淡色说明（大小、只读、少了什么），大纲说文件太大；⌘F 照样能找。超过 64 MB 的只有说明。
// 2–8 MB 跟踪了的：能编辑，只淡淡一句不和 HEAD 对比。
func TestBigFileBar(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	text := bigSource(docs.MaxEditBytes + 1<<20)
	os.WriteFile(filepath.Join(root, "big.go"), []byte(text), 0o644)
	f, err := os.Create(filepath.Join(root, "huge.log"))
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(docs.MaxViewBytes + 1)
	f.Close()
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	d := a.OpenFile(r, "big.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()
	mustText(t, tt, i18n.Tf("大文件（%s），只读：不和 HEAD 对比，没有大纲、冲突检查和 blame", docs.HumanSize(int64(len(text)))))
	if !d.St.ReadOnly || tt.HasText("转成 UTF-8…") {
		t.Error("大文件应只读、没有转换按钮")
	}
	a.ShowSidebar(workspace.ModeOutline)
	tt.Frame()
	mustText(t, tt, "文件太大，没有大纲")
	d.St.Find("msg7 ")
	if n, _ := d.St.FindMatches(); n != 1 {
		t.Errorf("⌘F 找到 %d 处，应为 1", n)
	}

	if tt.HasText("文件较大，不和 HEAD 对比") {
		t.Error("大文件的说明条里已经说了不对比")
	}

	h := a.OpenFile(r, "huge.log", true)
	wait("读好", func() bool { return !h.Loading && h.Info != "" })
	tt.Frame()
	mustText(t, tt, "文件超过 64 MB，用默认应用打开")
	if h.Buf != nil {
		t.Error("超过 64 MB 的不该读进来")
	}

	repo := testutil.NewRepo(t, "nobase")
	mid := bigSource(docs.MaxDiffBaseBytes + 1<<10)
	repo.Write("mid.go", mid)
	repo.Commit("中等")
	repo.Write("mid.go", mid+"// 改过\n")
	a2, tt2, wait2 := newUI(t, testutil.Workspace(t, repo.Root), "")
	m := a2.OpenFile(a2.Repos[0], "mid.go", true)
	wait2("读好", func() bool { return m.Buf != nil && !m.Loading })
	tt2.Frame()
	mustText(t, tt2, "文件较大，不和 HEAD 对比")
	if m.St.ReadOnly || m.St.DiffBase != nil || tt2.HasText(i18n.Tf("大文件（%s），只读：不和 HEAD 对比，没有大纲、冲突检查和 blame", docs.HumanSize(m.Size))) {
		t.Error("2–8 MB 的应能编辑、不对比、没有大文件说明条")
	}
}

// measureUI 和 newUI 一样建 App 和 Tester，但一帧一帧地推：run 跑掉排着的回调、
// 推一帧（有回调跑过就像窗口那样重建一次），记下每个回调和每帧在主线程上花的时间。
type measureUI struct {
	a      *app.App
	tt     *wui.Tester
	q      testutil.Queue
	ctx    *wui.Context
	step   time.Duration // 最长的一个回调
	frame  time.Duration // 最长的一帧
	frames []time.Duration
}

func newMeasureUI(t *testing.T, ws string) *measureUI {
	t.Helper()
	a, err := app.New(ws, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &measureUI{a: a, q: testutil.NewQueue()}
	a.Update = m.q.Post
	a.OpenURL = func(string) error { return nil }
	a.ShowPath = func(string) error { return nil }
	a.InFinder = func(string) {}
	t.Cleanup(a.Close)
	view := View(a)
	m.tt = wui.NewTester(func(c *wui.Context) { m.ctx = c; view(c) }, 1440, 880)
	m.tt.SetLocale(i18n.Locale())
	return m
}

// once 跑掉排着的回调、推一帧；没有要画的就歇一下（等后台）。
func (m *measureUI) once() {
	ran := false
	for {
		select {
		case fn := <-m.q:
			s := time.Now()
			fn()
			m.step = max(m.step, time.Since(s))
			ran = true
			continue
		default:
		}
		break
	}
	if ran {
		m.ctx.Invalidate()
	}
	if !m.tt.FramePending() {
		time.Sleep(time.Millisecond)
		return
	}
	s := time.Now()
	m.tt.Advance(time.Second / 60)
	d := time.Since(s)
	m.frame = max(m.frame, d)
	m.frames = append(m.frames, d)
}

func (m *measureUI) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等 %s 超时", what)
		}
		m.once()
	}
}

func (m *measureUI) reset() { m.step, m.frame, m.frames = 0, 0, m.frames[:0] }

func heapMB() float64 {
	runtime.GC()
	runtime.GC()
	var s runtime.MemStats
	runtime.ReadMemStats(&s)
	return float64(s.HeapAlloc) / (1 << 20)
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond)) }

func avg(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	return sum / time.Duration(len(ds))
}

// TestMeasureBigFile 量大文件：读到第一帧、主线程上最长的一步、打开马上 ⌘↓ 跳到末尾（换行开 / 关）几帧到位、多久有颜色
// （不再要帧、后台高亮算完）、追的期间每帧多久、重读换缓冲区、关掉后的堆；还有能编辑的文件（0.5–8 MB、改一处 / 千分之一 /
// 百分之一的行）带 HEAD 对比（2 MB 以上没有了）的第一帧，和后台高亮本身的快慢。WORKBENCH_MEASURE=1 才跑。
func TestMeasureBigFile(t *testing.T) {
	if os.Getenv("WORKBENCH_MEASURE") == "" {
		t.Skip("WORKBENCH_MEASURE=1 才量")
	}
	r := testutil.NewRepo(t, "big")
	r.Write("small.go", "package small\n")
	sizes := []int{512, 1024, 2048, 4096, 8192 - 256} // KB；改过的还在 8 MB 以内
	edits := []struct {
		name  string
		every int // 每隔几行改一行；0 是只改一处
	}{{"一处", 0}, {"千分之一", 1000}, {"百分之一", 100}}
	for _, kb := range sizes {
		for j := range edits {
			r.Write(fmt.Sprintf("e%d_%d.go", kb, j), bigSource(kb<<10))
		}
	}
	r.Commit("能编辑的")
	for _, kb := range sizes {
		for j, e := range edits {
			p := filepath.Join(r.Root, fmt.Sprintf("e%d_%d.go", kb, j))
			data, _ := os.ReadFile(p)
			lines := strings.Split(string(data), "\n")
			if e.every == 0 {
				lines[len(lines)/2] += " // 改过"
			}
			for i := 5; e.every > 0 && i < len(lines); i += e.every {
				lines[i] += " // 改过"
			}
			os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
			if j == 0 {
				os.WriteFile(filepath.Join(r.Root, fmt.Sprintf("n%d.go", kb)), data, 0o644)
			}
		}
	}
	for _, mb := range []int{8, 32, 64} {
		os.WriteFile(filepath.Join(r.Root, fmt.Sprintf("big%d.go", mb)), []byte(bigSource(mb<<20-4096)), 0o644)
		os.WriteFile(filepath.Join(r.Root, fmt.Sprintf("big%d.log", mb)), []byte(bigSource(mb<<20-4096)), 0o644)
	}
	m := newMeasureUI(t, testutil.Workspace(t, r.Root))
	a := m.a
	repo := a.Repos[0]
	a.RefreshRepo(repo, app.WhyManual)
	m.until(t, "仓库读完", func() bool { return repo.Loaded && !repo.Loading })
	// 先开一个小文件，量出一帧的底（整个界面重建、软件画 1440×880）
	small := a.OpenFile(repo, "small.go", true)
	m.until(t, "小文件", func() bool { return small.Buf != nil && !small.Loading })
	for range 30 {
		m.ctx.Invalidate()
		m.once()
	}
	t.Logf("一帧的底（小文件、整个界面重建、软件画）：平均 %s，最长 %s", ms(avg(m.frames)), ms(m.frame))
	a.CloseDoc(small)

	open := func(name string) (*docs.Doc, time.Duration) {
		m.reset()
		t0 := time.Now()
		d := a.OpenFile(repo, name, true)
		m.until(t, name, func() bool { return d.Buf != nil && !d.Loading })
		m.ctx.Invalidate()
		m.once()
		return d, time.Since(t0)
	}
	closeDoc := func(d *docs.Doc) {
		a.CloseDoc(d)
		for range 3 {
			m.ctx.Invalidate()
			m.once()
		}
	}

	for _, kb := range sizes {
		names := []string{fmt.Sprintf("n%d.go", kb)}
		for j := range edits {
			names = append(names, fmt.Sprintf("e%d_%d.go", kb, j))
		}
		for j, name := range names {
			what := "没跟踪、没有对比"
			if j > 0 {
				what = "HEAD 对比，改" + edits[j-1].name
			}
			d, first := open(name)
			t.Logf("%.1f MB 能编辑（%s，base=%v big=%v）：读到第一帧 %s，最长回调 %s，最长一帧 %s",
				float64(kb)/1024, what, d.St.DiffBase != nil, d.Big, ms(first), ms(m.step), ms(m.frame))
			closeDoc(d)
		}
	}

	for _, mb := range []int{8, 32, 64} {
		for _, ext := range []string{"log", "go"} {
			name := fmt.Sprintf("big%d.%s", mb, ext)
			for _, wrap := range []bool{false, true} {
				a.St.Settings.SoftWrap = wrap
				h0 := heapMB()
				d, first := open(name)
				t.Logf("%s（换行 %v）：读到第一帧 %s，最长回调 %s，最长一帧 %s", name, wrap, ms(first), ms(m.step), ms(m.frame))
				// 马上 ⌘↓：一帧一帧推到不再要帧（1800 帧和 30 s 都过了也停：后台高亮在算时帧很便宜，1800 帧不到 0.2 s），
				// 记下最后一行在屏幕上的位置哪一帧起不再变、后台高亮哪时算完
				last := d.Buf.LineCount() - 1
				d.St.GoTo(editor.Pos{Line: last})
				m.reset()
				m.ctx.Invalidate()
				settled, prev := 0, -1.0
				jump := time.Now()
				var bgDone time.Duration // 后台高亮算完（highlight.Running 归零）离 ⌘↓ 多久；没见它在算为 0
				seen := false
				for i := 0; (i < 1800 || time.Since(jump) < 30*time.Second) && m.tt.FramePending(); i++ {
					m.once()
					if n := highlight.Running(); n > 0 {
						seen = true
					} else if seen && bgDone == 0 {
						bgDone = time.Since(jump)
					}
					first, end := d.St.VisibleLines()
					y := d.St.LineTop(last) - d.St.ScrollY()
					if !(first <= last && last <= end) {
						y = -1
					}
					if y != prev || y < 0 {
						settled = len(m.frames)
					}
					prev = y
				}
				rest := m.frames
				if len(rest) > 1 {
					rest = rest[1:]
				}
				var worst time.Duration
				for _, f := range rest {
					worst = max(worst, f)
				}
				idle := time.Since(jump)
				t.Logf("  ⌘↓：第 %d 帧到位（60 Hz 约 %.0f ms），%d 帧、%s 后不再要帧（%v）；后台高亮算完 %s；跳的那帧 %s，之后每帧平均 %s、最长 %s",
					settled, float64(settled)*1000/60, len(m.frames), ms(idle), !m.tt.FramePending(), ms(bgDone), ms(m.frames[0]), ms(avg(rest)), ms(worst))
				opened := heapMB()
				// 重读：后台读好、建好缓冲区，主线程只换上
				if wrap && ext == "log" {
					l := docs.ReadFile(d.Abs, d.Root, d.Rel, true)
					gen := d.Start()
					s := time.Now()
					d.ApplyFile(gen, l)
					apply := time.Since(s)
					m.reset()
					m.ctx.Invalidate()
					m.once()
					t.Logf("  重读换缓冲区：ApplyFile %s，下一帧 %s", ms(apply), ms(m.frame))
				}
				closeDoc(d)
				for highlight.Running() != 0 { // 后台高亮拿着那份文字，算完才放
					m.once()
				}
				after := heapMB()
				t.Logf("  堆：开之前 %.1f MB，开着 %.1f MB，关了 %.1f MB（差 %+.1f）", h0, opened, after, after-h0)
			}
		}
	}

	// 后台高亮本身：64 MB 从头算完多久（退路 C 的线是 3 s），主线程上一帧分看得见的 60 行多久
	text := editor.NewBuffer(bigSource(64<<20 - 4096)).Snapshot()
	lang := highlight.For("big.go")
	hl := highlight.New(lang.Tokenizer, text, func() {})
	for done, _ := hl.Done(); !done; done, _ = hl.Done() {
		time.Sleep(time.Millisecond)
	}
	_, took := hl.Done()
	n := text.LineCount()
	var toks []editor.Token
	s := time.Now()
	for range 100 {
		for i := n - 60; i < n; i++ {
			toks, _ = hl.Tokens(i, text.Line(i), toks[:0])
		}
	}
	t.Logf("后台高亮 64 MB（%d 行）：算完 %s；看得见的 60 行分一次 %.1f µs", n, ms(took), float64(time.Since(s))/100/float64(time.Microsecond))
}
