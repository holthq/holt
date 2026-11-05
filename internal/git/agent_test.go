package git

import "testing"

// agent trailer 的识别：键不分大小写、几个 trailer 取认出的那个、普通人的 Co-Authored-By 不算。
func TestAgentOf(t *testing.T) {
	cases := []struct{ body, want string }{
		{"", ""},
		{"修了个 bug", ""},
		{"正文\n\nCo-Authored-By: Claude <noreply@anthropic.com>", "Claude"},
		{"co-authored-by: Claude Opus 5.5 <noreply@anthropic.com>", "Claude"},
		{"CO-AUTHORED-BY:   someone <bot@anthropic.com>", "Claude"},
		{"Co-authored-by: 张三 <z@example.com>", ""},
		{"Co-authored-by: Claudette Dupont <c@example.com>", ""}, // 名字里含 claude 但不是这个词
		{"Co-authored-by: 张三 <z@example.com>\nCo-authored-by: Codex <codex@openai.com>", "Codex"},
		{"Co-authored-by: Copilot <175728472+Copilot@users.noreply.github.com>", "Copilot"},
		{"Co-authored-by: gemini-code-assist[bot] <bot@users.noreply.github.com>", "Gemini"},
		{"Co-authored-by: Cursor Agent <cursoragent@cursor.com>", "Cursor"},
		{"Signed-off-by: Claude <noreply@anthropic.com>", ""}, // 别的 trailer 不算
		{"提到 Co-Authored-By: Claude 只是正文里的一句", ""},
	}
	for _, c := range cases {
		if got := AgentOf(c.body); got != c.want {
			t.Errorf("AgentOf(%q) = %q，应为 %q", c.body, got, c.want)
		}
	}
}
