package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 这个文件是 LSP 3.17 里 Holt 用得到的那部分协议：位置、文档同步、补全、悬停、定义、诊断。
// 服务端多给的字段一律不管；服务端少的字段用零值。

// Position 是文档里的一个位置：行从 0 起，character 按协商的编码算（默认 UTF-16 码元）。
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range 是一段文字。
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location 是一个文件里的一段。
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink 是 textDocument/definition 的另一种回答，比 Location 多一个选中的范围。
type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// WorkspaceFolder 是 initialize 里的工作区文件夹。
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// TextDocumentItem 是 didOpen 里的文档。
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// TextDocumentIdentifier 是只带 URI 的文档。
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier 是带版本的文档。
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentContentChangeEvent 是全文同步的一次改动（只有 text）。
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// DidOpenTextDocumentParams、DidChangeTextDocumentParams、DidSaveTextDocumentParams、DidCloseTextDocumentParams
// 是文档同步的四条通知。
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// CompletionParams 是补全请求。
type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *CompletionContext     `json:"context,omitempty"`
}

// CompletionContext 说明补全是用户叫的（1）还是敲字触发的（2）。
type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

// HoverParams 是悬停请求。
type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DefinitionParams 是跳转定义请求。
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// TextEdit 是替换一段文字。
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// CompletionItem 是一条补全。
type CompletionItem struct {
	Label            string          `json:"label"`
	Kind             int             `json:"kind,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	Documentation    json.RawMessage `json:"documentation,omitempty"`
	InsertText       string          `json:"insertText,omitempty"`
	InsertTextFormat int             `json:"insertTextFormat,omitempty"` // 1 普通文字、2 片段
	FilterText       string          `json:"filterText,omitempty"`
	SortText         string          `json:"sortText,omitempty"`
	TextEdit         *TextEdit       `json:"textEdit,omitempty"`
}

// CompletionList 是补全列表。
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// Hover 是悬停回答：contents 可能是字符串、MarkedString 或数组，Range 可以没有。
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// Diagnostic 是服务端报的一处问题。
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"` // 1 错误、2 警告、3 信息、4 提示
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// PublishDiagnosticsParams 是 textDocument/publishDiagnostics 的参数。
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ServerCapabilities 是 initialize 回答里的能力表（只留用得到的几项）。
type ServerCapabilities struct {
	CompletionProvider json.RawMessage `json:"completionProvider,omitempty"`
	HoverProvider      json.RawMessage `json:"hoverProvider,omitempty"`
	DefinitionProvider json.RawMessage `json:"definitionProvider,omitempty"`
}

// InitializeResult 是 initialize 的回答。
type InitializeResult struct {
	Capabilities     ServerCapabilities `json:"capabilities"`
	PositionEncoding string             `json:"positionEncoding,omitempty"`
}

// Has 是服务端有没有这项能力。
func (sc ServerCapabilities) Has(name string) bool {
	var raw json.RawMessage
	switch name {
	case "completion":
		raw = sc.CompletionProvider
	case "hover":
		raw = sc.HoverProvider
	case "definition":
		raw = sc.DefinitionProvider
	}
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("false"))
}

// hoverPart 是 Hover.contents 里的一项：字符串或 {language, value} 的 MarkedString。
func hoverPart(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var ms struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if json.Unmarshal(raw, &ms) == nil && ms.Value != "" {
		return strings.TrimSpace(ms.Value)
	}
	return ""
}

// HoverText 把 Hover.contents 换成能放进悬停卡的纯文字：Markdown 的强调、链接和代码围栏去掉，换行留着。
func HoverText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return plainHover(s)
		}
	case '[':
		var parts []json.RawMessage
		if json.Unmarshal(raw, &parts) == nil {
			var out []string
			for _, p := range parts {
				if s := hoverPart(p); s != "" {
					out = append(out, plainHover(s))
				}
			}
			return strings.Join(out, "\n\n")
		}
	case '{':
		var mc struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &mc) == nil && mc.Value != "" {
			return plainHover(mc.Value)
		}
	}
	return ""
}

// Documentation 把补全项的 documentation（字符串或 MarkupContent）换成文字。
func Documentation(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return plainHover(s)
		}
	case '{':
		var mc struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &mc) == nil {
			return plainHover(mc.Value)
		}
	}
	return ""
}

// plainHover 去掉常见的 Markdown 记号，留下文字：悬停卡是等宽字，不画 Markdown。
func plainHover(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var out []string
	inFence := false
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		if n := len(trim) - len(strings.TrimLeft(trim, "#")); n > 0 && n < len(trim) && trim[n] == ' ' {
			trim = strings.TrimSpace(trim[n:])
		}
		trim = stripLinks(trim)
		trim = strings.ReplaceAll(trim, "**", "")
		trim = strings.ReplaceAll(trim, "__", "")
		trim = strings.ReplaceAll(trim, "`", "")
		out = append(out, trim)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// stripLinks 把 [文字](地址) 换成“文字（地址）”，地址为空就只留文字。
func stripLinks(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '[')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i:], ']')
		if j < 0 {
			b.WriteString(s)
			break
		}
		j += i
		text := s[i+1 : j]
		rest := s[j+1:]
		if strings.HasPrefix(rest, "(") {
			if k := strings.IndexByte(rest, ')'); k >= 0 {
				url := rest[1:k]
				b.WriteString(s[:i])
				b.WriteString(text)
				if url != "" {
					b.WriteString("（")
					b.WriteString(url)
					b.WriteString("）")
				}
				s = rest[k+1:]
				continue
			}
		}
		b.WriteString(s[:j+1])
		s = rest
	}
	return b.String()
}
