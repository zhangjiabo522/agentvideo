package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Server struct {
	mu         sync.RWMutex
	dataDir    string
	distDir    string
	settings   Settings
	httpClient *http.Client
	studio     *studioState
	studioMux  http.Handler
}

func NewServer(dataDir, distDir string) (*Server, error) {
	for _, dir := range []string{dataDir, filepath.Join(dataDir, "uploads"), filepath.Join(dataDir, "exports")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	s := &Server{dataDir: dataDir, distDir: distDir, httpClient: &http.Client{Timeout: 3 * time.Minute}, studio: newStudioState()}
	settings, err := s.loadSettings()
	if err == nil {
		s.settings = settings
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("模型设置文件无法读取: %w", err)
	}
	s.settings = settingsDefaults(s.settings)
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.studioProjectNotification(s.createProject))
	mux.HandleFunc("GET /api/projects/{id}", s.getProject)
	mux.HandleFunc("PUT /api/projects/{id}", s.studioProjectNotification(s.updateProject))
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("POST /api/settings/test", s.testSettings)
	mux.HandleFunc("POST /api/models", s.listModels)
	mux.HandleFunc("POST /api/speech", s.speech)
	mux.HandleFunc("POST /api/agent", s.agent)
	mux.HandleFunc("POST /api/images/generate", s.generateImage)
	mux.HandleFunc("POST /api/uploads", s.upload)
	mux.HandleFunc("POST /api/bundle", s.postBundle)
	mux.HandleFunc("POST /api/projects/import", s.importBundle)
	mux.HandleFunc("GET /api/projects/{id}/bundle", s.getBundle)
	s.registerRenderRoutes(mux)
	s.registerStudioRoutes(mux)
	s.registerAgentRunRoutes(mux)
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(filepath.Join(s.dataDir, "uploads")))))
	mux.Handle("GET /exports/", http.StripPrefix("/exports/", http.FileServer(http.Dir(filepath.Join(s.dataDir, "exports")))))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { errorJSON(w, 404, "接口不存在") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			errorJSON(w, 405, "请求方法不支持")
			return
		}
		s.static(w, r)
	})
	s.mu.Lock()
	s.studioMux = mux
	s.mu.Unlock()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		defer func() {
			if value := recover(); value != nil {
				log.Printf("请求失败: %v", value)
				errorJSON(w, 500, "服务器处理失败")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	_, ffmpegErr := exec.LookPath("ffmpeg")
	_, nodeErr := exec.LookPath("node")
	_, scriptErr := os.Stat(filepath.Join("scripts", "render.mjs"))
	writeJSON(w, 200, map[string]any{"ok": true, "ffmpeg": ffmpegErr == nil, "renderer": nodeErr == nil && scriptErr == nil, "localTts": localSpeechTool() != "", "storage": s.dataDir})
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.distDir, filepath.Clean("/"+r.URL.Path))
	if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	index := filepath.Join(s.distDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		errorJSON(w, 503, "前端未构建，请执行 npm run build 或启动开发服务")
		return
	}
	http.ServeFile(w, r, index)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("响应写入失败: %v", err)
	}
}

func errorJSON(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		errorJSON(w, 400, "请求数据无效: "+err.Error())
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		errorJSON(w, 400, "请求只能包含一份 JSON 数据")
		return false
	}
	return true
}

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".save-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func endpoint(base, path string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/") + "/" + path
}
