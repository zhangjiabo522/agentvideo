package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func previewFixture(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	t.Setenv("VIDEO_PREVIEW_ONLY", "1")
	dataDir, distDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "exports"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(dataDir, "settings.json"):          "unreadable private settings",
		filepath.Join(dataDir, "project-private.json"):   "private project",
		filepath.Join(dataDir, "exports", "public.mp4"):  "0123456789abcdefghij",
		filepath.Join(dataDir, "exports", "poster.jpg"):  "public poster",
		filepath.Join(dataDir, "exports", "private.mp4"): "private video",
		filepath.Join(dataDir, "exports", "private.wav"): "private audio",
		filepath.Join(distDir, "index.html"):             "public preview page",
		filepath.Join(distDir, "render.html"):            "private render entry",
	} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	videos := []PublicVideo{{ID: "public", Title: "宣传片", URL: "/exports/public.mp4", Poster: "/exports/poster.jpg", Duration: 33.1, Width: 1280, Height: 720, Featured: true}}
	if err := atomicJSON(filepath.Join(dataDir, "public-videos.json"), videos); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(dataDir, distDir)
	if err != nil {
		t.Fatal(err)
	}
	return server, server.Handler(), dataDir
}

func TestPreviewSkipsPrivateServicesAndPublishesMetadata(t *testing.T) {
	server, handler, dataDir := previewFixture(t)
	if !server.previewOnly || server.studio != nil || server.studioMux != nil || server.httpClient != nil || server.settings != (Settings{}) {
		t.Fatal("公开预览初始化了私有服务或模型设置")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "uploads")); !os.IsNotExist(err) {
		t.Fatal("公开预览创建了素材目录")
	}
	response := requestJSON(t, handler, "GET", "/api/site", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"previewOnly":true`) || !strings.Contains(response.Body.String(), projectGitHubURL) {
		t.Fatalf("公开站点配置无效: %d %s", response.Code, response.Body.String())
	}
	response = requestJSON(t, handler, "GET", "/api/public/videos", nil)
	var videos []PublicVideo
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &videos) != nil || len(videos) != 1 || videos[0].Title != "宣传片" || videos[0].Duration != 33.1 {
		t.Fatalf("公开视频信息无效: %d %s", response.Code, response.Body.String())
	}
	response = requestJSON(t, handler, "GET", "/api/health", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), dataDir) || strings.Contains(response.Body.String(), "storage") {
		t.Fatal("公开健康检查泄露了本机路径")
	}
}

func TestPreviewRejectsMCPAndPrivateRoutes(t *testing.T) {
	_, handler, dataDir := previewFixture(t)
	for _, path := range []string{
		"/mcp", "/mcp/", "/api/tools", "/api/tools/call", "/api/projects", "/api/projects/private",
		"/api/settings", "/api/settings/test", "/api/models", "/api/agent", "/api/agent/runs",
		"/api/speech", "/api/images/generate", "/api/uploads", "/api/exports", "/api/bundle",
		"/uploads/private.png", "/data/settings.json", "/render.html", "/exports/private.mp4", "/exports/private.wav", "/exports/",
	} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"} {
			response := requestJSON(t, handler, method, path, map[string]string{"prompt": "修改项目"})
			if response.Code != 404 || strings.Contains(response.Body.String(), "private") {
				t.Fatalf("公开预览暴露了 %s %s: %d %s", method, path, response.Code, response.Body.String())
			}
		}
	}
	for _, path := range []string{"/api/site", "/api/public/videos", "/exports/public.mp4", "/"} {
		response := requestJSON(t, handler, "POST", path, nil)
		if response.Code != 404 {
			t.Fatalf("公开预览允许了写请求 %s: %d", path, response.Code)
		}
	}
	settings, err := os.ReadFile(filepath.Join(dataDir, "settings.json"))
	if err != nil || string(settings) != "unreadable private settings" {
		t.Fatal("公开请求修改了本地设置")
	}
}

func TestPreviewVideoRangeAndHead(t *testing.T) {
	_, handler, _ := previewFixture(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/exports/public.mp4", nil)
	request.Header.Set("Range", "bytes=3-7")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "34567" || response.Header().Get("Content-Range") != "bytes 3-7/20" || response.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("视频分段读取无效: %d %v %s", response.Code, response.Header(), response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/exports/public.mp4", nil))
	if response.Code != 200 || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "20" {
		t.Fatalf("视频 HEAD 请求无效: %d %v", response.Code, response.Header())
	}
	response = requestJSON(t, handler, "GET", "/exports/poster.jpg", nil)
	if response.Code != 200 || response.Body.String() != "public poster" || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("公开封面未正常返回")
	}
}

func TestPreviewRejectsPathEscapeAndSymlinks(t *testing.T) {
	_, handler, dataDir := previewFixture(t)
	for _, path := range []string{
		"/exports/../settings.json", "/exports/%2e%2e/settings.json", "/exports/%70ublic.mp4", "/exports/public.mp4/extra",
		"/exports//public.mp4", "/assets/../render.html", "/%72ender.html", "/../settings.json",
	} {
		response := requestJSON(t, handler, "GET", path, nil)
		if response.Code != 404 {
			t.Fatalf("接受了无效路径 %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	publicPath := filepath.Join(dataDir, "exports", "public.mp4")
	if err := os.Remove(publicPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dataDir, "settings.json"), publicPath); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, handler, "GET", "/exports/public.mp4", nil)
	if response.Code != 404 || strings.Contains(response.Body.String(), "private") {
		t.Fatal("公开导出沿符号链接读取了私有文件")
	}
}

func TestPreviewManifestValidationAndEmptyList(t *testing.T) {
	t.Setenv("VIDEO_PREVIEW_ONLY", "1")
	server, err := NewServer(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, server.Handler(), "GET", "/api/public/videos", nil)
	if response.Code != 200 || !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()), []byte("[]")) {
		t.Fatal("缺失清单时未返回空视频列表")
	}
	for _, value := range []string{
		`[{"id":"x","title":"x","url":"/exports/../settings.mp4"}]`,
		`[{"id":"x","title":"x","url":"/exports/a.wav"}]`,
		`[{"id":"x","title":"x","url":"https://example.com/a.mp4"}]`,
		`[{"id":"x","title":"x","url":"/exports/a.mp4","poster":"/exports/a.svg"}]`,
		`[{"id":"x","title":"x","url":"/exports/%61.mp4"}]`,
		`[{"id":"x","title":"x","url":"/exports/a.mp4"},{"id":"x","title":"x","url":"/exports/b.mp4"}]`,
		`[] {}`,
		`invalid`,
	} {
		dataDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dataDir, "public-videos.json"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewServer(dataDir, t.TempDir()); err == nil {
			t.Fatalf("无效公开视频清单被接受: %s", value)
		}
	}
}

func TestNormalSiteKeepsEditingServices(t *testing.T) {
	t.Setenv("VIDEO_PREVIEW_ONLY", "")
	server, handler := testServer(t)
	response := requestJSON(t, handler, "GET", "/api/site", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"previewOnly":false`) || server.studio == nil {
		t.Fatal("正常模式未保留编辑服务")
	}
}
