package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const projectGitHubURL = "https://github.com/zhangjiabo522/agentvideo"

type PublicVideo struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	URL         string  `json:"url"`
	Poster      string  `json:"poster"`
	Duration    float64 `json:"duration"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	CreatedAt   string  `json:"createdAt"`
	Featured    bool    `json:"featured"`
}

func newPreviewServer(dataDir, distDir string) (*Server, error) {
	s := &Server{dataDir: dataDir, distDir: distDir, previewOnly: true, publicVideos: []PublicVideo{}, publicFiles: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(dataDir, "public-videos.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("公开视频清单无法读取: %w", err)
	}
	if len(data) > 4<<20 {
		return nil, errors.New("公开视频清单过大")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s.publicVideos); err != nil {
		return nil, fmt.Errorf("公开视频清单格式无效: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("公开视频清单只能包含一份 JSON 数据")
	}
	if s.publicVideos == nil {
		s.publicVideos = []PublicVideo{}
	}
	ids := map[string]bool{}
	for _, video := range s.publicVideos {
		if video.ID == "" || video.Title == "" || ids[video.ID] || video.Duration < 0 || video.Width < 0 || video.Height < 0 {
			return nil, errors.New("公开视频信息缺失或重复")
		}
		ids[video.ID] = true
		name, err := publicFilename(video.URL, true)
		if err != nil {
			return nil, err
		}
		s.publicFiles[video.URL] = name
		if video.Poster != "" {
			name, err = publicFilename(video.Poster, false)
			if err != nil {
				return nil, err
			}
			s.publicFiles[video.Poster] = name
		}
	}
	return s, nil
}

func publicFilename(raw string, video bool) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || !strings.HasPrefix(raw, "/exports/") {
		return "", errors.New("公开视频地址必须位于 /exports/ 目录")
	}
	name := strings.TrimPrefix(raw, "/exports/")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", errors.New("公开视频文件名无效")
	}
	ext := strings.ToLower(filepath.Ext(name))
	if video && ext != ".mp4" || !video && ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
		return "", errors.New("公开视频或封面格式不支持")
	}
	return name, nil
}

func (s *Server) site(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"previewOnly": s.previewOnly, "githubUrl": projectGitHubURL})
}

func (s *Server) previewHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			errorJSON(w, http.StatusNotFound, "接口不存在")
			return
		}
		switch r.URL.Path {
		case "/api/site":
			w.Header().Set("Cache-Control", "no-cache")
			s.site(w, r)
		case "/api/health":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "previewOnly": true})
		case "/api/public/videos":
			w.Header().Set("Cache-Control", "no-cache")
			writeJSON(w, http.StatusOK, s.publicVideos)
		default:
			if strings.HasPrefix(r.URL.Path, "/exports/") {
				s.publicExport(w, r)
				return
			}
			for _, prefix := range []string{"/api", "/mcp", "/uploads", "/data", "/exports", "/render.html"} {
				if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
					errorJSON(w, http.StatusNotFound, "页面不存在")
					return
				}
			}
			s.previewStatic(w, r)
		}
	})
}

func (s *Server) publicExport(w http.ResponseWriter, r *http.Request) {
	name, ok := s.publicFiles[r.URL.Path]
	if !ok || r.URL.RawPath != "" {
		errorJSON(w, http.StatusNotFound, "视频不存在")
		return
	}
	root, err := filepath.EvalSymlinks(filepath.Join(s.dataDir, "exports"))
	if err != nil {
		errorJSON(w, http.StatusNotFound, "视频不存在")
		return
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil || filepath.Dir(path) != root {
		errorJSON(w, http.StatusNotFound, "视频不存在")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		errorJSON(w, http.StatusNotFound, "视频不存在")
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		errorJSON(w, http.StatusNotFound, "视频不存在")
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, name, stat.ModTime(), file)
}

func (s *Server) previewStatic(w http.ResponseWriter, r *http.Request) {
	root, err := filepath.EvalSymlinks(s.distDir)
	if err != nil {
		errorJSON(w, http.StatusServiceUnavailable, "预览页面尚未准备好")
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	if clean != r.URL.Path || r.URL.RawPath != "" {
		errorJSON(w, http.StatusNotFound, "页面不存在")
		return
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err == nil {
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			errorJSON(w, http.StatusNotFound, "页面不存在")
			return
		}
		if stat, statErr := os.Stat(path); statErr == nil && stat.Mode().IsRegular() {
			http.ServeFile(w, r, path)
			return
		}
	}
	index, err := filepath.EvalSymlinks(filepath.Join(root, "index.html"))
	if err != nil || filepath.Dir(index) != root {
		errorJSON(w, http.StatusServiceUnavailable, "预览页面尚未准备好")
		return
	}
	http.ServeFile(w, r, index)
}
