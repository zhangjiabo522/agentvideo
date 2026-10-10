import { useEffect, useRef, useState } from "react";
import { ArrowDown, ArrowUpRight, Check, Download, Film, Github, Play, Sparkles } from "lucide-react";
import "./public-gallery.css";

type PublicVideo = {
  id: string;
  title: string;
  description: string;
  url: string;
  poster: string;
  duration: number;
  width: number;
  height: number;
  createdAt: string;
  featured: boolean;
};

const durationLabel = (duration: number) => {
  const seconds = Math.max(0, Math.round(duration || 0));
  return `${Math.floor(seconds / 60).toString().padStart(2, "0")}:${(seconds % 60).toString().padStart(2, "0")}`;
};

const dateLabel = (date: string) => {
  const value = new Date(date);
  return Number.isNaN(value.getTime()) ? "" : value.toLocaleDateString("zh-CN", { month: "long", day: "numeric" });
};

export default function PublicGallery({ githubUrl }: { githubUrl: string }) {
  const [videos, setVideos] = useState<PublicVideo[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [playError, setPlayError] = useState(false);
  const player = useRef<HTMLVideoElement>(null);
  const playerSection = useRef<HTMLElement>(null);
  const current = videos.find((video) => video.id === selected) ?? videos.find((video) => video.featured) ?? videos[0];
  const repository = githubUrl || "https://github.com/zhangjiabo522/agentvideo";

  useEffect(() => {
    document.title = "映序 · AI 视频作品集";
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", "#101713");
    const controller = new AbortController();
    let active = true;
    const timeout = window.setTimeout(() => controller.abort(), 20000);
    setLoading(true);
    setFailed(false);
    fetch("/api/public/videos", { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error("作品暂时无法加载");
        const value = await response.json();
        if (!Array.isArray(value)) throw new Error("作品信息无法读取");
        if (active) setVideos(value);
      })
      .catch(() => { if (active) setFailed(true); })
      .finally(() => {
        window.clearTimeout(timeout);
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [attempt]);

  useEffect(() => {
    const pauseWhenHidden = () => {
      if (document.hidden) player.current?.pause();
    };
    document.addEventListener("visibilitychange", pauseWhenHidden);
    return () => document.removeEventListener("visibilitychange", pauseWhenHidden);
  }, []);

  useEffect(() => {
    const element = player.current;
    if (!element) return;
    const observer = new IntersectionObserver(([entry]) => {
      if (!entry.isIntersecting) element.pause();
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [current?.id]);

  const chooseVideo = (video: PublicVideo) => {
    player.current?.pause();
    setSelected(video.id);
    setPlaying(false);
    setPlayError(false);
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    playerSection.current?.scrollIntoView({ behavior: reducedMotion ? "instant" : "smooth", block: "start" });
    playerSection.current?.focus({ preventScroll: true });
  };

  const playVideo = () => {
    setPlayError(false);
    player.current?.play().catch(() => setPlayError(true));
  };

  return (
    <div className="public-gallery">
      <a className="gallery-skip" href="#gallery-main">跳转至作品</a>
      <header className="gallery-header gallery-width">
        <a className="gallery-brand" href="/" aria-label="映序首页">
          <span className="gallery-brand-mark"><Film size={22} aria-hidden="true" /></span>
          <span>映序</span>
          <span className="gallery-brand-caption">AI 视频作品集</span>
        </a>
        <nav aria-label="主要导航">
          <a className="gallery-browse-link" href="#works">浏览作品</a>
          <a className="gallery-source-link" href={repository} target="_blank" rel="noopener noreferrer">
            <Github size={18} aria-hidden="true" /><span>GitHub 源码</span><ArrowUpRight size={16} aria-hidden="true" />
          </a>
        </nav>
      </header>

      <main id="gallery-main" className="gallery-width">
        <section className="gallery-intro" aria-labelledby="gallery-title">
          <div className="gallery-eyebrow"><span /> 从一句话，到一段视频</div>
          <h1 id="gallery-title">映序<br /><span>AI 视频作品集</span></h1>
          <div className="gallery-intro-bottom">
            <p>让 AI 剪视频，把创意变成画面。<br className="gallery-desktop-break" />文本模型编排镜头，人看见每一步。</p>
            <a className="gallery-see-works" href="#works">观看作品 <ArrowDown size={18} aria-hidden="true" /></a>
          </div>
        </section>

        {loading ? (
          <div className="gallery-state gallery-loading" role="status"><Film size={32} aria-hidden="true" /><p>正在加载作品…</p></div>
        ) : failed ? (
          <div className="gallery-state" role="alert"><h2>作品暂时无法加载</h2><p>请检查网络连接后重试。</p><button className="gallery-button" onClick={() => setAttempt((value) => value + 1)}>重新加载</button></div>
        ) : !current ? (
          <div className="gallery-state"><Film size={32} aria-hidden="true" /><h2>下一段作品，正在路上</h2><p>公开作品将在这里展示。你也可以到 GitHub 查看项目源码。</p><a className="gallery-button" href={repository} target="_blank" rel="noopener noreferrer">查看源码 <ArrowUpRight size={16} aria-hidden="true" /></a></div>
        ) : (
          <>
            <section ref={playerSection} className="gallery-player-section" tabIndex={-1} aria-labelledby="current-video-title">
              <div className="gallery-player-topline"><span><span className="gallery-status-dot" />{current.featured ? "精选作品" : "正在观看"}</span><span>由映序制作</span></div>
              <div className="gallery-player-frame">
                <video
                  key={current.id}
                  ref={player}
                  src={current.url}
                  poster={current.poster || undefined}
                  preload="none"
                  controls
                  playsInline
                  aria-label={current.title}
                  onPlay={() => setPlaying(true)}
                  onPause={() => setPlaying(false)}
                  onEnded={() => setPlaying(false)}
                  onError={() => setPlayError(true)}
                />
                {!playing && !playError && <button className="gallery-play-button" onClick={playVideo} aria-label={`播放：${current.title}`}><Play size={25} fill="currentColor" aria-hidden="true" /></button>}
                {playError && <div className="gallery-player-error" role="alert"><p>视频暂时无法播放，请重试或下载观看。</p><button className="gallery-button" onClick={() => { player.current?.load(); playVideo(); }}>重试播放</button></div>}
              </div>
              <div className="gallery-player-details">
                <div><h2 id="current-video-title">{current.title}</h2>{current.description && <p>{current.description}</p>}<div className="gallery-video-meta"><span>{durationLabel(current.duration)}</span>{current.width > 0 && current.height > 0 && <span>{current.width} × {current.height}</span>}{dateLabel(current.createdAt) && <span>{dateLabel(current.createdAt)}</span>}</div></div>
                <a className="gallery-button gallery-download" href={current.url} download={`${current.title}.mp4`}><Download size={18} aria-hidden="true" />下载视频</a>
              </div>
            </section>

            <section className="gallery-works" id="works" aria-labelledby="works-title">
              <div className="gallery-section-heading"><div><span className="gallery-section-caption">每个想法，都值得被看见</span><h2 id="works-title">全部作品 <span>{videos.length.toString().padStart(2, "0")}</span></h2></div><span className="gallery-works-note">点击封面观看 · 支持下载</span></div>
              <div className="gallery-video-grid">
                {videos.map((video, index) => (
                  <article className={`gallery-video-card${current.id === video.id ? " is-current" : ""}`} key={video.id}>
                    <button className="gallery-card-cover" onClick={() => chooseVideo(video)} aria-label={`观看：${video.title}`} aria-pressed={current.id === video.id}>
                      <div className="gallery-poster-fallback"><Film size={32} aria-hidden="true" /></div>
                      {video.poster && <img src={video.poster} alt="" loading="lazy" width={video.width || 1280} height={video.height || 720} onError={(event) => { event.currentTarget.style.visibility = "hidden"; }} />}
                      <span className="gallery-card-shade" />
                      {video.featured && <span className="gallery-featured"><Sparkles size={12} aria-hidden="true" />精选</span>}
                      <span className="gallery-card-play"><Play size={18} fill="currentColor" aria-hidden="true" /></span>
                      <span className="gallery-card-duration">{durationLabel(video.duration)}</span>
                      {current.id === video.id && <span className="gallery-current-label"><Check size={12} aria-hidden="true" />当前作品</span>}
                    </button>
                    <div className="gallery-card-details"><div className="gallery-card-heading"><span className="gallery-card-index">{(index + 1).toString().padStart(2, "0")}</span><h3><button onClick={() => chooseVideo(video)}>{video.title}</button></h3><a className="gallery-card-download" href={video.url} download={`${video.title}.mp4`} aria-label={`下载：${video.title}`}><Download size={18} aria-hidden="true" /></a></div>{video.description && <p>{video.description}</p>}</div>
                  </article>
                ))}
              </div>
            </section>
          </>
        )}

        <section className="gallery-about" aria-labelledby="gallery-about-title"><div className="gallery-about-mark"><Film size={27} aria-hidden="true" /></div><div><h2 id="gallery-about-title">AI 创作，人看见。</h2><p>映序让文本 AI 使用工具编排、配音与剪辑，并把创作过程可视化。这里展示已经完成的视频作品。</p></div><a href={repository} target="_blank" rel="noopener noreferrer">在 GitHub 了解映序 <ArrowUpRight size={18} aria-hidden="true" /></a></section>
      </main>

      <footer className="gallery-footer gallery-width"><span>映序 · 用文本与代码，讲述你的故事。</span><a href={repository} target="_blank" rel="noopener noreferrer"><Github size={16} aria-hidden="true" />开源项目 <ArrowUpRight size={14} aria-hidden="true" /></a></footer>
    </div>
  );
}
