package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"agent/adb"
)

// newHealthHandler returns the /health endpoint handler. Exposed as a
// constructor so it can be tested with httptest without spinning up
// the whole server in main().
func newHealthHandler(mgr *adb.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Status  string `json:"status"`
			Devices int    `json:"devices"`
		}{Status: "ok", Devices: mgr.Count()}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("/health encode failed", "error", err)
		}
	}
}
