package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// 这个文件只负责 LSP 的报文框：Content-Length 头加一行 JSON。

const (
	// maxBodySize 是收得到的一条报文的上限：服务端的补全和诊断不会这么大，防的是坏掉的进程把内存吃光。
	maxBodySize = 128 << 20
	// maxHeaderLine 是一行头的上限。
	maxHeaderLine = 1 << 20
)

// readFrame 读一条报文的内容（不含头）。
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) > maxHeaderLine {
			return nil, errors.New("lsp: 报文头太长")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 || n > maxBodySize {
				return nil, fmt.Errorf("lsp: Content-Length %q 不对", strings.TrimSpace(value))
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: 没有 Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeFrame 写一条报文。
func writeFrame(w *bufio.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Flush()
}

// wireMessage 是收发的一条 JSON-RPC 报文：请求和通知有 Method，回答和请求有 ID。
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 是 JSON-RPC 的 error。
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "lsp: 未知错误"
	}
	return fmt.Sprintf("lsp: %s（%d）", e.Message, e.Code)
}
