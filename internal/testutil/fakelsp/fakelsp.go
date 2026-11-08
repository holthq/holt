// Package fakelsp 是给测试用的最小语言服务器：initialize、文档同步、补全、悬停、定义、
// 诊断各回一份固定的东西。测试程序在 TestMain 里见到 HOLT_FAKE_LSP=1 就运行它。
package fakelsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Env 是让测试程序当语言服务器的环境变量；LogEnv 是它把收到的报文记到哪个文件。
const (
	Env    = "HOLT_FAKE_LSP"
	LogEnv = "HOLT_FAKE_LSP_LOG"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Main 跑假服务器，直到收到 exit 或 stdin 关了。
func Main() {
	in := bufio.NewReaderSize(os.Stdin, 64<<10)
	out := bufio.NewWriterSize(os.Stdout, 64<<10)
	logf, _ := os.OpenFile(os.Getenv(LogEnv), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if logf != nil {
		defer logf.Close()
	}
	lastURI := ""
	for {
		body, err := readFrame(in)
		if err != nil {
			return
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			continue
		}
		if logf != nil {
			fmt.Fprintf(logf, "%s\t%s\n", m.Method, strings.ReplaceAll(string(m.Params), "\n", " "))
		}
		switch m.Method {
		case "initialize":
			reply(out, m.ID, map[string]any{
				"capabilities": map[string]any{
					"completionProvider": map[string]any{"triggerCharacters": []string{"."}},
					"hoverProvider":      true,
					"definitionProvider": true,
					"textDocumentSync":   1,
				},
				"serverInfo": map[string]any{"name": "fakelsp", "version": "1"},
			})
		case "initialized":
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.TextDocument.URI != "" {
				lastURI = p.TextDocument.URI
				publish(out, lastURI, p.TextDocument.Version)
			}
		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.TextDocument.URI != "" {
				lastURI = p.TextDocument.URI
				publish(out, lastURI, p.TextDocument.Version)
			}
		case "textDocument/completion":
			if m.ID != nil {
				reply(out, m.ID, []any{
					map[string]any{
						"label": "Alpha", "kind": 3, "detail": "func Alpha() bool",
						"insertText": "Alpha", "insertTextFormat": 1,
						"documentation": map[string]any{"kind": "markdown", "value": "**Alpha** 说明"},
					},
					map[string]any{
						"label": "Beta", "kind": 15, "insertText": "beta(${1:x})", "insertTextFormat": 2,
					},
				})
			}
		case "textDocument/hover":
			if m.ID != nil {
				reply(out, m.ID, map[string]any{
					"contents": map[string]any{"kind": "markdown", "value": "**假的** 悬停\n\n```go\nfunc A()\n```"},
					"range":    map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 1}},
				})
			}
		case "textDocument/definition":
			if m.ID != nil {
				uri := lastURI
				reply(out, m.ID, []any{map[string]any{
					"uri": uri,
					"range": map[string]any{
						"start": map[string]int{"line": 1, "character": 2},
						"end":   map[string]int{"line": 1, "character": 5},
					},
				}})
			}
		case "shutdown":
			if m.ID != nil {
				reply(out, m.ID, nil)
			}
		case "exit":
			return
		case "workspace/configuration", "workspace/workspaceFolders":
			// 服务端不该反过来问，这里只是不挂。
			if m.ID != nil {
				reply(out, m.ID, nil)
			}
		}
	}
}

func publish(w *bufio.Writer, uri string, version int) {
	write(w, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]any{
			"uri":     uri,
			"version": version,
			"diagnostics": []any{map[string]any{
				"range": map[string]any{
					"start": map[string]int{"line": 0, "character": 0},
					"end":   map[string]int{"line": 0, "character": 1},
				},
				"severity": 1,
				"source":   "fake",
				"message":  "fake error",
			}},
		},
	})
}

func reply(w *bufio.Writer, id json.RawMessage, result any) {
	write(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func write(w *bufio.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(b))
	_, _ = w.Write(b)
	_ = w.Flush()
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("bad content length")
			}
			length = n
		}
	}
	if length < 0 {
		return nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
