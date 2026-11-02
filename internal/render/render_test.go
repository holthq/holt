package render

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fake 是假的后端：Src 以 bad 开头是源码错（Mermaid 那样的多行），boom 开头是后端坏了，slow 开头等 release；
// 别的画一张 len(Src)×10 乘 20 DIP 的 PNG（按 Scale 放大）。记下渲染的顺序。
type fake struct {
	mu      sync.Mutex
	order   []string
	closed  int
	release chan struct{}
}

func (f *fake) Render(ctx context.Context, q Request, maxPixels int) ([]byte, float32, float32, float32, error) {
	f.mu.Lock()
	f.order = append(f.order, q.Src)
	f.mu.Unlock()
	switch {
	case strings.HasPrefix(q.Src, "bad"):
		return nil, 0, 0, 0, &SourceError{Msg: "Parse error on line 1:\n" + q.Src + "\n---^\nExpecting 'STR', got 'EOF'"}
	case strings.HasPrefix(q.Src, "boom"):
		return nil, 0, 0, 0, errors.New("page gone")
	case strings.HasPrefix(q.Src, "slow"):
		<-f.release
	}
	w, h := float32(len(q.Src)*10), float32(20)
	return pngOf(int(w*q.Scale), int(h*q.Scale)), w, h, 4, nil
}

func (f *fake) Close() {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

func (f *fake) rendered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	img.Set(0, 0, color.RGBA{255, 0, 255, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// eventually 等 cond 成立（后台 goroutine 在渲染），最多 5 秒。
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(end) {
			t.Fatalf("等不到：%s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// newFake 是用 fake 当后端的渲染器，opens 数建了几次，wakes 数叫了几次重画。
func newFake(t *testing.T) (*Renderer, *fake, func() (opens, wakes int)) {
	f := &fake{release: make(chan struct{})}
	var mu sync.Mutex
	opens, wakes := 0, 0
	r := New(func() (Backend, error) {
		mu.Lock()
		opens++
		mu.Unlock()
		return f, nil
	}, func() {
		mu.Lock()
		wakes++
		mu.Unlock()
	})
	t.Cleanup(r.Close)
	return r, f, func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return opens, wakes
	}
}

// 渲染器：没有的先排队、后台画好叫重画；同一张再要直接给；换主题 / 缩放先给旧的顶着（大小一样）；
// 源码错记着、后端照用；后端坏了关掉，那个错 30 秒后再试、再建后端；行内公式好了 Gen 加一；Idle 之后销毁后端；
// 后来的先画；Close 之后什么都不给、不再叫重画。
func TestRenderCache(t *testing.T) {
	r, f, counts := newFake(t)
	q := Request{Kind: Mermaid, Src: "graph TD; A-->B", Scale: 2}
	if _, ok := r.Get(q); ok || !r.Pending(q) {
		t.Fatal("第一次要该排队")
	}
	var res Result
	eventually(t, "图画好", func() bool { res, _ = r.Get(q); return res.Bitmap != nil })
	if w, h := res.Bitmap.Size(); res.W != 150 || res.H != 20 || w != 300 || h != 40 || res.Err != "" {
		t.Fatalf("结果 %+v，位图 %d×%d", res, w, h)
	}
	if opens, wakes := counts(); opens != 1 || wakes == 0 || !r.Alive() {
		t.Fatalf("建了 %d 次后端，叫了 %d 次重画", opens, wakes)
	}
	r.Get(q)
	if n := len(f.rendered()); n != 1 {
		t.Fatalf("同一张又画了：%d", n)
	}

	// 换深色：先给浅色的顶着，再换成深色的
	dq := q
	dq.Dark = true
	if got, ok := r.Get(dq); !ok || got.Bitmap != res.Bitmap {
		t.Fatal("换主题时没给旧的顶着")
	}
	eventually(t, "深色画好", func() bool { got, _ := r.Get(dq); return got.Bitmap != res.Bitmap })
	// Mermaid 不看字号：字号不同也是同一张
	sq := q
	sq.Size = 20
	if got, ok := r.Get(sq); !ok || got.Bitmap != res.Bitmap || r.Pending(sq) {
		t.Fatal("Mermaid 换字号不该重画")
	}

	// 源码错：头一行接上最后一行（中间的源码和箭头不要），记着，后端照用
	bad := Request{Kind: Mermaid, Src: "bad graph", Scale: 2}
	r.Get(bad)
	eventually(t, "源码错", func() bool { got, _ := r.Get(bad); return got.Err != "" })
	if got, _ := r.Get(bad); got.Err != "Parse error on line 1: Expecting 'STR', got 'EOF'" || got.Bitmap != nil || !r.Alive() {
		t.Fatalf("源码错：%+v，后端 %v", got, r.Alive())
	}
	n := len(f.rendered())
	r.Get(bad)
	if len(f.rendered()) != n || r.Pending(bad) {
		t.Error("源码错又画了一次")
	}

	// 后端坏了：关掉；那个错过一阵再试（这里把时候往前拨），再建后端
	boom := Request{Kind: Math, Src: "boom", Scale: 2, Size: 14, Color: "#000000"}
	r.Get(boom)
	eventually(t, "后端坏了", func() bool { got, _ := r.Get(boom); return got.Err == "page gone" })
	eventually(t, "坏了的后端关掉", func() bool { return !r.Alive() })
	if f.mu.Lock(); f.closed != 1 {
		t.Fatalf("关了 %d 次", f.closed)
	}
	f.mu.Unlock()
	if r.Pending(boom) {
		t.Error("不到时候不该再试")
	}
	r.mu.Lock()
	r.cache[keyOf(boom)].Value.(*entry).expires = time.Now().Add(-time.Second)
	r.mu.Unlock()
	if _, ok := r.Get(boom); ok {
		t.Error("过了时候的错还给")
	}
	eventually(t, "再建后端再试", func() bool { o, _ := counts(); return o == 2 })

	// 行内公式：没有可顶的，好了 Gen 加一
	gen := r.Gen()
	in := Request{Kind: Inline, Src: "x^2", Scale: 2, Size: 14, Color: "#000000"}
	if _, ok := r.Get(in); ok {
		t.Fatal("行内第一次要不该有")
	}
	eventually(t, "行内好了 Gen 加一", func() bool { return r.Gen() > gen })
	if got, ok := r.Get(in); !ok || got.Depth != 4 {
		t.Fatalf("行内：%+v", got)
	}
	// 换颜色有旧的顶着：不重排
	gen = r.Gen()
	cq := in
	cq.Color = "#ffffff"
	r.Get(cq)
	eventually(t, "换颜色画好", func() bool { return !r.Pending(cq) })
	if r.Gen() != gen {
		t.Error("有顶着的不该让段落重排")
	}

	// 后来的先画：slow 堵着时排 a、b、c，放开后 c、b、a
	slow := Request{Kind: Mermaid, Src: "slow", Scale: 1}
	r.Get(slow)
	eventually(t, "slow 在画", func() bool { s := f.rendered(); return s[len(s)-1] == "slow" })
	for _, s := range []string{"a", "b", "c"} {
		r.Get(Request{Kind: Mermaid, Src: s, Scale: 1})
	}
	f.release <- struct{}{}
	eventually(t, "三张画完", func() bool { return !r.Pending(Request{Kind: Mermaid, Src: "a", Scale: 1}) })
	if s := f.rendered(); strings.Join(s[len(s)-4:], " ") != "slow c b a" {
		t.Errorf("顺序：%q", s)
	}

	// 没活 Idle 之后销毁后端，缓存留着
	_, entries, _ := statsOf(r)
	r.mu.Lock()
	r.Idle = 20 * time.Millisecond
	r.mu.Unlock()
	r.Get(Request{Kind: Mermaid, Src: "last", Scale: 1})
	eventually(t, "闲了销毁后端", func() bool { return !r.Alive() && !r.Pending(q) })
	if _, e, _ := statsOf(r); e != entries+1 {
		t.Errorf("销毁后端不该清缓存：%d → %d", entries, e)
	}
	if got, ok := r.Get(q); !ok || got.Bitmap != res.Bitmap {
		t.Error("销毁后端后缓存里的没了")
	}

	// Close 时正在画的：画完不放进缓存、不叫重画
	r.Get(Request{Kind: Mermaid, Src: "slow2", Scale: 1})
	eventually(t, "slow2 在画", func() bool { s := f.rendered(); return s[len(s)-1] == "slow2" && r.Alive() })
	r.Close()
	_, wakes := counts()
	f.release <- struct{}{}
	eventually(t, "后台停了", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return !r.running })
	if _, w := counts(); w != wakes {
		t.Error("关了还叫重画")
	}
	if _, ok := r.Get(q); ok || r.Alive() {
		t.Error("关了还给")
	}
	if _, e, b := statsOf(r); e != 0 || b != 0 {
		t.Errorf("关了缓存还有 %d 张 %d 字节", e, b)
	}
}

func statsOf(r *Renderer) (renders, entries, bytes int) {
	renders, _, entries, bytes = r.Stats()
	return
}

// 缓存上限：超了条数、字节数挤最久没用的；正在显示的（inUse 之内取过的）不挤，闲下来再挤。
func TestRenderEvict(t *testing.T) {
	r, _, _ := newFake(t)
	old := time.Now().Add(-time.Minute)
	big := decode(t, pngOf(1024, 1024)) // 解一次，几张共用（解码慢，放的时候不能超过 inUse）
	put := func(i int, large bool) key {
		k := keyOf(Request{Kind: Mermaid, Src: strings.Repeat("x", i+1), Scale: 1})
		res := Result{W: 1, H: 1}
		if large {
			res.Bitmap = big
		}
		r.mu.Lock()
		r.store(k, res, false)
		r.mu.Unlock()
		return k
	}
	age := func() {
		r.mu.Lock()
		for e := r.lru.Front(); e != nil; e = e.Next() {
			e.Value.(*entry).used = old
		}
		r.mu.Unlock()
	}

	// 条数：都是刚放的（正在显示）不挤，超了上限也留着
	for i := range maxEntries + 10 {
		put(i, false)
	}
	if _, e, _ := statsOf(r); e != maxEntries+10 {
		t.Fatalf("正在显示的被挤了：%d", e)
	}
	// 都放了一阵了：再放一张时挤到上限，挤掉的是最早的
	age()
	last := put(maxEntries+10, false)
	if _, e, _ := statsOf(r); e != maxEntries {
		t.Fatalf("挤完 %d 张", e)
	}
	r.mu.Lock()
	_, first := r.cache[keyOf(Request{Kind: Mermaid, Src: "x", Scale: 1})]
	_, kept := r.cache[last]
	r.mu.Unlock()
	if first || !kept {
		t.Errorf("挤错了：最早的还在 %v，刚放的在 %v", first, kept)
	}

	// 字节数：1024×1024 一张 4 MB，放 10 张都在显示时超 32 MB 留着；闲下来挤到 32 MB 以内
	r.mu.Lock()
	r.lru.Init()
	clear(r.cache)
	clear(r.near)
	r.bytes = 0
	r.mu.Unlock()
	for i := range 10 {
		put(i, true)
	}
	if _, _, b := statsOf(r); b <= maxBytes {
		t.Fatalf("10 张才 %d 字节", b)
	}
	age()
	r.idle()
	if _, e, b := statsOf(r); b > maxBytes || e != 7 {
		t.Errorf("闲下来挤完 %d 张 %d 字节", e, b)
	}
}

func decode(t *testing.T, b []byte) *ui.Bitmap {
	t.Helper()
	bmp, err := ui.DecodeBitmap(b)
	if err != nil {
		t.Fatal(err)
	}
	return bmp
}

// 后端建不起来：那一张记下错；5 秒之内别的不再试（不排队），过了再试。
func TestRenderOpenFail(t *testing.T) {
	tries := 0
	var mu sync.Mutex
	r := New(func() (Backend, error) {
		mu.Lock()
		tries++
		mu.Unlock()
		return nil, errors.New("no window")
	}, func() {})
	defer r.Close()
	q := Request{Kind: Mermaid, Src: "graph TD; A", Scale: 1}
	r.Get(q)
	eventually(t, "记下错", func() bool { got, _ := r.Get(q); return got.Err == "no window" })
	other := Request{Kind: Mermaid, Src: "graph LR; B", Scale: 1}
	if _, ok := r.Get(other); ok || r.Pending(other) {
		t.Error("刚建不起来又排队")
	}
	r.mu.Lock()
	r.lastFail = time.Now().Add(-openRetry)
	r.mu.Unlock()
	r.Get(other)
	eventually(t, "过了再试", func() bool { mu.Lock(); defer mu.Unlock(); return tries == 2 })
}
