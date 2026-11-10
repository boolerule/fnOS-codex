//go:build noterminal

package main

import "net/http"

const terminalFeature = false

type SessionManager struct{}

func NewSessionManager(*RuntimeEnv) *SessionManager                               { return nil }
func (m *SessionManager) CloseAll()                                               {}
func sessionDirFor(string) string                                                 { return "" }
func (m *AdminMux) terminalRoute(http.ResponseWriter, *http.Request, string) bool { return false }
