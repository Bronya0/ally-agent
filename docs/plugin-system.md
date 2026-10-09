# Ally 插件开发指南（v1）

> 插件 = 一个 zip 包。装进来后侧栏多一个整页，页面用纯 JS/TS 写，只能通过宿主注入的 `host` 对象触达后端能力（HTTP / 存储 / 工作区只读 / 事件 / 主题），不用写 Go、不用打包、不用重启应用。
>
> **要让 AI 帮你写插件，就把这份文档的链接丢给它**：https://github.com/Bronya0/ally-agent/blob/main/docs/plugin-system.md
> 最容易错的是三处：清单字段、权限白名单、样式作用域——照训练数据里「浏览器扩展 / 油猴脚本」的经验瞎写，十有八九踩在这三处。
>
> 本文档只讲**怎么写一个插件**。插件系统自身的实现（加载器、资源中间件、权限判定代码在哪）不在这里维护，要看就去看代码。

---

## 1. 五分钟上手

### 1.1 最小骨架

```
my-plugin/
├── plugin.json    清单（只写下面那 10 个键，见 §2.2）
├── index.js       入口：export function mount(element, host)
└── style.css      可选
```

```js
// index.js
export async function mount(element, host) {
  // 自己的资源用自己的模块地址取：同源 + 相对路径，不需要额外 API
  const css = await fetch(new URL('./style.css', import.meta.url)).then((r) => r.text());
  const tag = document.createElement('style');
  tag.textContent = css;
  document.head.appendChild(tag);

  element.innerHTML = '<div class="my-root">…</div>';
  // 干正事：host.http / host.store / host.files（按清单声明的权限才有）

  return () => {
    tag.remove(); // ← 卸载函数，别漏
  };
}
```

`mount` 的返回值就是卸载函数，它是页面切走 / 重新导入时**唯一**的清理时机：取消订阅、清定时器、摘掉自己插进去的 `<style>`。

### 1.2 清单

```json
{
  "id": "my-helper",
  "name": "工单助手",
  "version": "1.0.0",
  "author": "twh",
  "description": "团队工单的查询与增删改",
  "entry": "index.js",
  "menu": [{ "key": "helper", "title": "工单", "icon": "ApiOutlined" }],
  "permissions": { "http": ["api.example.com", "*.example.com"], "workspace": "read" }
}
```

每个字段见 §2.2。

### 1.3 装进去

侧栏「插件」是管理页：

| 动作 | 说明 |
|---|---|
| 导入插件 | 选一个 zip（也支持拖拽到落区） |
| 从目录安装 | 选一个已展开的插件目录。校验与 zip **完全同一套**（给「让 AI 生成源码再装」用） |
| 导出 | 默认只导包本体；勾上「包含数据」才会把 `host.store` 那份一起打进包里 |
| 删除 | 二次确认，并单独问一次「数据要不要一起删」 |
| 启停 | 禁用 = 菜单消失、进不去；重新启用即回来 |

两条铁律：

1. **改完必须重新导入**才生效——模块 URL 带缓存戳，不重新导入就一直跑旧代码（见 §3.1）。
2. **同 id 重复导入 = 覆盖升级**：`host.store` 里的数据保留，旧版本留一份 `.old` 备份。

仓库里有一个可直接跑的示例插件：`internal/tools/plugin/testdata/demo-plugin/`（它同时是受测 fixture），想看完整写法可以先把它当参照。

---

## 2. 插件包规范

### 2.1 目录

```
my-helper-1.0.0.zip
├── plugin.json     必需，清单
├── index.js        必需，入口（也可以多文件，由入口负责 import）
├── style.css       可选
└── assets/*        可选
```

- 包根就是插件根。用户把整个文件夹打包（多一层 `my-helper-1.0.0/`）也认，安装时会剥掉这一层；但**入口与 `plugin.json` 必须在同一层**。
- 资源文件后缀必须在白名单里，否则请求会被拒（403）。允许的 21 种：

| | | |
|---|---|---|
| `.js` | `.mjs` | `.css` |
| `.json` | `.map` | `.html` |
| `.svg` | `.png` | `.jpg` |
| `.jpeg` | `.gif` | `.webp` |
| `.ico` | `.woff` | `.woff2` |
| `.ttf` | `.otf` | `.txt` |
| `.md` | | |

  `.wasm`、`.vue`、无后缀一律不行——要编译产物就先在包外编好，只把 `.js` / `.css` 放进去。

### 2.2 `plugin.json` 的 10 个键

**只认这 10 个**：`id` / `name` / `version` / `author` / `description` / `entry` / `menu` / `permissions` / `keepAlive` / `group`。解析是严格模式（`DisallowUnknownFields`），**多写任何一个键都会直接报错、装不上**，错误显示在管理页。

| 字段 | 规则 |
|---|---|
| `id` | `^[a-z0-9][a-z0-9._-]{1,48}$`。同时用作目录名、一级菜单 key（`plugin:<id>`）与数据文件名 |
| `name` | 展示名，不能为空 |
| `version` | `X.Y.Z`，仅用于展示与升级提示（不做多版本并存） |
| `author` / `description` | 可选，纯展示 |
| `entry` | 包内相对路径，必须是文件；不允许绝对路径或 `..` |
| `menu` | **必须恰好一项**（写两项、写空数组都校验不过）；`title` 必填；`icon` 见 §2.3 |
| `permissions` | 见下 |
| `keepAlive` | 可选布尔，默认 `true`。`true` = 切走只隐藏（状态留住）；`false` = 切走即销毁，下次进来重新 `mount`。见 §3.2 |
| `group` | 可选对象 `{ "key": "team", "title": "团队", "icon": "GlobalOutlined" }`：把页面挂到侧栏某个**一级菜单**下。`key` 是身份（同 key 即同组，`title` ≤ 24 字）。不写就自己占一个顶层图标 |

`menu[0].key` **在 v1 里没有任何判定使用它**（真正的一级菜单 key 是宿主拼的 `plugin:<id>`；页面内部的二级导航由插件自己在页面里实现）。写了不报错，但别指望它参与路由。

`permissions`：

| 键 | 取值 | 说明 |
|---|---|---|
| `http` | 主机名数组 | 白名单，`*.example.com` 匹配子域；匹配的是**实际连接的主机名**，重定向后的目标同样复判。没声明就谁也连不上（调用时返回 `E_PLUGIN_HOST_DENIED`） |
| `workspace` | `none` \| `read` | 默认 `none`。**v1 写 `write` 会在校验阶段被直接拒掉**（`host.files` 还没有写能力：给个假权限不如不给） |

**同组的展示信息以「先出现的那份」为准**（插件彼此独立，宿主不做汇总）。"先出现"是可预期的：插件列表按目录名（= 清单 id）排序，所以**同组里 id 最小的那个说了算**。想让菜单名稳，就把同组所有插件的 `title` / `icon` 写成完全一样。

### 2.3 菜单图标白名单

`icon` 只传名字（字符串），组件由宿主渲染——插件不能往宿主组件树里塞东西。名字不在名单里**不会报错**，只是静默回退成默认图标（`AppstoreOutlined`），写错了只能从图标看出来。

| | | | |
|---|---|---|---|
| `ApiOutlined` | `AppstoreOutlined` | `BarChartOutlined` | `BugOutlined` |
| `CloudServerOutlined` | `ClusterOutlined` | `CodeOutlined` | `DatabaseOutlined` |
| `DeploymentUnitOutlined` | `FileTextOutlined` | `GlobalOutlined` | `ProjectOutlined` |
| `RobotOutlined` | `SettingOutlined` | `ThunderboltOutlined` | `ToolOutlined` |

### 2.4 校验、上限与安装布局

装的时候逐条校验：清单存在且可解析 → `id` 合法 → `name` / `version` / `entry` 合法 → `menu` 恰好一项且图标名形状合法 → 权限字段合法 → 入口文件在包里。

体积上限（按**盘上真实字节**复验，不只信 zip 头声明的数字）：

| 项 | 上限 |
|---|---|
| 包本体（zip 文件） | 32 MiB |
| 解压后总字节 | 32 MiB |
| 单个文件 | 8 MiB |
| 包内条目数 | 512 |
| `plugin.json` | 64 KiB |
| `host.store` 的数据文件 | 1 MiB |

安装后的磁盘布局（**数据不住在插件目录里**，否则「删插件保留数据」无法成立）：

```
~/.ally_agent/plugins/
├── my-helper/                插件本体（= 包内容）
│   ├── package.zip           导入时的原始包（导出 = 原样复制，逐字节一致）
│   ├── plugin.json
│   ├── index.js
│   └── style.css
├── .data/my-helper.json      插件自己的数据（点开头，不会被当成插件扫描到）
└── .old/my-helper/           上一版本体（覆盖升级时留的备份）
```

包根带 `data.json`（导出时勾了「包含数据」）时，导入会把它搬到 `.data/<id>.json`，不会留在插件目录里当资源被服务出去。

---

## 3. 入口加载与页面生命周期

### 3.1 模块 URL 与缓存戳

插件资源与页面**同源**（`/plugins/<id>/<entry>`），所以入口用原生动态 `import` 加载，多文件、相对导入、CSS、图片全都能用：

```js
new URL('./style.css', import.meta.url); // 同源相对路径，直接用
```

两个要知道的点：

- **模块 URL 上带缓存戳，而且它落在目录段**：`/plugins/<id>/@<插件目录更新时间>/index.js`。这样插件内部 `import './util.js'` 解析出来的子模块会自动继承同一个戳，整棵树才真的换新（放查询串的话子模块继承不到，重新导入后仍跑旧代码）。
- **别 import 生成的 bindings**：`frontend/bindings/...` 在生产包里不存在。后端能力只能从 `host` 拿——这也是插件不随后端重构而碎掉的原因。

### 3.2 `keepAlive` 与 `shown` / `hidden`

| 声明 | 切走时 | 回来时 |
|---|---|---|
| 不写 / `true`（默认） | 只隐藏（v-show），插件自己的状态、滚动位置都在 | 原样露出 |
| `false` | 销毁页面，随后调用 `mount` 返回的清理函数 | 重新 `mount`（每次进来都是全新的） |

两种情况下插件都会收到 `shown` / `hidden` 两个事件：

```js
const off = host.events.on('hidden', () => stopPolling());
host.events.on('shown', () => startPolling());
```

两条实践要点：

1. 定时器 / 轮询 / 长连接要在 `hidden` 里停掉；不想操心生命周期就声明 `"keepAlive": false`，把销毁交给宿主。
2. **`mount()` 返回之后，宿主会立刻按当前可见性发一次 `shown` 或 `hidden`**。所以如果 `mount` 里已经拉过数据，`shown` 里就别再无条件拉一次（会双请求）——用一个"正在刷新"标志或时间戳兜住。

不管选哪种，这三种情况都会重来：重新导入（覆盖升级）会重挂、插件被禁用或删除会卸载、重启应用。另外**没打开过的插件一个组件都不渲染**（懒加载）。

---

## 4. 宿主 API（`host`）

### 4.1 类型

```ts
interface Host {
  app:   { version: string; locale: string; workspace: string };
  http:  { request(req: {
             method?: string; url: string; headers?: Record<string,string>;
             query?: Record<string,string>; body?: string; json?: unknown;
             timeout?: number; maxBytes?: number }): Promise<HttpResult> };
  store: { get(): Promise<unknown>; set(value: unknown): Promise<void> };
  files: { read(p: string): Promise<string>;
           list(p: string, options?: { maxDepth?: number; limit?: number; includeHidden?: boolean; includeIgnored?: boolean }): Promise<{ path: string; name: string; dir: boolean }[]>;
           stat(p: string): Promise<WorkspaceFileInfo>;
           mediaUrl(p: string): Promise<string> };        // 图片/视频/PDF 预览，直接塞进 src
  open:  { inFileManager(p: string): Promise<void> };
  events:{ on(name: string, cb: (payload: unknown) => void): () => void };  // 只有 theme / shown / hidden
  log:   { info(msg: string): void; error(msg: string): void };
  ui:    { notify(msg: string, type?: 'info'|'success'|'warning'|'error'): void;
           theme: { snapshot(): ThemeSnapshot;      // { name, mode, tokens }
                    tokens(): string[];             // 公开 token 的名字数组
                    on(cb: (t: ThemeSnapshot) => void): () => void } };
}

interface ThemeSnapshot { name: string; mode: 'dark' | 'light'; tokens: Record<string, string> }

// http.request 的返回
interface HttpResult {
  method: string; url: string; finalUrl: string;
  status: number; statusText: string; headers: Record<string, string>;
  contentType: string;
  body?: string; bodyBase64?: string; bodyEncoding: 'text' | 'base64';
  json?: unknown; jsonPreview?: string; jsonTruncated?: boolean;
  bytesRead: number;
  truncated: boolean;   // 超过 maxBytes 被截断；此时 json 一定为空（截断的 JSON 解析不出来），
                        // 别把"没有 json"当成"对方返回了空"
  durationMs: number; redirects?: string[]; savedPath?: string;
}
```

要点：

- `host` 是**按该插件权限现构造**的：没声明的能力直接不存在（拿不到引用）。但 v1 只有 `files` / `open` 走这条规则（两者都要求 `permissions.workspace` 声明到 `read`）；`host.http` 是一直挂着的——没声明 `permissions.http` 时是**调用时**返回 `E_PLUGIN_HOST_DENIED`。所以判断"这插件能不能联网"要看清单，不能拿 `if (host.http)` 做特性检测（会得到假阳性）。
- `host.store` 是插件**唯一**能落盘的地方：数据落在 `<appData>/plugins/.data/<id>.json`（0600、≤ 1 MiB、内容必须是合法 JSON）。`get()` 没数据时返回 `null`。**凭据只能放这里**，别写进代码或包——导出勾了「包含数据」会把它一起打出去。
- `host.log.info` 与 `host.log.error` 走的是同一条前端错误日志通道（**都算错误级别**），插件里分不出级别：能用，但别指望靠它分级。

### 4.2 能碰到的后端绑定

插件能触达的桥面一共 16 个，全在这里：

| 绑定 | 作用 |
|---|---|
| `ListPlugins()` | 已安装清单：id / 名称 / 版本 / 作者 / 包大小 / 更新时间 / 启用态 / 权限摘要 / 清单错误 |
| `SelectPluginPackage()` | 原生文件框选 zip，返回路径（取消返回空串） |
| `ImportPlugin(srcPath)` | 从 zip 安装 |
| `ImportPluginFromDir(dir)` | 从工作区目录安装（给「AI 生成插件」闭环用） |
| `ExportPlugin(id, includeData)` | 导出（无数据时直接复制原始包，逐字节一致） |
| `DeletePlugin(id, purgeData)` | 删插件本体；数据单独问一次 |
| `SetPluginEnabled(id, enabled)` | 启停 |
| `PluginHTTPRequest(req)` | HTTP 代理（目标主机必须命中该插件声明的白名单） |
| `PluginStoreGet(id)` / `PluginStoreSet(id, value)` | 读写插件数据文件 |
| `ReadWorkspaceFileAt` / `ListFiles` / `GetWorkspaceFileInfoAt` / `GetWorkspaceMediaURL` | `host.files`（要求 `workspace: read`） |
| `OpenPathInFileManager` | `host.open.inFileManager`（同上） |
| `LogFrontendError` | `host.log` |

`host.files` 的路径在宿主层就被收窄成**工作区内的相对路径**：空串、结对路径与任何一段 `..` 直接拒绝（后端那几个绑定对绝对路径是放行的，它们服务的是编辑器语义）。

### 4.3 权限与信任边界

- **默认拒绝**：未声明的主机、未声明的工作区写，一律拒绝并给出明确错误码；被拒的请求会记一笔审计（管理页按插件展示）。
- **安装不弹权限确认框**：按声明静默放行。声明内放行、声明外一律拒绝。管理页的权限摘要与被拒记录是用户唯一的事后感知手段。
- **`host` 不是沙箱**：插件 JS 跑在主页面上下文（同 origin、同 localStorage、同后端桥），后端调用入口挂在全局上，也就是说**插件能绕开 `host` 直接调任意后端绑定**。真正硬的边界只有一条：后端 HTTP 白名单（按插件身份判、默认拒绝、重定向目标复判）。管理页那句权限摘要是「这个插件声明要什么」的**留痕**，不是「它被限制在什么范围」的保证——别把自己的插件写成依赖"拿不到就会失败"的形状，也别对用户承诺这层隔离。

### 4.4 明确不给的能力（每条都有理由）

| 不给 | 理由 |
|---|---|
| `GetConfig` / `SaveConfig` | 配置里含用户的模型密钥池 |
| 会话与模型（`StartChat` / `SaveSession` / `SwitchModel` …） | 属于用户主流程，v1 不给 |
| SSH 全套 | 服务器凭据与授权闸门，最高敏感面 |
| 宿主生命周期（`ApplyUpdate` / `SetWindow` / `QuitForUpdate` …） | 内部接线 |
| 改全局配置（`SaveSkill` / `SaveMcpConfig` / `SaveApiSettings` …） | 同上 |
| 定时任务与服务（`ListScheduledTasks` / `StopService` …） | 避免插件在用户背后起后台任务 |
| 打开外部链接（`host.open.url`） | v1 没有这个 API，见 §8 第 1 条 |
| 写工作区 | `host.files` 只读（`workspace: "write"` 装不上） |
| 弹确认框 / 弹窗 | 只有 `host.ui.notify` |
| 多页面 | 一个插件一个页面，页面内部的二级导航自己实现 |

---

## 5. 样式与主题

插件页渲染在**同文档**的 DOM 里，主题变量挂在 `<html>` 上，所以第一层同步是零成本的。

### 5.1 直接用 CSS 变量

`data-theme`：`amber`（默认，不带属性）/ `icecream` / `ocean` / `forest` / `violet`；`data-mode`：`dark`（默认，不带属性）/ `light`。

插件只要写 `var(--ally-*)`，宿主切主题、切明暗时变量自动重算，**插件一行代码都不用写**。

### 5.2 需要具体色值时才用 API

图表（echarts）、canvas、内联 SVG 这类必须拿到色值：

- `host.ui.theme.snapshot()` → `{ name, mode, tokens }`。`name` / `mode` **只在这个快照里**（`host.ui.theme.name` 是 `undefined`）。
- `host.ui.theme.tokens()` → 公开 token 的名字数组。
- `host.ui.theme.on(cb)` → 变化回调，`cb` 收到新快照（宿主用属性观察实现，不是轮询）。

### 5.3 公开 token 契约

只能依赖下面这份**公开子集**，表外的 `--ally-*` 一律当内部实现，随时可能改名：

| 类别 | 公开变量 |
|---|---|
| 表面 | `--ally-surface-content` / `-panel` / `-chrome` / `-raised` / `-deep` |
| 文字 | `--ally-text-primary` / `-high` / `-body` / `-secondary` / `-soft` / `-tertiary` / `-muted` / `-faint` / `-ghost` |
| 语义 | `--ally-success`(`-text`) / `--ally-danger`(`-text`) / `--ally-warning`(`-text`) / `--ally-info` |
| 强调 | `--ally-accent` / `--ally-accent-ink` / `--ally-accent-bright` / `--ally-accent-strong` |
| 悬停与边框 | `--ally-hover-faint` / `--ally-hover-strong` / `--ally-border-input` |
| 字体字号 | `--ally-ui-font` / `--ally-mono-font` / `--ally-message-font-size` / `--ally-sub-font-size` / `--ally-aux-font-size` |
| 毛玻璃 | `--ally-composer-blur` / `--ally-panel-glass` |

四条禁令：

1. 别写死颜色（写死了切主题就不跟着变）。
2. 别用 `prefers-color-scheme`：宿主的明暗是显式设置，可能和系统不一致。
3. 要用 naive-ui 就自己带一份（插件 bundle 自带 vue 与 naive），它的组件主题得自己配——直接用上面这些变量最省事，宿主的 naive 主题状态不会跨 bundle 传过去。
4. 需要用表外的变量时（例如做表格要的分隔线 `--ally-border` / `--ally-border-subtle`，它们**不在契约里**），只能写成 `var(--内部token, var(--公开token))` 这种**带兜底**的形态，改名时自动退回兜底色，不会变成一条看不见的线。

### 5.4 版式三条

1. **样式是全局的，必须自己圈作用域**。`style.css` 是插进宿主文档的 `<style>`，不会自动隔离——写成 `div { … }`、`.card { … }` 这类通用选择器会把整个应用界面改坏。给根元素挂一个前缀 class（如 `.my-root`），所有规则都写在它下面，卸载时把 `<style>` 摘掉。
2. **别跨宿主的滚动容器做 `position: sticky`**。宿主给插件页容器留了内边距（`.plugin-page-body`，当前 `16px 22px 24px`），跨容器吸顶会被这个内边距卡住（滚动时留一条缝）。正解是插件自己撑满并自管滚动。
3. **别加大面积模糊**。`backdrop-filter` / `filter: blur()` 的成本是面积 × 半径，在插件页这种整屏区域上会直接拖垮滚动。毛玻璃只用 `--ally-composer-blur`。

---

## 6. 本地怎么验证（别只会「导入 zip 试试」）

`file://` 直接 `import` 会被 CORS 挡死，必须走 HTTP：

1. 插件目录的**上一层**起一个静态服务：`python -m http.server 8765`。
2. 写一张预览台 `preview.html`：直接 `import { mount } from './my-plugin/index.js'`，传一个**假 host**：
   - `http.request` 返回写死的响应（`{ status, json, truncated: false, … }`）；
   - `store.get` 返回预置配置，`store.set` 打到 console；
   - `events.on` 返回空取消函数，`log` / `ui.notify` 打到 console。
   - 外面套一层和宿主一样的容器样式（`.plugin-page-body` 的内边距照抄，当前 `16px 22px 24px`），否则版式对不上。
3. 打开 `http://127.0.0.1:8765/preview.html`，改一行刷新就见效，不用每次重新导入。

好处：改样式时能直接用无头浏览器断言（行高是否统一、表头与数据行列是否对齐、窄窗口有没有横向溢出），比拿肉眼看可靠得多。

---

## 7. 硬规矩

| # | 规矩 | 违反了会怎样 |
|---|---|---|
| 1 | 清单只写那 10 个键 | 装不上，报错在管理页 |
| 2 | `menu` 恰好一项 | 装不上 |
| 3 | `icon` 取白名单里的名字 | **静默**换成默认图标，不报错 |
| 4 | `permissions.workspace` 只能 `none` / `read` | 写 `write` 装不上 |
| 5 | `permissions.http` 要列全所有目标主机（含跳转后的） | 调用时 `E_PLUGIN_HOST_DENIED` |
| 6 | 样式只用公开 token（清单外的要带兜底），且所有选择器都圈在自己的根 class 下 | 前者：切主题时静默不跟随；后者：样式表是全局的，会把宿主界面改坏 |
| 7 | 别跨宿主的滚动容器做 `sticky` | 吸顶时留一条缝 |
| 8 | 凭据只能放 `host.store`，别写进代码或包 | 导出勾了「包含数据」会把凭据一起带出去 |
| 9 | `mount` 必须返回清理函数 | 切页 / 重装后订阅、定时器、样式表泄漏 |
| 10 | 改完必须**重新导入** | 一直跑旧代码 |

---

## 8. 一定会踩的坑

1. **打开外部链接没有 API**（`host.open` 只有 `inFileManager`）。这是插件最常见的需求，目前只能退而求其次做「复制链接」：把 URL 以文本显示 + 一个复制按钮。**不要用 `<a href>` 顶**——插件页和宿主在同一个文档里，点一下就把整个应用页面导航走了，用户得重启。想跟工单 / 文档互动，就先把链接复制出来。
2. **报错要自己加工**。`host.http` 抛出的 `Error.message` 是后端原样报错，形如 `Get "https://…": EOF`（整条 URL 都在里面），直接贴到界面上会糊成四五行的天书。映射成一句人话（"连接被对方掐断""请求超时"），原文交给 `host.log.error`。
3. **外部接口会按 IP 掐人，别只信一次实测**。同一个接口可能前一分钟通、后一分钟 EOF，直连和走代理结论一样。能选两个源就选两个，或者至少把失败明明白白显示出来，别静默留空。
4. **响应不是 JSON 不代表失败**。像 `var hq_str_x="…";` 这种就得用 `body` 自己解析（`json` 为空是正常的）；反过来 `truncated` 为真时 `json` 必定为空，别把两者混为一谈。
5. **挂载后立刻会有一次显隐事件**（见 §3.2 第 2 条），不注意就是双请求。
