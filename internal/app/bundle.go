package app

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const maxBundleBytes int64 = 500 << 20

func (s *Server) getBundle(w http.ResponseWriter, r *http.Request) {
	project, err := s.readProject(r.PathValue("id"))
	if err != nil {
		errorJSON(w, 404, "工程不存在")
		return
	}
	s.sendBundle(w, r, project)
}

func (s *Server) postBundle(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Project Project `json:"project"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	s.sendBundle(w, r, input.Project)
}

func (s *Server) sendBundle(w http.ResponseWriter, r *http.Request, project Project) {
	if err := validateProject(&project); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	data, _ := json.Marshal(project)
	var snapshot Project
	if err := json.Unmarshal(data, &snapshot); err != nil {
		errorJSON(w, 500, "工程快照创建失败")
		return
	}
	archive, err := os.CreateTemp(s.dataDir, ".bundle-*.zip")
	if err != nil {
		errorJSON(w, 500, "工程包暂存失败")
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	writer := zip.NewWriter(archive)
	references := map[string]string{}
	var total int64
	add := func(source *string) error {
		if *source == "" {
			return nil
		}
		if bundled, exists := references[*source]; exists {
			*source = bundled
			return nil
		}
		filePath, err := s.localAssetPath(*source)
		if err != nil {
			return err
		}
		stat, err := os.Lstat(filePath)
		if err != nil || !stat.Mode().IsRegular() {
			return fmt.Errorf("素材缺失或无法读取: %s", *source)
		}
		if stat.Size() > maxAssetBytes || stat.Size() < 1 {
			return fmt.Errorf("素材超过 30 MB 或为空: %s", *source)
		}
		total += stat.Size()
		if total > maxBundleBytes {
			return errors.New("工程素材总大小超过 500 MB")
		}
		entryName := fmt.Sprintf("assets/%04d%s", len(references)+1, strings.ToLower(filepath.Ext(filePath)))
		entry, err := writer.Create(entryName)
		if err != nil {
			return err
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, file)
		file.Close()
		if err != nil {
			return err
		}
		references[*source] = entryName
		*source = entryName
		return nil
	}
	for i := range snapshot.Scenes {
		for j := range snapshot.Scenes[i].Nodes {
			if err := add(&snapshot.Scenes[i].Nodes[j].Src); err != nil {
				writer.Close()
				errorJSON(w, 400, "工程包导出失败: "+err.Error())
				return
			}
		}
	}
	for i := range snapshot.Audio {
		if err := add(&snapshot.Audio[i].Src); err != nil {
			writer.Close()
			errorJSON(w, 400, "工程包导出失败: "+err.Error())
			return
		}
	}
	entry, err := writer.Create("project.json")
	if err == nil {
		err = json.NewEncoder(entry).Encode(snapshot)
	}
	closeErr := writer.Close()
	if err != nil || closeErr != nil {
		errorJSON(w, 500, "工程包写入失败")
		return
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		errorJSON(w, 500, "工程包读取失败")
		return
	}
	stat, err := archive.Stat()
	if err != nil {
		errorJSON(w, 500, "工程包读取失败")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+snapshot.ID+`.zip"`)
	http.ServeContent(w, r, snapshot.ID+".zip", stat.ModTime(), archive)
}

func (s *Server) localAssetPath(source string) (string, error) {
	var base, name string
	if strings.HasPrefix(source, "/uploads/") {
		base, name = filepath.Join(s.dataDir, "uploads"), strings.TrimPrefix(source, "/uploads/")
	} else if strings.HasPrefix(source, "/media/") {
		base, name = filepath.Join(s.distDir, "media"), strings.TrimPrefix(source, "/media/")
		if _, err := os.Stat(filepath.Join(base, name)); errors.Is(err, os.ErrNotExist) {
			base = filepath.Join("public", "media")
		}
	} else {
		return "", errors.New("工程包仅支持本地素材，请先上传外部图片或音频: " + source)
	}
	if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\%?#") || strings.HasPrefix(name, "../") {
		return "", errors.New("素材地址无效")
	}
	return filepath.Join(base, name), nil
}

func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBundleBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		errorJSON(w, 400, "工程包上传失败，文件不可超过 500 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil || header.Size > maxBundleBytes || header.Size < 1 {
		errorJSON(w, 400, "请选择不超过 500 MB 的 ZIP 工程包")
		return
	}
	defer file.Close()
	reader, err := zip.NewReader(file, header.Size)
	if err != nil {
		errorJSON(w, 400, "工程包不是有效的 ZIP 文件")
		return
	}
	project, created, err := s.unpackBundle(reader)
	if err != nil {
		for _, asset := range created {
			os.Remove(filepath.Join(s.dataDir, strings.TrimPrefix(asset, "/")))
		}
		errorJSON(w, 400, "工程包导入失败: "+err.Error())
		return
	}
	s.mu.Lock()
	projectPath, _ := s.projectPath(project.ID)
	err = atomicJSON(projectPath, project)
	s.mu.Unlock()
	if err != nil {
		for _, asset := range created {
			os.Remove(filepath.Join(s.dataDir, strings.TrimPrefix(asset, "/")))
		}
		errorJSON(w, 500, "导入工程保存失败")
		return
	}
	writeJSON(w, 201, project)
}

func (s *Server) unpackBundle(reader *zip.Reader) (Project, []string, error) {
	var project Project
	created := []string{}
	if len(reader.File) > 10000 {
		return project, created, errors.New("工程包最多包含 10000 个文件")
	}
	entries := make(map[string]*zip.File, len(reader.File))
	var expanded uint64
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") || entry.Mode()&os.ModeSymlink != 0 {
			return project, created, errors.New("工程包包含不安全的文件路径")
		}
		if _, duplicate := entries[entry.Name]; duplicate {
			return project, created, errors.New("工程包包含重复文件路径")
		}
		if entry.UncompressedSize64 > uint64(maxBundleBytes)-expanded {
			return project, created, errors.New("工程包解压后超过 500 MB")
		}
		expanded += entry.UncompressedSize64
		entries[entry.Name] = entry
	}
	manifest, exists := entries["project.json"]
	if !exists || manifest.UncompressedSize64 > 4<<20 {
		return project, created, errors.New("工程包缺少 project.json 或工程数据超过 4 MB")
	}
	data, err := readZipFile(manifest, 4<<20)
	if err != nil {
		return project, created, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&project); err != nil {
		return project, created, errors.New("工程数据格式无效")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return project, created, errors.New("工程数据包含多份 JSON 或额外内容")
	}
	references := map[string]string{}
	restore := func(source *string, imageOnly bool) error {
		if *source == "" {
			return nil
		}
		if restored, exists := references[*source]; exists {
			*source = restored
			return nil
		}
		if !strings.HasPrefix(*source, "assets/") {
			return errors.New("工程中的素材必须位于 assets/ 目录")
		}
		entry, exists := entries[*source]
		if !exists || entry.FileInfo().IsDir() || entry.UncompressedSize64 > maxAssetBytes {
			return fmt.Errorf("素材缺失或超过 30 MB: %s", *source)
		}
		data, err := readZipFile(entry, maxAssetBytes)
		if err != nil {
			return err
		}
		ext, _, err := assetFormat(data, imageOnly)
		if err != nil {
			return fmt.Errorf("素材 %s 格式无效: %s", *source, err)
		}
		restored, err := s.saveAsset(data, ext)
		if err != nil {
			return err
		}
		created = append(created, restored)
		references[*source] = restored
		*source = restored
		return nil
	}
	for i := range project.Scenes {
		for j := range project.Scenes[i].Nodes {
			if err := restore(&project.Scenes[i].Nodes[j].Src, true); err != nil {
				return project, created, err
			}
		}
	}
	for i := range project.Audio {
		if err := restore(&project.Audio[i].Src, false); err != nil {
			return project, created, err
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return project, created, err
	}
	project.ID = hex.EncodeToString(id[:])
	project.Revision = 1
	project.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := validateProject(&project); err != nil {
		return project, created, err
	}
	return project, created, nil
}

func readZipFile(entry *zip.File, limit int64) ([]byte, error) {
	file, err := entry.Open()
	if err != nil {
		return nil, errors.New("工程包条目无法读取")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("工程包文件损坏或超过大小限制")
	}
	return data, nil
}
