package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// encodeAs 用 x/text 现编样本。
func encodeAs(t *testing.T, e encoding.Encoding, s string) []byte {
	t.Helper()
	b, err := e.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 换编码：按选的编码重读、记进会话（Tab.Enc），重开窗口和 ⇧⌘T 都按它读；改了没存的先问，点了才重读；文件不动。
func TestReopenWithEncoding(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	tw := encodeAs(t, traditionalchinese.Big5, "設定檔讀取失敗時，程式會回傳錯誤並記錄日誌。\n")
	os.WriteFile(filepath.Join(root, "tw.txt"), tw, 0o644)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	r := a.Repos[0]
	d := a.OpenFile(r, "tw.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	if d.Enc != docs.Big5 || d.ForcedEnc != "" || !d.Editable() {
		t.Fatalf("自动认成 %q，手动 %q，能编辑 %v", d.Enc, d.ForcedEnc, d.Editable())
	}
	a.ReopenWithEncoding(d, docs.GB18030)
	wait("按 GB18030 重读", func() bool { return d.Enc == docs.GB18030 && !d.Loading })
	if d.ForcedEnc != docs.GB18030 || !d.Broken || d.Editable() || d.Convertible() {
		t.Fatalf("按 GB18030：手动 %q，解不通 %v，能编辑 %v", d.ForcedEnc, d.Broken, d.Editable())
	}
	if tab, _ := sessionTab(d); tab.Enc != "GB18030" {
		t.Fatalf("会话里记成 %+v", tab)
	}
	a.SaveState()
	a.Close()
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if s := st.Sessions[ws]; s == nil || len(s.Tabs) != 1 || s.Tabs[0].Enc != "GB18030" {
		t.Fatalf("存的是 %+v", s)
	}

	b, wait := newTestAppIn(t, ws, stateDir)
	b.restoreTabs()
	t.Cleanup(b.Close)
	bd := b.Docs[0]
	wait("重开读完", func() bool { return bd.Buf != nil && !bd.Loading && bd.Enc == docs.GB18030 })
	if bd.ForcedEnc != docs.GB18030 || !bd.Broken {
		t.Fatalf("重开：%q 手动 %q", bd.Enc, bd.ForcedEnc)
	}
	b.CloseDoc(bd)
	b.ReopenClosed()
	again := b.CurrentDoc()
	wait("⇧⌘T 读完", func() bool { return again.Buf != nil && !again.Loading && again.Enc == docs.GB18030 })
	if again.ForcedEnc != docs.GB18030 {
		t.Fatalf("⇧⌘T 重开没按选的编码：%q", again.ForcedEnc)
	}
	b.ReopenWithEncoding(again, "")
	wait("自动认", func() bool { return again.Enc == docs.Big5 && !again.Loading })
	if tab, _ := sessionTab(again); again.ForcedEnc != "" || tab.Enc != "" {
		t.Fatalf("回到自动认：%q，会话 %q", again.ForcedEnc, tab.Enc)
	}

	// 改了没存的先问
	x := b.OpenFile(b.Repos[0], "a.txt", true)
	wait("读好", func() bool { return x.Buf != nil && !x.Loading })
	x.Buf.Set("改了\n")
	b.ReopenWithEncoding(x, docs.Big5)
	if !b.ConfirmOpen || b.Confirm.Title != "按 Big5 重新打开 a.txt？" || b.Confirm.Message != "没保存的改动会丢掉。" || x.ForcedEnc != "" {
		t.Fatalf("没先问：%+v", b.Confirm)
	}
	b.Confirm.Fn()
	wait("重读", func() bool { return x.Enc == docs.Big5 && !x.Loading && !x.Modified() })
	if x.Buf.String() == "改了\n" {
		t.Fatal("点了重新打开还留着改动")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "tw.txt")); !bytes.Equal(got, tw) {
		t.Error("换编码把文件改了")
	}
}

// 替换（搜索在解码后的文字上做，位置 = 缓冲区里的）：开着、能编辑的 GB18030 标签在缓冲区上换、一步撤销、
// 存回 GB18030（CRLF 留着）；字节上凑出来的假匹配搜不到；点结果选中的正好是那几个字。
func TestReplaceGB18030(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	// fake 的 GB18030 是 81 66：第二个字节是 "f"，后面跟 "oo" 就在字节里凑出一个 "foo"
	fake, err := simplifiedchinese.GB18030.NewDecoder().String("\x81\x66")
	if err != nil {
		t.Fatal(err)
	}
	text := "中文foo" + fake + "oo\r\n第二行 foo\r\n"
	os.WriteFile(filepath.Join(root, "gb.txt"), encodeAs(t, simplifiedchinese.GB18030, text), 0o644)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	d := a.OpenFile(a.Repos[0], "gb.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	if d.Enc != docs.GB18030 || !d.Editable() {
		t.Fatalf("应能编辑的 GB18030：%q %v", d.Enc, d.Editable())
	}
	s := &a.Search
	searchFor(t, a, wait, search.Query{Text: "二行"})
	a.OpenSearchMatch(matchAt(t, s, "gb.txt", 2, len("第")), true)
	if p, _ := d.Cursor(); p.Line != 1 || d.St.SelectedText() != "二行" {
		t.Fatalf("点结果：光标 %+v 选中 %q", p, d.St.SelectedText())
	}

	searchFor(t, a, wait, search.Query{Text: "foo"})
	if want := "gb.txt×2 1:6 2:10"; rowsOf(s) != want {
		t.Fatalf("结果 %q，想要 %q", rowsOf(s), want)
	}
	s.Replace = "替换"
	a.ReplaceFile(matchAt(t, s, "gb.txt", 1, len("中文")))
	before := "中文foo" + fake + "oo\n第二行 foo\n"
	if want := "中文替换" + fake + "oo\n第二行 替换\n"; d.Buf.String() != want || d.Modified() {
		t.Fatalf("缓冲区 %q，改过 %v", d.Buf.String(), d.Modified())
	}
	want := encodeAs(t, simplifiedchinese.GB18030, "中文替换"+fake+"oo\r\n第二行 替换\r\n")
	if got, _ := os.ReadFile(filepath.Join(root, "gb.txt")); !bytes.Equal(got, want) {
		t.Fatalf("盘上 %q", got)
	}
	if rows := rowsOf(s); rows != "" || a.Toast != "替换了 2 处，1 个文件" {
		t.Errorf("换完结果 %q，提示 %q", rows, a.Toast)
	}
	if !d.Buf.Undo() || d.Buf.String() != before || !d.Modified() || d.Buf.Undo() {
		t.Errorf("一步撤销：%q", d.Buf.String())
	}
}

// 没开着的文件：和打开时一样认编码，在文字上换、按原来的编码写回（GB18030、带 BOM 的 UTF-16 / UTF-8），
// 编不回原样的（Shift_JIS 里单独的 0x80）不动，提示里说是哪种、为什么。
func TestReplaceEncodedOnDisk(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	gbLines := []string{"第一行不动 中文", "第二行 旧词 foo", "第三行"}
	gb := encodeAs(t, simplifiedchinese.GB18030, strings.Join(gbLines, "\r\n")+"\r\n")
	jaLine := "設定ファイルの読み込みに失敗した場合、プログラムはエラーを返してログに記録します。"
	ja := append(encodeAs(t, japanese.ShiftJIS, jaLine+"foo\n"), 0x80, '\n')
	jaAt := "ja.txt×1 1:" + strconv.Itoa(len(jaLine)) // 列是 UTF-8 字节
	write := func(name string, b []byte) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, b, 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gbPath := write("gb.txt", gb)
	jaPath := write("ja.txt", ja)
	lePath := write("le.txt", docs.Encode("ascii foo\n中文\n", docs.UTF16LE))
	bomPath := write("bom.txt", []byte("\xef\xbb\xbf中文 foo\n"))
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	searchFor(t, a, wait, search.Query{Text: "foo"})
	if want := "bom.txt×1 1:7 gb.txt×1 2:17 " + jaAt + " le.txt×1 1:6"; rowsOf(s) != want {
		t.Fatalf("结果 %q，想要 %q", rowsOf(s), want)
	}
	if got := s.Res.Matches[matchAt(t, s, "ja.txt", 1, len(jaLine))].Enc; got != docs.ShiftJIS {
		t.Fatalf("ja.txt 认成 %q", got)
	}
	s.Replace = "新"
	a.ReplaceMatches([]int{0, 1, 2, 3})
	if want := "替换了 3 处，3 个文件；1 个文件因为编码只读（Shift_JIS 编不回原样），没换"; a.Toast != want {
		t.Errorf("提示 %q，想要 %q", a.Toast, want)
	}
	got := mustRead(t, gbPath)
	if want := encodeAs(t, simplifiedchinese.GB18030, "第一行不动 中文\r\n第二行 旧词 新\r\n第三行\r\n"); got != string(want) {
		t.Errorf("gb.txt 盘上 %q", got)
	}
	// 没动的行逐字节一样，权限照旧
	if old, now := bytes.Split(gb, []byte("\r\n")), strings.Split(got, "\r\n"); string(old[0]) != now[0] || string(old[2]) != now[2] {
		t.Errorf("没动的行变了：%q", now)
	}
	if fi, _ := os.Stat(gbPath); fi.Mode().Perm() != 0o640 {
		t.Errorf("权限 %v", fi.Mode().Perm())
	}
	if got := mustRead(t, lePath); got != string(docs.Encode("ascii 新\n中文\n", docs.UTF16LE)) || !strings.HasPrefix(got, "\xff\xfe") {
		t.Errorf("le.txt 盘上 %q", got)
	}
	if got := mustRead(t, bomPath); got != "\xef\xbb\xbf中文 新\n" {
		t.Errorf("bom.txt 盘上 %q", got)
	}
	if got := mustRead(t, jaPath); got != string(ja) {
		t.Errorf("Shift-JIS 的文件被改了")
	}
	if rows := rowsOf(s); rows != jaAt {
		t.Errorf("换完结果 %q", rows)
	}
	// 只有只读编码的：提示只说这个
	a.ReplaceMatches([]int{0})
	if want := "1 个文件因为编码只读（Shift_JIS 编不回原样），没换"; a.Toast != want {
		t.Errorf("提示 %q，想要 %q", a.Toast, want)
	}
}
