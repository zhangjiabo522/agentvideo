package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func sampleProject() Project {
	return Project{
		ID: "project-test", Name: "测试工程", Width: 1280, Height: 720, FPS: 30,
		Scenes: []Scene{{ID: "scene-1", Name: "开场", Duration: 90, Background: "#151515", Nodes: []SceneNode{
			{ID: "node-1", Type: "text", Name: "标题", X: 100, Y: 100, Width: 800, Height: 120, Opacity: 1, Color: "#ffffff", Text: "原始文字", FontSize: 48, Start: 0, End: 90, Animation: "fade"},
			{ID: "node-2", Type: "rect", Name: "装饰", X: 100, Y: 400, Width: 600, Height: 20, Opacity: 1, Color: "#d4e56f", Start: 0, End: 90, Animation: "none"},
		}}},
		Audio: []AudioTrack{},
	}
}

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	server, err := NewServer(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return server, server.Handler()
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeProject(t *testing.T, response *httptest.ResponseRecorder) Project {
	t.Helper()
	var project Project
	if err := json.Unmarshal(response.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestProjectPersistenceAndRevisionConflict(t *testing.T) {
	server, handler := testServer(t)
	project := sampleProject()
	response := requestJSON(t, handler, "POST", "/api/projects", project)
	if response.Code != 201 {
		t.Fatalf("创建返回 %d: %s", response.Code, response.Body.String())
	}
	project = decodeProject(t, response)
	if project.Revision != 1 || project.UpdatedAt == "" {
		t.Fatal("首次保存未赋予版本和时间")
	}
	if response := requestJSON(t, handler, "POST", "/api/projects", project); response.Code != 409 {
		t.Fatalf("重复创建返回 %d", response.Code)
	}
	project.Name = "新标题"
	var group sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses <- requestJSON(t, handler, "PUT", "/api/projects/"+project.ID, project).Code
		}()
	}
	group.Wait()
	close(statuses)
	count := map[int]int{}
	for status := range statuses {
		count[status]++
	}
	if count[200] != 1 || count[409] != 1 {
		t.Fatalf("并发保存未阻止覆盖: %v", count)
	}
	reopened, err := NewServer(server.dataDir, server.distDir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.readProject(project.ID)
	if err != nil || loaded.Name != "新标题" || loaded.Revision != 2 {
		t.Fatalf("重启后工程不一致: %+v, %v", loaded, err)
	}
	if response := requestJSON(t, handler, "DELETE", "/api/projects/"+project.ID, nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response := requestJSON(t, handler, "GET", "/api/projects/"+project.ID, nil); response.Code != 404 {
		t.Fatalf("删除后返回 %d", response.Code)
	}
}

func TestProjectRejectsUnsafeAndInvalidData(t *testing.T) {
	_, handler := testServer(t)
	for _, mutation := range []func(*Project){
		func(project *Project) { project.Scenes = nil },
		func(project *Project) { project.ID = "../settings" },
		func(project *Project) { project.Scenes[0].Nodes[0].End = 91 },
		func(project *Project) { project.Scenes[0].Nodes[0].Src = "javascript:alert(1)" },
		func(project *Project) { project.Scenes[0].Nodes[1].ID = "node-1" },
		func(project *Project) { project.Width = 1279 },
		func(project *Project) { project.Scenes[0].Duration = 30 * 121 },
	} {
		project := sampleProject()
		mutation(&project)
		response := requestJSON(t, handler, "POST", "/api/projects", project)
		if response.Code != 400 {
			t.Fatalf("无效工程被接受: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestSettingsHidesAndPreservesKeys(t *testing.T) {
	server, handler := testServer(t)
	settings := Settings{BaseURL: "https://example.com/v1", Model: "text-model", APIKey: "text-secret", ImageBaseURL: "https://example.com/v1", ImageModel: "image-model", ImageAPIKey: "image-secret"}
	response := requestJSON(t, handler, "PUT", "/api/settings", settings)
	if response.Code != 200 || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("保存泄露密钥: %s", response.Body.String())
	}
	settings.APIKey, settings.ImageAPIKey = "", ""
	response = requestJSON(t, handler, "PUT", "/api/settings", settings)
	if response.Code != 200 || server.currentSettings().APIKey != "text-secret" || server.currentSettings().ImageAPIKey != "image-secret" {
		t.Fatal("空密钥覆盖了既有密钥")
	}
	response = requestJSON(t, handler, "GET", "/api/settings", nil)
	if strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), `"hasApiKey":true`) {
		t.Fatal("设置返回未隐藏密钥或未提供配置状态")
	}
	stat, err := os.Stat(filepath.Join(server.dataDir, "settings.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("设置文件权限应为0600")
	}
	saved, err := os.ReadFile(filepath.Join(server.dataDir, "settings.json"))
	if err != nil || bytes.Contains(saved, []byte("text-secret")) || bytes.Contains(saved, []byte("image-secret")) || bytes.Contains(saved, []byte("text-model")) {
		t.Fatal("模型设置文件未加密")
	}
	reopened, err := NewServer(server.dataDir, server.distDir)
	if err != nil || reopened.currentSettings().APIKey != "text-secret" || reopened.currentSettings().ImageAPIKey != "image-secret" {
		t.Fatalf("加密设置未能重新打开: %v", err)
	}
	key, err := os.Stat(filepath.Join(server.dataDir, ".settings.key"))
	if err != nil || key.Mode().Perm() != 0600 || key.Size() != 32 {
		t.Fatal("本机密钥大小或权限不正确")
	}
}

func TestSettingsTestsUnsavedConfigWithStoredKey(t *testing.T) {
	var authorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "连接成功"}}}})
	}))
	defer provider.Close()
	server, handler := testServer(t)
	server.settings = Settings{BaseURL: provider.URL, Model: "saved-model", APIKey: "existing-key"}
	input := map[string]any{"baseUrl": provider.URL, "model": "unsaved-model", "apiKey": "", "kind": "text"}
	response := requestJSON(t, handler, "POST", "/api/settings/test", input)
	if response.Code != 200 || authorization != "Bearer existing-key" || server.settings.Model != "saved-model" {
		t.Fatalf("连接测试未使用临时配置及旧密钥: %d %s", response.Code, response.Body.String())
	}
}

func TestAgentRequiresRealProvider(t *testing.T) {
	_, handler := testServer(t)
	response := requestJSON(t, handler, "POST", "/api/agent", agentRequest{Project: sampleProject(), Prompt: "改标题", Scope: "project"})
	if response.Code != 400 || !strings.Contains(response.Body.String(), "尚未配置文本模型") {
		t.Fatalf("没有配置时生成了假结果: %s", response.Body.String())
	}
}

func TestAgentValidatesScopeLocksAndTrailingContent(t *testing.T) {
	cases := []struct {
		name   string
		scope  string
		mutate func(*Project)
		tail   string
		status int
	}{
		{"修改选中标题", "node", func(project *Project) { project.Scenes[0].Nodes[0].Text = "已修改" }, "", 200},
		{"误改其他图层", "node", func(project *Project) { project.Scenes[0].Nodes[1].Color = "#ff0000" }, "", 422},
		{"误改锁定图层", "project", func(project *Project) { project.Scenes[0].Nodes[1].X = 300 }, "", 422},
		{"删除锁定图层", "scene", func(project *Project) { project.Scenes[0].Nodes = project.Scenes[0].Nodes[:1] }, "", 422},
		{"额外尾随内容", "project", func(project *Project) {}, "invalid", 502},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			project := sampleProject()
			project.Scenes[0].Nodes[1].Locked = true
			candidate := sampleProject()
			candidate.Scenes[0].Nodes[1].Locked = true
			test.mutate(&candidate)
			data, _ := json.Marshal(candidate)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("服务接口或密钥不一致")
				}
				writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(data) + test.tail}}}})
			}))
			defer provider.Close()
			server, handler := testServer(t)
			server.settings = Settings{BaseURL: provider.URL, Model: "test-model", APIKey: "test-key"}
			response := requestJSON(t, handler, "POST", "/api/agent", agentRequest{Project: project, Prompt: "修改当前标题", Scope: test.scope, SceneID: "scene-1", NodeID: "node-1"})
			if response.Code != test.status {
				t.Fatalf("预期 %d，实际%d: %s", test.status, response.Code, response.Body.String())
			}
		})
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	picture.Set(1, 1, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestUploadChecksActualFileFormat(t *testing.T) {
	_, handler := testServer(t)
	for _, test := range []struct {
		data   []byte
		status int
	}{
		{testPNG(t), 201},
		{[]byte("<svg><script>alert(1)</script></svg>"), 400},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("file", "fake.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(test.data); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "/api/uploads", &body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("素材验证返回 %d: %s", response.Code, response.Body.String())
		}
		if response.Code == 201 {
			var asset map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
				t.Fatal(err)
			}
			loaded := requestJSON(t, handler, "GET", asset["url"], nil)
			if loaded.Code != 200 || !bytes.Equal(loaded.Body.Bytes(), test.data) {
				t.Fatal("上传素材未可读取")
			}
		}
	}
}

func TestImageGenerationPersistsProviderOutput(t *testing.T) {
	data := testPNG(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer image-key" {
			t.Error("生图接口或密钥不一致")
		}
		writeJSON(w, 200, map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	}))
	defer provider.Close()
	server, handler := testServer(t)
	server.settings = Settings{ImageBaseURL: provider.URL, ImageModel: "image-model", ImageAPIKey: "image-key"}
	response := requestJSON(t, handler, "POST", "/api/images/generate", map[string]any{"prompt": "红色图形", "width": 1024, "height": 1024})
	if response.Code != 200 {
		t.Fatalf("生图返回 %d: %s", response.Code, response.Body.String())
	}
	var asset map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	loaded := requestJSON(t, handler, "GET", asset["url"], nil)
	if loaded.Code != 200 || !bytes.Equal(loaded.Body.Bytes(), data) {
		t.Fatal("生成图片未保存成可读取资产")
	}
}
