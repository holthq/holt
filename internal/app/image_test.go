package app

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// pngBytes 是 w×h、一种颜色的 PNG。
func pngBytes(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// 以十六进制查看：文本、图片切过去是只读的 dump、没有大纲 / blame / 预览，切得回来；改过没保存的不让切；
// 二进制自己就是十六进制、关不掉；记进会话，重开照十六进制读；“用默认应用打开”交给 ShowPath。文件都不动。
func TestHexToggle(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "bin.dat"), []byte{1, 0, 2, 0xff}, 0o644)
	pic := pngBytes(3, 2, color.RGBA{255, 0, 0, 255})
	os.WriteFile(filepath.Join(root, "pic.png"), pic, 0o644)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	r := a.Repos[0]

	d := a.OpenFile(r, "README.md", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	if !a.CanHex(d) || a.hexOn() {
		t.Fatalf("文本应能切：CanHex %v", a.CanHex(d))
	}
	d.Preview = true
	a.Exec("view.hex")
	wait("十六进制", func() bool { return d.Hex && d.Buf != nil && !d.Loading })
	if !strings.HasPrefix(d.Buf.String(), "00000000  23 20 64 65 6d 6f 0a") || !d.St.ReadOnly || d.Editable() || d.Preview || d.CanPreview() || a.CanBlame(d) {
		t.Fatalf("dump %q，只读 %v、预览 %v", d.Buf.String()[:30], d.St.ReadOnly, d.Preview)
	}
	if d.Base != nil || d.Outline() != nil || d.HexOnly() || d.HexTotal != int64(len("# demo\n\n第一行\n")) {
		t.Errorf("十六进制不该有基准 / 大纲：base %v，共 %d 字节", d.Base != nil, d.HexTotal)
	}
	if tab, _ := sessionTab(d); !tab.Hex {
		t.Errorf("会话里没记：%+v", tab)
	}
	a.ToggleHex()
	wait("切回文本", func() bool { return !d.Hex && d.Buf != nil && !d.Loading })
	if d.Buf.String() != "# demo\n\n第一行\n" || !d.Editable() {
		t.Fatalf("切回来：%q", d.Buf.String())
	}
	d.Buf.Set("改了\n")
	if a.CanHex(d) {
		t.Error("改过没保存的不该能切")
	}
	d.Buf.Set("# demo\n\n第一行\n")

	b := a.OpenFile(r, "bin.dat", true)
	wait("读好二进制", func() bool { return b.Buf != nil && !b.Loading })
	if !b.Hex || !b.HexOnly() || a.CanHex(b) || b.Info != "" || !strings.HasPrefix(b.Buf.String(), "00000000  01 00 02 ff ") {
		t.Fatalf("二进制：hex %v only %v info %q", b.Hex, b.HexOnly(), b.Info)
	}
	var shown []string
	a.ShowPath = func(p string) error { shown = append(shown, p); return nil }
	a.OpenDefault(b)
	if len(shown) != 1 || shown[0] != filepath.Join(root, "bin.dat") {
		t.Errorf("用默认应用打开：%q", shown)
	}
	a.ShowPath = func(string) error { return errors.New("没有能打开它的应用") }
	a.OpenDefault(b)
	if a.Toast != "打不开 bin.dat：没有能打开它的应用" {
		t.Errorf("打不开的提示 %q", a.Toast)
	}

	p := a.OpenFile(r, "pic.png", true)
	wait("读好图片", func() bool { return p.Bitmap != nil && !p.Loading })
	a.SetHex(p, true)
	wait("图片的十六进制", func() bool { return p.Hex && p.Buf != nil && !p.Loading })
	if p.Bitmap != nil || p.HexOnly() || !a.CanHex(p) || !strings.HasPrefix(p.Buf.String(), "00000000  89 50 4e 47 ") {
		t.Fatalf("图片的十六进制：bitmap %v only %v", p.Bitmap != nil, p.HexOnly())
	}

	a.SaveState()
	a.Close()
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	hexTabs := map[string]bool{}
	for _, tab := range st.Sessions[ws].Tabs {
		hexTabs[filepath.Base(tab.Path)] = tab.Hex
	}
	if !hexTabs["pic.png"] || !hexTabs["bin.dat"] || hexTabs["README.md"] {
		t.Fatalf("存的十六进制：%v", hexTabs)
	}
	n, wait := newTestAppIn(t, ws, stateDir)
	n.restoreTabs()
	t.Cleanup(n.Close)
	var np *docs.Doc
	for _, x := range n.Docs {
		if x.Name() == "pic.png" {
			np = x
		}
	}
	if np == nil {
		t.Fatal("没重开 pic.png")
	}
	wait("重开读完", func() bool { return np.Buf != nil && !np.Loading })
	if !np.Hex || np.Bitmap != nil {
		t.Fatalf("重开没照十六进制：hex %v bitmap %v", np.Hex, np.Bitmap != nil)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "pic.png")); !bytes.Equal(got, pic) {
		t.Error("图片被改了")
	}
}

// 图片的 diff：工作区改了的 PNG 左右两张（旧的从 HEAD、新的从磁盘），新加的旧的那边没有；
// 提交里的那个版本按图片开，不再是“二进制文件”。
func TestImageDiff(t *testing.T) {
	r := testutil.NewRepo(t, "pics")
	old := pngBytes(4, 4, color.RGBA{0, 0, 255, 255})
	os.WriteFile(filepath.Join(r.Root, "logo.png"), old, 0o644)
	r.Commit("加 logo")
	head := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	os.WriteFile(filepath.Join(r.Root, "logo.png"), pngBytes(8, 6, color.RGBA{0, 255, 0, 128}), 0o644)
	os.WriteFile(filepath.Join(r.Root, "new.png"), pngBytes(2, 2, color.RGBA{255, 255, 0, 255}), 0o644)
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Repos[0]
	a.RefreshRepo(repo, WhyManual)
	wait("读状态", idle(repo))

	a.ShowDiffFor(repo, "logo.png")
	d := a.CurrentDoc()
	wait("读 diff", func() bool { return d != nil && !d.Loading })
	if d.ImgDiff == nil || d.DiffSt != nil || d.Info != "" {
		t.Fatalf("没按图片 diff：info %q", d.Info)
	}
	s := d.ImgDiff
	if s[0].Bitmap == nil || s[1].Bitmap == nil || s[0].Size != len(old) {
		t.Fatalf("两边：%+v", s)
	}
	if w, h := s[1].Bitmap.Size(); w != 8 || h != 6 {
		t.Errorf("新的 %d×%d", w, h)
	}

	a.ShowDiffFor(repo, "new.png")
	n := a.CurrentDoc()
	wait("读新文件的 diff", func() bool { return n != nil && n != d && !n.Loading })
	if n.ImgDiff == nil || n.ImgDiff[0].Bitmap != nil || n.ImgDiff[0].Err != "" || n.ImgDiff[1].Bitmap == nil {
		t.Fatalf("新加的：info %q %+v", n.Info, n.ImgDiff)
	}

	v := a.OpenVersion(repo, git.Change{Kind: git.InCommit, Path: "logo.png", Code: 'A', Commit: head})
	wait("读版本", func() bool { return !v.Loading })
	if v.Bitmap == nil || v.Info != "" || v.Buf != nil {
		t.Fatalf("提交里的图片：info %q", v.Info)
	}
	if w, _ := v.Bitmap.Size(); w != 4 {
		t.Errorf("版本是 %d 宽，应是提交里的 4", w)
	}
}
