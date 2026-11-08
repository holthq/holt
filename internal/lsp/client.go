package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClosed 是客户端已经关掉（或服务端断了）之后再用它。
var ErrClosed = errors.New("lsp: 客户端已关闭")

// ErrStuck 是服务端不读 stdin：请求排不进去，多半是卡死了。
var ErrStuck = errors.New("lsp: 语言服务器没有在读")

// sendTimeout 是往管道里排一条报文最多等多久：服务端卡住时不能让主线程陪着一起卡。
const sendTimeout = 2 * time.Second

// DefaultInitTimeout 是 initialize 握手最多等多久：gopls 头一次给大仓库建索引可能慢，但也不该无限等。
const DefaultInitTimeout = 30 * time.Second

// Options 是起一个语言服务器要的东西。
type Options struct {
	Command       string // shell 命令（login shell -lc 跑），如 "gopls"
	Shell         string // login shell；空为 /bin/sh
	Dir           string // 工作目录，一般是仓库根
	RootURI       string // initialize 的 rootUri；空为 Dir 的 file:// 地址
	Folders       []WorkspaceFolder
	Env           []string // 额外的环境变量，K=V
	ClientName    string
	ClientVersion string
	InitTimeout   time.Duration
	Logf          func(format string, args ...any)
	OnDiagnostics func(PublishDiagnosticsParams)
	OnExit        func(error)
}

// Client 是一个跑着的语言服务器。
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   chan []byte
	done  chan struct{}

	closeOnce sync.Once
	failOnce  sync.Once
	stopping  atomic.Bool

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResult

	folders       []WorkspaceFolder
	caps          ServerCapabilities
	posEncoding   Encoding
	logf          func(string, ...any)
	onDiagnostics func(PublishDiagnosticsParams)
	onExit        func(error)
	tail          *tail
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

// tail 留语言服务器 stderr 的最后几行，initialize 失败时写在错误里。
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) add(line string) {
	t.mu.Lock()
	t.buf = append(t.buf, line...)
	t.buf = append(t.buf, '\n')
	if len(t.buf) > 8<<10 {
		t.buf = t.buf[len(t.buf)-(8<<10):]
	}
	t.mu.Unlock()
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// Start 起进程并完成 initialize；失败时把进程杀掉。
func Start(ctx context.Context, opts Options) (*Client, error) {
	if strings.TrimSpace(opts.Command) == "" {
		return nil, errors.New("lsp: 空命令")
	}
	shell := opts.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	dir := opts.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	timeout := opts.InitTimeout
	if timeout == 0 {
		timeout = DefaultInitTimeout
	}
	rootURI := opts.RootURI
	if rootURI == "" {
		rootURI = PathToURI(dir)
	}

	cmd := exec.Command(shell, "-lc", "exec "+opts.Command)
	cmd.Dir = dir
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &Client{
		cmd:           cmd,
		stdin:         stdin,
		out:           make(chan []byte, 256),
		done:          make(chan struct{}),
		pending:       map[int64]chan rpcResult{},
		folders:       opts.Folders,
		logf:          logf,
		onDiagnostics: opts.OnDiagnostics,
		onExit:        opts.OnExit,
		tail:          &tail{},
	}
	go c.writeLoop(stdin)
	go c.readLoop(stdout)
	go c.logLoop(stderr)
	go c.waitLoop()

	params := map[string]any{
		"processId": os.Getpid(),
		"clientInfo": map[string]any{
			"name":    clientName(opts.ClientName),
			"version": opts.ClientVersion,
		},
		"rootUri":      rootURI,
		"rootPath":     dir,
		"trace":        "off",
		"capabilities": clientCapabilities(),
	}
	if len(opts.Folders) > 0 {
		params["workspaceFolders"] = opts.Folders
	}
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var res InitializeResult
	if err := c.Request(initCtx, "initialize", params, &res); err != nil {
		detail := c.tail.String()
		c.Kill()
		if detail != "" {
			return nil, fmt.Errorf("%w\n%s", err, detail)
		}
		return nil, err
	}
	c.caps = res.Capabilities
	c.posEncoding = EncodingOf(res.PositionEncoding)
	c.Notify("initialized", map[string]any{})
	return c, nil
}

func clientName(name string) string {
	if name == "" {
		return "Holt"
	}
	return name
}

// clientCapabilities 是 Holt 支持的那部分客户端能力：全文同步、片段补全、Markdown 悬停、
// 诊断带版本。服务端据此决定给什么。
func clientCapabilities() map[string]any {
	return map[string]any{
		"workspace": map[string]any{
			"workspaceFolders": true,
			"configuration":    true,
			"applyEdit":        false,
		},
		"textDocument": map[string]any{
			"synchronization": map[string]any{
				"dynamicRegistration": false,
				"willSave":            false,
				"willSaveWaitUntil":   false,
				"didSave":             true,
			},
			"completion": map[string]any{
				"dynamicRegistration": false,
				"contextSupport":      true,
				"completionItem": map[string]any{
					"snippetSupport":       true,
					"documentationFormat":  []string{"markdown", "plaintext"},
					"insertReplaceSupport": false,
					"resolveSupport":       map[string]any{"properties": []string{}},
				},
			},
			"hover": map[string]any{
				"dynamicRegistration": false,
				"contentFormat":       []string{"markdown", "plaintext"},
			},
			"definition": map[string]any{
				"dynamicRegistration": false,
				"linkSupport":         true,
			},
			"publishDiagnostics": map[string]any{
				"relatedInformation": false,
				"versionSupport":     true,
				"tagSupport":         map[string]any{"valueSet": []int{}},
			},
		},
		"general": map[string]any{"positionEncodings": []string{"utf-16"}},
	}
}

// Supports 是服务端有没有补全、悬停、定义这几项能力。
func (c *Client) Supports(name string) bool { return c.caps.Has(name) }

// PositionEncoding 是服务端协商的位置单位。
func (c *Client) PositionEncoding() Encoding { return c.posEncoding }

// Request 发一条请求等回答；ctx 取消时给服务端发 $/cancelRequest。
func (c *Client) Request(ctx context.Context, method string, params any, result any) error {
	if c == nil {
		return ErrClosed
	}
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	raw, err := requestBytes(id, method, params)
	if err != nil {
		return err
	}
	if !c.send(raw, true) {
		select {
		case <-c.done:
			return ErrClosed
		default:
			return ErrStuck
		}
	}
	select {
	case res := <-ch:
		if res.err != nil {
			return res.err
		}
		if result != nil && len(res.result) > 0 {
			if err := json.Unmarshal(res.result, result); err != nil {
				return fmt.Errorf("lsp: %s 的回答解不开：%w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

// Notify 发一条通知；队列满（服务端卡住）时返回 false，调用方下次再发。
func (c *Client) Notify(method string, params any) bool {
	if c == nil {
		return false
	}
	raw, err := notificationBytes(method, params)
	if err != nil {
		return false
	}
	return c.send(raw, false)
}

// DidOpen、DidChange、DidSave、DidClose 是文档同步的四条通知。全文同步：didChange 带整份文字。
func (c *Client) DidOpen(uri, languageID string, version int, text string) bool {
	return c.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uri, LanguageID: languageID, Version: version, Text: text},
	})
}

func (c *Client) DidChange(uri string, version int, text string) bool {
	return c.Notify("textDocument/didChange", DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: uri, Version: version},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: text}},
	})
}

func (c *Client) DidSave(uri string) bool {
	return c.Notify("textDocument/didSave", DidSaveTextDocumentParams{TextDocument: TextDocumentIdentifier{URI: uri}})
}

func (c *Client) DidClose(uri string) bool {
	return c.Notify("textDocument/didClose", DidCloseTextDocumentParams{TextDocument: TextDocumentIdentifier{URI: uri}})
}

// Completion 问补全；服务端回 null 时是空。
func (c *Client) Completion(ctx context.Context, uri string, pos Position) ([]CompletionItem, error) {
	params := CompletionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
		Context:      &CompletionContext{TriggerKind: 1},
	}
	var raw json.RawMessage
	if err := c.Request(ctx, "textDocument/completion", params, &raw); err != nil {
		return nil, err
	}
	raw = trimNull(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '[' {
		var items []CompletionItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	var list CompletionList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Hover 问悬停；服务端回 null 时是 nil。
func (c *Client) Hover(ctx context.Context, uri string, pos Position) (*Hover, error) {
	var h *Hover
	err := c.Request(ctx, "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
	}, &h)
	if err != nil {
		return nil, err
	}
	if h == nil || len(trimNull(h.Contents)) == 0 {
		return nil, nil
	}
	return h, nil
}

// Definition 问跳转定义：Location 和 LocationLink 都换成 Location（有 targetSelectionRange 时用它）。
func (c *Client) Definition(ctx context.Context, uri string, pos Position) ([]Location, error) {
	var raw json.RawMessage
	if err := c.Request(ctx, "textDocument/definition", DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
	}, &raw); err != nil {
		return nil, err
	}
	raw = trimNull(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '{' {
		var one Location
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		return []Location{one}, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(arr))
	for _, item := range arr {
		item = trimNull(item)
		if len(item) == 0 {
			continue
		}
		if item[0] == '{' {
			var loc Location
			if json.Unmarshal(item, &loc) == nil && loc.URI != "" {
				out = append(out, loc)
				continue
			}
			var link LocationLink
			if json.Unmarshal(item, &link) == nil && link.TargetURI != "" {
				r := link.TargetRange
				if link.TargetSelectionRange != (Range{}) {
					r = link.TargetSelectionRange
				}
				out = append(out, Location{URI: link.TargetURI, Range: r})
			}
		}
	}
	return out, nil
}

// Close 先 shutdown、再 exit；进程不退就杀掉。可以重复调。
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.stopping.Store(true)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.Request(ctx, "shutdown", nil, nil)
		cancel()
		c.Notify("exit", nil)
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		select {
		case <-c.done:
		case <-time.After(1500 * time.Millisecond):
			c.Kill()
		}
	})
}

// Kill 立刻杀掉进程（连同它起的子进程）。
func (c *Client) Kill() {
	if c == nil {
		return
	}
	c.stopping.Store(true)
	killProcess(c.cmd)
	c.fail(ErrClosed)
}

func (c *Client) send(raw []byte, block bool) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	if block {
		t := time.NewTimer(sendTimeout)
		defer t.Stop()
		select {
		case c.out <- raw:
			return true
		case <-c.done:
			return false
		case <-t.C:
			return false
		}
	}
	select {
	case c.out <- raw:
		return true
	default:
		return false
	}
}

func (c *Client) writeLoop(stdout io.Writer) {
	w := bufio.NewWriterSize(stdout, 64<<10)
	for {
		select {
		case raw := <-c.out:
			if err := writeFrame(w, raw); err != nil {
				c.fail(err)
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *Client) readLoop(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		body, err := readFrame(r)
		if err != nil {
			c.fail(err)
			return
		}
		c.handle(body)
	}
}

func (c *Client) logLoop(stderr io.Reader) {
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// stderr 只留最后几行：init 失败、进程意外退出时写进错误里；不逐行抄进 workbench.log
		// （clangd 这类服务端一开就吐几十行，按平常的日志级别记会把日志刷满）。
		c.tail.add(line)
	}
}

func (c *Client) waitLoop() {
	err := c.cmd.Wait()
	if c.stopping.Load() {
		c.fail(ErrClosed)
		return
	}
	if err == nil {
		err = errors.New("lsp: 语言服务器退出了")
	}
	if detail := c.tail.String(); detail != "" {
		err = fmt.Errorf("%w\n%s", err, detail)
	}
	c.fail(err)
}

func (c *Client) fail(err error) {
	c.failOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		for id, ch := range c.pending {
			ch <- rpcResult{err: err}
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if !c.stopping.Load() && c.onExit != nil {
			c.onExit(err)
		}
	})
}

func (c *Client) handle(body []byte) {
	var m wireMessage
	if err := json.Unmarshal(body, &m); err != nil {
		c.logf("lsp: 收到解不开的报文：%v", err)
		return
	}
	switch {
	case m.Method == "" && m.ID != nil:
		c.handleResponse(m)
	case m.ID != nil:
		c.handleServerRequest(m)
	default:
		c.handleNotification(m)
	}
}

func (c *Client) handleResponse(m wireMessage) {
	id, ok := parseID(m.ID)
	if !ok {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if m.Error != nil {
		ch <- rpcResult{err: m.Error}
		return
	}
	ch <- rpcResult{result: m.Result}
}

func (c *Client) handleServerRequest(m wireMessage) {
	var result any
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		result = make([]any, len(p.Items))
	case "workspace/workspaceFolders":
		if len(c.folders) > 0 {
			result = c.folders
		}
	case "workspace/applyEdit":
		result = map[string]any{"applied": false}
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create",
		"window/showMessageRequest":
		// 不支持动态注册和工作区编辑，回 null / false，服务端照样能用静态能力。
	default:
		result = nil
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
	c.send(raw, true)
}

func (c *Client) handleNotification(m wireMessage) {
	switch m.Method {
	case "textDocument/publishDiagnostics":
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(m.Params, &p); err != nil || p.URI == "" {
			return
		}
		if c.onDiagnostics != nil {
			c.onDiagnostics(p)
		}
	case "window/logMessage", "window/showMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(m.Params, &p) == nil && p.Message != "" {
			c.logf("lsp: %s", p.Message)
		}
	}
}

func parseID(raw json.RawMessage) (int64, bool) {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func requestBytes(id int64, method string, params any) ([]byte, error) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	return json.Marshal(msg)
}

func notificationBytes(method string, params any) ([]byte, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return json.Marshal(msg)
}

func trimNull(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return nil
	}
	return raw
}
