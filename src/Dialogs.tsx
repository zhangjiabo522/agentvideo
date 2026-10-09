import {useEffect, useId, useRef, useState, type ReactNode} from 'react';
import {CheckCircle2, Code2, Download, Film, Image, LoaderCircle, Mic, Monitor, Package, Plus, RefreshCw, Save, Settings2, Smartphone, Square, Unplug, X} from 'lucide-react';
import {totalFrames, type ExportJob, type Project, type Settings} from './types';

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    response = await fetch(url, {...options, headers: {'Content-Type': 'application/json', ...options.headers}});
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new Error('无法连接服务，请检查网络连接后重试。');
  }
  const body = await response.text();
  let data: Record<string, unknown>;
  try {
    data = body ? JSON.parse(body) : {};
  } catch {
    throw new Error('服务返回了无法识别的数据，请稍后重试。');
  }
  if (!response.ok) {
    const detail = typeof data.error === 'string' ? data.error : typeof data.message === 'string' ? data.message : '';
    throw new Error(/[\u4e00-\u9fff]/.test(detail) ? detail : `请求未完成（${response.status}），请检查配置后重试。`);
  }
  return data as T;
}

function errorText(error: unknown) {
  return error instanceof Error && /[\u4e00-\u9fff]/.test(error.message) ? error.message : '操作未完成，请稍后重试。';
}

function downloadProject(project: Project) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(project, null, 2)], {type: 'application/json;charset=utf-8'}));
  const link = document.createElement('a');
  link.href = url;
  link.download = `${project.name.replace(/[\\/:*?"<>|]/g, '_') || '影序工程'}.json`;
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function downloadBundle(project: Project) {
  let response: Response;
  try {
    response = await fetch('/api/bundle', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({project})});
  } catch {throw new Error('无法连接服务，请检查网络连接后重试。');}
  if (!response.ok) {
    const result = await response.json().catch(() => ({}));
    throw new Error(typeof result.error === 'string' && /[\u4e00-\u9fff]/.test(result.error) ? result.error : '工程包下载失败，请稍后重试。');
  }
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = `${project.name.replace(/[\\/:*?"<>|]/g, '_') || '映序工程'}.zip`;
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function Modal({title, icon, onClose, children, wide = false}: {title: string; icon: ReactNode; onClose: () => void; children: ReactNode; wide?: boolean}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const previousFocus = document.activeElement;
    const element = dialog.current;
    if (element && !element.open) element.showModal();
    return () => {
      element?.close();
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
    };
  }, []);
  return <dialog ref={dialog} className={`modal${wide ? ' modal-wide' : ''}`} aria-labelledby={titleId} onCancel={event => {event.preventDefault(); onClose();}} onKeyDown={event => {
    if (event.key !== 'Tab') return;
    const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex="0"]')).filter(element => element.getClientRects().length > 0);
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (!first) {event.preventDefault(); return;}
    if (event.shiftKey && document.activeElement === first) {event.preventDefault(); last.focus();}
    else if (!event.shiftKey && document.activeElement === last) {event.preventDefault(); first.focus();}
  }} onClick={event => {if (event.target === event.currentTarget) {const bounds = event.currentTarget.getBoundingClientRect(); if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) onClose();}}}>
    <header className="modal-heading"><h2 id={titleId}>{icon}{title}</h2><button type="button" className="icon-button" onClick={onClose} aria-label="关闭弹窗" title="关闭"><X size={18} aria-hidden="true" /></button></header>
    {children}
  </dialog>;
}

const emptySettings: Settings = {baseUrl: '', model: '', apiKey: '', imageBaseUrl: '', imageModel: '', imageApiKey: '', ttsProvider: 'local', ttsBaseUrl: '', ttsModel: '', ttsApiKey: '', ttsVoice: 'cmn', ttsSpeed: 1};
type ModelTab = 'text' | 'image' | 'tts';
type ModelOption = {id: string; name?: string};
const ttsDefaults = {
  local: {ttsBaseUrl: '', ttsModel: '', ttsVoice: 'cmn'},
  mimo: {ttsBaseUrl: 'https://api.xiaomimimo.com/v1', ttsModel: 'mimo-v2.5-tts', ttsVoice: 'mimo_default'},
  openai: {ttsBaseUrl: 'https://api.openai.com/v1', ttsModel: 'gpt-4o-mini-tts', ttsVoice: 'alloy'}
};
const voiceOptions = {
  local: ['cmn', 'en'],
  mimo: ['mimo_default', '冰糖', '茉莉', '苏打', '白桦', 'Mia', 'Chloe', 'Milo', 'Dean'],
  openai: ['alloy', 'ash', 'coral', 'echo', 'fable', 'nova', 'onyx', 'sage', 'shimmer']
};

export function SettingsModal({onClose, onSaved}: {onClose: () => void; onSaved: () => void}) {
  const [settings, setSettings] = useState<Settings>(emptySettings);
  const [tab, setTab] = useState<ModelTab>('text');
  const [loading, setLoading] = useState(true);
  const [readFailed, setReadFailed] = useState(false);
  const [loadVersion, setLoadVersion] = useState(0);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [models, setModels] = useState<Record<ModelTab, ModelOption[]>>({text: [], image: [], tts: []});
  const [modelsLoading, setModelsLoading] = useState(false);
  const modelsController = useRef<AbortController | null>(null);
  const [feedback, setFeedback] = useState<{text: string; error: boolean} | null>(null);
  const baseId = useId();
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setReadFailed(false);
    setFeedback(null);
    request<Settings>('/api/settings', {signal: controller.signal}).then(value => {if (!controller.signal.aborted) setSettings({...emptySettings, ...value, apiKey: '', imageApiKey: '', ttsApiKey: ''});}).catch(error => {if (!controller.signal.aborted) {setReadFailed(true); setFeedback({text: errorText(error), error: true});}}).finally(() => {if (!controller.signal.aborted) setLoading(false);});
    return () => controller.abort();
  }, [loadVersion]);
  useEffect(() => () => modelsController.current?.abort(), []);
  const update = (field: keyof Settings, value: string | number) => {
    setSettings(previous => ({...previous, [field]: value}));
    setFeedback(null);
    if (['baseUrl', 'apiKey', 'imageBaseUrl', 'imageApiKey', 'ttsBaseUrl', 'ttsApiKey'].includes(field)) {
      modelsController.current?.abort();
      setModelsLoading(false);
      setModels(previous => ({...previous, [tab]: []}));
    }
  };
  const image = tab === 'image';
  const tts = tab === 'tts';
  const provider = settings.ttsProvider || 'local';
  const local = tts && provider === 'local';
  const baseField = tts ? 'ttsBaseUrl' : image ? 'imageBaseUrl' : 'baseUrl';
  const modelField = tts ? 'ttsModel' : image ? 'imageModel' : 'model';
  const keyField = tts ? 'ttsApiKey' : image ? 'imageApiKey' : 'apiKey';
  const hasKey = tts ? settings.hasTtsApiKey : image ? settings.hasImageApiKey : settings.hasApiKey;
  const busy = loading || saving || testing;
  const changeTab = (next: ModelTab) => {
    modelsController.current?.abort();
    setModelsLoading(false);
    setTab(next);
    setFeedback(null);
  };
  const changeProvider = (next: NonNullable<Settings['ttsProvider']>) => {
    modelsController.current?.abort();
    setModelsLoading(false);
    setModels(previous => ({...previous, tts: []}));
    setSettings(previous => ({...previous, ttsProvider: next, ...ttsDefaults[next], ttsApiKey: '', hasTtsApiKey: false}));
    setFeedback(null);
  };
  const refreshModels = async () => {
    modelsController.current?.abort();
    const controller = new AbortController();
    modelsController.current = controller;
    setModelsLoading(true);
    setFeedback(null);
    const kind = tab;
    try {
      const result = await request<{models: ModelOption[]}>('/api/models', {method: 'POST', signal: controller.signal, body: JSON.stringify({...settings, kind})});
      if (controller.signal.aborted) return;
      const options = Array.isArray(result.models) ? result.models.filter(item => item && typeof item.id === 'string' && item.id.trim()).map(item => ({id: item.id, name: typeof item.name === 'string' ? item.name : undefined})) : [];
      const unique = options.filter((item, index) => options.findIndex(other => other.id === item.id) === index);
      setModels(previous => ({...previous, [kind]: unique}));
      if (!unique.length) setFeedback({text: '接口未返回模型列表，可以手动输入模型名称。', error: true});
    } catch (error) {if (!controller.signal.aborted) setFeedback({text: errorText(error), error: true});}
    finally {if (!controller.signal.aborted) setModelsLoading(false);}
  };
  const test = async () => {
    setTesting(true);
    setFeedback(null);
    try {
      const result = await request<{message?: string; success?: boolean}>('/api/settings/test', {method: 'POST', body: JSON.stringify({...settings, kind: tab})});
      if (result.success === false) throw new Error(result.message || '连接测试失败，请检查接口地址、模型和密钥。');
      setFeedback({text: result.message && /[\u4e00-\u9fff]/.test(result.message) ? result.message : '连接成功，模型服务可用。', error: false});
    } catch (error) {setFeedback({text: errorText(error), error: true});}
    finally {setTesting(false);}
  };
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setFeedback(null);
    try {
      await request<Settings>('/api/settings', {method: 'PUT', body: JSON.stringify(settings)});
      onSaved();
      onClose();
    } catch (error) {setFeedback({text: errorText(error), error: true});}
    finally {setSaving(false);}
  };
  return <Modal title="模型设置" icon={<Settings2 size={20} aria-hidden="true" />} onClose={onClose}>
    <form onSubmit={save}>
      <div className="modal-body">
        <div className="modal-tabs" aria-label="模型类型"><button type="button" className={tab === 'text' ? 'active' : ''} aria-pressed={tab === 'text'} onClick={() => changeTab('text')} disabled={busy}><Code2 size={16} aria-hidden="true" />文本模型</button><button type="button" className={image ? 'active' : ''} aria-pressed={image} onClick={() => changeTab('image')} disabled={busy}><Image size={16} aria-hidden="true" />生图模型</button><button type="button" className={tts ? 'active' : ''} aria-pressed={tts} onClick={() => changeTab('tts')} disabled={busy}><Mic size={16} aria-hidden="true" />语音模型</button></div>
        {loading ? <div className="loading-row" role="status"><LoaderCircle size={18} className="spin" aria-hidden="true" />正在读取配置</div> : <div className="form-grid">
          {tts && <div className="form-field"><label htmlFor={`${baseId}-provider`}>语音来源</label><select id={`${baseId}-provider`} value={provider} onChange={event => changeProvider(event.target.value as NonNullable<Settings['ttsProvider']>)} disabled={busy}><option value="local">本地默认语音</option><option value="mimo">小米 MiMo</option><option value="openai">OpenAI 兼容接口</option></select></div>}
          {!local && <>
            <div className="form-field"><label htmlFor={`${baseId}-url`}>接口地址</label><input id={`${baseId}-url`} type="url" value={settings[baseField] || ''} onChange={event => update(baseField, event.target.value)} placeholder="https://api.example.com/v1" autoComplete="url" disabled={busy} /></div>
            <div className="form-field"><div className="field-label-row"><label htmlFor={`${baseId}-model`}>模型名称</label><button type="button" className="icon-button" onClick={refreshModels} disabled={busy || modelsLoading || readFailed || !settings[baseField]} aria-label="刷新模型列表" title="刷新模型列表">{modelsLoading ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <RefreshCw size={16} aria-hidden="true" />}</button></div><input id={`${baseId}-model`} value={settings[modelField] || ''} onChange={event => update(modelField, event.target.value)} placeholder={tts ? '输入语音模型名称' : image ? '输入生图模型名称' : '输入文本模型名称'} autoComplete="off" disabled={busy} />{modelsLoading && <span className="field-caption" role="status">正在获取模型列表</span>}{models[tab].length > 0 && <select className="model-picker" aria-label="选择模型" value={models[tab].some(item => item.id === settings[modelField]) ? settings[modelField] : ''} onChange={event => {if (event.target.value) update(modelField, event.target.value);}} disabled={busy}><option value="">从模型列表选择</option>{models[tab].map(item => <option key={item.id} value={item.id}>{item.name && item.name !== item.id ? `${item.name} · ${item.id}` : item.id}</option>)}</select>}</div>
            <div className="form-field"><label htmlFor={`${baseId}-key`}>访问密钥</label><input id={`${baseId}-key`} type="password" value={settings[keyField] || ''} onChange={event => update(keyField, event.target.value)} placeholder={hasKey ? '已保存，留空保留现有密钥' : '输入访问密钥'} autoComplete="new-password" disabled={busy} /><span className="field-caption">{hasKey ? '已保存密钥' : '尚未设置密钥'}</span></div>
          </>}
          {tts && <>
            <div className="form-field"><label htmlFor={`${baseId}-voice`}>默认音色</label><input id={`${baseId}-voice`} list={`${baseId}-voices`} value={settings.ttsVoice || ttsDefaults[provider].ttsVoice} onChange={event => update('ttsVoice', event.target.value)} autoComplete="off" disabled={busy} /><datalist id={`${baseId}-voices`}>{voiceOptions[provider].map(voice => <option key={voice} value={voice}>{voice === 'cmn' ? '普通话' : voice === 'en' ? '英语' : voice === 'mimo_default' ? '默认音色' : voice}</option>)}</datalist></div>
            <div className="form-field"><label htmlFor={`${baseId}-speed`}>默认语速 · {(settings.ttsSpeed || 1).toFixed(2)} 倍</label><input id={`${baseId}-speed`} type="range" min={0.5} max={2} step={0.05} value={settings.ttsSpeed || 1} onChange={event => update('ttsSpeed', Number(event.target.value))} disabled={busy} /></div>
          </>}
        </div>}
        {feedback && <div className={`status-message ${feedback.error ? 'error' : 'success'}`} role={feedback.error ? 'alert' : 'status'}>{!feedback.error && <CheckCircle2 size={16} aria-hidden="true" />}{feedback.text}</div>}
      </div>
      <footer className="modal-footer">{readFailed ? <button type="button" className="button secondary" onClick={() => setLoadVersion(version => version + 1)}><RefreshCw size={16} aria-hidden="true" />重新读取</button> : <button type="button" className="button secondary" onClick={test} disabled={busy || modelsLoading || (!local && (!settings[baseField] || !settings[modelField]))}>{testing ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <Unplug size={16} aria-hidden="true" />}{testing ? '正在测试' : local ? '检测本地语音' : '测试连接'}</button>}<button type="submit" className="button primary" disabled={busy || modelsLoading || readFailed}>{saving ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <Save size={16} aria-hidden="true" />}{saving ? '正在保存' : '保存设置'}</button></footer>
    </form>
  </Modal>;
}

const aspects = [{label: '16:9', name: '横屏', width: 1920, height: 1080, icon: Monitor, className: 'landscape'}, {label: '9:16', name: '竖屏', width: 1080, height: 1920, icon: Smartphone, className: 'portrait'}, {label: '1:1', name: '方形', width: 1080, height: 1080, icon: Square, className: 'square'}];

export function NewProjectModal({onClose, onCreate}: {onClose: () => void; onCreate: (name: string, width: number, height: number) => void | Promise<void>}) {
  const [name, setName] = useState('未命名工程');
  const [aspectIndex, setAspectIndex] = useState(0);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');
  const nameId = useId();
  const create = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!name.trim() || creating) return;
    setCreating(true);
    setError('');
    const aspect = aspects[aspectIndex];
    try {await onCreate(name.trim(), aspect.width, aspect.height);}
    catch (cause) {setError(errorText(cause));}
    finally {setCreating(false);}
  };
  return <Modal title="新建工程" icon={<Plus size={20} aria-hidden="true" />} onClose={onClose}>
    <form onSubmit={create}>
      <div className="modal-body"><div className="form-field"><label htmlFor={nameId}>工程名称</label><input id={nameId} value={name} onChange={event => {setName(event.target.value); setError('');}} maxLength={80} required autoFocus disabled={creating} /></div><fieldset className="form-section" disabled={creating}><legend>画布比例</legend><div className="aspect-options">{aspects.map((aspect, index) => <button key={aspect.label} type="button" className={`aspect-option${index === aspectIndex ? ' active' : ''}`} aria-pressed={index === aspectIndex} onClick={() => setAspectIndex(index)}><span className={`aspect-preview ${aspect.className}`}><aspect.icon size={24} aria-hidden="true" /></span><strong>{aspect.label}</strong><span>{aspect.name}</span></button>)}</div></fieldset>{error && <div className="status-message error" role="alert">{error}</div>}</div>
      <footer className="modal-footer"><button type="button" className="button secondary" onClick={onClose}><X size={16} aria-hidden="true" />取消</button><button type="submit" className="button primary" disabled={!name.trim() || creating}>{creating ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <Plus size={16} aria-hidden="true" />}{creating ? '正在创建' : '创建工程'}</button></footer>
    </form>
  </Modal>;
}

const jobLabels: Record<ExportJob['status'], string> = {queued: '等待渲染', running: '正在渲染', completed: '视频已就绪', failed: '导出失败', cancelled: '已取消导出'};

export function ExportModal({project, onClose}: {project: Project; onClose: () => void}) {
  const [format, setFormat] = useState<'video' | 'bundle' | 'json'>('video');
  const [job, setJob] = useState<ExportJob | null>(null);
  const [starting, setStarting] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [bundling, setBundling] = useState(false);
  const [error, setError] = useState('');
  const active = !!job && (job.status === 'queued' || job.status === 'running');
  useEffect(() => {
    if (!job || (job.status !== 'queued' && job.status !== 'running')) return;
    const controller = new AbortController();
    let timer: number;
    const poll = async () => {
      try {
        const next = await request<ExportJob>(`/api/exports/${encodeURIComponent(job.id)}`, {signal: controller.signal});
        if (!controller.signal.aborted) {setJob(next); setError(''); if (next.status === 'queued' || next.status === 'running') timer = window.setTimeout(poll, 1000);}
      } catch (cause) {
        if (!controller.signal.aborted) {setError(errorText(cause)); timer = window.setTimeout(poll, 1000);}
      }
    };
    timer = window.setTimeout(poll, 1000);
    return () => {controller.abort(); window.clearTimeout(timer);};
  }, [job?.id, job?.status]);
  const start = async () => {
    setStarting(true);
    setError('');
    try {setJob(await request<ExportJob>('/api/exports', {method: 'POST', body: JSON.stringify({project})}));}
    catch (cause) {setError(errorText(cause));}
    finally {setStarting(false);}
  };
  const cancel = async () => {
    if (!job) return;
    setCancelling(true);
    try {
      const next = await request<ExportJob>(`/api/exports/${encodeURIComponent(job.id)}/cancel`, {method: 'POST'});
      setJob(next.id ? next : {...job, status: 'cancelled', message: '已取消导出'});
      setError('');
    } catch (cause) {setError(errorText(cause));}
    finally {setCancelling(false);}
  };
  const progress = Math.min(100, Math.max(0, (job?.progress || 0) * 100));
  const bundle = async () => {
    setBundling(true);
    setError('');
    try {await downloadBundle(project);}
    catch (cause) {setError(errorText(cause));}
    finally {setBundling(false);}
  };
  return <Modal title="导出工程" icon={<Download size={20} aria-hidden="true" />} onClose={onClose}>
    <div className="modal-body"><div className="modal-tabs" aria-label="导出格式"><button type="button" className={format === 'video' ? 'active' : ''} aria-pressed={format === 'video'} onClick={() => setFormat('video')}><Film size={16} aria-hidden="true" />MP4 视频</button><button type="button" className={format === 'bundle' ? 'active' : ''} aria-pressed={format === 'bundle'} onClick={() => setFormat('bundle')}><Package size={16} aria-hidden="true" />完整工程包</button><button type="button" className={format === 'json' ? 'active' : ''} aria-pressed={format === 'json'} onClick={() => setFormat('json')}><Code2 size={16} aria-hidden="true" />JSON</button></div>
      <dl className="export-summary"><div><dt>工程</dt><dd>{project.name}</dd></div><div><dt>画布</dt><dd>{project.width} × {project.height}</dd></div><div><dt>时长</dt><dd>{(totalFrames(project) / project.fps).toFixed(1)} 秒</dd></div><div><dt>帧率</dt><dd>{project.fps} 帧／秒</dd></div></dl>
      {format === 'video' && job && <div className="export-progress"><div className="loading-row" role="status">{active ? <LoaderCircle size={18} className="spin" aria-hidden="true" /> : job.status === 'completed' ? <CheckCircle2 size={18} aria-hidden="true" /> : null}<strong>{jobLabels[job.status]}</strong><span>{Math.round(progress)}%</span></div><div className="progress-track" role="progressbar" aria-label="视频导出进度" aria-valuenow={Math.round(progress)} aria-valuemin={0} aria-valuemax={100}><div className="progress-fill" style={{width: `${progress}%`}} /></div>{job.message && /[\u4e00-\u9fff]/.test(job.message) && <p className={`status-message${job.status === 'failed' ? ' error' : ''}`} role={job.status === 'failed' ? 'alert' : 'status'}>{job.message}</p>}</div>}
      {error && <div className="status-message error" role="alert">{error}</div>}
    </div>
    <footer className="modal-footer"><button type="button" className="button secondary" onClick={onClose}><X size={16} aria-hidden="true" />关闭</button>{format === 'bundle' ? <button type="button" className="button primary" onClick={bundle} disabled={bundling}>{bundling ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <Package size={16} aria-hidden="true" />}{bundling ? '正在打包' : '下载工程包'}</button> : format === 'json' ? <button type="button" className="button primary" onClick={() => downloadProject(project)}><Download size={16} aria-hidden="true" />下载 JSON</button> : active ? <button type="button" className="button secondary" onClick={cancel} disabled={cancelling}>{cancelling ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <X size={16} aria-hidden="true" />}{cancelling ? '正在取消' : '取消导出'}</button> : job?.status === 'completed' && job.url ? <a className="button primary" href={job.url} download><Download size={16} aria-hidden="true" />下载视频</a> : <button type="button" className="button primary" onClick={start} disabled={starting}>{starting ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <Film size={16} aria-hidden="true" />}{starting ? '正在创建任务' : job ? '重新导出' : '开始导出'}</button>}</footer>
  </Modal>;
}

export function SourceModal({project, onClose}: {project: Project; onClose: () => void}) {
  const [bundling, setBundling] = useState(false);
  const [error, setError] = useState('');
  const bundle = async () => {
    setBundling(true);
    setError('');
    try {await downloadBundle(project);}
    catch (cause) {setError(errorText(cause));}
    finally {setBundling(false);}
  };
  return <Modal title="工程源码" icon={<Code2 size={20} aria-hidden="true" />} onClose={onClose} wide>
    <div className="modal-body"><pre className="source-code" tabIndex={0} aria-label="工程 JSON 源码"><code>{JSON.stringify(project, null, 2)}</code></pre>{error && <div className="status-message error" role="alert">{error}</div>}</div>
    <footer className="modal-footer"><button type="button" className="button secondary" onClick={onClose}><X size={16} aria-hidden="true" />关闭</button><button type="button" className="button secondary" onClick={() => downloadProject(project)}><Code2 size={16} aria-hidden="true" />下载 JSON</button><button type="button" className="button primary" onClick={bundle} disabled={bundling}>{bundling ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <Package size={16} aria-hidden="true" />}{bundling ? '正在打包' : '下载工程包'}</button></footer>
  </Modal>;
}
