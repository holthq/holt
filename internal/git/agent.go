package git

import "strings"

// 这个文件认"agent 写的提交"：提交正文里有 Co-Authored-By: trailer，名字或邮箱像编码 agent。
// 右栏历史在这样的提交行右边放一个四角星，头部的"只看 agent 写的"开关只留这些提交。

// agentPat 是一个 agent 的认法：words 是名字或邮箱用户名里的一个词（不分大小写，按非字母数字切开比），
// domains 是邮箱域名（等于或是它的子域名）。
type agentPat struct {
	name    string
	words   []string
	domains []string
}

// agentPats 是认得的 agent，按顺序比，先对上的算。新的 agent 加在这里。
var agentPats = []agentPat{
	{name: "Claude", words: []string{"claude"}, domains: []string{"anthropic.com"}},
	{name: "Codex", words: []string{"codex", "chatgpt"}, domains: []string{"openai.com"}},
	{name: "Copilot", words: []string{"copilot"}},
	{name: "Gemini", words: []string{"gemini", "jules"}},
	{name: "Cursor", words: []string{"cursor", "cursoragent"}, domains: []string{"cursor.com", "cursor.sh"}},
	{name: "Devin", words: []string{"devin"}, domains: []string{"cognition.ai"}},
	{name: "Aider", words: []string{"aider"}, domains: []string{"aider.chat"}},
	{name: "Amp", domains: []string{"ampcode.com"}},
	{name: "Windsurf", words: []string{"windsurf", "codeium"}, domains: []string{"codeium.com", "windsurf.com"}},
}

// AgentOf 是提交正文 body 里 Co-Authored-By: trailer 认出的 agent 名字（几个 trailer 时第一个认出的），没有为空。
// trailer 的键不分大小写；普通人的 Co-Authored-By 不算。
func AgentOf(body string) string {
	if body == "" {
		return ""
	}
	for line := range strings.SplitSeq(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "co-authored-by") {
			continue
		}
		if name := agentName(v); name != "" {
			return name
		}
	}
	return ""
}

// agentName 认一个 trailer 的值（"名字 <邮箱>"）。
func agentName(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	name, email := v, ""
	if i := strings.IndexByte(v, '<'); i >= 0 {
		name = v[:i]
		email = strings.TrimSuffix(strings.TrimSpace(v[i+1:]), ">")
	}
	user, domain, _ := strings.Cut(email, "@")
	words := strings.FieldsFunc(name+" "+user, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, p := range agentPats {
		for _, d := range p.domains {
			if domain == d || strings.HasSuffix(domain, "."+d) {
				return p.name
			}
		}
		for _, w := range words {
			for _, pw := range p.words {
				if w == pw {
					return p.name
				}
			}
		}
	}
	return ""
}
