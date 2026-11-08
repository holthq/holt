package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/holthq/holt/internal/testutil/fakelsp"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakelsp.Env) == "1" {
		fakelsp.Main()
		return
	}
	os.Exit(m.Run())
}

// testClient 起一个假语言服务器：测试程序自己再跑一遍，TestMain 见到环境变量就进服务端循环。
func testClient(t *testing.T, onDiag func(PublishDiagnosticsParams)) *Client {
	t.Helper()
	t.Setenv(fakelsp.Env, "1")
	t.Setenv(fakelsp.LogEnv, filepath.Join(t.TempDir(), "fake.log"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{
		Command:       shellQuote(exe),
		Dir:           t.TempDir(),
		InitTimeout:   15 * time.Second,
		OnDiagnostics: onDiag,
	})
	if err != nil {
		t.Fatalf("起假语言服务器：%v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestTextOffsets(t *testing.T) {
	text := NewText("a😀b\n中x")
	cases := []struct {
		pos Position
		off int
	}{
		{Position{0, 0}, 0},
		{Position{0, 1}, 1},
		{Position{0, 3}, 5},
		{Position{0, 99}, 6},
		{Position{1, 0}, 7},
		{Position{1, 1}, 10},
		{Position{1, 2}, 11},
		{Position{9, 0}, 11},
	}
	for _, c := range cases {
		if got := text.Offset(c.pos); got != c.off {
			t.Errorf("Offset(%v) = %d, want %d", c.pos, got, c.off)
		}
	}
	if got := text.Position(5); got != (Position{0, 3}) {
		t.Errorf("Position(5) = %v, want {0 3}", got)
	}
	if got := text.Position(10); got != (Position{1, 1}) {
		t.Errorf("Position(10) = %v, want {1 1}", got)
	}
}

func TestTextUTF8Encoding(t *testing.T) {
	text := NewTextEncoding("a😀b", UTF8)
	if got := text.Offset(Position{0, 2}); got != 2 {
		t.Errorf("UTF-8 character 2 = 字节 %d, want 2", got)
	}
	if got := text.Position(5); got.Character != 5 {
		t.Errorf("字节 5 的 character = %d, want 5", got)
	}
}

func TestTextWordAt(t *testing.T) {
	text := NewText("foo_bar baz")
	if s, e := text.WordAt(2); s != 0 || e != 7 {
		t.Errorf("WordAt(2) = %d,%d, want 0,7", s, e)
	}
	if s, e := text.WordAt(0); s != 0 || e != 7 {
		t.Errorf("WordAt(0) = %d,%d, want 0,7", s, e)
	}
}

func TestURIRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a b", "文件.go")
	uri := PathToURI(path)
	if !strings.HasPrefix(uri, "file://") || strings.Contains(uri, " ") {
		t.Fatalf("PathToURI(%q) = %q", path, uri)
	}
	if got := URIToPath(uri); got != path {
		t.Errorf("URIToPath(%q) = %q, want %q", uri, got, path)
	}
	if got := URIToPath("https://example.com/x"); got != "" {
		t.Errorf("非 file 地址 = %q, want 空", got)
	}
}

func TestHoverText(t *testing.T) {
	raw, err := json.Marshal(map[string]string{
		"kind":  "markdown",
		"value": "**粗** [链接](https://example.com)\n\n```go\nfunc A()\n```",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := HoverText(raw)
	if strings.Contains(got, "**") || strings.Contains(got, "```") {
		t.Fatalf("悬停还留着 Markdown 记号：%q", got)
	}
	if !strings.Contains(got, "粗") || !strings.Contains(got, "func A()") {
		t.Fatalf("悬停文字 = %q", got)
	}
}

func TestClientRoundTrip(t *testing.T) {
	diags := make(chan PublishDiagnosticsParams, 8)
	c := testClient(t, func(p PublishDiagnosticsParams) { diags <- p })
	dir := t.TempDir()
	uri := PathToURI(filepath.Join(dir, "a.go"))
	if !c.DidOpen(uri, "go", 7, "package a\n\nfunc A() {}\n") {
		t.Fatal("didOpen 没发出去")
	}
	select {
	case p := <-diags:
		if p.Version == nil || *p.Version != 7 {
			t.Fatalf("诊断版本 = %v, want 7", p.Version)
		}
		if len(p.Diagnostics) != 1 || p.Diagnostics[0].Message != "fake error" {
			t.Fatalf("诊断 = %+v", p.Diagnostics)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等诊断超时")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, err := c.Completion(ctx, uri, Position{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Label != "Alpha" || items[1].InsertTextFormat != 2 {
		t.Fatalf("补全 = %+v", items)
	}
	if doc := Documentation(items[0].Documentation); !strings.Contains(doc, "Alpha") {
		t.Fatalf("补全说明 = %q", doc)
	}

	h, err := c.Hover(ctx, uri, Position{0, 0})
	if err != nil || h == nil {
		t.Fatalf("悬停 = %v, %v", h, err)
	}
	if text := HoverText(h.Contents); !strings.Contains(text, "假的") {
		t.Fatalf("悬停文字 = %q", text)
	}

	locs, err := c.Definition(ctx, uri, Position{0, 0})
	if err != nil || len(locs) != 1 {
		t.Fatalf("定义 = %+v, %v", locs, err)
	}
	if locs[0].URI != uri || locs[0].Range.Start.Line != 1 || locs[0].Range.Start.Character != 2 {
		t.Fatalf("定义 = %+v", locs[0])
	}

	if !c.DidChange(uri, 8, "package a\n\nfunc A() {}\n// x\n") {
		t.Fatal("didChange 没发出去")
	}
	select {
	case p := <-diags:
		if p.Version == nil || *p.Version != 8 {
			t.Fatalf("改动后的诊断版本 = %v, want 8", p.Version)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等改动后的诊断超时")
	}
}
