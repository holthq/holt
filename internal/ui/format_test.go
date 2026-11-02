package ui

import (
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
)

// 格式化（临时仓库，假命令）：设置"格式化"页一种语言一行，改了记进设置、清空回到内置默认；加一种语言、移除；
// 编辑器有焦点时 ⇧⌥F 格式化（随后送来的 Ï 吞掉，只吞一段），跑的时候状态栏"正在格式化…"；右键"格式化文档"。
func TestFormatUI(t *testing.T) {
	repo := testutil.NewRepo(t, "fmt")
	repo.Write("a.go", "package a\n")
	repo.Commit("base")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	a.St.Settings.Shell = "/bin/sh"
	d := a.OpenFile(a.Repos[0], "a.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()

	st := wui.NewTester(SettingsView(a), 600, 1400)
	st.SetLocale(i18n.Locale())
	st.Frame()
	mustClick(t, st, "格式化")
	mustText(t, st, "保存时格式化")
	mustClick(t, st, "Go 的格式化命令")
	st.Type("sleep 0.3; tr a-z A-Z")
	st.Frame()
	if got := a.St.Settings.Formatters["Go"]; got != "sleep 0.3; tr a-z A-Z" {
		t.Fatalf("Go 的命令记成 %q", got)
	}
	mustClick(t, st, "选一种语言…")
	mustClick(t, st, "Kotlin")
	st.Frame()
	if v, ok := a.St.Settings.Formatters["Kotlin"]; !ok || v != "" || !st.HasText("移除") {
		t.Fatalf("加 Kotlin：%q %v", v, ok)
	}
	mustClick(t, st, "移除")
	st.Frame()
	if _, ok := a.St.Settings.Formatters["Kotlin"]; ok || st.HasText("移除") {
		t.Fatal("移除 Kotlin 没拿掉")
	}

	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	if !d.St.Focused() {
		t.Fatal("编辑器没拿到焦点")
	}
	tt.Key(wui.Shift|wui.Alt, wui.KeyF)
	tt.Type("Ï") // macOS 上 mygo 随后照样送 ⌥ 打出的字，要吞掉
	tt.Frame()
	if !d.Formatting || !tt.HasText("正在格式化…") || d.Buf.String() != "package a\n" {
		t.Fatalf("⇧⌥F：在跑 %v，文字 %q", d.Formatting, d.Buf.String())
	}
	wait("格式化完", func() bool { return !d.Formatting })
	tt.Frame()
	if d.Buf.String() != "PACKAGE A\n" || tt.HasText("正在格式化…") {
		t.Fatalf("⇧⌥F 之后 %q", d.Buf.String())
	}
	tt.Type("q") // 只吞一段
	tt.Frame()
	if !strings.Contains(d.Buf.String(), "q") || !d.Buf.Undo() || d.Buf.String() != "PACKAGE A\n" {
		t.Fatalf("吞多了：%q", d.Buf.String())
	}

	a.St.Settings.Formatters["Go"] = "tr A-Z a-z"
	lang, ok := tt.Find("语言 Go")
	if !ok {
		t.Fatal("状态栏没有语言")
	}
	tt.RightClickAt(lang.X-300, 200)
	tt.Frame()
	if err := tt.ChooseMenuItem("格式化文档"); err != nil {
		t.Fatalf("右键菜单 %v：%q", err, tt.Menu())
	}
	wait("右键格式化完", func() bool { return !d.Formatting && d.Buf.String() == "package a\n" })
}
