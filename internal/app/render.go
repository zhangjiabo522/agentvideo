package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ExportJob struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	URL      string  `json:"url,omitempty"`
}

type renderTask struct {
	job        ExportJob
	project    Project
	origin     string
	inputPath  string
	outputPath string
	ctx        context.Context
	cancel     context.CancelFunc
}

type exportManager struct {
	mu      sync.Mutex
	dataDir string
	jobsDir string
	script  string
	jobs    map[string]*renderTask
	queue   chan *renderTask
	runTask func(*renderTask) error
}

func newExportManager(dataDir string) (*exportManager, error) {
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	jobsDir := filepath.Join(absolute, "export-jobs")
	if err := os.MkdirAll(jobsDir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, "exports"), 0700); err != nil {
		return nil, err
	}
	root := os.Getenv("VIDEO_ROOT")
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	m := &exportManager{dataDir: absolute, jobsDir: jobsDir, script: filepath.Join(root, "scripts", "render.mjs"), jobs: make(map[string]*renderTask), queue: make(chan *renderTask, 3)}
	m.runTask = m.render
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".job.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(jobsDir, entry.Name()))
		var job ExportJob
		if err != nil || json.Unmarshal(data, &job) != nil || !validExportID(job.ID) {
			continue
		}
		if job.Status == "running" || job.Status == "queued" {
			job.Status = "failed"
			job.Message = "服务重启中断了导出，请重新导出"
			job.URL = ""
			_ = os.Remove(filepath.Join(absolute, "exports", job.ID+".mp4"))
			_ = os.Remove(filepath.Join(absolute, "exports", job.ID+".mp4.silent.mp4"))
			_ = os.Remove(filepath.Join(jobsDir, job.ID+".project.json"))
			_ = atomicJSON(filepath.Join(jobsDir, entry.Name()), job)
		}
		m.jobs[job.ID] = &renderTask{job: job}
	}
	go m.work()
	return m, nil
}

func validExportID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validateExport(project *Project) error {
	if err := validateProject(project); err != nil {
		return err
	}
	if len(project.Scenes) == 0 {
		return errors.New("请先添加镜头再导出")
	}
	if project.Width < 64 || project.Height < 64 || project.Width > 1920 || project.Height > 1920 || project.Width%2 != 0 || project.Height%2 != 0 {
		return errors.New("导出画布需为 64 到 1920 像素的偶数尺寸")
	}
	if len(project.Audio) > 12 {
		return errors.New("最多支持 12 条音轨")
	}
	for _, track := range project.Audio {
		if track.Start < 0 || track.TrimStart < 0 || math.IsNaN(track.TrimStart) || math.IsInf(track.TrimStart, 0) || track.Duration <= 0 || track.Volume < 0 || track.Volume > 2 || math.IsNaN(track.Volume) || math.IsInf(track.Volume, 0) {
			return errors.New("音轨时间和音量无效")
		}
		if !strings.HasPrefix(track.Src, "/uploads/") || strings.Contains(strings.TrimPrefix(track.Src, "/uploads/"), "/") || strings.Contains(track.Src, "..") {
			return errors.New("音频需先上传到素材库")
		}
	}
	return nil
}

func renderOrigin(r *http.Request) (string, error) {
	if configured := os.Getenv("RENDER_ORIGIN"); configured != "" {
		parsed, err := url.Parse(configured)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", errors.New("RENDER_ORIGIN 配置无效")
		}
		return strings.TrimRight(configured, "/"), nil
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
		port = "8080"
	}
	if host != "localhost" && net.ParseIP(host) == nil {
		return "", errors.New("请从本机地址打开工具后导出")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

func (s *Server) registerRenderRoutes(mux *http.ServeMux) {
	m, setupErr := newExportManager(s.dataDir)
	ready := func(w http.ResponseWriter) bool {
		if setupErr != nil {
			errorJSON(w, 503, "导出存储无法初始化: "+setupErr.Error())
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/exports", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var input struct {
			Project Project `json:"project"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := validateExport(&input.Project); err != nil {
			errorJSON(w, 400, err.Error())
			return
		}
		origin, err := renderOrigin(r)
		if err != nil {
			errorJSON(w, 400, err.Error())
			return
		}
		job, err := m.create(input.Project, origin)
		if err != nil {
			status := 500
			if errors.Is(err, errExportQueueFull) {
				status = 429
			}
			errorJSON(w, status, err.Error())
			return
		}
		writeJSON(w, 202, job)
	})
	mux.HandleFunc("GET /api/exports", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		m.mu.Lock()
		jobs := make([]ExportJob, 0, len(m.jobs))
		for _, task := range m.jobs {
			jobs = append(jobs, task.job)
		}
		m.mu.Unlock()
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
		writeJSON(w, 200, jobs)
	})
	mux.HandleFunc("GET /api/exports/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		job, found := m.get(r.PathValue("id"))
		if !found {
			errorJSON(w, 404, "导出任务不存在")
			return
		}
		writeJSON(w, 200, job)
	})
	mux.HandleFunc("POST /api/exports/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		job, found := m.cancel(r.PathValue("id"))
		if !found {
			errorJSON(w, 404, "导出任务不存在")
			return
		}
		writeJSON(w, 200, job)
	})
}

var errExportQueueFull = errors.New("导出队列已满，请等待当前任务完成")

func (m *exportManager) create(project Project, origin string) (ExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := 0
	for _, task := range m.jobs {
		if task.job.Status == "queued" || task.job.Status == "running" {
			pending++
		}
	}
	if pending >= 4 || len(m.queue) >= cap(m.queue) {
		return ExportJob{}, errExportQueueFull
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ExportJob{}, err
	}
	id := hex.EncodeToString(random[:])
	inputPath := filepath.Join(m.jobsDir, id+".project.json")
	if err := atomicJSON(inputPath, project); err != nil {
		return ExportJob{}, err
	}
	snapshotData, err := json.Marshal(project)
	if err != nil {
		return ExportJob{}, err
	}
	var snapshot Project
	if err := json.Unmarshal(snapshotData, &snapshot); err != nil {
		return ExportJob{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	task := &renderTask{job: ExportJob{ID: id, Status: "queued", Message: "等待导出"}, project: snapshot, origin: origin, inputPath: inputPath, outputPath: filepath.Join(m.dataDir, "exports", id+".mp4"), ctx: ctx, cancel: cancel}
	if err := atomicJSON(filepath.Join(m.jobsDir, id+".job.json"), task.job); err != nil {
		cancel()
		_ = os.Remove(inputPath)
		return ExportJob{}, err
	}
	m.jobs[id] = task
	m.queue <- task
	return task.job, nil
}

func (m *exportManager) get(id string) (ExportJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, found := m.jobs[id]
	if !found {
		return ExportJob{}, false
	}
	return task.job, true
}

func (m *exportManager) cancel(id string) (ExportJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, found := m.jobs[id]
	if !found {
		return ExportJob{}, false
	}
	if task.job.Status == "queued" || task.job.Status == "running" {
		task.job.Status = "cancelled"
		task.job.Message = "导出已取消"
		task.job.URL = ""
		task.cancel()
		_ = atomicJSON(filepath.Join(m.jobsDir, id+".job.json"), task.job)
	}
	return task.job, true
}

func (m *exportManager) update(task *renderTask, status string, progress float64, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task.job.Status == "cancelled" {
		return
	}
	previous := int(task.job.Progress * 100)
	task.job.Status = status
	task.job.Progress = math.Min(1, math.Max(0, progress))
	task.job.Message = message
	if status == "completed" {
		task.job.URL = "/exports/" + task.job.ID + ".mp4"
	}
	if status != "running" || int(task.job.Progress*100) != previous {
		_ = atomicJSON(filepath.Join(m.jobsDir, task.job.ID+".job.json"), task.job)
	}
}

func (m *exportManager) work() {
	for task := range m.queue {
		if task.ctx.Err() != nil {
			_ = os.Remove(task.inputPath)
			continue
		}
		m.update(task, "running", 0, "正在准备导出")
		err := m.runTask(task)
		if task.ctx.Err() != nil {
			_ = os.Remove(task.outputPath)
			_ = os.Remove(task.outputPath + ".silent.mp4")
		} else if err != nil {
			m.update(task, "failed", 0, err.Error())
			_ = os.Remove(task.outputPath)
			_ = os.Remove(task.outputPath + ".silent.mp4")
		} else {
			m.update(task, "completed", 1, "视频导出完成")
		}
		task.cancel()
		_ = os.Remove(task.inputPath)
	}
}

type renderErrors struct {
	mu   sync.Mutex
	data string
}

func (b *renderErrors) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data += string(data)
	if len(b.data) > 8000 {
		b.data = b.data[len(b.data)-8000:]
	}
	return len(data), nil
}

func (b *renderErrors) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.data)
}

func (m *exportManager) render(task *renderTask) error {
	ctx, cancel := context.WithTimeout(task.ctx, 30*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", m.script, task.inputPath, task.outputPath, task.origin, m.dataDir)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	var errorsOutput renderErrors
	command.Stderr = &errorsOutput
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("渲染器无法启动: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("渲染器无法启动: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		var update struct {
			Progress float64 `json:"progress"`
			Message  string  `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &update) == nil && !math.IsNaN(update.Progress) && !math.IsInf(update.Progress, 0) {
			m.update(task, "running", math.Min(0.99, update.Progress), update.Message)
		}
	}
	waitErr := command.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("导出超时，请缩短视频后重试")
	}
	if waitErr != nil {
		message := errorsOutput.String()
		if message == "" {
			message = waitErr.Error()
		}
		if index := strings.IndexByte(message, '\n'); index >= 0 {
			message = message[:index]
		}
		message = strings.TrimSpace(message)
		if strings.Contains(message, "图片无法加载") {
			return errors.New("导出失败: 图片素材无法加载，请检查素材是否存在")
		}
		if strings.Contains(message, "视频编码失败") || strings.Contains(message, "导出失败") {
			return errors.New("导出失败: 视频编码未完成，请重试")
		}
		return errors.New("导出失败: " + message)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("导出进度读取失败: %w", err)
	}
	stat, err := os.Stat(task.outputPath)
	if err != nil || stat.Size() == 0 {
		return errors.New("导出失败，未生成视频文件")
	}
	return nil
}
