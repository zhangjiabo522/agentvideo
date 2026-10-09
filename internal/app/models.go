package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type modelItem struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
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
	var baseURL, key string
	switch input.Kind {
	case "text":
		baseURL, key = settings.BaseURL, settings.APIKey
	case "image":
		baseURL, key = settings.ImageBaseURL, settings.ImageAPIKey
	case "tts":
		if settings.TTSProvider == "local" {
			errorJSON(w, 400, "本地配音使用本机语音引擎，无需拉取远程模型")
			return
		}
		baseURL, key = settings.TTSBaseURL, settings.TTSAPIKey
	default:
		errorJSON(w, 400, "模型类型必须是 text、image 或 tts")
		return
	}
	if baseURL == "" {
		errorJSON(w, 400, "请先填写模型服务地址")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), "GET", endpoint(baseURL, "models"), nil)
	if err != nil {
		errorJSON(w, 400, "模型服务地址无效")
		return
	}
	request.Header.Set("Accept", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
		if strings.EqualFold(request.URL.Hostname(), "api.xiaomimimo.com") {
			request.Header.Set("api-key", key)
		}
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		message := "模型列表查询失败，请检查服务地址、网络与密钥"
		if errors.Is(err, r.Context().Err()) {
			message = "模型列表查询已取消"
		}
		errorJSON(w, 502, message)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 {
		errorJSON(w, 502, "模型列表响应无法读取或超过 4 MB 限制")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		message := fmt.Sprintf("模型列表查询返回 HTTP %d", response.StatusCode)
		if json.Unmarshal(body, &failure) == nil && failure.Error.Message != "" {
			detail := failure.Error.Message
			if key != "" {
				detail = strings.ReplaceAll(detail, key, "[密钥已隐藏]")
			}
			if len([]rune(detail)) > 300 {
				detail = string([]rune(detail)[:300])
			}
			message += ": " + detail
		}
		errorJSON(w, 502, message)
		return
	}
	var result struct {
		Data []modelItem `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Data == nil {
		errorJSON(w, 502, "模型列表格式不兼容，需要 OpenAI 格式的 data 数组")
		return
	}
	models := make([]modelItem, 0, len(result.Data))
	seen := map[string]bool{}
	for _, model := range result.Data {
		if model.ID == "" || len(model.ID) > 300 || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	writeJSON(w, 200, map[string]any{"models": models})
}
