package tasks

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func cmds(ts []Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Cmd)
	}
	return out
}

// package.json 的 scripts 按文件里的顺序；包管理器：packageManager 字段优先，再看锁文件，都没有是 npm；
// 带空格之类的名字加引号；写错了的 package.json 报错、别的照认。
func TestScanPackageJSON(t *testing.T) {
	pkg := `{"name":"x","scripts":{"dev":"vite","build":"vite build","test:unit":"vitest","odd name":"echo"},"devDependencies":{"vite":"5"}}`
	for _, c := range []struct {
		files map[string]string
		pm    string
	}{
		{map[string]string{}, "npm"},
		{map[string]string{"pnpm-lock.yaml": ""}, "pnpm"},
		{map[string]string{"yarn.lock": ""}, "yarn"},
		{map[string]string{"bun.lockb": ""}, "bun"},
		{map[string]string{"bun.lock": ""}, "bun"},
		{map[string]string{"yarn.lock": "", "package.json": strings.Replace(pkg, `"name":"x"`, `"name":"x","packageManager":"pnpm@9.1.0"`, 1)}, "pnpm"},
	} {
		dir := t.TempDir()
		write(t, dir, map[string]string{"package.json": pkg})
		write(t, dir, c.files)
		ts, err := Scan(dir)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{c.pm + " run dev", c.pm + " run build", c.pm + " run test:unit", c.pm + " run 'odd name'"}
		if got := cmds(ts); !slices.Equal(got, want) {
			t.Fatalf("%v：%q，要 %q", c.files, got, want)
		}
		if ts[0].Name != "dev" || ts[0].Source != "package.json" {
			t.Fatalf("%+v", ts[0])
		}
	}

	dir := t.TempDir()
	write(t, dir, map[string]string{"package.json": `{"scripts": [1]}`, "go.mod": "module x\n"})
	ts, err := Scan(dir)
	if err == nil || len(ts) != 3 {
		t.Fatalf("写错的 package.json：%v，%q", err, cmds(ts))
	}
}

// Makefile：行首的目标，跳过 .PHONY、模式规则、带变量的、赋值（= := ::= ?=）、配方行、重复的。
func TestScanMakefile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"Makefile": strings.Join([]string{
		"CC ?= cc",
		"VERSION := 1.0",
		"OUT ::= bin",
		"FLAGS = -a:b",
		".PHONY: all test",
		"all: build",
		"build:",
		"\tgo build ./...",
		"test: build",
		"\t@echo test: done",
		"%.o: %.c",
		"$(OUT)/x: y",
		"docs/site.html : docs/site.md",
		"lint::",
		"build: more",
		"deploy-prod :",
	}, "\n")})
	ts, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"make all", "make build", "make test", "make docs/site.html", "make lint", "make deploy-prod"}
	if got := cmds(ts); !slices.Equal(got, want) {
		t.Fatalf("%q，要 %q", got, want)
	}
	if ts[0].Source != "Makefile" {
		t.Fatalf("Source %q", ts[0].Source)
	}

	// 磁盘上的真名：小写的 makefile 记成 makefile
	dir = t.TempDir()
	write(t, dir, map[string]string{"makefile": "x:\n"})
	if ts, _ := Scan(dir); len(ts) != 1 || ts[0].Source != "makefile" {
		t.Fatalf("makefile：%+v", ts)
	}
}

// justfile：配方（带参数、依赖的），跳过 := 赋值、set / alias / export、下划线开头的私有配方、注释、配方体。
func TestScanJustfile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"justfile": strings.Join([]string{
		"set shell := [\"bash\", \"-c\"]",
		"version := \"1\"",
		"alias b := build",
		"export RUST_LOG := \"info\"",
		"# build: not a recipe",
		"default:",
		"  just --list",
		"build target=\"debug\":",
		"  cargo build",
		"test *args: build",
		"_helper:",
		"serve-docs:",
	}, "\n")})
	ts, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"just default", "just build", "just test", "just serve-docs"}
	if got := cmds(ts); !slices.Equal(got, want) {
		t.Fatalf("%q，要 %q", got, want)
	}
}

// Cargo.toml / go.mod 各三条固定的；几种都有时按 package.json、Makefile、justfile、Cargo、go 的顺序；封顶 Max。
func TestScanCargoGo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"Cargo.toml": "[package]\n", "go.mod": "module x\n", "justfile": "fmt:\n", "Makefile": "all:\n", "package.json": `{"scripts":{"dev":"x"}}`})
	ts, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"npm run dev", "make all", "just fmt", "cargo build", "cargo test", "cargo run", "go build ./...", "go test ./...", "go vet ./..."}
	if got := cmds(ts); !slices.Equal(got, want) {
		t.Fatalf("%q，要 %q", got, want)
	}
	if ts[3].Source != "Cargo.toml" || ts[3].Name != "build" || ts[6].Source != "go.mod" {
		t.Fatalf("%+v %+v", ts[3], ts[6])
	}

	var b strings.Builder
	for i := range Max + 50 {
		b.WriteString("t" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + strconv.Itoa(i) + ":\n")
	}
	dir = t.TempDir()
	write(t, dir, map[string]string{"Makefile": b.String(), "go.mod": "module x\n"})
	start := time.Now()
	if ts, _ := Scan(dir); len(ts) != Max {
		t.Fatalf("封顶：%d", len(ts))
	}
	took := time.Since(start)

	// Stamp：改了文件才变
	start = time.Now()
	s1 := Stamp(dir)
	t.Logf("扫 %d 行的 Makefile %v，Stamp %v", Max+50, took, time.Since(start))
	if s2 := Stamp(dir); s1 != s2 || s1 == "" {
		t.Fatalf("没改 Stamp 也变了：%q %q", s1, s2)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(dir, "go.mod"), later, later)
	if Stamp(dir) == s1 {
		t.Fatal("改了 go.mod Stamp 没变")
	}
}
