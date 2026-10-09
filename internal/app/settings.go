package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) currentSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func publicSettings(settings Settings) Settings {
	settings.HasAPIKey = settings.APIKey != ""
	settings.HasImageAPIKey = settings.ImageAPIKey != ""
	settings.HasTTSAPIKey = settings.TTSAPIKey != ""
	settings.APIKey = ""
	settings.ImageAPIKey = ""
	settings.TTSAPIKey = ""
	settings = settingsDefaults(settings)
	return settings
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, publicSettings(s.currentSettings()))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if !decodeJSON(w, r, &settings) {
		return
	}
	if err := validateSettings(settings); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	settings = preserveKeys(settings, s.settings)
	if err := s.saveSettings(settings); err != nil {
		errorJSON(w, 500, "模型设置保存失败")
		return
	}
	s.settings = settings
	writeJSON(w, 200, publicSettings(settings))
}

type encryptedSettings struct {
	Nonce string `json:"nonce"`
	Data  string `json:"data"`
}

func (s *Server) settingsKey() ([]byte, error) {
	path := filepath.Join(s.dataDir, ".settings.key")
	key, err := os.ReadFile(path)
	if err == nil && len(key) == 32 {
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err == nil {
		return nil, errors.New("本机模型设置密钥已损坏")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Server) saveSettings(settings Settings) error {
	key, err := s.settingsKey()
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	envelope := encryptedSettings{Nonce: base64.RawStdEncoding.EncodeToString(nonce), Data: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, nil))}
	return atomicJSON(filepath.Join(s.dataDir, "settings.json"), envelope)
}

func (s *Server) loadSettings() (Settings, error) {
	var settings Settings
	path := filepath.Join(s.dataDir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return settings, err
	}
	var envelope encryptedSettings
	if json.Unmarshal(data, &envelope) == nil && envelope.Nonce != "" && envelope.Data != "" {
		key, keyErr := os.ReadFile(filepath.Join(s.dataDir, ".settings.key"))
		if keyErr != nil || len(key) != 32 {
			return settings, errors.New("本机模型设置密钥缺失或已损坏")
		}
		block, cipherErr := aes.NewCipher(key)
		if cipherErr != nil {
			return settings, cipherErr
		}
		gcm, cipherErr := cipher.NewGCM(block)
		if cipherErr != nil {
			return settings, cipherErr
		}
		nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
		sealed, dataErr := base64.RawStdEncoding.DecodeString(envelope.Data)
		if nonceErr != nil || dataErr != nil || len(nonce) != gcm.NonceSize() {
			return settings, errors.New("模型设置密文格式无效")
		}
		plain, openErr := gcm.Open(nil, nonce, sealed, nil)
		if openErr != nil {
			return settings, errors.New("模型设置无法解密，密钥可能已损坏")
		}
		decodeErr := json.Unmarshal(plain, &settings)
		return settings, decodeErr
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	if err := s.saveSettings(settings); err != nil {
		return settings, err
	}
	return settings, nil
}

func preserveKeys(settings, previous Settings) Settings {
	settings = settingsDefaults(settings)
	previous = settingsDefaults(previous)
	settings.APIKey = strings.TrimSpace(settings.APIKey)
	settings.ImageAPIKey = strings.TrimSpace(settings.ImageAPIKey)
	settings.TTSAPIKey = strings.TrimSpace(settings.TTSAPIKey)
	if strings.TrimSpace(settings.APIKey) == "" && settings.BaseURL == previous.BaseURL {
		settings.APIKey = previous.APIKey
	}
	if strings.TrimSpace(settings.ImageAPIKey) == "" && settings.ImageBaseURL == previous.ImageBaseURL {
		settings.ImageAPIKey = previous.ImageAPIKey
	}
	if strings.TrimSpace(settings.TTSAPIKey) == "" && settings.TTSProvider == previous.TTSProvider && settings.TTSBaseURL == previous.TTSBaseURL {
		settings.TTSAPIKey = previous.TTSAPIKey
	}
	settings.HasAPIKey = settings.APIKey != ""
	settings.HasImageAPIKey = settings.ImageAPIKey != ""
	settings.HasTTSAPIKey = settings.TTSAPIKey != ""
	settings.Model = strings.TrimSpace(settings.Model)
	settings.ImageModel = strings.TrimSpace(settings.ImageModel)
	settings.TTSModel = strings.TrimSpace(settings.TTSModel)
	settings.TTSVoice = strings.TrimSpace(settings.TTSVoice)
	return settings
}

func settingsDefaults(settings Settings) Settings {
	settings.BaseURL = normalizeProviderBaseURL(settings.BaseURL)
	settings.ImageBaseURL = normalizeProviderBaseURL(settings.ImageBaseURL)
	settings.TTSBaseURL = normalizeProviderBaseURL(settings.TTSBaseURL)
	if settings.TTSProvider == "" {
		settings.TTSProvider = "local"
	}
	if settings.TTSSpeed == 0 {
		settings.TTSSpeed = 1
	}
	return settings
}

func validateSettings(settings Settings) error {
	for _, value := range []string{settings.BaseURL, settings.ImageBaseURL, settings.TTSBaseURL} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("服务地址需要完整的 HTTP 或 HTTPS 地址，例如 https://api.openai.com/v1")
		}
	}
	if len(settings.APIKey) > 8192 || len(settings.ImageAPIKey) > 8192 || len(settings.TTSAPIKey) > 8192 || len(settings.Model) > 200 || len(settings.ImageModel) > 200 || len(settings.TTSModel) > 200 || len(settings.TTSVoice) > 200 || strings.ContainsAny(settings.APIKey+settings.ImageAPIKey+settings.TTSAPIKey, "\r\n") {
		return errors.New("模型名称或密钥格式无效")
	}
	if settings.TTSProvider != "" && settings.TTSProvider != "local" && settings.TTSProvider != "mimo" && settings.TTSProvider != "openai" {
		return errors.New("配音服务仅支持本地、MiMo 和 OpenAI 兼容接口")
	}
	if math.IsNaN(settings.TTSSpeed) || math.IsInf(settings.TTSSpeed, 0) || settings.TTSSpeed != 0 && (settings.TTSSpeed < .5 || settings.TTSSpeed > 2) {
		return errors.New("语速必须在 0.5 至 2 之间")
	}
	return nil
}

func (s *Server) testSettings(w http.ResponseWriter, r *http.Request) {
	input := struct {
		Settings
		Kind string `json:"kind"`
	}{Settings: publicSettings(s.currentSettings())}
	if !decodeJSON(w, r, &input) {
		return
	}
	settings := preserveKeys(input.Settings, s.currentSettings())
	if err := validateSettings(settings); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	if input.Kind == "image" {
		if settings.ImageBaseURL == "" || settings.ImageModel == "" {
			errorJSON(w, 400, "请先填写生图服务地址和模型名称")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "生图配置格式有效，尚未调用生图服务；生成图片时才会验证服务并产生费用", "verified": false})
		return
	}
	if input.Kind == "tts" {
		if settings.TTSProvider == "local" {
			if localSpeechTool() == "" {
				errorJSON(w, 503, "本地配音引擎不可用，请安装 espeak-ng 或选择远程配音服务")
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "message": "已找到本地配音引擎，尚未合成音频", "verified": false})
			return
		}
		if settings.TTSProvider == "openai" && (settings.TTSBaseURL == "" || settings.TTSModel == "") {
			errorJSON(w, 400, "请填写配音服务地址和模型名称")
			return
		}
		if settings.TTSProvider == "mimo" && settings.TTSAPIKey == "" {
			errorJSON(w, 400, "请填写 MiMo 配音服务密钥")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "配音配置格式有效，尚未调用配音服务；生成配音时才会验证服务并产生费用", "verified": false})
		return
	}
	if input.Kind != "" && input.Kind != "text" {
		errorJSON(w, 400, "测试类型无效")
		return
	}
	if settings.BaseURL == "" || settings.Model == "" {
		errorJSON(w, 400, "请先填写文本服务地址和模型名称")
		return
	}
	var response chatResponse
	request := map[string]any{"model": settings.Model, "messages": []map[string]string{{"role": "user", "content": "请只回复：连接成功"}}, "max_tokens": 20, "stream": false}
	if err := s.providerJSON(r.Context(), endpoint(settings.BaseURL, "chat/completions"), settings.APIKey, request, &response); err != nil {
		errorJSON(w, 502, "文本服务测试失败: "+err.Error())
		return
	}
	if len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		errorJSON(w, 502, "文本服务返回了空响应，请检查模型名称和兼容接口")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "文本模型连接成功", "verified": true})
}

func (s *Server) providerJSON(ctx context.Context, target, apiKey string, payload, output any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return errors.New("无法创建模型请求")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
		if strings.EqualFold(request.URL.Hostname(), "api.xiaomimimo.com") {
			request.Header.Set("api-key", apiKey)
		}
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errors.New("请求已取消")
		}
		return errors.New("模型服务无法连接或等待超时，请检查地址、网络与服务状态")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20+1))
	if err != nil || len(body) > 32<<20 {
		return errors.New("模型响应读取失败或超过 32 MB 限制")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		message := ""
		if json.Unmarshal(body, &failure) == nil && failure.Error.Message != "" {
			message = failure.Error.Message
			if apiKey != "" {
				message = strings.ReplaceAll(message, apiKey, "[密钥已隐藏]")
			}
			if len([]rune(message)) > 500 {
				message = string([]rune(message)[:500])
			}
			message = ": " + message
		}
		return fmt.Errorf("服务返回 HTTP %d%s", response.StatusCode, message)
	}
	body = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte{0xef, 0xbb, 0xbf}))
	if len(body) == 0 {
		return errors.New("模型服务返回空响应，请重试或检查服务状态")
	}
	if !json.Valid(body) {
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		prefix := strings.ToLower(string(body[:min(len(body), 256)]))
		if strings.Contains(contentType, "text/html") || strings.HasPrefix(prefix, "<!doctype html") || strings.HasPrefix(prefix, "<html") {
			return errors.New("模型服务返回了网页，请检查服务地址与接口路径，OpenAI 兼容接口通常使用小写 /v1")
		}
		if strings.Contains(contentType, "text/event-stream") || strings.HasPrefix(prefix, "data:") {
			return errors.New("模型服务返回了流式响应，当前接口需要非流式 JSON，请检查服务设置")
		}
		return errors.New("模型服务返回的内容不是有效 JSON，请检查服务地址、接口路径与服务状态")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return errors.New("模型服务返回的 JSON 结构不兼容，请检查模型名称与接口类型")
	}
	return nil
}
