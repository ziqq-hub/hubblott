# AGENTS.md

## What this is
Single Go package at the repo root (`package main`):
- `main.go` — 3x3 matrix execution engine + HTTP server on `:8080` (workers for tier 1 routing, tier 2 optimisation, tier 3 vault settlement).
- `ui.go` — minimal read-only landing page served at `/`, with a button that POSTs a test transaction to `/v1/matrix/ingest`.

## Running it here
```
docker compose -f docker-compose.base44.yml up -d --build
```
Host port **3000** is mapped to the container's `8080` (the preview entry point).

## Findings worth knowing
- **No external services, no credentials, no database.** Everything is in-process: the ed25519 keypair is generated at startup and the vault is an in-memory sharded map. The README's "KMS-encrypted vault isolation" is not backed by any KMS client in the code, so no secret values are required.
- **The committed `main.go` did not compile.** `_, pub, _ := ed25519.GenerateKey(nil)` bound the *private* key and passed it to `NewEngine(..., ed25519.PublicKey)`. Fixed to `pub, _, _`.
- **`go.mod` was added** (`module hubblott`). Without it `go build .` / `go run .` fail outside GOPATH, which also breaks the live-reload command.
- **Live reload uses `air`** (installed in `Dockerfile.base44`). Two quirks, both verified in this image:
  - air 1.61.4 ignores `-c <config>` / `.air.toml` here, so the build settings are passed as CLI flags in `docker-compose.base44.yml`.
  - `-tmp_dir` must be a single level under an existing directory (`/tmp/hubblott-air`): air uses `os.Mkdir`, not `os.MkdirAll`, and exits if the parent is missing.
  - Build artifacts go to `/tmp/hubblott-air`, so nothing is written into the repo working tree.
- No host/origin allowlisting is needed: `net/http.ServeMux` does not check `Host`/`Origin`, and there is no CORS, cookie or session auth. `BASE44_PREVIEW_MODE` is passed through but nothing gates on it.
- The Compose healthcheck probes `GET /healthz` with the image's own `curl`.

## Verifying it works
```
curl -i http://localhost:3000/healthz              # 200 ok
curl -i -X POST http://localhost:3000/v1/matrix/ingest   # 202 {"tx_id":"TX-...","matrix":"11"}
```
The POST also produces engine logs: `T2 settled tx=... spread=0.045` (tier 2 path) and `[VAULT] settled tx=... audit_hash=...` (tier 3 path). There is no test suite in the repo.
