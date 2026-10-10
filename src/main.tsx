import React, { lazy, Suspense, useEffect, useState } from "react";
import ReactDOM from "react-dom/client";
import "./entry.css";

const Workbench = lazy(() => import("./Workbench"));
const PublicGallery = lazy(() => import("./PublicGallery"));

type SiteConfig = { previewOnly: boolean; githubUrl: string };

function SiteEntry() {
  const [config, setConfig] = useState<SiteConfig | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    const timeout = window.setTimeout(() => controller.abort(), 15000);
    setFailed(false);
    fetch("/api/site", { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error("站点暂时无法连接");
        const value = await response.json();
        if (typeof value.previewOnly !== "boolean") {
          throw new Error("站点信息无法读取");
        }
        if (active) setConfig(value);
      })
      .catch(() => {
        if (active) setFailed(true);
      })
      .finally(() => window.clearTimeout(timeout));
    return () => {
      active = false;
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [attempt]);

  const loading = (
    <div className="site-entry-status" role="status">
      <strong>映序</strong>
      <p>{failed ? "暂时无法连接站点，请稍后重试。" : "正在打开映序…"}</p>
      {failed && <button onClick={() => setAttempt((value) => value + 1)}>重新连接</button>}
    </div>
  );

  if (!config) return loading;
  return (
    <Suspense fallback={loading}>
      {config.previewOnly ? <PublicGallery githubUrl={config.githubUrl} /> : <Workbench />}
    </Suspense>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <SiteEntry />
  </React.StrictMode>,
);
