package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestExportQueuedCancellationReleasesCapacity(t *testing.T) {
	m := testExportManager(t)
	jobs := make([]ExportJob, 0, 3)
	for index := 0; index < 3; index++ {
		job, err := m.create(renderProject(), "http://127.0.0.1:8080")
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
		if job.QueuePosition != index+1 || job.Stage != "queued" || job.TotalFrames != 10 {
			t.Fatalf("排队信息不正确: %+v", job)
		}
	}
	cancelled, _ := m.cancel(jobs[1].ID)
	if cancelled.Stage != "cancelled" || cancelled.FinishedAt == "" || len(m.queue) != 2 {
		t.Fatalf("取消排队任务未及时回收: %+v，队列=%d", cancelled, len(m.queue))
	}
	if _, err := os.Stat(m.jobs[jobs[1].ID].inputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("取消排队任务未清理快照: %v", err)
	}
	last, _ := m.get(jobs[2].ID)
	if last.QueuePosition != 2 {
		t.Fatalf("取消后排队位置未更新: %+v", last)
	}
	if _, err := m.create(renderProject(), "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("取消排队任务后仍无法提交: %v", err)
	}
	for _, job := range m.list("") {
		m.cancel(job.ID)
	}
}

func TestExportProgressAndProjectHistory(t *testing.T) {
	m := testExportManager(t)
	first, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	project := renderProject()
	project.ID = "another-project"
	other, err := m.create(project, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	history := m.list("render-project")
	if len(history) != 2 || history[0].ID != latest.ID || history[1].ID != first.ID {
		t.Fatalf("按工程查询最近导出不正确: %+v", history)
	}
	task := m.jobs[first.ID]
	m.update(task, "running", 0.01, "准备")
	m.updateProgress(task, "running", renderProgress{Stage: "rendering", Progress: 0.4, RenderedFrames: 4, Message: "渲染中"})
	m.mu.Lock()
	task.renderingStartedAt = time.Now().Add(-4 * time.Second)
	task.job.StartedAt = task.renderingStartedAt.UTC().Format(time.RFC3339Nano)
	lastProgress := task.lastProgressAt
	m.mu.Unlock()
	m.updateProgress(task, "running", renderProgress{Stage: "rendering", Progress: 0.3, RenderedFrames: 4, Message: "渲染中"})
	job, _ := m.get(first.ID)
	if job.Progress != 0.4 || job.RenderedFrames != 4 || job.TotalFrames != 10 || job.StartedAt == "" || job.QueuePosition != 0 || job.ElapsedSeconds < 4 || job.EstimatedRemainingSeconds == nil || *job.EstimatedRemainingSeconds < 6 {
		t.Fatalf("渲染状态不正确: %+v", job)
	}
	m.mu.Lock()
	if !task.lastProgressAt.Equal(lastProgress) {
		t.Fatal("无进展心跳重置了卡住检测")
	}
	m.mu.Unlock()
	m.update(task, "completed", 1, "完成")
	completed, _ := m.get(first.ID)
	if completed.FinishedAt == "" || completed.Stage != "completed" || completed.EstimatedRemainingSeconds != nil || completed.RenderedFrames != 10 {
		t.Fatalf("完成状态不正确: %+v", completed)
	}
	m.cancel(other.ID)
	m.cancel(latest.ID)
	defer task.cancel()
}

func TestExportOrderingWithVariablePrecisionTimestamps(t *testing.T) {
	m := testExportManager(t)
	createdAt := []string{"2026-10-10T01:00:00.1Z", "2026-10-10T01:00:00.11Z", "2026-10-10T01:00:00.09Z"}
	jobs := make([]ExportJob, 0, len(createdAt))
	for _, timestamp := range createdAt {
		job, err := m.create(renderProject(), "http://127.0.0.1:8080")
		if err != nil {
			t.Fatal(err)
		}
		m.jobs[job.ID].job.CreatedAt = timestamp
		jobs = append(jobs, job)
	}
	history := m.list("render-project")
	if len(history) != 3 || history[0].ID != jobs[1].ID || history[1].ID != jobs[0].ID || history[2].ID != jobs[2].ID {
		t.Fatalf("不同小数精度的时间导致导出历史错序: %+v", history)
	}
	for index, expected := range []int{2, 3, 1} {
		job, _ := m.get(jobs[index].ID)
		if job.QueuePosition != expected {
			t.Fatalf("不同小数精度的时间导致排队位置错序: %+v，预期=%d", job, expected)
		}
	}
	for _, job := range jobs {
		m.cancel(job.ID)
	}
}

func TestExportWorkerSurvivesRendererPanic(t *testing.T) {
	m := testExportManager(t)
	first, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	m.runTask = func(task *renderTask) error {
		if task.job.ID == first.ID {
			panic("renderer failure")
		}
		return nil
	}
	close(m.queue)
	finished := make(chan struct{})
	go func() { m.work(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("渲染器异常后队列卡住")
	}
	failed, _ := m.get(first.ID)
	completed, _ := m.get(second.ID)
	if failed.Status != "failed" || failed.FinishedAt == "" || completed.Status != "completed" {
		t.Fatalf("渲染器异常后队列状态不正确: %+v %+v", failed, completed)
	}
}

func TestExportHeartbeatCannotMaskStall(t *testing.T) {
	m := testExportManager(t)
	m.stallTimeout = 60 * time.Millisecond
	job, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	task := m.jobs[job.ID]
	m.updateProgress(task, "running", renderProgress{Stage: "rendering", Progress: 0.2, RenderedFrames: 2, Message: "渲染中"})
	ctx, cancel := context.WithCancel(task.ctx)
	defer cancel()
	defer task.cancel()
	done := make(chan struct{})
	defer close(done)
	stalled := make(chan struct{}, 1)
	go m.monitorProgress(task, ctx, cancel, done, stalled)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			select {
			case <-stalled:
				return
			default:
				t.Fatal("任务取消未记录卡住原因")
			}
		case <-ticker.C:
			m.updateProgress(task, "running", renderProgress{Stage: "rendering", Progress: 0.2, RenderedFrames: 2, Message: "渲染中"})
		case <-deadline.C:
			t.Fatal("无进展心跳导致卡住任务始终不停止")
		}
	}
}

func TestExportRenderStallStopsProcess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("未安装 Node.js")
	}
	m := testExportManager(t)
	m.stallTimeout = 250 * time.Millisecond
	m.renderTimeout = 2 * time.Second
	m.script = filepath.Join(t.TempDir(), "stalled.mjs")
	script := "setInterval(() => process.stdout.write(JSON.stringify({stage: 'rendering', progress: 0.2, renderedFrames: 2, totalFrames: 10, message: '渲染中'}) + '\\n'), 10);"
	if err := os.WriteFile(m.script, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	job, err := m.create(renderProject(), "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	task := m.jobs[job.ID]
	m.update(task, "running", 0.01, "准备")
	started := time.Now()
	err = m.render(task)
	if err == nil || !strings.Contains(err.Error(), "没有进展") || time.Since(started) > time.Second {
		t.Fatalf("卡住的渲染进程未及时停止: %v，耗时=%s", err, time.Since(started))
	}
	m.cancel(job.ID)
}
