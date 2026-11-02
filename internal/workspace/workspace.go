// Package workspace 解析 .code-workspace，以及 state.json 的 schema 和迁移
// （旧的 panels 表 / 四个布尔的数组 → 侧栏、右栏、底栏三个开关）。
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/lemoeade/grove/internal/i18n"
)

// DefaultPath 是只读测试和截图用的真实工作区（.code-workspace 或目录），取自环境变量 WORKBENCH_TEST_WS；
// 没设时为空，用到它的测试自己跳过。产品代码不用它（没有最近的工作区时开空工作区，见 app.New）。
var DefaultPath = os.Getenv("WORKBENCH_TEST_WS")

// Load 读取 VS Code 的 .code-workspace，返回 folders 的绝对路径。
func Load(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ws struct {
		Folders []struct {
			Path string `json:"path"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &ws); err != nil {
		// .code-workspace 是 JSONC：去掉注释和多余的逗号后再试一次。
		if err2 := json.Unmarshal(StripJSONC(data), &ws); err2 != nil {
			return nil, fmt.Errorf(i18n.T("解析 %s: %w"), path, err)
		}
	}
	base := filepath.Dir(path)
	roots := make([]string, 0, len(ws.Folders))
	for _, f := range ws.Folders {
		p := f.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		roots = append(roots, filepath.Clean(p))
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf(i18n.T("%s 里没有 folders"), path)
	}
	return roots, nil
}

func stripLineComments(data []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// WsSettings 是 .code-workspace 的 settings（或文件夹的 .vscode/settings.json）里我们认的几项。
type WsSettings struct {
	// Exclude 是 files.exclude 里值为 true 的模式（排好序）：false 的、带 when 的对象不认
	Exclude []string
}

// LoadSettings 读 .code-workspace 的 settings。
func LoadSettings(path string) (WsSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WsSettings{}, err
	}
	var ws struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := unmarshalJSONC(data, &ws); err != nil {
		return WsSettings{}, fmt.Errorf(i18n.T("解析 %s: %w"), path, err)
	}
	return settingsOf(ws.Settings), nil
}

// LoadFolderSettings 读文件夹 dir 的 .vscode/settings.json（顶层就是设置）；没有这个文件是零值、没有错。
func LoadFolderSettings(dir string) (WsSettings, error) {
	p := filepath.Join(dir, ".vscode", "settings.json")
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return WsSettings{}, nil
	}
	if err != nil {
		return WsSettings{}, err
	}
	var m map[string]json.RawMessage
	if err := unmarshalJSONC(data, &m); err != nil {
		return WsSettings{}, fmt.Errorf(i18n.T("解析 %s: %w"), p, err)
	}
	return settingsOf(m), nil
}

func settingsOf(m map[string]json.RawMessage) WsSettings {
	var s WsSettings
	var ex map[string]json.RawMessage
	if raw, ok := m["files.exclude"]; ok && json.Unmarshal(raw, &ex) == nil {
		for k, v := range ex {
			if string(bytes.TrimSpace(v)) == "true" {
				s.Exclude = append(s.Exclude, k)
			}
		}
	}
	slices.Sort(s.Exclude)
	return s
}

func unmarshalJSONC(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		if json.Unmarshal(StripJSONC(data), v) != nil {
			return err
		}
	}
	return nil
}

// StripJSONC 把 JSONC（VS Code 的设置和工作区文件）变成 JSON：去掉字符串外面的 // 和 /* */ 注释、} ] 前面多余的逗号。
func StripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	str := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case str:
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				str = false
			}
		case c == '"':
			str = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			if j := bytes.IndexByte(data[i:], '\n'); j >= 0 {
				i += j - 1 // 换行留着
			} else {
				i = len(data)
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			if j := bytes.Index(data[i+2:], []byte("*/")); j >= 0 {
				i += j + 3
			} else {
				i = len(data)
			}
			out = append(out, ' ')
		case c == '}' || c == ']':
			k := len(bytes.TrimRight(out, " \t\r\n"))
			if k > 0 && out[k-1] == ',' {
				out = append(out[:k-1], out[k:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
