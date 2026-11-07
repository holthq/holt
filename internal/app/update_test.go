package app

import "testing"

// TestCheckUpdates：检查更新交给 main 接的更新窗口（mygo 的 updater）；没接（测试）就什么也不做。
func TestCheckUpdates(t *testing.T) {
	a := &App{}
	a.CheckUpdates() // 没接：不炸
	opened := 0
	a.OpenUpdates = func() { opened++ }
	a.CheckUpdates()
	if opened != 1 {
		t.Fatalf("更新窗口开了 %d 次", opened)
	}
}
