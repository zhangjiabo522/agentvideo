package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type StudioEvent struct {
	Seq       uint64         `json:"seq"`
	Type      string         `json:"type"`
	ProjectID string         `json:"projectId"`
	RunID     string         `json:"runId,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Message   string         `json:"message"`
	CreatedAt string         `json:"createdAt"`
	Project   *Project       `json:"project,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type studioState struct {
	mu          sync.Mutex
	seq         uint64
	events      map[string][]StudioEvent
	subscribers map[string]map[chan StudioEvent]bool
	history     map[string][]Project
}

func newStudioState() *studioState {
	return &studioState{events: map[string][]StudioEvent{}, subscribers: map[string]map[chan StudioEvent]bool{}, history: map[string][]Project{}}
}

func cloneStudioProject(project Project) Project {
	data, _ := json.Marshal(project)
	var copied Project
	_ = json.Unmarshal(data, &copied)
	return copied
}

func (s *Server) publishStudioEvent(event StudioEvent) {
	if event.ProjectID == "" {
		return
	}
	if event.Project != nil {
		copy := cloneStudioProject(*event.Project)
		event.Project = &copy
	}
	state := s.studio
	state.mu.Lock()
	defer state.mu.Unlock()
	state.seq++
	event.Seq = state.seq
	if event.CreatedAt == "" {
		event.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	entries := append(state.events[event.ProjectID], event)
	if len(entries) > 200 {
		entries = entries[len(entries)-200:]
	}
	state.events[event.ProjectID] = entries
	for channel := range state.subscribers[event.ProjectID] {
		select {
		case channel <- event:
		default:
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- event:
			default:
			}
		}
	}
}

func (s *Server) studioActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.readProject(id); err != nil {
		errorJSON(w, 404, "工程不存在")
		return
	}
	s.studio.mu.Lock()
	entries := append([]StudioEvent{}, s.studio.events[id]...)
	s.studio.mu.Unlock()
	writeJSON(w, 200, map[string]any{"events": entries})
}

func (s *Server) studioEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		errorJSON(w, 400, "工程编号无效")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		errorJSON(w, 500, "当前连接不支持实时事件")
		return
	}
	channel := make(chan StudioEvent, 64)
	s.studio.mu.Lock()
	if s.studio.subscribers[id] == nil {
		s.studio.subscribers[id] = map[chan StudioEvent]bool{}
	}
	s.studio.subscribers[id][channel] = true
	last, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	if last == 0 {
		last, _ = strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	}
	backlog := []StudioEvent{}
	for _, event := range s.studio.events[id] {
		if last > 0 && event.Seq > last {
			backlog = append(backlog, event)
		}
	}
	seq := s.studio.seq
	s.studio.mu.Unlock()
	defer func() {
		s.studio.mu.Lock()
		delete(s.studio.subscribers[id], channel)
		if len(s.studio.subscribers[id]) == 0 {
			delete(s.studio.subscribers, id)
		}
		s.studio.mu.Unlock()
	}()
	project, err := s.readProject(id)
	if err != nil {
		errorJSON(w, 404, "工程不存在")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event StudioEvent) bool {
		data, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return false
		}
		if _, writeErr := fmt.Fprintf(w, "id: %d\nevent: message\ndata: %s\n\n", event.Seq, data); writeErr != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for _, event := range backlog {
		if !send(event) {
			return
		}
	}
	if !send(StudioEvent{Seq: seq, Type: "snapshot", ProjectID: id, Project: &project, Message: "已同步当前工程", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}) {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event := <-channel:
			if !send(event) {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) studioProjectNotification(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		capture := &studioResponse{ResponseWriter: w, status: 200}
		next(capture, r)
		if capture.status < 200 || capture.status >= 300 {
			return
		}
		var project Project
		if json.Unmarshal(capture.data, &project) == nil && project.ID != "" {
			s.studio.mu.Lock()
			delete(s.studio.history, project.ID)
			s.studio.mu.Unlock()
			s.publishStudioEvent(StudioEvent{Type: "project.updated", ProjectID: project.ID, Project: &project, Message: "工程已保存"})
		}
	}
}

type studioResponse struct {
	http.ResponseWriter
	status int
	data   []byte
}

func (w *studioResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *studioResponse) Write(data []byte) (int, error) {
	w.data = append(w.data, data...)
	return w.ResponseWriter.Write(data)
}
