package main

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(value)
}

func writeErr(w http.ResponseWriter, message string, status int) {
	writeErrU(w, status, "", message)
}

func writeErrU(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Code  string `json:"code,omitempty"`
	}{false, message, code})
}
