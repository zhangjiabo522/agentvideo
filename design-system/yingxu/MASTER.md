# 映序工作台设计系统

## 定位

这是一个面向桌面浏览器的 AI 视频工程编辑器。界面服务于连续创作、检查和修订，不采用落地页的 Hero、营销卡片或装饰性插画结构。用户需要同时查看画布、图层、时间线和 Agent 反馈，因此采用密集但有明确分区的本地工作台。

## 检索依据与取舍

UI UX Pro Max 检索：`productivity dashboard minimal` 的设计系统结果确认了扁平、低装饰、图标化的生产力工具方向；`dashboard editor minimalism` 的 style 检索首个且唯一结果是有效的 `Minimalism & Swiss Style`，明确适用企业应用、Dashboard 和专业工具；`productivity dashboard` 的 product 检索首个结果为 Productivity Tool，强调清晰层级和功能性色彩。`keyboard focus modal` 的 UX 结果要求弹窗控件有可见焦点、焦点不得被遮挡；React 栈结果要求弹窗打开时管理焦点并在关闭时恢复焦点。

检索中的页面模式返回了 Product Demo 与 Hero Landing 结构，不适合编辑器，因此只采用其扁平视觉和交互原则。当前工作台采用白色和浅灰表面、黑色主文字、青绿色操作色；时间线用粉、蓝、绿区分内容类别。正文优先使用 Noto Sans SC，未加载字体时使用系统无衬线字体。

## 视觉令牌

| 用途 | 令牌 | 值 |
| --- | --- | --- |
| 页面背景 | `--color-canvas` | `#F4F6F5` |
| 工作台表面 | `--color-surface` | `#FFFFFF` |
| 主要文字 | `--color-ink` | `#18201E` |
| 次要文字 | `--color-muted` | `#65716D` |
| 线框 | `--color-line` | `#DCE3E0` |
| 主操作 | `--color-primary` | `#0F8F83` |
| 主操作文字 | `--color-on-primary` | `#FFFFFF` |
| 危险状态 | `--color-danger` | `#C83E4D` |
| 时间线文字 | `--timeline-pink` | `#E66B9B` |
| 时间线图片 | `--timeline-blue` | `#5F98D4` |
| 时间线图形 | `--timeline-green` | `#62A486` |
| 键盘焦点 | `--focus-ring` | `#0F8F83` |

所有主要文字保持至少 4.5:1 对比度。图标来自 Lucide React，不使用 Emoji 作为结构图标。

## 排版与密度

- 字体：`Noto Sans SC`, `-apple-system`, `BlinkMacSystemFont`, `"Segoe UI"`, sans-serif。
- 正文最小 14px，辅助文字不小于 12px；正文行高 1.5。
- 使用 4px / 8px 间距节奏。工作台栏间距 8–16px，弹窗内分组间距 16–24px。
- 常规按钮和图标按钮的可操作区域至少 40px；移动或触屏查看时至少 44px。
- 圆角集中在 4px，弹窗可使用 8px；避免多层卡片套卡片。
- 仅使用颜色、边框和透明度变化表达悬停与按下状态，不能通过缩放改变布局。

## 弹窗与表单

- 使用原生 `dialog`，通过 `showModal()` 获得背景阻塞和 Escape 行为。
- 打开时保留触发控件，关闭时将焦点还给触发控件；弹窗标题使用 `aria-labelledby`。
- 每个输入框都有可见 label、合理的 autocomplete 和中文错误反馈。
- 错误紧贴相关操作显示，并使用 `role=alert`；成功反馈使用 `role=status`。
- 长内容（工程 JSON）必须在内部滚动，不能撑破视口。
- `prefers-reduced-motion: reduce` 时移除非必要过渡和旋转动画。

## 组件类名

主应用可为这些结构提供 CSS：`modal`、`modal-wide`、`modal-heading`、`modal-body`、`modal-footer`、`modal-tabs`、`form-grid`、`form-field`、`form-section`、`field-label-row`、`model-picker`、`field-caption`、`status-message`、`status-message error`、`status-message success`、`aspect-options`、`aspect-option`、`aspect-option active`、`aspect-preview landscape`、`aspect-preview portrait`、`aspect-preview square`、`export-summary`、`export-progress`、`progress-track`、`progress-fill`、`source-code`、`loading-row`、`spin`。原生 `dialog::backdrop` 提供遮罩。

## 响应式边界

完整编辑优先支持桌面宽度 1024px 以上。宽度 768px 以下，弹窗改为接近全屏并保持 footer 可见，工程 JSON 继续内部滚动。宽度 375px 仍需避免横向滚动，比例选择项可以换行。

## 实施状态

本文件是工程实现约束，不代表所有页面已经完成验收。完成新页面后需要在 375、768、1024 和 1440px 宽度检查焦点可见性、错误状态、长文本和 reduced motion。

## 暗色模式

UI UX Pro Max 的 `dark mode contrast` UX 检索首个结果为 Contrast Readability，第二个结果为 Color Contrast，适用本工作台的文字与输入。采用独立暗色色阶，同时遵循 quick-reference 中 `color-dark-mode` 与 `color-accessible-pairs`：降低表面亮度、提高文字亮度，分别验证明暗主题的对比度，不反转素材颜色。

| 用途 | 暗色值 |
| --- | --- |
| 编辑区域背景 | `#101713` |
| 工作台表面 | `#19221C` |
| 较高表面 | `#222E25` |
| 输入表面 | `#131C16` |
| 主要文字 | `#E6EEE9` |
| 次要文字 | `#ADBCB2` |
| 分区线框 | `#34453A` |
| 主操作与焦点 | `#7DD3AE` |
| 主操作文字 | `#13271C` |
| 错误文字 | `#FFB6AE` |

默认跟随系统色彩偏好。顶部月亮或太阳按钮即时切换，中文名称描述下一步操作。手动选择存为 `yingxu-theme`，刷新及不同窗口保留；没有手动选择时继续响应系统变化。根元素通过 `data-theme` 和 `color-scheme` 表达主题，让原生下拉、音频控件与滚动条同步。

暗色覆盖工作台、时间线、属性、聊天、弹窗、表单、成功与错误状态。`SceneView` 中的镜头背景、文字、图形及图片维持工程原始颜色；禁用对整个页面或画布施加反色、亮度滤镜等主题转换。
