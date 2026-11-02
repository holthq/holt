package render

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// assets 是渲染页和它要的脚本（mermaid.min.js、MathJax 的 tex-svg.js 和 TeX 扩展，许可证见 assets/LICENSES），
// 从 wbrender:// 协议给 web view（不在 Go 这边拼几 MB 的字符串，页面自己按需加载）。
//
//go:embed assets
var assets embed.FS

const scheme = "wbrender"

// Register 注册渲染页的协议 wbrender://。mygo 要在建窗口之前注册（main 里调一次）。
func Register() error {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	return mygo.Protocol.Handle(scheme, mygo.FileServer(sub))
}

// webBackend 是看不见的 web view：建了不放 ui.WebView 元素，它就只在后台跑页面。
type webBackend struct {
	v *mygo.WebView
}

// WebView 是在 win（要是 Content 窗口）里建 web view 的后端，给 New 用。建好、渲染页加载完才返回（在后台 goroutine 里等）。
func WebView(win *mygo.Window) func() (Backend, error) {
	return func() (Backend, error) {
		if win == nil || win.IsDestroyed() {
			return nil, errors.New("no window")
		}
		v, err := win.NewWebView(mygo.WebViewOptions{Transparent: true})
		if err != nil {
			return nil, err
		}
		p := v.Page()
		loaded := make(chan error, 1)
		var once sync.Once
		done := func(err error) { once.Do(func() { loaded <- err }) }
		off1 := p.OnDidFinishLoad(func() { done(nil) })
		off2 := p.OnDidFailLoad(func(e *mygo.LoadError) { done(e) })
		defer off1()
		defer off2()
		if err := p.LoadURL(scheme + "://localhost/page.html"); err != nil {
			v.Destroy()
			return nil, err
		}
		select {
		case err = <-loaded:
		case <-time.After(10 * time.Second):
			err = errors.New("render page did not load")
		}
		if err != nil {
			v.Destroy()
			return nil, err
		}
		return &webBackend{v: v}, nil
	}
}

// Render 在页面里跑 wbRender（page.html），解出 PNG。脚本抛的错是源码的错（SourceError）。
func (b *webBackend) Render(ctx context.Context, q Request, maxPixels int) ([]byte, float32, float32, float32, error) {
	code := fmt.Sprintf("wbRender(%s, %s, %t, %g, %g, %s, %d)", js(q.Kind), js(q.Src), q.Dark, q.Scale, q.Size, js(q.Color), maxPixels)
	v, err := b.v.Page().EvalContext(ctx, code)
	if err != nil {
		var ee *mygo.EvalError
		if errors.As(err, &ee) {
			return nil, 0, 0, 0, &SourceError{Msg: strings.TrimPrefix(ee.Message, "Error: ")} // WebKit 给的带 Error: 前缀
		}
		return nil, 0, 0, 0, err
	}
	m, _ := v.(map[string]any)
	s, _ := m["png"].(string)
	png, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(png) == 0 {
		return nil, 0, 0, 0, fmt.Errorf("bad picture from the render page: %v", err)
	}
	num := func(k string) float32 { f, _ := m[k].(float64); return float32(f) }
	return png, num("w"), num("h"), num("depth"), nil
}

func (b *webBackend) Close() {
	if !b.v.IsDestroyed() {
		b.v.Destroy()
	}
}

// js 是 s 的 JavaScript 字符串字面量。
func js(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
