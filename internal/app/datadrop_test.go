package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"

	"github.com/holthq/holt/internal/testutil"
)

// 别的应用拖进来的：拖来的文字插在编辑器光标处、文件照旧打开，拖到终端的文件是单引号写法。

func TestShellFiles(t *testing.T) {
	d := ShellFiles([]string{"/tmp/a b.txt", "/tmp/c"})
	if b, err := d.Read(transfer.Text); err != nil || string(b) != "'/tmp/a b.txt' /tmp/c " {
		t.Fatalf("文字 %q %v", b, err)
	}
	if paths, err := d.Files(); err != nil || !slices.Equal(paths, []string{"/tmp/a b.txt", "/tmp/c"}) {
		t.Fatalf("文件列表 %q %v", paths, err)
	}
	items := d.Items()
	if len(items) != 2 || !slices.Contains(items[0].Formats(), transfer.Text) || !slices.Contains(items[0].Formats(), transfer.URIList) ||
		slices.Contains(items[1].Formats(), transfer.Text) {
		t.Fatalf("第一项带文件列表、URL 和文字，后面的不带文字：%d 项", len(items))
	}
	if b, _ := ShellFiles([]string{"rel"}).Read(transfer.Text); string(b) != "rel " {
		t.Fatalf("不是绝对路径的只有文字：%q", b)
	}
}

func TestDroppedText(t *testing.T) {
	url := transfer.New(transfer.NewItem(transfer.Bytes(transfer.URIList, []byte("https://example.com/a"))),
		transfer.NewItem(transfer.Bytes(transfer.URIList, []byte("https://example.com/b"))))
	for name, c := range map[string]struct {
		d    transfer.Data
		want string
	}{
		"文字":     {transfer.TextData("一段\r\n字"), "一段\r\n字"},
		"只有 URL": {url, "https://example.com/a\nhttps://example.com/b"},
		"都没有":    {transfer.New(), ""},
	} {
		if got := droppedText(c.d); got != c.want {
			t.Errorf("%s：%q，要 %q", name, got, c.want)
		}
	}
}

func TestDropOnDoc(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.Current()
	at := a.OpenFile(r, "a.txt", true)
	wait("读完", func() bool { return !at.Loading })
	before := at.Buf.String()
	at.St.Select(0, 0)

	a.FocusDoc = false
	a.DropOnDoc(at, transfer.Drop{Data: transfer.TextData("拖来的\r\n"), Operation: transfer.Copy})
	if got := at.Buf.String(); got != "拖来的\n"+before || !at.Modified() || !a.FocusDoc {
		t.Fatalf("插在光标处（换行收成 \\n）、标签改过、键盘给它：%q…", firstLine(got))
	}
	at.Buf.Undo()
	if at.Buf.String() != before {
		t.Fatal("撤销不回去")
	}

	at.St.ReadOnly = true
	a.DropOnDoc(at, transfer.Drop{Data: transfer.TextData("x")})
	if at.Buf.String() != before {
		t.Fatal("只读的也插了")
	}
	at.St.ReadOnly = false

	files, err := transfer.FileData(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	// 访达的拖放板上也有文件名的文字：带文件的照旧打开文件，不插字
	item := files.Items()[0]
	b, _ := item.Read(transfer.FileList)
	both := transfer.New(transfer.NewItem(transfer.Bytes(transfer.FileList, b), transfer.Bytes(transfer.Text, []byte("README.md"))))
	a.DropOnDoc(at, transfer.Drop{Data: both, Operation: transfer.Copy})
	if d := a.CurrentDoc(); d == nil || d.Rel != "README.md" || at.Buf.String() != before {
		t.Fatalf("带文件的要打开文件：当前 %v", d)
	}
}

func TestDropOnTermIdle(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.Current()
	p := &TermPane{}
	a.DropOnTerm(r, p, transfer.Drop{Data: transfer.TextData("echo hi")})
	p.Exited = true
	a.DropOnTerm(r, p, transfer.Drop{Data: transfer.TextData("echo hi")})
	if a.FocusTerm || a.TermFocused || a.CurrentDoc() != nil {
		t.Fatal("shell 没起来 / 退出了的格子不接文字")
	}
	// 退出了的格子：访达拖来的文件照窗口的规矩打开
	files, err := transfer.FileData(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	a.DropOnTerm(r, p, transfer.Drop{Data: files, Operation: transfer.Copy})
	wait("开了", func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == "README.md" })
	if a.FocusTerm {
		t.Fatal("键盘不该给退出了的格子")
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
