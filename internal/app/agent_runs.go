package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type AgentRun struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Prompt      string `json:"prompt"`
	Status      string `json:"status"`
	Step        int    `json:"step"`
	MaxSteps    int    `json:"maxSteps"`
	ToolCount   int    `json:"toolCount"`
	MaxTools    int    `json:"maxTools"`
	Message     string `json:"message"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	ExportJobID string `json:"exportJobId,omitempty"`
}

type agentRunState struct {
	mu      sync.Mutex
	dir     string
	runs    map[string]AgentRun
	active  map[string]string
	cancels map[string]context.CancelFunc
}

var agentRunStates sync.Map
var agentRunInitMu sync.Mutex

func (s *Server) agentRunState() (*agentRunState, error) {
	if state, ok := agentRunStates.Load(s); ok {
		return state.(*agentRunState), nil
	}
	agentRunInitMu.Lock()
	defer agentRunInitMu.Unlock()
	if state, ok := agentRunStates.Load(s); ok {
		return state.(*agentRunState), nil
	}
	state := &agentRunState{dir: filepath.Join(s.dataDir, "agent-runs"), runs: map[string]AgentRun{}, active: map[string]string{}, cancels: map[string]context.CancelFunc{}}
	if err := os.MkdirAll(state.dir, 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(state.dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(state.dir, entry.Name()))
		var run AgentRun
		if readErr != nil || json.Unmarshal(data, &run) != nil || !validExportID(run.ID) || entry.Name() != run.ID+".json" {
			continue
		}
		if run.Status == "running" || run.Status == "queued" {
			run.Status = "failed"
			run.Message = "服务重启中断了 AI 任务，已完成的剪辑已保留，请重新提交后续要求"
			run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := atomicJSON(filepath.Join(state.dir, run.ID+".json"), run); err != nil {
				return nil, err
			}
		}
		state.runs[run.ID] = run
	}
	agentRunStates.Store(s, state)
	return state, nil
}

func (s *Server) registerAgentRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/runs", s.createAgentRun)
	mux.HandleFunc("GET /api/agent/runs/{id}", s.getAgentRun)
	mux.HandleFunc("POST /api/agent/runs/{id}/cancel", s.cancelAgentRun)
	mux.HandleFunc("GET /api/projects/{id}/runs", s.listAgentRuns)
}

func (s *Server) createAgentRun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProjectID string `json:"projectId"`
		Prompt    string `json:"prompt"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if len([]rune(input.Prompt)) < 1 || len([]rune(input.Prompt)) > 6000 {
		errorJSON(w, 400, "请输入 1 至 6000 个字符的视频制作要求")
		return
	}
	if !safeID.MatchString(input.ProjectID) {
		errorJSON(w, 400, "工程编号无效")
		return
	}
	if _, err := s.readProject(input.ProjectID); err != nil {
		errorJSON(w, 404, "工程不存在，请先创建或保存工程")
		return
	}
	settings := s.currentSettings()
	if strings.TrimSpace(settings.BaseURL) == "" || strings.TrimSpace(settings.Model) == "" {
		errorJSON(w, 400, "尚未配置文本模型，请填写支持工具调用的模型地址与模型名称")
		return
	}
	state, err := s.agentRunState()
	if err != nil {
		errorJSON(w, 500, "AI 任务记录无法读取")
		return
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		errorJSON(w, 500, "无法创建任务编号")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	run := AgentRun{ID: hex.EncodeToString(random[:]), ProjectID: input.ProjectID, Prompt: input.Prompt, Status: "queued", MaxSteps: 20, MaxTools: 40, Message: "已收到要求，AI 将读取工程并逐步调用剪辑工具", CreatedAt: now, UpdatedAt: now}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	ctx = context.WithValue(ctx, studioOriginKey{}, r.Host)
	state.mu.Lock()
	if _, exists := state.active[run.ProjectID]; exists {
		state.mu.Unlock()
		cancel()
		errorJSON(w, 409, "当前工程已有 AI 任务正在执行或停止，请等待完成后再提交")
		return
	}
	if err := atomicJSON(filepath.Join(state.dir, run.ID+".json"), run); err != nil {
		state.mu.Unlock()
		cancel()
		errorJSON(w, 500, "AI 任务无法保存")
		return
	}
	state.runs[run.ID] = run
	state.active[run.ProjectID] = run.ID
	state.cancels[run.ID] = cancel
	state.mu.Unlock()
	s.publishAgentRun(run, "")
	writeJSON(w, 202, map[string]any{"run": run})
	go s.performAgentRun(ctx, cancel, state, run, settings)
}

func (s *Server) getAgentRun(w http.ResponseWriter, r *http.Request) {
	state, err := s.agentRunState()
	if err != nil {
		errorJSON(w, 500, "AI 任务记录无法读取")
		return
	}
	state.mu.Lock()
	run, exists := state.runs[r.PathValue("id")]
	state.mu.Unlock()
	if !exists {
		errorJSON(w, 404, "AI 任务不存在")
		return
	}
	writeJSON(w, 200, map[string]any{"run": run})
}

func (s *Server) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	if _, err := s.readProject(r.PathValue("id")); err != nil {
		errorJSON(w, 404, "工程不存在")
		return
	}
	state, err := s.agentRunState()
	if err != nil {
		errorJSON(w, 500, "AI 任务记录无法读取")
		return
	}
	runs := []AgentRun{}
	state.mu.Lock()
	for _, run := range state.runs {
		if run.ProjectID == r.PathValue("id") {
			runs = append(runs, run)
		}
	}
	state.mu.Unlock()
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt > runs[j].CreatedAt })
	if len(runs) > 50 {
		runs = runs[:50]
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

func (s *Server) cancelAgentRun(w http.ResponseWriter, r *http.Request) {
	state, err := s.agentRunState()
	if err != nil {
		errorJSON(w, 500, "AI 任务记录无法读取")
		return
	}
	state.mu.Lock()
	run, exists := state.runs[r.PathValue("id")]
	if !exists {
		state.mu.Unlock()
		errorJSON(w, 404, "AI 任务不存在")
		return
	}
	if run.Status == "running" || run.Status == "queued" {
		run.Status = "cancelled"
		run.Message = "AI 任务已停止，已经完成的剪辑已保留"
		run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		state.runs[run.ID] = run
		if cancel := state.cancels[run.ID]; cancel != nil {
			cancel()
		}
		err = atomicJSON(filepath.Join(state.dir, run.ID+".json"), run)
	}
	state.mu.Unlock()
	s.publishAgentRun(run, "")
	if err != nil {
		errorJSON(w, 500, "任务已停止，但停止记录无法保存")
		return
	}
	writeJSON(w, 200, map[string]any{"run": run})
}

func (s *Server) publishAgentRun(run AgentRun, tool string) {
	s.publishStudioEvent(StudioEvent{Type: "run", ProjectID: run.ProjectID, RunID: run.ID, Tool: tool, Message: run.Message, Data: map[string]any{"run": run}})
}

func (s *Server) updateAgentRun(state *agentRunState, id, tool string, mutate func(*AgentRun)) (AgentRun, error) {
	state.mu.Lock()
	run := state.runs[id]
	if run.Status == "cancelled" {
		state.mu.Unlock()
		return run, context.Canceled
	}
	mutate(&run)
	run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	err := atomicJSON(filepath.Join(state.dir, run.ID+".json"), run)
	if err != nil {
		run.Status = "failed"
		run.Message = "任务记录无法保存，AI 已停止，请检查本地存储"
		if cancel := state.cancels[id]; cancel != nil {
			cancel()
		}
	}
	state.runs[id] = run
	state.mu.Unlock()
	s.publishAgentRun(run, tool)
	return run, err
}

func (s *Server) finishAgentRun(state *agentRunState, id, status, message string) {
	state.mu.Lock()
	run := state.runs[id]
	if run.Status != "cancelled" && run.Status != "failed" {
		run.Status = status
		run.Message = message
		run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := atomicJSON(filepath.Join(state.dir, run.ID+".json"), run); err != nil {
			run.Status = "failed"
			run.Message = "任务记录无法保存，已经完成的剪辑已保留"
		}
		state.runs[id] = run
	}
	if state.active[run.ProjectID] == id {
		delete(state.active, run.ProjectID)
	}
	delete(state.cancels, id)
	state.mu.Unlock()
	s.publishAgentRun(run, "")
}

type agentRunToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type agentRunChatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   json.RawMessage    `json:"content"`
			ToolCalls []agentRunToolCall `json:"tool_calls"`
			Refusal   string             `json:"refusal"`
		} `json:"message"`
	} `json:"choices"`
}

func agentRunText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) == nil {
		for _, part := range parts {
			text += part.Text
		}
	}
	return strings.TrimSpace(text)
}

func agentRunMessage(message string, settings Settings) string {
	for _, key := range []string{settings.APIKey, settings.ImageAPIKey, settings.TTSAPIKey} {
		if key != "" {
			message = strings.ReplaceAll(message, key, "[密钥已隐藏]")
		}
	}
	if runes := []rune(message); len(runes) > 1200 {
		message = string(runes[:1200]) + "…"
	}
	return message
}

func scopeAgentToolArgs(name, arguments, projectID string) (json.RawMessage, error) {
	if name == "video_project_create" {
		return nil, errors.New("当前任务仅能操作指定工程，不允许另建工程")
	}
	if len(arguments) > 256<<10 {
		return nil, errors.New("工具参数超过 256 KB，请分镜头逐步编辑")
	}
	var input map[string]any
	if json.Unmarshal([]byte(arguments), &input) != nil || input == nil {
		return nil, errors.New("工具参数必须是有效 JSON 对象")
	}
	var check func(any) error
	check = func(value any) error {
		switch typed := value.(type) {
		case map[string]any:
			for key, item := range typed {
				if key == "projectId" && item != projectID {
					return errors.New("工具调用超出任务范围，只能操作当前工程")
				}
				if key == "project" {
					if project, ok := item.(map[string]any); ok && project["id"] != nil && project["id"] != projectID {
						return errors.New("工具调用中的工程编号超出任务范围")
					}
				}
				if err := check(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range typed {
				if err := check(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := check(input); err != nil {
		return nil, err
	}
	input["projectId"] = projectID
	return json.Marshal(input)
}

func agentRunExportID(result map[string]any) string {
	if id, ok := result["exportJobId"].(string); ok {
		return id
	}
	if job, ok := result["job"].(map[string]any); ok {
		id, _ := job["id"].(string)
		return id
	}
	if job, ok := result["job"].(ExportJob); ok {
		return job.ID
	}
	return ""
}

func agentRunTools(definitions []map[string]any) []map[string]any {
	tools := []map[string]any{}
	for _, definition := range definitions {
		function, ok := definition["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := function["name"].(string)
		if name == "video_project_create" || name == "video_project_list" {
			continue
		}
		tools = append(tools, definition)
	}
	return tools
}

func agentRunDefersExport(prompt string) bool {
	prompt = strings.Join(strings.Fields(prompt), "")
	for _, phrase := range []string{"不要导出", "不导出", "别导出", "无需导出", "不需要导出", "不用导出", "不必导出", "禁止导出", "取消导出", "不进行导出", "现在不导出", "目前不导出", "当前不导出", "不要现在导出", "无需现在导出", "暂不导出", "暂时不导出", "先不导出", "先别导出", "不要提交导出", "暂不提交导出", "不提交导出", "确认后再导出", "确认再导出", "等我确认导出", "等我确认后导出"} {
		if strings.Contains(prompt, phrase) {
			return true
		}
	}
	return false
}

func agentRunNeedsExport(prompt string) bool {
	if agentRunDefersExport(prompt) {
		return false
	}
	for _, phrase := range []string{"宣传片", "成片", "导出"} {
		if strings.Contains(prompt, phrase) {
			return true
		}
	}
	return false
}

func scopeAgentRunTool(call agentRunToolCall, run AgentRun) (json.RawMessage, error) {
	if call.Function.Name == "video_project_list" {
		return nil, errors.New("当前任务只允许读取指定工程，不能列出其他工程")
	}
	if call.Function.Name == "video_export_status" {
		var input struct {
			JobID string `json:"jobId"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &input) != nil || input.JobID == "" || input.JobID != run.ExportJobID {
			return nil, errors.New("只能查询当前 AI 任务产生的导出任务")
		}
		return json.Marshal(input)
	}
	return scopeAgentToolArgs(call.Function.Name, call.Function.Arguments, run.ProjectID)
}

func (s *Server) performAgentRun(ctx context.Context, cancel context.CancelFunc, state *agentRunState, initial AgentRun, settings Settings) {
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.finishAgentRun(state, initial.ID, "failed", "AI 任务执行发生异常，已完成的剪辑已保留")
		}
	}()
	fail := func(message string) {
		status := "failed"
		if errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
			message = "AI 任务已停止，已经完成的剪辑已保留"
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			message = "AI 任务超过 15 分钟上限，已保留完成的剪辑；请分成较小任务继续"
		}
		s.finishAgentRun(state, initial.ID, status, agentRunMessage(message, settings))
	}
	run, err := s.updateAgentRun(state, initial.ID, "", func(run *AgentRun) {
		run.Status = "running"
		run.Message = "AI 正在规划制作步骤"
	})
	if err != nil {
		fail("AI 任务启动失败")
		return
	}
	tools := agentRunTools(s.studioToolDefinitions())
	if len(tools) == 0 {
		fail("当前没有可调用的视频工具")
		return
	}
	system := `你是映序的自主视频剪辑 Agent。你是工具的操作者，人类通过实时画布、时间线和步骤记录观察你的制作过程。使用标准 function calling 调用提供的工具，逐步制作真实可编辑视频，不能仅输出方案或完整工程 JSON 让用户操作。
首先用 video_project_get 读取指定工程和结构，明确用户目标，简短说明制作计划。随后用 video_edit 按镜头逐步编辑，读取工具说明和返回数据后填写正确参数。每次工具操作都会保存工程，并立即更新人类看到的预览。不要等待人类逐项确认常规编辑。
全部工具只能操作用户指定的 projectId。依据当前工程 revision 提交剪辑，版本冲突后重新读取工程，不能覆盖其他更新。不得解除用户锁定的图层或改写 protected 属性。可创建文字、图形、图表和图片图层，支持的图层类型以工具 schema 为准。使用真实已有素材或生图工具结果，禁止编造素材地址。只有文本 LLM 和工具参与制作，不调用视频生成模型。
    先编写分镜、文案和镜头长度，按实际生成的旁白 duration 对齐时间线。所有时间均为整数帧，帧数=秒数×工程 fps。图层 start/end 为镜头内时间，音轨 start 为工程全局时间；音轨 trimStart 与 duration 裁剪源素材；镜头总长不超过120秒。用图片、布局、图层动画和音轨形成节奏，当前不支持多轨视频素材剪裁，中文字号和对比度保持可读，留安全边距。
	必要时调用提供的生图、语音、素材、预览与导出工具。语音生成工具会自动加入工程音轨，必要时延长最后一个镜头，请随后读取实际工程并对齐画面。生图工具会把图片作为底图自动加入指定镜头。每次校验失败，阅读具体错误并修正参数后继续。编辑结果须保留在指定工程中。制作后调用预览检查；只有用户明确要求成片、宣传片或导出，且没有明确说不要、暂不或先不导出时，才调用导出工具。用户明确要求不导出时不要调用 video_export 或 video_export_status，也不要要求用户提交导出。导出返回任务编号后即结束剪辑任务，由界面继续追踪渲染，不要循环轮询视频导出。明确视频正在渲染，不能声称渲染已完成。收到非剪辑问题时可读取工程后回答。
你最多有20轮模型调用和40次工具调用，合并同一镜头的小修改，避免无限重复。完成时用简洁中文描述实际完成内容、存在限制以及导出情况，不虚构执行成功。`
	messages := []map[string]any{{"role": "system", "content": system}, {"role": "user", "content": fmt.Sprintf("目标工程 projectId=%s。用户要求：%s", run.ProjectID, run.Prompt)}}
	successfulTools := 0
	needsExport := agentRunNeedsExport(run.Prompt)
	for step := 1; step <= run.MaxSteps; step++ {
		if ctx.Err() != nil {
			fail("AI 任务已经中断")
			return
		}
		run, err = s.updateAgentRun(state, run.ID, "", func(run *AgentRun) {
			run.Step = step
			run.Message = fmt.Sprintf("AI 正在决定第 %d 步操作", step)
		})
		if err != nil {
			fail("任务状态无法更新")
			return
		}
		payload := map[string]any{"model": settings.Model, "messages": messages, "tools": tools, "tool_choice": "auto", "stream": false}
		var response agentRunChatResponse
		if err := s.providerJSON(ctx, endpoint(settings.BaseURL, "chat/completions"), settings.APIKey, payload, &response); err != nil {
			fail("AI 模型调用失败；请确认模型支持工具调用：" + err.Error())
			return
		}
		if len(response.Choices) == 0 {
			fail("模型没有返回可执行内容，请检查模型的工具调用兼容性")
			return
		}
		choice := response.Choices[0]
		text := agentRunText(choice.Message.Content)
		if choice.FinishReason == "length" {
			fail("模型输出被截断，当前步骤未执行，请减少单次制作内容后重试")
			return
		}
		if choice.Message.Refusal != "" {
			fail("模型拒绝了当前制作请求：" + choice.Message.Refusal)
			return
		}
		if len(choice.Message.ToolCalls) == 0 {
			if successfulTools == 0 {
				fail("模型未调用任何剪辑工具。请选择支持标准工具调用的模型；仅返回文字或工程 JSON 的接口无法自主剪辑")
				return
			}
			if needsExport && run.ExportJobID == "" {
				messages = append(messages, map[string]any{"role": "assistant", "content": text}, map[string]any{"role": "user", "content": "当前尚未提交视频导出。请完成必要剪辑和预览后，调用 video_export 提交成片，再结束任务。"})
				continue
			}
			if text == "" {
				text = "AI 已完成工具操作，工程已保存，可在画布和时间线查看"
			}
			s.finishAgentRun(state, run.ID, "completed", agentRunMessage(text, settings))
			return
		}
		seen := map[string]bool{}
		for _, call := range choice.Message.ToolCalls {
			if call.Type != "function" || call.ID == "" || len(call.ID) > 200 || seen[call.ID] || strings.TrimSpace(call.Function.Name) == "" {
				fail("模型返回的工具调用格式不兼容，已停止以避免错误剪辑")
				return
			}
			seen[call.ID] = true
		}
		var content any
		if len(choice.Message.Content) > 0 {
			_ = json.Unmarshal(choice.Message.Content, &content)
		}
		messages = append(messages, map[string]any{"role": "assistant", "content": content, "tool_calls": choice.Message.ToolCalls})
		if text != "" {
			if _, err := s.updateAgentRun(state, run.ID, "", func(run *AgentRun) { run.Message = agentRunMessage(text, settings) }); err != nil {
				fail("任务状态无法更新")
				return
			}
		}
		for _, call := range choice.Message.ToolCalls {
			if ctx.Err() != nil {
				fail("AI 任务已经中断")
				return
			}
			if run.ToolCount >= run.MaxTools {
				fail("已达到 40 次工具操作上限，工程已保留；请继续提交更具体的后续要求")
				return
			}
			run, err = s.updateAgentRun(state, run.ID, call.Function.Name, func(run *AgentRun) {
				run.ToolCount++
				run.Message = fmt.Sprintf("AI 正在调用 %s（第 %d 次工具操作）", call.Function.Name, run.ToolCount)
			})
			if err != nil {
				fail("任务状态无法更新")
				return
			}
			arguments, toolErr := scopeAgentRunTool(call, run)
			if agentRunDefersExport(run.Prompt) && (call.Function.Name == "video_export" || call.Function.Name == "video_export_status") {
				toolErr = errors.New("用户已明确要求暂不导出，当前任务不能调用导出工具")
			}
			var result map[string]any
			if toolErr == nil {
				result, toolErr = s.executeStudioTool(ctx, call.Function.Name, arguments)
			}
			resultMessage := "工具操作已完成，AI 正在检查结果"
			if toolErr != nil {
				resultMessage = "工具校验失败，AI 将读取错误并调整：" + agentRunMessage(toolErr.Error(), settings)
				result = map[string]any{"ok": false, "error": agentRunMessage(toolErr.Error(), settings)}
			} else {
				successfulTools++
			}
			if ctx.Err() != nil {
				fail("AI 任务已经中断")
				return
			}
			run, err = s.updateAgentRun(state, run.ID, call.Function.Name, func(run *AgentRun) {
				run.Message = resultMessage
				if call.Function.Name == "video_export" {
					if id := agentRunExportID(result); id != "" {
						run.ExportJobID = id
					}
				}
			})
			if err != nil {
				fail("任务状态无法更新")
				return
			}
			data, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				fail("剪辑工具返回格式无效，已完成的操作已保留")
				return
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(data)})
			if toolErr == nil && call.Function.Name == "video_export" && run.ExportJobID != "" {
				s.finishAgentRun(state, run.ID, "completed", "AI 已完成剪辑并提交视频导出，工程已保存，视频正在渲染")
				return
			}
		}
	}
	fail("已达到 20 轮 AI 操作上限，工程已保留；请继续提交更具体的后续要求")
}
