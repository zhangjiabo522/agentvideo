import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  AudioLines,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Circle,
  Clapperboard,
  Code2,
  Copy,
  Download,
  Eye,
  EyeOff,
  Film,
  Folder,
  ImagePlus,
  Layers,
  Loader2,
  Lock,
  Maximize,
  MoreHorizontal,
  MousePointer2,
  Pause,
  Play,
  Plus,
  Redo2,
  Save,
  Send,
  Settings2,
  SlidersHorizontal,
  Sparkles,
  Square,
  Trash2,
  Type,
  Undo2,
  Unlock,
  Upload,
  X,
  BarChart3,
} from "lucide-react";
import { api, downloadJSON } from "./api";
import { AssetLibrary, assetKind, type Asset } from "./AssetLibrary";
import { readAudioDuration } from "./media";
import { ProjectSwitcher } from "./ProjectSwitcher";
import { blankScene, createNode, createProject, demoProject } from "./demo";
import { SceneView } from "./SceneView";
import {
  ExportModal,
  NewProjectModal,
  SettingsModal,
  SourceModal,
} from "./Dialogs";
import { SpeechModal, type SpeechResult } from "./SpeechModal";
import { Timeline } from "./Timeline";
import { StudioPanel, type StudioEvent } from "./StudioPanel";
import { ThemeToggle } from "./ThemeToggle";
import {
  locateFrame,
  fitAudioToProject,
  resizeScene,
  totalFrames,
  uid,
  type Project,
  type Scene,
  type SceneNode,
} from "./types";

type SideTab = "scenes" | "assets" | "text" | "layers" | "projects";
type Message = { role: "user" | "assistant" | "error"; text: string };
const clone = <T,>(value: T): T => structuredClone(value);
const contentKey = (value: Project) =>
  JSON.stringify({ ...value, revision: 0, updatedAt: "" });
const seconds = (frame: number, fps = 30) => (frame / fps).toFixed(1);
const clock = (frame: number, fps = 30) =>
  `${Math.floor(frame / fps / 60)
    .toString()
    .padStart(2, "0")}:${Math.floor((frame / fps) % 60)
    .toString()
    .padStart(2, "0")}.${Math.floor(((frame % fps) / fps) * 100)
    .toString()
    .padStart(2, "0")}`;

function IconButton({
  label,
  children,
  onClick,
  disabled = false,
  active = false,
  className = "",
}: {
  label: string;
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  active?: boolean;
  className?: string;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={active}
      className={`icon-button ${active ? "active" : ""} ${className}`}
      onClick={onClick}
      disabled={disabled}
    >
      {children}
    </button>
  );
}

function Field({
  label,
  value,
  onChange,
  min,
  max,
  step = 1,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
}) {
  return (
    <label className="number-field">
      <span>{label}</span>
      <input
        type="number"
        value={Number(value.toFixed(2))}
        min={min}
        max={max}
        step={step}
        onChange={(event) => {
          if (event.target.value !== "") {
            let n = Number(event.target.value);
            if (min !== undefined) n = Math.max(min, n);
            if (max !== undefined) n = Math.min(max, n);
            onChange(n);
          }
        }}
      />
    </label>
  );
}

export default function App() {
  const [project, setProject] = useState<Project>(() => {
    try {
      const local = localStorage.getItem("yingxu-recovery");
      if (local) {
        const value = JSON.parse(local);
        if (
          value.id &&
          value.width >= 64 &&
          value.height >= 64 &&
          value.fps > 0 &&
          Array.isArray(value.scenes) &&
          value.scenes.length > 0 &&
          value.scenes.every(
            (s: Scene) => s.duration > 0 && Array.isArray(s.nodes),
          ) &&
          Array.isArray(value.audio)
        )
          return value;
      }
    } catch {}
    return demoProject();
  });
  const projectRef = useRef(project);
  projectRef.current = project;
  const revisionRef = useRef(project.revision);
  const savedContentRef = useRef(new Map<string, string>());
  const [loaded, setLoaded] = useState(false);
  const [observerMode, setObserverMode] = useState(true);
  const [saveStatus, setSaveStatus] = useState("正在连接");
  const [projects, setProjects] = useState<Project[]>([]);
  const [tab, setTab] = useState<SideTab>("scenes");
  const [panel, setPanel] = useState<"agent" | "properties">("agent");
  const [selectedId, setSelectedId] = useState<string>();
  const [frame, setFrame] = useState(45);
  const [playing, setPlaying] = useState(false);
  const [scale, setScale] = useState(0.5);
  const stageRef = useRef<HTMLDivElement>(null);
  const [modal, setModal] = useState<
    "settings" | "new" | "export" | "source" | "speech" | null
  >(null);
  const [selectedAudioId, setSelectedAudioId] = useState<string>();
  const [toast, setToast] = useState("");
  const [past, setPast] = useState<Project[]>([]);
  const [future, setFuture] = useState<Project[]>([]);
  const [scope, setScope] = useState<"project" | "scene" | "node">("scene");
  const [messages, setMessages] = useState<Message[]>([
    {
      role: "assistant",
      text: "已打开「山野之间」示例工程。4 个镜头，24 秒。",
    },
  ]);
  const [busy, setBusy] = useState(false);
  const [assetBusy, setAssetBusy] = useState(false);
  const [imagePrompt, setImagePrompt] = useState("");
  const [libraryAssets, setLibraryAssets] = useState<Asset[]>([]);
  const [recentAssets, setRecentAssets] = useState<Asset[]>([]);
  const [assetsLoading, setAssetsLoading] = useState(false);
  const [assetsError, setAssetsError] = useState("");
  const [assetRefresh, setAssetRefresh] = useState(0);
  const assetsRequestRef = useRef<AbortController | undefined>(undefined);
  const assetOperationRef = useRef(false);
  const switchProjectRef = useRef(false);
  const interactionRef = useRef({ observerMode, busy });
  interactionRef.current = { observerMode, busy };
  const uploadRef = useRef<HTMLInputElement>(null);
  const importRef = useRef<HTMLInputElement>(null);
  const [saveTick, setSaveTick] = useState(0);
  const savingRef = useRef(false);
  const frameCount = totalFrames(project);
  const current = locateFrame(project, Math.min(frame, frameCount - 1));
  const selected = current.scene?.nodes.find((node) => node.id === selectedId);
  const selectedAudio = project.audio.find(
    (track) => track.id === selectedAudioId,
  );

  const projectAssets = useMemo(() => {
    const images = project.scenes.flatMap(scene => scene.nodes.filter(node => node.type === "image" && node.src).map(node => ({
      id: node.src!, url: node.src!, name: node.name || "工程图片", type: "image", source: "project",
    })));
    const audio = project.audio.map(track => ({
      id: track.src, url: track.src, name: track.name || "工程音频", type: "audio", source: "project",
      durationSeconds: (track.sourceDuration || track.duration + track.trimStart) / project.fps,
    }));
    return [...images, ...audio] as Asset[];
  }, [project.scenes, project.audio, project.fps]);
  const projectAssetKey = projectAssets.map(asset => asset.url).sort().join("|");
  const usedAssetURLs = useMemo(() => new Set(projectAssets.map(asset => asset.url)), [projectAssets]);
  const assets = useMemo(() => {
    const unique = new Map<string, Asset>();
    for (const asset of [...libraryAssets, ...recentAssets, ...projectAssets]) {
      if (!asset.url || unique.has(asset.url)) continue;
      unique.set(asset.url, { ...asset, id: asset.id || asset.url, name: asset.name || "未命名素材" });
    }
    return Array.from(unique.values()).sort((a, b) => Number(usedAssetURLs.has(b.url)) - Number(usedAssetURLs.has(a.url)) || (b.createdAt || "").localeCompare(a.createdAt || ""));
  }, [libraryAssets, recentAssets, projectAssets, usedAssetURLs]);
  const refreshAssets = useCallback(async () => {
    assetsRequestRef.current?.abort();
    const request = new AbortController();
    assetsRequestRef.current = request;
    setAssetsLoading(true);
    setAssetsError("");
    try {
      const result = await api<Asset[] | { assets: Asset[] }>("/api/assets", { signal: request.signal, cache: "no-store" });
      if (request.signal.aborted) return;
      const list = Array.isArray(result) ? result : result.assets;
      if (!Array.isArray(list)) throw new Error("素材列表格式不正确，请刷新重试。");
      setLibraryAssets(list);
      setRecentAssets(items => items.filter(item => !list.some(asset => asset.url === item.url)));
    } catch (error) {
      if (!request.signal.aborted) setAssetsError((error as Error).message || "素材库连接失败，请重试。");
    } finally {
      if (!request.signal.aborted) setAssetsLoading(false);
    }
  }, []);

  useEffect(() => {
    if (tab !== "assets" || !loaded) return;
    void refreshAssets();
    const refreshVisible = () => { if (document.visibilityState === "visible") void refreshAssets(); };
    const timer = window.setInterval(refreshVisible, 15000);
    window.addEventListener("focus", refreshVisible);
    return () => {
      clearInterval(timer);
      window.removeEventListener("focus", refreshVisible);
      assetsRequestRef.current?.abort();
    };
  }, [tab, loaded, project.id, projectAssetKey, assetRefresh, refreshAssets]);

  const notify = useCallback((text: string) => setToast(text), []);
  const receiveStudioEvent = useCallback((event: StudioEvent) => {
    if (event.projectId !== projectRef.current.id) return;
    if (event.type === "assets.updated" || event.tool === "video_image_generate" || event.tool === "video_speech_generate") setAssetRefresh(value => value + 1);
    if (event.type === "preview.seek" && typeof event.data?.frame === "number") {
      setPlaying(false);
      setFrame(Math.max(0, Math.min(event.data.frame, totalFrames(projectRef.current) - 1)));
    }
    const incoming = event.project;
    if (!incoming || incoming.revision <= projectRef.current.revision) return;
    const before = projectRef.current;
    const sameContent = contentKey(before) === contentKey(incoming);
    if (savedContentRef.current.get(before.id) !== contentKey(before) && contentKey(before) !== contentKey(incoming)) {
      notify("AI 已更新工程，当前还有未保存的人工修改。请保存或重新打开工程后继续观看。");
      return;
    }
    revisionRef.current = incoming.revision;
    savedContentRef.current.set(incoming.id, contentKey(incoming));
    projectRef.current = incoming;
    setProject(incoming);
    setProjects(items => [incoming, ...items.filter(item => item.id !== incoming.id)]);
    setSaveStatus(sameContent ? "已保存" : "AI 已保存");
    if (sameContent) return;
    setSelectedId(undefined);
    setSelectedAudioId(undefined);
    setPast([]);
    setFuture([]);
    const changedIndex = incoming.scenes.findIndex((scene, index) => JSON.stringify(scene) !== JSON.stringify(before.scenes[index]));
    if (changedIndex >= 0) {
      const offset = incoming.scenes.slice(0, changedIndex).reduce((sum, scene) => sum + scene.duration, 0);
      setPlaying(false);
      setFrame(offset + Math.min(incoming.fps, incoming.scenes[changedIndex].duration - 1));
    }
  }, [notify]);
  useEffect(() => {
    if (!toast) return;
    const timeout = setTimeout(() => setToast(""), 4500);
    return () => clearTimeout(timeout);
  }, [toast]);

  useEffect(() => {
    let cancelled = false;
    api<Project[]>("/api/projects")
      .then(async (list) => {
        if (cancelled) return;
        setProjects(list);
        const local = projectRef.current;
        const requestedId = new URLSearchParams(location.search).get("project");
        const requested = list.find((item) => item.id === requestedId);
        const saved = list.find((item) => item.id === local.id);
        let value: Project;
        if (requested) value = requested;
        else if (saved) {
          const localTime = new Date(local.updatedAt).getTime();
          if (
            localTime > new Date(saved.updatedAt).getTime() &&
            local.revision === saved.revision
          ) {
            value = await api<Project>(`/api/projects/${local.id}`, {
              method: "PUT",
              body: JSON.stringify(local),
            });
          } else value = saved;
        } else if (list.length && (local.revision > 0 || !localStorage.getItem('yingxu-recovery'))) value = list[0];
        else
          value = await api<Project>("/api/projects", {
            method: "POST",
            body: JSON.stringify(local),
          });
        if (cancelled) return;
        revisionRef.current = value.revision;
        savedContentRef.current.set(value.id, contentKey(value));
        projectRef.current = value;
        setProject(value);
        const requestedFrame = Number(new URLSearchParams(location.search).get("frame") ?? 45);
        setFrame(Math.max(0, Math.min(Number.isFinite(requestedFrame) ? Math.round(requestedFrame) : 45, totalFrames(value) - 1)));
        setLoaded(true);
        setSaveStatus("已保存");
      })
      .catch((error) => {
        if (!cancelled) {
          setLoaded(true);
          setSaveStatus("连接失败 · 本地已保留");
          notify(error.message);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [notify]);

  useEffect(() => {
    if (!loaded) return;
    try {
      localStorage.setItem("yingxu-recovery", JSON.stringify(project));
    } catch {
      setSaveStatus("本地空间不足");
    }
    if (savedContentRef.current.get(project.id) === contentKey(project)) return;
    const timer = setTimeout(async () => {
      if (savingRef.current) {
        setSaveTick((n) => n + 1);
        return;
      }
      savingRef.current = true;
      const snapshot = projectRef.current;
      setSaveStatus("保存中");
      try {
        const saved = await api<Project>(`/api/projects/${snapshot.id}`, {
          method: "PUT",
          body: JSON.stringify({ ...snapshot, revision: revisionRef.current }),
        });
        if (projectRef.current.id !== saved.id) return;
        if (saved.revision < revisionRef.current) {
          if (savedContentRef.current.get(saved.id) !== contentKey(projectRef.current)) {
            setSaveStatus("待保存");
            setSaveTick((n) => n + 1);
          }
          return;
        }
        savedContentRef.current.set(saved.id, contentKey(saved));
        revisionRef.current = saved.revision;
        setProjects((items) => [
          saved,
          ...items.filter((item) => item.id !== saved.id),
        ]);
        if (contentKey(projectRef.current) === contentKey(snapshot)) {
          projectRef.current = saved;
          setProject(saved);
          setSaveStatus("已保存");
        } else {
          setSaveStatus("待保存");
          setSaveTick((n) => n + 1);
        }
      } catch (error) {
        setSaveStatus("保存失败 · 本地已保留");
        notify((error as Error).message);
      } finally {
        savingRef.current = false;
      }
    }, 1000);
    return () => clearTimeout(timer);
  }, [project.updatedAt, loaded, saveTick, notify]);

  useEffect(() => {
    setFrame((value) => Math.max(0, Math.min(value, frameCount - 1)));
  }, [frameCount]);

  useEffect(() => {
    const target = stageRef.current;
    if (!target) return;
    const observer = new ResizeObserver((entries) => {
      const box = entries[0].contentRect;
      setScale(
        Math.max(0.05, Math.min(
          (box.width - (box.width < 600 ? 24 : 40)) / project.width,
          (box.height - 52) / project.height,
          2,
        )),
      );
    });
    observer.observe(target);
    return () => observer.disconnect();
  }, [project.width, project.height]);

  useEffect(() => {
    if (!playing) return;
    let animation: number;
    const started = performance.now();
    const startFrame = frame;
    const tick = (time: number) => {
      const next =
        startFrame + Math.floor(((time - started) / 1000) * project.fps);
      if (next >= frameCount) {
        setFrame(frameCount - 1);
        setPlaying(false);
        return;
      }
      setFrame(next);
      animation = requestAnimationFrame(tick);
    };
    animation = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(animation);
  }, [playing, project.fps, frameCount]);

  const audioRefs = useRef(new Map<string, HTMLAudioElement>());
  useEffect(() => {
    const tracks = new Set(project.audio.map((track) => track.id));
    for (const [id, audio] of audioRefs.current)
      if (!tracks.has(id)) {
        audio.pause();
        audioRefs.current.delete(id);
      }
    for (const track of project.audio) {
      let audio = audioRefs.current.get(track.id);
      if (!audio || audio.getAttribute("src") !== track.src) {
        audio?.pause();
        audio = new Audio(track.src);
        audioRefs.current.set(track.id, audio);
      }
      audio.volume = Math.max(0, Math.min(1, track.volume));
      const localFrame = frame - track.start;
      const fadeIn = track.fadeIn
        ? Math.max(0, Math.min(1, localFrame / track.fadeIn))
        : 1;
      const fadeOut = track.fadeOut
        ? Math.max(
            0,
            Math.min(1, (track.duration - localFrame) / track.fadeOut),
          )
        : 1;
      audio.volume = Math.max(0, Math.min(1, track.volume * fadeIn * fadeOut));
      const active =
        frame >= track.start && frame < track.start + track.duration;
      const expected = (frame - track.start + track.trimStart) / project.fps;
      if (active && Math.abs(audio.currentTime - expected) > 0.15)
        audio.currentTime = Math.max(0, expected);
      if (playing && active) audio.play().catch(() => {});
      else audio.pause();
    }
  }, [frame, playing, project.audio, project.fps]);
  useEffect(
    () => () => {
      for (const audio of audioRefs.current.values()) audio.pause();
    },
    [],
  );

  function edit(mutator: (draft: Project) => void, history = true) {
    if (switchProjectRef.current) {
      notify("正在切换工程，请稍候再修改。");
      return false;
    }
    if (interactionRef.current.observerMode || interactionRef.current.busy) {
      notify(interactionRef.current.busy ? "AI 正在剪辑，请先停止任务再接管。" : "当前为实时观看，点击人工接管后可手动修改。");
      return false;
    }
    const before = projectRef.current;
    const draft = clone(before);
    mutator(draft);
    if (
      draft.scenes.length > 20 ||
      totalFrames(draft) > 120 * draft.fps ||
      draft.scenes.reduce((sum, scene) => sum + scene.nodes.length, 0) > 300 ||
      draft.audio.length > 12
    ) {
      notify("工程上限：120 秒、20 个镜头、300 个图层、12 条音轨");
      return false;
    }
    if (fitAudioToProject(draft))
      notify("超出工程结尾的音频片段已裁掉，可撤销恢复；原素材保留。");
    draft.updatedAt = new Date().toISOString();
    if (history) {
      setPast((items) => [...items.slice(-49), clone(before)]);
      setFuture([]);
    }
    projectRef.current = draft;
    setProject(draft);
    setSaveStatus("待保存");
    return true;
  }

  function updateNode(patch: Partial<SceneNode>) {
    if (!selected || (selected.locked && patch.locked === undefined)) return;
    edit((draft) => {
      const node = draft.scenes
        .find((scene) => scene.id === current.scene.id)
        ?.nodes.find((node) => node.id === selected.id);
      if (node) {
        Object.assign(node, patch);
        node.protected = [
          ...new Set([
            ...(node.protected || []),
            ...Object.keys(patch).filter(
              (key) => key !== "locked" && key !== "hidden",
            ),
          ]),
        ];
      }
    });
  }

  function goScene(index: number) {
    const latest = projectRef.current;
    index = Math.min(index, latest.scenes.length - 1);
    setFrame(
      latest.scenes
        .slice(0, index)
        .reduce((sum, scene) => sum + scene.duration, 0) +
        Math.min(latest.fps, latest.scenes[index].duration - 1),
    );
    setSelectedId(undefined);
    setSelectedAudioId(undefined);
    setPlaying(false);
  }

  function updateAudio(patch: Partial<Project["audio"][number]>) {
    if (!selectedAudio) return;
    edit((draft) => {
      const audio = draft.audio.find((track) => track.id === selectedAudio.id)!;
      Object.assign(audio, patch);
      audio.start = Math.max(0, Math.min(audio.start, frameCount - 1));
      audio.trimStart = Math.max(0, audio.trimStart);
      audio.duration = Math.max(
        1,
        Math.min(
          audio.duration,
          frameCount - audio.start,
          (audio.sourceDuration || audio.trimStart + audio.duration) -
            audio.trimStart,
        ),
      );
      audio.fadeIn = Math.min(audio.fadeIn || 0, audio.duration);
      audio.fadeOut = Math.min(audio.fadeOut || 0, audio.duration);
    });
  }

  function undo() {
    if (interactionRef.current.observerMode || interactionRef.current.busy || switchProjectRef.current) return;
    if (!past.length) return;
    const previous = past[past.length - 1];
    const snapshot = clone(projectRef.current);
    setFuture((items) => [...items, snapshot]);
    setPast((items) => items.slice(0, -1));
    const value = {
      ...clone(previous),
      revision: revisionRef.current,
      updatedAt: new Date().toISOString(),
    };
    projectRef.current = value;
    setProject(value);
  }

  function redo() {
    if (interactionRef.current.observerMode || interactionRef.current.busy || switchProjectRef.current) return;
    if (!future.length) return;
    const next = future[future.length - 1];
    const snapshot = clone(projectRef.current);
    setPast((items) => [...items, snapshot]);
    setFuture((items) => items.slice(0, -1));
    const value = {
      ...clone(next),
      revision: revisionRef.current,
      updatedAt: new Date().toISOString(),
    };
    projectRef.current = value;
    setProject(value);
  }

  function addNode(
    type: SceneNode["type"],
    overrides: Partial<SceneNode> = {},
  ) {
    const node = createNode(type, current.scene.duration, {
      x: project.width * 0.12,
      y: project.height * 0.25,
      width: project.width * 0.6,
      height: type === "chart" ? project.height * 0.45 : project.height * 0.24,
      ...overrides,
    });
    edit((draft) => draft.scenes[current.index].nodes.push(node));
    setSelectedId(node.id);
    setPanel("properties");
  }

  async function saveBeforeSwitch() {
    while (savingRef.current)
      await new Promise((resolve) => setTimeout(resolve, 50));
    const snapshot = projectRef.current;
    if (savedContentRef.current.get(snapshot.id) === contentKey(snapshot))
      return;
    savingRef.current = true;
    try {
      const saved = await api<Project>(`/api/projects/${snapshot.id}`, {
        method: "PUT",
        body: JSON.stringify({ ...snapshot, revision: revisionRef.current }),
      });
      if (projectRef.current.id !== saved.id)
        throw new Error("保存期间工程已切换，请重新提交当前操作。");
      if (saved.revision >= revisionRef.current) {
        revisionRef.current = saved.revision;
        savedContentRef.current.set(saved.id, contentKey(saved));
        setProjects((items) => [
          saved,
          ...items.filter((item) => item.id !== saved.id),
        ]);
        if (contentKey(projectRef.current) === contentKey(snapshot)) {
          projectRef.current = saved;
          setProject(saved);
          setSaveStatus("已保存");
        }
      }
      if (savedContentRef.current.get(saved.id) !== contentKey(projectRef.current))
        throw new Error("保存期间工程又有修改，请稍后切换。");
    } finally {
      savingRef.current = false;
    }
  }

  async function beforeAgentStart() {
    if (switchProjectRef.current || assetOperationRef.current) throw new Error("请先等待当前素材或工程操作完成，再启动 AI 剪辑。");
    await saveBeforeSwitch();
    if (switchProjectRef.current || assetOperationRef.current) throw new Error("当前正在处理素材或工程，请稍后再启动 AI 剪辑。");
  }

  function addSpeech(result: SpeechResult, text: string) {
    rememberAsset({ ...result, id: result.url, source: "speech", durationSeconds: result.durationSeconds || result.duration / 30 });
    if (interactionRef.current.observerMode || interactionRef.current.busy) {
      setModal(null);
      notify("旁白已保存在素材库，人工接管后可加入时间线。");
      return;
    }
    const duration = Math.max(
      1,
      Math.ceil((result.durationSeconds || result.duration / 30) * project.fps),
    );
    const start = Math.min(current.offset, Math.max(0, frameCount - duration));
    const usable = Math.min(duration, frameCount - start);
    const changed = edit((draft) =>
      draft.audio.push({
        id: uid(),
        name: `旁白 · ${text.slice(0, 12)}`,
        src: result.url,
        start,
        trimStart: 0,
        duration: usable,
        sourceDuration: duration,
        volume: 1,
        fadeIn: 0,
        fadeOut: 0,
      }),
    );
    if (!changed) return;
    setModal(null);
    notify(
      duration > usable
        ? "旁白已加入，超出项目部分已裁剪，可在音轨属性调整"
        : "旁白已加入时间线",
    );
  }

  async function newProject(name: string, width: number, height: number) {
    if (switchProjectRef.current) return;
    switchProjectRef.current = true;
    try {
      if (assetOperationRef.current || interactionRef.current.busy)
        throw new Error("请先停止或等待当前生成任务完成，再切换工程。");
      await saveBeforeSwitch();
      const value = await api<Project>("/api/projects", {
        method: "POST",
        body: JSON.stringify(createProject(name, width, height)),
      });
      if (interactionRef.current.busy) {
        setProjects(items => [value, ...items.filter(item => item.id !== value.id)]);
        throw new Error("新工程已创建并保留在项目列表；AI 任务正在运行，暂未切换。");
      }
      revisionRef.current = value.revision;
      savedContentRef.current.set(value.id, contentKey(value));
      projectRef.current = value;
      setProject(value);
      setPlaying(false);
      setFrame(0);
      setSelectedId(undefined);
      setSelectedAudioId(undefined);
      setPast([]);
      setFuture([]);
      setModal(null);
      setMessages([{ role: "assistant", text: `已创建「${name}」。` }]);
      setProjects((items) => [value, ...items]);
    } catch (error) {
      notify((error as Error).message);
      throw error;
    } finally {
      switchProjectRef.current = false;
    }
  }

  async function openProject(value: Project) {
    if (assetOperationRef.current || interactionRef.current.busy || switchProjectRef.current) {
      notify("请先停止或等待当前任务完成，再切换工程。");
      return;
    }
    switchProjectRef.current = true;
    try {
      await saveBeforeSwitch();
      const latest = await api<Project>(`/api/projects/${value.id}`, { cache: "no-store" });
      if (interactionRef.current.busy) throw new Error("AI 任务已开始，请停止后再切换工程。");
      setPlaying(false);
      revisionRef.current = latest.revision;
      savedContentRef.current.set(latest.id, contentKey(latest));
      projectRef.current = clone(latest);
      setProject(projectRef.current);
      setProjects(items => [latest, ...items.filter(item => item.id !== latest.id)]);
      setFrame(0);
      setSelectedId(undefined);
      setSelectedAudioId(undefined);
      setPast([]);
      setFuture([]);
      setTab("scenes");
      setSaveStatus("已保存");
      setMessages([{ role: "assistant", text: `已打开「${latest.name}」。` }]);
    } catch (error) {
      notify((error as Error).message);
    } finally {
      switchProjectRef.current = false;
    }
  }

  function rememberAsset(asset: Asset) {
    setRecentAssets(items => [asset, ...items.filter(item => item.url !== asset.url)]);
    setAssetRefresh(value => value + 1);
  }

  async function placeAsset(asset: Asset, target = { projectId: projectRef.current.id, sceneId: current.scene.id, nodeId: selectedId, frame }) {
    const writable = () => projectRef.current.id === target.projectId && !interactionRef.current.observerMode && !interactionRef.current.busy;
    if (!writable()) {
      notify("素材已保存在素材库；人工接管后可以放入镜头。");
      return;
    }
    if (assetKind(asset) === "audio") {
      const durationSeconds = asset.durationSeconds && Number.isFinite(asset.durationSeconds) && asset.durationSeconds > 0
        ? asset.durationSeconds : await readAudioDuration(asset.url);
      if (!writable()) {
        notify("素材已保留，工程或操作模式已变化，未添加到时间线。");
        return;
      }
      const latest = projectRef.current;
      const duration = Math.max(1, Math.ceil(durationSeconds * latest.fps));
      const start = Math.max(0, Math.min(target.frame, totalFrames(latest) - 1));
      const usable = Math.min(duration, totalFrames(latest) - start);
      const id = uid();
      const changed = edit(draft => draft.audio.push({
        id, name: asset.name, src: asset.url, start, trimStart: 0,
        duration: usable, sourceDuration: duration, volume: 0.8,
      }));
      if (changed) {
        setSelectedId(undefined);
        setSelectedAudioId(id);
        setPanel("properties");
        notify(duration > usable ? "音频已加入，超出工程的部分已裁剪。" : "音频已加入时间线");
      }
      return;
    }
    const latest = projectRef.current;
    const scene = latest.scenes.find(item => item.id === target.sceneId);
    if (!scene) throw new Error("目标镜头已变更，请重新选择镜头后添加素材。");
    const selectedImage = scene.nodes.find(node => node.id === target.nodeId && node.type === "image");
    if (selectedImage?.locked) throw new Error("请先解锁选中的图片，或取消选择后新增图片。");
    if (selectedImage) {
      const changed = edit(draft => {
        const node = draft.scenes.find(item => item.id === target.sceneId)?.nodes.find(item => item.id === selectedImage.id);
        if (!node) return;
        node.src = asset.url;
        node.name = asset.name;
        node.protected = [...new Set([...(node.protected || []), "src", "name"])];
      });
      if (changed) notify("选中的图片已替换");
      return;
    }
    const ratio = asset.width && asset.height ? asset.width / asset.height : 16 / 9;
    const width = Math.min(latest.width * 0.72, latest.height * 0.66 * ratio);
    const height = width / ratio;
    const node = createNode("image", scene.duration, {
      src: asset.url, name: asset.name, objectFit: "contain", width, height,
      x: (latest.width - width) / 2, y: (latest.height - height) / 2,
    });
    const changed = edit(draft => draft.scenes.find(item => item.id === target.sceneId)?.nodes.push(node));
    if (changed) {
      setSelectedId(node.id);
      setSelectedAudioId(undefined);
      setPanel("properties");
      notify("图片已添加到镜头");
    }
  }

  async function useAsset(asset: Asset) {
    if (assetOperationRef.current || switchProjectRef.current) return;
    assetOperationRef.current = true;
    setAssetBusy(true);
    try {
      await placeAsset(asset);
    } catch (error) {
      notify((error as Error).message);
    } finally {
      assetOperationRef.current = false;
      setAssetBusy(false);
    }
  }

  async function upload(file: File) {
    if (assetOperationRef.current || switchProjectRef.current) return;
    if (!file.size || file.size > 30 * 1024 * 1024) {
      notify("请选择 1 字节至 30 MB 的图片或音频文件。");
      return;
    }
    const target = { projectId: projectRef.current.id, sceneId: current.scene.id, nodeId: selectedId, frame };
    const shouldPlace = !interactionRef.current.observerMode && !interactionRef.current.busy;
    assetOperationRef.current = true;
    setAssetBusy(true);
    try {
      const form = new FormData();
      form.append("file", file);
      const result = await api<Asset>("/api/uploads", { method: "POST", body: form });
      let dimensions: { width?: number; height?: number } = {};
      if (assetKind(result) === "image") {
        try {
          const bitmap = await createImageBitmap(file);
          dimensions = { width: bitmap.width, height: bitmap.height };
          bitmap.close();
        } catch {}
      }
      const value = { ...result, ...dimensions, id: result.id || result.url, size: file.size, source: "upload" };
      rememberAsset(value);
      if (shouldPlace) await placeAsset(value, target);
      else notify("素材已上传到素材库，AI 可以使用。人工接管后可手动放入镜头。");
    } catch (error) {
      notify((error as Error).message);
    } finally {
      assetOperationRef.current = false;
      setAssetBusy(false);
    }
  }

  async function generateImage() {
    if (!imagePrompt.trim() || assetOperationRef.current || switchProjectRef.current || interactionRef.current.observerMode || interactionRef.current.busy) return;
    const target = { projectId: projectRef.current.id, sceneId: current.scene.id, nodeId: selectedId, frame };
    assetOperationRef.current = true;
    setAssetBusy(true);
    try {
      const result = await api<Asset>("/api/images/generate", {
        method: "POST",
        body: JSON.stringify({ prompt: imagePrompt.trim(), width: 1024, height: 1024 }),
      });
      const asset = { ...result, id: result.id || result.url, name: result.name || "生成图片", type: result.type || "image", source: "generated", width: result.width || 1024, height: result.height || 1024 };
      rememberAsset(asset);
      await placeAsset(asset, target);
    } catch (error) {
      notify((error as Error).message);
    } finally {
      assetOperationRef.current = false;
      setAssetBusy(false);
    }
  }

  const pointerHistory = useRef(false);
  function recordHistory() {
    const snapshot = clone(projectRef.current);
    setPast((items) => [...items.slice(-49), snapshot]);
    setFuture([]);
  }
  const dragNode = (id: string, x: number, y: number) => {
    if (!pointerHistory.current) {
      recordHistory();
      pointerHistory.current = true;
    }
    edit((draft) => {
      const node = draft.scenes[current.index].nodes.find(
        (node) => node.id === id,
      );
      if (node && !node.locked) {
        node.x = x;
        node.y = y;
        node.protected = [...new Set([...(node.protected || []), "x", "y"])];
      }
    }, false);
  };
  const resizeNode = (id: string, width: number, height: number) => {
    if (!pointerHistory.current) {
      recordHistory();
      pointerHistory.current = true;
    }
    edit((draft) => {
      const node = draft.scenes[current.index].nodes.find((n) => n.id === id);
      if (node && !node.locked) {
        node.width = width;
        node.height = height;
        node.protected = [
          ...new Set([...(node.protected || []), "width", "height"]),
        ];
      }
    }, false);
  };
  useEffect(() => {
    const end = () => {
      pointerHistory.current = false;
    };
    window.addEventListener("pointerup", end);
    window.addEventListener("keyup", end);
    return () => {
      window.removeEventListener("pointerup", end);
      window.removeEventListener("keyup", end);
    };
  }, []);

  const actionsRef = useRef({ undo, redo });
  actionsRef.current = { undo, redo };
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      if (modal) return;
      if (
        (event.target as HTMLElement)?.closest(
          'input,textarea,select,button,a,[role="dialog"],[contenteditable="true"]',
        )
      )
        return;
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "z") {
        event.preventDefault();
        if (event.shiftKey) actionsRef.current.redo();
        else actionsRef.current.undo();
      }
      if (event.code === "Space" && !modal) {
        event.preventDefault();
        setPlaying((value) => !value);
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [modal]);

  async function importProject(file: File) {
    if (switchProjectRef.current) return;
    switchProjectRef.current = true;
    try {
      if (assetOperationRef.current || interactionRef.current.busy)
        throw new Error("请先停止或等待当前生成任务完成，再导入工程。");
      await saveBeforeSwitch();
      if (file.name.toLowerCase().endsWith(".zip")) {
        const form = new FormData();
        form.append("file", file);
        const saved = await api<Project>("/api/projects/import", {
          method: "POST",
          body: form,
        });
        setProjects((items) => [saved, ...items]);
        switchProjectRef.current = false;
        if (interactionRef.current.busy) {
          notify("工程与素材已导入；AI 任务正在运行，暂未切换。");
          return;
        }
        await openProject(saved);
        notify("工程与素材已导入");
        return;
      }
      const value = JSON.parse(await file.text()) as Project;
      value.id = uid();
      value.revision = 0;
      value.name = `${value.name} · 导入`;
      const saved = await api<Project>("/api/projects", {
        method: "POST",
        body: JSON.stringify(value),
      });
      setProjects((items) => [saved, ...items]);
      switchProjectRef.current = false;
      if (interactionRef.current.busy) {
        notify("工程已导入；AI 任务正在运行，暂未切换。");
        return;
      }
      await openProject(saved);
      notify("工程已导入");
    } catch (error) {
      notify(`导入失败：${(error as Error).message}`);
    } finally {
      switchProjectRef.current = false;
    }
  }

  function sceneMutation(type: "up" | "down" | "copy" | "delete") {
    if (type === "delete" && project.scenes.length === 1) return;
    const index = current.index;
    edit((draft) => {
      if (type === "copy") {
        const scene = clone(draft.scenes[index]);
        scene.id = uid();
        scene.name += " · 副本";
        scene.nodes.forEach((n) => (n.id = uid()));
        draft.scenes.splice(index + 1, 0, scene);
      }
      if (type === "delete") draft.scenes.splice(index, 1);
      if (type === "up" && index > 0)
        [draft.scenes[index - 1], draft.scenes[index]] = [
          draft.scenes[index],
          draft.scenes[index - 1],
        ];
      if (type === "down" && index < draft.scenes.length - 1)
        [draft.scenes[index + 1], draft.scenes[index]] = [
          draft.scenes[index],
          draft.scenes[index + 1],
        ];
    });
    setSelectedId(undefined);
    setFrame(Math.min(frame, totalFrames(projectRef.current) - 1));
  }

  return (
    <div className={`app-shell ${observerMode ? "observer-mode" : ""}`}>
      <a className="skip-link" href="#workspace">
        跳转至画布
      </a>
      <header className="topbar">
        <button
          className="brand"
          onClick={() => setTab("projects")}
          aria-label="映序项目"
        >
          <span className="brand-symbol">
            <Film size={20} />
          </span>
          <span>映序</span>
          <span className="brand-divider" />
        </button>
        <div className="project-heading">
          <input
            aria-label="项目名称"
            value={project.name}
            readOnly={observerMode || busy}
            onChange={(e) =>
              edit((draft) => {
                draft.name = e.target.value;
              })
            }
          />
          <ProjectSwitcher currentId={project.id} projects={projects} disabled={busy || assetBusy} onProjects={setProjects} onSelect={openProject} onNew={() => setModal("new")} onImport={() => importRef.current?.click()} />
          <span
            className={`save-state ${saveStatus.includes("失败") ? "error" : ""}`}
          >
            <span className="save-dot" />
            {saveStatus}
          </span>
        </div>
        <div className="top-actions">
          <button className="workflow-mode" aria-label={observerMode ? "人工接管" : "返回实时观看"} disabled={busy} onClick={() => setObserverMode(value => !value)}>{observerMode ? <Eye size={15} /> : <MousePointer2 size={15} />}{observerMode ? "AI 剪辑 · 观看" : "人工接管中"}</button>
          <ThemeToggle />
          <IconButton label="撤销" onClick={undo} disabled={!past.length || observerMode || busy}>
            <Undo2 size={17} />
          </IconButton>
          <IconButton label="重做" onClick={redo} disabled={!future.length || observerMode || busy}>
            <Redo2 size={17} />
          </IconButton>
          <span className="vertical-divider" />
          <IconButton label="查看场景数据" onClick={() => setModal("source")}>
            <Code2 size={18} />
          </IconButton>
          <IconButton label="模型设置" onClick={() => setModal("settings")}>
            <Settings2 size={18} />
          </IconButton>
          <button
            className="button primary export-button"
            onClick={() => {
              setPlaying(false);
              setModal("export");
            }}
          >
            <Download size={16} />
            导出视频
          </button>
          <span className="avatar" aria-label="本地工作区">
            映
          </span>
        </div>
      </header>
      <div className="observer-banner"><Sparkles size={14} /><span>{busy ? "AI 正在调用剪辑工具，画面和时间线实时更新" : observerMode ? "AI 操作工作台 · 描述目标或连接外部 AI，实时观看制作过程" : "已人工接管 · 开始 AI 任务前会先保存当前修改"}</span><button onClick={() => { if (busy) { notify("请在 AI 剪辑导演中停止当前任务后接管。"); return; } setObserverMode(value => !value); }}>{observerMode ? "人工接管" : "返回观看"}</button></div>
      <div className="editor-layout">
        <nav className="toolrail" aria-label="工作区导航">
          {(
            [
              { id: "projects", name: "项目", icon: Folder },
              { id: "scenes", name: "镜头", icon: Clapperboard },
              { id: "assets", name: "素材", icon: ImagePlus },
              { id: "text", name: "文字", icon: Type },
              { id: "layers", name: "图层", icon: Layers },
            ] as const
          ).map((item) => (
            <button
              key={item.id}
              className={`rail-button ${tab === item.id ? "active" : ""}`}
              title={item.name}
              aria-pressed={tab === item.id}
              onClick={() => setTab(item.id)}
            >
              <item.icon size={21} />
              <span>{item.name}</span>
            </button>
          ))}
          <div className="rail-bottom">
            <IconButton label="新建工程" onClick={() => setModal("new")}>
              <Plus size={20} />
            </IconButton>
          </div>
        </nav>
        <aside className="library-panel">
          <div className="panel-heading">
            <h2>
              {tab === "scenes"
                ? "分镜"
                : tab === "assets"
                  ? "素材库"
                  : tab === "text"
                    ? "文字与图形"
                    : tab === "layers"
                      ? "当前镜头图层"
                      : "我的项目"}
            </h2>
            {tab !== "text" && <span className="count">
              {tab === "scenes"
                ? project.scenes.length
                : tab === "layers"
                  ? current.scene.nodes.length
                  : tab === "assets" ? assets.length : projects.length}
            </span>}
            <IconButton
              label={tab === "scenes" ? "添加镜头" : tab === "assets" ? "上传素材" : "新建工程"}
              disabled={busy || assetBusy || (tab === "scenes" && observerMode)}
              onClick={() => {
                if (tab === "scenes") {
                  edit((d) => d.scenes.push(blankScene()));
                } else if (tab === "assets") uploadRef.current?.click();
                else setModal("new");
              }}
            >
              <Plus size={17} />
            </IconButton>
          </div>
          <div className={`library-content ${tab === "assets" ? "asset-library-content" : ""}`}>
            {tab === "scenes" && (
              <>
                <div className="section-caption">
                  {project.width} × {project.height}
                  <span>{seconds(frameCount, project.fps)} 秒</span>
                </div>
                <div className="scene-list">
                  {project.scenes.map((scene, index) => (
                    <button
                      key={scene.id}
                      className={`scene-item ${current.index === index ? "active" : ""}`}
                      onClick={() => goScene(index)}
                    >
                      <div
                        className="scene-thumb"
                        style={{
                          aspectRatio: `${project.width}/${project.height}`,
                        }}
                      >
                        <div
                          className="thumb-scale"
                          style={{
                            width: project.width,
                            height: project.height,
                            transform: `scale(${194 / project.width})`,
                          }}
                        >
                          <SceneView
                            project={project}
                            scene={scene}
                            frame={Math.min(45, scene.duration - 1)}
                          />
                        </div>
                        <span className="scene-index">
                          {String(index + 1).padStart(2, "0")}
                        </span>
                        <span className="scene-duration">
                          {seconds(scene.duration, project.fps)}s
                        </span>
                      </div>
                      <div className="scene-caption">
                        <span>{scene.name}</span>
                        {current.index === index ? (
                          <span className="selected-indicator" />
                        ) : (
                          <MoreHorizontal size={15} />
                        )}
                      </div>
                    </button>
                  ))}
                </div>
                <button
                  className="add-scene"
                  onClick={() => edit((d) => d.scenes.push(blankScene()))}
                >
                  <Plus size={16} />
                  添加镜头
                </button>
              </>
            )}
            {tab === "assets" && (
              <>
                <button
                  className="button upload-button"
                  onClick={() => uploadRef.current?.click()}
                  disabled={assetBusy || busy}
                >
                  {assetBusy ? (
                    <Loader2 className="spin" size={17} />
                  ) : (
                    <Upload size={17} />
                  )}
                  上传图片或音频
                </button>
                <button
                  className="button secondary full-width"
                  disabled={observerMode || busy}
                  onClick={() => setModal("speech")}
                >
                  <AudioLines size={16} />
                  生成旁白
                </button>
                <AssetLibrary
                  assets={assets}
                  loading={assetsLoading}
                  error={assetsError}
                  disabled={observerMode || busy || assetBusy}
                  usedURLs={usedAssetURLs}
                  onRefresh={() => void refreshAssets()}
                  onUse={asset => void useAsset(asset)}
                />
                <div className="subsection-title">
                  <Sparkles size={15} />
                  生成图片
                </div>
                <label className="field-label" htmlFor="image-prompt">
                  画面描述
                </label>
                <textarea
                  id="image-prompt"
                  rows={4}
                  value={imagePrompt}
                  onChange={(e) => setImagePrompt(e.target.value)}
                  placeholder="黄昏的山野，纪实摄影，柔和光线"
                />
                <button
                  className="button secondary full-width"
                  onClick={generateImage}
                  disabled={!imagePrompt.trim() || assetBusy || observerMode || busy}
                >
                  {assetBusy ? (
                    <Loader2 className="spin" size={15} />
                  ) : (
                    <Sparkles size={15} />
                  )}
                  生成并添加
                </button>
              </>
            )}
            {tab === "text" && (
              <>
                <button
                  className="text-preset large"
                  onClick={() =>
                    addNode("text", {
                      text: "新的标题",
                      fontSize: 72,
                      name: "标题",
                    })
                  }
                >
                  添加标题
                  <Plus size={16} />
                </button>
                <button
                  className="text-preset"
                  onClick={() =>
                    addNode("text", {
                      text: "写下一段正文",
                      fontSize: 32,
                      name: "正文",
                    })
                  }
                >
                  添加正文
                  <Plus size={16} />
                </button>
                <div className="subsection-title">基础图形</div>
                <div className="shape-grid">
                  <button
                    onClick={() =>
                      addNode("rect", {
                        color: "#b3d7b9",
                        width: 300,
                        height: 240,
                      })
                    }
                  >
                    <Square size={30} />
                    <span>矩形</span>
                  </button>
                  <button
                    onClick={() =>
                      addNode("circle", {
                        color: "#b3d7b9",
                        width: 240,
                        height: 240,
                      })
                    }
                  >
                    <Circle size={30} />
                    <span>圆形</span>
                  </button>
                  <button
                    onClick={() =>
                      addNode("chart", {
                        width: project.width * 0.65,
                        height: project.height * 0.45,
                        color: "#b3d7b9",
                      })
                    }
                  >
                    <BarChart3 size={30} />
                    <span>柱状图</span>
                  </button>
                </div>
              </>
            )}
            {tab === "layers" && (
              <div className="layer-list">
                {[...current.scene.nodes].reverse().map((node) => (
                  <div
                    key={node.id}
                    className={`layer-row ${selectedId === node.id ? "active" : ""}`}
                  >
                    <button
                      className="layer-select"
                      onClick={() => {
                        setSelectedId(node.id);
                        setPanel("properties");
                      }}
                    >
                      {node.type === "text" ? (
                        <Type size={16} />
                      ) : node.type === "image" ? (
                        <ImagePlus size={16} />
                      ) : (
                        <Square size={16} />
                      )}
                      <span>{node.name}</span>
                    </button>
                    <IconButton
                      label={node.hidden ? "显示图层" : "隐藏图层"}
                      onClick={() =>
                        edit((d) => {
                          const n = d.scenes[current.index].nodes.find(
                            (n) => n.id === node.id,
                          )!;
                          n.hidden = !n.hidden;
                        })
                      }
                    >
                      {node.hidden ? <EyeOff size={14} /> : <Eye size={14} />}
                    </IconButton>
                    <IconButton
                      label={node.locked ? "解锁图层" : "锁定图层"}
                      onClick={() =>
                        edit((d) => {
                          const n = d.scenes[current.index].nodes.find(
                            (n) => n.id === node.id,
                          )!;
                          n.locked = !n.locked;
                        })
                      }
                    >
                      {node.locked ? <Lock size={14} /> : <Unlock size={14} />}
                    </IconButton>
                  </div>
                ))}
              </div>
            )}
            {tab === "projects" && (
              <>
                <button
                  className="button primary full-width"
                  onClick={() => setModal("new")}
                >
                  <Plus size={16} />
                  新建工程
                </button>
                <button
                  className="button secondary full-width"
                  onClick={() => importRef.current?.click()}
                >
                  <Upload size={16} />
                  导入工程
                </button>
                <div className="project-list">
                  {projects.map((item) => (
                    <div key={item.id} className="project-list-row">
                      <button onClick={() => openProject(item)}>
                        <Film size={18} />
                        <span>
                          <strong>{item.name}</strong>
                          <small>
                            {item.scenes.length} 个镜头 ·{" "}
                            {seconds(totalFrames(item), item.fps)} 秒
                          </small>
                        </span>
                      </button>
                      <IconButton
                        label="删除工程"
                        disabled={item.id === project.id}
                        onClick={async () => {
                          if (window.confirm(`删除「${item.name}」？`)) {
                            await api(`/api/projects/${item.id}`, {
                              method: "DELETE",
                            });
                            setProjects((p) =>
                              p.filter((x) => x.id !== item.id),
                            );
                          }
                        }}
                      >
                        <Trash2 size={14} />
                      </IconButton>
                    </div>
                  ))}
                </div>
                <button
                  className="button secondary full-width"
                  onClick={() => downloadJSON(project, `${project.name}.json`)}
                >
                  <Save size={15} />
                  下载当前工程
                </button>
              </>
            )}
          </div>
          <div className="library-footer">
            <span className="status-light" />
            本地工作区<span>v0.1</span>
          </div>
        </aside>
        <main className="workspace" id="workspace">
          <div className="canvas-toolbar">
            <div className="toolbar-group">
              <IconButton label="选择对象" active>
                <MousePointer2 size={17} />
              </IconButton>
              <span className="toolbar-label">
                镜头 {String(current.index + 1).padStart(2, "0")}
              </span>
              <span className="toolbar-scene-name">{current.scene.name}</span>
            </div>
            <div className="toolbar-group">
              <span className="canvas-ratio">
                {project.width === project.height
                  ? "1:1"
                  : project.width > project.height
                    ? "16:9"
                    : "9:16"}
              </span>
              <span className="zoom-label">{Math.round(scale * 100)}%</span>
              <IconButton
                label="适应画布"
                onClick={() => {
                  stageRef.current
                    ?.requestFullscreen()
                    .catch(() => notify("当前浏览器无法进入全屏"));
                }}
              >
                <Maximize size={16} />
              </IconButton>
            </div>
          </div>
          <div className="stage-area" ref={stageRef}>
            <div
              className="canvas-wrapper"
              style={{
                width: project.width * scale,
                height: project.height * scale,
              }}
            >
              <div
                className="canvas-scaled"
                style={{
                  transform: `scale(${scale})`,
                  width: project.width,
                  height: project.height,
                }}
              >
                <SceneView
                  project={project}
                  scene={current.scene}
                  frame={current.localFrame}
                  selectedId={selectedId}
                  editable={!observerMode && !busy}
                  onSelect={(id) => {
                    setSelectedAudioId(undefined);
                    setSelectedId(id);
                    setPanel("properties");
                    setScope("node");
                  }}
                  onDrag={dragNode}
                  onResize={resizeNode}
                />
              </div>
            </div>
            <div className="canvas-bottom-caption">
              <span>{current.scene.notes || current.scene.name}</span>
            </div>
          </div>
          <div className="playback-toolbar">
            <div className="toolbar-group">
              <IconButton
                label="上一镜头"
                onClick={() => goScene(Math.max(0, current.index - 1))}
              >
                <ChevronLeft size={17} />
              </IconButton>
              <button
                className="play-button"
                aria-label={playing ? "暂停" : "播放"}
                onClick={() => {
                  if (frame >= frameCount - 1) setFrame(0);
                  setPlaying((v) => !v);
                }}
              >
                {playing ? (
                  <Pause size={17} fill="currentColor" />
                ) : (
                  <Play size={17} fill="currentColor" />
                )}
              </button>
              <IconButton
                label="下一镜头"
                onClick={() =>
                  goScene(
                    Math.min(project.scenes.length - 1, current.index + 1),
                  )
                }
              >
                <ChevronRight size={17} />
              </IconButton>
              <span className="timecode">
                {clock(frame, project.fps)}
                <span>/ {clock(frameCount, project.fps)}</span>
              </span>
            </div>
            <div className="toolbar-group">
              <span className="preview-quality">{project.fps} 帧 / 秒</span>
              <IconButton
                label="音频素材"
                onClick={() => {
                  setTab("assets");
                  uploadRef.current?.click();
                }}
              >
                <AudioLines size={17} />
              </IconButton>
            </div>
          </div>
          <Timeline
            project={project}
            frame={frame}
            sceneIndex={current.index}
            selectedId={selectedId}
            onFrame={(value) => {
              setPlaying(false);
              setFrame(value);
            }}
            onScene={goScene}
            onSelect={(id) => {
              setSelectedId(id);
              setSelectedAudioId(undefined);
              setPanel("properties");
              setScope("node");
            }}
            onAudio={(id) => {
              setSelectedAudioId(id);
              setSelectedId(undefined);
              setPanel("properties");
            }}
            onUpload={() => uploadRef.current?.click()}
            onSpeech={() => setModal("speech")}
            readonly={observerMode || busy}
            onChange={edit}
            onBegin={recordHistory}
          />
        </main>
        <aside className="inspector">
          <div className="inspector-tabs" role="tablist">
            <button
              role="tab"
              aria-selected={panel === "agent"}
              className={panel === "agent" ? "active" : ""}
              onClick={() => setPanel("agent")}
            >
              <Sparkles size={15} />
              智能助手
            </button>
            <button
              role="tab"
              aria-selected={panel === "properties"}
              className={panel === "properties" ? "active" : ""}
              onClick={() => setPanel("properties")}
            >
              <SlidersHorizontal size={15} />
              属性
            </button>
          </div>
          <div className="studio-container" style={{ display: panel === "agent" ? "flex" : "none", flex: 1, minHeight: 0 }}>
            <StudioPanel projectId={project.id} loaded={loaded} onEvent={receiveStudioEvent} onBusy={setBusy} beforeStart={beforeAgentStart} onSettings={() => setModal("settings")} />
          </div>
          {panel === "properties" && (
            <div className={`properties-panel ${observerMode || busy ? "readonly" : ""}`}>
              {selectedAudio ? (
                <div className="audio-properties">
                  <div className="property-object-title">
                    <span>
                      <AudioLines size={17} />
                      <input
                        aria-label="音频名称"
                        value={selectedAudio.name}
                        onChange={(e) => updateAudio({ name: e.target.value })}
                      />
                    </span>
                  </div>
                  <div className="property-section">
                    <h3>音频预览</h3>
                    <audio controls src={selectedAudio.src} />
                  </div>
                  <div className="property-section">
                    <h3>时间与裁剪</h3>
                    <div className="fields-row">
                      <Field
                        label="位置 / 秒"
                        value={selectedAudio.start / project.fps}
                        min={0}
                        max={(frameCount - 1) / project.fps}
                        step={0.1}
                        onChange={(start) =>
                          updateAudio({
                            start: Math.round(start * project.fps),
                          })
                        }
                      />
                      <Field
                        label="时长 / 秒"
                        value={selectedAudio.duration / project.fps}
                        min={1 / project.fps}
                        max={(frameCount - selectedAudio.start) / project.fps}
                        step={0.1}
                        onChange={(duration) =>
                          updateAudio({
                            duration: Math.round(duration * project.fps),
                          })
                        }
                      />
                    </div>
                    <Field
                      label="源文件起点 / 秒"
                      value={selectedAudio.trimStart / project.fps}
                      min={0}
                      max={
                        ((selectedAudio.sourceDuration ||
                          selectedAudio.trimStart + selectedAudio.duration) -
                          1) /
                        project.fps
                      }
                      step={0.1}
                      onChange={(trimStart) =>
                        updateAudio({
                          trimStart: Math.round(trimStart * project.fps),
                        })
                      }
                    />
                  </div>
                  <div className="property-section">
                    <h3>音量 · {Math.round(selectedAudio.volume * 100)}%</h3>
                    <input
                      type="range"
                      aria-label="音频音量"
                      min={0}
                      max={1}
                      step={0.01}
                      value={selectedAudio.volume}
                      onChange={(e) =>
                        updateAudio({ volume: Number(e.target.value) })
                      }
                    />
                    <div className="fields-row">
                      <Field
                        label="淡入 / 秒"
                        value={(selectedAudio.fadeIn || 0) / project.fps}
                        min={0}
                        max={selectedAudio.duration / project.fps}
                        step={0.1}
                        onChange={(fadeIn) =>
                          updateAudio({
                            fadeIn: Math.round(fadeIn * project.fps),
                          })
                        }
                      />
                      <Field
                        label="淡出 / 秒"
                        value={(selectedAudio.fadeOut || 0) / project.fps}
                        min={0}
                        max={selectedAudio.duration / project.fps}
                        step={0.1}
                        onChange={(fadeOut) =>
                          updateAudio({
                            fadeOut: Math.round(fadeOut * project.fps),
                          })
                        }
                      />
                    </div>
                  </div>
                  <div className="object-actions">
                    <button
                      className="button danger"
                      onClick={() => {
                        edit((d) => {
                          d.audio = d.audio.filter(
                            (track) => track.id !== selectedAudio.id,
                          );
                        });
                        setSelectedAudioId(undefined);
                      }}
                    >
                      <Trash2 size={14} />
                      删除音频
                    </button>
                  </div>
                </div>
              ) : selected ? (
                <>
                  <div className="property-object-title">
                    <span>
                      {selected.type === "text" ? (
                        <Type size={17} />
                      ) : (
                        <Layers size={17} />
                      )}
                      <input
                        aria-label="图层名称"
                        value={selected.name}
                        onChange={(e) => updateNode({ name: e.target.value })}
                      />
                    </span>
                    <IconButton
                      label={selected.locked ? "解锁对象" : "锁定对象"}
                      onClick={() => updateNode({ locked: !selected.locked })}
                    >
                      {selected.locked ? (
                        <Lock size={16} />
                      ) : (
                        <Unlock size={16} />
                      )}
                    </IconButton>
                  </div>
                  <fieldset
                    disabled={selected.locked}
                    className="property-fieldset"
                  >
                    {selected.type === "text" && (
                      <>
                        <div className="property-section">
                          <h3>文字内容</h3>
                          <textarea
                            aria-label="文字内容"
                            rows={4}
                            value={selected.text || ""}
                            onChange={(e) =>
                              updateNode({ text: e.target.value })
                            }
                          />
                          <div className="fields-row">
                            <Field
                              label="字号"
                              value={selected.fontSize || 32}
                              min={8}
                              max={300}
                              onChange={(fontSize) => updateNode({ fontSize })}
                            />
                            <label className="number-field">
                              <span>字重</span>
                              <select
                                aria-label="字重"
                                value={selected.fontWeight || 400}
                                onChange={(e) =>
                                  updateNode({
                                    fontWeight: Number(e.target.value),
                                  })
                                }
                              >
                                <option value={400}>常规</option>
                                <option value={500}>中等</option>
                                <option value={600}>半粗</option>
                                <option value={700}>粗体</option>
                              </select>
                            </label>
                          </div>
                        </div>
                      </>
                    )}
                    <div className="property-section">
                      <h3>布局</h3>
                      <div className="fields-row">
                        <Field
                          label="X"
                          value={selected.x}
                          onChange={(x) => updateNode({ x })}
                        />
                        <Field
                          label="Y"
                          value={selected.y}
                          onChange={(y) => updateNode({ y })}
                        />
                      </div>
                      <div className="fields-row">
                        <Field
                          label="宽"
                          value={selected.width}
                          min={10}
                          max={project.width * 2}
                          onChange={(width) => updateNode({ width })}
                        />
                        <Field
                          label="高"
                          value={selected.height}
                          min={10}
                          max={project.height * 2}
                          onChange={(height) => updateNode({ height })}
                        />
                      </div>
                      <div className="fields-row">
                        <Field
                          label="旋转"
                          value={selected.rotation}
                          min={-360}
                          max={360}
                          onChange={(rotation) => updateNode({ rotation })}
                        />
                        <Field
                          label="透明度 %"
                          value={selected.opacity * 100}
                          min={0}
                          max={100}
                          onChange={(opacity) =>
                            updateNode({ opacity: opacity / 100 })
                          }
                        />
                      </div>
                    </div>
                    <div className="property-section">
                      <h3>外观</h3>
                      <label className="color-field">
                        <span>颜色</span>
                        <input
                          aria-label="对象颜色"
                          type="color"
                          value={selected.color}
                          onChange={(e) =>
                            updateNode({ color: e.target.value })
                          }
                        />
                        <span className="hex-value">
                          {selected.color.toUpperCase()}
                        </span>
                      </label>
                      {selected.type === "image" && (
                        <label className="select-field">
                          <span>图片适配</span>
                          <select
                            aria-label="图片适配"
                            value={selected.objectFit || "cover"}
                            onChange={(e) =>
                              updateNode({
                                objectFit: e.target.value as
                                  "cover" | "contain",
                              })
                            }
                          >
                            <option value="cover">铺满并裁剪</option>
                            <option value="contain">完整显示</option>
                          </select>
                        </label>
                      )}
                      {selected.type === "image" && (
                        <button
                          className="button secondary full-width"
                          onClick={() => uploadRef.current?.click()}
                        >
                          <ImagePlus size={15} />
                          替换图片
                        </button>
                      )}
                      {selected.type === "chart" && (
                        <label className="field-label">
                          图表数据
                          <input
                            aria-label="图表数据"
                            value={selected.data?.join(", ") || ""}
                            onChange={(e) => {
                              const data = e.target.value
                                .split(",")
                                .map(Number);
                              if (data.every(Number.isFinite))
                                updateNode({ data });
                            }}
                          />
                        </label>
                      )}
                    </div>
                    <div className="property-section">
                      <h3>时间与动画</h3>
                      <div className="fields-row">
                        <Field
                          label="开始 / 秒"
                          value={selected.start / project.fps}
                          min={0}
                          max={(selected.end - 1) / project.fps}
                          step={0.1}
                          onChange={(start) =>
                            updateNode({
                              start: Math.round(start * project.fps),
                            })
                          }
                        />
                        <Field
                          label="结束 / 秒"
                          value={selected.end / project.fps}
                          min={(selected.start + 1) / project.fps}
                          max={current.scene.duration / project.fps}
                          step={0.1}
                          onChange={(end) =>
                            updateNode({ end: Math.round(end * project.fps) })
                          }
                        />
                      </div>
                      <label className="select-field">
                        <span>入场动画</span>
                        <select
                          value={selected.animation}
                          onChange={(e) =>
                            updateNode({
                              animation: e.target
                                .value as SceneNode["animation"],
                            })
                          }
                        >
                          <option value="none">无动画</option>
                          <option value="fade">淡入</option>
                          <option value="slide">向上移入</option>
                          <option value="zoom">缩放进入</option>
                        </select>
                      </label>
                    </div>
                  </fieldset>
                  <div className="object-actions">
                    <button
                      className="button secondary"
                      onClick={() => {
                        const node = {
                          ...clone(selected),
                          id: uid(),
                          name: selected.name + " · 副本",
                          x: selected.x + 20,
                          y: selected.y + 20,
                        };
                        edit((d) => d.scenes[current.index].nodes.push(node));
                        setSelectedId(node.id);
                      }}
                    >
                      <Copy size={14} />
                      复制
                    </button>
                    <button
                      className="button danger"
                      disabled={selected.locked}
                      onClick={() => {
                        edit((d) => {
                          d.scenes[current.index].nodes = d.scenes[
                            current.index
                          ].nodes.filter((n) => n.id !== selected.id);
                        });
                        setSelectedId(undefined);
                      }}
                    >
                      <Trash2 size={14} />
                      删除
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <div className="property-section">
                    <h3>镜头设置</h3>
                    <label className="field-label">
                      镜头名称
                      <input
                        value={current.scene.name}
                        onChange={(e) =>
                          edit((d) => {
                            d.scenes[current.index].name = e.target.value;
                          })
                        }
                      />
                    </label>
                    <Field
                      label="时长 / 秒"
                      value={current.scene.duration / project.fps}
                      min={1}
                      max={120}
                      step={0.1}
                      onChange={(duration) =>
                        edit((d) => {
                          const scene = d.scenes[current.index];
                          resizeScene(
                            scene,
                            Math.round(duration * project.fps),
                            "right",
                          );
                        })
                      }
                    />
                    <label className="color-field">
                      <span>背景颜色</span>
                      <input
                        type="color"
                        value={current.scene.background}
                        onChange={(e) =>
                          edit((d) => {
                            d.scenes[current.index].background = e.target.value;
                          })
                        }
                      />
                      <span className="hex-value">
                        {current.scene.background.toUpperCase()}
                      </span>
                    </label>
                    <label className="field-label">
                      分镜描述
                      <textarea
                        rows={4}
                        value={current.scene.notes}
                        onChange={(e) =>
                          edit((d) => {
                            d.scenes[current.index].notes = e.target.value;
                          })
                        }
                      />
                    </label>
                  </div>
                  <div className="object-empty">
                    <MousePointer2 size={23} />
                    <span>未选中对象</span>
                  </div>
                </>
              )}
            </div>
          )}
        </aside>
      </div>
      <input
        ref={uploadRef}
        type="file"
        accept="image/png,image/jpeg,image/webp,audio/*"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) upload(file);
          e.target.value = "";
        }}
      />
      <input
        ref={importRef}
        type="file"
        accept="application/json,.json,.zip"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) importProject(file);
          e.target.value = "";
        }}
      />
      {toast && (
        <div className="toast" role="status">
          <Check size={16} />
          <span>{toast}</span>
          <IconButton label="关闭提示" onClick={() => setToast("")}>
            <X size={14} />
          </IconButton>
        </div>
      )}
      {modal === "settings" && (
        <SettingsModal
          onClose={() => setModal(null)}
          onSaved={() => notify("模型配置已保存")}
        />
      )}
      {modal === "new" && (
        <NewProjectModal onClose={() => setModal(null)} onCreate={newProject} />
      )}
      {modal === "export" && (
        <ExportModal project={project} onClose={() => setModal(null)} />
      )}
      {modal === "source" && (
        <SourceModal project={project} onClose={() => setModal(null)} />
      )}
      {modal === "speech" && (
        <SpeechModal
          initialText={
            selected?.type === "text"
              ? selected.text || ""
              : current.scene.nodes
                  .filter(
                    (node) =>
                      node.type === "text" && (node.fontSize || 0) >= 24,
                  )
                  .map((node) => node.text || "")
                  .join("\n")
          }
          onClose={() => setModal(null)}
          onAdd={addSpeech}
          onSettings={() => setModal("settings")}
        />
      )}
    </div>
  );
}
