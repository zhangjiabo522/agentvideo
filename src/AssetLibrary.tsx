import { useMemo, useState } from "react";
import { AudioLines, Image, ImageOff, Loader2, RefreshCw, Search, X } from "lucide-react";

export type Asset = {
  id: string;
  url: string;
  name: string;
  type: string;
  mimeType?: string;
  size?: number;
  width?: number;
  height?: number;
  durationSeconds?: number;
  createdAt?: string;
  source?: string;
};

export const assetKind = (asset: Pick<Asset, "type" | "mimeType">) =>
  (asset.mimeType || asset.type).startsWith("audio") ? "audio" : "image";

const durationLabel = (value: number) => {
  const seconds = Math.max(1, Math.round(value));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
};

const sizeLabel = (value?: number) => !value ? "" : value >= 1024 * 1024
  ? `${(value / 1024 / 1024).toFixed(1)} MB`
  : `${Math.max(1, Math.round(value / 1024))} KB`;

function AssetCard({ asset, used, disabled, onUse }: {
  asset: Asset;
  used: boolean;
  disabled: boolean;
  onUse: (asset: Asset) => void;
}) {
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);
  const kind = assetKind(asset);
  const detail = kind === "audio"
    ? asset.durationSeconds ? durationLabel(asset.durationSeconds) : "音频"
    : asset.width && asset.height ? `${asset.width} × ${asset.height}` : "图片";
  const source = asset.source === "generated" ? "AI 生成" : asset.source === "speech" ? "旁白" : asset.source === "builtin" ? "内置" : "";
  const src = retry && !asset.url.startsWith("data:") && !asset.url.startsWith("blob:")
    ? `${asset.url}${asset.url.includes("?") ? "&" : "?"}preview=${retry}`
    : asset.url;
  return (
    <article className={`asset-card ${used ? "is-used" : ""}`}>
      <button
        type="button"
        className="asset-card-select"
        disabled={disabled || failed}
        onClick={() => onUse(asset)}
        aria-label={`${kind === "audio" ? "添加音频" : "使用图片"}：${asset.name}`}
        title={disabled ? "人工接管后可使用素材" : `使用 ${asset.name}`}
      >
        <span className={`asset-card-preview ${kind === "audio" ? "is-audio" : ""}`}>
          {kind === "audio" ? <AudioLines size={30} aria-hidden="true" /> : failed
            ? <span className="asset-image-failure"><ImageOff size={23} aria-hidden="true" /><span>图片加载失败</span></span>
            : <img key={src} src={src} alt={asset.name} loading="lazy" decoding="async" onError={() => setFailed(true)} />}
          {used && <span className="asset-used-label">已使用</span>}
          {kind === "audio" && asset.durationSeconds ? <span className="asset-duration">{durationLabel(asset.durationSeconds)}</span> : null}
        </span>
        <span className="asset-card-info">
          <strong title={asset.name}>{asset.name}</strong>
          <span>{detail}{sizeLabel(asset.size) ? ` · ${sizeLabel(asset.size)}` : ""}</span>
          {source && <small>{source}</small>}
        </span>
      </button>
      {failed && <button type="button" className="asset-retry" onClick={() => { setFailed(false); setRetry(Date.now()); }} aria-label={`重新加载 ${asset.name}`}><RefreshCw size={13} aria-hidden="true" />重新加载</button>}
    </article>
  );
}

export function AssetLibrary({ assets, loading, error, disabled, usedURLs, onRefresh, onUse }: {
  assets: Asset[];
  loading: boolean;
  error: string;
  disabled: boolean;
  usedURLs: Set<string>;
  onRefresh: () => void;
  onUse: (asset: Asset) => void;
}) {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<"all" | "image" | "audio">("all");
  const visible = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return assets.filter(asset => (filter === "all" || assetKind(asset) === filter) && (!query || asset.name.toLocaleLowerCase().includes(query)));
  }, [assets, filter, search]);
  const images = assets.filter(asset => assetKind(asset) === "image").length;
  return (
    <section className="asset-library" aria-label="工作区素材" aria-busy={loading}>
      <div className="asset-library-heading">
        <span>{assets.length} 个素材<span className="asset-library-secondary"> · 本地素材库</span></span>
        <button type="button" className="asset-refresh" onClick={onRefresh} disabled={loading} aria-label="刷新素材库" title="刷新素材库">
          {loading ? <Loader2 className="spin" size={16} aria-hidden="true" /> : <RefreshCw size={16} aria-hidden="true" />}
        </button>
      </div>
      <label className="asset-search">
        <Search size={15} aria-hidden="true" />
        <span className="sr-only">搜索素材名称</span>
        <input aria-label="搜索素材名称" value={search} onChange={event => setSearch(event.target.value)} placeholder="搜索素材名称" />
        {search && <button type="button" onClick={() => setSearch("")} aria-label="清除素材搜索"><X size={14} aria-hidden="true" /></button>}
      </label>
      <div className="asset-filters" aria-label="素材类型">
        {([{ id: "all", name: "全部", count: assets.length }, { id: "image", name: "图片", count: images }, { id: "audio", name: "音频", count: assets.length - images }] as const).map(item => (
          <button type="button" key={item.id} aria-pressed={filter === item.id} className={filter === item.id ? "active" : ""} onClick={() => setFilter(item.id)}>{item.name}<span>{item.count}</span></button>
        ))}
      </div>
      {error && <div className="asset-error" role="alert"><span>{error}</span><button type="button" onClick={onRefresh} disabled={loading}>重试</button></div>}
      {loading && !assets.length ? <div className="asset-empty" role="status"><Loader2 className="spin" size={24} aria-hidden="true" /><strong>正在读取素材</strong><span>图片、上传文件和生成旁白会保存在这里。</span></div>
        : !visible.length ? <div className="asset-empty" role="status"><Image size={26} aria-hidden="true" /><strong>{assets.length ? "没有匹配的素材" : "素材库等待你的灵感"}</strong><span>{assets.length ? "试试其他名称，或切换素材类型。" : "上传图片或音频，AI 生成的素材也会自动出现。"}</span>{(search || filter !== "all") && <button type="button" onClick={() => { setSearch(""); setFilter("all"); }}>查看全部素材</button>}</div>
          : <div className="asset-grid">{visible.map(asset => <AssetCard key={asset.url} asset={asset} used={usedURLs.has(asset.url)} disabled={disabled} onUse={onUse} />)}</div>}
      {visible.length > 0 && <p className="asset-usage-hint">{disabled ? "AI 可以使用库中素材；人工接管后，点击素材可放入镜头。" : "点击图片可替换选中的图片，或添加到当前镜头。音频会加入时间线。"}</p>}
    </section>
  );
}
