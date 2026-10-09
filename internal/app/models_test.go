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

func TestModelsUsesUnsavedConfigAndRetainsStoredSecret(t *testing.T) {
	var receivedPath, authorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath, authorization = r.URL.Path, r.Header.Get("Authorization")
		if r.Method != "GET" {
			t.Error("模型列表必须GET")
		}
		writeJSON(w, 200, map[string]any{"data": []modelItem{{ID: "model-z"}, {ID: "model-a", Name: "模型 A"}, {ID: "model-a"}}})
	}))
	defer provider.Close()
	server, handler := testServer(t)
	server.settings = Settings{BaseURL: provider.URL + "/v1", Model: "saved-model", APIKey: "text-secret", ImageBaseURL: provider.URL + "/images", ImageAPIKey: "image-secret", TTSProvider: "openai", TTSBaseURL: provider.URL + "/audio", TTSAPIKey: "tts-secret"}
	for _, kind := range []string{"text", "image", "tts"} {
		input := map[string]any{"kind": kind, "baseUrl": provider.URL + "/v1", "model": "unsaved-model", "imageBaseUrl": provider.URL + "/images", "ttsProvider": "openai", "ttsBaseUrl": provider.URL + "/audio", "apiKey": "", "imageApiKey": "", "ttsApiKey": ""}
		response := requestJSON(t, handler, "POST", "/api/models", input)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var result struct {
			Models []modelItem `json:"models"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Models) != 2 || result.Models[0].ID != "model-a" {
			t.Fatal("模型列表解析、去重或顺序不正确")
		}
		paths := map[string]string{"text": "/v1/models", "image": "/images/models", "tts": "/audio/models"}
		if receivedPath != paths[kind] || authorization != "Bearer "+kind+"-secret" {
			t.Fatalf("%s未使用对应未保存配置: %s %s", kind, receivedPath, authorization)
		}
	}
	if server.currentSettings().Model != "saved-model" {
		t.Fatal("模型查询保存了临时配置")
	}
	if _, err := os.Stat(filepath.Join(server.dataDir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("模型查询不应写入设置文件")
	}
}

func TestKeysStayWithTheirProviderAndAddress(t *testing.T) {
	previous := Settings{BaseURL: "https://text.example/v1", APIKey: "text-private", ImageBaseURL: "https://image.example/v1", ImageAPIKey: "image-private", TTSProvider: "openai", TTSBaseURL: "https://voice.example/v1", TTSAPIKey: "voice-private"}
	same := preserveKeys(Settings{BaseURL: " https://text.example/v1/ ", ImageBaseURL: "https://image.example/v1/", TTSProvider: "openai", TTSBaseURL: "https://voice.example/v1/"}, previous)
	if same.APIKey != previous.APIKey || same.ImageAPIKey != previous.ImageAPIKey || same.TTSAPIKey != previous.TTSAPIKey {
		t.Fatal("相同地址的空密钥未保留")
	}
	changed := preserveKeys(Settings{BaseURL: "https://other-text.example/v1", ImageBaseURL: "https://image.example/v2", TTSProvider: "mimo", TTSBaseURL: "https://voice.example/v1"}, previous)
	if changed.APIKey != "" || changed.ImageAPIKey != "" || changed.TTSAPIKey != "" {
		t.Fatal("密钥被传至新地址或新供应商")
	}
	changed = preserveKeys(Settings{TTSProvider: "openai", TTSBaseURL: "https://voice.example/v2"}, previous)
	if changed.TTSAPIKey != "" {
		t.Fatal("TTS路径改变仍沿用了旧密钥")
	}
}

func TestModelsDoesNotSendOldKeyToNewAddress(t *testing.T) {
	var authorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"data": []modelItem{{ID: "new-model"}}})
	}))
	defer provider.Close()
	server, handler := testServer(t)
	server.settings = Settings{BaseURL: "https://old.example/v1", APIKey: "old-secret"}
	response := requestJSON(t, handler, "POST", "/api/models", map[string]any{"kind": "text", "baseUrl": provider.URL})
	if response.Code != 200 || authorization != "" {
		t.Fatal("模型查询向新地址发送了旧服务密钥")
	}
}

func TestModelsReportsProviderFailureWithoutLeakingKey(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, map[string]any{"error": map[string]string{"message": "Invalid rejected-secret"}})
	}))
	defer provider.Close()
	_, handler := testServer(t)
	response := requestJSON(t, handler, "POST", "/api/models", map[string]any{"kind": "text", "baseUrl": provider.URL, "apiKey": "rejected-secret"})
	if response.Code != 502 || strings.Contains(response.Body.String(), "rejected-secret") || !strings.Contains(response.Body.String(), "401") {
		t.Fatal("模型错误未明确反馈或泄露密钥")
	}
}

func TestTTSSettingsAreEncryptedAndRedacted(t *testing.T) {
	server, handler := testServer(t)
	settings := Settings{TTSProvider: "mimo", TTSBaseURL: "https://api.xiaomimimo.com/v1", TTSModel: "mimo-v2.5-tts", TTSAPIKey: "tts-private-key", TTSVoice: "mimo_default", TTSSpeed: 1.25}
	response := requestJSON(t, handler, "PUT", "/api/settings", settings)
	if response.Code != 200 || strings.Contains(response.Body.String(), "tts-private-key") || !strings.Contains(response.Body.String(), `"hasTtsApiKey":true`) {
		t.Fatal("TTS密钥未正确脱敏")
	}
	settings.TTSAPIKey = ""
	response = requestJSON(t, handler, "PUT", "/api/settings", settings)
	if response.Code != 200 || server.currentSettings().TTSAPIKey != "tts-private-key" {
		t.Fatal("空TTS密钥覆盖保存密钥")
	}
	data, err := os.ReadFile(filepath.Join(server.dataDir, "settings.json"))
	if err != nil || bytes.Contains(data, []byte("tts-private-key")) {
		t.Fatal("TTS密钥未加密保存")
	}
	reopened, err := NewServer(server.dataDir, server.distDir)
	if err != nil || reopened.currentSettings().TTSAPIKey != "tts-private-key" || reopened.currentSettings().TTSSpeed != 1.25 {
		t.Fatalf("TTS设置未保存: %v", err)
	}
}

func TestAudioTrimAndFadeValidation(t *testing.T) {
	project := sampleProject()
	project.Audio = []AudioTrack{{ID: "audio-1", Src: "/uploads/test.wav", Start: 0, TrimStart: 20, Duration: 50, SourceDuration: 80, Volume: 1, FadeIn: 10, FadeOut: 20}}
	if err := validateProject(&project); err != nil {
		t.Fatal(err)
	}
	project.Audio[0].SourceDuration = 60
	if err := validateProject(&project); err == nil {
		t.Fatal("裁剪超出素材时长未拒绝")
	}
	project.Audio[0].SourceDuration = 80
	project.Audio[0].FadeIn = 51
	if err := validateProject(&project); err == nil {
		t.Fatal("淡入超出片段时长未拒绝")
	}
}
