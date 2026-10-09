package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func renderProject() Project {
	return Project{ID: "render-project", Name: "测试工程", Width: 640, Height: 360, FPS: 10, Scenes: []Scene{{ID: "render-scene", Name: "测试镜头", Duration: 10, Background: "#17212a", Nodes: []SceneNode{{ID: "render-text", Type: "text", Name: "文字", Width: 200, Height: 60, Opacity: 1, Color: "#ffffff", Text: "导出测试", FontSize: 32, End: 10, Animation: "none"}}}}, Audio: []AudioTrack{}}
}

func testExportManager(t *testing.T) *exportManager {
	t.Helper()
	dataDir := t.TempDir()
	jobsDir := filepath.Join(dataDir, "export-jobs")
	if err := os.MkdirAll(jobsDir, 0700); err != nil {
		t.Fatal(err)
	}
	return &exportManager{dataDir: dataDir, jobsDir: jobsDir, jobs: make(map[string]*renderTask), queue: make(chan *renderTask, 3)}
}

func TestExportSnapshotAndQueueLimit(t *testing.T) {
	m := testExportManager(t)
	project := renderProject()
	job, err := m.create(project, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	project.Scenes[0].Nodes[0].Text = "已修改"
	task := m.jobs[job.ID]
	if task.project.Scenes[0].Nodes[0].Text != "导出测试" {
		t.Fatal("导出快照被后续编辑修改")
	}
	data, err := os.ReadFile(task.inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved Project
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Scenes[0].Nodes[0].Text != "导出测试" {
		t.Fatal("磁盘导出快照不正确")
	}
	for i := 0; i < 2; i++ {
		if _, err := m.create(renderProject(), "http://127.0.0.1:8080"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.create(renderProject(), "http://127.0.0.1:8080"); !errors.Is(err, errExportQueueFull) {
		t.Fatalf("满队列未拒绝新任务: %v", err)
	}
	for _, task := range m.jobs {
		task.cancel()
	}
}

func TestExportCancellationCannotBeOverwritten(t *testing.T) {
	m := testExportManager(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	m.runTask = func(task *renderTask) error {
		close(started)
		<-task.ctx.Done()
		m.update(task, "completed", 1, "结束")
		return context.Canceled
	}
	job, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	go func() { m.work(); close(finished) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("任务未开始")
	}
	cancelled, found := m.cancel(job.ID)
	if !found || cancelled.Status != "cancelled" {
		t.Fatal("任务未取消")
	}
	close(m.queue)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("取消后任务未停止")
	}
	final, _ := m.get(job.ID)
	if final.Status != "cancelled" || final.URL != "" {
		t.Fatalf("取消被完成状态覆盖: %+v", final)
	}
	if _, err := os.Stat(m.jobs[job.ID].inputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("取消后未清理临时工程")
	}
}

func TestExportRestartRecovery(t *testing.T) {
	dataDir := t.TempDir()
	jobsDir := filepath.Join(dataDir, "export-jobs")
	if err := os.MkdirAll(jobsDir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	if err := atomicJSON(filepath.Join(jobsDir, id+".job.json"), ExportJob{ID: id, Status: "running", Progress: 0.5}); err != nil {
		t.Fatal(err)
	}
	m, err := newExportManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer close(m.queue)
	job, found := m.get(id)
	if !found || job.Status != "failed" || job.URL != "" {
		t.Fatalf("重启任务未恢复为失败: %+v", job)
	}
}

func TestExportValidationAndOrigin(t *testing.T) {
	project := renderProject()
	if err := validateExport(&project); err != nil {
		t.Fatal(err)
	}
	project.Scenes = nil
	if validateExport(&project) == nil {
		t.Fatal("空工程允许导出")
	}
	project = renderProject()
	project.Audio = []AudioTrack{{ID: "a", Src: "https://example.com/audio.mp3", Duration: 10, Volume: 1}}
	if validateExport(&project) == nil {
		t.Fatal("外部音频未要求先上传")
	}
	t.Setenv("RENDER_ORIGIN", "")
	request := httptest.NewRequest("POST", "http://localhost:8181/api/exports", nil)
	origin, err := renderOrigin(request)
	if err != nil || origin != "http://127.0.0.1:8181" {
		t.Fatalf("导出未采用实际服务端口: %s %v", origin, err)
	}
	request.Host = "arbitrary.example:80"
	if _, err := renderOrigin(request); err == nil {
		t.Fatal("任意 Host 可改变渲染地址")
	}
}
