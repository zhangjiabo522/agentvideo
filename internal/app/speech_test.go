package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func testSpeechWAV() []byte {
	const sampleRate = 16000
	data := make([]byte, 44+sampleRate*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], sampleRate)
	binary.LittleEndian.PutUint32(data[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], sampleRate*2)
	return data
}

func speechTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func requireSpeechProbe(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("未安装 ffprobe")
	}
}

func TestSpeechOpenAICompatibilityAndSavedDuration(t *testing.T) {
	requireSpeechProbe(t)
	type call struct {
		Path          string
		Authorization string
		Payload       map[string]any
	}
	requests := make(chan call, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		requests <- call{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Payload: payload}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testSpeechWAV())
	}))
	defer provider.Close()
	server := speechTestServer(t)
	server.settings = Settings{TTSProvider: "openai", TTSBaseURL: provider.URL + "/v1", TTSModel: "tts-test", TTSAPIKey: "tts-secret", TTSVoice: "alloy", TTSSpeed: 1}
	response := requestJSON(t, http.HandlerFunc(server.speech), "POST", "/api/speech", map[string]any{"text": "你好，映序。", "speed": 1.25})
	if response.Code != 200 {
		t.Fatalf("生成配音失败 %d: %s", response.Code, response.Body.String())
	}
	request := <-requests
	if request.Path != "/v1/audio/speech" || request.Authorization != "Bearer tts-secret" || request.Payload["input"] != "你好，映序。" || request.Payload["model"] != "tts-test" || request.Payload["speed"] != 1.25 || request.Payload["response_format"] != "wav" {
		t.Fatalf("OpenAI 协议不符: %+v", request)
	}
	var result struct {
		URL      string `json:"url"`
		Duration int    `json:"duration"`
		Provider string `json:"provider"`
		Type     string `json:"type"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Duration != 30 || result.Provider != "openai" || result.Type != "audio/wav" || !strings.HasPrefix(result.URL, "/uploads/") {
		t.Fatalf("音频响应不符: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(server.dataDir, strings.TrimPrefix(result.URL, "/")))
	if err != nil || !bytes.Equal(data, testSpeechWAV()) {
		t.Fatal("配音未以真实音频保存至素材库")
	}
}

func TestSpeechMiMoAssistantTextAndAPIKey(t *testing.T) {
	requireSpeechProbe(t)
	type call struct {
		Key     string
		Path    string
		Payload map[string]any
	}
	requests := make(chan call, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		requests <- call{Key: r.Header.Get("api-key"), Path: r.URL.Path, Payload: payload}
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]string{"data": base64.StdEncoding.EncodeToString(testSpeechWAV())}}}}})
	}))
	defer provider.Close()
	server := speechTestServer(t)
	server.settings = Settings{TTSProvider: "mimo", TTSBaseURL: provider.URL + "/v1", TTSAPIKey: "mimo-secret"}
	response := requestJSON(t, http.HandlerFunc(server.speech), "POST", "/api/speech", map[string]any{"text": "测试中文配音", "voice": "冰糖", "instructions": "温柔自然"})
	if response.Code != 200 {
		t.Fatalf("MiMo 配音失败 %d: %s", response.Code, response.Body.String())
	}
	request := <-requests
	messages, ok := request.Payload["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("MiMo messages 无效: %+v", request.Payload)
	}
	user, assistant := messages[0].(map[string]any), messages[1].(map[string]any)
	audio := request.Payload["audio"].(map[string]any)
	if request.Key != "mimo-secret" || request.Path != "/v1/chat/completions" || request.Payload["model"] != "mimo-v2.5-tts" || user["role"] != "user" || user["content"] != "温柔自然" || assistant["role"] != "assistant" || assistant["content"] != "测试中文配音" || audio["voice"] != "冰糖" || audio["format"] != "wav" {
		t.Fatalf("MiMo 协议不符: %+v", request)
	}
	if _, found := request.Payload["speed"]; found {
		t.Fatal("MiMo 请求包含未支持的 speed 参数")
	}
	if strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), `"duration":30`) {
		t.Fatal("MiMo 音频响应无效")
	}
}

func TestSpeechLocalCreatesChineseAudio(t *testing.T) {
	requireSpeechProbe(t)
	tool, err := exec.LookPath("espeak-ng")
	if err != nil {
		tool, err = filepath.Abs(filepath.Join("..", "..", ".cache", "tts", "espeak-ng", "usr", "bin", "espeak-ng"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tool); err != nil {
			t.Skip("本机尚未安装 espeak-ng")
		}
	}
	t.Setenv("LOCAL_TTS_PATH", tool)
	server := speechTestServer(t)
	response := requestJSON(t, http.HandlerFunc(server.speech), "POST", "/api/speech", map[string]any{"text": "你好，欢迎使用映序。"})
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"provider":"local"`) {
		t.Fatalf("默认本地配音失败 %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		URL      string `json:"url"`
		Duration int    `json:"duration"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(server.dataDir, strings.TrimPrefix(result.URL, "/")))
	if err != nil || len(data) < 1000 || string(data[:4]) != "RIFF" || result.Duration < 30 {
		t.Fatalf("本地中文未生成有效 WAV: duration=%d bytes=%d err=%v", result.Duration, len(data), err)
	}
	server.settings = Settings{TTSProvider: "openai", TTSBaseURL: "https://example.com/v1", TTSAPIKey: "saved-key", TTSVoice: "alloy", TTSModel: "tts"}
	response = requestJSON(t, server.Handler(), "POST", "/api/speech", map[string]any{"text": "本地配音仍然可用。", "provider": "local"})
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"provider":"local"`) {
		t.Fatalf("远程配置阻止本地配音: %d %s", response.Code, response.Body.String())
	}
}

func TestSpeechRejectsUnsavedRemoteProviderBeforeCallingService(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer provider.Close()
	for _, test := range []struct {
		Saved     string
		Requested string
	}{
		{Saved: "openai", Requested: "mimo"},
		{Saved: "mimo", Requested: "openai"},
		{Saved: "local", Requested: "openai"},
		{Saved: "", Requested: "mimo"},
	} {
		t.Run(test.Saved+"-"+test.Requested, func(t *testing.T) {
			server := speechTestServer(t)
			server.settings = Settings{TTSProvider: test.Saved, TTSBaseURL: provider.URL, TTSAPIKey: "saved-key", TTSModel: "saved-model"}
			response := requestJSON(t, server.Handler(), "POST", "/api/speech", map[string]any{"text": "测试配音", "provider": test.Requested})
			if response.Code != 400 || !strings.Contains(response.Body.String(), "请在语音设置中切换并保存该服务后再生成") {
				t.Fatalf("未保存的远程服务未被拒绝: %d %s", response.Code, response.Body.String())
			}
			if server.currentSettings().TTSProvider != test.Saved || server.currentSettings().TTSAPIKey != "saved-key" {
				t.Fatal("请求改变了已保存的语音配置")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("未保存的远程服务请求已发送到模型服务")
	}
}

func TestSpeechMiMoVoiceDesignOmitsPresetVoice(t *testing.T) {
	requireSpeechProbe(t)
	requests := make(chan map[string]any, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		requests <- payload
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]string{"data": base64.StdEncoding.EncodeToString(testSpeechWAV())}}}}})
	}))
	defer provider.Close()
	server := speechTestServer(t)
	server.settings = Settings{TTSProvider: "mimo", TTSBaseURL: provider.URL + "/v1", TTSAPIKey: "mimo-secret", TTSModel: "mimo-v2.5-tts-voicedesign"}
	response := requestJSON(t, http.HandlerFunc(server.speech), "POST", "/api/speech", map[string]any{"text": "测试中文配音", "instructions": "清亮的青年女性声音"})
	if response.Code != 200 {
		t.Fatalf("MiMo 音色设计失败 %d: %s", response.Code, response.Body.String())
	}
	payload := <-requests
	audio := payload["audio"].(map[string]any)
	if _, found := audio["voice"]; found {
		t.Fatal("MiMo 音色设计请求包含预置 voice")
	}
	if _, found := audio["optimize_text_preview"]; found {
		t.Fatal("MiMo 音色设计改变了输入文本")
	}
	if !strings.Contains(response.Body.String(), `"durationSeconds":1`) {
		t.Fatal("配音响应缺少实际秒数")
	}
}

func TestSpeechRejectsBadParametersAndProviderResponse(t *testing.T) {
	server := speechTestServer(t)
	handler := http.HandlerFunc(server.speech)
	for _, input := range []map[string]any{
		{"text": ""},
		{"text": "你好", "provider": "unknown"},
		{"text": "你好", "speed": 0.1},
		{"text": "你好", "provider": "local", "voice": "alloy"},
		{"text": strings.Repeat("字", speechMaxText+1)},
	} {
		response := requestJSON(t, handler, "POST", "/api/speech", input)
		if response.Code != 400 {
			t.Fatalf("无效请求被接受: %d %s", response.Code, response.Body.String())
		}
	}
	server.settings = Settings{TTSProvider: "mimo", TTSAPIKey: "secret", TTSModel: "mimo-v2.5-tts-voicedesign"}
	response := requestJSON(t, handler, "POST", "/api/speech", map[string]any{"text": "测试"})
	if response.Code != 400 || !strings.Contains(response.Body.String(), "音色描述") {
		t.Fatal("MiMo 音色设计未校验必填描述")
	}
	server.settings.TTSModel = "mimo-v2.5-tts-unknown"
	response = requestJSON(t, handler, "POST", "/api/speech", map[string]any{"text": "测试"})
	if response.Code != 400 {
		t.Fatal("未知 MiMo 配音模型被接受")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"error": "not audio"})
	}))
	defer provider.Close()
	server.settings = Settings{TTSProvider: "openai", TTSBaseURL: provider.URL, TTSModel: "tts"}
	response = requestJSON(t, handler, "POST", "/api/speech", map[string]any{"text": "测试"})
	if response.Code != 502 {
		t.Fatal("JSON 伪音频被接受")
	}
}

func TestSpeechRedactsProviderSecret(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, map[string]any{"error": map[string]string{"message": "bad token top-secret"}})
	}))
	defer provider.Close()
	server := speechTestServer(t)
	server.settings = Settings{TTSProvider: "openai", TTSBaseURL: provider.URL, TTSModel: "tts", TTSAPIKey: "top-secret"}
	response := requestJSON(t, http.HandlerFunc(server.speech), "POST", "/api/speech", map[string]any{"text": "测试"})
	if response.Code != 502 || strings.Contains(response.Body.String(), "top-secret") {
		t.Fatal("错误响应泄露配音密钥")
	}
}

func TestSpeechTempoPreservesAudioAndChangesDuration(t *testing.T) {
	requireSpeechProbe(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("未安装 ffmpeg")
	}
	server := speechTestServer(t)
	data, err := speechTempo(context.Background(), testSpeechWAV(), 2, server.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	duration, err := speechDuration(context.Background(), data, ".wav", server.dataDir)
	if err != nil || duration > 0.6 || duration < 0.3 {
		t.Fatalf("语速未改变音频时长: duration=%f err=%v", duration, err)
	}
}
