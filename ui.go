package main

import "net/http"

// statusPage is a minimal, read-only landing page served at "/" so the running
// engine can be exercised from a browser. It is purely presentational: all
// transaction processing still goes through /v1/matrix/ingest.
const statusPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Hubblott 3x3 Matrix Execution Engine</title>
<style>
  :root { color-scheme: dark; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         background: #0d1117; color: #c9d1d9;
         font: 15px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace; }
  main { width: min(620px, 90vw); padding: 32px; border: 1px solid #30363d;
         border-radius: 10px; background: #161b22; }
  h1 { margin: 0 0 4px; font-size: 17px; color: #e6edf3; }
  .sub { margin: 0 0 20px; font-size: 12px; color: #8b949e; }
  p { margin: 0 0 8px; }
  code { padding: 2px 6px; border-radius: 4px; background: #0d1117; color: #79c0ff; }
  button { margin-top: 14px; padding: 10px 16px; border: 1px solid #30363d; border-radius: 6px;
           background: #238636; color: #fff; font: inherit; cursor: pointer; }
  button:disabled { opacity: .6; cursor: progress; }
  pre { margin: 14px 0 0; min-height: 40px; padding: 12px; border: 1px solid #30363d;
        border-radius: 6px; background: #0d1117; color: #7ee787; overflow: auto; }
</style>
</head>
<body>
<main>
  <h1>Hubblott 3x3 Matrix Execution Engine</h1>
  <p class="sub">Tier&nbsp;1 routing &rarr; Tier&nbsp;2 optimisation &rarr; Tier&nbsp;3 vault settlement</p>
  <p>Health <code>GET /healthz</code></p>
  <p>Ingest <code>POST /v1/matrix/ingest</code></p>
  <button id="submit">Submit test transaction</button>
  <pre id="out">idle</pre>
</main>
<script>
  const out = document.getElementById('out');
  const button = document.getElementById('submit');
  button.addEventListener('click', async () => {
    button.disabled = true;
    out.textContent = 'submitting...';
    try {
      const res = await fetch('/v1/matrix/ingest', { method: 'POST' });
      out.textContent = res.status + ' ' + JSON.stringify(await res.json());
    } catch (err) {
      out.textContent = 'request failed: ' + err;
    } finally {
      button.disabled = false;
    }
  });
</script>
</body>
</html>
`

// registerUI serves the browser-facing landing page on the engine's mux.
func registerUI(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(statusPage))
	})
}
