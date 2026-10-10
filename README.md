# 映序

让 AI 调用工具制作视频，人在中文工作台实时观看、提出修改要求，并在需要时接管。后端使用 Go，前端使用 React。场景、文字、图片、图形、图表与音频保存在统一工程数据中，浏览器预览和 MP4 导出共用渲染组件，不依赖视频生成模型。

AI 有两种接入方式：在工作台使用已配置的文本模型进行多轮工具调用，或通过本机 HTTP MCP 服务让外部 AI 操作工程。两者使用同一套剪辑工具与校验规则。接入步骤、工具清单和请求示例见 [AI 接入说明](docs/AI接入.md)。

公开视频预览：[llmvideo.jx.fyi](https://llmvideo.jx.fyi)。源码：[GitHub 仓库](https://github.com/zhangjiabo522/agentvideo)。预览站展示已导出视频，支持播放、拖动进度和下载；剪辑功能在本机工作台使用。

## 公开预览部署

设置 `VIDEO_PREVIEW_ONLY=1` 后，服务只提供公开作品页、站点配置、公开视频清单，以及清单中明确列出的 MP4 和封面。公开视频支持分段读取，浏览器可以拖动播放进度。预览模式不读取模型设置和密钥，不初始化 AI 剪辑、MCP、工程编辑或导出任务；MCP、模型调用、工程、设置、原素材及写入接口均关闭。

公开数据单独放在 `public-data`，只复制允许公开的成片和封面到其 `exports` 目录。不要把本机整个 `data` 目录上传到预览站。服务从 `public-data/public-videos.json` 读取作品清单，更新清单后需要重启；清单缺失时显示空作品列表，清单格式错误时服务拒绝启动。

```json
[
  {
    "id": "yingxu-demo",
    "title": "映序 AI 视频宣传片",
    "description": "AI 制作视频的实际导出作品。",
    "url": "/exports/yingxu-demo.mp4",
    "poster": "/exports/yingxu-demo.jpg",
    "duration": 33.1,
    "width": 1280,
    "height": 720,
    "createdAt": "2026-10-10T12:00:00+08:00",
    "featured": true
  }
]
```

`duration` 使用秒，尺寸使用像素，`createdAt` 使用带时区的日期时间。`url` 和 `poster` 必须是 `/exports/` 下的单层文件名；视频只允许 `.mp4`，封面只允许 `.png`、`.jpg`、`.jpeg`、`.webp`，无封面时可填写空字符串。未列出的文件、目录浏览和越界路径都不会提供。

服务器按版本目录部署：

```text
/opt/yingxu-preview/
  current -> releases/版本号
  releases/
    版本号/
      yingxu
      dist/
      public-data/
        public-videos.json
        exports/
  acme/
```

先在构建机器运行 `npm ci`、`npm run build` 和 `go build -o yingxu ./cmd/server`。构建环境需要与服务器的操作系统和处理器架构一致，或使用 Go 交叉编译。公开预览进程不需要 Node.js、FFmpeg、Chromium 或模型密钥。

把程序、`dist`、独立的 `public-data` 上传到一个新的版本目录，目录和文件归管理员所有，给予服务用户读取权限。首次安装时创建专用用户、证书验证目录，并安装服务模板：

```bash
useradd --system --user-group --home-dir /nonexistent --shell /usr/sbin/nologin yingxu-preview
install -d -m 0755 /opt/yingxu-preview/releases /opt/yingxu-preview/acme
install -m 0644 deploy/yingxu-preview.service /etc/systemd/system/yingxu-preview.service
systemctl daemon-reload
```

切换 `current` 前确认版本目录已完整上传；替换下列 `20261010-120000` 为实际版本号：

```bash
ln -s /opt/yingxu-preview/releases/20261010-120000 /opt/yingxu-preview/current.next
mv -Tf /opt/yingxu-preview/current.next /opt/yingxu-preview/current
systemctl enable --now yingxu-preview
systemctl restart yingxu-preview
curl --fail http://127.0.0.1:18090/api/health
```

服务由专用用户运行，只监听 `127.0.0.1:18090`，使用只读文件系统限制。使用独立 Nginx 站点反向代理，不改变服务器已有站点。域名 `llmvideo.jx.fyi` 的 DNS 应指向部署服务器，HTTP 证书验证目录为 `/opt/yingxu-preview/acme`，证书放在 `/etc/letsencrypt/live/llmvideo.jx.fyi/`。首次签发证书时先启用模板中的 HTTP 验证配置，取得证书后再安装完整 HTTPS 配置：

```bash
certbot certonly --webroot -w /opt/yingxu-preview/acme -d llmvideo.jx.fyi --deploy-hook "/www/server/nginx/sbin/nginx -t && /www/server/nginx/sbin/nginx -s reload"
install -m 0644 deploy/llmvideo.jx.fyi.conf /www/server/panel/vhost/nginx/llmvideo.jx.fyi.conf
nginx -t
nginx -s reload
```

证书续期也使用同一验证目录，续期成功后重新加载 Nginx。更新应用或视频时上传新版本目录，原子切换 `current` 并重启服务，再检查首页、公开视频列表、视频分段读取及 `/mcp` 的拒绝响应；回滚时将 `current` 切回保留的上一版本并重启。服务日志使用 `journalctl -u yingxu-preview` 查看，Nginx 日志放在 `/opt/yingxu-preview/`。

## 启动

需要 Go 1.22 或更新版本、Node.js 22、FFmpeg，以及 Chromium。

```bash
npm install
npx playwright install chromium
npm run start
```

打开 [本机工作台](http://127.0.0.1:8080)。已安装的 Go 不在默认路径时，可通过 `GO_PATH` 指定其可执行文件。`VIDEO_ADDR` 可以修改监听地址，例如 `127.0.0.1:8081`。项目数据默认保存在 `data`，由 `VIDEO_DATA` 修改；`CHROME_PATH` 可指定已有 Chromium。

本地基础 TTS 使用 espeak-ng，可生成可导出的普通话 WAV，声音为机械音。Linux 系统已有 espeak-ng 时无需额外配置；当前 Debian/Ubuntu 环境可以运行以下免管理员权限安装命令。其他环境需自行安装 espeak-ng，并可用 `LOCAL_TTS_PATH` 指定程序。

```bash
npm run setup:tts
```

## 让 AI 开始剪辑

在“AI 剪辑导演”中描述目标，例如“制作一支 30 秒的映序宣传片，展示 AI 自动剪辑，加入中文旁白并导出”。内置 AI 使用模型设置中的文本服务，模型需要支持标准工具调用。没有模型配置会立即提示；只返回文字、不调用工具的模型不会被当作剪辑成功。

外部 AI 的连接地址为 `http://127.0.0.1:8080/mcp`，连接类型选择 HTTP。可在“连接外部 AI”窗口取得当前工程编号和配置，也可参考 [MCP 配置示例](docs/mcp-config.json)。把工程编号交给 AI，要求它先读取工程，再逐步编辑、预览和导出。映序当前仅支持本机 MCP 连接。

工作台默认处于“AI 剪辑 · 观看”：镜头、图层、音频和执行步骤随服务端事件实时更新。需要手动修改时点击“人工接管”；内置 AI 正在运行时先停止任务。开始新 AI 任务前会保存人工修改。停止 AI 保留已经提交的剪辑，已经提交的导出任务需单独取消。

顶部月亮或太阳按钮切换亮色、暗色模式，选择保存在当前浏览器；首次使用跟随系统主题。界面主题不会改变工程颜色或导出画面。

## 已有能力

- 新建横屏、竖屏、方形工程，镜头增删、复制与排序。
- 文字、图片、矩形、圆形和柱状图；选择、移动、缩放、旋转、颜色、显示时间与基础动画编辑。
- 时间线片段可拖动和左右裁剪。镜头调整顺序与时长，图层调整显示区间，音频调整位置与源素材截取区间。
- 音频上传、旁白生成、音量、淡入淡出、预览和导出混音。
- 本地 TTS、小米 MiMo 预置音色与音色设计、OpenAI 兼容 `/audio/speech` 接口。
- 文本、生图与 TTS 独立配置，并通过供应商 `/models` 获取模型列表；不支持该接口的服务仍可手动填写模型名称。
- AI 通过工具读取工程、批量编辑镜头和图层、移动裁剪音频、生成并放置图片与旁白、定位预览和导出。
- MCP、HTTP 工具调用与内置多轮 Agent 共用校验和版本控制；每次有效修改立即保存，并通过服务端事件流推送。
- 内置任务状态、步骤、取消与重启中断记录；每个工程同时一个内置任务，最多 20 轮模型调用、40 次工具调用、15 分钟。
- 锁定图层和手动属性受保护；版本冲突后 AI 重新读取工程修正，不覆盖已经提交的修改。
- 撤销重做、自动保存、本地恢复、版本冲突检测、JSON 与含素材 ZIP 工程包导入导出。
- 真实 H.264 MP4 导出、AAC 音频、阶段与帧数进度、预计剩余时间、取消与失败重试。

模型密钥由后端加密保存，设置响应和工程包不包含密钥。修改服务地址或 TTS 供应商后需重新填密钥；同地址留空保留原密钥。

导出任务在后台运行，关闭窗口或刷新页面后，再次打开“导出视频”可恢复当前工程的任务并下载结果。排队时显示队列位置；渲染时显示帧数、耗时和预计剩余时间；连接中断会提示并自动重连。视频按原始帧率逐帧编码，静止画面复用无损截图，动画及图层出现、消失仍按工程帧号渲染。图片、截图和编码等待均有截止时间，连续 2 分钟无进展会终止异常渲染，总导出上限为 30 分钟。取消排队会立即释放位置。

## 配音接入

打开模型设置的“语音模型”页签，选择语音来源。小米 MiMo 默认地址为 `https://api.xiaomimimo.com/v1`，模型为 `mimo-v2.5-tts`，默认音色为 `mimo_default`。也支持冰糖、茉莉、苏打、白桦等官方预置音色。`mimo-v2.5-tts-voicedesign` 需要填写音色或风格描述，当前未实现音色克隆入口。

MiMo 按[官方文档](https://mimo.mi.com/docs/zh-CN/quick-start/usage-guide/audio/speech-synthesis-v2.5)调用 `/chat/completions`，将旁白放入 `assistant` 消息，读取 Base64 WAV。OpenAI 兼容来源调用 `/audio/speech` 并接收音频文件。

人工配音入口支持先试听再加入时间线；AI 的 `video_speech_generate` 工具会实际合成并自动加入音轨，必要时延长最后一个镜头，随后由 AI 根据真实旁白时长调整画面。自动延长仍需遵守工程上限和受保护内容约束。

## 验证

```bash
go test -race ./...
go vet ./...
npm run build
npm run test:browser
npm run test:export
```

浏览器验收需要已启动的本地服务，报告与截图保存在 `test-results`。可通过 `TEST_ORIGIN` 指定其他端口。验收使用独立工程，模型请求用测试响应替代，避免发起付费调用；后端协议测试也使用独立测试服务。

导出验收会启动独立临时服务，验证动画、镜头切换、静止帧复用、音频裁剪与混合，以及取消、素材缺失和编码器异常时的任务清理。运行需要 Go、FFmpeg 与 Chromium，结果保存在 `test-results/export-verification.json`。

内置 Agent 的多轮工具执行、校验修复、取消、重复任务、工程隔离、循环上限、重启恢复和宣传片导出提交已通过模拟集成及竞态测试。当前 MCP 外部客户端实际连接、新版实时观看与人工接管、暗色模式和真实模型宣传片仍需完成端到端验收，不能用早期手动编辑测试替代这些证据。

## 当前范围

这是可运行的首版，尚未实现完整 PRD 的多人协作、历史版本工作台、自由代码编辑、复杂三维、逐词字幕对齐、自动语音识别和专业视频素材剪辑。镜头目前连续排列，拖动主体调整顺序；图层在所属镜头内移动，音频在全局时间线上移动。镜头缩短会裁剪所属图层显示区间及超过工程结尾的音频片段，完全越界的片段会移出时间线，可撤销恢复；源图片与音频文件保留。

建议工程范围为最多 120 秒、20 个镜头、300 个视觉对象、12 条音轨。视频按工程原始尺寸导出，最大 1920 像素；不同机器的渲染速度需要实测。MiMo、OpenAI 和生图服务的真实效果取决于用户配置，现有验证不代表已使用真实密钥测试所有供应商。

示例风景图片来自 Unsplash，随工程以素材形式保存。详细规划见 [产品需求文档](docs/PRD.md)，实现记录见 [实现说明](docs/实现说明.md)。
