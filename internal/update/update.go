// Package update 查 GitHub 仓库的最新 release，和当前版本比。只在用户点了“检查更新”时用。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Release 是一次发布。
type Release struct {
	Version string // 标签去掉前缀 v，如 "0.1.3"
	URL     string // 发布页 html_url
	Notes   string // 发布说明（正文）
}

// ErrNoRelease 是这个仓库还没有发布过 release（GitHub 返回 404）。
var ErrNoRelease = errors.New("这个仓库还没有 release")

// apiBase 是 GitHub REST 的根。测试里换成 httptest.Server 的地址。
var apiBase = "https://api.github.com"

// client 只给 Latest 用：调用方的 ctx 先到时请求立刻取消，这是没人取消时的兜底。
var client = &http.Client{Timeout: 10 * time.Second}

// maxBody 是最多读多少响应：release 的说明再长也到不了 1 MB。
const maxBody = 1 << 20

// Latest 查 owner/repo 的最新 release（GitHub REST：GET https://api.github.com/repos/{owner}/{repo}/releases/latest）。
// 请求跟着 ctx 走，超时由调用方控制（比如 20 秒）；client 另有 10 秒兜底。失败时错误里带上状态码或原因。
// 没有 release（404）返回 ErrNoRelease。
func Latest(ctx context.Context, owner, repo string) (Release, error) {
	if owner == "" || repo == "" {
		return Release{}, errors.New("检查更新：owner 或 repo 是空的")
	}
	u := strings.TrimRight(apiBase, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Release{}, fmt.Errorf("检查更新：%w", err)
	}
	req.Header.Set("User-Agent", "Holt")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("检查更新：%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Release{}, fmt.Errorf("检查更新：读 GitHub 的响应：%w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, ErrNoRelease
	case resp.StatusCode != http.StatusOK:
		return Release{}, githubError(resp, body)
	case len(body) > maxBody:
		return Release{}, fmt.Errorf("检查更新：GitHub 的响应超过 %d KB", maxBody>>10)
	}

	// 只取用得到的三个字段：别的一律不读。
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Release{}, fmt.Errorf("检查更新：GitHub 的响应不是 JSON：%w", err)
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return Release{}, errors.New("检查更新：GitHub 的 release 没有 tag_name")
	}
	return Release{
		Version: strings.TrimPrefix(tag, "v"),
		URL:     payload.HTMLURL,
		Notes:   payload.Body,
	}, nil
}

// httpError 是 GitHub 返回的非 200、非 404 响应：错误里带状态码和 GitHub 的一句说明。
type httpError struct {
	Code    int    // HTTP 状态码
	Message string // GitHub 的 message，没有时是状态码的英文
}

func (e *httpError) Error() string {
	return fmt.Sprintf("GitHub 返回 %d：%s", e.Code, e.Message)
}

// githubError 把错误响应拼成 httpError：message 取 GitHub 的 JSON，不是 JSON 就用状态码的英文；
// 限额用完时（X-RateLimit-Remaining: 0）再带上一个重置时间。
func githubError(resp *http.Response, body []byte) error {
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &payload) // 读不出来也不要紧，下面还有状态码
	msg := strings.Join(strings.Fields(payload.Message), " ")
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	if msg == "" {
		msg = "没有说明"
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		msg += "（GitHub 的限额已用完"
		if sec, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			msg += "，" + time.Unix(sec, 0).Local().Format("15:04") + " 重置"
		}
		msg += "）"
	}
	return &httpError{Code: resp.StatusCode, Message: msg}
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
