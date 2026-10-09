package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// server exposes the matrix mesh over HTTP; it owns no engine state of its own.
type server struct {
	router *MatrixRouter
	nextID uint64
}

// handleIngest feeds one transaction into Layer 0 of the matrix.
func (s *server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := atomic.AddUint64(&s.nextID, 1)
	tx := &Transaction{
		ID:        id,
		Amount:    15000.75 * float64(id),
		Payload:   "B2B_INFRA_ROUTING_DATA_STREAM",
		Timestamp: time.Now(),
	}
	s.router.RouteIngestion(tx)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tx_id":           tx.ID,
		"matrix":          "3x3",
		"total_processed": s.router.TotalProcessed(),
	})
}

func main() {
	router := NewMatrixRouter()
	router.Start()
	defer router.Stop()

	s := &server{router: router}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/v1/matrix/ingest", s.handleIngest)
	mux.HandleFunc("/v1/matrix/telemetry", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"matrix":    "3x3",
			"processed": router.TotalProcessed(),
		})
	})
	registerUI(mux)

	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Println("Hubblott listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
