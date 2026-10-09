package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageGenerationNormalizesVersionPath(t *testing.T) {
	data := testPNG(t)
	for _, test := range []struct {
		base string
		path string
	}{
		{base: "/V1", path: "/v1/images/generations"},
		{base: "/V1/", path: "/v1/images/generations"},
		{base: "/v1", path: "/v1/images/generations"},
		{base: "/Proxy/V1", path: "/Proxy/v1/images/generations"},
	} {
		t.Run(test.base, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("生图请求地址错误：%s", r.URL.Path)
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("<!doctype html><html><body>首页</body></html>"))
					return
				}
				var payload struct {
					Model  string `json:"model"`
					Prompt string `json:"prompt"`
					Size   string `json:"size"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload.Model != "gpt-image-2" || payload.Prompt != "红色图形" || payload.Size != "1024x1024" {
					t.Errorf("生图参数错误：%+v", payload)
				}
				writeJSON(w, 200, map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
			}))
			defer provider.Close()
			server, handler := testServer(t)
			server.settings = Settings{ImageBaseURL: provider.URL + test.base, ImageModel: "gpt-image-2"}
			response := requestJSON(t, handler, http.MethodPost, "/api/images/generate", map[string]any{"prompt": "红色图形", "width": 1024, "height": 1024})
			if response.Code != http.StatusOK {
				t.Fatalf("生图返回 %d: %s", response.Code, response.Body.String())
			}
			var asset map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
				t.Fatal(err)
			}
			loaded := requestJSON(t, handler, http.MethodGet, asset["url"], nil)
			if loaded.Code != http.StatusOK || !bytes.Equal(loaded.Body.Bytes(), data) {
				t.Fatal("生图结果没有保存成可读取素材")
			}
		})
	}
}

func TestSettingsVersionCorrectionRetainsKeysOnlyForSameHost(t *testing.T) {
	server, handler := testServer(t)
	server.settings = Settings{
		BaseURL: "https://text.example/Proxy/V1", APIKey: "text-private-key",
		ImageBaseURL: "https://image.example/Proxy/V1", ImageAPIKey: "image-private-key",
		TTSProvider: "openai", TTSBaseURL: "https://speech.example/Proxy/V1", TTSAPIKey: "speech-private-key",
	}
	response := requestJSON(t, handler, http.MethodPut, "/api/settings", Settings{
		BaseURL: "https://text.example/Proxy/v1", ImageBaseURL: "https://image.example/Proxy/v1",
		TTSProvider: "openai", TTSBaseURL: "https://speech.example/Proxy/v1",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("纠正地址大小写返回 %d: %s", response.Code, response.Body.String())
	}
	stored := server.currentSettings()
	if stored.APIKey != "text-private-key" || stored.ImageAPIKey != "image-private-key" || stored.TTSAPIKey != "speech-private-key" {
		t.Fatal("纠正版本路径大小写导致已保存密钥丢失")
	}
	if stored.BaseURL != "https://text.example/Proxy/v1" || stored.ImageBaseURL != "https://image.example/Proxy/v1" || stored.TTSBaseURL != "https://speech.example/Proxy/v1" {
		t.Fatal("版本路径没有纠正或自定义路径大小写被修改")
	}
	if strings.Contains(response.Body.String(), "private-key") {
		t.Fatal("更新设置泄露了已保存密钥")
	}
	response = requestJSON(t, handler, http.MethodPut, "/api/settings", Settings{
		BaseURL: "https://other-text.example/Proxy/V1", ImageBaseURL: "https://other-image.example/Proxy/V1",
		TTSProvider: "openai", TTSBaseURL: "https://other-speech.example/Proxy/V1",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("更换服务地址返回 %d: %s", response.Code, response.Body.String())
	}
	stored = server.currentSettings()
	if stored.APIKey != "" || stored.ImageAPIKey != "" || stored.TTSAPIKey != "" {
		t.Fatal("版本路径纠正后仍把旧密钥带到新服务地址")
	}
}

func TestImageGenerationDiagnosesHTMLWithoutLeakingResponse(t *testing.T) {
	for _, contentType := range []string{"text/html; charset=utf-8", "application/octet-stream"} {
		t.Run(contentType, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte(" \n<!DOCTYPE html><html><body>上游敏感原文 image-private-key</body></html>"))
			}))
			defer provider.Close()
			server, handler := testServer(t)
			server.settings = Settings{ImageBaseURL: provider.URL, ImageModel: "gpt-image-2", ImageAPIKey: "image-private-key"}
			response := requestJSON(t, handler, http.MethodPost, "/api/images/generate", map[string]any{"prompt": "红色图形"})
			if response.Code != http.StatusBadGateway {
				t.Fatalf("网页响应返回 %d: %s", response.Code, response.Body.String())
			}
			var failure struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(failure.Error, "网页") || !strings.Contains(failure.Error, "地址") {
				t.Fatalf("未说明模型接口返回网页及地址问题：%s", failure.Error)
			}
			if strings.Contains(failure.Error, "上游敏感原文") || strings.Contains(failure.Error, "image-private-key") || strings.Contains(failure.Error, "<html>") {
				t.Fatalf("错误响应泄露了上游原文：%s", failure.Error)
			}
		})
	}
}

func TestImageGenerationAcceptsJSONWithBOM(t *testing.T) {
	data := testPNG(t)
	body, err := json.Marshal(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append([]byte{0xef, 0xbb, 0xbf}, body...))
	}))
	defer provider.Close()
	server, handler := testServer(t)
	server.settings = Settings{ImageBaseURL: provider.URL, ImageModel: "gpt-image-2"}
	response := requestJSON(t, handler, http.MethodPost, "/api/images/generate", map[string]any{"prompt": "红色图形"})
	if response.Code != http.StatusOK {
		t.Fatalf("带 BOM 的生图结果返回 %d: %s", response.Code, response.Body.String())
	}
	var asset map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	loaded := requestJSON(t, handler, http.MethodGet, asset["url"], nil)
	if loaded.Code != http.StatusOK || !bytes.Equal(loaded.Body.Bytes(), data) {
		t.Fatal("带 BOM 的生图结果没有保存成可读取素材")
	}
}
