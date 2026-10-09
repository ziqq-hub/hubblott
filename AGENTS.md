# AGENTS.md

## What this is
Single Go package at the repo root (`package main`):
- `matrix.go` — the 3x3 matrix engine: `Layers`×`Nodes` channel mesh with a worker pool per node (layer 0 ingestion, layer 1 execution, layer 2 state routing) and a lock-free `processed` counter.
- `main.go` — HTTP front door (`:8080`): `GET /healthz`, `POST /v1/matrix/ingest` (one transaction), `POST /v1/matrix/burst` (`?count=`, default 1000, max 50000), `GET /v1/matrix/telemetry`, plus graceful shutdown that stops the server before `MatrixRouter.Stop()`.
- `burst.go` — runs a burst (push `count` transactions, wait for the mesh to settle, measure duration/throughput) and records the outcome.
- `sheets.go` — appends one row per completed burst to Google Sheets with a service account key; stdlib only (signs its own RS256 JWT, exchanges it for an access token, caches the token until ~1 min before expiry). No `google.golang.org/api` dependency.
- `ui.go` + `web/dashboard.html` — real-time dashboard at `/`: polls telemetry once a second for the processed count, current tx/s, peak and a 60-sample chart. "Run burst (1,000)" calls the burst endpoint and prints the measured result; "Send 1 transaction" posts a single ingest. The HTML is embedded with `go:embed`, so editing it triggers an air rebuild (air's default `include_ext` covers `html`).

## Running it here
```
docker compose -f docker-compose.base44.yml up -d --build
```
Host port **3000** is mapped to the container's `8080` (the preview entry point).

## Google Sheets history (optional)
- Secrets come from the platform at `/run/base44/app.env`, wired in compose as the only `env_file:` entry (kept last so the user's values win). Read with `os.Getenv`, never via compose `environment:`.
- `GOOGLE_SHEET_ID` — the ID from the sheet URL; `GOOGLE_SERVICE_ACCOUNT_JSON` — the service account key JSON, either raw or base64-encoded (base64 is safer through an env file, since the key contains newlines); optional `GOOGLE_SHEET_TAB` (default `Sheet1`).
- The sheet must be shared with the key's `client_email` as **Editor**, and the Sheets API enabled on the project.
- Row appended to `<tab>!A:E` with `INSERT_ROWS`: `timestamp (RFC3339 UTC)`, `transactions`, `duration_ms`, `throughput_tx_s`, `total_processed`. **Header row is not written** — add it once by hand.
- Without the secrets the engine still boots and runs; a burst reports `"recorded": false` plus the reason, and logs `[SHEETS] burst history disabled: …`.

## Findings worth knowing
- **The engine itself needs no credentials** — the mesh, its counters and the ledger commit are all in-process. Google Sheets is the only external service, and only for burst history.
- **`go.mod` was added** (`module hubblott`). Without it `go build .` / `go run .` fail outside GOPATH, which also breaks the live-reload command.
- **Live reload uses `air`** (installed in `Dockerfile.base44`). Two quirks, both verified in this image:
  - air 1.61.4 ignores `-c <config>` / `.air.toml` here, so the build settings are passed as CLI flags in `docker-compose.base44.yml`.
  - `-tmp_dir` must be a single level under an existing directory (`/tmp/hubblott-air`): air uses `os.Mkdir`, not `os.MkdirAll`, and exits if the parent is missing.
  - Build artifacts go to `/tmp/hubblott-air`, so nothing is written into the repo working tree.
- No host/origin allowlisting is needed: `net/http.ServeMux` does not check `Host`/`Origin`, and there is no CORS, cookie or session auth. `BASE44_PREVIEW_MODE` is passed through but nothing gates on it.
- The Compose healthcheck probes `GET /healthz` with the image's own `curl`.
- `MatrixRouter.Stop()` closes the mesh channels after `wg.Wait()`, so it must not overlap in-flight `RouteIngestion` calls — hence `srv.Shutdown` runs first in `main`.
- Routing is `tx.ID % Nodes`, so every transaction must carry a non-zero `ID`; the HTTP handlers assign one from an atomic counter.
- A burst waits for `TotalProcessed` to reach `before+count`, with a 30 s cap, so a stalled mesh still returns a result instead of hanging.

## Verifying it works
```
curl -i http://localhost:3000/healthz                       # 200 ok
curl -i -X POST http://localhost:3000/v1/matrix/ingest      # 202 {"tx_id":N,"matrix":"3x3","total_processed":...}
curl -i -X POST 'http://localhost:3000/v1/matrix/burst?count=500'
curl -i http://localhost:3000/v1/matrix/telemetry           # {"matrix":"3x3","processed":N}
```
`processed` in the ingest response can lag by one request because routing is asynchronous — the telemetry endpoint is the accurate read. There is no test suite in the repo.
