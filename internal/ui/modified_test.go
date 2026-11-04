package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 改过未存的大文件每帧不再整篇比较（#12）：标签的圆点和状态栏每帧都问 Doc.Modified，
// 版本不变时只能用缓存，画 N 帧 Buffer.Modified 只问常数次。
// 撤回到原样、保存、外部改了重读之后，未保存的标记照样跟着变。
func TestModifiedCached(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	var b strings.Builder
	for i := 0; b.Len() < 200<<10; i++ {
		fmt.Fprintf(&b, "func f%d() int { return %d } // 一行注释\n", i, i)
	}
	orig := "package big\n\n" + b.String()
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := newUI(t, ws, t.TempDir())
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	d := a.OpenFile(r, "big.go", true)
	wait("big.go 读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()
	if d.Modified() || tt.HasText("未保存 · ⌘S") {
		t.Fatal("刚打开就算改过")
	}

	mustClick(t, tt, "编辑 big.go")
	d.St.Select(0, 0)
	tt.Type("x")
	tt.Frame()
	if !d.Modified() || !tt.HasText("未保存 · ⌘S") {
		t.Fatalf("输入后应标为未保存: modified=%v", d.Modified())
	}

	// 改过未存，连画 N 帧（中间挪鼠标）：Buffer.Modified 只问常数次
	n0 := d.ModifiedChecks()
	for i := range 50 {
		tt.Move(float32(200+i), 300)
		tt.Frame()
	}
	if got := d.ModifiedChecks() - n0; got > 1 {
		t.Errorf("画 50 帧问了 %d 次 Buffer.Modified，应只问常数次", got)
	}
	if !d.Modified() {
		t.Error("画了几帧后改动没了")
	}

	// 撤回到原样 → 未保存标记消失
	tt.Key(wui.Cmd, wui.KeyZ)
	tt.Frame()
	if d.Buf.String() != orig {
		t.Fatalf("⌘Z 没撤回到原样")
	}
	if d.Modified() || tt.HasText("未保存 · ⌘S") {
		t.Error("撤回到和磁盘一样后还标着未保存")
	}

	// 再改、保存 → 标记消失（保存不动内容版本，靠存盘次数让缓存失效）
	d.St.Select(0, 0)
	tt.Type("y")
	tt.Frame()
	if !d.Modified() {
		t.Fatal("再改后应算改过")
	}
	tt.Key(wui.Cmd, wui.KeyS)
	tt.Frame()
	if data, _ := os.ReadFile(d.Abs); !strings.HasPrefix(string(data), "ypackage big") {
		t.Fatalf("⌘S 没写盘: %q", string(data[:20]))
	}
	if d.Modified() || tt.HasText("未保存 · ⌘S") {
		t.Error("保存后还标着未保存")
	}
	n0 = d.ModifiedChecks()
	for range 20 {
		tt.Frame()
	}
	if got := d.ModifiedChecks() - n0; got > 0 {
		t.Errorf("保存后画 20 帧问了 %d 次 Buffer.Modified", got)
	}

	// 外部改了文件、⌘R 重读 → 没改过；再敲字又算改过
	if err := os.WriteFile(d.Abs, []byte("package big\n\n// 别处改的\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tt.Key(wui.Cmd, wui.KeyR)
	wait("big.go 重读", func() bool { return strings.Contains(d.Buf.String(), "别处改的") })
	tt.Frame()
	if d.Modified() || tt.HasText("未保存 · ⌘S") {
		t.Error("重读后还标着未保存")
	}
	d.St.Select(0, 0)
	tt.Type("z")
	tt.Frame()
	if !d.Modified() || !tt.HasText("未保存 · ⌘S") {
		t.Error("重读后再改应标为未保存")
	}
}
