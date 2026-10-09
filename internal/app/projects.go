package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

var nodeProperties = map[string]bool{
	"name": true, "x": true, "y": true, "width": true, "height": true, "rotation": true,
	"opacity": true, "color": true, "text": true, "fontSize": true, "fontWeight": true,
	"src": true, "objectFit": true, "data": true, "labels": true, "start": true, "end": true,
	"animation": true, "hidden": true, "type": true,
}

func (s *Server) projectPath(id string) (string, error) {
	if !safeID.MatchString(id) {
		return "", errors.New("工程编号无效")
	}
	return filepath.Join(s.dataDir, "project-"+id+".json"), nil
}

func (s *Server) readProject(id string) (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readProjectLocked(id)
}

func (s *Server) readProjectLocked(id string) (Project, error) {
	var project Project
	path, err := s.projectPath(id)
	if err != nil {
		return project, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return project, err
	}
	err = json.Unmarshal(data, &project)
	return project, err
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	paths, err := filepath.Glob(filepath.Join(s.dataDir, "project-*.json"))
	if err != nil {
		errorJSON(w, 500, "无法读取工程目录")
		return
	}
	projects := make([]Project, 0, len(paths))
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		var project Project
		if readErr != nil || json.Unmarshal(data, &project) != nil {
			errorJSON(w, 500, "工程文件损坏，无法加载: "+filepath.Base(path))
			return
		}
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].UpdatedAt > projects[j].UpdatedAt })
	writeJSON(w, 200, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var project Project
	if !decodeJSON(w, r, &project) {
		return
	}
	if err := validateProject(&project); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, _ := s.projectPath(project.ID)
	if _, err := os.Stat(path); err == nil {
		errorJSON(w, 409, "工程已存在，请使用更新接口")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		errorJSON(w, 500, "无法检查工程文件")
		return
	}
	project.Revision = 1
	project.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := atomicJSON(path, project); err != nil {
		errorJSON(w, 500, "工程保存失败")
		return
	}
	writeJSON(w, 201, project)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.readProject(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			errorJSON(w, 404, "工程不存在")
		} else {
			errorJSON(w, 400, "无法打开工程: "+err.Error())
		}
		return
	}
	writeJSON(w, 200, project)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var project Project
	if !decodeJSON(w, r, &project) {
		return
	}
	if project.ID != r.PathValue("id") {
		errorJSON(w, 400, "工程编号与请求路径不一致")
		return
	}
	if err := validateProject(&project); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readProjectLocked(project.ID)
	if err != nil {
		errorJSON(w, 404, "工程不存在")
		return
	}
	if project.Revision != current.Revision {
		errorJSON(w, 409, "工程已有更新，请重新打开后再保存，以免覆盖其他修改")
		return
	}
	project.Revision = current.Revision + 1
	project.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	path, _ := s.projectPath(project.ID)
	if err := atomicJSON(path, project); err != nil {
		errorJSON(w, 500, "工程保存失败")
		return
	}
	writeJSON(w, 200, project)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.projectPath(r.PathValue("id"))
	if err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			errorJSON(w, 404, "工程不存在")
		} else {
			errorJSON(w, 500, "工程删除失败")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func validateProject(project *Project) error {
	if !safeID.MatchString(project.ID) {
		return errors.New("工程编号无效")
	}
	if strings.TrimSpace(project.Name) == "" || len([]rune(project.Name)) > 120 {
		return errors.New("工程名称需要包含 1 至 120 个字符")
	}
	if project.Width < 64 || project.Width > 1920 || project.Height < 64 || project.Height > 1920 || project.Width%2 != 0 || project.Height%2 != 0 {
		return errors.New("画布宽高必须是 64 至 1920 范围内的偶数")
	}
	if project.FPS < 1 || project.FPS > 60 {
		return errors.New("帧率必须在 1 至 60 之间")
	}
	if len(project.Scenes) < 1 || len(project.Scenes) > 20 || len(project.Audio) > 12 {
		return errors.New("工程需要包含 1 至 20 个镜头，最多 12 条音频")
	}
	if project.Scenes == nil {
		project.Scenes = []Scene{}
	}
	if project.Audio == nil {
		project.Audio = []AudioTrack{}
	}
	ids := map[string]bool{}
	total, nodes := 0, 0
	for i := range project.Scenes {
		scene := &project.Scenes[i]
		if !safeID.MatchString(scene.ID) || ids[scene.ID] {
			return errors.New("镜头编号无效或重复")
		}
		ids[scene.ID] = true
		if scene.Duration < 1 || scene.Duration > project.FPS*120 {
			return fmt.Errorf("镜头「%s」时长必须在 1 帧至 120 秒之间", scene.Name)
		}
		if len(scene.Name) > 400 || len(scene.Notes) > 24000 || len(scene.Background) > 128 {
			return errors.New("镜头文本或颜色过长")
		}
		if scene.Nodes == nil {
			scene.Nodes = []SceneNode{}
		}
		total += scene.Duration
		nodes += len(scene.Nodes)
		for _, node := range scene.Nodes {
			if !safeID.MatchString(node.ID) || ids[node.ID] {
				return errors.New("图层编号无效或重复")
			}
			ids[node.ID] = true
			if node.Type != "text" && node.Type != "image" && node.Type != "rect" && node.Type != "circle" && node.Type != "chart" {
				return errors.New("不支持的图层类型: " + node.Type)
			}
			if node.Animation != "none" && node.Animation != "fade" && node.Animation != "slide" && node.Animation != "zoom" {
				return errors.New("不支持的动画: " + node.Animation)
			}
			if !finite(node.X, node.Y, node.Width, node.Height, node.Rotation, node.Opacity, node.FontSize) || node.Width <= 0 || node.Height <= 0 || node.Width > 8192 || node.Height > 8192 || math.Abs(node.X) > 8192 || math.Abs(node.Y) > 8192 || node.Opacity < 0 || node.Opacity > 1 {
				return fmt.Errorf("图层「%s」的位置、尺寸或不透明度无效", node.Name)
			}
			if node.Start < 0 || node.End <= node.Start || node.End > scene.Duration {
				return fmt.Errorf("图层「%s」的出入场时间超出镜头范围", node.Name)
			}
			if len(node.Text) > 32000 || len(node.Name) > 400 || len(node.Color) > 128 || node.FontSize < 0 || node.FontSize > 2048 {
				return errors.New("图层文字或字号无效")
			}
			if !validAssetURL(node.Src) {
				return errors.New("素材地址必须是上传文件或 HTTP 图片地址")
			}
			if node.ObjectFit != "" && node.ObjectFit != "cover" && node.ObjectFit != "contain" {
				return errors.New("图片适配模式仅支持 cover 或 contain")
			}
			protected := map[string]bool{}
			for _, property := range node.Protected {
				if !nodeProperties[property] || protected[property] {
					return errors.New("手动属性保护列表含未知或重复属性")
				}
				protected[property] = true
			}
			if len(node.Data) > 100 || len(node.Labels) > 100 || !finite(node.Data...) {
				return errors.New("图表最多包含 100 个有效数字")
			}
		}
	}
	if total > project.FPS*120 || nodes > 300 {
		return errors.New("工程最多为 120 秒、300 个图层")
	}
	for _, audio := range project.Audio {
		if !safeID.MatchString(audio.ID) || ids[audio.ID] || !validAssetURL(audio.Src) || audio.Src == "" || audio.Start < 0 || audio.Duration < 1 || audio.Start > total || audio.Duration > project.FPS*120 || !finite(audio.TrimStart, audio.Volume) || audio.TrimStart < 0 || math.Trunc(audio.TrimStart) != audio.TrimStart || audio.Volume < 0 || audio.Volume > 2 {
			return errors.New("音频编号、素材或播放参数无效")
		}
		if audio.SourceDuration < 0 || audio.SourceDuration > 0 && audio.TrimStart+float64(audio.Duration) > float64(audio.SourceDuration) || audio.FadeIn < 0 || audio.FadeOut < 0 || audio.FadeIn > audio.Duration || audio.FadeOut > audio.Duration {
			return errors.New("音频裁剪超出素材时长，或淡入淡出超出片段时长")
		}
		ids[audio.ID] = true
	}
	return nil
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func validAssetURL(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "/uploads/") || strings.HasPrefix(value, "/media/") {
		return !strings.Contains(value, "..") && !strings.ContainsAny(value, "\\?#%")
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}
