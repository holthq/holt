package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clock 是测试的钟：每次 tick 往前走 d。
type clock struct{ t time.Time }

func (c *clock) now() time.Time       { return c.t }
func (c *clock) tick(d time.Duration) { c.t = c.t.Add(d) }
func newStore(t *testing.T) (*Store, *clock) {
	c := &clock{time.Date(2026, 11, 1, 14, 0, 0, 0, time.Local)}
	s := New(filepath.Join(t.TempDir(), "history"))
	s.Now = c.now
	return s, c
}

func whys(x Index) string {
	var b []string
	for _, e := range x.Entries {
		b = append(b, e.Why)
	}
	return strings.Join(b, ",")
}

// TestHistoryRecord：第一次保存前记一条磁盘上的、写完记一条保存；10 s 内再存替换上一条保存，隔久了另记；
// 和最新一条一样不记；别的程序改了磁盘上的，下次保存前先记一条 disk；内容文件是写进去的原始字节。
func TestHistoryRecord(t *testing.T) {
	s, c := newStore(t)
	abs := filepath.Join(t.TempDir(), "a.go")
	os.WriteFile(abs, []byte("v0"), 0o644)

	save := func(body string) {
		t.Helper()
		if _, err := s.BeforeWrite(abs); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(abs, []byte(body), 0o644)
		if _, err := s.Record(abs, []byte(body), WhySave); err != nil {
			t.Fatal(err)
		}
	}
	save("v1")
	if x := s.Load(abs); whys(x) != "disk,save" || x.Path != abs {
		t.Fatalf("第一次保存：%q %q", whys(x), x.Path)
	}
	c.tick(3 * time.Second)
	save("v2")
	x := s.Load(abs)
	if whys(x) != "disk,save" {
		t.Fatalf("10 s 内再存该替换：%q", whys(x))
	}
	if b, _ := s.Content(abs, x.Entries[1]); string(b) != "v2" {
		t.Errorf("替换后的内容 %q", b)
	}
	if n, _ := os.ReadDir(filepath.Join(s.Dir, Key(abs))); len(n) != 3 { // index + 两份内容
		t.Errorf("被替换的内容文件没删：%d 个文件", len(n))
	}
	c.tick(20 * time.Second)
	save("v2") // 一样的不记
	c.tick(20 * time.Second)
	save("v3")
	if x = s.Load(abs); whys(x) != "disk,save,save" {
		t.Fatalf("隔久了另记、一样的不记：%q", whys(x))
	}
	c.tick(20 * time.Second)
	os.WriteFile(abs, []byte("别的程序改的"), 0o644)
	save("v4")
	if x = s.Load(abs); whys(x) != "disk,save,save,disk,save" {
		t.Fatalf("磁盘被改过：%q", whys(x))
	}
	if b, _ := s.Content(abs, x.Entries[3]); string(b) != "别的程序改的" {
		t.Errorf("disk 条的内容 %q", b)
	}
	for i := 1; i < len(x.Entries); i++ {
		if x.Entries[i].T <= x.Entries[i-1].T {
			t.Errorf("T 没有严格递增：%v", x.Entries)
		}
	}

	// 同一毫秒里的 disk 和 save 不撞名
	c2 := filepath.Join(t.TempDir(), "b.txt")
	os.WriteFile(c2, []byte("旧"), 0o644)
	save2 := func() {
		s.BeforeWrite(c2)
		s.Record(c2, []byte("新"), WhySave)
	}
	save2()
	if y := s.Load(c2); whys(y) != "disk,save" || y.Entries[1].T != y.Entries[0].T+1 {
		t.Errorf("同一毫秒：%+v", y.Entries)
	}

	// 新文件（磁盘上还没有）只有 save；太大的不记
	nf := filepath.Join(t.TempDir(), "new.md")
	if _, err := s.BeforeWrite(nf); err != nil {
		t.Fatal(err)
	}
	s.Record(nf, []byte("# 新"), WhySave)
	if y := s.Load(nf); whys(y) != "save" {
		t.Errorf("新文件：%q", whys(y))
	}
	if ok, _ := s.Record(nf, make([]byte, MaxSnapshot+1), WhySave); ok {
		t.Error("大于 MaxSnapshot 的记了")
	}
	if b, n := s.Usage(); n != 3 || b == 0 {
		t.Errorf("Usage %d 字节 %d 个文件", b, n)
	}
}

// TestHistoryLimits：每个文件最多 MaxEntries 条；超过保留天数的条目清理掉、没条目的目录删掉；
// 总量超了从最早的删起；没被引用的文件清掉；路径没了的列进 Deleted。
func TestHistoryLimits(t *testing.T) {
	s, c := newStore(t)
	abs := filepath.Join(t.TempDir(), "a.txt")
	for i := range MaxEntries + 5 {
		c.tick(time.Minute)
		s.Record(abs, []byte{byte(i)}, WhySave)
	}
	x := s.Load(abs)
	if len(x.Entries) != MaxEntries {
		t.Fatalf("%d 条，最多 %d", len(x.Entries), MaxEntries)
	}
	if b, _ := s.Content(abs, x.Entries[0]); len(b) != 1 || b[0] != 5 {
		t.Errorf("删的不是最早的：%v", b)
	}
	if n, _ := os.ReadDir(filepath.Join(s.Dir, Key(abs))); len(n) != MaxEntries+1 {
		t.Errorf("删掉的条目的内容文件还在：%d 个文件", len(n))
	}

	// 保留天数：old 的全都过期 → 目录删掉；abs 一部分过期
	old := filepath.Join(t.TempDir(), "old.txt")
	s.Record(old, []byte("很久以前"), WhySave)
	os.WriteFile(filepath.Join(s.Dir, Key(abs), "999"), []byte("没人引用"), 0o600)
	c.tick(31 * 24 * time.Hour)
	s.Record(abs, []byte("新的"), WhySave)
	n, err := s.Prune(0)
	if err != nil {
		t.Fatal(err)
	}
	if n != MaxEntries || len(s.Load(abs).Entries) != 1 {
		t.Errorf("过期删了 %d 条，剩 %d", n, len(s.Load(abs).Entries))
	}
	if _, err := os.Stat(filepath.Join(s.Dir, Key(old))); err == nil {
		t.Error("全过期的目录没删")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, Key(abs), "999")); err == nil {
		t.Error("没被引用的文件没删")
	}

	// 总量（Limit 改小成 10 KB）：五个 3 KB 的，超了从最早的删，删到 9 KB
	s.ClearAll()
	s.Limit = 10 << 10
	var paths []string
	for i := range 5 {
		p := filepath.Join(t.TempDir(), "big.bin")
		c.tick(time.Second)
		s.Record(p, []byte(strings.Repeat(string(rune('a'+i)), 3<<10)), WhySave)
		paths = append(paths, p)
	}
	if n, _ := s.Prune(365); n != 2 {
		t.Errorf("超总量删了 %d 条，要 2", n)
	}
	if len(s.Load(paths[0]).Entries) != 0 || len(s.Load(paths[1]).Entries) != 0 || len(s.Load(paths[2]).Entries) != 1 {
		t.Error("超总量删的不是最早的")
	}

	// Deleted：路径没了的才列，最近的在前
	gone1, gone2 := filepath.Join(t.TempDir(), "删了.go"), filepath.Join(t.TempDir(), "也删了.go")
	s.Record(gone1, []byte("1"), WhySave)
	c.tick(time.Second)
	s.Record(gone2, []byte("2"), WhySave)
	keep := filepath.Join(t.TempDir(), "还在.go")
	os.WriteFile(keep, []byte("3"), 0o644)
	s.Record(keep, []byte("3"), WhySave)
	var got []string
	for _, x := range s.Deleted() {
		if x.Path == gone1 || x.Path == gone2 || x.Path == keep {
			got = append(got, filepath.Base(x.Path))
		}
	}
	if strings.Join(got, ",") != "也删了.go,删了.go" {
		t.Errorf("Deleted：%v", got)
	}
	if err := s.ClearAll(); err != nil || len(s.All()) != 0 {
		t.Errorf("ClearAll：%v，还有 %d", err, len(s.All()))
	}
}

// TestHistoryRename：改名把目录搬到新路径的 key 下、Path 改掉、追加改名条（内容是改名前最新的那份）；
// 新路径以前有记录就合起来；再改回去照样；删条目、清掉一个文件。
func TestHistoryRename(t *testing.T) {
	s, c := newStore(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	s.Record(a, []byte("a1"), WhySave)
	c.tick(time.Minute)
	s.Record(a, []byte("a2"), WhySave)
	c.tick(time.Minute)
	if err := s.Rename(a, b); err != nil {
		t.Fatal(err)
	}
	if x := s.Load(a); len(x.Entries) != 0 {
		t.Errorf("旧路径还有 %d 条", len(x.Entries))
	}
	x := s.Load(b)
	if whys(x) != "save,save,rename" || x.Path != b || x.Entries[2].From != a {
		t.Fatalf("改名后：%q %q %+v", whys(x), x.Path, x.Entries)
	}
	if body, _ := s.Content(b, x.Entries[2]); string(body) != "a2" {
		t.Errorf("改名条的内容 %q", body)
	}
	if err := s.Rename(filepath.Join(dir, "没记过.go"), a); err != nil || len(s.Load(a).Entries) != 0 {
		t.Error("没记过的改名不该动")
	}

	// 改到一个以前有记录的路径：合起来，按时间排
	c2 := filepath.Join(dir, "c.go")
	c.tick(time.Minute)
	s.Record(c2, []byte("c1"), WhySave)
	c.tick(time.Minute)
	if err := s.Rename(b, c2); err != nil {
		t.Fatal(err)
	}
	x = s.Load(c2)
	if whys(x) != "save,save,rename,save,rename" {
		t.Fatalf("合起来：%q", whys(x))
	}
	for i, want := range []string{"a1", "a2", "a2", "c1", "a2"} { // 最后那条改名是 b.go 的内容搬过来
		if body, _ := s.Content(c2, x.Entries[i]); string(body) != want {
			t.Errorf("第 %d 条内容 %q，要 %q", i, body, want)
		}
	}
	if len(s.All()) != 1 {
		t.Errorf("合起来以后还有 %d 个目录", len(s.All()))
	}

	// 删条目：c1 没有改名条指着，只删它；删 a2 时指着它的两条改名一起没了
	if err := s.Delete(c2, x.Entries[3].T); err != nil {
		t.Fatal(err)
	}
	x = s.Load(c2)
	if body, _ := s.Content(c2, x.Entries[3]); whys(x) != "save,save,rename,rename" || string(body) != "a2" {
		t.Errorf("删 c1 后：%q %q", whys(x), body)
	}
	s.Delete(c2, x.Entries[1].T)
	if x = s.Load(c2); whys(x) != "save" {
		t.Errorf("删 a2 后：%q", whys(x))
	}
	if err := s.Clear(c2); err != nil || len(s.Load(c2).Entries) != 0 {
		t.Errorf("Clear：%v", err)
	}

	// 目录改名：下面记过的文件各挪一次，旁边同前缀的目录不动
	d1, d2 := filepath.Join(dir, "pkg"), filepath.Join(dir, "lib")
	in1, in2, side := filepath.Join(d1, "x.go"), filepath.Join(d1, "sub", "y.go"), filepath.Join(dir, "pkg2", "z.go")
	for _, p := range []string{in1, in2, side} {
		c.tick(time.Minute)
		s.Record(p, []byte(p), WhySave)
	}
	if n, err := s.Move(d1, d2, true); n != 2 || err != nil {
		t.Errorf("目录改名挪了 %d 个：%v", n, err)
	}
	if x := s.Load(filepath.Join(d2, "sub", "y.go")); whys(x) != "save,rename" || len(s.Load(in2).Entries) != 0 || len(s.Load(side).Entries) != 1 {
		t.Errorf("目录改名后：%q", whys(x))
	}
	if n, _ := s.Move(filepath.Join(dir, "没记过"), d1, false); n != 0 {
		t.Error("没记过的文件改名不该挪")
	}
}
