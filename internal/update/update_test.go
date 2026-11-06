package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve 起一个 httptest.Server 当作 github.com，测试期间把 siteBase 指过去，结束时换回来。
func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() {
		siteBase = old
		srv.Close()
	})
	return srv
}

// 正常 release：打开 /octo/holt/releases/latest，跟着跳到 /releases/tag/<标签>，从最后落在的地址取版本；
// 不走 API 的 /repos/… 路径。
func TestLatest(t *testing.T) {
	var srv *httptest.Server
	srv = serve(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			t.Errorf("用了 GitHub API：%s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/octo/holt/releases/latest":
			if r.Method != http.MethodGet {
				t.Errorf("方法 = %s，要 GET", r.Method)
			}
			if ua := r.Header.Get("User-Agent"); ua != "Holt" {
				t.Errorf("User-Agent = %q，要 Holt", ua)
			}
			http.Redirect(w, r, "/octo/holt/releases/tag/v0.1.3", http.StatusFound)
		case "/octo/holt/releases/tag/v0.1.3":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><body>Holt 0.1.3</body></html>`)
		default:
			http.NotFound(w, r)
		}
	})

	rel, err := Latest(context.Background(), "octo", "holt")
	if err != nil {
		t.Fatal(err)
	}
	want := Release{Version: "0.1.3", URL: srv.URL + "/octo/holt/releases/tag/v0.1.3"}
	if rel != want {
		t.Fatalf("Latest() = %+v，要 %+v", rel, want)
	}
}

// 出错的情况：404 / 没有 release（送到 /releases）/ 别的状态码。
func TestLatestErrors(t *testing.T) {
	for _, c := range []struct {
		name     string
		status   int
		redirect string // 不是空时跳到这里
		sentinel error  // 要 errors.Is 上的哨兵；nil 表示只要 err != nil
		want     []string
	}{
		{name: "404", status: http.StatusNotFound, sentinel: ErrNoRelease},
		{name: "还没有 release，送到 /releases", status: http.StatusFound, redirect: "/octo/holt/releases", sentinel: ErrNoRelease},
		{name: "500", status: http.StatusInternalServerError, want: []string{"500", "Internal Server Error"}},
		{name: "502", status: http.StatusBadGateway, want: []string{"502", "Bad Gateway"}},
		{name: "403", status: http.StatusForbidden, want: []string{"403", "Forbidden"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			serve(t, func(w http.ResponseWriter, r *http.Request) {
				if c.redirect != "" && r.URL.Path == "/octo/holt/releases/latest" {
					http.Redirect(w, r, c.redirect, http.StatusFound)
					return
				}
				if c.redirect != "" { // 跳过去以后的那一页
					fmt.Fprint(w, "<html>releases</html>")
					return
				}
				w.WriteHeader(c.status)
				fmt.Fprint(w, "<html>oops</html>")
			})

			rel, err := Latest(context.Background(), "octo", "holt")
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

// 200 但没有跳到 tag：认不出版本，算“还没有 release”。
func TestLatestNoTag(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>latest</html>")
	})
	if _, err := Latest(context.Background(), "octo", "holt"); err != ErrNoRelease {
		t.Fatalf("错误 = %v，要 ErrNoRelease", err)
	}
}

// owner / repo 没写全时不去请求 GitHub。
func TestLatestEmptyRepo(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("owner 或 repo 是空的，不该发请求")
	})
	if _, err := Latest(context.Background(), "", "holt"); err == nil {
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
	if _, err := Latest(ctx, "octo", "holt"); !errors.Is(err, context.Canceled) {
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
