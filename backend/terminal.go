//go:build !noterminal

// 终端功能可在编译期整体剔除（`-tags noterminal`，见 Makefile / build.sh 的 TERMINAL=0）。
// 无终端构建由 terminal_disabled.go 提供跨文件引用的空实现。

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// 会话历史回放/读取的大小上限：避免把无限增长的临时文件全部塞进单次 WebSocket
// 或 JSON 响应。仅回放最近 maxHistoryBytes 的内容即可满足“恢复会话历史”。
//
// 它同时是**历史文件的写入侧上限**（见 Session.trimHistoryLocked）：文件本身若不
// 截断，读取侧的限制只能保证「一次不读太多」，磁盘占用仍随会话时长无限增长
// —— 一个长跑会话 + 高频输出足以把数据目录所在分区写满。
const maxHistoryBytes = 4 * 1024 * 1024

const (
	// maxFrameBytes 是单个 WebSocket 帧的载荷上限。帧头里的长度是客户端说了算的
	// （16/64 位），畸形或恶意的一帧可以声明 TB 级长度 —— 直接 make 会把控制台打爆
	// （OOM 杀掉本进程；而它同时是 dsh 的守护进程，等于整站掉线）。
	maxFrameBytes = 1 << 20 // 1 MiB
	// maxMessageBytes 是分片累积后单条消息的上限：否则用连续的 continuation 帧就能
	// 把 msgBuf 撑爆。
	maxMessageBytes = 8 << 20 // 8 MiB
	// connWriteTimeout 是向终端连接写数据的超时。客户端不再读取（笔记本休眠、网络
	// 半开、代理不再收）时，写会在发送缓冲写满后阻塞 —— 而广播与历史回放都发生在
	// histMu 临界区内：没有超时就会连带冻结 PTY 读、/api/sessions（list 持 m.mu 再取
	// histMu）与新挂载，连「重连夺回」这条自救路径都会失效。
	connWriteTimeout = 5 * time.Second
)

// errFrameTooLarge 表示客户端发来的帧/消息超过上限，连接会被关闭。
var errFrameTooLarge = errors.New("terminal websocket frame too large")

// wsChunkSize 是历史回放时单帧的最大字节数。
const wsChunkSize = 32 * 1024 // 32KB

// errNotOwner：请求来自已不是操作端的连接（已被其他设备顶掉的旧连接）。
// 上层据此静默忽略：它只是残留帧，不是错误。
var errNotOwner = errors.New("not session owner")

// Session 是一个独立的 PTY 终端会话。输出实时镜像到临时历史文件（应用停止时
// 整目录清除），浏览器断开只“解挂载”不杀会话，刷新后凭 id 重新挂载并回放历史。
type Session struct {
	id        string
	user      string
	workspace string
	mode      string
	done      chan struct{}
	cmd       *exec.Cmd
	pty       *os.File
	hist      *os.File
	start     time.Time

	// histMu 串行化“追加历史 + 广播到已挂载连接”：回放历史与实时追加在同一把
	// 锁内完成，保证不存在重复或丢失的字节。
	histMu sync.Mutex
	conn   net.Conn
	connMu sync.Mutex

	exited bool
	mu     sync.Mutex
	closed bool
}

func (s *Session) markExited() {
	s.mu.Lock()
	s.exited = true
	s.mu.Unlock()
}

func (s *Session) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// writeFrame 向终端连接写一帧，带写超时。返回错误表示该端已经写不动了
// （不读数据/连接已断），调用方应把它当死连接处理。timeout 取值见 connWriteTimeout。
func writeFrame(c net.Conn, frame []byte) error {
	if c == nil {
		return nil
	}
	// 每次写前重设：上一次的过期时间不能留下来影响本次。
	_ = c.SetWriteDeadline(time.Now().Add(connWriteTimeout))
	_, err := c.Write(frame)
	return err
}

// dropConn 把写不动的连接从会话上摘掉并关闭。会话本身不受影响（继续运行并写历史
// 文件），下次挂载仍可回放。调用方可能持有 histMu —— detach 只取 connMu，顺序与
// pump/attach 一致，不会反向嵌套。
func (s *Session) dropConn(c net.Conn, err error) {
	if c == nil {
		return
	}
	s.detach(c)
	_ = c.Close()
	logWarn("[terminal] session %s: dropped unresponsive client connection: %v", s.id, err)
}

// pump 持续读取 PTY 输出：追加到历史文件，并向已挂载的连接实时广播。
// 同一把 histMu 保证“文件”与“连接”两路输出顺序一致。
func (s *Session) pump() {
	buf := make([]byte, 8192)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.histMu.Lock()
			s.appendHistoryLocked(chunk)
			s.connMu.Lock()
			c := s.conn
			s.connMu.Unlock()
			if c != nil {
				if werr := writeFrame(c, wsFrame(opBin, chunk)); werr != nil {
					// 写不动就不写了：摘掉并关闭它，别让它继续占着 histMu。
					s.dropConn(c, werr)
				}
			}
			s.histMu.Unlock()
		}
		if err != nil {
			break
		}
	}
	_ = s.cmd.Wait()
	_ = s.pty.Close()
	defer close(s.done)
	// 进程退出后补一条提示帧与退出控制帧（若还有连接在挂载）
	s.histMu.Lock()
	s.markExited()
	note := []byte("\r\n\x1b[31m[process exited]\x1b[0m\r\n")
	s.appendHistoryLocked(note)
	s.connMu.Lock()
	c := s.conn
	s.connMu.Unlock()
	if c != nil {
		if werr := writeFrame(c, wsFrame(opBin, note)); werr == nil {
			if werr = writeFrame(c, wsFrame(opText, []byte("\x1b]exit\x07"))); werr != nil {
				s.dropConn(c, werr)
			}
		} else {
			s.dropConn(c, werr)
		}
	}
	s.histMu.Unlock()
}

// appendHistoryLocked 把一段 PTY 输出追加到历史文件，并维护写入侧上限。
// 调用方必须持有 s.histMu（与「回放历史 + 实时广播」共用同一临界区）。
//
// 抽成独立方法是为了让「写入侧上限」这条语义可以被单测覆盖：沙箱里建不起 PTY
// （/dev/ptmx 权限），pump 本体没法起，但这段纯文件逻辑可以。
func (s *Session) appendHistoryLocked(chunk []byte) {
	if s.hist == nil {
		return
	}
	if _, err := s.hist.Seek(0, io.SeekEnd); err != nil {
		logWarn("[terminal] session %s: seek history failed: %v", s.id, err)
		return
	}
	if _, err := s.hist.Write(chunk); err != nil {
		// 历史写失败不影响实时流，但回放能力会降级 —— 留一条线索（重复行由
		// logging.go 的重复抑制兜住，不会刷屏）。
		logWarn("[terminal] session %s: append history failed: %v", s.id, err)
		return
	}
	s.trimHistoryLocked()
}

// trimHistoryLocked 给历史文件加**写入侧上限**：超过 maxHistoryBytes 时滚动截断，
// 只保留末尾 maxHistoryBytes 字节。调用方必须持有 s.histMu，且在 Write 之后调用。
//
// 为什么选「滚动截断」而不是「给已退出会话加 TTL 回收」：
//   - 读取侧本来只回放末尾 maxHistoryBytes，截掉更早的字节不影响任何可见语义
//     （attach 回放、/api/session/history、/api/sessions 的 size 都只看末尾那段）；
//   - 会话继续运行时磁盘占用有界，而 TTL 回收只解决「已退出会话」那一半问题；
//   - TTL 回收会让「浏览器断开 → 稍后回来重连」这条被明确要求保留的路径出现
//     新语义（过期后 history 为空、会话从列表消失），风险更大。
//
// 保留的边界是「末尾 N 字节」而不是整数个转义序列：attach/history 的读取路径本来
// 就从任意偏移开始回放，这里与它们保持一致（终端渲染对残缺的头部序列本就容错）。
func (s *Session) trimHistoryLocked() {
	if s.hist == nil {
		return
	}
	st, err := s.hist.Stat()
	if err != nil || st.Size() <= maxHistoryBytes {
		return
	}
	keep := int64(maxHistoryBytes)
	start := st.Size() - keep
	buf := make([]byte, keep)
	// ReadAt/WriteAt 不依赖回放留下的读游标。
	if _, err := s.hist.ReadAt(buf, start); err != nil {
		logWarn("[terminal] session %s: read history tail failed: %v", s.id, err)
		return
	}
	if err := s.hist.Truncate(0); err != nil {
		logWarn("[terminal] session %s: truncate history failed: %v", s.id, err)
		return
	}
	// 截断后从文件起点写回保留的尾部。
	if _, err := s.hist.WriteAt(buf, 0); err != nil {
		logWarn("[terminal] session %s: rewrite history tail failed: %v", s.id, err)
	}
}

// attach 把连接挂载到会话：先回放历史文件内容（上限 maxHistoryBytes，分帧发送），
// 再发送 ready 标记，最后接管实时流。整段在 histMu 内完成，防止新增输出既被回放
// 又被实时广播造成重复。
//
// **单挂载点语义**：一个会话在任何时刻只允许一个操作端（能写输入、能改 PTY 尺寸）。
// 挂载成功即成为操作端，返回值是**被顶掉的旧连接**（无则 nil），由调用方负责通知并
// 关闭它（terminal.go 的 kickDetached）——只影响这一个会话，其他会话的连接不动。
func (s *Session) attach(c net.Conn) (net.Conn, error) {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	if s.isClosed() {
		return nil, errors.New("session closed")
	}
	if s.hist != nil {
		size, err := s.hist.Seek(0, io.SeekEnd)
		if err == nil {
			start := int64(0)
			if size > maxHistoryBytes {
				start = size - maxHistoryBytes
			}
			if _, err := s.hist.Seek(start, io.SeekStart); err == nil {
				chunk := make([]byte, wsChunkSize)
				for {
					n, rerr := s.hist.Read(chunk)
					if n > 0 {
						if werr := writeFrame(c, wsFrame(opBin, chunk[:n])); werr != nil {
							return nil, werr
						}
					}
					if rerr != nil {
						break
					}
				}
			}
		}
	}
	if err := writeFrame(c, wsFrame(opText, []byte("\x1b]ready\x07"))); err != nil {
		return nil, err
	}
	if s.isExited() {
		if err := writeFrame(c, wsFrame(opText, []byte("\x1b]exit\x07"))); err != nil {
			return nil, err
		}
	}
	// 换主与历史回放同处一个 histMu 临界区：新连接既不漏字节，也不与广播交叉。
	s.connMu.Lock()
	prev := s.conn
	s.conn = c
	s.connMu.Unlock()
	if prev == c {
		prev = nil // 重复挂载同一连接：无需顶号
	}
	return prev, nil
}

// isOwner 判断该连接是否仍是会话的操作端（被顶掉的旧连接为 false）。
func (s *Session) isOwner(c net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.conn != nil && s.conn == c
}

// detach 解除当前连接挂载（不杀会话，会话继续运行并写入历史文件）。
func (s *Session) detach(c net.Conn) {
	s.connMu.Lock()
	if s.conn == c {
		s.conn = nil
	}
	s.connMu.Unlock()
}

// writeInput 把客户端输入写入 PTY。**只有当前操作端能写**：被顶掉的旧连接即使还有
// 残留帧抵达，也在服务端被丢弃——否则两台设备会同时操作同一个 PTY（两边都能打字，
// 却只有一边看得到输出）。
func (s *Session) writeInput(c net.Conn, p []byte) error {
	if !s.isOwner(c) {
		return errNotOwner
	}
	_, err := s.pty.Write(p)
	return err
}

// resize 调整 PTY 尺寸。同样只接受操作端的请求：被顶掉的旧设备随后会触发一次
// 尺寸重排，若不加限制就会把 PTY 改成它自己的窗口大小。
func (s *Session) resize(c net.Conn, cols, rows uint16) error {
	if !s.isOwner(c) {
		return errNotOwner
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

// close 终止进程并清理资源（删除历史文件）。调用方负责从管理器移除。
func (s *Session) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil && !s.isExited() {
		stopTerminalProcess(s.cmd.Process)
	}
	if s.pty != nil {
		_ = s.pty.Close()
	}
	if s.done != nil {
		<-s.done
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	s.connMu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	s.connMu.Unlock()
	if s.hist != nil {
		name := s.hist.Name()
		_ = s.hist.Close()
		s.hist = nil
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SessionManager 管理全部活动会话。
type SessionManager struct {
	renv     *RuntimeEnv
	mu       sync.Mutex
	sessions map[string]*Session
	closing  bool
}

func NewSessionManager(renv *RuntimeEnv) *SessionManager {
	return &SessionManager{renv: renv, sessions: map[string]*Session{}}
}

// Only pass settings needed by the CLI. The console process also has fnOS
// gateway credentials and its own login secrets in its environment.
func codexTerminalCommand(binary, home, cwd, mode string) (*exec.Cmd, error) {
	var args []string
	switch mode {
	case "codex":
		args = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
	case "login":
		args = []string{"login", "--device-auth"}
		cwd = home
	default:
		return nil, errors.New("unsupported Codex session mode")
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(codexUserEnv(home), "PWD="+cwd, "SHELL=/bin/bash",
		"LANG="+localeLang(os.Getenv("LANG")), "TERM=xterm-256color", "COLORTERM=truecolor")
	return cmd, nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", "")
}

// Start the official CLI directly in a PTY. workspace has already been resolved
// against the caller's current fnOS authorization by the HTTP entry point.
func (m *SessionManager) create(user, workspace, mode, binary string) (*Session, error) {
	if user == "" || m.renv.CodexHome == "" || m.renv.SessionDir == "" {
		return nil, errors.New("missing Codex console session settings")
	}
	home, err := filepath.Abs(filepath.Join(m.renv.CodexHome, user))
	if err != nil {
		return nil, err
	}
	cmd, err := codexTerminalCommand(binary, home, workspace, mode)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.renv.SessionDir, 0o700); err != nil {
		return nil, err
	}
	id := newID()
	fname := filepath.Join(m.renv.SessionDir, id+".log")
	hist, err := os.OpenFile(fname, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	// Hold the manager lock across spawn and publication so CloseAll never leaves an orphan.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		hist.Close()
		os.Remove(fname)
		return nil, errors.New("console is stopping")
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		hist.Close()
		os.Remove(fname)
		return nil, err
	}
	s := &Session{
		id: id, user: user, workspace: workspace, mode: mode, done: make(chan struct{}),
		cmd: cmd, pty: f, hist: hist, start: time.Now(),
	}
	m.sessions[id] = s
	go s.pump()
	return s, nil
}

func (m *SessionManager) get(user, id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || user == "" || s.user != user {
		return nil, false
	}
	return s, true
}

// list 返回会话摘要列表（供前端启动时恢复会话）。
func (m *SessionManager) list(user string) []sessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sessionInfo, 0, len(m.sessions))
	for id, s := range m.sessions {
		if user == "" || user != s.user {
			continue
		}
		info := sessionInfo{
			ID: id, Workspace: s.workspace, Mode: s.mode,
			Created: s.start.UTC().Format(time.RFC3339Nano),
			Exited:  s.isExited(),
		}
		s.histMu.Lock()
		if s.hist != nil {
			if st, err := s.hist.Stat(); err == nil {
				info.Size = st.Size()
			}
		}
		s.histMu.Unlock()
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return m.sessions[out[i].ID].start.After(m.sessions[out[j].ID].start) })
	return out
}

// history 读取会话历史文件内容（上限 maxHistoryBytes，取末尾部分）。
func (m *SessionManager) history(user, id string) ([]byte, error) {
	s, ok := m.get(user, id)
	if !ok {
		return nil, errors.New("session not found")
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	if s.hist == nil {
		return []byte{}, nil
	}
	size, err := s.hist.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	start := int64(0)
	if size > maxHistoryBytes {
		start = size - maxHistoryBytes
	}
	if _, err := s.hist.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(s.hist)
}

// clearKeepLineBytes 是「清屏时从文件末尾往回找当前行行首」的窗口上限：最后一行比它
// 还长（进度条、全屏 TUI 这类长时间不换行的输出）就当作没有可保留的当前行，整份清空。
// 提示符加上用户敲了一半的命令远小于这个值。
const clearKeepLineBytes = 8 * 1024

// clearHistory 清空指定会话的历史临时文件（用于前端「清屏」同步清除历史，
// 重连/刷新后不再回放旧内容）。
//
// 但**当前行必须留下**：清屏清掉的是「已经翻过去的内容」，shell 此刻正停在的那一行
// （通常是提示符，可能还带着用户敲了一半的命令）并没有消失 —— 终端的可见状态就是
// 「这一行 + 光标列」，而历史文件又是重连时唯一的回放源。整份截到 0 的话，前端
// reset 掉屏幕后没有任何字节可回放：bash 以为提示符还在屏幕上，不会重打，于是重连
// 得到一个空白终端，必须手动回车一次才「有消息」（用户报告的现象）。
// 截到最后一个 '\n' 之后，回放的起点是行首、写出的列数与 PTY 侧完全一致。
//
// 当前行为空（文件以换行结尾，光标停在空行行首）时没有可保留的内容，仍然整份清空：
// 那种状态下屏幕上本来就只有一条空行。
func (m *SessionManager) clearHistory(user, id string) error {
	s, ok := m.get(user, id)
	if !ok {
		return errors.New("session not found")
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	if s.hist == nil {
		return nil
	}
	return s.truncateToCurrentLineLocked()
}

// truncateToCurrentLineLocked 丢弃当前行之前的全部历史（语义见 clearHistory）。
// 调用方必须持有 s.histMu：改写与 pump 的追加共用同一临界区，否则一次并发的
// append 就会插到「清屏前」的位置上。
//
// 为什么不是一次 Truncate：Truncate 只能砍掉**文件末尾**，而这里要丢的是开头，
// 只能「读出当前行 → 清空 → 写回」（与 trimHistoryLocked 同一个套路，只是它要保
// 的是末尾）。代价是一次 ≤ clearKeepLineBytes 的读写。
//
// 只认 '\n' 作为换行：'\r' 是「回到行首重画」，行并没有结束，按它切会把半行输出
// 留在文件开头、回放时错位。
func (s *Session) truncateToCurrentLineLocked() error {
	st, err := s.hist.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	if size == 0 {
		return nil
	}
	start := size - clearKeepLineBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := s.hist.ReadAt(buf, start); err != nil {
		return err
	}
	var keep []byte
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		// 最后一个换行之后才是当前行；它恰好为空（文件以换行结尾）时没有可留的内容。
		keep = buf[i+1:]
	} else if start == 0 {
		// 整份历史只有一行（刚建好、只打了一个提示符的会话就是这种形态）：
		// 这一行就是当前行，不能因为「找不到换行」把它当成没有内容清掉。
		keep = buf
	}
	// start > 0 且窗口里没有换行 = 当前行比 clearKeepLineBytes 还长（进度条 /
	// 全屏 TUI）：从中间截一段回放只会是乱码，宁可整份清空。
	if err := s.hist.Truncate(0); err != nil {
		return err
	}
	if len(keep) == 0 {
		return nil
	}
	_, err = s.hist.WriteAt(keep, 0)
	return err
}

// closeByID 关闭指定会话并移除。
func (m *SessionManager) closeByID(user, id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || user == "" || s.user != user {
		m.mu.Unlock()
		return errors.New("session not found")
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	return s.close()
}

// CloseAll 终止所有会话（进程 + 文件），随后由 main 删除整个临时目录。
func (m *SessionManager) CloseAll() {
	m.mu.Lock()
	m.closing = true
	items := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		items = append(items, s)
	}
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, s := range items {
		s.close()
	}
}

// ServeHTTP hijacks the connection, upgrades to WebSocket, then either creates
// a new session (id empty → 先发 \x1b]id;<id>\x07 控制帧) or attaches to an
// existing one (回放历史 + \x1b]ready\x07 后进入实时流)。
// 浏览器断开只解挂载；会话继续运行并写入历史临时文件，刷新后可重新挂载。
// **单挂载点**：一个会话同时只有一个操作端，新挂载会顶掉旧连接（kickDetached）。
func (m *AdminMux) handleTerminal(w http.ResponseWriter, r *http.Request) {
	user := consoleUser(r)
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	rawKey, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(rawKey) != 16 || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "invalid websocket handshake", http.StatusBadRequest)
		return
	}
	query := r.URL.Query()
	id := query.Get("id")
	var session *Session
	var binary string
	workspace := query.Get("workspace")
	mode := query.Get("mode")
	if mode == "" {
		mode = "codex"
	}
	if id != "" {
		var exists bool
		session, exists = m.sessions.get(user, id)
		if !exists {
			http.NotFound(w, r)
			return
		}
		workspace = session.workspace
	} else if mode != "codex" && mode != "login" {
		http.Error(w, "invalid session mode", http.StatusBadRequest)
		return
	}
	workspace, err = m.workspace(r, workspace)
	if err != nil || (session != nil && workspace != session.workspace) {
		http.Error(w, "workspace unavailable", http.StatusForbidden)
		return
	}
	if session == nil {
		binary, err = m.codex.Binary()
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"); err != nil {
		return
	}
	if err := brw.Flush(); err != nil {
		return
	}
	if session == nil {
		session, err = m.sessions.create(user, workspace, mode, binary)
		if err != nil {
			_ = writeFrame(conn, wsFrame(opText, []byte("\r\nCodex: "+err.Error()+"\r\n")))
			_ = writeFrame(conn, wsCloseFrame(1011, "failed to start Codex"))
			return
		}
		if err := writeFrame(conn, wsFrame(opText, []byte("\x1b]id;"+session.id+"\x07"))); err != nil {
			_ = m.sessions.closeByID(user, session.id)
			return
		}
	}
	prev, err := session.attach(conn)
	if err != nil {
		return
	}
	if prev != nil {
		kickDetached(prev)
	}
	defer session.detach(conn)

	// 读取客户端帧：普通数据写入 PTY；resize/ping 为 OSC 控制消息不进 PTY。
	var msgBuf []byte
	for {
		op, fin, payload, rerr := wsReadFrame(brw)
		if rerr != nil {
			break
		}
		switch op {
		case opText, opBin, opCont:
			if op != opCont {
				msgBuf = msgBuf[:0]
			}
			// 分片累积同样要有上限：否则客户端用连续的 continuation 帧就能把
			// msgBuf 撑到内存耗尽（单帧上限在 wsReadFrame 里，这里管整条消息）。
			if len(msgBuf)+len(payload) > maxMessageBytes {
				logWarn("[terminal] message exceeds %d bytes, closing connection", maxMessageBytes)
				return
			}
			msgBuf = append(msgBuf, payload...)
			if !fin {
				continue
			}
			s := string(msgBuf)
			if strings.HasPrefix(s, "\x1b]resize;") && strings.HasSuffix(s, "\x07") {
				trimmed := strings.TrimSuffix(strings.TrimPrefix(s, "\x1b]resize;"), "\x07")
				parts := strings.Split(trimmed, ";")
				if len(parts) == 2 {
					cols, err1 := strconv.Atoi(parts[0])
					rows, err2 := strconv.Atoi(parts[1])
					if err1 == nil && err2 == nil && cols > 0 && cols <= 65535 && rows > 0 && rows <= 65535 {
						if err := session.resize(conn, uint16(cols), uint16(rows)); err != nil && !errors.Is(err, errNotOwner) {
							logWarn("[terminal] resize failed: %v", err)
						}
					}
				}
			} else if strings.HasPrefix(s, "\x1b]ping") && strings.HasSuffix(s, "\x07") {
				// 心跳：仅保持连接不被代理/NAT 空闲超时断开，不写入 PTY
			} else {
				if err := session.writeInput(conn, msgBuf); err != nil && !errors.Is(err, errNotOwner) {
					logWarn("[terminal] write input failed: %v", err)
				}
			}
		case opPing:
			if werr := writeFrame(conn, wsFrame(opPong, payload)); werr != nil {
				logWarn("[terminal] pong write failed: %v", werr)
				return
			}
		case opClose:
			return
		}
	}

}

// localeLang 决定 bash 会话的 LANG。中文输入依赖 UTF-8 多字节支持：
// 若 LANG 为空或非 UTF-8，回退到内建于 glibc 的 C.UTF-8。
func localeLang(s string) string {
	u := strings.ToUpper(s)
	if s != "" && (strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")) {
		return s
	}
	return "C.UTF-8"
}

// wsAccept computes the Sec-WebSocket-Accept value for a handshake key.
func wsAccept(key string) string {
	h := sha1.New()
	io.WriteString(h, key+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

const (
	// wsCloseTaken 是「会话被其他设备接管」的 WebSocket 关闭码（4000–4999 为应用自定
	// 区间）。前端据此提示「已在其他设备打开」，不自动重连（否则两台设备会互抢）。
	wsCloseTaken = 4001
	// oscDetached 是顶号通知（OSC 控制帧，不写入 PTY）。与 close 帧双保险：
	// 帧先到 → 立即提示；帧被代理吞掉时还能靠 close 码判定。
	oscDetached = "\x1b]detached\x07"

	opText  = 0x1
	opBin   = 0x2
	opCont  = 0x0
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xA
)

// wsFrame builds a single unmasked server->client frame.
func wsFrame(op byte, payload []byte) []byte {
	n := len(payload)
	var hdr []byte
	if n < 126 {
		hdr = []byte{0x80 | op, byte(n)}
	} else if n < 65536 {
		hdr = []byte{0x80 | op, 126, byte(n >> 8), byte(n)}
	} else {
		hdr = []byte{0x80 | op, 127}
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
	}
	return append(hdr, payload...)
}

// wsCloseFrame 构造服务端 close 帧：payload = 2 字节状态码 + UTF-8 reason。
func wsCloseFrame(code uint16, reason string) []byte {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	copy(payload[2:], reason)
	return wsFrame(opClose, payload)
}

// kickDetached 通知被顶掉的旧连接：先发 \x1b]detached\x07，再发 close(4001)，最后关闭。
// 写带 1s 超时：旧连接 TCP 缓冲可能已满，绝不能让这次写阻塞拖住新设备的挂载。
func kickDetached(c net.Conn) {
	_ = c.SetWriteDeadline(time.Now().Add(time.Second))
	_, _ = c.Write(wsFrame(opText, []byte(oscDetached)))
	_, _ = c.Write(wsCloseFrame(wsCloseTaken, "detached"))
	_ = c.Close()
}

// wsReadFrame reads a single client frame, unmasking the payload.
func wsReadFrame(r io.Reader) (byte, bool, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, false, nil, err
	}
	fin := h[0]&0x80 != 0
	op := h[0] & 0x0f
	masked := h[1]&0x80 != 0
	ln := uint64(h[1] & 0x7f)
	if ln == 126 {
		var e [2]byte
		if _, err := io.ReadFull(r, e[:]); err != nil {
			return 0, false, nil, err
		}
		ln = uint64(binary.BigEndian.Uint16(e[:]))
	} else if ln == 127 {
		var e [8]byte
		if _, err := io.ReadFull(r, e[:]); err != nil {
			return 0, false, nil, err
		}
		ln = binary.BigEndian.Uint64(e[:])
	}
	// 上限校验放在最前面（读完长度、还没读掩码/载荷、更没 make）：长度由客户端声明，
	// 64 位长度可到 TB 级，畸形帧根本不该继续处理。
	if ln > maxFrameBytes {
		return 0, false, nil, errFrameTooLarge
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, false, nil, err
		}
	}
	payload := make([]byte, ln)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, false, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, fin, payload, nil
}
