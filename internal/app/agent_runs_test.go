package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func agentRunResponse(callID, name string, arguments any) map[string]any {
	data, _ := json.Marshal(arguments)
	return map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "content": "正在调用剪辑工具", "tool_calls": []any{map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": name, "arguments": string(data)}}}}}}}
}

func agentRunCompleteResponse(message string) map[string]any {
	return map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": message}}}}
}

func decodeAgentRun(t *testing.T, response *httptest.ResponseRecorder) AgentRun {
	t.Helper()
	var envelope struct {
		Run AgentRun `json:"run"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Run.ID == "" {
		t.Fatalf("任务响应无效: %s", response.Body.String())
	}
	return envelope.Run
}

func waitAgentRun(t *testing.T, server *Server, id string) AgentRun {
	t.Helper()
	state, err := server.agentRunState()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state.mu.Lock()
		run := state.runs[id]
		_, active := state.active[run.ProjectID]
		state.mu.Unlock()
		if !active && run.Status != "running" && run.Status != "queued" {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("AI 测试任务未结束")
	return AgentRun{}
}

func createAgentRunTestProject(t *testing.T, handler http.Handler) Project {
	t.Helper()
	response := requestJSON(t, handler, "POST", "/api/projects", sampleProject())
	if response.Code != 201 {
		t.Fatalf("测试工程创建失败: %s", response.Body.String())
	}
	return decodeProject(t, response)
}

func TestAgentRunCallsToolsAndRepairsValidation(t *testing.T) {
	server, handler := testServer(t)
	project := createAgentRunTestProject(t, handler)
	var turns atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer run-test-secret" {
			t.Error("模型请求地址或密钥不正确")
		}
		var payload struct {
			Model    string           `json:"model"`
			Messages []map[string]any `json:"messages"`
			Tools    []map[string]any `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Model != "mock-tools" || len(payload.Tools) < 5 {
			t.Error("模型未收到工具声明")
		}
		for _, definition := range payload.Tools {
			function := definition["function"].(map[string]any)
			if function["name"] == "video_project_create" || function["name"] == "video_project_list" {
				t.Error("任务暴露了跨工程工具")
			}
		}
		turn := turns.Add(1)
		switch turn {
		case 1:
			writeJSON(w, 200, agentRunResponse("call-read", "video_project_get", map[string]any{"projectId": project.ID}))
		case 2:
			if payload.Messages[len(payload.Messages)-1]["role"] != "tool" || !strings.Contains(payload.Messages[len(payload.Messages)-1]["content"].(string), "原始文字") {
				t.Error("工程工具结果没有反馈给模型")
			}
			writeJSON(w, 200, agentRunResponse("call-bad", "video_edit", map[string]any{"projectId": project.ID, "revision": 1, "operations": []any{map[string]any{"type": "scene.update", "sceneId": "scene-1", "patch": map[string]any{"duration": -10}}}}))
		case 3:
			if !strings.Contains(payload.Messages[len(payload.Messages)-1]["content"].(string), "error") {
				t.Error("校验错误没有反馈给模型")
			}
			writeJSON(w, 200, agentRunResponse("call-edit", "video_edit", map[string]any{"projectId": project.ID, "revision": 1, "operations": []any{map[string]any{"type": "node.update", "sceneId": "scene-1", "nodeId": "node-1", "patch": map[string]any{"text": "AI 已完成剪辑"}}}}))
		case 4:
			if !strings.Contains(payload.Messages[len(payload.Messages)-1]["content"].(string), "AI 已完成剪辑") {
				t.Error("编辑结果没有反馈给模型")
			}
			writeJSON(w, 200, agentRunCompleteResponse("已完成标题修改"))
		default:
			t.Error("模型调用没有按期结束")
			writeJSON(w, 500, nil)
		}
	}))
	defer provider.Close()
	server.settings.BaseURL = provider.URL + "/v1"
	server.settings.Model = "mock-tools"
	server.settings.APIKey = "run-test-secret"
	response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "把开场标题修改成 AI 已完成剪辑"})
	if response.Code != 202 {
		t.Fatalf("创建任务返回 %d: %s", response.Code, response.Body.String())
	}
	run := waitAgentRun(t, server, decodeAgentRun(t, response).ID)
	if run.Status != "completed" || run.ToolCount != 3 || run.Step != 4 || turns.Load() != 4 {
		t.Fatalf("多轮任务不正确: %+v", run)
	}
	updated, err := server.readProject(project.ID)
	if err != nil || updated.Revision != 2 || updated.Scenes[0].Nodes[0].Text != "AI 已完成剪辑" {
		t.Fatalf("编辑没有实际保存: %+v, %v", updated, err)
	}
	server.studio.mu.Lock()
	events := append([]StudioEvent{}, server.studio.events[project.ID]...)
	server.studio.mu.Unlock()
	before, after, projectUpdate := false, false, false
	for _, event := range events {
		if event.Type == "run" && event.RunID == run.ID && event.Tool == "video_edit" {
			before = before || strings.Contains(event.Message, "正在调用")
			after = after || strings.Contains(event.Message, "工具操作已完成")
		}
		projectUpdate = projectUpdate || event.Type == "project.updated" && event.Project != nil && event.Project.Revision == 2
	}
	if !before || !after || !projectUpdate {
		t.Fatal("实时运行步骤或工程事件缺失")
	}
	if response := requestJSON(t, handler, "GET", "/api/agent/runs/"+run.ID, nil); response.Code != 200 || decodeAgentRun(t, response).Status != "completed" {
		t.Fatal("任务详情不能读取")
	}
	if response := requestJSON(t, handler, "GET", "/api/projects/"+project.ID+"/runs", nil); response.Code != 200 || !strings.Contains(response.Body.String(), run.ID) {
		t.Fatal("工程任务记录未保存")
	}
}

func TestAgentRunRejectsTextOnlyModelAndRedactsKeys(t *testing.T) {
	server, handler := testServer(t)
	project := createAgentRunTestProject(t, handler)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, agentRunCompleteResponse("已经剪辑完成，但没有实际调用工具"))
	}))
	defer provider.Close()
	server.settings.BaseURL = provider.URL
	server.settings.Model = "mock-tools"
	response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "剪辑我的视频"})
	run := waitAgentRun(t, server, decodeAgentRun(t, response).ID)
	if run.Status != "failed" || !strings.Contains(run.Message, "工具调用") {
		t.Fatalf("文字响应被伪造为成功: %+v", run)
	}
	settings := Settings{APIKey: "text-secret", ImageAPIKey: "image-secret", TTSAPIKey: "speech-secret"}
	if message := agentRunMessage("text-secret image-secret speech-secret", settings); strings.Contains(message, "secret") {
		t.Fatalf("密钥未隐藏: %s", message)
	}
}

func TestAgentRunCancellationStopsModelAndPreventsDuplicate(t *testing.T) {
	server, handler := testServer(t)
	project := createAgentRunTestProject(t, handler)
	started := make(chan struct{})
	finished := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			t.Error("取消任务没有中断远程模型请求")
		}
		close(finished)
	}))
	defer provider.Close()
	server.settings.BaseURL = provider.URL
	server.settings.Model = "mock-tools"
	response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "剪辑我的视频"})
	if response.Code != 202 {
		t.Fatalf("任务创建失败: %s", response.Body.String())
	}
	run := decodeAgentRun(t, response)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("模型请求未发出")
	}
	duplicate := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "另一个任务"})
	if duplicate.Code != 409 {
		t.Fatalf("重复任务未拒绝: %s", duplicate.Body.String())
	}
	cancelled := requestJSON(t, handler, "POST", "/api/agent/runs/"+run.ID+"/cancel", nil)
	if cancelled.Code != 200 || decodeAgentRun(t, cancelled).Status != "cancelled" {
		t.Fatal("任务未停止")
	}
	run = waitAgentRun(t, server, run.ID)
	if run.Status != "cancelled" {
		t.Fatalf("取消状态被覆写: %+v", run)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("模型网络请求未被取消")
	}
}

func TestAgentRunScopesToolArguments(t *testing.T) {
	valid, err := scopeAgentToolArgs("video_edit", `{"revision":1,"operations":[]}`, "project-current")
	if err != nil || !strings.Contains(string(valid), `"projectId":"project-current"`) {
		t.Fatal("未自动注入目标工程")
	}
	for _, input := range []string{`{"projectId":"another-project"}`, `{"operations":[{"projectId":"another-project"}]}`, `{"project":{"id":"another-project"}}`, `[]`, `{`} {
		if _, err := scopeAgentToolArgs("video_edit", input, "project-current"); err == nil {
			t.Fatalf("未拒绝超范围参数: %s", input)
		}
	}
	if _, err := scopeAgentToolArgs("video_project_create", `{}`, "project-current"); err == nil {
		t.Fatal("任务允许另建工程")
	}
	call := agentRunToolCall{}
	call.Function.Name = "video_export_status"
	call.Function.Arguments = `{"jobId":"another-job"}`
	if _, err := scopeAgentRunTool(call, AgentRun{ExportJobID: "own-job"}); err == nil {
		t.Fatal("任务可以查询无关导出")
	}
}

func TestAgentRunRestartMarksInterruptedTasks(t *testing.T) {
	dataDir := t.TempDir()
	runsDir := filepath.Join(dataDir, "agent-runs")
	if err := os.MkdirAll(runsDir, 0700); err != nil {
		t.Fatal(err)
	}
	run := AgentRun{ID: strings.Repeat("a", 32), ProjectID: "project-test", Status: "running", Message: "尚在工作"}
	if err := atomicJSON(filepath.Join(runsDir, run.ID+".json"), run); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(dataDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, err := server.agentRunState()
	if err != nil {
		t.Fatal(err)
	}
	if recovered := state.runs[run.ID]; recovered.Status != "failed" || !strings.Contains(recovered.Message, "重启") || len(state.active) != 0 {
		t.Fatalf("重启留下错误的运行状态: %+v", recovered)
	}
}

func TestAgentRunRequiresConfiguredModel(t *testing.T) {
	_, handler := testServer(t)
	project := createAgentRunTestProject(t, handler)
	response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "剪辑我的视频"})
	if response.Code != 400 || !strings.Contains(response.Body.String(), "尚未配置") {
		t.Fatalf("未配置模型的任务没有立即失败: %s", response.Body.String())
	}
}

func TestAgentRunStopsAtStepAndToolLimits(t *testing.T) {
	for _, test := range []struct {
		name     string
		perRound int
		steps    int32
		tools    int
		message  string
	}{{"模型轮数", 1, 20, 20, "20 轮"}, {"工具次数", 3, 14, 40, "40 次"}} {
		t.Run(test.name, func(t *testing.T) {
			server, handler := testServer(t)
			project := createAgentRunTestProject(t, handler)
			var turns atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				turns.Add(1)
				response := agentRunResponse("call-one", "video_project_get", map[string]any{"projectId": project.ID})
				message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
				calls := message["tool_calls"].([]any)
				for i := 1; i < test.perRound; i++ {
					extra := agentRunResponse("call-"+strings.Repeat("x", i), "video_project_get", map[string]any{"projectId": project.ID})
					calls = append(calls, extra["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)[0])
				}
				message["tool_calls"] = calls
				writeJSON(w, 200, response)
			}))
			defer provider.Close()
			server.settings.BaseURL = provider.URL
			server.settings.Model = "mock-tools"
			response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "剪辑我的视频"})
			run := waitAgentRun(t, server, decodeAgentRun(t, response).ID)
			if run.Status != "failed" || !strings.Contains(run.Message, test.message) || run.ToolCount != test.tools || turns.Load() != test.steps {
				t.Fatalf("循环没有按上限停止: %+v，模型调用=%d", run, turns.Load())
			}
		})
	}
}

func TestAgentRunRequiresExportForFinishedVideoAndEndsAfterSubmission(t *testing.T) {
	server, handler := testServer(t)
	project := createAgentRunTestProject(t, handler)
	exportID := strings.Repeat("b", 32)
	toolsMux := http.NewServeMux()
	toolsMux.HandleFunc("POST /api/exports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 202, ExportJob{ID: exportID, Status: "queued", Message: "视频排队中"})
	})
	server.studioMux = toolsMux
	var turns atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		switch turns.Add(1) {
		case 1:
			writeJSON(w, 200, agentRunResponse("call-read", "video_project_get", map[string]any{"projectId": project.ID}))
		case 2:
			writeJSON(w, 200, agentRunCompleteResponse("成片完成了"))
		case 3:
			if !strings.Contains(payload.Messages[len(payload.Messages)-1]["content"].(string), "尚未提交") {
				t.Error("模型过早结束未被要求导出")
			}
			writeJSON(w, 200, agentRunResponse("call-export", "video_export", map[string]any{"projectId": project.ID}))
		default:
			t.Error("导出提交后仍继续调用模型")
			writeJSON(w, 500, nil)
		}
	}))
	defer provider.Close()
	server.settings.BaseURL = provider.URL
	server.settings.Model = "mock-tools"
	response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": "制作工具的宣传片"})
	run := waitAgentRun(t, server, decodeAgentRun(t, response).ID)
	if run.Status != "completed" || run.ExportJobID != exportID || !strings.Contains(run.Message, "正在渲染") || turns.Load() != 3 {
		t.Fatalf("宣传片任务完成状态不正确: %+v", run)
	}
}

func TestAgentRunHonorsDeferredExportAndRetainsEditing(t *testing.T) {
	for _, test := range []struct {
		name          string
		prompt        string
		attemptExport bool
	}{
		{"宣传片先不导出", "制作工具的宣传片，先不要导出", false},
		{"成片暂不导出", "做好成片，暂不导出，只保存工程", false},
		{"确认后导出", "剪辑宣传片，等我确认后再导出", false},
		{"拒绝模型擅自导出", "制作宣传片，不需要导出", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, handler := testServer(t)
			project := createAgentRunTestProject(t, handler)
			var exportCalls atomic.Int32
			toolsMux := http.NewServeMux()
			toolsMux.HandleFunc("POST /api/exports", func(w http.ResponseWriter, r *http.Request) {
				exportCalls.Add(1)
				writeJSON(w, 202, ExportJob{ID: strings.Repeat("c", 32), Status: "queued"})
			})
			server.studioMux = toolsMux
			var turns atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Messages []map[string]any `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) == 0 {
					t.Error("模型消息无效")
					writeJSON(w, 500, nil)
					return
				}
				switch turns.Add(1) {
				case 1:
					system, _ := payload.Messages[0]["content"].(string)
					if strings.Contains(system, "已有视频剪裁") || !strings.Contains(system, "当前不支持多轨视频素材剪裁") {
						t.Error("模型收到的能力说明与实际剪辑工具不符")
					}
					writeJSON(w, 200, agentRunResponse("call-read", "video_project_get", map[string]any{"projectId": project.ID}))
				case 2:
					writeJSON(w, 200, agentRunResponse("call-edit", "video_edit", map[string]any{"projectId": project.ID, "revision": project.Revision, "operations": []any{map[string]any{"type": "node.update", "sceneId": "scene-1", "nodeId": "node-1", "patch": map[string]any{"text": "工程已剪辑，等待人工审阅"}}}}))
				case 3:
					if test.attemptExport {
						writeJSON(w, 200, agentRunResponse("call-export", "video_export", map[string]any{"projectId": project.ID}))
					} else {
						writeJSON(w, 200, agentRunCompleteResponse("剪辑已保存，按要求暂不导出"))
					}
				case 4:
					if !test.attemptExport {
						t.Error("用户不导出的要求被强制要求导出")
					} else if last := payload.Messages[len(payload.Messages)-1]; last["role"] != "tool" || !strings.Contains(last["content"].(string), "暂不导出") {
						t.Error("禁止导出的工具错误没有反馈给模型")
					}
					writeJSON(w, 200, agentRunCompleteResponse("工程已保存，按要求没有导出"))
				default:
					t.Error("尊重不导出意图后没有结束任务")
					writeJSON(w, 500, nil)
				}
			}))
			defer provider.Close()
			server.settings.BaseURL = provider.URL
			server.settings.Model = "mock-tools"
			response := requestJSON(t, handler, "POST", "/api/agent/runs", map[string]any{"projectId": project.ID, "prompt": test.prompt})
			if response.Code != 202 {
				t.Fatalf("任务创建失败: %s", response.Body.String())
			}
			run := waitAgentRun(t, server, decodeAgentRun(t, response).ID)
			expectedTurns := int32(3)
			if test.attemptExport {
				expectedTurns = 4
			}
			if run.Status != "completed" || run.ExportJobID != "" || exportCalls.Load() != 0 || turns.Load() != expectedTurns {
				t.Fatalf("延后导出要求未正确执行: %+v，导出调用=%d，模型轮数=%d", run, exportCalls.Load(), turns.Load())
			}
			updated, err := server.readProject(project.ID)
			if err != nil || updated.Revision != project.Revision+1 || updated.Scenes[0].Nodes[0].Text != "工程已剪辑，等待人工审阅" {
				t.Fatalf("不导出时剪辑工程未保存: %+v, %v", updated, err)
			}
		})
	}
}

func TestAgentRunExportIntentKeepsPositiveRequests(t *testing.T) {
	for _, prompt := range []string{"制作工具的宣传片", "制作成片并导出", "剪辑完成后不要忘记导出", "不要改文字，导出工程"} {
		if !agentRunNeedsExport(prompt) {
			t.Errorf("明确导出要求被禁止: %s", prompt)
		}
	}
	if agentRunNeedsExport("调整开场标题") {
		t.Error("普通编辑被强制要求导出")
	}
}
