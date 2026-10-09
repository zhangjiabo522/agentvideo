package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func studioCall(t *testing.T, server *Server, name string, args any) (map[string]any, error) {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return server.executeStudioTool(context.Background(), name, data)
}

func studioSaved(t *testing.T, handler http.Handler, project Project) Project {
	t.Helper()
	response := requestJSON(t, handler, "POST", "/api/projects", project)
	if response.Code != 201 {
		t.Fatalf("工程创建失败: %s", response.Body.String())
	}
	return decodeProject(t, response)
}

func TestStudioAtomicEditAndRevision(t *testing.T) {
	server, handler := testServer(t)
	project := studioSaved(t, handler, sampleProject())
	args := map[string]any{"projectId": project.ID, "revision": project.Revision, "operations": []any{
		map[string]any{"type": "node.update", "sceneId": "scene-1", "nodeId": "node-1", "patch": map[string]any{"text": "变化"}},
		map[string]any{"type": "node.update", "sceneId": "scene-1", "nodeId": "missing", "patch": map[string]any{"text": "不存在"}},
	}}
	if _, err := studioCall(t, server, "video_edit", args); err == nil {
		t.Fatal("非法批次未拒绝")
	}
	unchanged, _ := server.readProject(project.ID)
	if unchanged.Revision != project.Revision || unchanged.Scenes[0].Nodes[0].Text != "原始文字" {
		t.Fatal("失败批次污染工程")
	}
	args["operations"] = []any{map[string]any{"type": "node.update", "sceneId": "scene-1", "nodeId": "node-1", "patch": map[string]any{"text": "变化", "start": 12, "end": 75}}}
	if _, err := studioCall(t, server, "video_edit", args); err != nil {
		t.Fatal(err)
	}
	if _, err := studioCall(t, server, "video_edit", args); err == nil {
		t.Fatal("旧版本覆盖未拒绝")
	}
	updated, _ := server.readProject(project.ID)
	if updated.Revision != 2 || updated.Scenes[0].Nodes[0].Start != 12 || updated.Scenes[0].Nodes[0].End != 75 {
		t.Fatal("时间裁剪未保存")
	}
	if _, err := studioCall(t, server, "video_undo", map[string]any{"projectId": project.ID, "revision": updated.Revision}); err != nil {
		t.Fatal(err)
	}
	undone, _ := server.readProject(project.ID)
	if undone.Revision != 3 || undone.Scenes[0].Nodes[0].Text != "原始文字" {
		t.Fatal("撤销失败")
	}
}

func TestStudioProtectionAndTrimBounds(t *testing.T) {
	server, handler := testServer(t)
	project := sampleProject()
	project.Scenes[0].Nodes[0].Protected = []string{"text"}
	project.Scenes[0].Nodes[1].Locked = true
	project = studioSaved(t, handler, project)
	for _, operation := range []map[string]any{
		{"type": "node.update", "sceneId": "scene-1", "nodeId": "node-1", "patch": map[string]any{"text": "不得改"}},
		{"type": "node.delete", "sceneId": "scene-1", "nodeId": "node-2"},
		{"type": "scene.update", "sceneId": "scene-1", "patch": map[string]any{"duration": 30}},
		{"type": "audio.add", "audio": AudioTrack{ID: "a1", Name: "音频", Src: "/uploads/test.wav", Duration: 120, SourceDuration: 120, Volume: 1}},
		{"type": "audio.add", "audio": AudioTrack{ID: "a2", Name: "裁剪", Src: "/uploads/test.wav", Duration: 90, TrimStart: 10, SourceDuration: 90, Volume: 1}},
	} {
		if _, err := studioCall(t, server, "video_edit", map[string]any{"projectId": project.ID, "revision": project.Revision, "operations": []any{operation}}); err == nil {
			t.Fatalf("不安全操作未拒绝: %v", operation)
		}
	}
	loaded, _ := server.readProject(project.ID)
	if loaded.Revision != project.Revision {
		t.Fatal("受保护操作仍保存了工程")
	}
}

func TestStudioCreateMoveAndIDs(t *testing.T) {
	server, _ := testServer(t)
	created, err := studioCall(t, server, "video_project_create", map[string]any{"name": "AI 新视频"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(created["project"])
	var project Project
	_ = json.Unmarshal(data, &project)
	if project.Revision != 1 || len(project.Scenes) != 1 {
		t.Fatal("空工程创建失败")
	}
	scene := Scene{Name: "第二镜头", Duration: 90, Background: "#000000", Nodes: []SceneNode{{Type: "text", Name: "新增文字", Width: 400, Height: 100, Opacity: 1, Color: "#ffffff", Text: "自动编号", End: 90, Animation: "fade"}}}
	if _, err := studioCall(t, server, "video_edit", map[string]any{"projectId": project.ID, "revision": project.Revision, "operations": []any{map[string]any{"type": "scene.add", "scene": scene}}}); err != nil {
		t.Fatal(err)
	}
	project, _ = server.readProject(project.ID)
	added := project.Scenes[1]
	if added.ID == "" || added.Nodes[0].ID == "" {
		t.Fatal("未分配自动编号")
	}
	if _, err := studioCall(t, server, "video_edit", map[string]any{"projectId": project.ID, "revision": project.Revision, "operations": []any{map[string]any{"type": "scene.move", "sceneId": added.ID, "index": 0}}}); err != nil {
		t.Fatal(err)
	}
	project, _ = server.readProject(project.ID)
	if project.Scenes[0].ID != added.ID {
		t.Fatal("镜头移动未保存")
	}
}

func TestStudioMCPNegotiationCallsAndOrigin(t *testing.T) {
	server, handler := testServer(t)
	studioSaved(t, handler, sampleProject())
	call := func(body string, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://127.0.0.1:8080/mcp", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatal(response.Body.String())
	}
	response = call(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, "http://127.0.0.1:8080")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"inputSchema"`) || !strings.Contains(response.Body.String(), `video_edit`) {
		t.Fatal(response.Body.String())
	}
	response = call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"video_project_get","arguments":{"projectId":"project-test"}}}`, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"structuredContent"`) || !strings.Contains(response.Body.String(), `"isError":false`) {
		t.Fatal(response.Body.String())
	}
	response = call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"video_preview","arguments":{"projectId":"project-test","frame":999}}}`, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"isError":true`) {
		t.Fatal(response.Body.String())
	}
	response = call(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, "")
	if response.Code != 202 {
		t.Fatal("通知未返回202")
	}
	response = call(`{"jsonrpc":"2.0","id":5,"method":"ping"}`, "https://wrong.example")
	if response.Code != 403 {
		t.Fatal("跨站调用未拒绝")
	}
	if len(server.studioToolDefinitions()) != 10 {
		t.Fatal("工具目录不完整")
	}
}

func TestStudioEventsAndExternalSave(t *testing.T) {
	server, handler := testServer(t)
	project := studioSaved(t, handler, sampleProject())
	remote := httptest.NewServer(handler)
	defer remote.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", remote.URL+"/api/projects/"+project.ID+"/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				lines <- strings.TrimPrefix(scanner.Text(), "data: ")
			}
		}
		close(lines)
	}()
	read := func() StudioEvent {
		select {
		case line := <-lines:
			var event StudioEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			return event
		case <-time.After(3 * time.Second):
			t.Fatal("实时事件等待超时")
			return StudioEvent{}
		}
	}
	initial := read()
	if initial.Type != "snapshot" || initial.Project == nil || initial.Project.Revision != 1 {
		t.Fatalf("初始快照无效: %+v", initial)
	}
	project.Name = "外部保存更新"
	result := requestJSON(t, handler, "PUT", "/api/projects/"+project.ID, project)
	if result.Code != 200 {
		t.Fatal(result.Body.String())
	}
	event := read()
	if event.Type != "project.updated" || event.Project.Name != project.Name || event.Seq <= initial.Seq {
		t.Fatalf("外部保存未广播: %+v", event)
	}
	server.publishStudioEvent(StudioEvent{Type: "preview.seek", ProjectID: project.ID, Tool: "video_preview", Message: "查看第12帧", Data: map[string]any{"frame": 12}})
	preview := read()
	if preview.Type != "preview.seek" || preview.Data["frame"] != float64(12) {
		t.Fatal("预览定位未广播")
	}
	activity := requestJSON(t, handler, "GET", "/api/projects/"+project.ID+"/activity", nil)
	if activity.Code != 200 || !bytes.Contains(activity.Body.Bytes(), []byte("preview.seek")) {
		t.Fatal("活动记录缺少预览")
	}
}
