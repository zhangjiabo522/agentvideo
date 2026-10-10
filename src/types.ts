export type NodeType = "text" | "image" | "rect" | "circle" | "chart";
export type Animation = "none" | "fade" | "slide" | "zoom";
export type SceneNode = {
  id: string;
  type: NodeType;
  name: string;
  x: number;
  y: number;
  width: number;
  height: number;
  rotation: number;
  opacity: number;
  color: string;
  text?: string;
  fontSize?: number;
  fontWeight?: number;
  src?: string;
  objectFit?: "cover" | "contain";
  data?: number[];
  labels?: string[];
  start: number;
  end: number;
  animation: Animation;
  locked: boolean;
  hidden: boolean;
  protected?: string[];
};
export type Scene = {
  id: string;
  name: string;
  duration: number;
  background: string;
  notes: string;
  nodes: SceneNode[];
};
export type AudioTrack = {
  id: string;
  name: string;
  src: string;
  start: number;
  trimStart: number;
  duration: number;
  volume: number;
  sourceDuration?: number;
  fadeIn?: number;
  fadeOut?: number;
};
export type Project = {
  id: string;
  name: string;
  width: number;
  height: number;
  fps: number;
  revision: number;
  updatedAt: string;
  scenes: Scene[];
  audio: AudioTrack[];
};
export type Settings = {
  baseUrl: string;
  model: string;
  apiKey?: string;
  hasApiKey?: boolean;
  imageBaseUrl: string;
  imageModel: string;
  imageApiKey?: string;
  hasImageApiKey?: boolean;
  ttsProvider?: "local" | "mimo" | "openai";
  ttsBaseUrl?: string;
  ttsModel?: string;
  ttsApiKey?: string;
  hasTtsApiKey?: boolean;
  ttsVoice?: string;
  ttsSpeed?: number;
};
export type ExportJob = {
  id: string;
  projectId?: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled";
  progress: number;
  message: string;
  url?: string;
  stage?: string;
  renderedFrames?: number;
  totalFrames?: number;
  elapsedSeconds?: number;
  estimatedRemainingSeconds?: number;
  queuePosition?: number;
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
  updatedAt?: string;
};
export const uid = () => crypto.randomUUID();
export const totalFrames = (project: Project) =>
  project.scenes.reduce((sum, scene) => sum + scene.duration, 0);
export function resizeScene(
  scene: Scene,
  duration: number,
  edge: "left" | "right",
) {
  const previous = scene.duration;
  const delta = duration - previous;
  const shift = edge === "left" ? delta : 0;
  scene.duration = duration;
  scene.nodes = scene.nodes
    .filter((node) => node.end + shift > 0 && node.start + shift < duration)
    .map((node) => {
      const end =
        edge === "right" && delta > 0 && node.end === previous
          ? duration
          : node.end + shift;
      return {
        ...node,
        start: Math.min(Math.max(node.start + shift, 0), duration - 1),
        end: Math.min(Math.max(end, 1), duration),
      };
    })
    .filter((node) => node.end > node.start);
}
export function fitAudioToProject(project: Project) {
  const total = totalFrames(project);
  let changed = false;
  project.audio = project.audio
    .filter((track) => {
      if (track.start < total) return true;
      changed = true;
      return false;
    })
    .map((track) => {
      const duration = Math.min(track.duration, total - track.start);
      const fadeIn = Math.min(track.fadeIn || 0, duration);
      const fadeOut = Math.min(track.fadeOut || 0, duration);
      if (
        duration !== track.duration ||
        fadeIn !== (track.fadeIn || 0) ||
        fadeOut !== (track.fadeOut || 0)
      ) changed = true;
      return { ...track, duration, fadeIn, fadeOut };
    });
  return changed;
}
export const locateFrame = (project: Project, frame: number) => {
  let offset = 0;
  for (let index = 0; index < project.scenes.length; index++) {
    const scene = project.scenes[index];
    if (frame < offset + scene.duration || index === project.scenes.length - 1)
      return { scene, index, localFrame: Math.max(0, frame - offset), offset };
    offset += scene.duration;
  }
  return { scene: project.scenes[0], index: 0, localFrame: 0, offset: 0 };
};
