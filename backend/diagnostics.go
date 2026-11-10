package main

import (
	"sync"
	"time"
)

// Only fixed operational events can enter the browser log. Process output and
// RPC error text can contain credentials, chat contents and other users' paths.
var diagnosticEvents = map[string]struct {
	level   logLevel
	message string
}{
	"service_started":            {levelInfo, "Codex Console started."},
	"app_server_started":         {levelInfo, "Codex App Server started."},
	"app_server_start_failed":    {levelError, "Could not start Codex App Server. Check the selected Codex installation."},
	"app_server_exited":          {levelWarn, "Codex App Server exited."},
	"device_code_forbidden":      {levelWarn, "Device code request was refused (HTTP 403)."},
	"device_code_failed":         {levelWarn, "Device code request failed."},
	"device_code_issued":         {levelInfo, "Device code was issued. Waiting for authorization."},
	"browser_auth_started":       {levelInfo, "Browser sign-in started. Waiting for the callback."},
	"browser_auth_failed":        {levelWarn, "Could not start browser sign-in."},
	"browser_callback_rejected":  {levelWarn, "Browser sign-in callback could not be accepted."},
	"browser_callback_failed":    {levelWarn, "Codex could not complete the submitted browser callback."},
	"browser_callback_forwarded": {levelInfo, "Browser sign-in callback submitted to Codex."},
	"api_key_failed":             {levelWarn, "Codex rejected API key sign-in."},
	"api_key_saved":              {levelInfo, "Codex accepted the API key for this account."},
	"account_login_completed":    {levelInfo, "Codex confirmed account sign-in."},
	"account_login_failed":       {levelWarn, "Codex reported that account sign-in failed."},
}

type diagnosticEntry struct {
	user    string
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

type diagnosticLog struct {
	mu      sync.Mutex
	entries [200]diagnosticEntry
	next    int
	count   int
}

func (d *diagnosticLog) record(user, code string) {
	event, ok := diagnosticEvents[code]
	if !ok || (user == "") != (code == "service_started") {
		return
	}
	switch event.level {
	case levelWarn:
		logWarn("%s", event.message)
	case levelError:
		logError("%s", event.message)
	default:
		logInfo("%s", event.message)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries[d.next] = diagnosticEntry{user: user, Time: time.Now().UTC(), Level: event.level.name(), Code: code, Message: event.message}
	d.next = (d.next + 1) % len(d.entries)
	if d.count < len(d.entries) {
		d.count++
	}
}

func (d *diagnosticLog) snapshot(user string) []diagnosticEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]diagnosticEntry, 0)
	for i := 1; i <= d.count; i++ {
		row := d.entries[(d.next+len(d.entries)-i)%len(d.entries)]
		if row.user == "" || row.user == user {
			result = append(result, row)
		}
	}
	return result
}
