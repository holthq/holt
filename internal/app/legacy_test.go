package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"
	"golang.org/x/text/encoding/japanese"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/search"
	"github.com/holthq/holt/internal/testutil"
)

// 老编码能编辑：Shift_JIS 的文件改了按 Shift_JIS 存；加了写不进去的 emoji，⌘S 先问"转成 UTF-8 保存？"
// （不点就不存，文件不动），点了按 UTF-8 写；自动保存遇到这种不问、只提示。替换按原来的编码写回，替换成写不进去的字的不换。
func TestSaveUnencodable(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	line := "設定ファイルの読み込みに失敗した場合、プログラムはエラーを返してログに記録します。"
	ja := encodeAs(t, japanese.ShiftJIS, line+"\n")
	p := filepath.Join(root, "ja.txt")
	os.WriteFile(p, ja, 0o644)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	d := a.OpenFile(a.Repos[0], "ja.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	if d.Enc != docs.ShiftJIS || !d.Editable() {
		t.Fatalf("读成 %q，能编辑 %v", d.Enc, d.Editable())
	}

	d.Buf.Edit(editor.Edit{Start: 0, End: 0, Text: "新"})
	a.Save(d)
	if got, _ := os.ReadFile(p); a.ConfirmOpen || d.Modified() || !bytes.Equal(got, encodeAs(t, japanese.ShiftJIS, "新"+line+"\n")) {
		t.Fatalf("按 Shift_JIS 存：问 %v，改过 %v，%q", a.ConfirmOpen, d.Modified(), got)
	}

	d.Buf.Edit(editor.Edit{Start: 0, End: 0, Text: "😀"})
	saved, _ := os.ReadFile(p)
	if a.WriteDoc(d, true) == nil {
		t.Fatal("有写不进去的字还算存上了")
	}
	c := a.Confirm
	if !a.ConfirmOpen || c.Title != "有 1 个字 Shift_JIS 写不了：转成 UTF-8 保存？" || c.Button != "转成 UTF-8" ||
		!strings.Contains(c.Message, "git 里会显示整个文件改了") {
		t.Fatalf("问话 %+v", c)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, saved) || !d.Modified() {
		t.Fatal("没点就存了")
	}
	a.Confirm, a.ConfirmOpen = nil, false // 取消：不存
	a.St.Settings.AutoSave = "blur"
	a.OnBlur()
	if got, _ := os.ReadFile(p); a.ConfirmOpen || !bytes.Equal(got, saved) || a.Toast != "ja.txt 没自动保存：有 1 个字 Shift_JIS 写不了" {
		t.Fatalf("自动保存：问 %v，提示 %q", a.ConfirmOpen, a.Toast)
	}
	a.St.Settings.AutoSave = ""
	a.Save(d)
	a.Confirm.Fn()
	if got, _ := os.ReadFile(p); string(got) != "😀新"+line+"\n" || d.Enc != docs.UTF8 || d.Modified() || a.Toast != "已按 UTF-8 保存 ja.txt" {
		t.Fatalf("转成 UTF-8 存：%q %q，提示 %q", got, d.Enc, a.Toast)
	}

	// 替换：没开着的 Shift_JIS 按 Shift_JIS 写回；替换成 emoji 的不换，报出来
	sj := encodeAs(t, japanese.ShiftJIS, line+"foo\n")
	q := filepath.Join(root, "sj.txt")
	os.WriteFile(q, sj, 0o644)
	s := &a.Search
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "😀"
	a.ReplaceMatches([]int{matchAt(t, s, "sj.txt", 1, len(line))})
	if got, _ := os.ReadFile(q); !bytes.Equal(got, sj) {
		t.Error("替换成写不进去的字也换了")
	}
	if !a.AlertOpen || !strings.Contains(a.AlertMsg, "sj.txt：替换成的文字有 1 个字 Shift_JIS 写不了") {
		t.Errorf("没报：提示 %q，弹框 %q", a.Toast, a.AlertMsg)
	}
	a.AlertOpen = false
	s.Replace = "済"
	a.ReplaceMatches([]int{matchAt(t, s, "sj.txt", 1, len(line))})
	if got, _ := os.ReadFile(q); !bytes.Equal(got, encodeAs(t, japanese.ShiftJIS, line+"済\n")) {
		t.Errorf("按 Shift_JIS 写回：%q，提示 %q", got, a.Toast)
	}
}
