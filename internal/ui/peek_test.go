package ui

import (
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/testutil"
)

// 装订线改动块的就地卡：点块里的行号（OnGutterClick）弹卡，头一行“第 1 / 2 处改动”、−1 +1，下面是 HEAD 里的旧行；
// 纯新增的块写一句；没保存时暂存灰着；点还原，编辑器里那一行换回去；Esc 关；块外的行号照旧选中那一行。
func TestPeekCardUI(t *testing.T) {
	repo := testutil.NewRepo(t, "peek")
	repo.Write("a.txt", "1\n2\n3\n4\n5\n6\n7\n8\n9\n")
	repo.Commit("base")
	repo.Write("a.txt", "1\n二\n3\n4\n5\n6\n7\n8\n八点五\n9\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	d := a.OpenFile(r, "a.txt", true)
	wait("读好", func() bool { return d.Buf != nil && d.Base != nil && !d.Loading })
	tt.Frame()
	tt.Frame()
	if d.St.OnGutterClick == nil {
		t.Fatal("有改动块时该接管装订线的点击")
	}
	d.St.OnGutterClick(1)
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"第 1 / 2 处改动", "−1", "+1", "2", "还原", "暂存", "在 Diff 中打开"} {
		if !shown(tt, s) {
			t.Fatalf("卡上没有 %q：%q", s, tt.Texts())
		}
	}
	tt.Click("下一处改动")
	tt.Frame()
	tt.Frame()
	if !shown(tt, "第 2 / 2 处改动") || !shown(tt, "新增 1 行，HEAD 里没有") {
		t.Fatalf("↓ 后：%q", tt.Texts())
	}
	tt.Click("上一处改动")
	tt.Frame()
	if err := tt.Click("还原"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if got := d.Buf.Line(1); got != "2" || !d.Modified() {
		t.Fatalf("还原后第 2 行 %q，脏 %v", got, d.Modified())
	}
	if !shown(tt, "第 1 / 1 处改动") {
		t.Fatalf("还原后卡落到剩下那块：%q", tt.Texts())
	}
	if b, ok := tt.Find("暂存"); !ok || b.W == 0 {
		t.Fatal("暂存按钮该在（灰着）")
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if a.Peek.Doc != nil || shown(tt, "第 1 / 1 处改动") {
		t.Fatalf("Esc 该关：%+v", a.Peek)
	}
	d.St.OnGutterClick(4)
	if s := d.St.Selection(); d.Buf.Slice(min(s.Anchor, s.Head), max(s.Anchor, s.Head)) != "5\n" {
		t.Errorf("块外的行号该选中那一行：%+v", s)
	}
}
