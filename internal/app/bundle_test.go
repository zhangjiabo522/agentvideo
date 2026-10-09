package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func importRequest(t *testing.T, handler http.Handler, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "工程.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/projects/import", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestBundleSnapshotRoundTripWithImageAndAudio(t *testing.T) {
	server, handler := testServer(t)
	project := sampleProject()
	picture := testPNG(t)
	imageURL, err := server.saveAsset(picture, ".png")
	if err != nil {
		t.Fatal(err)
	}
	wav := append([]byte("RIFF"), []byte{4, 0, 0, 0}...)
	wav = append(wav, []byte("WAVEfmt ")...)
	audioURL, err := server.saveAsset(wav, ".wav")
	if err != nil {
		t.Fatal(err)
	}
	project.Scenes[0].Nodes[0].Text = "尚未自动保存的文案"
	project.Scenes[0].Nodes[1].Type = "image"
	project.Scenes[0].Nodes[1].Src = imageURL
	project.Scenes[0].Nodes[1].Protected = []string{"x", "src"}
	project.Audio = []AudioTrack{{ID: "audio-1", Name: "音乐", Src: audioURL, Duration: 60, Volume: .8}}
	response := requestJSON(t, handler, "POST", "/api/bundle", map[string]any{"project": project})
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("打包返回 %d: %s", response.Code, response.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil || len(reader.File) != 3 {
		t.Fatalf("工程包素材数量不正确: %v", err)
	}
	for _, entry := range reader.File {
		if entry.Name == "settings.json" || entry.Name == ".settings.key" {
			t.Fatal("工程包含模型设置")
		}
	}
	imported := importRequest(t, handler, response.Body.Bytes())
	if imported.Code != 201 {
		t.Fatalf("导入返回 %d: %s", imported.Code, imported.Body.String())
	}
	loaded := decodeProject(t, imported)
	if loaded.ID == project.ID || loaded.Revision != 1 || loaded.Scenes[0].Nodes[0].Text != "尚未自动保存的文案" || len(loaded.Scenes[0].Nodes[1].Protected) != 2 {
		t.Fatal("工程导入丢失可编辑内容或未创建独立编号")
	}
	for _, asset := range []struct {
		url  string
		data []byte
	}{{loaded.Scenes[0].Nodes[1].Src, picture}, {loaded.Audio[0].Src, wav}} {
		if asset.url == imageURL || asset.url == audioURL {
			t.Fatal("导入资产未重新分配路径")
		}
		response := requestJSON(t, handler, "GET", asset.url, nil)
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), asset.data) {
			t.Fatal("工程包素材未完整恢复")
		}
	}
}

func archiveData(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestBundleRejectsTraversalMissingAssetsAndUnsafeFiles(t *testing.T) {
	server, handler := testServer(t)
	project := sampleProject()
	project.Scenes[0].Nodes[0].Src = "assets/missing.png"
	manifest, _ := json.Marshal(project)
	for _, entries := range []map[string][]byte{
		{"project.json": manifest, "../escaped.txt": []byte("payload")},
		{"project.json": manifest},
		{"project.json": manifest, "assets/missing.png": []byte("<svg></svg>")},
		{"project.json": manifest, "/root/escaped": []byte("payload")},
	} {
		response := importRequest(t, handler, archiveData(t, entries))
		if response.Code != 400 {
			t.Fatalf("不安全工程被接受: %d %s", response.Code, response.Body.String())
		}
	}
	if files, _ := filepath.Glob(filepath.Join(server.dataDir, "project-*.json")); len(files) != 0 {
		t.Fatal("失败导入仍保存了工程")
	}
}

func TestBundleIncludesBuiltInMediaAndRejectsMissingMedia(t *testing.T) {
	server, handler := testServer(t)
	if err := os.MkdirAll(filepath.Join(server.distDir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server.distDir, "media", "test.png"), testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	project := sampleProject()
	project.Scenes[0].Nodes[0].Src = "/media/test.png"
	response := requestJSON(t, handler, "POST", "/api/bundle", map[string]any{"project": project})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	project.Scenes[0].Nodes[0].Src = "/uploads/missing.png"
	response = requestJSON(t, handler, "POST", "/api/bundle", map[string]any{"project": project})
	if response.Code != 400 {
		t.Fatal("导出未拒绝缺失素材")
	}
}

func TestSettingsMigratesPlaintextAndRejectsTampering(t *testing.T) {
	dir := t.TempDir()
	settings := Settings{BaseURL: "https://example.com/v1", Model: "test-model", APIKey: "old-plaintext-key"}
	if err := atomicJSON(filepath.Join(dir, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(dir, t.TempDir())
	if err != nil || server.currentSettings().APIKey != settings.APIKey {
		t.Fatalf("旧设置迁移失败: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || bytes.Contains(data, []byte(settings.APIKey)) {
		t.Fatal("旧设置未迁移为密文")
	}
	var envelope encryptedSettings
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Nonce = "bad"
	if err := atomicJSON(filepath.Join(dir, "settings.json"), envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(dir, t.TempDir()); err == nil {
		t.Fatal("损坏密文未被拒绝")
	}
}

func TestAgentRespectsManualProperties(t *testing.T) {
	project := sampleProject()
	project.Scenes[0].Nodes[0].Protected = []string{"text", "x"}
	candidate := sampleProject()
	candidate.Scenes[0].Nodes[0].Protected = []string{"text", "x"}
	candidate.Scenes[0].Nodes[0].Text = "Agent标题"
	input := agentRequest{Project: project, Scope: "project"}
	if err := validateAgentChange(input, candidate); err == nil {
		t.Fatal("全工程Agent覆盖了手动修改")
	}
	input.Scope, input.SceneID, input.NodeID = "node", "scene-1", "node-1"
	if err := validateAgentChange(input, candidate); err != nil {
		t.Fatalf("选中图层明确修改被拒绝: %v", err)
	}
	candidate.Scenes[0].Nodes[0].Protected = nil
	if err := validateAgentChange(input, candidate); err == nil {
		t.Fatal("Agent可以移除手动属性保护")
	}
}
