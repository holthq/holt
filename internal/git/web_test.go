package git

import "testing"

// 远程地址推网页：https / ssh / scp 三种写法、带用户名密码和端口的、GitLab 子组、
// 五种风格各自怎么认；本地路径、file:// 和只有一段路径的为空。
func TestWebOf(t *testing.T) {
	for _, tc := range []struct {
		remote string
		kind   WebKind
		site   string
		base   string
	}{
		{"https://github.com/egoist/mygo.git", WebGitHub, "GitHub", "https://github.com/egoist/mygo"},
		{"https://user:token@github.com/egoist/mygo", WebGitHub, "GitHub", "https://github.com/egoist/mygo"},
		{"git@github.com:egoist/mygo.git", WebGitHub, "GitHub", "https://github.com/egoist/mygo"},
		{"ssh://git@ssh.github.com:443/egoist/mygo.git", WebGitHub, "GitHub", "https://github.com/egoist/mygo"},
		{"git@gitlab.com:group/sub/proj.git", WebGitLab, "GitLab", "https://gitlab.com/group/sub/proj"},
		{"https://gitlab.example.com/team/proj/", WebGitLab, "GitLab", "https://gitlab.example.com/team/proj"},
		{"ssh://git@gitlab.example.com:2222/team/sub/proj.git", WebGitLab, "GitLab", "https://gitlab.example.com/team/sub/proj"},
		{"https://bitbucket.org/team/proj.git", WebBitbucket, "Bitbucket", "https://bitbucket.org/team/proj"},
		{"git@bitbucket.org:team/proj.git", WebBitbucket, "Bitbucket", "https://bitbucket.org/team/proj"},
		{"ssh://git@codeberg.org/ada/notes.git", WebGitea, "Codeberg", "https://codeberg.org/ada/notes"},
		{"git@gitea.example.com:ada/notes.git", WebGitea, "Gitea", "https://gitea.example.com/ada/notes"},
		{"https://forgejo.example.com/ada/notes", WebGitea, "Forgejo", "https://forgejo.example.com/ada/notes"},
		{"git@git.example.com:ada/notes.git", WebGeneric, "git.example.com", "https://git.example.com/ada/notes"},
		{"ssh://git@git.example.com:2222/ada/notes.git", WebGeneric, "git.example.com", "https://git.example.com/ada/notes"},
		{"https://git.example.com:8443/ada/notes.git", WebGeneric, "git.example.com:8443", "https://git.example.com:8443/ada/notes"},
		{"http://10.0.0.5/ada/notes.git", WebGeneric, "10.0.0.5", "http://10.0.0.5/ada/notes"},
		{"/tmp/origin.git", "", "", ""},
		{"../origin.git", "", "", ""},
		{`C:\repos\x.git`, "", "", ""},
		{"file:///tmp/github.com/x/y.git", "", "", ""},
		{"https://github.com/onlyowner", "", "", ""},
		{"", "", "", ""},
	} {
		w := ParseWeb(tc.remote)
		if w.Kind != tc.kind || w.Site() != tc.site || w.Base != tc.base {
			t.Errorf("%q → %+v（%s），应为 %s %s %s", tc.remote, w, w.Site(), tc.kind, tc.site, tc.base)
		}
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	web := func(k WebKind) Web { return Web{Kind: k, Host: "h.test", Base: "https://h.test/o/r"} }
	for _, tc := range []struct {
		kind                   WebKind
		commit, file, dir, one string
		lines                  string
	}{
		{WebGitHub, "/commit/" + sha, "/blob/" + sha + "/docs/%E8%AF%B4%E6%98%8E%201.md", "/tree/feature/x/src", "#L7", "#L10-L20"},
		{WebGitLab, "/-/commit/" + sha, "/-/blob/" + sha + "/docs/%E8%AF%B4%E6%98%8E%201.md", "/-/tree/feature/x/src", "#L7", "#L10-20"},
		{WebBitbucket, "/commits/" + sha, "/src/" + sha + "/docs/%E8%AF%B4%E6%98%8E%201.md", "/src/feature/x/src", "#lines-7", "#lines-10:20"},
		{WebGitea, "/commit/" + sha, "/src/commit/" + sha + "/docs/%E8%AF%B4%E6%98%8E%201.md", "/src/branch/feature/x/src", "#L7", "#L10-L20"},
		{WebGeneric, "/commit/" + sha, "/blob/" + sha + "/docs/%E8%AF%B4%E6%98%8E%201.md", "/tree/feature/x/src", "#L7", "#L10-L20"},
	} {
		w := web(tc.kind)
		base := w.Base
		for _, c := range []struct{ got, want string }{
			{w.Commit(sha), base + tc.commit},
			{w.File(sha, "docs/说明 1.md", false), base + tc.file},
			{w.File("feature/x", "src", true), base + tc.dir},
			{w.Lines(sha, "docs/说明 1.md", 7, 7), base + tc.file + tc.one},
			{w.Lines(sha, "docs/说明 1.md", 10, 20), base + tc.file + tc.lines},
		} {
			if c.got != c.want {
				t.Errorf("%s：%s，应为 %s", tc.kind, c.got, c.want)
			}
		}
	}
	if w := ParseWeb("git@git.example.com:o/r.git").With(WebGitLab); w.Kind != WebGitLab || w.Commit("abc") != "https://git.example.com/o/r/-/commit/abc" {
		t.Errorf("With：%+v", w)
	}
	if w := (Web{}).With(WebGitLab); w.OK() {
		t.Errorf("没有网页时 With 不该变出来：%+v", w)
	}
}
