package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	defaultBurst = 1000
	maxBurst     = 50000
	burstDrain   = 30 * time.Second
)

// burstResult is the record of one completed burst; the same values are appended
// to the spreadsheet when the credentials are configured.
type burstResult struct {
	StartedAt      time.Time `json:"started_at"`
	Count          uint64    `json:"count"`
	DurationMS     int64     `json:"duration_ms"`
	ThroughputTxS  float64   `json:"throughput_tx_s"`
	TotalProcessed uint64    `json:"total_processed"`
	Recorded       bool      `json:"recorded"`
	Reason         string    `json:"reason,omitempty"`
}

// newTransaction numbers and builds the next transaction for the mesh.
func (s *server) newTransaction() *Transaction {
	id := atomic.AddUint64(&s.nextID, 1)
	return &Transaction{
		ID:        id,
		Amount:    15000.75 * float64(id),
		Payload:   "B2B_INFRA_ROUTING_DATA_STREAM",
		Timestamp: time.Now(),
	}
}

// handleBurst runs a burst through the mesh and records the outcome: POST /v1/matrix/burst?count=N
func (s *server) handleBurst(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	count := uint64(defaultBurst)
	if raw := r.URL.Query().Get("count"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || parsed == 0 {
			http.Error(w, "invalid count", http.StatusBadRequest)
			return
		}
		count = min(parsed, maxBurst)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.runBurst(count))
}

// runBurst pushes count transactions in, waits for the mesh to settle, and
// records the elapsed time and throughput.
func (s *server) runBurst(count uint64) burstResult {
	started := time.Now()
	before := s.router.TotalProcessed()

	for i := uint64(0); i < count; i++ {
		s.router.RouteIngestion(s.newTransaction())
	}

	deadline := started.Add(burstDrain)
	for s.router.TotalProcessed() < before+count && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	elapsed := time.Since(started)
	result := burstResult{
		StartedAt:      started.UTC(),
		Count:          count,
		DurationMS:     elapsed.Milliseconds(),
		ThroughputTxS:  float64(count) / elapsed.Seconds(),
		TotalProcessed: s.router.TotalProcessed(),
	}

	if err := s.sheets.Append(result); err != nil {
		result.Reason = err.Error()
		log.Printf("[SHEETS] burst of %d not recorded: %v", count, err)
	} else {
		result.Recorded = true
		log.Printf("[SHEETS] recorded burst of %d (%.1f tx/s)", count, result.ThroughputTxS)
	}
	return result
}
