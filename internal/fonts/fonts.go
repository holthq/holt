// Package fonts 挑终端、编辑器和 diff 用的等宽字体，必要时注册自带的 Nerd Font。
// 它是叶子包：app（终端字体）和 ui 都用它。
package fonts

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Bundled 是 resources/fonts 里自带的等宽字体（带 Nerd Font 图标）。
// 它要占应用自己约 20 MB 内存，所以系统里装了 Nerd Font 时优先用系统的。
const Bundled = "JetBrainsMono Nerd Font"

// systemNerdFonts 是常见的 Nerd Font 家族，按偏好排序；装了哪个就用哪个。
var systemNerdFonts = []string{
	"JetBrainsMono Nerd Font Mono",
	"JetBrainsMono Nerd Font",
	"MesloLGS Nerd Font Mono",
	"MesloLGS Nerd Font",
	"MesloLGM Nerd Font Mono",
	"Hack Nerd Font Mono",
	"FiraCode Nerd Font Mono",
}

var (
	loadOnce sync.Once
	loaded   bool
	monoOnce sync.Once
	mono     string
)

// Load 注册 resources/fonts 里的字体：打包后在 Contents/Resources/fonts，
// go test 和 go run 时从当前目录往上找 resources/fonts。
func Load() bool {
	loadOnce.Do(func() {
		var dirs []string
		if res, err := mygo.App.Path(mygo.PathResources); err == nil {
			dirs = append(dirs, filepath.Join(res, "fonts"))
		}
		if wd, err := os.Getwd(); err == nil {
			for d, i := wd, 0; i < 6; d, i = filepath.Dir(d), i+1 {
				dirs = append(dirs, filepath.Join(d, "resources", "fonts"))
			}
		}
		for _, dir := range dirs {
			files, _ := filepath.Glob(filepath.Join(dir, "*.ttf"))
			for _, f := range files {
				data, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				if ui.RegisterFont(data, Bundled) == nil {
					loaded = true
				}
			}
			if loaded {
				return
			}
		}
	})
	return loaded
}

// Mono 是终端和 diff 用的等宽字体，按这个顺序挑：
//  1. 环境变量 WORKBENCH_FONT 指定的家族（"monospace" 为系统默认等宽字体，
//     "JetBrainsMono Nerd Font" 强制用自带字体）；
//  2. 系统里已经装好的 Nerd Font，不占应用自己的内存；
//  3. 自带的 JetBrainsMono Nerd Font；
//  4. 系统默认等宽字体。
func Mono() string {
	monoOnce.Do(func() {
		mono = Pick(os.Getenv("WORKBENCH_FONT"), ui.FontFamilies())
	})
	return mono
}

// Pick 是 Mono 的挑法，families 是系统里的字体家族。
func Pick(forced string, families []ui.FontFamily) string {
	if forced != "" {
		if forced == Bundled {
			Load()
		}
		return forced
	}
	// 只看系统的字体；已注册的自带字体不算，它恰恰是要避免加载的那个。
	have := make(map[string]bool, len(families))
	for _, f := range families {
		if !f.Registered {
			have[strings.ToLower(f.Name)] = true
		}
	}
	for _, name := range systemNerdFonts {
		if have[strings.ToLower(name)] {
			return name
		}
	}
	for _, f := range families {
		if !f.Registered && f.Monospaced && strings.Contains(f.Name, "Nerd Font") {
			return f.Name
		}
	}
	if Load() {
		return Bundled
	}
	return "monospace"
}
