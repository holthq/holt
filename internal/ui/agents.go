package ui

import (
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// agent 状态的符号：终端标签上、后面 Git 栏的工作树行上用同一套，不加别的颜色。
//   - 等你：7px 的琥珀色圆点（浅色 #E8950C，深色 #F2A93B）；
//   - 运行中：13px 的灰色转圈（次要文字色，3/4 圈弧线加 22% 的底圈，慢慢转）；
//   - 空闲、没有 agent：不画。

// iconAgentSpin 是运行中的转圈：22% 的底圈加从 12 点顺时针 3/4 圈的弧。
var iconAgentSpin = icon("spinner-ring")

// agentWaitColor 是"等你"圆点的琥珀色。
func agentWaitColor(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.Hex("#F2A93B")
	}
	return wui.Hex("#E8950C")
}

// agentMark 画 agent 状态 k 的符号（没有就不画）。
func agentMark(c *wui.Context, k app.AgentKind) {
	t := c.Theme()
	switch k {
	case app.AgentWaiting:
		wui.Box(c.Key("agent-wait")).Size(7, 7).Radius(999).Shrink(0).Background(agentWaitColor(t)).
			Label(i18n.T("agent 在等你")).Tooltip(i18n.T("agent 在等你"))
	case app.AgentRunning:
		spin := wui.Icon(c.Key("agent-run"), iconAgentSpin).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0).
			Label(i18n.T("agent 运行中")).Tooltip(i18n.T("agent 运行中"))
		spin.Rotate(spin.Loop("spin", 1600*time.Millisecond, wui.Linear) * 360)
	}
}
