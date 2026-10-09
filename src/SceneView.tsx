import {useRef, type CSSProperties, type PointerEvent} from 'react';
import type {Project, Scene, SceneNode} from './types';
import './render.css';

type SceneViewProps = {
  project: Project;
  scene: Scene;
  frame: number;
  selectedId?: string;
  onSelect?: (id: string) => void;
  onDrag?: (id: string, x: number, y: number) => void;
  onResize?: (id: string, width: number, height: number) => void;
  editable?: boolean;
};

const easeOut = (value: number) => 1 - Math.pow(1 - value, 3);

function nodeStyle(node: SceneNode, frame: number, fps: number): CSSProperties {
  const entrance = easeOut(Math.min(1, Math.max(0, (frame - node.start) / Math.max(1, Math.min(fps * 0.5, (node.end - node.start) / 3)))));
  const exit = Math.min(1, Math.max(0, (node.end - frame) / Math.max(1, Math.min(fps * 0.25, (node.end - node.start) / 4))));
  const animation = node.animation || 'none';
  const opacity = animation === 'none' ? node.opacity : node.opacity * entrance * exit;
  const displacement = animation === 'slide' ? (1 - entrance) * 48 : 0;
  const scale = animation === 'zoom' ? 0.82 + entrance * 0.18 : 1;
  return {
    position: 'absolute',
    left: node.x,
    top: node.y,
    width: node.width,
    height: node.height,
    opacity,
    transform: `translateY(${displacement}px) rotate(${node.rotation}deg) scale(${scale})`,
    transformOrigin: 'center',
    color: node.color,
  };
}

function NodeContent({node}: {node: SceneNode}) {
  if (node.type === 'text') return <div className="scene-text" style={{fontSize: node.fontSize || 48, fontWeight: node.fontWeight || 500}}>{node.text || ''}</div>;
  if (node.type === 'image') return node.src ? <img className="scene-image" src={node.src} alt={node.name} draggable={false} style={{objectFit: node.objectFit || 'cover'}} /> : <div className="scene-image-empty">图片</div>;
  if (node.type === 'circle') return <div className="scene-shape" style={{background: node.color, borderRadius: '50%'}} />;
  if (node.type === 'chart') {
    const values = node.data || [30, 60, 45, 90];
    const maximum = Math.max(1, ...values.map(value => Math.abs(value)));
    return <div className="scene-chart">
      <div className="scene-chart-bars">{values.map((value, index) => <div className="scene-chart-column" key={index}>
        <span className="scene-chart-value" style={{fontSize: Math.max(12, Math.min(node.width / Math.max(1, values.length) / 4, 24))}}>{value}</span>
        <div className="scene-chart-bar" style={{height: `${Math.max(2, Math.abs(value) / maximum * 80)}%`, background: node.color}} />
      </div>)}</div>
      <div className="scene-chart-labels">{values.map((_, index) => <span key={index} style={{fontSize: Math.max(12, Math.min(node.width / Math.max(1, values.length) / 5, 20))}}>{node.labels?.[index] || `${index + 1}`}</span>)}</div>
    </div>;
  }
  return <div className="scene-shape" style={{background: node.color}} />;
}

export function SceneView({project, scene, frame, selectedId, onSelect, onDrag, onResize, editable = false}: SceneViewProps) {
  const canvas = useRef<HTMLDivElement>(null);
  const drag = useRef<{id: string; pointerId: number; x: number; y: number; width: number; height: number; rotation: number; resizing: boolean; startX: number; startY: number; scaleX: number; scaleY: number} | null>(null);

  const pointerDown = (event: PointerEvent<HTMLElement>, node: SceneNode, resizing = false) => {
    if (!editable || event.button !== 0) return;
    event.stopPropagation();
    onSelect?.(node.id);
    if (node.locked || (resizing ? !onResize : !onDrag) || !canvas.current) return;
    const rect = canvas.current.getBoundingClientRect();
    drag.current = {id: node.id, pointerId: event.pointerId, x: node.x, y: node.y, width: node.width, height: node.height, rotation: node.rotation, resizing, startX: event.clientX, startY: event.clientY, scaleX: rect.width / project.width, scaleY: rect.height / project.height};
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const pointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    const dx = (event.clientX - current.startX) / current.scaleX;
    const dy = (event.clientY - current.startY) / current.scaleY;
    if (current.resizing) {
      const radians = current.rotation * Math.PI / 180;
      const width = Math.max(4, Math.min(8192, current.width + dx * Math.cos(radians) + dy * Math.sin(radians)));
      const height = event.shiftKey ? Math.max(4, Math.min(8192, width * current.height / current.width)) : Math.max(4, Math.min(8192, current.height - dx * Math.sin(radians) + dy * Math.cos(radians)));
      onResize?.(current.id, Math.round(width), Math.round(height));
    } else onDrag?.(current.id, Math.round(current.x + dx), Math.round(current.y + dy));
  };

  const pointerEnd = (event: PointerEvent<HTMLDivElement>) => {
    if (drag.current?.pointerId === event.pointerId) drag.current = null;
  };

  return <div ref={canvas} className={`scene-canvas${editable ? ' is-editable' : ''}`} style={{width: project.width, height: project.height, background: scene.background}} role="img" aria-label={scene.name}>
    {scene.nodes.filter(node => !node.hidden && frame >= node.start && frame < node.end).map(node => <div
      key={node.id}
      className={`scene-node${editable && selectedId === node.id ? ' is-selected' : ''}${node.locked ? ' is-locked' : ''}`}
      style={nodeStyle(node, frame, project.fps)}
      data-node-id={node.id}
      tabIndex={editable ? 0 : undefined}
      role={editable ? 'group' : undefined}
      aria-label={editable ? `${node.name}${selectedId === node.id ? '，已选中' : ''}${node.locked ? '，已锁定' : ''}` : undefined}
      onPointerDown={event => pointerDown(event, node)}
      onPointerMove={pointerMove}
      onPointerUp={pointerEnd}
      onPointerCancel={pointerEnd}
      onKeyDown={event => {
        if (!editable) return;
        if (event.key === 'Enter' || event.key === ' ') {event.preventDefault(); onSelect?.(node.id);}
        if (!node.locked && ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
          event.preventDefault();
          const step = event.shiftKey ? 10 : 1;
          onSelect?.(node.id);
          onDrag?.(node.id, node.x + (event.key === 'ArrowRight' ? step : event.key === 'ArrowLeft' ? -step : 0), node.y + (event.key === 'ArrowDown' ? step : event.key === 'ArrowUp' ? -step : 0));
        }
      }}
    ><NodeContent node={node} />{editable && selectedId === node.id && !node.locked && onResize && <button type="button" className="scene-resize-handle" aria-label={`调整${node.name}尺寸`} title="调整尺寸" onPointerDown={event => pointerDown(event, node, true)} onKeyDown={event => {
      event.stopPropagation();
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
        event.preventDefault();
        const step = event.shiftKey ? 10 : 1;
        onResize(node.id, Math.max(4, Math.min(8192, node.width + (event.key === 'ArrowRight' ? step : event.key === 'ArrowLeft' ? -step : 0))), Math.max(4, Math.min(8192, node.height + (event.key === 'ArrowDown' ? step : event.key === 'ArrowUp' ? -step : 0))));
      }
    }} />}</div>)}
  </div>;
}
