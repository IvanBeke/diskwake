package main

import (
	"strings"
	"sync"
	"time"
)

type diskRuntime struct {
	Name     string
	Device   string
	Interval string
	Windows  []string

	InWindow       bool
	LastTickAt     time.Time
	LastReadAt     time.Time
	LastReadResult string
	LastError      string
}

type logSnapshot struct {
	Lines []string `json:"lines"`
}

type diskStatusSnapshot struct {
	Name           string   `json:"name"`
	Device         string   `json:"device"`
	Interval       string   `json:"interval"`
	Windows        []string `json:"windows"`
	CurrentState   string   `json:"current_state"`
	LastTickAt     string   `json:"last_tick_at"`
	LastReadAt     string   `json:"last_read_at"`
	LastReadResult string   `json:"last_read_result"`
	LastError      string   `json:"last_error"`
}

type statusSnapshot struct {
	ServerTime     string               `json:"server_time"`
	ServerTimezone string               `json:"server_timezone"`
	ConfigPath     string               `json:"config_path"`
	Disks          []diskStatusSnapshot `json:"disks"`
}

type configSnapshot struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type runtimeState struct {
	mu          sync.RWMutex
	configPath  string
	configText  string
	timezone    string
	disks       map[string]*diskRuntime
	diskOrder   []string
	logs        []string
	maxLogs     int
	partialLine string
	nextSubID   int
	subscribers map[int]chan string
}

func newRuntimeState(cfg *Config, configPath, configText string, maxLogs int) *runtimeState {
	s := &runtimeState{
		configPath:  configPath,
		configText:  configText,
		timezone:    time.Now().Location().String(),
		disks:       make(map[string]*diskRuntime, len(cfg.Disks)),
		diskOrder:   make([]string, 0, len(cfg.Disks)),
		logs:        make([]string, 0, maxLogs),
		maxLogs:     maxLogs,
		subscribers: make(map[int]chan string),
	}

	for _, d := range cfg.Disks {
		windows := make([]string, 0, len(d.Windows))
		for _, w := range d.Windows {
			label := w.Start + "->" + w.End
			dayLabels := make([]string, 0, len(w.Days)+1)
			if w.Day != "" {
				dayLabels = append(dayLabels, strings.ToLower(strings.TrimSpace(w.Day)))
			}
			for _, day := range w.Days {
				dayLabels = append(dayLabels, strings.ToLower(strings.TrimSpace(day)))
			}
			if len(dayLabels) > 0 {
				label += " (" + strings.Join(dayLabels, ",") + ")"
			}
			windows = append(windows, label)
		}

		s.disks[d.Name] = &diskRuntime{
			Name:           d.Name,
			Device:         d.Device,
			Interval:       d.KeepaliveInterval,
			Windows:        windows,
			LastReadResult: "not yet attempted",
		}
		s.diskOrder = append(s.diskOrder, d.Name)
	}

	return s
}

func formatServerTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05 MST")
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return formatServerTime(t)
}

func (s *runtimeState) recordOutsideWindow(name string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.disks[name]
	if !ok {
		return
	}
	d.InWindow = false
	d.LastTickAt = now
}

func (s *runtimeState) recordReadResult(name string, now time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.disks[name]
	if !ok {
		return
	}
	d.InWindow = true
	d.LastTickAt = now
	d.LastReadAt = now
	if err != nil {
		d.LastReadResult = "error"
		d.LastError = err.Error()
		return
	}
	d.LastReadResult = "ok"
	d.LastError = ""
}

func (s *runtimeState) statusSnapshot() statusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := statusSnapshot{
		ServerTime:     formatServerTime(time.Now()),
		ServerTimezone: s.timezone,
		ConfigPath:     s.configPath,
		Disks:          make([]diskStatusSnapshot, 0, len(s.diskOrder)),
	}

	for _, name := range s.diskOrder {
		d, ok := s.disks[name]
		if !ok {
			continue
		}

		currentState := "outside configured windows"
		if d.InWindow {
			currentState = "in window"
		}

		out.Disks = append(out.Disks, diskStatusSnapshot{
			Name:           d.Name,
			Device:         d.Device,
			Interval:       d.Interval,
			Windows:        append([]string(nil), d.Windows...),
			CurrentState:   currentState,
			LastTickAt:     formatOptionalTime(d.LastTickAt),
			LastReadAt:     formatOptionalTime(d.LastReadAt),
			LastReadResult: d.LastReadResult,
			LastError:      d.LastError,
		})
	}

	return out
}

func (s *runtimeState) configSnapshot() configSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return configSnapshot{
		Path:    s.configPath,
		Content: s.configText,
	}
}

func (s *runtimeState) logsSnapshot() logSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lines := make([]string, len(s.logs))
	copy(lines, s.logs)
	return logSnapshot{Lines: lines}
}

func (s *runtimeState) appendLogsChunk(chunk string) {
	if chunk == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	joined := s.partialLine + chunk
	parts := strings.Split(joined, "\n")

	if strings.HasSuffix(joined, "\n") {
		s.partialLine = ""
	} else {
		s.partialLine = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}

	if len(parts) == 0 {
		return
	}

	live := make([]chan string, 0, len(s.subscribers))
	for _, p := range parts {
		line := strings.TrimRight(p, "\r")
		if line == "" {
			continue
		}

		s.logs = append(s.logs, line)
		if len(s.logs) > s.maxLogs {
			s.logs = s.logs[len(s.logs)-s.maxLogs:]
		}

		if len(s.subscribers) > 0 {
			live = live[:0]
			for _, ch := range s.subscribers {
				live = append(live, ch)
			}
			for _, ch := range live {
				select {
				case ch <- line:
				default:
				}
			}
		}
	}
}

func (s *runtimeState) subscribeLogs() (int, <-chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextSubID
	s.nextSubID++

	ch := make(chan string, 64)
	s.subscribers[id] = ch
	return id, ch
}

func (s *runtimeState) unsubscribeLogs(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch, ok := s.subscribers[id]
	if !ok {
		return
	}
	delete(s.subscribers, id)
	close(ch)
}

type stateLogWriter struct {
	state *runtimeState
}

func newStateLogWriter(state *runtimeState) *stateLogWriter {
	return &stateLogWriter{state: state}
}

func (w *stateLogWriter) Write(p []byte) (int, error) {
	w.state.appendLogsChunk(string(p))
	return len(p), nil
}
