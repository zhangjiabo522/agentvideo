package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	speechMaxText   = 12000
	speechMaxData   = 30 << 20
	speechFrameRate = 30
)

type speechRequest struct {
	Text         string  `json:"text"`
	Provider     string  `json:"provider"`
	Voice        string  `json:"voice"`
	Speed        float64 `json:"speed"`
	Model        string  `json:"model"`
	Instructions string  `json:"instructions"`
}

type speechResult struct {
	Data     []byte
	Ext      string
	Type     string
	Provider string
}

func (s *Server) speech(w http.ResponseWriter, r *http.Request) {
	var input speechRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	text := strings.TrimSpace(input.Text)
	if text == "" || len([]rune(text)) > speechMaxText {
		errorJSON(w, 400, "配音文本需要包含 1 至 12000 个字符")
		return
	}
	settings := s.currentSettings()
	savedProvider := strings.ToLower(strings.TrimSpace(settings.TTSProvider))
	if savedProvider == "" {
		savedProvider = "local"
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider == "" {
		provider = savedProvider
	}
	if provider != "local" && provider != "espeak" && provider != "espeak-ng" && provider != savedProvider {
		errorJSON(w, 400, "请在语音设置中切换并保存该服务后再生成")
		return
	}
	speed := input.Speed
	if speed == 0 {
		speed = settings.TTSSpeed
	}
	if speed == 0 {
		speed = 1
	}
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0.5 || speed > 2 {
		errorJSON(w, 400, "语速需要在 0.5 至 2.0 之间")
		return
	}
	voice := strings.TrimSpace(input.Voice)
	if voice == "" && (input.Provider == "" || provider == settings.TTSProvider) {
		voice = strings.TrimSpace(settings.TTSVoice)
	}
	if voice == "" {
		voice = defaultSpeechVoice(provider)
	}
	model := strings.TrimSpace(input.Model)
	if model == "" && (input.Provider == "" || provider == settings.TTSProvider) {
		model = strings.TrimSpace(settings.TTSModel)
	}
	instructions := strings.TrimSpace(input.Instructions)
	if len([]rune(voice)) > 120 || len([]rune(model)) > 200 || len([]rune(instructions)) > 2000 {
		errorJSON(w, 400, "配音参数过长")
		return
	}
	result, err := s.synthesize(r.Context(), provider, text, voice, speed, model, instructions)
	if err != nil {
		status := 502
		if strings.HasPrefix(err.Error(), "参数错误") || strings.HasPrefix(err.Error(), "本地配音") {
			status = 400
		}
		errorJSON(w, status, err.Error())
		return
	}
	if len(result.Data) == 0 || len(result.Data) > speechMaxData {
		errorJSON(w, 502, "配音服务返回的音频无效或超过 30 MB 限制")
		return
	}
	duration, err := speechDuration(r.Context(), result.Data, result.Ext, s.dataDir)
	if err != nil {
		errorJSON(w, 502, "配音音频无法解析，请重试")
		return
	}
	assetURL, err := s.saveAsset(result.Data, result.Ext)
	if err != nil {
		errorJSON(w, 500, "配音音频保存失败")
		return
	}
	writeJSON(w, 200, map[string]any{"url": assetURL, "name": speechName(result.Provider), "type": result.Type, "duration": int(math.Ceil(duration * speechFrameRate)), "durationSeconds": duration, "provider": result.Provider})
}

func defaultSpeechVoice(provider string) string {
	if provider == "local" || provider == "espeak" || provider == "espeak-ng" {
		return "cmn"
	}
	if provider == "mimo" {
		return "mimo_default"
	}
	return "alloy"
}

func speechName(provider string) string {
	switch provider {
	case "local":
		return "本地中文配音"
	case "mimo":
		return "MiMo 配音"
	default:
		return "AI 配音"
	}
}

func (s *Server) synthesize(ctx context.Context, provider, text, voice string, speed float64, model, instructions string) (speechResult, error) {
	switch provider {
	case "local", "espeak", "espeak-ng":
		if voice != "" && voice != "cmn" && voice != "zh" && voice != "zh-cmn" {
			return speechResult{}, errors.New("参数错误: 本地配音仅支持 cmn 中文音色")
		}
		data, err := localSpeech(ctx, text, speed, s.dataDir)
		if err != nil {
			return speechResult{}, err
		}
		return speechResult{Data: data, Ext: ".wav", Type: "audio/wav", Provider: "local"}, nil
	case "openai", "openai-compatible", "custom":
		return s.openAISpeech(ctx, text, voice, speed, model, provider)
	case "mimo":
		return s.miMoSpeech(ctx, text, voice, speed, model, instructions)
	default:
		return speechResult{}, errors.New("参数错误: 不支持的配音服务，可选 local、openai、mimo")
	}
}

func localSpeech(ctx context.Context, text string, speed float64, dataDir string) ([]byte, error) {
	tool := localSpeechTool()
	if tool == "" {
		return nil, errors.New("本地配音不可用: 未找到 espeak-ng，请安装本地中文语音引擎")
	}
	temp, err := os.CreateTemp(filepath.Join(dataDir, "uploads"), ".tts-*.wav")
	if err != nil {
		return nil, errors.New("本地配音临时文件创建失败")
	}
	target := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(target)
		return nil, errors.New("本地配音临时文件创建失败")
	}
	defer os.Remove(target)
	wordsPerMinute := strconv.Itoa(int(math.Round(175 * speed)))
	runContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(runContext, tool, "-v", "cmn", "-s", wordsPerMinute, "-w", target, "--", text)
	if output, err := command.CombinedOutput(); err != nil {
		if runContext.Err() != nil {
			return nil, errors.New("本地配音超时，请缩短文本后重试")
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = "语音引擎执行失败"
		}
		return nil, errors.New("本地配音失败: " + message)
	}
	data, err := os.ReadFile(target)
	if err != nil || len(data) == 0 {
		return nil, errors.New("本地配音未生成音频")
	}
	return data, nil
}

func localSpeechTool() string {
	if configured := strings.TrimSpace(os.Getenv("LOCAL_TTS_PATH")); configured != "" {
		if tool, err := exec.LookPath(configured); err == nil {
			return tool
		}
		return ""
	}
	if tool, err := exec.LookPath("espeak-ng"); err == nil {
		return tool
	}
	root := os.Getenv("VIDEO_ROOT")
	if root == "" {
		root = "."
	}
	for _, candidate := range []string{"/usr/bin/espeak-ng", "/usr/local/bin/espeak-ng", filepath.Join(root, ".cache", "tts", "espeak-ng", "usr", "bin", "espeak-ng")} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate
		}
	}
	return ""
}

func (s *Server) openAISpeech(ctx context.Context, text, voice string, speed float64, model, provider string) (speechResult, error) {
	settings := s.currentSettings()
	base := strings.TrimRight(strings.TrimSpace(settings.TTSBaseURL), "/")
	if base == "" {
		return speechResult{}, errors.New("参数错误: 请先填写 OpenAI 兼容配音服务地址")
	}
	if model == "" {
		return speechResult{}, errors.New("参数错误: 请先填写配音模型名称")
	}
	if voice == "" {
		voice = "alloy"
	}
	payload := map[string]any{"model": model, "input": text, "voice": voice, "speed": speed, "response_format": "wav"}
	data, contentType, err := s.audioRequest(ctx, endpoint(base, "audio/speech"), settings.TTSAPIKey, "Authorization", payload)
	if err != nil {
		return speechResult{}, err
	}
	ext, kind := speechAudioFormat(data, contentType)
	if ext == "" {
		return speechResult{}, errors.New("OpenAI 配音服务返回的不是支持的音频格式")
	}
	return speechResult{Data: data, Ext: ext, Type: kind, Provider: provider}, nil
}

func (s *Server) miMoSpeech(ctx context.Context, text, voice string, speed float64, model, instructions string) (speechResult, error) {
	settings := s.currentSettings()
	base := strings.TrimRight(strings.TrimSpace(settings.TTSBaseURL), "/")
	if base == "" {
		base = "https://api.xiaomimimo.com/v1"
	}
	if model == "" {
		model = "mimo-v2.5-tts"
	}
	if model != "mimo-v2.5-tts" && model != "mimo-v2.5-tts-voicedesign" {
		return speechResult{}, errors.New("参数错误: MiMo 仅支持 mimo-v2.5-tts 或 mimo-v2.5-tts-voicedesign")
	}
	if settings.TTSAPIKey == "" {
		return speechResult{}, errors.New("参数错误: 请先填写 MiMo API 密钥")
	}
	if voice == "" {
		voice = "mimo_default"
	}
	if model == "mimo-v2.5-tts" {
		validVoice := false
		for _, name := range []string{"mimo_default", "冰糖", "茉莉", "苏打", "白桦", "Mia", "Chloe", "Milo", "Dean"} {
			if voice == name {
				validVoice = true
				break
			}
		}
		if !validVoice {
			return speechResult{}, errors.New("参数错误: MiMo 预置音色名称无效")
		}
	}
	if model == "mimo-v2.5-tts-voicedesign" && instructions == "" {
		return speechResult{}, errors.New("参数错误: MiMo 音色设计需要填写音色描述")
	}
	messages := []map[string]string{{"role": "assistant", "content": text}}
	if instructions != "" {
		messages = append([]map[string]string{{"role": "user", "content": instructions}}, messages...)
	}
	audio := map[string]string{"format": "wav"}
	if model == "mimo-v2.5-tts" {
		audio["voice"] = voice
	}
	payload := map[string]any{"model": model, "messages": messages, "audio": audio, "stream": false}
	data, _, err := s.audioRequest(ctx, endpoint(base, "chat/completions"), settings.TTSAPIKey, "api-key", payload)
	if err != nil {
		return speechResult{}, err
	}
	var response struct {
		Choices []struct {
			Message struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &response); err != nil || len(response.Choices) == 0 || response.Choices[0].Message.Audio.Data == "" {
		return speechResult{}, errors.New("MiMo 配音服务未返回 WAV 音频")
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Choices[0].Message.Audio.Data)
	if err != nil || len(decoded) == 0 {
		return speechResult{}, errors.New("MiMo 返回的音频编码无效")
	}
	if len(decoded) < 12 || string(decoded[:4]) != "RIFF" || string(decoded[8:12]) != "WAVE" {
		return speechResult{}, errors.New("MiMo 返回的音频不是有效 WAV 格式")
	}
	if speed != 1 {
		decoded, err = speechTempo(ctx, decoded, speed, s.dataDir)
		if err != nil {
			return speechResult{}, errors.New("MiMo 配音语速调整失败，请重试")
		}
	}
	return speechResult{Data: decoded, Ext: ".wav", Type: "audio/wav", Provider: "mimo"}, nil
}

func (s *Server) audioRequest(ctx context.Context, target, apiKey, authHeader string, payload any) ([]byte, string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, "", errors.New("配音请求格式化失败")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.New("配音请求地址无效")
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		if authHeader == "Authorization" {
			request.Header.Set(authHeader, "Bearer "+apiKey)
		} else {
			request.Header.Set(authHeader, apiKey)
		}
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, "", errors.New("配音请求已取消")
		}
		return nil, "", errors.New("配音服务无法连接或等待超时")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, speechMaxData+1))
	if err != nil || len(body) > speechMaxData {
		return nil, "", errors.New("配音服务响应超过 30 MB 限制")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := ""
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &failure) == nil {
			message = strings.TrimSpace(failure.Error.Message)
			if apiKey != "" {
				message = strings.ReplaceAll(message, apiKey, "[密钥已隐藏]")
			}
		}
		if len([]rune(message)) > 300 {
			message = string([]rune(message)[:300])
		}
		if message != "" {
			message = ": " + message
		}
		return nil, "", fmt.Errorf("配音服务返回 HTTP %d%s", response.StatusCode, message)
	}
	return body, response.Header.Get("Content-Type"), nil
}

func speechTempo(ctx context.Context, data []byte, speed float64, dataDir string) ([]byte, error) {
	temp, err := os.CreateTemp(filepath.Join(dataDir, "uploads"), ".speed-*.wav")
	if err != nil {
		return nil, err
	}
	target := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(target)
		return nil, err
	}
	defer os.Remove(target)
	runContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(runContext, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", "pipe:0", "-filter:a", fmt.Sprintf("atempo=%g", speed), "-f", "wav", target)
	command.Stdin = bytes.NewReader(data)
	if _, err := command.CombinedOutput(); err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

func speechAudioFormat(data []byte, contentType string) (string, string) {
	contentType = strings.ToLower(strings.Split(contentType, ";")[0])
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return ".wav", "audio/wav"
	}
	if len(data) >= 3 && string(data[:3]) == "ID3" {
		return ".mp3", "audio/mpeg"
	}
	if len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 {
		return ".mp3", "audio/mpeg"
	}
	if strings.Contains(contentType, "wav") {
		return ".wav", "audio/wav"
	}
	if strings.Contains(contentType, "mpeg") || strings.Contains(contentType, "mp3") {
		return ".mp3", "audio/mpeg"
	}
	return "", ""
}

func speechDuration(ctx context.Context, data []byte, ext, dataDir string) (float64, error) {
	temp, err := os.CreateTemp(filepath.Join(dataDir, "uploads"), ".duration-*"+ext)
	if err != nil {
		return 0, err
	}
	target := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(target)
		return 0, err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(target)
		return 0, err
	}
	defer os.Remove(target)
	runContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(runContext, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", target)
	output, err := command.Output()
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, errors.New("音频时长无效")
	}
	return seconds, nil
}
