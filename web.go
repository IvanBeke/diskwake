package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

func startWebUI(stop <-chan struct{}, logger *log.Logger, addr string, state *runtimeState) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, state.statusSnapshot())
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, state.configSnapshot())
	})
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, state.logsSnapshot())
	})
	mux.HandleFunc("/api/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		handleLogStream(w, r, state)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", addr, err)
	}

	go func() {
		<-stop
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("web ui server stopped with error: %v", err)
		}
	}()

	logger.Printf("web ui listening on http://0.0.0.0%s", normalizeAddrForLog(addr))
	return nil
}

func normalizeAddrForLog(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, ":") {
		return trimmed
	}
	host, port, err := net.SplitHostPort(trimmed)
	if err == nil {
		if host == "" {
			return ":" + port
		}
		return ":" + port
	}
	return trimmed
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func handleLogStream(w http.ResponseWriter, r *http.Request, state *runtimeState) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	snapshot := state.logsSnapshot()
	if len(snapshot.Lines) > 0 {
		for _, line := range snapshot.Lines {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", escapeSSEData(line))
		}
		flusher.Flush()
	}

	subID, ch := state.subscribeLogs()
	defer state.unsubscribeLogs(subID)

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case line, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", escapeSSEData(line))
			flusher.Flush()
		}
	}
}

func escapeSSEData(s string) string {
	// A log line should normally be one line already, but SSE data requires newline folding.
	return strings.ReplaceAll(s, "\n", "\\n")
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>diskwake</title>
  <style>
    :root {
      --bg: #f5f4ef;
      --panel: #fffefb;
      --ink: #1d2228;
      --muted: #5f6a74;
      --line: #d8d4c9;
      --ok: #1f7a3a;
      --warn: #8a6f00;
      --err: #a12727;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      color: var(--ink);
      font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
      background:
        radial-gradient(circle at top right, #ece6d9 0%, transparent 38%),
        radial-gradient(circle at bottom left, #e8edf0 0%, transparent 35%),
        var(--bg);
    }
    .shell {
      max-width: 1200px;
      margin: 20px auto;
      padding: 0 12px 24px;
      display: grid;
      gap: 12px;
    }
    .panel {
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 8px;
      padding: 12px;
      box-shadow: 0 1px 0 #e6e2d8;
    }
    .header {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 12px;
    }
    h1, h2 { margin: 0; }
    h1 {
      font-size: 1.2rem;
      letter-spacing: 0.02em;
      text-transform: uppercase;
      font-family: "IBM Plex Mono", "Courier New", monospace;
    }
    h2 { font-size: 1rem; margin-bottom: 10px; }
    .muted { color: var(--muted); }
    .status-grid {
      display: grid;
      gap: 10px;
      grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    }
    .card {
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px;
      background: #fff;
    }
    .row {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      margin-top: 4px;
      font-size: 0.92rem;
      font-family: "IBM Plex Mono", "Courier New", monospace;
    }
    .pill {
      display: inline-block;
      border-radius: 999px;
      padding: 2px 8px;
      font-size: 0.78rem;
      border: 1px solid;
      font-family: "IBM Plex Mono", "Courier New", monospace;
    }
    .pill-ok { color: var(--ok); border-color: #80b993; background: #eef8f1; }
    .pill-warn { color: var(--warn); border-color: #d6be5b; background: #fff8dd; }
    .pill-err { color: var(--err); border-color: #dea2a2; background: #fff1f1; }
    pre {
      margin: 0;
      overflow: auto;
      background: #faf9f6;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px;
      font-size: 0.88rem;
      line-height: 1.35;
      font-family: "IBM Plex Mono", "Courier New", monospace;
      white-space: pre;
    }
    .logs {
      height: 280px;
      white-space: pre-wrap;
      overflow: auto;
      background: #111519;
      color: #d7dfeb;
      border: 1px solid #2a323b;
      border-radius: 6px;
      padding: 10px;
      font-family: "IBM Plex Mono", "Courier New", monospace;
      font-size: 0.84rem;
      line-height: 1.35;
    }
  </style>
</head>
<body>
  <main class="shell">
    <section class="panel header">
      <h1>diskwake</h1>
      <div id="clock" class="muted">loading time...</div>
    </section>

    <section class="panel">
      <h2>Status</h2>
      <div id="status-grid" class="status-grid"></div>
    </section>

    <section class="panel">
      <h2>Config (read-only)</h2>
      <div id="config-path" class="muted"></div>
      <pre id="config"></pre>
    </section>

    <section class="panel">
      <h2>Live Logs (current process)</h2>
      <div id="logs" class="logs"></div>
    </section>
  </main>

  <script>
    const statusGrid = document.getElementById('status-grid');
    const logsEl = document.getElementById('logs');
    const configEl = document.getElementById('config');
    const configPathEl = document.getElementById('config-path');
    const clockEl = document.getElementById('clock');

    function makePill(result, inWindow) {
      if (!inWindow) {
        return '<span class="pill pill-warn">OUTSIDE WINDOW</span>';
      }
      if (result === 'ok') {
        return '<span class="pill pill-ok">IN WINDOW / OK</span>';
      }
      if (result === 'error') {
        return '<span class="pill pill-err">IN WINDOW / ERROR</span>';
      }
      return '<span class="pill pill-warn">IN WINDOW</span>';
    }

    function renderStatus(data) {
      clockEl.textContent = 'Server TZ: ' + data.server_timezone + ' | ' + data.server_time;
      statusGrid.innerHTML = data.disks.map(d => {
        const inWindow = d.current_state === 'in window';
        return [
          '<article class="card">',
          '<strong>' + escapeHtml(d.name) + '</strong>',
          '<div style="margin-top:6px">' + makePill(d.last_read_result, inWindow) + '</div>',
          '<div class="row"><span>Device</span><span>' + escapeHtml(d.device) + '</span></div>',
          '<div class="row"><span>Interval</span><span>' + escapeHtml(d.interval) + '</span></div>',
          '<div class="row"><span>Windows</span><span>' + escapeHtml(d.windows.join(', ')) + '</span></div>',
          '<div class="row"><span>Last Tick</span><span>' + escapeHtml(d.last_tick_at) + '</span></div>',
          '<div class="row"><span>Last Read</span><span>' + escapeHtml(d.last_read_at) + '</span></div>',
          '<div class="row"><span>Result</span><span>' + escapeHtml(d.last_read_result) + '</span></div>',
          '<div class="row"><span>Error</span><span>' + escapeHtml(d.last_error || '-') + '</span></div>',
          '</article>'
        ].join('');
      }).join('');
    }

    function appendLogLine(line) {
      const nearBottom = logsEl.scrollTop + logsEl.clientHeight >= logsEl.scrollHeight - 24;
      logsEl.textContent += (logsEl.textContent ? '\n' : '') + line;
      if (nearBottom) {
        logsEl.scrollTop = logsEl.scrollHeight;
      }
    }

    function escapeHtml(str) {
      return String(str)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#39;');
    }

    async function loadConfig() {
      const r = await fetch('/api/config');
      if (!r.ok) return;
      const data = await r.json();
      configPathEl.textContent = data.path;
      configEl.textContent = data.content;
    }

    async function refreshStatus() {
      const r = await fetch('/api/status');
      if (!r.ok) return;
      const data = await r.json();
      renderStatus(data);
    }

    async function loadInitialLogs() {
      const r = await fetch('/api/logs');
      if (!r.ok) return;
      const data = await r.json();
      logsEl.textContent = data.lines.join('\n');
      logsEl.scrollTop = logsEl.scrollHeight;
    }

    function connectStream() {
      const es = new EventSource('/api/logs/stream');
      es.onmessage = (ev) => appendLogLine(ev.data);
      es.onerror = () => {
        es.close();
        setTimeout(connectStream, 2000);
      };
    }

    (async () => {
      await Promise.all([loadConfig(), refreshStatus(), loadInitialLogs()]);
      connectStream();
      setInterval(refreshStatus, 5000);
    })();
  </script>
</body>
</html>
`
