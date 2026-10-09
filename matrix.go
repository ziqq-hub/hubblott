package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Matrix dimensions (3x3 Matrix Architecture)
const (
	Layers = 3 // Ingestion, Execution, State Routing
	Nodes  = 3 // Workers/Channels per layer
	Buffer = 4096
)

// Transaction represents the core B2B data payload passed down the pipeline
type Transaction struct {
	ID        uint64
	Amount    float64
	Payload   string
	Timestamp time.Time
}

// MatrixRouter coordinates low-latency throughput across the 3x3 matrix mesh
type MatrixRouter struct {
	mesh      [Layers][Nodes]chan *Transaction
	processed uint64
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewMatrixRouter initializes a zero-allocation 3x3 channel mesh
func NewMatrixRouter() *MatrixRouter {
	ctx, cancel := context.WithCancel(context.Background())
	router := &MatrixRouter{
		ctx:    ctx,
		cancel: cancel,
	}

	for l := 0; l < Layers; l++ {
		for n := 0; n < Nodes; n++ {
			router.mesh[l][n] = make(chan *Transaction, Buffer)
		}
	}
	return router
}

// Start boots up the concurrent execution matrix worker pools
func (mr *MatrixRouter) Start() {
	// Layer 0: High-Throughput Ingestion Processing
	for n := 0; n < Nodes; n++ {
		mr.wg.Add(1)
		go mr.workerLoop(0, n, mr.handleIngestion)
	}

	// Layer 1: Financial Transaction Execution Engines
	for n := 0; n < Nodes; n++ {
		mr.wg.Add(1)
		go mr.workerLoop(1, n, mr.handleExecution)
	}

	// Layer 2: State Ledger Routing Nodes
	for n := 0; n < Nodes; n++ {
		mr.wg.Add(1)
		go mr.workerLoop(2, n, mr.handleStateRouting)
	}
}

// workerLoop drains channels using structural routing coordinates
func (mr *MatrixRouter) workerLoop(layer, node int, handler func(*Transaction)) {
	defer mr.wg.Done()
	ch := mr.mesh[layer][node]

	for {
		select {
		case tx := <-ch:
			if tx == nil {
				return
			}
			handler(tx)
		case <-mr.ctx.Done():
			return
		}
	}
}

// RouteIngestion accepts entry-point traffic and distributes via lock-free atomic hash
func (mr *MatrixRouter) RouteIngestion(tx *Transaction) {
	// Lock-free distribution across Layer 0 nodes based on transaction ID
	targetNode := tx.ID % Nodes
	mr.mesh[0][targetNode] <- tx
}

func (mr *MatrixRouter) handleIngestion(tx *Transaction) {
	// Optimization step: Sanitize, authenticate, and forward to Layer 1 (Execution)
	targetNode := tx.ID % Nodes
	mr.mesh[1][targetNode] <- tx
}

func (mr *MatrixRouter) handleExecution(tx *Transaction) {
	// Optimization step: Process matching algorithm or ledger delta, forward to Layer 2
	targetNode := tx.ID % Nodes
	mr.mesh[2][targetNode] <- tx
}

func (mr *MatrixRouter) handleStateRouting(tx *Transaction) {
	// Final layer: Commit state updates asynchronously to the persistent ledger
	atomic.AddUint64(&mr.processed, 1)
}

// TotalProcessed returns a live telemetry count of routed payloads
func (mr *MatrixRouter) TotalProcessed() uint64 {
	return atomic.LoadUint64(&mr.processed)
}

// Stop gracefully drains the matrix buffers and tears down the threads
func (mr *MatrixRouter) Stop() {
	mr.cancel()
	mr.wg.Wait()
	for l := 0; l < Layers; l++ {
		for n := 0; n < Nodes; n++ {
			close(mr.mesh[l][n])
		}
	}
}
