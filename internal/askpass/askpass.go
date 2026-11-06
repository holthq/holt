// Package askpass 是 git / ssh 要凭据时弹 Holt 自己的框。
//
// 应用给每条 git 写命令设 GIT_ASKPASS / SSH_ASKPASS 指向一个两行脚本（Server.Script），脚本 exec "Holt askpass <提示>"
// （helper 模式，Main，main 第一行就分出去，不碰 mygo）；helper 连上应用监听的 unix 套接字（EnvSock），发一行 JSON
// （提示原文和这条命令的 id，EnvID），等应用回一行：ok 就把回答加换行写到 stdout、退出 0；不 ok、连不上、读断了都退出 1，
// 什么都不打印（git / ssh 当作没拿到，命令失败）。helper 自己不设超时：用户可能在想。
//
// 应用这边是 Server：监听、每个连接读一个 Request 交给 handle（它按 id 找是哪个窗口的哪条命令、弹框），handle 答了把 Reply 写回去、
// 关连接。套接字和脚本放在 os.TempDir()（每个用户私有、没空格、够短）：git 用 sh -c 起 askpass，路径带空格就断，
// macOS 的 unix 套接字路径又不能超过 104 字节，所以不放状态目录（~/Library/Application Support 带空格）。
// Classify 把提示分成四种框（HTTPS 用户名 / 密码、SSH 密钥口令、主机指纹、别的）。
package askpass

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// 环境变量：套接字路径、这条命令的 id（应用拿它找是谁在问，不认识的 id 一律回不 ok）。
const (
	EnvSock = "WORKBENCH_ASKPASS_SOCK"
	EnvID   = "WORKBENCH_ASKPASS_ID"
)

// Request 是 helper 发给应用的一行。
type Request struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
}

// Reply 是应用回 helper 的一行：OK 为 false 是用户取消了（或问的命令已经不在了）。
type Reply struct {
	OK   bool   `json:"ok"`
	Text string `json:"text,omitempty"`
}

// maxLine 是一行 JSON 最长多少（主机指纹那段提示也就几百字节）。
const maxLine = 64 << 10

// Main 是 helper 模式（Holt askpass <提示…>）：git / ssh 把提示当一个参数传进来。返回退出码。
func Main(args []string) int {
	return Run(strings.Join(args, " "), os.Getenv(EnvSock), os.Getenv(EnvID), os.Stdout)
}

// Run 把提示发到套接字 sock，答了就把回答加换行写到 out、返回 0；取消、连不上、读断了返回 1，out 里什么都不写。
func Run(prompt, sock, id string, out io.Writer) int {
	if sock == "" {
		return 1
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return 1
	}
	defer conn.Close()
	b, _ := json.Marshal(Request{ID: id, Prompt: prompt})
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return 1
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxLine)).ReadBytes('\n')
	if err != nil {
		return 1
	}
	var rep Reply
	if json.Unmarshal(line, &rep) != nil || !rep.OK {
		return 1
	}
	if _, err := fmt.Fprintln(out, rep.Text); err != nil {
		return 1
	}
	return 0
}

// Server 是应用这边的套接字和脚本。
type Server struct {
	Sock   string // 套接字（WORKBENCH_ASKPASS_SOCK）
	Script string // GIT_ASKPASS / SSH_ASKPASS 指的脚本

	ln     net.Listener
	handle func(req Request, reply func(Reply))
	once   sync.Once
}

// prefix 是套接字和脚本的文件名前缀，后面是进程号。
const prefix = "grove-askpass-"

// Listen 在 dir 里监听 grove-askpass-<pid>.sock（0600）、写 grove-askpass-<pid>.sh（0700，exec exe askpass "$@"），
// 先删掉同前缀、进程已经不在了的旧文件。handle 在 Server 自己的 goroutine 上调，reply 调一次（之后关连接）；
// 不调 reply 的话连接一直开着，helper 一直等（git 被取消时 helper 跟着进程组没了，连接也就断了）。
func Listen(dir, exe string, handle func(req Request, reply func(Reply))) (*Server, error) {
	CleanStale(dir)
	pid := strconv.Itoa(os.Getpid())
	s := &Server{Sock: filepath.Join(dir, prefix+pid+".sock"), Script: filepath.Join(dir, prefix+pid+".sh"), handle: handle}
	os.Remove(s.Sock)
	ln, err := net.Listen("unix", s.Sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(s.Sock, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	script := "#!/bin/sh\nexec " + shellQuote(exe) + " askpass \"$@\"\n"
	if err := os.WriteFile(s.Script, []byte(script), 0o700); err != nil {
		ln.Close()
		return nil, err
	}
	if err := os.Chmod(s.Script, 0o700); err != nil { // 文件已经在的话 WriteFile 不改权限
		ln.Close()
		return nil, err
	}
	s.ln = ln
	go s.serve()
	return s, nil
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.conn(conn)
	}
}

func (s *Server) conn(conn net.Conn) {
	line, err := bufio.NewReader(io.LimitReader(conn, maxLine)).ReadBytes('\n')
	var req Request
	if err != nil || json.Unmarshal(line, &req) != nil {
		conn.Close()
		return
	}
	var once sync.Once
	s.handle(req, func(rep Reply) {
		once.Do(func() {
			b, _ := json.Marshal(rep)
			conn.Write(append(b, '\n'))
			conn.Close()
		})
	})
}

// Close 关掉监听，删掉套接字和脚本（退出时）。
func (s *Server) Close() {
	s.once.Do(func() {
		s.ln.Close()
		os.Remove(s.Sock)
		os.Remove(s.Script)
	})
}

// CleanStale 删掉 dir 里同前缀、进程已经不在了的套接字和脚本（上次没正常退出留下的）。
func CleanStale(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		num, _, _ := strings.Cut(rest, ".")
		pid, err := strconv.Atoi(num)
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		if !processAlive(pid) {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// shellQuote 给 sh 用的单引号括起来（里面的单引号换成：收引号、反斜杠单引号、再开引号）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
