package main

import (
	"testing"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/workspace"
)

// 主窗口的材质：窗口底是柔和（默认）、纯色时什么都不透；通透时默认侧栏，设置里选的那种，WORKBENCH_MATERIAL 压过设置。
func TestVibrancy(t *testing.T) {
	t.Setenv("WORKBENCH_MATERIAL", "")
	for _, c := range []struct {
		s    workspace.Settings
		env  string
		want mygo.Vibrancy
	}{
		{workspace.Settings{}, "", mygo.VibrancyNone}, // 柔和
		{workspace.Settings{Backdrop: "flat"}, "", mygo.VibrancyNone},
		{workspace.Settings{Material: "menu"}, "", mygo.VibrancyNone},
		{workspace.Settings{}, "menu", mygo.VibrancyNone},
		{workspace.Settings{Backdrop: "flat"}, "menu", mygo.VibrancyNone},
		{workspace.Settings{Backdrop: "vibrancy"}, "", mygo.VibrancySidebar},
		{workspace.Settings{Backdrop: "vibrancy", Material: "menu"}, "", mygo.VibrancyMenu},
		{workspace.Settings{Backdrop: "vibrancy", Material: "glass"}, "", mygo.VibrancyGlass},
		{workspace.Settings{Backdrop: "vibrancy", Material: "menu"}, "popover", mygo.VibrancyPopover},
		{workspace.Settings{Backdrop: "vibrancy"}, "under-window", mygo.VibrancyUnderWindow},
		{workspace.Settings{Backdrop: "vibrancy"}, "hud", mygo.VibrancyHUD}, // 设置里没有了，开发开关照样能试
	} {
		t.Setenv("WORKBENCH_MATERIAL", c.env)
		if got := vibrancy(&c.s); got != c.want {
			t.Errorf("%+v WORKBENCH_MATERIAL=%q：%q，应是 %q", c.s, c.env, got, c.want)
		}
	}
	// 设置里能选的都是 mygo 认的材质名
	t.Setenv("WORKBENCH_MATERIAL", "")
	for _, m := range workspace.Materials[1:] {
		if v := vibrancy(&workspace.Settings{Backdrop: "vibrancy", Material: m}); string(v) != m {
			t.Errorf("材质 %q 成了 %q", m, v)
		}
	}
}
