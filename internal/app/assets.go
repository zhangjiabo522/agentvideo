package app

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxAssetBytes = 30 << 20

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAssetBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		errorJSON(w, 400, "上传失败，文件不可超过 30 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		errorJSON(w, 400, "请选择需要上传的文件")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
	if err != nil || len(data) > maxAssetBytes || len(data) == 0 {
		errorJSON(w, 400, "文件读取失败，文件需为 1 字节至 30 MB")
		return
	}
	ext, kind, err := assetFormat(data, false)
	if err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	assetURL, err := s.saveAsset(data, ext)
	if err != nil {
		errorJSON(w, 500, "上传素材保存失败")
		return
	}
	writeJSON(w, 201, map[string]string{"url": assetURL, "name": filepath.Base(header.Filename), "type": kind})
}

func assetFormat(data []byte, imageOnly bool) (string, string, error) {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		if err := checkImageDimensions(data); err != nil {
			return "", "", err
		}
		return ".png", "image/png", nil
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		if err := checkImageDimensions(data); err != nil {
			return "", "", err
		}
		return ".jpg", "image/jpeg", nil
	}
	if len(data) >= 16 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ".webp", "image/webp", nil
	}
	if !imageOnly {
		if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
			return ".wav", "audio/wav", nil
		}
		if len(data) >= 4 && string(data[:4]) == "OggS" {
			return ".ogg", "audio/ogg", nil
		}
		if len(data) >= 12 && string(data[4:8]) == "ftyp" && (strings.HasPrefix(string(data[8:12]), "M4A") || string(data[8:12]) == "isom" || string(data[8:12]) == "mp42") {
			return ".m4a", "audio/mp4", nil
		}
		if len(data) >= 3 && (string(data[:3]) == "ID3" || data[0] == 0xff && data[1]&0xe0 == 0xe0) {
			return ".mp3", "audio/mpeg", nil
		}
	}
	return "", "", errors.New("仅支持 PNG、JPEG、WebP 图片，以及 MP3、WAV、M4A、OGG 音频")
}

func checkImageDimensions(data []byte) error {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 {
		return errors.New("图片格式损坏，无法读取")
	}
	if config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return errors.New("图片过大，尺寸需不超过 8192 且总像素不超过 3200 万")
	}
	return nil
}

func (s *Server) saveAsset(data []byte, ext string) (string, error) {
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(identifier[:]) + ext
	if err := os.WriteFile(filepath.Join(s.dataDir, "uploads", name), data, 0600); err != nil {
		return "", err
	}
	return "/uploads/" + name, nil
}

func (s *Server) generateImage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Prompt string `json:"prompt"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Prompt) == "" || len([]rune(input.Prompt)) > 4000 {
		errorJSON(w, 400, "图片描述需要包含 1 至 4000 个字符")
		return
	}
	if input.Width == 0 {
		input.Width = 1024
	}
	if input.Height == 0 {
		input.Height = 1024
	}
	if input.Width < 256 || input.Height < 256 || input.Width > 2048 || input.Height > 2048 {
		errorJSON(w, 400, "图片宽高需要在 256 至 2048 之间")
		return
	}
	settings := s.currentSettings()
	if settings.ImageBaseURL == "" || settings.ImageModel == "" {
		errorJSON(w, 400, "尚未配置生图服务，请填写生图服务地址和模型名称，也可以直接上传图片")
		return
	}
	payload := map[string]any{"model": settings.ImageModel, "prompt": input.Prompt, "n": 1, "size": fmt.Sprintf("%dx%d", input.Width, input.Height)}
	var response struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
			Revised string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := s.providerJSON(r.Context(), endpoint(settings.ImageBaseURL, "images/generations"), settings.ImageAPIKey, payload, &response); err != nil {
		errorJSON(w, 502, "生图失败: "+err.Error())
		return
	}
	if len(response.Data) == 0 {
		errorJSON(w, 502, "生图服务没有返回图片")
		return
	}
	result := response.Data[0]
	var data []byte
	var err error
	if result.B64JSON != "" {
		data, err = base64.StdEncoding.DecodeString(result.B64JSON)
	} else if result.URL != "" {
		parsed, parseErr := url.Parse(result.URL)
		if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
			errorJSON(w, 502, "生图服务返回的图片地址无效")
			return
		}
		request, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, result.URL, nil)
		if requestErr != nil {
			errorJSON(w, 502, "无法读取生成图片")
			return
		}
		remote, remoteErr := s.httpClient.Do(request)
		if remoteErr != nil {
			errorJSON(w, 502, "生成图片下载失败，请重试")
			return
		}
		defer remote.Body.Close()
		if remote.StatusCode != 200 {
			errorJSON(w, 502, "生成图片下载失败，图片地址已失效")
			return
		}
		data, err = io.ReadAll(io.LimitReader(remote.Body, maxAssetBytes+1))
	} else {
		errorJSON(w, 502, "生图服务返回缺少图片地址或数据")
		return
	}
	if err != nil || len(data) > maxAssetBytes || len(data) == 0 {
		errorJSON(w, 502, "生成图片无法读取或超过 30 MB 限制")
		return
	}
	ext, kind, err := assetFormat(data, true)
	if err != nil {
		errorJSON(w, 502, "生成图片格式无效: "+err.Error())
		return
	}
	assetURL, err := s.saveAsset(data, ext)
	if err != nil {
		errorJSON(w, 500, "生成图片保存失败")
		return
	}
	writeJSON(w, 200, map[string]string{"url": assetURL, "name": "生成图片", "type": kind, "revisedPrompt": result.Revised})
}
