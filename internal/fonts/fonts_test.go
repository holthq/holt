package fonts

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPick(t *testing.T) {
	withMeslo := []ui.FontFamily{{Name: "Menlo", Monospaced: true}, {Name: "MesloLGS Nerd Font Mono", Monospaced: true}}
	if got := Pick("", withMeslo); got != "MesloLGS Nerd Font Mono" {
		t.Fatalf("装了 Meslo 时应该用它，得到 %q", got)
	}
	if got := Pick("Hack", withMeslo); got != "Hack" {
		t.Fatalf("WORKBENCH_FONT 应该优先，得到 %q", got)
	}
	other := []ui.FontFamily{{Name: "Menlo", Monospaced: true}, {Name: "0xProto Nerd Font Mono", Monospaced: true}}
	if got := Pick("", other); got != "0xProto Nerd Font Mono" {
		t.Fatalf("别的 Nerd Font 等宽家族也应该用，得到 %q", got)
	}
	// 没有系统 Nerd Font 时退到自带字体（go test 下从上级目录的 resources/fonts 读）。
	if got := Pick("", []ui.FontFamily{{Name: "Menlo", Monospaced: true}}); got != Bundled {
		t.Fatalf("没有系统 Nerd Font 时应该用自带字体，得到 %q", got)
	}
	t.Logf("这台机器会用 %q", Mono())
}
