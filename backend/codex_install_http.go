package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (m *AdminMux) handleCodexInstall(w http.ResponseWriter, r *http.Request) {
	// Older clients send no body. Only an omitted version selects the default.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	var req *struct {
		Version    json.RawMessage `json:"version"`
		SystemPath json.RawMessage `json:"systemPath"`
	}
	target := "latest"
	if err := dec.Decode(&req); !errors.Is(err, io.EOF) {
		if err != nil || req == nil || dec.Decode(new(json.RawMessage)) != io.EOF {
			writeErrU(w, http.StatusBadRequest, "err_codex_install_request", "安装请求格式错误")
			return
		}
		if req.Version != nil {
			var version string
			if err := json.Unmarshal(req.Version, &version); err != nil {
				writeErrU(w, http.StatusBadRequest, "err_codex_version_invalid", errInvalidCodexVersion.Error())
				return
			}
			target = version
		}
	}
	var err error
	if req != nil && req.SystemPath != nil {
		var path string
		if json.Unmarshal(req.SystemPath, &path) != nil || path == "" {
			writeErrU(w, http.StatusBadRequest, "err_codex_install_request", "安装请求格式错误")
			return
		}
		err = m.codex.selectSystem(target, path)
	} else {
		err = m.codex.Install(target)
	}
	if err != nil {
		if errors.Is(err, errInvalidCodexVersion) {
			writeErrU(w, http.StatusBadRequest, "err_codex_version_invalid", err.Error())
			return
		}
		writeErr(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
