# AGENTS.md

## What this is
Single Go package at the repo root (`package main`):
- `matrix.go` — the 3x3 matrix engine: `Layers`×`Nodes` channel mesh with a worker pool per node (layer 0 ingestion, layer 1 execution, layer 2 state routing) and a lock-free `processed` counter.
- `main.go` — HTTP front door (`:8080`): `GET /healthz`, `POST /v1/matrix/ingest` (routes one transaction into layer 0), `GET /v1/matrix/telemetry` (live processed count), plus graceful shutdown that stops the server before `MatrixRouter.Stop()`.
- `ui.go` + `web/dashboard.html` — real-time dashboard at `/`: polls `GET /v1/matrix/telemetry` once a second to show the processed count, current tx/s, peak and a 60-sample throughput chart, plus buttons that POST to the ingest endpoint. The HTML is embedded with `go:embed`, so editing it triggers an air rebuild (air's default `include_ext` covers `html`).

## Findings worth knowing (continued)
- The dashboard samples telemetry over `fetch` polling — there is no websocket/SSE, and the server keeps no history; rates are derived client-side from consecutive counts. `/` is served with `Cache-Control: no-store` so the preview never renders a stale page after a rebuild.


## Running it here
```
docker compose -f docker-compose.base44.yml up -d --build
```
Host port **3000** is mapped to the container's `8080` (the preview entry point).

## Findings worth knowing
- **No external services, no credentials, no database.** The mesh and its counters are in-process; the ledger commit step is a counter increment. Nothing needs a secret value.
- **`go.mod` was added** (`module hubblott`). Without it `go build .` / `go run .` fail outside GOPATH, which also breaks the live-reload command.
- **Live reload uses `air`** (installed in `Dockerfile.base44`). Two quirks, both verified in this image:
  - air 1.61.4 ignores `-c <config>` / `.air.toml` here, so the build settings are passed as CLI flags in `docker-compose.base44.yml`.
  - `-tmp_dir` must be a single level under an existing directory (`/tmp/hubblott-air`): air uses `os.Mkdir`, not `os.MkdirAll`, and exits if the parent is missing.
  - Build artifacts go to `/tmp/hubblott-air`, so nothing is written into the repo working tree.
- No host/origin allowlisting is needed: `net/http.ServeMux` does not check `Host`/`Origin`, and there is no CORS, cookie or session auth. `BASE44_PREVIEW_MODE` is passed through but nothing gates on it.
- The Compose healthcheck probes `GET /healthz` with the image's own `curl`.
- `MatrixRouter.Stop()` closes the mesh channels after `wg.Wait()`, so it must not overlap in-flight `RouteIngestion` calls — hence `srv.Shutdown` runs first in `main`.
- Routing is `tx.ID % Nodes`, so every transaction must carry a non-zero `ID`; the HTTP handler assigns one from an atomic counter.

## Verifying it works
```
curl -i http://localhost:3000/healthz                       # 200 ok
curl -i -X POST http://localhost:3000/v1/matrix/ingest      # 202 {"tx_id":N,"matrix":"3x3","total_processed":...}
curl -i http://localhost:3000/v1/matrix/telemetry           # {"matrix":"3x3","processed":N}
```
`processed` in the ingest response can lag by one request because routing is asynchronous — the telemetry endpoint is the accurate read. There is no test suite in the repo.
