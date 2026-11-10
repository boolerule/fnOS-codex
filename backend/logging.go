package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type logLevel int

const (
	levelInfo logLevel = iota
	levelWarn
	levelError
)

// tag 返回写入日志行的等级标记（控制台日志页据此着色）。
func (l logLevel) tag() string {
	return "[" + l.name() + "]"
}

func (l logLevel) name() string {
	switch l {
	case levelWarn:
		return "WARN"
	case levelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// ansi 返回该等级在终端上的颜色；INFO 不着色（终端默认前景色即白色）。
func (l logLevel) ansi() string {
	switch l {
	case levelWarn:
		return ansiYellow
	case levelError:
		return ansiRed
	default:
		return ""
	}
}

const (
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiReset  = "\x1b[0m"
	// repeatMsgMax 是重复汇总行回显原消息的长度上限（按 rune 截断）。
	repeatMsgMax = 120
)

// logSink 是全局日志状态。各等级日志共用一把锁，
// 避免两个来源交叉写坏同一行。
type logSink struct {
	mu     sync.Mutex
	stdout io.Writer
	file   io.Writer // 日志文件（未配置时为 nil）
	color  bool      // stdout 是终端时用 ANSI 着色

	lastKey   string   // 上一条 harness 日志的「等级+正文」
	lastMsg   string   // 上一条 harness 日志的正文（汇总行回显）
	lastLevel logLevel // 上一条 harness 日志的等级
	repeats   int      // 被抑制的连续重复条数
}

var logs = &logSink{stdout: os.Stdout, color: stdoutIsTerminal()}

// stdoutIsTerminal 判断 stdout 是否终端：只有终端才写 ANSI 颜色，
// 平台把 stdout 重定向到日志文件时不留下转义序列。
func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// logAt 写入一条 harness 日志。语义与 log.Printf 一致：消息里的字面量百分号
// 需写成 %%（这样 go vet 的 printf 检查能挡住漏参的调用）。
func logAt(level logLevel, format string, args ...interface{}) {
	logs.write(level, fmt.Sprintf(format, args...))
}

// logInfo 记录普通状态变化（白）。
func logInfo(format string, args ...interface{}) { logAt(levelInfo, format, args...) }

// logWarn 记录可恢复的异常/风险（黄）。
func logWarn(format string, args ...interface{}) { logAt(levelWarn, format, args...) }

// logError 记录失败（红）。
func logError(format string, args ...interface{}) { logAt(levelError, format, args...) }

// write 输出一条日志；与上一条完全相同（等级+正文）时只计数不输出，
// 序列结束时补一条汇总行。
func (s *logSink) write(level logLevel, msg string) {
	key := level.tag() + " " + msg
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == s.lastKey {
		s.repeats++
		return
	}
	if s.repeats > 0 {
		s.emitLocked(s.lastLevel, repeatSummary(s.lastMsg, s.repeats))
	}
	s.lastKey, s.lastMsg, s.lastLevel, s.repeats = key, msg, level, 0
	s.emitLocked(level, msg)
}

// repeatSummary 生成被抑制重复的汇总行正文。
func repeatSummary(msg string, n int) string {
	r := []rune(msg)
	if len(r) > repeatMsgMax {
		msg = string(r[:repeatMsgMax]) + "…"
	}
	return fmt.Sprintf("(previous message repeated %d times) %s", n, msg)
}

// emitLocked 按固定格式写出单行日志（调用方需持有 s.mu）。
func (s *logSink) emitLocked(level logLevel, msg string) {
	line := fmt.Sprintf("[Codex Console] %s %s %s\n", time.Now().Format("2006/01/02 15:04:05"), level.tag(), msg)
	if s.stdout != nil {
		if s.color && level.ansi() != "" {
			io.WriteString(s.stdout, level.ansi()+strings.TrimSuffix(line, "\n")+ansiReset+"\n")
		} else {
			io.WriteString(s.stdout, line)
		}
	}
	if s.file != nil {
		io.WriteString(s.file, line)
	}
}

// flushLog 在退出前补出被抑制重复的汇总行（否则计数会丢失）。
func flushLog() {
	s := logs
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repeats > 0 {
		s.emitLocked(s.lastLevel, repeatSummary(s.lastMsg, s.repeats))
		s.repeats = 0
	}
}
