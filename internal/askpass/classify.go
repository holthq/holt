package askpass

import (
	"net/url"
	"regexp"
	"strings"
)

// Kind 是提示的种类，一种一个框。
type Kind int

const (
	Other      Kind = iota // 认不出的：标题是提示原文，一栏普通文本
	Username               // HTTPS 用户名（git 接着会问密码）
	Password               // HTTPS 密码 / 令牌
	Passphrase             // SSH 密钥口令
	HostKey                // SSH 第一次连这台主机：确认指纹（回 yes）
)

// Prompt 是认出来的提示。
type Prompt struct {
	Kind Kind
	Text string // 原文（去掉首尾空白）
	Host string // Username / Password / HostKey：如 github.com（带端口的照留）
	User string // Password：提示里带的用户名
	Key  string // Passphrase：钥匙的路径
}

var (
	reUsername = regexp.MustCompile(`^Username for '([^']*)'`)
	rePassword = regexp.MustCompile(`^Password for '([^']*)'`)
	reKey      = regexp.MustCompile(`^Enter passphrase for (?:key )?'([^']+)'`)
	reHost     = regexp.MustCompile(`authenticity of host '([^' ]+)`)
)

// Classify 认 git（credential 的 Username for / Password for）和 OpenSSH（Enter passphrase for key、
// 第一次连主机的 yes/no）的提示；LC_ALL=C，提示是英文的。
func Classify(text string) Prompt {
	p := Prompt{Text: strings.TrimSpace(text)}
	switch {
	case strings.Contains(p.Text, "(yes/no"):
		p.Kind = HostKey
		if m := reHost.FindStringSubmatch(p.Text); m != nil {
			p.Host = m[1]
		}
	case reUsername.MatchString(p.Text):
		p.Kind = Username
		p.Host, _ = urlHost(reUsername.FindStringSubmatch(p.Text)[1])
	case rePassword.MatchString(p.Text):
		p.Kind = Password
		p.Host, p.User = urlHost(rePassword.FindStringSubmatch(p.Text)[1])
	case reKey.MatchString(p.Text):
		p.Kind = Passphrase
		p.Key = reKey.FindStringSubmatch(p.Text)[1]
	}
	return p
}

// urlHost 是 git 提示里那个地址的主机和用户名（https://user@github.com → github.com、user）；解析不了就原样当主机。
func urlHost(s string) (host, user string) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s, ""
	}
	if u.User != nil {
		user = u.User.Username()
	}
	return u.Host, user
}

// Title 是认不出的提示当标题用的样子：去掉末尾的冒号和空白。
func (p Prompt) Title() string {
	return strings.TrimRight(p.Text, ": \t")
}
