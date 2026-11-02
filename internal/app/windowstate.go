package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// WindowStateFile 是窗口位置和大小的文件，和 state.json 放在一个目录里。
const WindowStateFile = "window-state.json"

// WindowStates 是窗口位置和大小（mygo 的 WindowOptions.StateKey）存在哪个目录：交给 mygo.App.SetWindowStateStore，
// 要在 Run 之前、第一个窗口之前。文件名、格式都和 mygo 默认的那份一样，平时这个目录就是 PathUserData，还是原来那个文件；
// 设了 WORKBENCH_STATE 的调试运行（量内存、手测）存到那个目录里，不动用户的窗口位置。
type WindowStates string

func (d WindowStates) path() string { return filepath.Join(string(d), WindowStateFile) }

// LoadWindowStates 读 window-state.json：没有是 nil（窗口用自己的默认位置），坏了记一笔、也当没有（下次存时整个重写）。
func (d WindowStates) LoadWindowStates() (map[string]mygo.WindowState, error) {
	b, err := os.ReadFile(d.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var states map[string]mygo.WindowState
	if err == nil {
		err = json.Unmarshal(b, &states)
	}
	if err != nil {
		log.Printf("读 %s: %v", WindowStateFile, err)
		return nil, err
	}
	return states, nil
}

// SaveWindowStates 整个重写（先写 .tmp 再换名）。写不成记一笔，mygo 下次（关窗口、退出）再写，不影响别的。
func (d WindowStates) SaveWindowStates(states map[string]mygo.WindowState) error {
	b, err := json.MarshalIndent(states, "", "  ")
	if err == nil {
		err = os.MkdirAll(string(d), 0o755)
	}
	if err == nil {
		err = os.WriteFile(d.path()+".tmp", b, 0o644)
	}
	if err == nil {
		err = os.Rename(d.path()+".tmp", d.path())
	}
	if err != nil {
		log.Printf("存 %s: %v", WindowStateFile, err)
	}
	return err
}
