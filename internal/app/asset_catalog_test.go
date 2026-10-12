package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func listedAssets(t *testing.T, handler http.Handler, query string) []Asset {
	t.Helper()
	response := requestJSON(t, handler, "GET", "/api/assets"+query, nil)
	if response.Code != 200 {
		t.Fatalf("素材列表返回 %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Assets []Asset `json:"assets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Assets == nil {
		t.Fatalf("素材列表格式错误: %v", err)
	}
	return result.Assets
}

func uploadCatalogAsset(t *testing.T, handler http.Handler, name string, data []byte) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/uploads", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("上传返回 %d: %s", response.Code, response.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result["url"]
}

func TestAssetCatalogUploadPersistsAndMatchesActualFiles(t *testing.T) {
	server, handler := testServer(t)
	if assets := listedAssets(t, handler, ""); len(assets) != 0 {
		t.Fatalf("初始素材目录非空: %+v", assets)
	}
	url := uploadCatalogAsset(t, handler, "我的山湖.png", testPNG(t))
	assets := listedAssets(t, handler, "?type=image&source=upload&query=山湖")
	if len(assets) != 1 {
		t.Fatalf("上传后素材列表错误: %+v", assets)
	}
	asset := assets[0]
	if asset.URL != url || asset.Name != "我的山湖.png" || asset.Source != "upload" || asset.Type != "image" || asset.MIMEType != "image/png" || asset.Width != 8 || asset.Height != 8 || asset.Size != int64(len(testPNG(t))) || asset.ID == "" || asset.CreatedAt == "" || asset.ProjectIDs == nil {
		t.Fatalf("素材元信息不完整: %+v", asset)
	}
	reopened, err := NewServer(server.dataDir, server.distDir)
	if err != nil {
		t.Fatal(err)
	}
	assets = listedAssets(t, reopened.Handler(), "")
	if len(assets) != 1 || assets[0].ID != asset.ID || assets[0].Name != asset.Name || assets[0].CreatedAt != asset.CreatedAt || assets[0].Source != "upload" {
		t.Fatalf("重启丢失素材信息: %+v", assets)
	}
	if err := os.Remove(filepath.Join(server.dataDir, strings.TrimPrefix(url, "/"))); err != nil {
		t.Fatal(err)
	}
	if len(listedAssets(t, reopened.Handler(), "")) != 0 {
		t.Fatal("已删除素材仍出现在列表")
	}
}

func TestAssetCatalogBackfillsLegacyFilesAndProjectReferences(t *testing.T) {
	server, handler := testServer(t)
	imageURL := "/uploads/00112233445566778899aabbccddeeff.png"
	if err := os.WriteFile(filepath.Join(server.dataDir, strings.TrimPrefix(imageURL, "/")), testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(server.distDir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server.distDir, "media", "sample.png"), testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	assets := listedAssets(t, handler, "?source=existing")
	if len(assets) != 1 || !strings.HasPrefix(assets[0].Name, "图片素材 · ") {
		t.Fatalf("历史素材未生成可读名称: %+v", assets)
	}
	project := sampleProject()
	project.Scenes[0].Nodes[0].Type = "image"
	project.Scenes[0].Nodes[0].Src = imageURL
	project.Scenes[0].Nodes[0].Name = "镜头中的晨光"
	project = studioSaved(t, handler, project)
	assets = listedAssets(t, handler, "?projectId="+project.ID)
	if len(assets) != 1 || assets[0].Name != "镜头中的晨光" || len(assets[0].ProjectIDs) != 1 || assets[0].ProjectIDs[0] != project.ID {
		t.Fatalf("未回填工程引用信息: %+v", assets)
	}
	if assets := listedAssets(t, handler, "?source=builtin"); len(assets) != 1 || assets[0].URL != "/media/sample.png" {
		t.Fatalf("内置素材未扫描: %+v", assets)
	}
	project.Scenes[0].Nodes[0].Src = ""
	if response := requestJSON(t, handler, "PUT", "/api/projects/"+project.ID, project); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if assets := listedAssets(t, handler, "?projectId="+project.ID); len(assets) != 0 {
		t.Fatal("删除工程引用后未同步")
	}
}

func TestAssetCatalogRejectsUnsafeFilesAndExcludesPreview(t *testing.T) {
	server, handler := testServer(t)
	data := testPNG(t)
	preview, err := server.saveAssetWithMetadata(context.Background(), data, ".png", assetOptions{Source: "preview", Name: "截图"})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.png")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(server.dataDir, "uploads")
	if err := os.Symlink(outside, filepath.Join(root, "link.png")); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{".tts-hidden.wav": testSpeechWAV(), "not-image.png": []byte("<html>私密内容</html>"), "settings.json": []byte(`{"secret":"private"}`)} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(listedAssets(t, handler, "")) != 0 {
		t.Fatal("素材列表暴露了预览截图、隐藏文件或无效素材")
	}
	for _, path := range []string{"/uploads/link.png", "/uploads/", "/uploads/.tts-hidden.wav", "/uploads/settings.json", "/uploads/not-image.png"} {
		if response := requestJSON(t, handler, "GET", path, nil); response.Code != 404 {
			t.Fatalf("不安全素材地址 %s 返回 %d", path, response.Code)
		}
	}
	if response := requestJSON(t, handler, "GET", preview.URL, nil); response.Code != 200 {
		t.Fatal("合法的工具截图不能单独读取")
	}
	for _, query := range []string{"?type=video", "?source=preview", "?projectId=../private"} {
		if response := requestJSON(t, handler, "GET", "/api/assets"+query, nil); response.Code != 400 {
			t.Fatal("无效筛选条件未拒绝")
		}
	}
}

func TestAssetCatalogConcurrentSaveAndRefresh(t *testing.T) {
	server, _ := testServer(t)
	data := testPNG(t)
	var group sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 10; i++ {
		group.Add(2)
		go func() {
			defer group.Done()
			_, err := server.saveAssetWithMetadata(context.Background(), data, ".png", assetOptions{Name: "并发上传", Source: "upload"})
			failures <- err
		}()
		go func() {
			defer group.Done()
			_, err := server.catalogAssets(context.Background(), assetFilter{})
			failures <- err
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := NewServer(server.dataDir, server.distDir)
	if err != nil {
		t.Fatal(err)
	}
	assets := listedAssets(t, reopened.Handler(), "")
	if len(assets) != 10 {
		t.Fatalf("并发写入丢失素材记录: %d", len(assets))
	}
	for _, asset := range assets {
		if asset.Name != "并发上传" || asset.Source != "upload" {
			t.Fatalf("并发刷新覆盖元信息: %+v", asset)
		}
	}
}

func TestAssetCatalogRefreshesChangedContent(t *testing.T) {
	server, handler := testServer(t)
	url := uploadCatalogAsset(t, handler, "持续更新.png", testPNG(t))
	initial := listedAssets(t, handler, "")[0]
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 16, 10))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(server.dataDir, strings.TrimPrefix(url, "/"))
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	changed := listedAssets(t, handler, "")[0]
	if changed.Width != 16 || changed.Height != 10 || changed.Name != initial.Name || changed.ID != initial.ID || changed.CreatedAt != initial.CreatedAt || changed.Size != int64(data.Len()) {
		t.Fatalf("文件替换后缓存未更新: %+v", changed)
	}
}

func TestAssetCatalogGeneratedSpeechAndAITool(t *testing.T) {
	data := testPNG(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	}))
	defer provider.Close()
	server, handler := testServer(t)
	server.settings = Settings{ImageBaseURL: provider.URL, ImageModel: "image-test"}
	if response := requestJSON(t, handler, "POST", "/api/images/generate", map[string]any{"prompt": "东方晨曦"}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err := server.saveAssetWithMetadata(context.Background(), testSpeechWAV(), ".wav", assetOptions{Name: "开场旁白", Source: "speech", DurationSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	project := studioSaved(t, handler, sampleProject())
	args, err := scopeAgentToolArgs("video_assets_list", `{"type":"image"}`, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.executeStudioTool(context.Background(), "video_assets_list", args)
	if err != nil {
		t.Fatal(err)
	}
	assets := result["assets"].([]Asset)
	if len(assets) != 1 || assets[0].Source != "generated" || !strings.Contains(assets[0].Name, "东方晨曦") {
		t.Fatalf("AI 无法发现未加入当前工程的生图素材: %+v", assets)
	}
	audio := listedAssets(t, handler, "?type=audio&source=speech")
	if len(audio) != 1 || audio[0].DurationSeconds != 1 || audio[0].MIMEType != "audio/wav" {
		t.Fatalf("配音素材时长或来源错误: %+v", audio)
	}
	found := false
	for _, definition := range agentRunTools(server.studioToolDefinitions()) {
		found = found || definition["function"].(map[string]any)["name"] == "video_assets_list"
	}
	if !found {
		t.Fatal("内置 AI 未开放素材读取工具")
	}
}

func TestAssetCatalogWebPDimensionsAndImageLimits(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("UklGRkgAAABXRUJQVlA4IDwAAAAQAwCdASoSAAoAPpE4l0eloyIhMAgAsBIJQBdl0AAQNgAA/u7T3/4DdfFtMv/3OB/3OB/3OB/HM1mAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	server, handler := testServer(t)
	url := uploadCatalogAsset(t, handler, "真实 WebP.webp", data)
	assets := listedAssets(t, handler, "")
	if len(assets) != 1 || assets[0].URL != url || assets[0].Width != 18 || assets[0].Height != 10 || assets[0].MIMEType != "image/webp" {
		t.Fatalf("WebP 尺寸识别错误: %+v", assets)
	}
	oversized := append([]byte{}, data...)
	oversized[26], oversized[27] = 0xff, 0x3f
	for _, invalid := range [][]byte{data[:19], oversized, []byte("RIFFxxxxxxxxWEBPVP8Xxxxxxxxxxxxx")} {
		if _, _, err := assetFormat(invalid, true); err == nil {
			t.Fatal("截断或超限 WebP 未拒绝")
		}
	}
	if server.assetRecords[url].Width != 18 {
		t.Fatal("WebP 元信息未持久记录")
	}
}
