package main

import (
	_ "embed"
	"net/http"
)

//go:embed web/dashboard.html
var dashboardHTML []byte

// registerUI serves the real-time matrix dashboard on the engine's mux.
func registerUI(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(dashboardHTML)
	})
}
