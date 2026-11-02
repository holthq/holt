package app

import (
	"os"
	"path/filepath"
	"testing"
)

// .gitignore 的一行：以 / 开头只认这一个，目录带 /，通配符、反斜杠、末尾空格加 \。
func TestIgnoreLine(t *testing.T) {
	for _, c := range []struct {
		rel  string
		dir  bool
		want string
	}{
		{"a.txt", false, "/a.txt"},
		{"build", true, "/build/"},
		{"docs/x y.md", false, "/docs/x y.md"},
		{"*.log", false, `/\*.log`},
		{"a[1]?.txt", false, `/a\[1]\?.txt`},
		{`back\slash`, false, `/back\\slash`},
		{"trail ", false, `/trail\ `},
		{"#note", false, "/#note"},
	} {
		if got := IgnoreLine(c.rel, c.dir); got != c.want {
			t.Errorf("IgnoreLine(%q, %v) = %q，想要 %q", c.rel, c.dir, got, c.want)
		}
	}
}

// 加到 .gitignore 末尾：没有就建，原来最后一行没换行先补一个。
func TestAppendIgnore(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".gitignore")
	if err := appendIgnore(root, []string{"/a.txt"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "/a.txt\n" {
		t.Fatalf("新建的 .gitignore 是 %q", b)
	}
	if err := os.WriteFile(p, []byte("node_modules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendIgnore(root, []string{"/b/", "/c"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "node_modules\n/b/\n/c\n" {
		t.Fatalf("加在没换行的末尾之后是 %q", b)
	}
}

// 粘贴、创建副本的名字：没重名用原名，重名了 "a 副本.txt"、"a 副本 2.txt"；目录和点开头的不拆扩展名。
func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	src := write("a.txt")
	if got := freeName(t.TempDir(), src); got != "a.txt" {
		t.Errorf("别的目录里没重名：%q", got)
	}
	if got := freeName(dir, src); got != "a 副本.txt" {
		t.Errorf("重名一次：%q", got)
	}
	write("a 副本.txt")
	if got := freeName(dir, src); got != "a 副本 2.txt" {
		t.Errorf("重名两次：%q", got)
	}
	if got := freeName(dir, write(".env")); got != ".env 副本" {
		t.Errorf("点开头的：%q", got)
	}
	sub := filepath.Join(dir, "build.d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := freeName(dir, sub); got != "build.d 副本" {
		t.Errorf("目录：%q", got)
	}
}
