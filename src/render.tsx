import {useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {SceneView} from './SceneView';
import {locateFrame, totalFrames, type Project} from './types';

declare global {
  interface Window {
    __setProject: (project: Project) => Promise<void>;
    __renderFrame: (frame: number) => Promise<boolean>;
    __rendererReady: boolean;
  }
}

const decodedImages = new WeakMap<HTMLImageElement, string>();

async function waitForAssets() {
  await document.fonts.ready;
  await Promise.all(Array.from(document.images).map(async image => {
    if (decodedImages.get(image) === image.src) return;
    if (!image.complete) await new Promise<void>((resolve, reject) => {
      const finish = (error?: Error) => {
        clearTimeout(timer);
        image.removeEventListener('load', loaded);
        image.removeEventListener('error', failed);
        if (error) reject(error); else resolve();
      };
      const loaded = () => finish();
      const failed = () => finish(new Error(`图片无法加载：${image.getAttribute('src')}`));
      const timer = setTimeout(() => finish(new Error(`图片加载超时：${image.getAttribute('src')}`)), 15000);
      image.addEventListener('load', loaded, {once: true});
      image.addEventListener('error', failed, {once: true});
    });
    if (!image.naturalWidth) throw new Error(`图片无法加载：${image.getAttribute('src')}`);
    await image.decode();
    decodedImages.set(image, image.src);
  }));
  await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
}

function Renderer() {
  const [project, setProject] = useState<Project | null>(null);
  const [frame, setFrame] = useState(0);
  const renderKey = useRef('');
  window.__setProject = async value => {
    renderKey.current = '';
    flushSync(() => {setProject(value); setFrame(0);});
    await waitForAssets();
  };
  window.__renderFrame = async value => {
    if (!project || value < 0 || value >= totalFrames(project)) throw new Error('帧编号超出工程范围');
    flushSync(() => setFrame(value));
    const nextKey = document.querySelector('.scene-canvas')?.outerHTML || '';
    if (nextKey === renderKey.current) return false;
    await waitForAssets();
    renderKey.current = nextKey;
    return true;
  };
  window.__rendererReady = true;
  if (!project || !project.scenes.length) return null;
  const current = locateFrame(project, frame);
  return <SceneView project={project} scene={current.scene} frame={current.localFrame} />;
}

createRoot(document.getElementById('root')!).render(<Renderer />);
