export async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort(options?.signal?.reason);
  const headers = new Headers(options?.headers);
  if (!(options?.body instanceof FormData) && options?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (options?.signal?.aborted) abort();
  else options?.signal?.addEventListener("abort", abort, { once: true });
  const uploadRequest = options?.body instanceof FormData;
  const timeoutMs = uploadRequest ? 15 * 60 * 1000 : /\/api\/(images\/generate|speech|agent)$/.test(path) ? 200000 : 30000;
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(path, { ...options, headers, signal: controller.signal });
    if (response.status === 204) return undefined as T;
    const value = await response.json().catch(() => {
      throw new Error(response.ok ? "服务返回的数据格式异常，请刷新后重试。" : `服务暂时不可用（${response.status}），请稍后重试。`);
    });
    if (!response.ok) {
      throw new Error(typeof value?.error === "string" ? value.error : `请求失败（${response.status}）`);
    }
    return value as T;
  } catch (error) {
    if (options?.signal?.aborted) throw error;
    if (controller.signal.aborted) throw new Error(uploadRequest ? "上传等待超时，请检查网络，并在素材库或项目列表确认结果后重试。" : "请求等待超时，请重试；已提交的后台任务可重新打开查看。");
    if (error instanceof TypeError) throw new Error("无法连接服务，请检查网络或确认服务已启动。");
    throw error;
  } finally {
    window.clearTimeout(timeout);
    options?.signal?.removeEventListener("abort", abort);
  }
}

export function downloadJSON(value: unknown, name: string) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
