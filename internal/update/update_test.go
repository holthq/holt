package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// serve 起一个 httptest.Server 当作 GitHub API，测试期间把 apiBase 指过去，结束时换回来。
func serve(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() {
		apiBase = old
		srv.Close()
	})
}

// 正常 release：请求方法、路径、两个头；tag_name 去掉 v，html_url 和正文原样带回来。
func TestLatest(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("方法 = %s，要 GET", r.Method)
		}
		if r.URL.Path != "/repos/octo/grove/releases/latest" {
			t.Errorf("路径 = %q", r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); ua != "Holt" {
			t.Errorf("User-Agent = %q，要 Holt", ua)
		}
		if accept := r.Header.Get("Accept"); accept != "application/vnd.github+json" {
			t.Errorf("Accept = %q", accept)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"tag_name": "v0.1.3",
			"html_url": "https://github.com/octo/grove/releases/tag/v0.1.3",
			"body": "修了文件树的滚动。\n\n还有别的。",
			"name": "Holt 0.1.3",
			"draft": false
		}`)
	})

	rel, err := Latest(context.Background(), "octo", "grove")
	if err != nil {
		t.Fatal(err)
	}
	want := Release{
		Version: "0.1.3",
		URL:     "https://github.com/octo/grove/releases/tag/v0.1.3",
		Notes:   "修了文件树的滚动。\n\n还有别的。",
	}
	if rel != want {
		t.Fatalf("Latest() = %+v，要 %+v", rel, want)
	}
}

// 404 是 ErrNoRelease；别的状态码、坏 JSON 都是别的错误，错误里要有状态码或 GitHub 的说明。
func TestLatestErrors(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()
	for _, c := range []struct {
		name     string
		status   int
		body     string
		header   map[string]string
		sentinel error    // 要 errors.Is 上的哨兵；nil 表示只要 err != nil
		want     []string // 错误里要有的话
	}{
		{
			name:     "404 没有 release",
			status:   http.StatusNotFound,
			body:     `{"message":"Not Found"}`,
			sentinel: ErrNoRelease,
		},
		{
			name:   "500",
			status: http.StatusInternalServerError,
			body:   `{"message":"Internal Server Error"}`,
			want:   []string{"500", "Internal Server Error"},
		},
		{
			name:   "坏 JSON",
			status: http.StatusOK,
			body:   `{"tag_name":`,
			want:   []string{"JSON"},
		},
		{
			name:   "限流",
			status: http.StatusForbidden,
			body:   `{"message":"API rate limit exceeded for 1.2.3.4."}`,
			header: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(reset, 10),
			},
			want: []string{"403", "rate limit", "重置"},
		},
		{
			name:   "没有 message 的状态码",
			status: http.StatusBadGateway,
			body:   `not json`,
			want:   []string{"502", "Bad Gateway"},
		},
		{
			name:   "状态码连英文都没有",
			status: 599,
			body:   `not json`,
			want:   []string{"599", "没有说明"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			serve(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			})

			rel, err := Latest(context.Background(), "octo", "grove")
			if rel != (Release{}) {
				t.Errorf("出错时还给了 release：%+v", rel)
			}
			if c.sentinel != nil {
				if err != c.sentinel {
					t.Fatalf("错误 = %v，要 %v", err, c.sentinel)
				}
				return
			}
			if err == nil {
				t.Fatal("没有出错")
			}
			for _, s := range c.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("错误 %q 里没有 %q", err, s)
				}
			}
		})
	}
}

// 响应体最多读 1 MB：再大的直接报错，不整个读进来。
func TestLatestBodyLimit(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.1.0","body":"`+strings.Repeat("a", maxBody)+`"}`)
	})
	_, err := Latest(context.Background(), "octo", "grove")
	if err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("错误 = %v，要提示响应太大", err)
	}
}

// 200 但没有 tag_name：认不出版本，算错误。
func TestLatestNoTag(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"html_url":"https://github.com/octo/grove/releases/tag/v0.1.0"}`)
	})
	_, err := Latest(context.Background(), "octo", "grove")
	if err == nil || !strings.Contains(err.Error(), "tag_name") {
		t.Fatalf("错误 = %v，要说明没有 tag_name", err)
	}
}

// owner / repo 没写全时不去请求 GitHub。
func TestLatestEmptyRepo(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("owner 或 repo 是空的，不该发请求")
	})
	if _, err := Latest(context.Background(), "", "grove"); err == nil {
		t.Fatal("没有出错")
	}
	if _, err := Latest(context.Background(), "octo", ""); err == nil {
		t.Fatal("没有出错")
	}
}

// ctx 已经取消时立刻返回，不等 client 的 10 秒超时，也不发请求。
func TestLatestContext(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("ctx 已经取消了，还是发了请求")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Latest(ctx, "octo", "grove"); !errors.Is(err, context.Canceled) {
		t.Fatalf("错误 = %v，要 context.Canceled", err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"0.1.3", "0.1.2", true},   // 补丁号
		{"0.2.0", "0.1.9", true},   // 按数字比，不是按字符串
		{"0.10.0", "0.9.9", true},  // 10 > 9，不能按字符串比
		{"0.9.9", "0.10.0", false}, // 位数少的更小
		{"v0.1.3", "0.1.2", true},  // 认 v 前缀
		{"0.1.3", "v0.1.2", true},  // 当前版本带 v 也认
		{"0.1", "0.1.0", false},    // 段数不同，缺的补 0
		{"0.1.0", "0.1", false},
		{"0.1.1", "0.1", true},
		{"0.1.0.1", "0.1", true},
		{"0.1.0.0", "0.1", false},
		{"0.1.0", "0.1.0", false}, // 相等
		{"0.1.0", "0.2.0", false}, // 当前版本更高
		{"0.1.0", "1.0.0", false},
		{"0.1.0-beta", "0.1.0", false}, // 预发布比同号的正式版旧
		{"0.1.0", "0.1.0-beta", true},
		{"0.1.0-rc.1", "0.1.0-beta", false}, // 预发布之间不比大小
		{"0.1.0-beta", "0.1.0-rc.1", false},
		{"0.1.1-beta", "0.1.0", true}, // 数字段大就是新，不看预发布
		{"0.2.0-beta", "0.1.9", true},
		{"0.1.0+build.5", "0.1.0", false}, // + 后面的构建元数据不参与比较
		{"0.1.0", "0.1.0+build.5", false},
		{"dev", "0.1.0", false}, // 认不出的版本不比大小
		{"0.1.0", "dev", false},
		{"0.1.x", "0.1.0", false},
		{"", "0.1.0", false},
		{"0.01.0", "0.1.0", false}, // 前导 0 不算数
		{"0.1.1", "0.1.0.0", true},
		{" 0.1.1 ", "0.1.0", true}, // 两边空白不算数
	} {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v，要 %v", c.latest, c.current, got, c.want)
		}
	}
}
