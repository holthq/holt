package termd

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// 线上的帧：1 字节类型 + 4 字节大端长度 + 内容。只有 new / attach / hello / list 的内容是 JSON。
// 一个请求一条 unix 连接：连上先发一帧说要干什么（new / attach 之后这条连接就是那个会话的流）。
const (
	// 应用 → termd
	frameNew     = 'n' // NewReq
	frameAttach  = 'a' // AttachReq
	frameInput   = 'i' // 敲的字
	frameResize  = 'r' // 4 个 uint32：列、行、格宽、格高（像素）
	frameTheme   = 'T' // terminal.Theme 的 JSON：程序问颜色时 termd 答这个
	frameClear   = 'c' // 清屏（⌘K）
	frameKill    = 'k' // 关掉会话：shell 收到 SIGHUP
	frameDetach  = 'd' // 只断开，会话留着
	frameList    = 'l'
	frameKillall = 'K'
	frameStop    = 'S' // termd 自己退出（测试用）
	framePing    = 'p'

	// termd → 应用
	frameHello   = 'h' // Hello
	frameOutput  = 'f' // 镜像帧（terminal.Mirror）
	frameResized = 'z' // 2 个 uint32：termd 那边的屏幕换成的大小，之后的帧按它排
	frameDir     = 'w' // OSC 7 报的目录
	frameExited  = 'x' // int32 退出码：shell 退出了，会话留到关掉
	frameGone    = 'g' // 会话不在了；内容 "kicked" 是被别的连接接走了
	frameNoClear = 'N' // 全屏程序里不清屏
	frameBell    = 'b' // 程序响铃（BEL）或要通知（OSC 9、OSC 777）：镜像帧里没有，单独告诉窗口
	frameListed  = 'L' // []Info
	frameOK      = 'o'
	frameError   = 'e'
)

const maxFrame = 256 << 20

// NewReq 开一个新会话。Env 是应用当时的环境（shell 用它，termd 自己的不算），终端配置的环境接在后面（同名的后面的算）。
// Args 是给 Shell 的参数（终端配置的），空为 -l。
type NewReq struct {
	WS         string   `json:"ws,omitempty"`
	Repo       string   `json:"repo,omitempty"`
	Dir        string   `json:"dir,omitempty"`
	Cols       int      `json:"cols,omitempty"`
	Rows       int      `json:"rows,omitempty"`
	CellW      int      `json:"cellW,omitempty"`
	CellH      int      `json:"cellH,omitempty"`
	Env        []string `json:"env,omitempty"`
	Shell      string   `json:"shell,omitempty"`
	Args       []string `json:"args,omitempty"`
	Scrollback int      `json:"scrollback,omitempty"`
}

// AttachReq 接回一个会话；大小不是 0 时先换成这个大小再发第一帧。
type AttachReq struct {
	Sid   string `json:"sid"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	CellW int    `json:"cellW,omitempty"`
	CellH int    `json:"cellH,omitempty"`
}

// Hello 是 new / attach 的回答：之后的第一帧按 Cols × Rows 排。
type Hello struct {
	Sid    string `json:"sid"`
	Pid    int    `json:"pid"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
	Exited bool   `json:"exited,omitempty"`
	Code   int    `json:"code,omitempty"`
	Dir    string `json:"dir,omitempty"`
}

// Info 是 list 里的一个会话。
type Info struct {
	Sid      string    `json:"sid"`
	WS       string    `json:"ws,omitempty"`
	Repo     string    `json:"repo,omitempty"`
	Dir      string    `json:"dir,omitempty"` // OSC 7 报过的，没有是开的时候的目录
	Pid      int       `json:"pid"`
	Exited   bool      `json:"exited,omitempty"`
	Code     int       `json:"code,omitempty"`
	Attached bool      `json:"attached,omitempty"` // 有窗口接着
	Busy     bool      `json:"busy,omitempty"`     // 前台在跑命令
	Started  time.Time `json:"started"`
}

// ErrGone 是要接的会话不在了（termd 重启过、被关了）。
var ErrGone = errors.New("termd: 会话不在了")

func encodeFrame(typ byte, p []byte) []byte {
	b := make([]byte, 5+len(p))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:5], uint32(len(p)))
	copy(b[5:], p)
	return b
}

func writeFrame(w io.Writer, typ byte, p []byte) error {
	_, err := w.Write(encodeFrame(typ, p))
	return err
}

func writeJSON(w io.Writer, typ byte, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFrame(w, typ, b)
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("termd: 帧太大（%d 字节）", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	return head[0], p, nil
}

func uint32s(vs ...int) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.BigEndian.PutUint32(b[4*i:], uint32(max(0, v)))
	}
	return b
}

func parseUint32s(p []byte, n int) ([]int, bool) {
	if len(p) != 4*n {
		return nil, false
	}
	vs := make([]int, n)
	for i := range vs {
		vs[i] = int(binary.BigEndian.Uint32(p[4*i:]))
	}
	return vs, true
}
