package app

import (
	"context"
	"errors"
	"time"

	"github.com/holthq/holt/internal/i18n"
	upd "github.com/holthq/holt/internal/update"
	"github.com/holthq/holt/internal/version"
)

// updateOwner、updateRepo 是"检查更新"看的 GitHub 仓库。
const (
	updateOwner = "holthq"
	updateRepo  = "holt"
)

// latestRelease 查最新 release；测试换成桩，不联网。
var latestRelease = upd.Latest

// CheckUpdates 是应用菜单的"检查更新…"：查 GitHub 上最新的 release，和编进来的版本比。
// 只在用户点了菜单时查：启动、刷新、开标签都不查。
func (a *App) CheckUpdates() {
	cur := version.App
	if cur == "dev" {
		a.Toast = i18n.T("开发版不检查更新")
		return
	}
	a.Toast = i18n.T("正在检查更新…")
	up := a.Update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rel, err := latestRelease(ctx, updateOwner, updateRepo)
		up(func() { a.checkedUpdates(rel, err, cur) })
	}()
}

// checkedUpdates 是查出结果以后的回话（主线程上）：有新版本问要不要打开发布页，查不了弹错误，
// 没有 release、已经是最新就提示一句。
func (a *App) checkedUpdates(rel upd.Release, err error, cur string) {
	switch {
	case err != nil && !errors.Is(err, upd.ErrNoRelease):
		a.Toast = ""
		a.Fail(i18n.T("检查更新失败"), err)
	case err == nil && upd.Newer(rel.Version, cur):
		a.Toast = ""
		a.Ask(i18n.Tf("发现新版本 %s", rel.Version), i18n.Tf("现在是 %s。要打开发布页吗？", cur), i18n.T("打开发布页"), func() {
			if err := a.OpenURL(rel.URL); err != nil {
				a.Fail(i18n.T("打不开网页"), err)
			}
		})
	default:
		a.Toast = i18n.Tf("已是最新版本（%s）", cur)
	}
}
