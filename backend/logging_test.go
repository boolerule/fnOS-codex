package main

import (
	"bytes"

	"os"
	"path/filepath"
	"strings"
	"testing"

	"unicode"
)

// captureLogs 把全局日志出口换成内存缓冲，返回缓冲与还原函数。
func captureLogs(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, file := &bytes.Buffer{}, &bytes.Buffer{}
	s := logs
	s.mu.Lock()
	oldStdout, oldFile, oldColor := s.stdout, s.file, s.color
	oldKey, oldMsg, oldLevel, oldRepeats := s.lastKey, s.lastMsg, s.lastLevel, s.repeats
	s.stdout, s.file, s.color = out, nil, false
	s.lastKey, s.lastMsg, s.lastLevel, s.repeats = "", "", levelInfo, 0
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.stdout, s.file, s.color = oldStdout, oldFile, oldColor
		s.lastKey, s.lastMsg, s.lastLevel, s.repeats = oldKey, oldMsg, oldLevel, oldRepeats
		s.mu.Unlock()
	})
	return out, file
}

// 等级标记：INFO/WARN/ERROR 分别落在日志行里，供控制台按等级着色。
func TestLogLevelTags(t *testing.T) {
	out, _ := captureLogs(t)
	logInfo("plain %d", 1)
	logWarn("careful %s", "x")
	logError("broken %v", "y")
	got := out.String()
	for _, want := range []string{"[INFO] plain 1", "[WARN] careful x", "[ERROR] broken y"} {
		if !strings.Contains(got, want) {
			t.Fatalf("日志缺少 %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasPrefix(line, "[Codex Console] ") {
			t.Fatalf("日志行缺少固定前缀: %q", line)
		}
	}
}

// 与 log.Printf 一致：字面量百分号写成 %%，输出为单个 %。
func TestLogPercentEscape(t *testing.T) {
	out, _ := captureLogs(t)
	logInfo("usage 100%% done")
	if got := out.String(); !strings.Contains(got, "usage 100% done") {
		t.Fatalf("百分号转义不符: %s", got)
	}
}

// 连续重复的同一行只输出一次，序列结束时补一条汇总行。
func TestLogRepeatSuppression(t *testing.T) {
	out, _ := captureLogs(t)
	for i := 0; i < 5; i++ {
		logInfo("terminal pid changed: tracked=%d -> live=%d", 1, 2)
	}
	logInfo("next message")
	got := out.String()
	if n := strings.Count(got, "terminal pid changed"); n != 2 { // 1 条正文 + 1 条汇总行回显
		t.Fatalf("重复行未被抑制（出现 %d 次）:\n%s", n, got)
	}
	if !strings.Contains(got, "(previous message repeated 4 times)") {
		t.Fatalf("缺少重复汇总行:\n%s", got)
	}
	if !strings.Contains(got, "[INFO] next message") {
		t.Fatalf("后续消息丢失:\n%s", got)
	}
}

// flushLog 在退出前补出被抑制重复的汇总行。
func TestFlushLogEmitsPendingSummary(t *testing.T) {
	out, _ := captureLogs(t)
	logInfo("same line")
	logInfo("same line")
	logInfo("same line")
	if strings.Contains(out.String(), "repeated") {
		t.Fatalf("汇总行不应在序列结束前出现:\n%s", out.String())
	}
	flushLog()
	if !strings.Contains(out.String(), "(previous message repeated 2 times)") {
		t.Fatalf("flushLog 未补出汇总行:\n%s", out.String())
	}
}

// 不同等级的同文本不算重复。
func TestLogRepeatIsLevelSensitive(t *testing.T) {
	out, _ := captureLogs(t)
	logInfo("same text")
	logWarn("same text")
	got := out.String()
	if strings.Contains(got, "repeated") {
		t.Fatalf("不同等级被误判为重复:\n%s", got)
	}
	if !strings.Contains(got, "[WARN] same text") {
		t.Fatalf("WARN 行丢失:\n%s", got)
	}
}

// dsh 子进程输出：原样透传（不加前缀、不做重复抑制），只有终端才加黄色。
func TestLogFileHasNoAnsi(t *testing.T) {
	out, file := captureLogs(t)
	s := logs
	s.mu.Lock()
	s.file = file
	s.color = true
	s.mu.Unlock()
	logWarn("careful")

	if strings.Contains(file.String(), "\x1b[") {
		t.Fatalf("日志文件含 ANSI 转义: %q", file.String())
	}
	if !strings.Contains(file.String(), "[WARN] careful") {
		t.Fatalf("日志文件缺少等级标记: %q", file.String())
	}
	if !strings.Contains(out.String(), "\x1b[33m") {
		t.Fatalf("终端未着色: %q", out.String())
	}
}

// --- 日志语言：消息文本与「会被日志打印的错误」都必须是英文（AGENTS 规则 7）---

// 日志调用里的字面量不许出现中文。变量里的中文拦不住（见下一条测试），
// 但这条能挡住「新增日志行直接写中文」。
func TestNoChineseInLogCallText(t *testing.T) {
	logCalls := []string{"logInfo(", "logWarn(", "logError("}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, call := range logCalls {
			for at := 0; ; {
				idx := strings.Index(src[at:], call)
				if idx < 0 {
					break
				}
				start := at + idx
				at = start + len(call)
				// 取整个调用的实参文本（按括号配对），跨行也算
				end := matchCallEnd(src, start+len(call)-1)
				if end < 0 {
					continue
				}
				args := src[at:end]
				if !isASCII(args) {
					line := strings.Count(src[:start], "\n") + 1
					t.Errorf("%s:%d 日志调用的实参含非 ASCII 字符（日志一律英文）:\n\t%s",
						file, line, strings.Join(strings.Fields(args), " "))
				}
			}
		}
	}
}

// matchCallEnd 返回与 open 位置的 "(" 配对的 ")" 的下标；找不到返回 -1。
func matchCallEnd(src string, open int) int {
	depth := 0
	inStr := byte(0)
	for i := open; i < len(src); i++ {
		c := src[i]
		if inStr != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inStr = c
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// isASCII 报告字符串是否只含 ASCII 字符。
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > unicode.MaxASCII {
			return false
		}
	}
	return true
}
