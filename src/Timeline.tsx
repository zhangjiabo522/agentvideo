import { useRef, useState, type PointerEvent } from "react";
import {
  ArrowLeft,
  ArrowRight,
  AudioLines,
  Copy,
  Film,
  Layers,
  Plus,
  Trash2,
  Type,
} from "lucide-react";
import {
  totalFrames,
  uid,
  type Project,
  type SceneNode,
  resizeScene,
} from "./types";

type Props = {
  readonly?: boolean;
  project: Project;
  frame: number;
  sceneIndex: number;
  selectedId?: string;
  onFrame: (frame: number) => void;
  onScene: (index: number) => void;
  onSelect: (id: string) => void;
  onAudio: (id: string) => void;
  onUpload: () => void;
  onSpeech: () => void;
  onChange: (mutator: (draft: Project) => void, history?: boolean) => void;
  onBegin: () => void;
};
type Drag = {
  kind: "scene" | "node" | "audio";
  id: string;
  mode: "move" | "left" | "right";
  startX: number;
  snapshot: Project;
  sceneIndex: number;
  scale: number;
  started: boolean;
};
const clamp = (value: number, min: number, max: number) =>
  Math.min(Math.max(value, min), Math.max(min, max));
const clipTextColor = (background: string, hasImage: boolean) => {
  if (hasImage) return "#FFFFFF";
  let hex = background.replace(/^#/, "");
  if (hex.length === 3) hex = hex.split("").map(value => value + value).join("");
  if (!/^[a-f\d]{6}$/i.test(hex)) return "#FFFFFF";
  const channels = [0, 2, 4].map(offset => {
    const value = parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722 > 0.179 ? "#000000" : "#FFFFFF";
};
export function Timeline({
  readonly = false,
  project,
  frame,
  sceneIndex,
  selectedId,
  onFrame,
  onScene,
  onSelect,
  onAudio,
  onUpload,
  onSpeech,
  onChange,
  onBegin,
}: Props) {
  const trackRef = useRef<HTMLDivElement>(null);
  const drag = useRef<Drag | null>(null);
  const [dragging, setDragging] = useState<string>();
  const [previewIndex, setPreviewIndex] = useState<number>();
  const frames = totalFrames(project);
  const offsets = project.scenes.map((_, index) =>
    project.scenes
      .slice(0, index)
      .reduce((sum, scene) => sum + scene.duration, 0),
  );
  const scene = project.scenes[sceneIndex];
  const visibleNodes = scene.nodes.filter((node) => !node.hidden);
  const nodeRows = selectedId
    ? [
        ...visibleNodes.filter((node) => node.id === selectedId),
        ...visibleNodes.filter((node) => node.id !== selectedId),
      ]
    : visibleNodes;

  const start = (
    event: PointerEvent<HTMLElement>,
    kind: Drag["kind"],
    id: string,
    mode: Drag["mode"],
  ) => {
    if (readonly) {
      if (kind === "scene") onScene(project.scenes.findIndex(item => item.id === id));
      if (kind === "node") onSelect(id);
      if (kind === "audio") onAudio(id);
      return;
    }
    if (event.button !== 0 || !trackRef.current) return;
    const node = scene.nodes.find((n) => n.id === id);
    if (kind === "node" && node?.locked) return;
    event.preventDefault();
    event.stopPropagation();
    drag.current = {
      kind,
      id,
      mode,
      startX: event.clientX,
      snapshot: structuredClone(project),
      sceneIndex,
      scale: trackRef.current.getBoundingClientRect().width / frames,
      started: false,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging(id);
    if (kind === "node") onSelect(id);
    if (kind === "audio") onAudio(id);
  };

  const move = (event: PointerEvent<HTMLElement>) => {
    const state = drag.current;
    if (!state) return;
    event.stopPropagation();
    const delta = Math.round((event.clientX - state.startX) / state.scale);
    if (!state.started) {
      if (Math.abs(event.clientX - state.startX) < 3) return;
      state.started = true;
      onBegin();
    }
    const snapshot = state.snapshot;
    const count = totalFrames(snapshot);
    if (state.kind === "scene") {
      const index = snapshot.scenes.findIndex((s) => s.id === state.id);
      const original = snapshot.scenes[index];
      if (state.mode === "move") {
        const startFrame = snapshot.scenes
          .slice(0, index)
          .reduce((sum, s) => sum + s.duration, 0);
        const target = clamp(
          startFrame + delta + original.duration / 2,
          0,
          count - 1,
        );
        let elapsed = 0;
        let targetIndex = index;
        snapshot.scenes.forEach((s, i) => {
          if (target >= elapsed && target < elapsed + s.duration)
            targetIndex = i;
          elapsed += s.duration;
        });
        setPreviewIndex(targetIndex);
      } else {
        const minimum = Math.max(1, Math.round(snapshot.fps * 0.5));
        const extra = 120 * snapshot.fps - count;
        const duration = clamp(
          original.duration + (state.mode === "right" ? delta : -delta),
          minimum,
          original.duration + extra,
        );
        onChange((draft) => {
          const changed = structuredClone(original);
          resizeScene(changed, duration, state.mode === "left" ? "left" : "right");
          draft.scenes[index] = changed;
          draft.audio = structuredClone(snapshot.audio);
        }, false);
      }
    } else if (state.kind === "node") {
      const original = snapshot.scenes[state.sceneIndex].nodes.find(
        (n) => n.id === state.id,
      )!;
      const duration = snapshot.scenes[state.sceneIndex].duration;
      let startFrame = original.start,
        end = original.end;
      if (state.mode === "move") {
        startFrame = clamp(
          original.start + delta,
          0,
          duration - (original.end - original.start),
        );
        end = startFrame + (original.end - original.start);
      }
      if (state.mode === "left")
        startFrame = clamp(original.start + delta, 0, original.end - 1);
      if (state.mode === "right")
        end = clamp(original.end + delta, original.start + 1, duration);
      onChange((draft) => {
        const node = draft.scenes[state.sceneIndex].nodes.find(
          (n) => n.id === state.id,
        )!;
        node.start = startFrame;
        node.end = end;
        node.protected = [
          ...new Set([...(node.protected || []), "start", "end"]),
        ];
      }, false);
    } else {
      const original = snapshot.audio.find((a) => a.id === state.id)!;
      let { start: position, trimStart, duration } = original;
      const source =
        original.sourceDuration || original.trimStart + original.duration;
      if (state.mode === "move")
        position = clamp(original.start + delta, 0, count - original.duration);
      if (state.mode === "left") {
        const shift = clamp(
          delta,
          -Math.min(original.trimStart, original.start),
          original.duration - 1,
        );
        position += shift;
        trimStart += shift;
        duration -= shift;
      }
      if (state.mode === "right")
        duration = clamp(
          original.duration + delta,
          1,
          Math.min(source - trimStart, count - position),
        );
      onChange((draft) => {
        const track = draft.audio.find((a) => a.id === state.id)!;
        Object.assign(track, {
          start: position,
          trimStart,
          duration,
          fadeIn: Math.min(track.fadeIn || 0, duration),
          fadeOut: Math.min(track.fadeOut || 0, duration),
        });
      }, false);
    }
  };

  const end = (event: PointerEvent<HTMLElement>) => {
    const state = drag.current;
    if (!state) return;
    event.stopPropagation();
    if (
      state.kind === "scene" &&
      state.mode === "move" &&
      state.started &&
      previewIndex !== undefined
    ) {
      const from = state.snapshot.scenes.findIndex((s) => s.id === state.id);
      const to = previewIndex;
      onChange((draft) => {
        const next = [...draft.scenes];
        const [moved] = next.splice(from, 1);
        next.splice(to, 0, moved);
        draft.scenes = next;
      }, false);
      onScene(to);
    } else if (
      !state.started &&
      state.mode === "move" &&
      state.kind === "scene"
    )
      onScene(state.snapshot.scenes.findIndex((s) => s.id === state.id));
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId);
    drag.current = null;
    setDragging(undefined);
    setPreviewIndex(undefined);
  };

  const sceneAction = (action: "left" | "right" | "copy" | "delete") => {
    if (readonly) return;
    onChange((draft) => {
      if (action === "left" && sceneIndex > 0)
        [draft.scenes[sceneIndex - 1], draft.scenes[sceneIndex]] = [
          draft.scenes[sceneIndex],
          draft.scenes[sceneIndex - 1],
        ];
      if (action === "right" && sceneIndex < draft.scenes.length - 1)
        [draft.scenes[sceneIndex + 1], draft.scenes[sceneIndex]] = [
          draft.scenes[sceneIndex],
          draft.scenes[sceneIndex + 1],
        ];
      if (action === "copy" && draft.scenes.length < 20) {
        const copy = structuredClone(scene);
        copy.id = uid();
        copy.name += " · 副本";
        copy.nodes.forEach((n) => (n.id = uid()));
        draft.scenes.splice(sceneIndex + 1, 0, copy);
      }
      if (action === "delete" && draft.scenes.length > 1)
        draft.scenes.splice(sceneIndex, 1);
    });
  };

  const keyTrim = (
    event: React.KeyboardEvent<HTMLElement>,
    kind: Drag["kind"],
    id: string,
    mode: "left" | "right",
  ) => {
    if (readonly) return;
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    event.stopPropagation();
    const delta =
      (event.key === "ArrowRight" ? 1 : -1) *
      (event.shiftKey ? project.fps : 1);
    onChange((draft) => {
      if (kind === "scene") {
        const target = draft.scenes.find((s) => s.id === id)!;
        const duration = clamp(
          target.duration + (mode === "right" ? delta : -delta),
          Math.max(1, Math.round(draft.fps * 0.5)),
          target.duration + 120 * draft.fps - totalFrames(draft),
        );
        resizeScene(target, duration, mode);
      } else if (kind === "node") {
        const target = draft.scenes[sceneIndex].nodes.find((n) => n.id === id)!;
        if (mode === "left")
          target.start = clamp(target.start + delta, 0, target.end - 1);
        else
          target.end = clamp(
            target.end + delta,
            target.start + 1,
            scene.duration,
          );
        target.protected = [
          ...new Set([...(target.protected || []), "start", "end"]),
        ];
      } else {
        const target = draft.audio.find((a) => a.id === id)!;
        if (mode === "left") {
          const change = clamp(
            delta,
            -Math.min(target.start, target.trimStart),
            target.duration - 1,
          );
          target.start += change;
          target.trimStart += change;
          target.duration -= change;
        } else
          target.duration = clamp(
            target.duration + delta,
            1,
            Math.min(
              totalFrames(draft) - target.start,
              (target.sourceDuration || target.trimStart + target.duration) -
                target.trimStart,
            ),
          );
        target.fadeIn = Math.min(target.fadeIn || 0, target.duration);
        target.fadeOut = Math.min(target.fadeOut || 0, target.duration);
      }
    });
  };

  const handles = (kind: Drag["kind"], id: string, label: string) => (
    <>
      <span
        className="clip-handle left"
        role="button"
        aria-label={`裁剪${label}开始时间`}
        title="拖动调整开始时间"
        tabIndex={0}
        onPointerDown={(e) => start(e, kind, id, "left")}
        onPointerMove={move}
        onPointerUp={end}
        onPointerCancel={end}
        onKeyDown={(e) => keyTrim(e, kind, id, "left")}
      />
      <span
        className="clip-handle right"
        role="button"
        aria-label={`裁剪${label}结束时间`}
        title="拖动调整结束时间"
        tabIndex={0}
        onPointerDown={(e) => start(e, kind, id, "right")}
        onPointerMove={move}
        onPointerUp={end}
        onPointerCancel={end}
        onKeyDown={(e) => keyTrim(e, kind, id, "right")}
      />
    </>
  );
  return (
    <section className="timeline editable-timeline" aria-label="视频时间线">
      <div className="timeline-header">
        <div className="toolbar-group">
          <Layers size={15} />
          <strong>时间线</strong>
          <span className="timeline-count">{project.scenes.length} 个镜头</span>
        </div>
        <div className="toolbar-group">
          <button
            className="icon-button"
            title="镜头前移"
            aria-label="镜头前移"
            disabled={readonly || sceneIndex === 0}
            onClick={() => sceneAction("left")}
          >
            <ArrowLeft size={15} />
          </button>
          <button
            className="icon-button"
            title="镜头后移"
            aria-label="镜头后移"
            disabled={readonly || sceneIndex === project.scenes.length - 1}
            onClick={() => sceneAction("right")}
          >
            <ArrowRight size={15} />
          </button>
          <button
            className="icon-button"
            title="复制镜头"
            aria-label="复制镜头"
            disabled={readonly}
            onClick={() => sceneAction("copy")}
          >
            <Copy size={15} />
          </button>
          <button
            className="icon-button"
            title="删除镜头"
            aria-label="删除镜头"
            disabled={readonly || project.scenes.length === 1}
            onClick={() => sceneAction("delete")}
          >
            <Trash2 size={15} />
          </button>
        </div>
      </div>
      <div className="timeline-ruler">
        <span className="track-label" />
        <div className="ruler-numbers">
          {Array.from({ length: 7 }, (_, i) => (
            <span key={i}>{((frames * i) / 6 / project.fps).toFixed(1)}s</span>
          ))}
        </div>
      </div>
      <div className="timeline-scroll">
        <div className="timeline-tracks">
          <div className="track-row">
            <span className="track-label">
              <Film size={13} />
              画面
            </span>
            <div className="absolute-track" ref={trackRef}>
              {project.scenes.map((s, index) => (
                <div
                  key={s.id}
                  className={`scene-clip draggable-clip ${sceneIndex === index ? "active" : ""} ${dragging === s.id ? "dragging" : ""} ${previewIndex === index ? "drop-target" : ""}`}
                  role="button"
                  tabIndex={0}
                  aria-label={`镜头片段 ${s.name}`}
                  style={{
                    left: `${(offsets[index] / frames) * 100}%`,
                    width: `${(s.duration / frames) * 100}%`,
                    backgroundColor: s.background,
                    color: clipTextColor(s.background, s.nodes.some(n => n.type === "image" && !!n.src)),
                    backgroundImage: s.nodes.find((n) => n.type === "image")
                      ?.src
                      ? `linear-gradient(90deg,rgba(14,24,20,.65),rgba(14,24,20,.65)),url(${s.nodes.find((n) => n.type === "image")!.src})`
                      : undefined,
                  }}
                  onPointerDown={(e) => start(e, "scene", s.id, "move")}
                  onPointerMove={move}
                  onPointerUp={end}
                  onPointerCancel={end}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onScene(index);
                    }
                  }}
                >
                  <span>{String(index + 1).padStart(2, "0")}</span>
                  <strong>{s.name}</strong>
                  <small>{(s.duration / project.fps).toFixed(1)}s</small>
                  {handles("scene", s.id, s.name)}
                </div>
              ))}
            </div>
          </div>
          {nodeRows.map((node) => (
            <div className="track-row object-track" key={node.id}>
              <span className="track-label" title={node.name}>
                {node.type === "text" ? (
                  <Type size={12} />
                ) : (
                  <Layers size={12} />
                )}
                <span>{node.name}</span>
              </span>
              <div className="absolute-track">
                <div
                  className={`node-time-clip draggable-clip ${node.id === selectedId ? "active" : ""} ${node.locked ? "locked" : ""}`}
                  role="button"
                  tabIndex={0}
                  aria-label={`图层片段 ${node.name}`}
                  style={{
                    left: `${((offsets[sceneIndex] + node.start) / frames) * 100}%`,
                    width: `${((node.end - node.start) / frames) * 100}%`,
                  }}
                  onPointerDown={(e) => start(e, "node", node.id, "move")}
                  onPointerMove={move}
                  onPointerUp={end}
                  onPointerCancel={end}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      onSelect(node.id);
                    }
                  }}
                >
                  <Type size={11} />
                  <span>
                    {node.type === "text"
                      ? node.text?.replaceAll("\n", " ")
                      : node.name}
                  </span>
                  {!node.locked && handles("node", node.id, node.name)}
                </div>
              </div>
            </div>
          ))}
          {project.audio.map((track) => (
            <div className="track-row" key={track.id}>
              <span className="track-label">
                <AudioLines size={13} />
                音频
              </span>
              <div className="absolute-track">
                <div
                  className="audio-time-clip draggable-clip"
                  role="button"
                  tabIndex={0}
                  aria-label={`音频片段 ${track.name}`}
                  style={{
                    left: `${(track.start / frames) * 100}%`,
                    width: `${(track.duration / frames) * 100}%`,
                  }}
                  onPointerDown={(e) => start(e, "audio", track.id, "move")}
                  onPointerMove={move}
                  onPointerUp={end}
                  onPointerCancel={end}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") onAudio(track.id);
                  }}
                >
                  <AudioLines size={12} />
                  <span>{track.name}</span>
                  {handles("audio", track.id, track.name)}
                </div>
              </div>
            </div>
          ))}
          {!project.audio.length && (
            <div className="track-row">
              <span className="track-label">
                <AudioLines size={13} />
                音频
              </span>
              <div className="audio-track-area">
                <button className="empty-audio" onClick={onUpload} disabled={readonly}>
                  <Plus size={12} />
                  上传音频
                </button>
                <button className="empty-audio" onClick={onSpeech} disabled={readonly}>
                  <AudioLines size={12} />
                  生成旁白
                </button>
              </div>
            </div>
          )}
          <div className="playhead-area">
            <div
              className="playhead"
              style={{ left: `${Math.min(100, (frame / frames) * 100)}%` }}
            >
              <span />
            </div>
          </div>
        </div>
      </div>
      <input
        className="timeline-scrubber"
        type="range"
        aria-label="播放位置"
        min={0}
        max={frames - 1}
        value={frame}
        onChange={(e) => onFrame(Number(e.target.value))}
      />
    </section>
  );
}
