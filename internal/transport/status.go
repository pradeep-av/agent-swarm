package transport

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/pradeep-av/agent-swarm/internal/registry"
)

// StatusHandler serves the agent status dashboard and JSON API.
// The /status JSON endpoint requires the same Bearer token as the Hub (if configured).
type StatusHandler struct {
	registry *registry.Registry
	token    string
}

// NewStatusHandler creates a StatusHandler.
func NewStatusHandler(reg *registry.Registry, token string) *StatusHandler {
	return &StatusHandler{registry: reg, token: token}
}

type workerStatus struct {
	ID             string    `json:"id"`
	Hostname       string    `json:"hostname"`
	Capabilities   []string  `json:"capabilities"`
	Models         []string  `json:"models"`
	Labels         []string  `json:"labels"`
	Busy           bool      `json:"busy"`
	CurrentSession string    `json:"current_session,omitempty"`
	LastSeen       time.Time `json:"last_seen"`
}

type statusResponse struct {
	Workers   []workerStatus `json:"workers"`
	Total     int            `json:"total"`
	BusyCount int            `json:"busy"`
	IdleCount int            `json:"idle"`
}

func (h *StatusHandler) authorized(r *http.Request) bool {
	if h.token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+h.token
}

// ServeHTTP routes GET /status → JSON, everything else → HTML dashboard.
func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/status" {
		if !h.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.serveJSON(w, r)
		return
	}
	// HTML dashboard — no auth header check; token is handled client-side via localStorage
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

func (h *StatusHandler) serveJSON(w http.ResponseWriter, r *http.Request) {
	workers := h.registry.All()
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].ID < workers[j].ID
	})

	resp := statusResponse{Workers: make([]workerStatus, 0, len(workers))}
	for _, w := range workers {
		ws := workerStatus{
			ID:             w.ID,
			Hostname:       w.Hostname,
			Capabilities:   w.Capabilities,
			Models:         w.Models,
			Labels:         w.Labels,
			Busy:           w.Busy,
			CurrentSession: w.CurrentSession,
			LastSeen:       w.LastSeen,
		}
		resp.Workers = append(resp.Workers, ws)
		resp.Total++
		if w.Busy {
			resp.BusyCount++
		} else {
			resp.IdleCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Swarm Status</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: ui-monospace, "Cascadia Code", monospace; background: #0d1117; color: #e6edf3; padding: 2rem; }
  h1 { font-size: 1.4rem; font-weight: 600; margin-bottom: 0.25rem; }
  .subtitle { color: #8b949e; font-size: 0.85rem; margin-bottom: 1.5rem; }
  .stats { display: flex; gap: 1rem; margin-bottom: 1.5rem; }
  .stat { background: #161b22; border: 1px solid #30363d; border-radius: 6px; padding: 0.75rem 1.25rem; min-width: 100px; }
  .stat-label { font-size: 0.75rem; color: #8b949e; text-transform: uppercase; letter-spacing: 0.05em; }
  .stat-value { font-size: 1.6rem; font-weight: 700; margin-top: 0.15rem; }
  .stat-value.green { color: #3fb950; }
  .stat-value.yellow { color: #d29922; }
  table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  thead th { text-align: left; padding: 0.5rem 0.75rem; color: #8b949e; font-weight: 500;
             border-bottom: 1px solid #30363d; text-transform: uppercase; font-size: 0.72rem; letter-spacing: 0.05em; }
  tbody tr { border-bottom: 1px solid #21262d; transition: background 0.1s; }
  tbody tr:hover { background: #161b22; }
  td { padding: 0.6rem 0.75rem; vertical-align: middle; }
  .badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 12px; font-size: 0.72rem; font-weight: 500; margin: 0.1rem 0.15rem; }
  .badge-cap  { background: #1f3a5f; color: #79c0ff; }
  .badge-model{ background: #2d1f3a; color: #d2a8ff; }
  .badge-label{ background: #1f3a2d; color: #7ee787; }
  .status-idle { color: #3fb950; font-weight: 600; }
  .status-busy { color: #d29922; font-weight: 600; }
  .lastseen { color: #8b949e; }
  .error-bar { background: #3d1e1e; border: 1px solid #6e3030; color: #f97171; padding: 0.6rem 1rem;
               border-radius: 6px; margin-bottom: 1rem; display: none; }
  .refresh-note { color: #484f58; font-size: 0.75rem; margin-top: 1rem; }
  #token-modal { position: fixed; inset: 0; background: rgba(0,0,0,0.7); display: flex;
                 align-items: center; justify-content: center; z-index: 10; }
  .modal-box { background: #161b22; border: 1px solid #30363d; border-radius: 10px;
               padding: 2rem; width: 360px; }
  .modal-box h2 { font-size: 1rem; margin-bottom: 0.5rem; }
  .modal-box p  { color: #8b949e; font-size: 0.82rem; margin-bottom: 1rem; }
  .modal-box input { width: 100%; background: #0d1117; border: 1px solid #30363d; color: #e6edf3;
                     border-radius: 6px; padding: 0.5rem 0.75rem; font-family: inherit; font-size: 0.9rem; }
  .modal-box button { margin-top: 0.75rem; width: 100%; background: #238636; color: #fff; border: none;
                      border-radius: 6px; padding: 0.55rem; font-size: 0.9rem; cursor: pointer; font-family: inherit; }
  .modal-box button:hover { background: #2ea043; }
</style>
</head>
<body>

<div id="token-modal">
  <div class="modal-box">
    <h2>Swarm Dashboard</h2>
    <p>Enter the swarm pre-shared token to continue.</p>
    <input type="password" id="token-input" placeholder="Bearer token" autocomplete="off">
    <button onclick="saveToken()">Connect</button>
  </div>
</div>

<h1>Swarm Status</h1>
<p class="subtitle" id="subtitle">Connecting…</p>

<div class="stats">
  <div class="stat"><div class="stat-label">Total</div><div class="stat-value" id="s-total">—</div></div>
  <div class="stat"><div class="stat-label">Idle</div><div class="stat-value green" id="s-idle">—</div></div>
  <div class="stat"><div class="stat-label">Busy</div><div class="stat-value yellow" id="s-busy">—</div></div>
</div>

<div class="error-bar" id="error-bar"></div>

<table>
  <thead>
    <tr>
      <th>Worker ID</th>
      <th>Hostname</th>
      <th>Capabilities</th>
      <th>Models</th>
      <th>Labels</th>
      <th>Status</th>
      <th>Last Seen</th>
    </tr>
  </thead>
  <tbody id="tbody"></tbody>
</table>
<p class="refresh-note">Auto-refreshes every 5 seconds.</p>

<script>
const STORAGE_KEY = 'swarm_token';
let token = localStorage.getItem(STORAGE_KEY);

function saveToken() {
  const v = document.getElementById('token-input').value.trim();
  if (!v) return;
  localStorage.setItem(STORAGE_KEY, v);
  token = v;
  document.getElementById('token-modal').style.display = 'none';
  refresh();
}

document.getElementById('token-input').addEventListener('keydown', e => {
  if (e.key === 'Enter') saveToken();
});

if (token) document.getElementById('token-modal').style.display = 'none';

function relativeTime(iso) {
  const diff = Math.round((Date.now() - new Date(iso)) / 1000);
  if (diff < 5)  return 'just now';
  if (diff < 60) return diff + 's ago';
  if (diff < 3600) return Math.round(diff/60) + 'm ago';
  return Math.round(diff/3600) + 'h ago';
}

function badges(arr, cls) {
  if (!arr || !arr.length) return '<span style="color:#484f58">—</span>';
  return arr.map(v => '<span class="badge ' + cls + '">' + v + '</span>').join('');
}

async function refresh() {
  if (!token) return;
  try {
    const resp = await fetch('/status', { headers: { 'Authorization': 'Bearer ' + token } });
    if (resp.status === 401) {
      localStorage.removeItem(STORAGE_KEY);
      token = null;
      document.getElementById('token-modal').style.display = 'flex';
      document.getElementById('error-bar').style.display = 'none';
      return;
    }
    const data = await resp.json();
    document.getElementById('error-bar').style.display = 'none';
    document.getElementById('s-total').textContent = data.total;
    document.getElementById('s-idle').textContent  = data.idle;
    document.getElementById('s-busy').textContent  = data.busy;
    document.getElementById('subtitle').textContent =
      'Last updated ' + new Date().toLocaleTimeString();

    const rows = (data.workers || []).map(w => ` + "`" + `
      <tr>
        <td>${w.id}</td>
        <td>${w.hostname}</td>
        <td>${badges(w.capabilities, 'badge-cap')}</td>
        <td>${badges(w.models, 'badge-model')}</td>
        <td>${badges(w.labels, 'badge-label')}</td>
        <td>${w.busy
          ? '<span class="status-busy">● busy</span>' + (w.current_session ? ' <span style="color:#484f58;font-size:0.75rem">' + w.current_session + '</span>' : '')
          : '<span class="status-idle">● idle</span>'}</td>
        <td class="lastseen">${relativeTime(w.last_seen)}</td>
      </tr>` + "`" + `).join('');
    document.getElementById('tbody').innerHTML = rows ||
      '<tr><td colspan="7" style="color:#484f58;text-align:center;padding:2rem">No agents connected</td></tr>';
  } catch(e) {
    const bar = document.getElementById('error-bar');
    bar.textContent = 'Failed to reach /status: ' + e.message;
    bar.style.display = 'block';
  }
}

refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>`
