import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, Folder, Loader2, Plus, Search, Upload, X } from "lucide-react";
import { api } from "./api";
import { totalFrames, type Project } from "./types";

export function ProjectSwitcher({ currentId, projects, disabled, onProjects, onSelect, onNew, onImport }: {
  currentId: string;
  projects: Project[];
  disabled: boolean;
  onProjects: (projects: Project[]) => void;
  onSelect: (project: Project) => Promise<void>;
  onNew: () => void;
  onImport: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [switching, setSwitching] = useState<string>();
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const onProjectsRef = useRef(onProjects);
  onProjectsRef.current = onProjects;
  useEffect(() => {
    if (!open) return;
    searchRef.current?.focus();
    const controller = new AbortController();
    setLoading(true);
    setError("");
    api<Project[]>("/api/projects", { signal: controller.signal, cache: "no-store" })
      .then(value => { if (!controller.signal.aborted) onProjectsRef.current(value); })
      .catch(cause => { if (!controller.signal.aborted) setError((cause as Error).message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    const outside = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const keyboard = (event: KeyboardEvent) => {
      if (event.key === "Escape") { event.preventDefault(); setOpen(false); triggerRef.current?.focus(); }
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", keyboard);
    return () => {
      controller.abort();
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", keyboard);
    };
  }, [open]);
  const filtered = projects.filter(project => project.name.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
  return (
    <div className="project-switcher" ref={containerRef}>
      <button ref={triggerRef} type="button" className="project-switcher-trigger" aria-label="切换项目" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? "project-switcher-panel" : undefined} onClick={() => setOpen(value => !value)}><ChevronDown size={17} aria-hidden="true" /></button>
      {open && <div id="project-switcher-panel" className="project-switcher-panel" role="dialog" aria-label="切换项目">
        <div className="project-switcher-heading"><strong>切换项目</strong><button type="button" aria-label="关闭项目列表" onClick={() => { setOpen(false); triggerRef.current?.focus(); }}><X size={16} aria-hidden="true" /></button></div>
        <label className="project-switcher-search"><Search size={16} aria-hidden="true" /><span className="sr-only">搜索项目</span><input ref={searchRef} value={query} onChange={event => setQuery(event.target.value)} placeholder="搜索项目" /></label>
        {error && <p className="project-switcher-error" role="alert">{error}</p>}
        <div className="project-switcher-list" aria-busy={loading}>
          {loading && !projects.length ? <p role="status">正在读取项目…</p> : !filtered.length ? <p role="status">没有找到匹配的项目</p> : filtered.map(project => <button type="button" key={project.id} disabled={disabled || !!switching} className={project.id === currentId ? "active" : ""} aria-current={project.id === currentId ? "true" : undefined} onClick={async () => {
            setSwitching(project.id);
            try { await onSelect(project); setOpen(false); } finally { setSwitching(undefined); }
          }}><Folder size={18} aria-hidden="true" /><span><strong>{project.name}</strong><small>{project.scenes.length} 个镜头 · {(totalFrames(project) / project.fps).toFixed(1)} 秒</small></span>{switching === project.id ? <Loader2 className="spin" size={16} aria-hidden="true" /> : project.id === currentId && <Check size={16} aria-label="当前项目" />}</button>)}
        </div>
        <div className="project-switcher-actions"><button type="button" disabled={disabled} onClick={() => { setOpen(false); onNew(); }}><Plus size={16} aria-hidden="true" />新建项目</button><button type="button" disabled={disabled} onClick={() => { setOpen(false); onImport(); }}><Upload size={16} aria-hidden="true" />导入项目</button></div>
      </div>}
    </div>
  );
}
