package git

import (
	"regexp"
	"strings"

	"github.com/lemoeade/grove/internal/i18n"
)

// explains 是常见失败的认法和说明（gaps 2.2 顺带）：按顺序认，第一个对上的算。git 和 ssh 在 LC_ALL=C 下是英文的，
// 只认英文原文，认不出就照旧把原文整段给人看。锁文件那句和取消后的提醒（app.locksText）是同一个意思。
var explains = []struct {
	re   *regexp.Regexp
	text string
}{
	{regexp.MustCompile(`fetch first|non-fast-forward|! \[rejected\]`), "远程有本地没有的提交：先拉取（或变基到远程）再推送。"},
	{regexp.MustCompile(`Authentication failed for|could not read Username|could not read Password|Invalid username or (?:token|password)`),
		"凭据没给或不对；HTTPS 远程要的是访问令牌，不是网站密码。"},
	{regexp.MustCompile(`Permission denied \(publickey`), "远程不认这把 SSH 密钥：看看 ssh-agent 里有没有它、公钥加没加到账号上。"},
	{regexp.MustCompile(`Host key verification failed`), "远程主机的指纹没确认：下次提示时选“继续连接”，或者在终端里 ssh 一次。"},
	{regexp.MustCompile(`Repository not found|does not appear to be a git repository|repository '[^']*' not found`), "远程仓库不存在，或者没有权限看它。"},
	{regexp.MustCompile(`Could not resolve host|Could not read from remote repository|unable to access|Connection refused|Connection timed out|Operation timed out`),
		"连不上远程：看看网络和地址。"},
	{regexp.MustCompile(`already exists and is not an empty directory`), "目标目录已经存在，而且不是空的。"},
	{regexp.MustCompile(`index\.lock|Unable to create '[^']*\.lock'`),
		"仓库里有锁文件：有另一个 git 在跑，或者上次中断时留下的。如果确定没有别的 git 在跑，手动删掉它再继续；Grove 不会替你删。"},
	{regexp.MustCompile(`Your local changes to the following files would be overwritten`), "工作区里的改动会被覆盖：先提交或贮藏。"},
	{regexp.MustCompile(`CONFLICT \(`), "有冲突：在 Git 栏里解决之后继续。"},
}

// Explain 从 git / ssh 的输出里认出常见的失败，返回一句说明（当前语言）；认不出返回空。
func Explain(out string) string {
	if strings.TrimSpace(out) == "" {
		return ""
	}
	for _, e := range explains {
		if e.re.MatchString(out) {
			return i18n.T(e.text)
		}
	}
	return ""
}
