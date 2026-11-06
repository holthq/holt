package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/render"
	"github.com/holthq/holt/internal/testutil"
)

// shotRender 是截图用的渲染后端（WORKBENCH_SHOT_RENDER=目录）：预览要的每一张记进目录里的 requests.jsonl；
// 目录里有画好的 <键>.png + <键>.json（{"w","h","depth"}）就给那张，<键>.err 是源码错，都没有就说还没画。
// 画是另一步，仓库里没有这个工具：用无窗口的 WKWebView 跑 internal/render/assets/page.html，读 requests.jsonl 画出来，
// 再跑一遍截图。这样图里的 Mermaid 和公式是真 WebKit 画的，排版是预览自己的。
type shotRender struct {
	dir  string
	mu   sync.Mutex
	seen map[string]bool
}

func shotRenderKey(q render.Request) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%t|%g|%g|%s", q.Kind, q.Src, q.Dark, q.Scale, q.Size, q.Color))
	return hex.EncodeToString(sum[:8])
}

func (s *shotRender) Render(ctx context.Context, q render.Request, maxPixels int) ([]byte, float32, float32, float32, error) {
	k := shotRenderKey(q)
	s.mu.Lock()
	if !s.seen[k] {
		s.seen[k] = true
		line, _ := json.Marshal(map[string]any{"key": k, "kind": q.Kind, "src": q.Src, "dark": q.Dark, "scale": q.Scale,
			"size": q.Size, "color": q.Color, "maxPixels": maxPixels})
		if f, err := os.OpenFile(filepath.Join(s.dir, "requests.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Write(append(line, '\n'))
			f.Close()
		}
	}
	s.mu.Unlock()
	if b, err := os.ReadFile(filepath.Join(s.dir, k+".err")); err == nil {
		return nil, 0, 0, 0, &render.SourceError{Msg: string(b)}
	}
	pic, err := os.ReadFile(filepath.Join(s.dir, k+".png"))
	if err != nil {
		return nil, 0, 0, 0, errors.New("not rendered yet")
	}
	var size struct{ W, H, Depth float32 }
	b, _ := os.ReadFile(filepath.Join(s.dir, k+".json"))
	if err := json.Unmarshal(b, &size); err != nil {
		return nil, 0, 0, 0, err
	}
	return pic, size.W, size.H, size.Depth, nil
}

func (s *shotRender) Close() {}

// writeDisc 在 root 下写一张 w×h 的 PNG：透明底上一个渐变的圆（看得见棋盘格）。
func writeDisc(t *testing.T, root, name string, w, h int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	cx, cy, rad := float64(w)/2, float64(h)/2, float64(min(w, h))*0.42
	for y := range h {
		for x := range w {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / rad
			if d > 1 {
				continue
			}
			u := float64(x) / float64(w)
			img.SetNRGBA(x, y, color.NRGBA{uint8(40 + 180*u), uint8(110 + 60*(1-d)), uint8(230 - 120*u), uint8(255 - 90*d)})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	if err := os.WriteFile(filepath.Join(root, name), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const shotDiagramsMD = "# 下单流程\n\n" +
	"```mermaid\ngraph LR\n  A[加入购物车] --> B{库存够吗}\n  B -->|够| C[下单]\n  B -->|不够| D[到货提醒]\n  C --> E[发货]\n```\n\n" +
	"价格按 $p = c \\times (1 + r)$ 算，$r$ 是税率；一单的总价不超过预算 $B$：\n\n" +
	"$$\n\\sum_{i=1}^{n} q_i \\, p_i \\le B\n$$\n\n" +
	"```mermaid\nsequenceDiagram\n  用户->>店: 下单\n  店-->>用户: 订单号\n```\n\n" +
	"写错的图：\n\n```mermaid\ngraph TD; A--\n```\n"

// TestScreenshotMedia 是编辑器里看媒体文件的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套，临时仓库 shop：
//
//	svg-preview       SVG 的预览：画布、上面的提示条、状态栏的大小和倍数
//	image-zoom        放大到 200% 的透明 PNG：棋盘格、工具栏、状态栏
//	image-diff        改过的 PNG 的 diff：左右两张、下面的说明
//	hex               二进制文件的十六进制视图
//	mirror            同一个文件在两组里各一份，右边那份的标签上有标记
//	diagrams          Markdown 预览里的 Mermaid 图、块级和行内公式、写错的图（WORKBENCH_SHOT_RENDER=目录 时，见 shotRender）
//	snippet-complete  Go 文件里敲 fo：补全里的 fori、forr
//	snippet           接了 fori 之后：$1 的三处一起选中，状态栏“片段 1/2”
//	snippet-settings  设置的“片段”页
//	sticky-cursor     光标跳到被吸着的函数头盖住的那一行：露在叠层下面
func TestScreenshotMedia(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	shop := testutil.NewRepo(t, "shop")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\n// main 打个招呼。\nfunc main() {\n\tfmt.Println(\"shop\")\n\t\n}\n")
	shop.Write("src/long.go", stickyGo())
	shop.Write("assets/logo.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="120"><rect width="240" height="120" rx="16" fill="#3b82f6"/><circle cx="60" cy="60" r="34" fill="none" stroke="white" stroke-width="8"/><path d="M120 40h80M120 60h60M120 80h70" stroke="white" stroke-width="8" stroke-linecap="round"/></svg>`)
	shop.Write("docs/design.md", shotDiagramsMD)
	writeDisc(t, shop.Root, "assets/icon.png", 64, 64)
	data := make([]byte, 600)
	for i := range data {
		data[i] = byte(i*37 + i/9)
	}
	copy(data, "\x89PNG\r\n\x1a\nshop-data-v1\x00")
	shop.Write("data/blob.bin", string(data))
	shop.Commit("初始")
	writeDisc(t, shop.Root, "assets/icon.png", 96, 64) // 改了：diff 里两张
	writeDisc(t, shop.Root, "assets/photo.png", 480, 320)
	ws := testutil.Workspace(t, shop.Root)
	suffix := func(dark bool) string {
		if dark {
			return "-dark.png"
		}
		return "-light.png"
	}

	// fresh 是一个新开的窗口（每张一个，互不影响）
	fresh := func() (*app.App, *wui.Tester, func(string, func() bool)) {
		a, tt, wait := newUI(t, ws, termState(t, false)) // 底栏关着、shell 是 /bin/sh
		tt.SetScale(2)
		tt.SetTitleBar(shotBar)
		r := a.Repos[0]
		a.RefreshRepo(r, app.WhyManual)
		wait("git status", idle(r))
		return a, tt, wait
	}
	shots := func(tt *wui.Tester, name string) {
		t.Helper()
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			writeShot(t, tt, dir, name+suffix(dark))
		}
	}
	open := func(a *app.App, wait func(string, func() bool), rel string) {
		t.Helper()
		d := a.OpenFile(a.Repos[0], rel, true)
		wait(rel+" 打开", func() bool { return d.Buf != nil && !d.Loading })
	}

	a, tt, wait := fresh()
	open(a, wait, "assets/logo.svg")
	a.Exec("view.markdownPreview")
	shots(tt, "svg-preview")

	a, tt, wait = fresh()
	d := a.OpenFile(a.Repos[0], "assets/photo.png", true)
	wait("photo.png", func() bool { return d.Bitmap != nil && !d.Loading })
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	mustClick(t, tt, "100%")
	tt.Frame() // 键盘回到画布
	tt.Key(0, wui.KeyEqual)
	tt.Key(0, wui.KeyEqual)
	shots(tt, "image-zoom")

	a, tt, wait = fresh()
	a.ShowDiffFor(a.Repos[0], "assets/icon.png")
	wait("icon.png 的 diff", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading && d.ImgDiff != nil })
	shots(tt, "image-diff")

	a, tt, wait = fresh()
	open(a, wait, "data/blob.bin")
	shots(tt, "hex")

	a, tt, wait = fresh()
	open(a, wait, "src/main.go")
	a.Exec("view.openInOtherGroup")
	shots(tt, "mirror")

	if rd := os.Getenv("WORKBENCH_SHOT_RENDER"); rd != "" {
		a, tt, wait = fresh()
		a.RenderOpen = func() (render.Backend, error) { return &shotRender{dir: rd, seen: map[string]bool{}}, nil }
		t.Cleanup(func() {
			if a.Rend != nil {
				a.Rend.Close()
			}
		})
		open(a, wait, "docs/design.md")
		a.Exec("view.markdownPreview")
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame() // 这一帧才要深色的那些
			wait("图和公式", func() bool { return !tt.HasText(i18n.T("渲染中…")) && a.Rend != nil && !a.Rend.Busy() })
			tt.Frame()
			tt.Frame()
			writeShot(t, tt, dir, "diagrams"+suffix(dark))
		}
	} else {
		t.Log("没有 WORKBENCH_SHOT_RENDER：不截 diagrams")
	}

	a, tt, wait = fresh()
	d = a.OpenFile(a.Repos[0], "src/main.go", true)
	wait("main.go", func() bool { return d.Buf != nil && !d.Loading })
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	d.St.GoTo(editor.Pos{Line: 7, Col: 1})
	tt.Frame()
	tt.Type("f")
	tt.Type("o")
	tt.Frame()
	if !tt.HasText("fori") {
		t.Fatalf("没出片段补全：%q", tt.Texts())
	}
	shots(tt, "snippet-complete")
	tt.Key(0, wui.KeyTab)
	tt.Frame()
	if d.Snip == nil {
		t.Fatal("没进跳位")
	}
	shots(tt, "snippet")

	sv := wui.NewTester(SettingsView(a), 760, 560)
	sv.SetScale(2)
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	mustClick(t, sv, "片段")
	for _, dark := range []bool{false, true} {
		sv.SetDark(dark)
		sv.Frame()
		writeShot(t, sv, dir, "snippet-settings"+suffix(dark))
	}

	a, tt, wait = fresh()
	d = a.OpenFile(a.Repos[0], "src/long.go", true)
	wait("long.go", func() bool { return d.Buf != nil && !d.Loading })
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	d.St.SetScrollY(d.St.LineTop(60))
	tt.Frame()
	d.St.GoTo(editor.Pos{Line: 60, Col: 2})
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "func Long() {") {
		t.Fatalf("没吸 func Long：%q", tt.Texts())
	}
	shots(tt, "sticky-cursor")
}
