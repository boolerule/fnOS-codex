//go:build !noterminal

// terminal_routes.go —— 终端功能的 HTTP 入口（WebSocket 会话、会话管理）。
//
// 这些路由从 admin.go 的 buildHandler 里拆出来，由 terminalRoute 一处分发：
// 终端功能可在编译期整体剔除（`-tags noterminal`），无终端构建里同名方法恒返回 false
// （见 terminal_disabled.go），于是这几个路径连**字符串与 handler 都不会进入产物**。
// 新增终端相关接口时请加在这里，不要塞回 admin.go 的主 switch。

package main

import (
	"net/http"
	"strings"
)

// sessionInfo is the JSON summary of a live terminal session.
type sessionInfo struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Mode      string `json:"mode"`
	Created   string `json:"created"`
	Size      int64  `json:"size"`
	Exited    bool   `json:"exited"`
}

// terminalRoute 处理终端相关路由，返回 true 表示已响应（调用方直接返回）。
func (m *AdminMux) terminalRoute(w http.ResponseWriter, r *http.Request, p string) bool {
	// Only the trusted HTTP entry points can attach a console identity.
	if (p == "/terminal" || p == "/api/sessions" || p == "/api/session" || strings.HasPrefix(p, "/api/session/")) && consoleUser(r) == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return true
	}
	switch {
	case p == "/terminal" && isWebSocket(r):
		m.handleTerminal(w, r)
	case p == "/api/sessions" && r.Method == http.MethodGet:
		m.handleSessions(w, r)
	case p == "/api/session/history" && r.Method == http.MethodGet:
		m.handleSessionHistory(w, r)
	case p == "/api/session" && r.Method == http.MethodDelete:
		m.handleCloseSession(w, r)
	case p == "/api/session/clear" && r.Method == http.MethodPost:
		m.handleClearSession(w, r)
	default:
		return false
	}
	return true
}

// handleSessions 返回活动终端会话列表（供前端启动时恢复会话）。
func (m *AdminMux) handleSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{"ok": true, "sessions": m.sessions.list(consoleUser(r))})
}

// handleSessionHistory 读取指定会话的历史镜像内容。
func (m *AdminMux) handleSessionHistory(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, "missing session id", http.StatusBadRequest)
		return
	}
	data, err := m.sessions.history(consoleUser(r), id)
	if err != nil {
		writeErr(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": id, "size": len(data), "content": string(data)})
}

// handleCloseSession 关闭指定会话（终止进程 + 删除历史文件）。
func (m *AdminMux) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, "missing session id", http.StatusBadRequest)
		return
	}
	if err := m.sessions.closeByID(consoleUser(r), id); err != nil {
		writeErr(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": id})
}

// handleClearSession 清空指定会话的历史镜像文件（前端「清屏」同步）。
func (m *AdminMux) handleClearSession(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, "missing session id", http.StatusBadRequest)
		return
	}
	if err := m.sessions.clearHistory(consoleUser(r), id); err != nil {
		writeErr(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": id})
}

// isWebSocket 判断请求是不是一条 WebSocket 升级请求（终端会话走它）。
func isWebSocket(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	up := strings.ToLower(r.Header.Get("Upgrade"))
	return up == "websocket"
}
