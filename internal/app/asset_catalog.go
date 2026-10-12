package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Asset struct {
	ID              string   `json:"id"`
	URL             string   `json:"url"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	MIMEType        string   `json:"mimeType"`
	Size            int64    `json:"size"`
	Width           int      `json:"width,omitempty"`
	Height          int      `json:"height,omitempty"`
	DurationSeconds float64  `json:"durationSeconds,omitempty"`
	CreatedAt       string   `json:"createdAt"`
	Source          string   `json:"source"`
	ProjectIDs      []string `json:"projectIds"`
}

type assetRecord struct {
	Asset
	ModifiedAt int64 `json:"modifiedAt"`
}

type assetOptions struct {
	Name            string
	Source          string
	DurationSeconds float64
}

type assetFilter struct {
	Query     string `json:"query,omitempty"`
	Type      string `json:"type,omitempty"`
	Source    string `json:"source,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
}

func (s *Server) loadAssetCatalog() error {
	s.assetRecords = map[string]assetRecord{}
	file, err := openRegularAsset(filepath.Join(s.dataDir, "assets.json"), 32<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("素材目录无法读取")
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 32<<20)).Decode(&s.assetRecords); err != nil {
		return errors.New("素材目录格式损坏，请恢复 assets.json 后重试")
	}
	if s.assetRecords == nil {
		s.assetRecords = map[string]assetRecord{}
	}
	return nil
}

func openRegularAsset(path string, limit int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("素材文件无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, errors.New("素材文件已变更，请重试")
	}
	return file, nil
}

func assetFilename(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || filepath.Base(name) != name || strings.ContainsAny(name, "/\\%?#\x00") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".wav", ".mp3", ".m4a", ".ogg":
		return true
	}
	return false
}

func readableAssetName(name string) string {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "." || name == "/" {
		return ""
	}
	if chars := []rune(name); len(chars) > 120 {
		name = string(chars[:120])
	}
	return strings.TrimSpace(name)
}

func assetID(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:12])
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	filter := assetFilter{Query: r.URL.Query().Get("query"), Type: r.URL.Query().Get("type"), Source: r.URL.Query().Get("source"), ProjectID: r.URL.Query().Get("projectId")}
	if err := validateAssetFilter(filter); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	assets, err := s.catalogAssets(r.Context(), filter)
	if err != nil {
		errorJSON(w, 500, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"assets": assets})
}

func validateAssetFilter(filter assetFilter) error {
	if len([]rune(filter.Query)) > 200 || filter.Type != "" && filter.Type != "image" && filter.Type != "audio" {
		return errors.New("素材筛选条件无效")
	}
	switch filter.Source {
	case "", "upload", "generated", "speech", "imported", "existing", "builtin":
	default:
		return errors.New("素材来源无效")
	}
	if filter.ProjectID != "" && !safeID.MatchString(filter.ProjectID) {
		return errors.New("工程编号无效")
	}
	return nil
}

func (s *Server) assetReferences() (map[string]string, map[string][]string) {
	names, projects := map[string]string{}, map[string][]string{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	paths, _ := filepath.Glob(filepath.Join(s.dataDir, "project-*.json"))
	for _, path := range paths {
		file, err := openRegularAsset(path, 4<<20)
		if err != nil {
			continue
		}
		var project Project
		err = json.NewDecoder(file).Decode(&project)
		file.Close()
		if err != nil || !safeID.MatchString(project.ID) {
			continue
		}
		add := func(url, name string) {
			if !strings.HasPrefix(url, "/uploads/") && !strings.HasPrefix(url, "/media/") {
				return
			}
			if names[url] == "" {
				names[url] = readableAssetName(name)
			}
			ids := projects[url]
			if len(ids) == 0 || ids[len(ids)-1] != project.ID {
				projects[url] = append(ids, project.ID)
			}
		}
		for _, scene := range project.Scenes {
			for _, node := range scene.Nodes {
				add(node.Src, node.Name)
			}
		}
		for _, audio := range project.Audio {
			add(audio.Src, audio.Name)
		}
	}
	return names, projects
}

func (s *Server) catalogAssets(ctx context.Context, filter assetFilter) ([]Asset, error) {
	names, projects := s.assetReferences()
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	if s.assetRecords == nil {
		s.assetRecords = map[string]assetRecord{}
	}
	mediaRoot := filepath.Join(s.distDir, "media")
	if _, err := os.Lstat(mediaRoot); errors.Is(err, os.ErrNotExist) {
		mediaRoot = filepath.Join("public", "media")
	}
	seen := map[string]bool{}
	changed := false
	for _, root := range []struct{ path, prefix, source string }{{filepath.Join(s.dataDir, "uploads"), "/uploads/", "existing"}, {mediaRoot, "/media/", "builtin"}} {
		info, err := os.Lstat(root.path)
		if errors.Is(err, os.ErrNotExist) && root.source == "builtin" {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("素材目录无法读取")
		}
		entries, err := os.ReadDir(root.path)
		if err != nil {
			return nil, errors.New("素材目录无法读取")
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, errors.New("素材读取已取消")
			}
			if !assetFilename(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxAssetBytes {
				continue
			}
			url := root.prefix + entry.Name()
			record, exists := s.assetRecords[url]
			if !exists || record.ModifiedAt != info.ModTime().UnixNano() || record.Size != info.Size() {
				file, err := openRegularAsset(filepath.Join(root.path, entry.Name()), maxAssetBytes)
				if err != nil {
					continue
				}
				data, err := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
				file.Close()
				if err != nil || len(data) > maxAssetBytes {
					continue
				}
				options := assetOptions{Name: record.Name, Source: record.Source}
				if options.Source == "" {
					options.Source = root.source
				}
				asset, err := describeAsset(ctx, data, filepath.Join(root.path, entry.Name()), url, options, info.ModTime())
				if err != nil {
					continue
				}
				if exists && record.CreatedAt != "" {
					asset.CreatedAt = record.CreatedAt
				}
				record = assetRecord{Asset: asset, ModifiedAt: info.ModTime().UnixNano()}
				changed = true
			}
			seen[url] = true
			if (record.Source == "existing" || record.Source == "builtin") && names[url] != "" && record.Name != names[url] {
				record.Name = names[url]
				changed = true
			}
			if strings.Join(record.ProjectIDs, "\x00") != strings.Join(projects[url], "\x00") {
				record.ProjectIDs = append([]string{}, projects[url]...)
				changed = true
			}
			s.assetRecords[url] = record
		}
	}
	for url := range s.assetRecords {
		if !seen[url] {
			delete(s.assetRecords, url)
			changed = true
		}
	}
	if changed {
		if err := atomicJSON(filepath.Join(s.dataDir, "assets.json"), s.assetRecords); err != nil {
			return nil, errors.New("素材信息保存失败")
		}
	}
	assets := []Asset{}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, record := range s.assetRecords {
		asset := record.Asset
		if asset.Source == "preview" || filter.Type != "" && asset.Type != filter.Type || filter.Source != "" && asset.Source != filter.Source || query != "" && !strings.Contains(strings.ToLower(asset.Name), query) {
			continue
		}
		if filter.ProjectID != "" {
			found := false
			for _, id := range asset.ProjectIDs {
				found = found || id == filter.ProjectID
			}
			if !found {
				continue
			}
		}
		asset.ProjectIDs = append([]string{}, asset.ProjectIDs...)
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].CreatedAt == assets[j].CreatedAt {
			return assets[i].ID < assets[j].ID
		}
		return assets[i].CreatedAt > assets[j].CreatedAt
	})
	return assets, nil
}

func describeAsset(ctx context.Context, data []byte, path, url string, options assetOptions, created time.Time) (Asset, error) {
	_, kind, err := assetFormat(data, false)
	if err != nil {
		return Asset{}, err
	}
	asset := Asset{ID: assetID(url), URL: url, Name: readableAssetName(options.Name), Type: strings.SplitN(kind, "/", 2)[0], MIMEType: kind, Size: int64(len(data)), CreatedAt: created.UTC().Format(time.RFC3339Nano), Source: options.Source, ProjectIDs: []string{}}
	if asset.Type == "image" {
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err == nil {
			asset.Width, asset.Height = config.Width, config.Height
		} else if kind == "image/webp" {
			asset.Width, asset.Height, _ = webpDimensions(data)
		}
	} else {
		asset.DurationSeconds = options.DurationSeconds
		if asset.DurationSeconds <= 0 {
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			output, err := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
			cancel()
			if err == nil {
				value, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
				if err == nil && value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
					asset.DurationSeconds = value
				}
			}
		}
	}
	if asset.Source == "" {
		asset.Source = "existing"
	}
	if asset.Name == "" {
		name := filepath.Base(url)
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if _, err := hex.DecodeString(stem); err == nil && len(stem) == 32 {
			label := "图片素材"
			if asset.Type == "audio" {
				label = "音频素材"
			}
			name = label + " · " + created.Local().Format("01-02 15:04") + " · " + stem[:6]
		}
		if url == "/media/landscape.jpg" {
			name = "山湖风景"
		}
		asset.Name = name
	}
	return asset, nil
}

func (s *Server) saveAssetWithMetadata(ctx context.Context, data []byte, ext string, options assetOptions) (Asset, error) {
	if len(data) == 0 || len(data) > maxAssetBytes || !assetFilename("asset"+ext) {
		return Asset{}, errors.New("素材格式或大小无效")
	}
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return Asset{}, errors.New("素材编号生成失败")
	}
	root := filepath.Join(s.dataDir, "uploads")
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Asset{}, errors.New("素材目录无法读取")
	}
	name := hex.EncodeToString(identifier[:]) + ext
	path, url := filepath.Join(root, name), "/uploads/"+name
	temp, err := os.CreateTemp(root, ".asset-*")
	if err != nil {
		return Asset{}, err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(data)
	closeErr := temp.Close()
	if err != nil || closeErr != nil {
		return Asset{}, errors.New("素材文件写入失败")
	}
	asset, err := describeAsset(ctx, data, temp.Name(), url, options, time.Now())
	if err != nil {
		return Asset{}, err
	}
	s.assetMu.Lock()
	if s.assetRecords == nil {
		s.assetRecords = map[string]assetRecord{}
	}
	if err = os.Rename(temp.Name(), path); err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			err = statErr
		} else {
			s.assetRecords[url] = assetRecord{Asset: asset, ModifiedAt: info.ModTime().UnixNano()}
			err = atomicJSON(filepath.Join(s.dataDir, "assets.json"), s.assetRecords)
		}
	}
	if err != nil {
		delete(s.assetRecords, url)
		os.Remove(path)
	}
	s.assetMu.Unlock()
	if err != nil {
		return Asset{}, errors.New("素材信息保存失败")
	}
	if asset.Source != "preview" {
		s.notifyAssetsChanged(asset)
	}
	return asset, nil
}

func (s *Server) notifyAssetsChanged(asset Asset) {
	if s.studio == nil {
		return
	}
	s.studio.mu.Lock()
	ids := make([]string, 0, len(s.studio.subscribers))
	for id := range s.studio.subscribers {
		ids = append(ids, id)
	}
	s.studio.mu.Unlock()
	for _, id := range ids {
		s.publishStudioEvent(StudioEvent{Type: "assets.updated", ProjectID: id, Message: "素材库已更新", Data: map[string]any{"asset": asset}})
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/uploads/")
	if !assetFilename(name) || r.URL.RawPath != "" {
		errorJSON(w, 404, "素材不存在")
		return
	}
	root := filepath.Join(s.dataDir, "uploads")
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		errorJSON(w, 404, "素材不存在")
		return
	}
	file, err := openRegularAsset(filepath.Join(root, name), maxAssetBytes)
	if err != nil {
		errorJSON(w, 404, "素材不存在")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		errorJSON(w, 404, "素材不存在")
		return
	}
	s.assetMu.Lock()
	record, known := s.assetRecords[r.URL.Path]
	s.assetMu.Unlock()
	kind := record.MIMEType
	if !known || record.ModifiedAt != info.ModTime().UnixNano() || record.Size != info.Size() {
		data, readErr := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
		_, kind, err = assetFormat(data, false)
		if readErr != nil || err != nil || len(data) > maxAssetBytes {
			errorJSON(w, 404, "素材不存在")
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			errorJSON(w, 404, "素材无法读取")
			return
		}
	}
	w.Header().Set("Content-Type", kind)
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (s *Server) studioAssets(ctx context.Context, args json.RawMessage) (map[string]any, error) {
	var filter assetFilter
	if err := studioDecode(args, &filter); err != nil {
		return nil, err
	}
	if err := validateAssetFilter(filter); err != nil {
		return nil, err
	}
	if filter.ProjectID != "" {
		if _, err := s.readProject(filter.ProjectID); err != nil {
			return nil, errors.New("工程不存在")
		}
	}
	filter.ProjectID = ""
	assets, err := s.catalogAssets(ctx, filter)
	if err != nil {
		return nil, err
	}
	return map[string]any{"assets": assets}, nil
}

func assetPromptName(prefix, prompt string) string {
	text := strings.Join(strings.Fields(prompt), " ")
	if runes := []rune(text); len(runes) > 36 {
		text = string(runes[:36]) + "…"
	}
	return fmt.Sprintf("%s · %s", prefix, text)
}
