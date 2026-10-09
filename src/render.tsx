import {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {SceneView} from './SceneView';
import {locateFrame, totalFrames, type Project} from './types';

declare global {
  interface Window {
    __setProject: (project: Project) => Promise<void>;
    __renderFrame: (frame: number) => Promise<void>;
    __rendererReady: boolean;
  }
}

async function waitForAssets() {
  await document.fonts.ready;
  await Promise.all(Array.from(document.images).map(async image => {
    if (!image.complete) await new Promise<void>((resolve, reject) => {
      image.addEventListener('load', () => resolve(), {once: true});
      image.addEventListener('error', () => reject(new Error(`图片无法加载：${image.getAttribute('src')}`)), {once: true});
    });
    if (!image.naturalWidth) throw new Error(`图片无法加载：${image.getAttribute('src')}`);
    await image.decode();
  }));
  await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
}

function Renderer() {
  const [project, setProject] = useState<Project | null>(null);
  const [frame, setFrame] = useState(0);
  window.__setProject = async value => {
    flushSync(() => {setProject(value); setFrame(0);});
    await waitForAssets();
  };
  window.__renderFrame = async value => {
    if (!project || value < 0 || value >= totalFrames(project)) throw new Error('帧编号超出工程范围');
    flushSync(() => setFrame(value));
    await waitForAssets();
  };
  window.__rendererReady = true;
  if (!project || !project.scenes.length) return null;
  const current = locateFrame(project, frame);
  return <SceneView project={project} scene={current.scene} frame={current.localFrame} />;
}

createRoot(document.getElementById('root')!).render(<Renderer />);
