// Package version 是版本信息：应用自己的版本号、编进来的 mygo 和 Go 的版本。
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// App 是应用的版本号。打包的应用由 main 设成 mygo.App.Version()：mygo build 把 mygo.json 的
// version 用 -ldflags -X 链接进来，macOS 上也写进 Info.plist。go run 和测试里是 "dev"。
var App = "dev"

// MygoModule 是 mygo 的模块路径。
const MygoModule = "github.com/egoist/mygo"

// MygoPinned 是 go.mod 里锁的 mygo 版本，读不到构建信息（测试的二进制没有依赖列表）时用它；
// TestVersions 保证它和 go.mod 一致。
const MygoPinned = "v0.7.5-0.20261106080026-6121009b900d"

// Mygo 是编进来的 mygo 的版本。
func Mygo() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return MygoPinned
	}
	for _, d := range bi.Deps {
		if d.Path == MygoModule {
			if d.Replace != nil {
				d = d.Replace
			}
			return d.Version
		}
	}
	return MygoPinned
}

// Go 是编译用的 Go 版本。
func Go() string { return runtime.Version() }

// Platform 是系统和架构，如 darwin/arm64。
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// String 是一行的版本信息，写在日志开头。
func String() string {
	return fmt.Sprintf("Grove %s (mygo %s, %s, %s)", App, Mygo(), Go(), Platform())
}
