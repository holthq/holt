// Package update 查 GitHub 仓库的最新 release，和当前版本比。只在用户点了“检查更新”时用。
package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Release 是一次发布。
type Release struct {
	Version string // 标签去掉前缀 v，如 "0.1.3"
	URL     string // 发布页（最后落在的 /releases/tag/… 地址）
}

// ErrNoRelease 是这个仓库还没有发布过 release。
var ErrNoRelease = errors.New("这个仓库还没有 release")

// siteBase 是 GitHub 站点（测试里换成 httptest.Server 的地址）。
var siteBase = "https://github.com"

// client 只给 Latest 用：调用方的 ctx 先到时请求立刻取消，这是没人取消时的兜底。
var client = &http.Client{Timeout: 10 * time.Second}

// Latest 查 owner/repo 的最新 release：打开 https://github.com/{owner}/{repo}/releases/latest，
// GitHub 会 302 到 /releases/tag/<标签>，从最后落到的地址里取标签。不走 API，不占限额，也不读那一页 HTML。
// 请求跟着 ctx 走；没有 release 时（GitHub 把人送到 /releases，或者 404）返回 ErrNoRelease。
func Latest(ctx context.Context, owner, repo string) (Release, error) {
	if owner == "" || repo == "" {
		return Release{}, errors.New("检查更新：owner 或 repo 是空的")
	}
	page := strings.TrimRight(siteBase, "/") + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page, nil)
	if err != nil {
		return Release{}, fmt.Errorf("检查更新：%w", err)
	}
	req.Header.Set("User-Agent", "Holt")
	req.Header.Set("Accept", "text/html")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("检查更新：%w", err)
	}
	defer resp.Body.Close()
	// 只要最后落在哪，不读那一页（它可能有几十 KB）：关掉就行。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))

	switch resp.StatusCode {
	case http.StatusNotFound:
		return Release{}, ErrNoRelease
	case http.StatusOK:
	default:
		return Release{}, fmt.Errorf("检查更新：GitHub 返回 %d：%s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	tag, ok := tagOf(resp.Request.URL)
	if !ok {
		return Release{}, ErrNoRelease // 送到了 /releases：还没有 release
	}
	return Release{Version: strings.TrimPrefix(tag, "v"), URL: resp.Request.URL.String()}, nil
}

// tagOf 从 /releases/tag/<标签> 里取标签（去掉 path 转义）。
func tagOf(u *url.URL) (string, bool) {
	const marker = "/releases/tag/"
	i := strings.Index(u.Path, marker)
	if i < 0 {
		return "", false
	}
	tag, err := url.PathUnescape(u.Path[i+len(marker):])
	if err != nil || tag == "" {
		return "", false
	}
	return tag, true
}

// Newer 报告 latest 是否比 current 新。按数字逐段比（"0.2.0" > "0.1.9"），认 v 前缀，
// 段数不同按缺的补 0（"0.1" == "0.1.0"）；带 - 的预发布后缀比同号的正式版旧，不是数字的部分不比大小。
// 版本号里有认不出的段（比如开发版的 "dev"）时也比不了大小，返回 false。
func Newer(latest, current string) bool {
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	for i := 0; i < max(len(l.num), len(c.num)); i++ {
		a, b := "0", "0" // 缺的段补 0
		if i < len(l.num) {
			a = l.num[i]
		}
		if i < len(c.num) {
			b = c.num[i]
		}
		if cmp := compareNum(a, b); cmp != 0 {
			return cmp > 0
		}
	}
	// 数字部分一样：正式版比同号的预发布版新；两个都是预发布（或都是正式版）时不分先后。
	return !l.pre && c.pre
}

// parsedVersion 是拆开的版本号：num 是点分开的数字段，pre 表示有没有 - 后面的预发布后缀。
type parsedVersion struct {
	num []string // 每段去掉前导 0 的十进制写法（"0" 就是 "0"）
	pre bool     // 有 - 后面的预发布后缀
}

// parseVersion 认 v 前缀、点分开的数字段和 - 后面的预发布后缀；+ 后面的构建元数据不参与比较。
// 有空段或者不是数字的段（"dev"、"0.1.x"）时认不出，ok 是 false。
func parseVersion(s string) (v parsedVersion, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i] // 1.0.0+build.5 和 1.0.0 一样
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = i+1 < len(s) // 后面空着（"0.1.0-"）当没有
		s = s[:i]
	}
	if s == "" {
		return parsedVersion{}, false
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" || strings.IndexFunc(seg, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return parsedVersion{}, false
		}
		v.num = append(v.num, trimZeros(seg))
	}
	return v, true
}

// trimZeros 去掉十进制数字串的前导 0："007" → "7"，"000" → "0"。
func trimZeros(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

// compareNum 比两个已经去掉前导 0 的十进制数字串：位数多的更大，位数一样时按字符串比。
func compareNum(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}
