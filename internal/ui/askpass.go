package ui

import (
	"regexp"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/askpass"
	"github.com/lemoeade/grove/internal/i18n"
)

// credDlgKey 给每个凭据提示一份自己的输入框状态（换下一个提示时框是新的、焦点重新落到第一栏）。
type credDlgKey struct{ q *app.CredReq }

// credDialog 是 git / ssh 要凭据时的框（a.Cred[0]，app/askpass.go）：HTTPS 用户名 + 密码 / 令牌、只要密码、
// SSH 密钥口令、确认主机指纹、认不出的提示各一种脸。都是提问的图标，右上角是发起命令的仓库；Esc / 取消 = 回不 ok。
func credDialog(a *app.App, c *wui.Context) {
	if len(a.Cred) == 0 {
		return
	}
	q := a.Cred[0]
	c = c.Key(credDlgKey{q})
	t := c.Theme()
	ready := app.CredReady(q)
	submit := func() {
		if app.CredReady(q) {
			a.AnswerCred(true)
		}
	}
	token := i18n.T("GitHub / Gitee 这类 HTTPS 远程要的是个人访问令牌，不是网站密码。")
	o := dialogOpts{Kind: dlgQuestion, Repo: q.Repo, Hint: i18n.T("继续")}
	var body func()
	foot := "" // 底下一行小字
	switch q.Kind {
	case askpass.Username:
		o.Title, o.Note, o.Hint = i18n.Tf("登录 %s", q.Host), token, i18n.T("登录")
		foot = i18n.T("要让 git 记住：在终端里 git config --global credential.helper osxkeychain")
		body = func() {
			dlgField(c, i18n.T("用户名"), "", func() {
				if dlgInput(c, wui.TextInput(c, &q.User).Label(i18n.T("用户名")).AutoFocus()).Submitted() {
					submit()
				}
			})
			dlgField(c, i18n.T("密码 / 令牌"), "", func() {
				if dlgInput(c, wui.TextInput(c, &q.Pass).Label(i18n.T("密码 / 令牌")).Password()).Submitted() {
					submit()
				}
			})
		}
	case askpass.Password:
		o.Title, o.Note, o.Hint = i18n.Tf("登录 %s", q.Host), token, i18n.T("登录")
		if q.User != "" {
			o.Note = i18n.Tf("用户 %s。", q.User) + token
		}
		foot = i18n.T("要让 git 记住：在终端里 git config --global credential.helper osxkeychain")
		body = func() {
			dlgField(c, i18n.T("密码 / 令牌"), "", func() {
				if dlgInput(c, wui.TextInput(c, &q.Pass).Label(i18n.T("密码 / 令牌")).Password().AutoFocus()).Submitted() {
					submit()
				}
			})
		}
	case askpass.Passphrase:
		o.Title, o.Note = i18n.T("密钥口令"), i18n.Tf("解开 %s 要口令。", q.Key)
		foot = i18n.Tf("不想每次输：在终端里 ssh-add --apple-use-keychain %s", q.Key)
		body = func() {
			dlgField(c, i18n.T("口令"), "", func() {
				if dlgInput(c, wui.TextInput(c, &q.Pass).Label(i18n.T("口令")).Password().AutoFocus()).Submitted() {
					submit()
				}
			})
		}
	case askpass.HostKey:
		o.Title, o.Width, o.Hint, o.Enter = i18n.T("确认远程主机指纹"), 460, i18n.T("继续连接"), submit
		o.Note = i18n.Tf("第一次连 %s：核对下面的指纹和服务方公布的一样再继续，之后 ssh 记在 ~/.ssh/known_hosts 里。", q.Host)
		body = func() {
			wui.Column(c).Padding(8, 10).Radius(6).Background(t.Text.Alpha(0.05)).Selectable().MinWidth(0).Children(func() {
				wui.Text(c, q.Prompt.Text).Font(mono()).FontSize(11).LineHeight(1.45).TextColor(t.TextMuted).MinWidth(0)
			})
		}
	default:
		o.Title = q.Title()
		body = func() {
			dlgField(c, i18n.T("回答"), "", func() {
				in := wui.TextInput(c, &q.Answer).Label(q.Title()).AutoFocus()
				if reSecret.MatchString(q.Prompt.Text) {
					in.Password()
				}
				if dlgInput(c, in).Submitted() {
					submit()
				}
			})
		}
	}
	open := true
	dialogFrame(c, &open, o, func() {
		body()
		if foot != "" {
			wui.Text(c, foot).FontSize(11).LineHeight(16.0 / 11).TextColor(t.TextMuted).MinWidth(0).Selectable()
		}
	}, func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			open = false
		}
		if dlgButton(c, o.Hint, btnPrimary, !ready, q.Kind == askpass.HostKey) {
			submit()
		}
	})
	if !open && len(a.Cred) > 0 && a.Cred[0] == q {
		a.AnswerCred(false)
	}
}

// reSecret 是认不出的提示像在要密码（如 ssh 的 "git@host's password:"、"Enter PIN"）：回答框遮成点。
var reSecret = regexp.MustCompile(`(?i)\b(password|passphrase|pin|token)\b`)
