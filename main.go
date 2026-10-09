package main

import (
    "context"
    "crypto/ed25519"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "hash/fnv"
    "log"
    "net/http"
    "os"
    "os/signal"
    "runtime"
    "strings"
    "sync"
    "sync/atomic"
    "syscall"
    "time"
)

type StateCode uint8

const (
    T1S1 StateCode = 11
    T1S2 StateCode = 12
    T1S3 StateCode = 13
    T2S1 StateCode = 21
    T2S2 StateCode = 22
    T2S3 StateCode = 23
    T3S1 StateCode = 31
    T3S2 StateCode = 32
    T3S3 StateCode = 33
)

type Transaction struct {
    ID        string
    Payload   []byte
    Margin    float64
    State     StateCode
    Signature []byte
    Metadata  map[string]string
}

var txPool = sync.Pool{New: func() any {
    return &Transaction{
        Payload:   make([]byte, 0, 1024),
        Signature: make([]byte, 0, ed25519.SignatureSize),
        Metadata:  make(map[string]string),
    }
}}

const ShardCount = 256

type StateShard struct {
    sync.RWMutex
    store map[string]*Transaction
}

type ShardedStateStore struct {
    shards [ShardCount]*StateShard
}

func NewShardedStore() *ShardedStateStore {
    s := &ShardedStateStore{}
    for i := range s.shards {
        s.shards[i] = &StateShard{store: make(map[string]*Transaction)}
    }
    return s
}

func (s *ShardedStateStore) getShard(key string) *StateShard {
    h := fnv.New32a()
    _, _ = h.Write([]byte(key))
    return s.shards[h.Sum32()%ShardCount]
}

func (s *ShardedStateStore) UpsertState(tx *Transaction) {
    sh := s.getShard(tx.ID)
    sh.Lock()
    sh.store[tx.ID] = tx
    sh.Unlock()
}

type Engine struct {
    Store        *ShardedStateStore
    T1RouteChan  chan *Transaction
    T2OptChan    chan *Transaction
    T3VaultChan  chan *Transaction
    PublicKey    ed25519.PublicKey
    TotalSettled uint64
}

func NewEngine(bufferSize int, pubKey ed25519.PublicKey) *Engine {
    return &Engine{
        Store:       NewShardedStore(),
        T1RouteChan: make(chan *Transaction, bufferSize),
        T2OptChan:   make(chan *Transaction, bufferSize),
        T3VaultChan: make(chan *Transaction, bufferSize),
        PublicKey:   pubKey,
    }
}

func (e *Engine) HandleIngest(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    tx := txPool.Get().(*Transaction)
    tx.ID = fmt.Sprintf("TX-%d", time.Now().UnixNano())
    tx.State = T1S1
    tx.Payload = append(tx.Payload[:0], []byte(`{"vol":1500,"tier":1}`)...)
    e.Store.UpsertState(tx)

    select {
    case e.T1RouteChan <- tx:
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusAccepted)
        _ = json.NewEncoder(w).Encode(map[string]string{"tx_id": tx.ID, "matrix": "11"})
    default:
        e.recycleTx(tx)
        http.Error(w, "ingestion queue full", http.StatusTooManyRequests)
    }
}

func (e *Engine) RunTier1Workers(ctx context.Context, workers int) {
    for i := 0; i < workers; i++ {
        go func() {
            for {
                select {
                case <-ctx.Done():
                    return
                case tx := <-e.T1RouteChan:
                    if tx == nil {
                        return
                    }
                    tx.State = T1S2
                    e.Store.UpsertState(tx)

                    if strings.Contains(string(tx.Payload), `"tier":3`) {
                        select {
                        case e.T3VaultChan <- tx:
                        case <-ctx.Done():
                            e.recycleTx(tx)
                            return
                        }
                    } else {
                        select {
                        case e.T2OptChan <- tx:
                        case <-ctx.Done():
                            e.recycleTx(tx)
                            return
                        }
                    }
                }
            }
        }()
    }
}

func (e *Engine) RunTier2Workers(ctx context.Context, workers int) {
    for i := 0; i < workers; i++ {
        go func() {
            for {
                select {
                case <-ctx.Done():
                    return
                case tx := <-e.T2OptChan:
                    if tx == nil {
                        return
                    }
                    tx.State = T2S1
                    e.Store.UpsertState(tx)

                    tx.State = T2S2
                    tx.Margin = 0.045
                    e.Store.UpsertState(tx)

                    tx.State = T2S3
                    log.Printf("T2 settled tx=%s spread=%.3f", tx.ID, tx.Margin)
                    e.recycleTx(tx)
                }
            }
        }()
    }
}

func (e *Engine) RunTier3Workers(ctx context.Context, workers int) {
    for i := 0; i < workers; i++ {
        go func() {
            for {
                select {
                case <-ctx.Done():
                    return
                case tx := <-e.T3VaultChan:
                    if tx == nil {
                        return
                    }
                    tx.State = T3S1
                    e.Store.UpsertState(tx)
                    time.Sleep(5 * time.Millisecond)

                    tx.State = T3S2
                    e.Store.UpsertState(tx)

                    tx.State = T3S3
                    atomic.AddUint64(&e.TotalSettled, 1)
                    e.sanitizeAndWipe(tx)
                }
            }
        }()
    }
}

func (e *Engine) sanitizeAndWipe(tx *Transaction) {
    sum := sha256.Sum256(tx.Payload)
    for i := range tx.Payload {
        tx.Payload[i] = 0
    }
    for i := range tx.Signature {
        tx.Signature[i] = 0
    }
    for k := range tx.Metadata {
        delete(tx.Metadata, k)
    }
    log.Printf("[VAULT] settled tx=%s audit_hash=%s", tx.ID, hex.EncodeToString(sum[:]))
    e.recycleTx(tx)
}

func (e *Engine) recycleTx(tx *Transaction) {
    sh := e.Store.getShard(tx.ID)
    sh.Lock()
    delete(sh.store, tx.ID)
    sh.Unlock()

    tx.ID = ""
    tx.Payload = tx.Payload[:0]
    tx.Signature = tx.Signature[:0]
    tx.Margin = 0
    tx.State = 0
    txPool.Put(tx)
}

func main() {
    pub, _, _ := ed25519.GenerateKey(nil)
    e := NewEngine(50000, pub)

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    workers := runtime.NumCPU()
    e.RunTier1Workers(ctx, max(1, workers))
    e.RunTier2Workers(ctx, max(1, workers/2))
    e.RunTier3Workers(ctx, max(1, workers/4))

    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok\n"))
    })
    mux.HandleFunc("/v1/matrix/ingest", e.HandleIngest)
    registerUI(mux)

    server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
    go func() {
        log.Println("Hubblott listening on :8080")
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatal(err)
        }
    }()

    <-ctx.Done()
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer shutdownCancel()
    _ = server.Shutdown(shutdownCtx)
}

func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}
