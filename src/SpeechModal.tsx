import { useEffect, useState } from "react";
import { AudioLines, Loader2, Mic, Plus, Settings2, X } from "lucide-react";
import { api } from "./api";
import { Modal } from "./Dialogs";
import { type Settings } from "./types";

export type SpeechResult = {
  url: string;
  name: string;
  type: string;
  duration: number;
  durationSeconds?: number;
  provider: string;
};

const voices = {
  local: [{ id: "cmn", name: "普通话" }],
  mimo: [
    { id: "mimo_default", name: "默认" },
    { id: "冰糖", name: "冰糖 · 女声" },
    { id: "茉莉", name: "茉莉 · 女声" },
    { id: "苏打", name: "苏打 · 男声" },
    { id: "白桦", name: "白桦 · 男声" },
    { id: "Mia", name: "Mia · 英语" },
    { id: "Chloe", name: "Chloe · 英语" },
    { id: "Milo", name: "Milo · 英语" },
    { id: "Dean", name: "Dean · 英语" },
  ],
  openai: [
    { id: "alloy", name: "alloy" },
    { id: "ash", name: "ash" },
    { id: "coral", name: "coral" },
    { id: "echo", name: "echo" },
    { id: "fable", name: "fable" },
    { id: "nova", name: "nova" },
    { id: "onyx", name: "onyx" },
    { id: "sage", name: "sage" },
    { id: "shimmer", name: "shimmer" },
  ],
};

export function SpeechModal({
  initialText,
  onClose,
  onAdd,
  onSettings,
}: {
  initialText: string;
  onClose: () => void;
  onAdd: (result: SpeechResult, text: string) => void;
  onSettings: () => void;
}) {
  const [text, setText] = useState(initialText);
  const [provider, setProvider] = useState<"local" | "mimo" | "openai">(
    "local",
  );
  const [voice, setVoice] = useState("cmn");
  const [speed, setSpeed] = useState(1);
  const [instructions, setInstructions] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [result, setResult] = useState<SpeechResult>();
  const [generatedText, setGeneratedText] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    api<Settings>("/api/settings", { signal: controller.signal })
      .then((settings) => {
        if (controller.signal.aborted) return;
        const selected = settings.ttsProvider || "local";
        setProvider(selected);
        setVoice(
          settings.ttsVoice ||
            (selected === "local"
              ? "cmn"
              : selected === "mimo"
                ? "mimo_default"
                : "alloy"),
        );
        setSpeed(settings.ttsSpeed || 1);
        setModel(settings.ttsModel || "");
      })
      .catch((cause) => {
        if (!controller.signal.aborted) setError((cause as Error).message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, []);
  const changeProvider = (next: typeof provider) => {
    setResult(undefined);
    setProvider(next);
    setVoice(
      next === "local" ? "cmn" : next === "mimo" ? "mimo_default" : "alloy",
    );
    setModel(
      next === "mimo"
        ? "mimo-v2.5-tts"
        : next === "openai"
          ? "gpt-4o-mini-tts"
          : "",
    );
    setError("");
  };
  const generate = async () => {
    if (!text.trim() || busy) return;
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const value = await api<SpeechResult>("/api/speech", {
        method: "POST",
        body: JSON.stringify({
          text,
          provider,
          voice,
          speed,
          model,
          instructions,
        }),
      });
      setResult(value);
      setGeneratedText(text);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title="生成旁白"
      icon={<Mic size={20} aria-hidden="true" />}
      onClose={onClose}
    >
      <div className="modal-body">
        {loading ? (
          <div className="loading-row">
            <Loader2 size={17} className="spin" />
            读取语音配置
          </div>
        ) : (
          <>
            <div className="form-field">
              <label htmlFor="speech-provider">语音来源</label>
              <select
                id="speech-provider"
                value={provider}
                onChange={(e) =>
                  changeProvider(e.target.value as typeof provider)
                }
                disabled={busy}
              >
                <option value="local">本地默认语音</option>
                <option value="mimo">小米 MiMo</option>
                <option value="openai">OpenAI 兼容接口</option>
              </select>
            </div>
            <div className="speech-fields">
              <div className="form-field">
                <label htmlFor="speech-voice">音色</label>
                <input
                  id="speech-voice"
                  list="speech-voices"
                  value={voice}
                  onChange={(e) => {
                    setVoice(e.target.value);
                    setResult(undefined);
                  }}
                  disabled={busy}
                />
                <datalist id="speech-voices">
                  {voices[provider].map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name}
                    </option>
                  ))}
                </datalist>
              </div>
              <div className="form-field">
                <label htmlFor="speech-speed">
                  语速 · {speed.toFixed(2)} 倍
                </label>
                <input
                  id="speech-speed"
                  type="range"
                  min={0.5}
                  max={2}
                  step={0.05}
                  value={speed}
                  onChange={(e) => {
                    setSpeed(Number(e.target.value));
                    setResult(undefined);
                  }}
                  disabled={busy}
                />
              </div>
            </div>
            {provider === "local" && (
              <div className="status-message">本地普通话 · 基础合成音色</div>
            )}
            {provider !== "local" && (
              <div className="form-field">
                <label htmlFor="speech-model">模型</label>
                <input
                  id="speech-model"
                  value={model}
                  onChange={(e) => {
                    setModel(e.target.value);
                    setResult(undefined);
                  }}
                  disabled={busy}
                />
              </div>
            )}
            <div className="form-field speech-text">
              <label htmlFor="speech-text">
                旁白文本<span>{text.length} / 3000</span>
              </label>
              <textarea
                id="speech-text"
                rows={5}
                value={text}
                maxLength={3000}
                onChange={(e) => {
                  setText(e.target.value);
                  setResult(undefined);
                }}
                disabled={busy}
              />
            </div>
            {provider === "mimo" && (
              <div className="form-field">
                <label htmlFor="speech-style">语气与风格</label>
                <textarea
                  id="speech-style"
                  rows={2}
                  value={instructions}
                  maxLength={1500}
                  onChange={(e) => {
                    setInstructions(e.target.value);
                    setResult(undefined);
                  }}
                  placeholder="温柔、自然，像讲述一段山野旅行"
                  disabled={busy}
                />
              </div>
            )}
          </>
        )}
        {error && (
          <div className="status-message error" role="alert">
            {error}
          </div>
        )}
        {result && (
          <div className="speech-result">
            <div>
              <AudioLines size={16} />
              <strong>旁白已生成</strong>
              <span>{(result.duration / 30).toFixed(1)} 秒</span>
            </div>
            <audio controls src={result.url} preload="metadata" />
            <a href={result.url} download className="speech-download">
              下载音频
            </a>
          </div>
        )}
      </div>
      <footer className="modal-footer">
        <button
          className="button secondary"
          onClick={onSettings}
          disabled={busy}
        >
          <Settings2 size={16} />
          语音设置
        </button>
        <button className="button secondary" onClick={onClose}>
          <X size={16} />
          关闭
        </button>
        {result ? (
          <button
            className="button primary"
            onClick={() => onAdd(result, generatedText)}
          >
            <Plus size={16} />
            添加至时间线
          </button>
        ) : (
          <button
            className="button primary"
            onClick={generate}
            disabled={!text.trim() || busy || loading}
          >
            {busy ? <Loader2 size={16} className="spin" /> : <Mic size={16} />}{" "}
            {busy ? "生成中" : "生成语音"}
          </button>
        )}
      </footer>
    </Modal>
  );
}
