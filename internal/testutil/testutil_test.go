package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 临时仓库的形状是界面测试的前提：两个提交在 main、feature 多一个，a.txt 改了、u.txt 未跟踪。
func TestTempRepo(t *testing.T) {
	root, ws := TempRepo(t)
	if got := GitOut(t, root, "branch", "--show-current"); got != "main" {
		t.Errorf("当前分支 %q，要 main", got)
	}
	if got := GitOut(t, root, "rev-list", "--count", "main"); got != "2" {
		t.Errorf("main 上 %s 个提交，要 2", got)
	}
	if got := GitOut(t, root, "rev-list", "--count", "feature"); got != "3" {
		t.Errorf("feature 上 %s 个提交，要 3", got)
	}
	// GitOut 去掉了首尾空白，第一行开头的空格（未暂存的 " M"）也在内。
	st := GitOut(t, root, "status", "--porcelain")
	if !strings.Contains(st, "M a.txt") || !strings.Contains(st, "?? u.txt") {
		t.Errorf("status 不对:\n%s", st)
	}
	data, err := os.ReadFile(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.ToSlash(root)) {
		t.Errorf("工作区文件里没有仓库根: %s", data)
	}
}

// 回调可以从别的 goroutine 排进来，Drain 在调用方 goroutine 上按顺序跑完。
func TestQueue(t *testing.T) {
	q := NewQueue()
	var got []int
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.Post(func() { got = append(got, i) })
		}()
	}
	wg.Wait()
	q.Drain()
	if len(got) != 10 {
		t.Fatalf("跑了 %d 个回调，要 10", len(got))
	}
}
