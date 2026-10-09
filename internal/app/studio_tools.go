package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type studioOperation struct {
	Type    string          `json:"type"`
	SceneID string          `json:"sceneId,omitempty"`
	NodeID  string          `json:"nodeId,omitempty"`
	AudioID string          `json:"audioId,omitempty"`
	Index   *int            `json:"index,omitempty"`
	Scene   *Scene          `json:"scene,omitempty"`
	Node    *SceneNode      `json:"node,omitempty"`
	Audio   *AudioTrack     `json:"audio,omitempty"`
	Patch   json.RawMessage `json:"patch,omitempty"`
}

type studioToolError struct {
	status  int
	message string
}

func (e studioToolError) Error() string { return e.message }

func studioConflict() error {
	return studioToolError{409, "工程已有更新，请先调用 video_project_get 获取最新 revision 后重试"}
}

func studioDecode(data json.RawMessage, target any) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("工具参数无效: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("工具参数只能包含一个 JSON 对象")
	}
	return nil
}

func studioID(prefix string) string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(data[:])
}

func (s *Server) executeStudioTool(ctx context.Context, name string, args json.RawMessage) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.New("操作已取消")
	}
	switch name {
	case "video_project_list":
		var input struct{}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		result, err := s.studioInvoke(ctx, "GET", "/api/projects", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"projects": result}, nil
	case "video_project_get":
		var input struct {
			ProjectID string `json:"projectId"`
		}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		project, err := s.readProject(input.ProjectID)
		if err != nil {
			return nil, errors.New("工程不存在或无法读取")
		}
		return map[string]any{"project": project}, nil
	case "video_project_create":
		return s.studioCreate(ctx, args)
	case "video_edit":
		var input struct {
			ProjectID  string            `json:"projectId"`
			Revision   int               `json:"revision"`
			Operations []studioOperation `json:"operations"`
		}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		if len(input.Operations) < 1 || len(input.Operations) > 100 {
			return nil, errors.New("一次剪辑需要包含 1 至 100 个操作")
		}
		project, err := s.studioChange(input.ProjectID, input.Revision, false, func(candidate *Project) error {
			for i, operation := range input.Operations {
				if err := applyStudioOperation(candidate, operation); err != nil {
					return fmt.Errorf("第 %d 个操作失败: %w", i+1, err)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.publishStudioEvent(StudioEvent{Type: "project.updated", ProjectID: project.ID, Tool: name, Message: fmt.Sprintf("AI 已完成 %d 个剪辑操作", len(input.Operations)), Project: &project, Data: map[string]any{"operations": input.Operations}})
		return map[string]any{"project": project, "applied": len(input.Operations)}, nil
	case "video_undo":
		var input struct {
			ProjectID string `json:"projectId"`
			Revision  int    `json:"revision"`
		}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		project, err := s.studioUndo(input.ProjectID, input.Revision)
		if err != nil {
			return nil, err
		}
		s.publishStudioEvent(StudioEvent{Type: "project.updated", ProjectID: project.ID, Tool: name, Message: "AI 已撤销上一步剪辑", Project: &project})
		return map[string]any{"project": project}, nil
	case "video_speech_generate":
		return s.studioSpeech(ctx, args)
	case "video_image_generate":
		return s.studioImage(ctx, args)
	case "video_export":
		var input struct {
			ProjectID string `json:"projectId"`
		}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		project, err := s.readProject(input.ProjectID)
		if err != nil {
			return nil, errors.New("工程不存在")
		}
		result, err := s.studioInvoke(ctx, "POST", "/api/exports", map[string]any{"project": project})
		if err != nil {
			return nil, err
		}
		job := result.(map[string]any)
		s.publishStudioEvent(StudioEvent{Type: "export.started", ProjectID: project.ID, Tool: name, Message: "AI 已提交视频导出", Data: job})
		return map[string]any{"job": job}, nil
	case "video_export_status":
		var input struct {
			JobID string `json:"jobId"`
		}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		if !validExportID(input.JobID) {
			return nil, errors.New("导出任务编号无效")
		}
		result, err := s.studioInvoke(ctx, "GET", "/api/exports/"+input.JobID, nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job": result}, nil
	case "video_preview":
		var input struct {
			ProjectID    string `json:"projectId"`
			Frame        int    `json:"frame"`
			IncludeImage bool   `json:"includeImage,omitempty"`
		}
		if err := studioDecode(args, &input); err != nil {
			return nil, err
		}
		project, err := s.readProject(input.ProjectID)
		if err != nil {
			return nil, errors.New("工程不存在")
		}
		total := studioTotal(project)
		if input.Frame < 0 || input.Frame >= total {
			return nil, fmt.Errorf("预览帧需要在 0 至 %d 之间", total-1)
		}
		s.publishStudioEvent(StudioEvent{Type: "preview.seek", ProjectID: project.ID, Tool: name, Message: fmt.Sprintf("AI 正在查看第 %d 帧", input.Frame), Data: map[string]any{"frame": input.Frame}})
		screenshot, err := s.studioScreenshot(ctx, project, input.Frame)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": project, "frame": input.Frame, "previewUrl": "/?project=" + project.ID + fmt.Sprintf("&frame=%d", input.Frame), "screenshot": screenshot}, nil
	default:
		return nil, errors.New("剪辑工具不存在: " + name)
	}
}

func (s *Server) studioCreate(ctx context.Context, args json.RawMessage) (map[string]any, error) {
	var input struct {
		Project *Project `json:"project,omitempty"`
		Name    string   `json:"name,omitempty"`
		Width   int      `json:"width,omitempty"`
		Height  int      `json:"height,omitempty"`
		FPS     int      `json:"fps,omitempty"`
	}
	if err := studioDecode(args, &input); err != nil {
		return nil, err
	}
	project := Project{ID: studioID("project-"), Name: input.Name, Width: input.Width, Height: input.Height, FPS: input.FPS, Audio: []AudioTrack{}}
	if input.Project != nil {
		project = cloneStudioProject(*input.Project)
		if project.ID == "" {
			project.ID = studioID("project-")
		}
		for i := range project.Scenes {
			studioAssignSceneIDs(&project.Scenes[i])
		}
		for i := range project.Audio {
			if project.Audio[i].ID == "" {
				project.Audio[i].ID = studioID("audio-")
			}
		}
	} else {
		if strings.TrimSpace(project.Name) == "" {
			project.Name = "AI 视频工程"
		}
		if project.Width == 0 {
			project.Width = 1280
		}
		if project.Height == 0 {
			project.Height = 720
		}
		if project.FPS == 0 {
			project.FPS = 30
		}
		project.Scenes = []Scene{{ID: studioID("scene-"), Name: "开场", Duration: project.FPS * 5, Background: "#10141c", Nodes: []SceneNode{}}}
	}
	result, err := s.studioInvoke(ctx, "POST", "/api/projects", project)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": result}, nil
}

func (s *Server) studioChange(id string, revision int, allowLatest bool, mutate func(*Project) error) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readProjectLocked(id)
	if err != nil {
		return Project{}, errors.New("工程不存在")
	}
	if !allowLatest && current.Revision != revision {
		return Project{}, studioConflict()
	}
	candidate := cloneStudioProject(current)
	if err := mutate(&candidate); err != nil {
		return Project{}, err
	}
	if err := validateProject(&candidate); err != nil {
		return Project{}, err
	}
	for _, audio := range candidate.Audio {
		if audio.Start+audio.Duration > studioTotal(candidate) {
			return Project{}, errors.New("音轨播放区间超出视频总时长，请先延长镜头或裁剪音轨")
		}
	}
	protect := candidate
	protect.Width, protect.Height, protect.FPS = current.Width, current.Height, current.FPS
	if err := validateAgentChange(agentRequest{Project: current, Scope: "project"}, protect); err != nil {
		return Project{}, fmt.Errorf("剪辑触及受保护内容: %w", err)
	}
	candidate.Revision = current.Revision + 1
	candidate.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	path, _ := s.projectPath(id)
	if err := atomicJSON(path, candidate); err != nil {
		return Project{}, errors.New("剪辑工程保存失败")
	}
	s.studio.mu.Lock()
	history := append(s.studio.history[id], cloneStudioProject(current))
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	s.studio.history[id] = history
	s.studio.mu.Unlock()
	return candidate, nil
}

func (s *Server) studioUndo(id string, revision int) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readProjectLocked(id)
	if err != nil {
		return Project{}, errors.New("工程不存在")
	}
	if current.Revision != revision {
		return Project{}, studioConflict()
	}
	s.studio.mu.Lock()
	defer s.studio.mu.Unlock()
	history := s.studio.history[id]
	if len(history) == 0 {
		return Project{}, errors.New("当前服务没有可以撤销的 AI 剪辑步骤")
	}
	previous := cloneStudioProject(history[len(history)-1])
	candidate := previous
	candidate.Width, candidate.Height, candidate.FPS = current.Width, current.Height, current.FPS
	if err := validateAgentChange(agentRequest{Project: current, Scope: "project"}, candidate); err != nil {
		return Project{}, errors.New("最近修改含受保护图层，无法自动撤销，请先解除保护")
	}
	previous.Revision = current.Revision + 1
	previous.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	path, _ := s.projectPath(id)
	if err := atomicJSON(path, previous); err != nil {
		return Project{}, errors.New("撤销保存失败")
	}
	s.studio.history[id] = history[:len(history)-1]
	return previous, nil
}

func studioAssignSceneIDs(scene *Scene) {
	if scene.ID == "" {
		scene.ID = studioID("scene-")
	}
	for i := range scene.Nodes {
		if scene.Nodes[i].ID == "" {
			scene.Nodes[i].ID = studioID("node-")
		}
	}
}

func studioTotal(project Project) int {
	total := 0
	for _, scene := range project.Scenes {
		total += scene.Duration
	}
	return total
}

func studioPatch(target any, patch json.RawMessage, allowed map[string]bool) error {
	var changes map[string]json.RawMessage
	if err := json.Unmarshal(patch, &changes); err != nil || len(changes) == 0 {
		return errors.New("patch 必须是包含可修改属性的对象")
	}
	data, _ := json.Marshal(target)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(data, &merged)
	for key, value := range changes {
		if !allowed[key] {
			return errors.New("不可修改的属性: " + key)
		}
		merged[key] = value
	}
	data, _ = json.Marshal(merged)
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("修改属性类型无效")
	}
	return nil
}

func studioProperties(properties ...string) map[string]bool {
	result := map[string]bool{}
	for _, property := range properties {
		result[property] = true
	}
	return result
}

func applyStudioOperation(project *Project, op studioOperation) error {
	if op.Type == "project.update" {
		return studioPatch(project, op.Patch, studioProperties("name", "width", "height", "fps"))
	}
	if op.Type == "scene.add" {
		if op.Scene == nil {
			return errors.New("scene.add 需要 scene 对象")
		}
		scene := *op.Scene
		studioAssignSceneIDs(&scene)
		index := len(project.Scenes)
		if op.Index != nil {
			index = *op.Index
		}
		if index < 0 || index > len(project.Scenes) {
			return errors.New("镜头插入位置超出范围")
		}
		project.Scenes = append(project.Scenes, Scene{})
		copy(project.Scenes[index+1:], project.Scenes[index:])
		project.Scenes[index] = scene
		return nil
	}
	if strings.HasPrefix(op.Type, "audio.") {
		if op.Type == "audio.add" {
			if op.Audio == nil {
				return errors.New("audio.add 需要 audio 对象")
			}
			audio := *op.Audio
			if audio.ID == "" {
				audio.ID = studioID("audio-")
			}
			project.Audio = append(project.Audio, audio)
			return nil
		}
		for i := range project.Audio {
			if project.Audio[i].ID != op.AudioID {
				continue
			}
			switch op.Type {
			case "audio.update":
				return studioPatch(&project.Audio[i], op.Patch, studioProperties("name", "src", "start", "trimStart", "duration", "sourceDuration", "volume", "fadeIn", "fadeOut"))
			case "audio.delete":
				project.Audio = append(project.Audio[:i], project.Audio[i+1:]...)
				return nil
			default:
				return errors.New("未知音轨操作: " + op.Type)
			}
		}
		return errors.New("指定音轨不存在")
	}
	for i := range project.Scenes {
		if project.Scenes[i].ID != op.SceneID {
			continue
		}
		scene := &project.Scenes[i]
		switch op.Type {
		case "scene.update":
			if err := studioPatch(scene, op.Patch, studioProperties("name", "duration", "background", "notes")); err != nil {
				return err
			}
			for j := range scene.Nodes {
				if scene.Nodes[j].End > scene.Duration {
					scene.Nodes[j].End = scene.Duration
				}
				if scene.Nodes[j].Start >= scene.Duration {
					scene.Nodes[j].Start = scene.Duration - 1
				}
			}
			return nil
		case "scene.delete":
			project.Scenes = append(project.Scenes[:i], project.Scenes[i+1:]...)
			return nil
		case "scene.move":
			if op.Index == nil || *op.Index < 0 || *op.Index >= len(project.Scenes) {
				return errors.New("scene.move 需要有效 index，位置从 0 开始")
			}
			saved := *scene
			project.Scenes = append(project.Scenes[:i], project.Scenes[i+1:]...)
			index := *op.Index
			project.Scenes = append(project.Scenes, Scene{})
			copy(project.Scenes[index+1:], project.Scenes[index:])
			project.Scenes[index] = saved
			return nil
		case "node.add":
			if op.Node == nil {
				return errors.New("node.add 需要 node 对象")
			}
			node := *op.Node
			if node.ID == "" {
				node.ID = studioID("node-")
			}
			index := len(scene.Nodes)
			if op.Index != nil {
				index = *op.Index
			}
			if index < 0 || index > len(scene.Nodes) {
				return errors.New("图层插入位置超出范围")
			}
			scene.Nodes = append(scene.Nodes, SceneNode{})
			copy(scene.Nodes[index+1:], scene.Nodes[index:])
			scene.Nodes[index] = node
			return nil
		case "node.update", "node.delete":
			for j := range scene.Nodes {
				if scene.Nodes[j].ID != op.NodeID {
					continue
				}
				if op.Type == "node.delete" {
					scene.Nodes = append(scene.Nodes[:j], scene.Nodes[j+1:]...)
					return nil
				}
				return studioPatch(&scene.Nodes[j], op.Patch, nodeProperties)
			}
			return errors.New("指定图层不存在")
		default:
			return errors.New("未知剪辑操作: " + op.Type)
		}
	}
	return errors.New("指定镜头不存在")
}

func (s *Server) studioSpeech(ctx context.Context, args json.RawMessage) (map[string]any, error) {
	var input struct {
		ProjectID    string  `json:"projectId"`
		Text         string  `json:"text"`
		Provider     string  `json:"provider,omitempty"`
		Voice        string  `json:"voice,omitempty"`
		Speed        float64 `json:"speed,omitempty"`
		Model        string  `json:"model,omitempty"`
		Instructions string  `json:"instructions,omitempty"`
		Start        int     `json:"start,omitempty"`
	}
	if err := studioDecode(args, &input); err != nil {
		return nil, err
	}
	if input.Start < 0 {
		return nil, errors.New("旁白起点不能小于 0 帧")
	}
	before, err := s.readProject(input.ProjectID)
	if err != nil {
		return nil, errors.New("工程不存在")
	}
	if len(before.Audio) >= 12 {
		return nil, errors.New("工程最多支持 12 条音轨")
	}
	result, err := s.studioInvoke(ctx, "POST", "/api/speech", speechRequest{Text: input.Text, Provider: input.Provider, Voice: input.Voice, Speed: input.Speed, Model: input.Model, Instructions: input.Instructions})
	if err != nil {
		return nil, err
	}
	asset := result.(map[string]any)
	seconds, _ := asset["durationSeconds"].(float64)
	duration := int(math.Ceil(seconds * float64(before.FPS)))
	if duration < 1 {
		return nil, errors.New("语音时长无效")
	}
	url, _ := asset["url"].(string)
	name, _ := asset["name"].(string)
	track := AudioTrack{ID: studioID("audio-"), Name: name, Src: url, Start: input.Start, Duration: duration, SourceDuration: duration, Volume: 1}
	project, err := s.studioChange(input.ProjectID, before.Revision, false, func(candidate *Project) error {
		end := input.Start + duration
		if end > candidate.FPS*120 {
			return errors.New("整段旁白超过视频 120 秒上限，请缩短文本")
		}
		if end > studioTotal(*candidate) {
			extra := end - studioTotal(*candidate)
			last := &candidate.Scenes[len(candidate.Scenes)-1]
			old := last.Duration
			last.Duration += extra
			for i := range last.Nodes {
				if last.Nodes[i].End == old && !last.Nodes[i].Locked && len(last.Nodes[i].Protected) == 0 {
					last.Nodes[i].End = last.Duration
				}
			}
		}
		candidate.Audio = append(candidate.Audio, track)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("语音已生成于 %s，但未加入工程: %w", url, err)
	}
	s.publishStudioEvent(StudioEvent{Type: "project.updated", ProjectID: project.ID, Tool: "video_speech_generate", Message: "AI 已生成旁白并加入音轨", Project: &project, Data: map[string]any{"audioId": track.ID, "url": url}})
	return map[string]any{"project": project, "audio": track, "asset": asset}, nil
}

func (s *Server) studioImage(ctx context.Context, args json.RawMessage) (map[string]any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		Prompt    string `json:"prompt"`
		SceneID   string `json:"sceneId,omitempty"`
	}
	if err := studioDecode(args, &input); err != nil {
		return nil, err
	}
	before, err := s.readProject(input.ProjectID)
	if err != nil {
		return nil, errors.New("工程不存在")
	}
	if input.SceneID == "" {
		input.SceneID = before.Scenes[0].ID
	}
	found := false
	for _, scene := range before.Scenes {
		if scene.ID == input.SceneID {
			found = true
		}
	}
	if !found {
		return nil, errors.New("指定镜头不存在")
	}
	result, err := s.studioInvoke(ctx, "POST", "/api/images/generate", map[string]any{"prompt": input.Prompt, "width": 1024, "height": 1024})
	if err != nil {
		return nil, err
	}
	asset := result.(map[string]any)
	url, _ := asset["url"].(string)
	var node SceneNode
	project, err := s.studioChange(input.ProjectID, before.Revision, false, func(candidate *Project) error {
		for i := range candidate.Scenes {
			if candidate.Scenes[i].ID != input.SceneID {
				continue
			}
			scene := &candidate.Scenes[i]
			node = SceneNode{ID: studioID("node-"), Type: "image", Name: "AI 生成图片", Width: float64(candidate.Width), Height: float64(candidate.Height), Opacity: 1, Color: "#ffffff", Src: url, ObjectFit: "cover", End: scene.Duration, Animation: "fade"}
			scene.Nodes = append([]SceneNode{node}, scene.Nodes...)
			return nil
		}
		return errors.New("指定镜头已删除")
	})
	if err != nil {
		return nil, fmt.Errorf("图片已生成于 %s，但未加入工程: %w", url, err)
	}
	s.publishStudioEvent(StudioEvent{Type: "project.updated", ProjectID: project.ID, Tool: "video_image_generate", Message: "AI 已生成图片并加入镜头", Project: &project, Data: map[string]any{"nodeId": node.ID, "url": url}})
	return map[string]any{"project": project, "node": node, "asset": asset}, nil
}

func (s *Server) studioInvoke(ctx context.Context, method, path string, body any) (any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("内部工具参数编码失败")
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(data)).WithContext(ctx)
	request.Host = "127.0.0.1:8080"
	if origin, ok := ctx.Value(studioOriginKey{}).(string); ok && origin != "" {
		request.Host = origin
	}
	request.Header.Set("Content-Type", "application/json")
	s.mu.RLock()
	handler := s.studioMux
	s.mu.RUnlock()
	if handler == nil {
		return nil, errors.New("剪辑服务尚未启动")
	}
	handler.ServeHTTP(recorder, request)
	var response any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		return nil, errors.New("内部工具返回格式无效")
	}
	if recorder.Code < 200 || recorder.Code >= 300 {
		message := "剪辑工具执行失败"
		if value, ok := response.(map[string]any); ok {
			if text, ok := value["error"].(string); ok {
				message = text
			}
		}
		return nil, studioToolError{recorder.Code, message}
	}
	return response, nil
}

type studioOriginKey struct{}

func (s *Server) studioScreenshot(ctx context.Context, project Project, frame int) (map[string]any, error) {
	originHost := "127.0.0.1:8080"
	if origin, ok := ctx.Value(studioOriginKey{}).(string); ok && origin != "" {
		originHost = origin
	}
	origin, err := renderOrigin(&http.Request{Host: originHost})
	if err != nil {
		return nil, err
	}
	projectFile, err := os.CreateTemp(s.dataDir, ".preview-*.json")
	if err != nil {
		return nil, errors.New("截图工程暂存失败")
	}
	projectPath := projectFile.Name()
	defer os.Remove(projectPath)
	if err := json.NewEncoder(projectFile).Encode(project); err != nil {
		projectFile.Close()
		return nil, errors.New("截图工程编码失败")
	}
	if err := projectFile.Close(); err != nil {
		return nil, errors.New("截图工程暂存失败")
	}
	output, err := os.CreateTemp(s.dataDir, ".preview-*.png")
	if err != nil {
		return nil, errors.New("截图输出暂存失败")
	}
	outputPath := output.Name()
	output.Close()
	defer os.Remove(outputPath)
	root := os.Getenv("VIDEO_ROOT")
	if root == "" {
		root, err = os.Getwd()
	}
	if err != nil {
		return nil, errors.New("截图脚本目录无法确定")
	}
	runContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(runContext, "node", filepath.Join(root, "scripts", "preview.mjs"), projectPath, outputPath, origin, strconv.Itoa(frame))
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	if output, err := command.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("截图已取消")
		}
		if runContext.Err() != nil {
			return nil, errors.New("截图等待超时，请检查素材加载")
		}
		message := strings.TrimSpace(string(output))
		if len([]rune(message)) > 500 {
			message = string([]rune(message)[:500])
		}
		if message == "" {
			message = "渲染器无法启动"
		}
		return nil, errors.New("截图失败: " + message)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, errors.New("截图文件无法读取")
	}
	if _, _, err := assetFormat(data, true); err != nil {
		return nil, errors.New("截图格式无效")
	}
	assetURL, err := s.saveAsset(data, ".png")
	if err != nil {
		return nil, errors.New("截图保存失败")
	}
	absPath, _ := filepath.Abs(filepath.Join(s.dataDir, strings.TrimPrefix(assetURL, "/")))
	return map[string]any{"url": assetURL, "path": absPath, "mimeType": "image/png", "width": project.Width, "height": project.Height}, nil
}

func (s *Server) registerStudioRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"tools": s.studioToolDefinitions(), "mcpUrl": "/mcp"})
	})
	mux.HandleFunc("POST /api/tools/call", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		ctx := context.WithValue(r.Context(), studioOriginKey{}, r.Host)
		result, err := s.executeStudioTool(ctx, input.Name, input.Arguments)
		if err != nil {
			status := 400
			var failure studioToolError
			if errors.As(err, &failure) {
				status = failure.status
			}
			errorJSON(w, status, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"result": result})
	})
	mux.HandleFunc("GET /api/projects/{id}/events", s.studioEvents)
	mux.HandleFunc("GET /api/projects/{id}/activity", s.studioActivity)
	mux.HandleFunc("POST /mcp", s.studioMCP)
	mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST, DELETE")
		errorJSON(w, 405, "此 MCP 端点为无状态连接，请使用 POST；实时工程事件位于 /api/projects/{id}/events")
	})
	mux.HandleFunc("DELETE /mcp", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
}
