// Package tasks 从仓库根的几个文件里认出能跑的任务：package.json 的 scripts、Makefile / justfile 的目标、
// Cargo.toml / go.mod 的常用命令。只读文件、不跑任何东西；跑是应用把命令送进终端（app/tasks.go）。
package tasks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Task 是一个能跑的任务。
type Task struct {
	Name   string // 脚本名、目标名、"build"……（终端标签的名字）
	Cmd    string // 送进终端的那一行，在仓库根跑
	Source string // 从哪个文件认出来的（相对仓库根）："package.json"、"Makefile"、"justfile"、"Cargo.toml"、"go.mod"
}

// Max 是一个仓库最多认几个任务。
const Max = 200

// maxFile 是认任务时一个文件最多读多少：再大的不是手写的。
const maxFile = 1 << 20

// files 是认任务要看的文件（Stamp 看它们的修改时间）：锁文件定 package.json 用哪个包管理器。
var files = []string{
	"package.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock",
	"GNUmakefile", "makefile", "Makefile", "justfile", "Justfile", ".justfile",
	"Cargo.toml", "go.mod",
}

// Stamp 是 root 下那几个文件的名字、大小和修改时间连起来：变了才要重扫（大小写不分的磁盘上同一个文件可能记两遍，不要紧）。
func Stamp(root string) string {
	var b strings.Builder
	for _, f := range files {
		if fi, err := os.Stat(filepath.Join(root, f)); err == nil && fi.Mode().IsRegular() {
			fmt.Fprintf(&b, "%s %d %d\n", f, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return b.String()
}

// Scan 认出 root 下的任务：按 package.json、Makefile、justfile、Cargo.toml、go.mod 的顺序，各自按在文件里出现的顺序，
// 最多 Max 个。读不了、写错了的文件跳过，错误连同别的文件认出来的任务一起返回。
func Scan(root string) ([]Task, error) {
	d, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	list, _ := d.Readdirnames(-1)
	d.Close()
	names := map[string]bool{} // 按目录里的真名认：大小写不分的磁盘上 Stat("makefile") 也认得 Makefile
	for _, n := range list {
		names[n] = true
	}
	has := func(n string) bool { return names[n] }
	var out []Task
	var errs []error
	add := func(ts []Task, err error) {
		out = append(out, ts...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if has("package.json") {
		add(scanPackageJSON(root, names))
	}
	for _, n := range []string{"GNUmakefile", "makefile", "Makefile"} { // make 自己找的顺序
		if has(n) {
			add(scanLines(root, n, "make", makeTarget))
			break
		}
	}
	for _, n := range []string{"justfile", "Justfile", ".justfile"} {
		if has(n) {
			add(scanLines(root, n, "just", justRecipe))
			break
		}
	}
	if has("Cargo.toml") {
		for _, c := range []string{"build", "test", "run"} {
			out = append(out, Task{Name: c, Cmd: "cargo " + c, Source: "Cargo.toml"})
		}
	}
	if has("go.mod") {
		for _, c := range []string{"build", "test", "vet"} {
			out = append(out, Task{Name: c, Cmd: "go " + c + " ./...", Source: "go.mod"})
		}
	}
	if len(out) > Max {
		out = out[:Max]
	}
	return out, errors.Join(errs...)
}

// readSmall 读 root 下的 name，超过 maxFile 的不读。
func readSmall(root, name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("%s 超过 %d 字节", name, maxFile)
	}
	return data, nil
}

// scanPackageJSON 认 package.json 的 scripts（按文件里的顺序）→ "<包管理器> run <名字>"。
func scanPackageJSON(root string, names map[string]bool) ([]Task, error) {
	data, err := readSmall(root, "package.json")
	if err != nil {
		return nil, err
	}
	scripts, pm, err := parsePackageJSON(data)
	if err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	if pm == "" {
		pm = "npm"
		for _, l := range []struct{ file, pm string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}} {
			if names[l.file] {
				pm = l.pm
				break
			}
		}
	}
	out := make([]Task, 0, len(scripts))
	for _, s := range scripts {
		out = append(out, Task{Name: s, Cmd: pm + " run " + quote(s), Source: "package.json"})
	}
	return out, nil
}

// parsePackageJSON 读出 scripts 的名字（按出现顺序、去重）和 packageManager 字段写的包管理器（"pnpm@9.1.0" → "pnpm"；
// 不认识的为 ""，按锁文件定）。
func parsePackageJSON(data []byte) (scripts []string, pm string, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, "", errors.New("不是一个对象")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, "", err
		}
		key, _ := t.(string)
		switch key {
		case "scripts":
			if t, err := dec.Token(); err != nil || t != json.Delim('{') {
				return nil, "", errors.New("scripts 不是一个对象")
			}
			for dec.More() {
				t, err := dec.Token()
				if err != nil {
					return nil, "", err
				}
				name, _ := t.(string)
				var v json.RawMessage
				if err := dec.Decode(&v); err != nil {
					return nil, "", err
				}
				if name != "" && !seen[name] && !strings.ContainsAny(name, "\n\r\x00") {
					seen[name] = true
					scripts = append(scripts, name)
				}
			}
			if _, err := dec.Token(); err != nil {
				return nil, "", err
			}
		case "packageManager":
			var v string
			if err := dec.Decode(&v); err != nil {
				return nil, "", err
			}
			name, _, _ := strings.Cut(v, "@")
			switch name {
			case "npm", "pnpm", "yarn", "bun":
				pm = name
			}
		default:
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				return nil, "", err
			}
		}
	}
	return scripts, pm, nil
}

var (
	makeRe = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_./-]*)\s*:([^=]|$)`)
	justRe = regexp.MustCompile(`^([a-zA-Z_][\w-]*)(\s+[^:]*)?:`)
)

// makeTarget 是 Makefile 一行定义的目标（行首、不是变量赋值）；不是为 ""。
// 点开头的（.PHONY 之类）正则本来就不认；含 % / $ 的（模式规则、变量）也不认；::= / :::= 是赋值。
func makeTarget(line string) string {
	m := makeRe.FindStringSubmatch(line)
	if m == nil || strings.HasPrefix(strings.TrimLeft(line[len(m[1]):], " \t:"), "=") {
		return ""
	}
	return m[1]
}

// justRecipe 是 justfile 一行定义的配方（行首、不是 := 赋值、set / alias / export 这些带 := 的行）；
// 下划线开头的是 just 约定的私有配方，不列。不是为 ""。
func justRecipe(line string) string {
	m := justRe.FindStringSubmatch(line)
	if m == nil || strings.HasPrefix(line[len(m[0]):], "=") || strings.HasPrefix(m[1], "_") {
		return ""
	}
	return m[1]
}

// scanLines 一行行认目标（name 是磁盘上的文件名），去重 → "<tool> <目标>"。
func scanLines(root, name, tool string, target func(string) string) ([]Task, error) {
	data, err := readSmall(root, name)
	if err != nil {
		return nil, err
	}
	var out []Task
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), maxFile)
	for sc.Scan() {
		t := target(sc.Text())
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, Task{Name: t, Cmd: tool + " " + t, Source: name})
		if len(out) >= Max {
			break
		}
	}
	return out, sc.Err()
}

// quote 是 s 在 shell 里的写法：只有常见字符的原样，别的单引号括起来。
func quote(s string) string {
	safe := s != ""
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-.:/@%+=,", c)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
