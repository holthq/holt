package git

import (
	"net/url"
	"strconv"
	"strings"
)

// WebKind 是托管网页的风格：提交、文件、行的地址怎么拼。
type WebKind string

const (
	WebGitHub    WebKind = "github"
	WebGitLab    WebKind = "gitlab"
	WebBitbucket WebKind = "bitbucket"
	WebGitea     WebKind = "gitea"   // 含 Forgejo、codeberg.org
	WebGeneric   WebKind = "generic" // 不认识的主机：按 GitHub 的路径布局
)

// WebKinds 是设置里"网页风格"下拉的五种，按这个顺序。
var WebKinds = []WebKind{WebGitHub, WebGitLab, WebBitbucket, WebGitea, WebGeneric}

// Web 是一个仓库的网页（从远程地址推出来）。零值表示没有远程，或远程不是网页能开的地址（本地路径、file://）。
type Web struct {
	Kind WebKind
	Host string // 小写主机名（https 远程带着端口），设置里"网页风格"按它查
	Base string // https://github.com/owner/repo
}

// OK 是有网页可开。
func (w Web) OK() bool { return w.Kind != "" }

// Site 是菜单里"在 … 打开"的名字：GitHub / GitLab / Bitbucket / Gitea（Codeberg、Forgejo 照写），通用的是主机名。
func (w Web) Site() string {
	switch w.Kind {
	case WebGitHub:
		return "GitHub"
	case WebGitLab:
		return "GitLab"
	case WebBitbucket:
		return "Bitbucket"
	case WebGitea:
		switch {
		case w.Host == "codeberg.org":
			return "Codeberg"
		case strings.Contains(w.Host, "forgejo"):
			return "Forgejo"
		}
		return "Gitea"
	}
	return w.Host
}

// With 换成 k 的风格（设置里给这台主机指定的）；k 空或没有网页时不变。
func (w Web) With(k WebKind) Web {
	if w.OK() && k != "" {
		w.Kind = k
	}
	return w
}

// KindOf 按主机名认风格：github.com、主机名里带 gitlab、bitbucket.org、主机名里带 gitea / forgejo / codeberg，别的都算通用。
func KindOf(host string) WebKind {
	host = strings.ToLower(host)
	switch {
	case host == "github.com":
		return WebGitHub
	case strings.Contains(host, "gitlab"):
		return WebGitLab
	case host == "bitbucket.org":
		return WebBitbucket
	case strings.Contains(host, "gitea") || strings.Contains(host, "forgejo") || strings.Contains(host, "codeberg"):
		return WebGitea
	}
	return WebGeneric
}

// ReadWeb 读 origin（没有就第一个远程）的地址，推出它的网页（git config / git remote，只读）。
func ReadWeb(root string) Web {
	out, err := Run(root, "config", "--get", "remote.origin.url")
	if err != nil {
		names, err := Run(root, "remote")
		if err != nil {
			return Web{}
		}
		first, _, _ := strings.Cut(strings.TrimSpace(string(names)), "\n")
		if first == "" {
			return Web{}
		}
		if out, err = Run(root, "config", "--get", "remote."+first+".url"); err != nil {
			return Web{}
		}
	}
	return ParseWeb(strings.TrimSpace(string(out)))
}

// ParseWeb 从远程地址（https://、ssh://、git@host:owner/repo 的 scp 写法）推出网页，风格按主机名认（KindOf）。
// ssh / scp 写法的端口和用户名丢掉（网页在 https 的默认端口上）；https 的端口留着、用户名密码丢掉；
// http 远程的网页也是 http。GitLab 子组 owner/group/repo 整段保留。
func ParseWeb(remote string) Web {
	var host, path string
	scheme := "https"
	u, err := url.Parse(remote)
	if err == nil && u.Scheme != "" && u.Host == "" {
		return Web{} // file:///…
	}
	if err == nil && u.Host != "" {
		switch u.Scheme {
		case "https", "http":
			host, scheme = u.Host, u.Scheme
		case "ssh", "git", "git+ssh":
			host = u.Hostname()
		default:
			return Web{}
		}
		path = u.Path
	} else if at, rest, ok := strings.Cut(remote, ":"); ok && !strings.Contains(at, "/") && len(at) > 1 {
		// scp 写法：[user@]host:owner/repo.git（单个字母的是 Windows 盘符，不算）
		_, host, _ = strings.Cut(at, "@")
		if host == "" {
			host = at
		}
		path = rest
	} else {
		return Web{}
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || !strings.Contains(path, "/") {
		return Web{}
	}
	host = strings.ToLower(host)
	switch host {
	case "www.github.com", "ssh.github.com":
		host = "github.com"
	case "altssh.bitbucket.org":
		host = "bitbucket.org"
	}
	if strings.Contains(host, "gitlab") {
		host = strings.TrimPrefix(host, "ssh.")
	}
	return Web{Kind: KindOf(hostOnly(host)), Host: host, Base: scheme + "://" + host + "/" + path}
}

// hostOnly 是去掉端口的主机名。
func hostOnly(host string) string {
	if h, _, ok := strings.Cut(host, ":"); ok {
		return h
	}
	return host
}

// Commit 是一个提交的网页。
func (w Web) Commit(hash string) string {
	switch w.Kind {
	case WebGitLab:
		return w.Base + "/-/commit/" + hash
	case WebBitbucket:
		return w.Base + "/commits/" + hash
	}
	return w.Base + "/commit/" + hash
}

// File 是 ref（提交哈希或分支名，HEAD 是默认分支）里一个文件（dir 时是目录）的网页。
func (w Web) File(ref, rel string, dir bool) string {
	ref, rel = escapePath(ref), escapePath(rel)
	switch w.Kind {
	case WebGitLab:
		if dir {
			return w.Base + "/-/tree/" + ref + "/" + rel
		}
		return w.Base + "/-/blob/" + ref + "/" + rel
	case WebBitbucket:
		return w.Base + "/src/" + ref + "/" + rel
	case WebGitea:
		if isHashPrefix(ref) && len(ref) >= 7 {
			return w.Base + "/src/commit/" + ref + "/" + rel
		}
		return w.Base + "/src/branch/" + ref + "/" + rel
	}
	if dir {
		return w.Base + "/tree/" + ref + "/" + rel
	}
	return w.Base + "/blob/" + ref + "/" + rel
}

// Lines 是 ref 里一个文件第 from–to 行（从 1 数，含两头）的网页。
func (w Web) Lines(ref, rel string, from, to int) string {
	f, t := strconv.Itoa(from), strconv.Itoa(to)
	anchor := "#L" + f
	switch {
	case w.Kind == WebBitbucket && to > from:
		anchor = "#lines-" + f + ":" + t
	case w.Kind == WebBitbucket:
		anchor = "#lines-" + f
	case w.Kind == WebGitLab && to > from:
		anchor += "-" + t
	case to > from:
		anchor += "-L" + t
	}
	return w.File(ref, rel, false) + anchor
}

// escapePath 按段转义路径（分支名里的 / 也当分段，GitHub / GitLab 都认）。
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
