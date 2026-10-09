import { useEffect, useRef, useState } from "react";
import { ArrowUp, Cable, Check, ChevronRight, Circle, Loader2, Settings2, Sparkles, Square, Wrench } from "lucide-react";
import { api } from "./api";
import { Modal } from "./Dialogs";
import { type Project, type ExportJob } from "./types";
import "./studio.css";

export type StudioRun = {
  id: string;
  projectId: string;
  prompt: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled";
  step: number;
  maxSteps: number;
  message: string;
  createdAt: string;
  updatedAt: string;
  exportJobId?: string;
};
export type StudioEvent = {
  seq: number;
  type: string;
  projectId: string;
  runId?: string;
  tool?: string;
  message: string;
  createdAt: string;
  project?: Project;
  data?: Record<string, unknown>;
};
const isActive = (run?: StudioRun) => run?.status === "running" || run?.status === "queued";
const toolLabels: Record<string, string> = {
  video_project_get: "查看工程",
  video_project_list: "查找工程",
  video_project_create: "创建工程",
  video_edit: "编辑时间线",
  video_undo: "撤销操作",
  video_speech_generate: "生成并放置旁白",
  video_image_generate: "生成并放置图片",
  video_preview: "检查画面",
  video_export: "导出视频",
  video_export_status: "查看导出进度",
};

export function StudioPanel({ projectId, loaded, onEvent, onBusy, beforeStart, onSettings }: {
  projectId: string;
  loaded: boolean;
  onEvent: (event: StudioEvent) => void;
  onBusy: (busy: boolean) => void;
  beforeStart: () => Promise<void>;
  onSettings: () => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [run, setRun] = useState<StudioRun>();
  const [events, setEvents] = useState<StudioEvent[]>([]);
  const [connection, setConnection] = useState("连接中");
  const [error, setError] = useState("");
  const [starting, setStarting] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [copied, setCopied] = useState(false);
  const [exportJob, setExportJob] = useState<ExportJob>();
  const [exportJobId, setExportJobId] = useState<string>();
  const callbacks = useRef({ onEvent, onBusy, beforeStart });
  callbacks.current = { onEvent, onBusy, beforeStart };
  const logRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!loaded) return;
    let cancelled = false;
    setEvents([]);
    setRun(undefined);
    setError("");
    setExportJob(undefined);
    setExportJobId(undefined);
    const receive = (event: StudioEvent) => {
      if (cancelled || event.projectId !== projectId) return;
      callbacks.current.onEvent(event);
      if (event.type !== "snapshot" && event.message) {
        setEvents(items => [...items.filter(item => item.seq !== event.seq), event].slice(-100));
      }
      if (event.type === "run" && event.data) {
        const value = (event.data.run || event.data) as unknown as StudioRun;
        if (value.id && value.status) setRun(value);
      }
      if (event.type === "export.started" && typeof event.data?.id === "string") {
        setExportJobId(event.data.id);
      }
    };
    const stream = new EventSource(`/api/projects/${projectId}/events`);
    stream.onopen = () => { if (!cancelled) setConnection("实时连接"); };
    stream.onerror = () => { if (!cancelled) setConnection("重新连接中"); };
    stream.onmessage = message => {
      try { receive(JSON.parse(message.data)); } catch { setError("无法读取实时更新，请刷新页面。"); }
    };
    api<{ events: StudioEvent[] }>(`/api/projects/${projectId}/activity`).then(value => {
      const lastExport = [...value.events].reverse().find(item => item.type === "export.started");
      if (!cancelled && typeof lastExport?.data?.id === "string") setExportJobId(lastExport.data.id);
      if (!cancelled) setEvents(items => {
        const merged = new Map([...value.events, ...items].filter(item => item.type !== "snapshot" && item.message).map(item => [item.seq, item]));
        return Array.from(merged.values()).sort((a, b) => a.seq - b.seq).slice(-100);
      });
    }).catch(() => {});
    const refreshRuns = () => api<{ runs: StudioRun[] }>(`/api/projects/${projectId}/runs`).then(value => {
      if (!cancelled && value.runs.length) setRun(value.runs[0]);
    }).catch(() => {});
    void refreshRuns();
    const timer = window.setInterval(refreshRuns, 2000);
    return () => {
      cancelled = true;
      stream.close();
      clearInterval(timer);
      callbacks.current.onBusy(false);
    };
  }, [projectId, loaded]);
  useEffect(() => {
    callbacks.current.onBusy(starting || isActive(run));
  }, [starting, run?.status]);
  useEffect(() => {
    const node = logRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [events.length]);
  useEffect(() => {
    const id = exportJobId || run?.exportJobId;
    if (!id) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const value = await api<ExportJob>(`/api/exports/${id}`);
        if (!cancelled) setExportJob(value);
      } catch {}
    };
    void refresh();
    const timer = setInterval(refresh, 1500);
    return () => { cancelled = true; clearInterval(timer); };
  }, [run?.exportJobId, exportJobId]);
  const start = async () => {
    if (!prompt.trim() || starting || isActive(run)) return;
    setStarting(true);
    setError("");
    setExportJob(undefined);
    setExportJobId(undefined);
    try {
      await callbacks.current.beforeStart();
      const value = await api<{ run: StudioRun }>("/api/agent/runs", {
        method: "POST", body: JSON.stringify({ projectId, prompt }),
      });
      setRun(value.run);
      setPrompt("");
    } catch (cause) { setError((cause as Error).message); }
    finally { setStarting(false); }
  };
  const cancel = async () => {
    if (!run) return;
    try {
      const value = await api<{ run: StudioRun }>(`/api/agent/runs/${run.id}/cancel`, { method: "POST" });
      setRun(value.run);
    } catch (cause) { setError((cause as Error).message); }
  };
  const config = JSON.stringify({ mcpServers: { yingxu: { url: `${location.origin}/mcp` } } }, null, 2);
  return <div className="studio-panel">
    <div className="studio-intro">
      <div className="studio-title"><Sparkles size={19} /><h2>AI 剪辑导演</h2><button className="icon-button" aria-label="连接外部 AI" title="连接外部 AI" onClick={() => setConnecting(true)}><Cable size={18} /></button></div>
      <p>描述成片目标，AI 调用工具剪辑。<br />画面和时间线随每一步实时更新。</p>
      <span className="studio-live"><Circle size={7} fill="currentColor" />{connection}</span>
    </div>
    <div className="studio-log" ref={logRef} aria-live="polite" aria-label="AI 执行记录">
      {run && <div className={`studio-run ${run.status}`}><strong>{run.prompt}</strong><span>{isActive(run) ? `正在执行 · 第 ${run.step} 步` : run.status === "completed" ? "执行完成" : run.status === "failed" ? "执行失败" : "已停止"}</span><p>{run.message}</p></div>}
      {!events.length && !run && <div className="studio-empty"><Wrench size={24} /><strong>等待 AI 开始剪辑</strong><p>可以使用内置 AI，也可以通过 MCP 让外部 AI 连接此工程。</p><button className="button secondary" onClick={() => setConnecting(true)}>连接外部 AI<ChevronRight size={14} /></button></div>}
      {events.map(event => <div className={`studio-step ${event.type.includes("error") ? "error" : ""}`} key={event.seq}><span className="studio-step-icon">{event.type === "run" ? <Sparkles size={13} /> : <Wrench size={13} />}</span><div><strong>{event.tool ? toolLabels[event.tool] || event.tool : event.type === "project.updated" ? "工程已更新" : "执行进度"}</strong><p>{event.message}</p><time>{new Date(event.createdAt).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time></div></div>)}
      {exportJob && <div className="studio-export"><strong>{exportJob.status === "completed" ? "视频已导出" : exportJob.status === "failed" ? "视频导出失败" : `视频导出 · ${Math.round(exportJob.progress * 100)}%`}</strong><p>{exportJob.message}</p>{exportJob.url && <a className="button primary" href={exportJob.url} download>下载视频</a>}</div>}
      {error && <div className="chat-message error" role="alert">{error}</div>}
    </div>
    <div className="studio-composer">
      <label htmlFor="studio-prompt">告诉 AI 你想做什么</label>
      <div className="composer-input"><textarea id="studio-prompt" aria-label="向智能助手发送要求" value={prompt} onChange={event => setPrompt(event.target.value)} placeholder="制作一支 30 秒的映序宣传片，展示 AI 自动剪辑，添加中文旁白并导出…" maxLength={6000} rows={4} disabled={starting || isActive(run)} onKeyDown={event => { if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); void start(); } }} /><div className="composer-actions"><button className="icon-button" aria-label="模型设置" onClick={onSettings}><Settings2 size={16} /></button>{isActive(run) ? <button className="send-button" aria-label="停止生成" title="停止 AI 剪辑" onClick={cancel}><Square size={13} /></button> : <button className="send-button" aria-label="发送要求" title="开始 AI 剪辑" onClick={start} disabled={!prompt.trim() || starting || !loaded}>{starting ? <Loader2 size={17} className="spin" /> : <ArrowUp size={18} />}</button>}</div></div>
      <p className="studio-caption">每次工具操作立即保存 · 可停止 · 支持外部 AI</p>
    </div>
    {connecting && <Modal title="连接外部 AI" icon={<Cable size={20} />} onClose={() => setConnecting(false)}>
      <div className="modal-body"><p>在支持 MCP 的 AI 工具中添加下面的服务地址。连接后，AI 能读取工程、剪辑片段、生成图片和旁白、检查画面并导出视频。</p><div className="form-field"><label htmlFor="mcp-url">MCP 服务地址</label><input id="mcp-url" readOnly value={`${location.origin}/mcp`} /></div><div className="form-field"><label htmlFor="mcp-project">当前工程编号</label><input id="mcp-project" readOnly value={projectId} /></div><pre className="source-code studio-config">{config}</pre><div className="status-message">把当前工程编号告诉 AI，并要求它使用映序的剪辑工具。连接类型选择 HTTP。</div></div>
      <footer className="modal-footer"><button className="button secondary" onClick={() => setConnecting(false)}>关闭</button><button className="button primary" onClick={async () => { try { await navigator.clipboard.writeText(config); setCopied(true); } catch { setError("复制失败，可直接选择配置文本复制。"); } }}>{copied ? <Check size={16} /> : <Cable size={16} />}{copied ? "已复制" : "复制连接配置"}</button></footer>
    </Modal>}
  </div>;
}
