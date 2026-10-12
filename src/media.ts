export function readAudioDuration(url: string, signal?: AbortSignal): Promise<number> {
  return new Promise((resolve, reject) => {
    const audio = new Audio();
    let settled = false;
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      const duration = audio.duration;
      clearTimeout(timeout);
      signal?.removeEventListener("abort", abort);
      audio.onloadedmetadata = null;
      audio.onerror = null;
      audio.removeAttribute("src");
      audio.load();
      if (error) reject(error);
      else if (!Number.isFinite(duration) || duration <= 0) reject(new Error("无法读取音频时长，请换一个音频文件。"));
      else resolve(duration);
    };
    const abort = () => finish(new DOMException("音频读取已取消", "AbortError"));
    const timeout = setTimeout(() => finish(new Error("读取音频超时，素材已保留，可稍后从素材库重试。")), 15000);
    if (signal?.aborted) { abort(); return; }
    signal?.addEventListener("abort", abort, { once: true });
    audio.preload = "metadata";
    audio.onloadedmetadata = () => finish();
    audio.onerror = () => finish(new Error("音频无法读取，素材已保留，请检查文件后重试。"));
    audio.src = url;
  });
}
