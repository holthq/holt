package docs

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

func open(t *testing.T, d *Doc) {
	t.Helper()
	first := d.Buf == nil && d.Bitmap == nil
	gen := d.Start()
	if d.Loading != first {
		t.Error("第一次读应该是 Loading")
	}
	if !d.ApplyFile(gen, ReadFile(d.Abs, d.Root, d.Rel, d.Buf == nil || d.Big)) {
		t.Fatal("ApplyFile 丢了结果")
	}
}

// 读、改、存、外部改动、冲突、保留我的。只在临时仓库上。
func TestFileDoc(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	d := NewFile(root, "a.txt")
	open(t, d)
	if d.Info != "" || d.Buf == nil || d.Loading {
		t.Fatalf("读完 %+v", d)
	}
	if !strings.Contains(d.Buf.String(), "第 20 行（改过）") || d.Base == nil || !strings.Contains(d.Base.String(), "第 20 行\n") {
		t.Error("内容或 HEAD 基准不对")
	}
	if d.St.Label != "编辑 a.txt" || d.St.DiffBase != d.Base || d.Buf.Language() != nil {
		t.Errorf("编辑器状态 label=%q", d.St.Label)
	}
	if d.Modified() || !d.Editable() || d.Where() != "a.txt" {
		t.Error("刚读完不该是改过的")
	}
	d.Buf.Set("新内容\n")
	if !d.Modified() {
		t.Fatal("改了却不是 Modified")
	}
	if d.NeedsOverwrite() {
		t.Error("磁盘没变不用问覆盖")
	}
	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(d.Abs); string(data) != "新内容\n" || d.Modified() {
		t.Errorf("写盘后 %q modified=%v", data, d.Modified())
	}
	// 外部改动：时间往后拨一秒，免得和刚才写的同一刻
	if err := os.WriteFile(d.Abs, []byte("别处改的\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := d.Mtime.Add(time.Second)
	os.Chtimes(d.Abs, later, later)
	mt, size, err := Stat(d.Abs)
	if err != nil || !d.ChangedOnDisk(mt, size) {
		t.Fatal("没发现磁盘上的改动")
	}
	d.Buf.Set("这里也改了\n")
	if !d.NeedsOverwrite() {
		t.Error("磁盘被改过，保存前要问")
	}
	d.Conflict = true
	d.KeepMine()
	if d.Conflict || d.NeedsOverwrite() {
		t.Error("保留我的之后不该再问")
	}
	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(d.Abs); string(data) != "这里也改了\n" {
		t.Errorf("覆盖后 %q", data)
	}
	// 重读保留同一个缓冲区
	buf := d.Buf
	open(t, d)
	if d.Buf != buf || d.Buf.String() != "这里也改了\n" {
		t.Error("重读换了缓冲区")
	}
	// 过期的结果丢掉
	gen := d.Start()
	d.Start()
	if d.ApplyFile(gen, Loaded{Err: os.ErrNotExist}) || d.Info != "" {
		t.Error("过期的结果没丢")
	}
}

func TestFileDocKinds(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	// 未跟踪：没有 HEAD 基准
	d := NewFile(root, "u.txt")
	open(t, d)
	if d.Base != nil || d.Buf.String() != "未跟踪\n" {
		t.Errorf("未跟踪文件 base=%v", d.Base)
	}
	// 二进制：按十六进制看
	os.WriteFile(filepath.Join(root, "bin.dat"), []byte{1, 0, 2}, 0o644)
	d = NewFile(root, "bin.dat")
	open(t, d)
	if d.Buf == nil || d.Info != "" || !d.HexOnly() || !strings.HasPrefix(d.Buf.String(), "00000000  01 00 02 ") || d.Editable() {
		t.Errorf("二进制 info=%q hex=%v", d.Info, d.Hex)
	}
	// 图片
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	os.WriteFile(filepath.Join(root, "p.PNG"), b.Bytes(), 0o644)
	d = NewFile(root, "p.PNG")
	open(t, d)
	if d.Bitmap == nil || d.Buf != nil {
		t.Errorf("图片 info=%q", d.Info)
	} else if w, h := d.Bitmap.Size(); w != 3 || h != 2 {
		t.Errorf("图片大小 %d×%d", w, h)
	}
	// 读不到
	d = NewFile(root, "没有.txt")
	open(t, d)
	if !strings.HasPrefix(d.Info, "读取失败：") {
		t.Errorf("没有的文件 info=%q", d.Info)
	}
	// 仓库外
	out := filepath.Join(t.TempDir(), "x.go")
	os.WriteFile(out, []byte("package x\n"), 0o644)
	d = NewPath(out)
	open(t, d)
	if d.Base != nil || d.Buf.Language() == nil || d.Where() != out || d.Key().Path != out {
		t.Errorf("仓库外文件 where=%q", d.Where())
	}
}

func TestVersionAndDiff(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	cs, err := git.Log(context.Background(), root, 0, 10, git.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := git.CommitFiles(context.Background(), root, cs[0])
	d := NewVersion(root, files[0])
	if d.Title != "a.txt @ "+git.Short(cs[0].Hash) || d.Where() != d.Title {
		t.Errorf("版本标题 %q / %q", d.Title, d.Where())
	}
	gen := d.Start()
	d.ApplyVersion(gen, ReadVersion(root, files[0], true))
	if !d.St.ReadOnly || d.Base != nil || !strings.HasPrefix(d.Buf.String(), "第 1 行") || d.Modified() || d.Editable() {
		t.Errorf("版本 readOnly=%v base=%v", d.St.ReadOnly, d.Base)
	}
	st, _ := git.ReadStatus(context.Background(), root)
	ch := st.Changes[0] // 未暂存的 a.txt
	d = NewDiff(root, ch)
	if d.Title != "diff: a.txt" || d.Abs == "" {
		t.Errorf("diff 标题 %q", d.Title)
	}
	gen = d.Start()
	dc, err := git.LoadDiffContent(context.Background(), root, ch)
	d.ApplyDiff(gen, dc, err, false)
	if d.DiffSt == nil || d.Info != "" {
		t.Fatalf("diff info=%q", d.Info)
	}
	if add, del := d.DiffSt.Stats(); add != 1 || del != 1 {
		t.Errorf("diff 统计 +%d −%d", add, del)
	}
	same := d.DiffSt
	gen = d.Start()
	d.ApplyDiff(gen, dc, nil, true)
	if d.DiffSt != same {
		t.Error("内容没变不该换 DiffState")
	}
	gen = d.Start()
	d.ApplyDiff(gen, git.DiffContent{Binary: true}, nil, false)
	if d.DiffSt != nil || !strings.Contains(d.Info, "二进制") {
		t.Errorf("二进制 diff info=%q", d.Info)
	}
	if NewDiff(root, ch).Key() != d.Key() || NewDiff(root, st.Changes[1]).Key() == d.Key() {
		t.Error("Key 认错了")
	}
	if NewFile(root, "a.txt").Key() == d.Key() {
		t.Error("文件和 diff 不该是同一个标签")
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 3 << 20: "3.0 MB"} {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q，要 %q", n, got, want)
		}
	}
}
