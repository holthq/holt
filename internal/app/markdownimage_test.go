package app

import (
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

// clearImages 清掉网络图片的缓存，测试之间不串。
func clearImages() {
	remoteImages.Lock()
	remoteImages.m, remoteImages.order = nil, nil
	remoteImages.Unlock()
}

// 本地路径照旧从文档所在的目录读，读不到就报错。
func TestMarkdownImagesLocal(t *testing.T) {
	clearImages()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), pngBytes(2, 2, color.RGBA{R: 255, A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	load := markdownImages(dir)
	src, err := load("a.png")
	if err != nil || src == nil {
		t.Fatalf("本地图片：%v %v", src, err)
	}
	if _, err := load("nope.png"); err == nil {
		t.Fatal("不存在的图片没有报错")
	}
}

// http / https 的图片下下来解码（PNG、SVG），成功过的按地址留着；404、解不开的都报错。
func TestMarkdownImagesRemote(t *testing.T) {
	clearImages()
	t.Cleanup(clearImages)
	data := pngBytes(2, 2, color.RGBA{R: 255, A: 255})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "Holt" {
			t.Errorf("User-Agent = %q，要 Holt", ua)
		}
		switch r.URL.Path {
		case "/ok.png":
			hits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Write(data)
		case "/logo.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"></svg>`))
		case "/sniff.svg":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"></svg>`))
		case "/bad.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("不是 PNG"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	load := markdownImages(t.TempDir())
	src, err := load(srv.URL + "/ok.png")
	if err != nil || src == nil {
		t.Fatalf("网络 PNG：%v %v", src, err)
	}
	if _, ok := src.(*ui.Bitmap); !ok {
		t.Fatalf("网络 PNG 解出来的是 %T", src)
	}
	if _, err := load(srv.URL + "/ok.png"); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("同一张图下了 %d 次，应该只下一次", n)
	}
	if svg, err := load(srv.URL + "/logo.svg"); err != nil {
		t.Fatalf("网络 SVG：%v", err)
	} else if _, ok := svg.(*ui.SVG); !ok {
		t.Fatalf("网络 SVG 解出来的是 %T", svg)
	}
	if svg, err := load(srv.URL + "/sniff.svg"); err != nil {
		t.Fatalf("按开头认的 SVG：%v", err)
	} else if _, ok := svg.(*ui.SVG); !ok {
		t.Fatalf("按开头认的 SVG 解出来的是 %T", svg)
	}
	if _, err := load(srv.URL + "/gone.png"); err == nil {
		t.Fatal("404 没有报错")
	}
	if _, err := load(srv.URL + "/bad.png"); err == nil {
		t.Fatal("解不开的 PNG 没有报错")
	}
}
