---
name: playwright-cli
description: 用 playwright-cli 命令行驱动浏览器（打开页面、点击、填表、截图、无障碍快照、抓 JS 渲染内容）。含测前端 bug 的标准工作流（起服务→开窗口→点/填→看结果）、Ally 下的会话托管、读页面成本控制、稳定定位器与 AntD 组件库实操。
type: browser-automation
whenToUse: 用户要求操作浏览器、自动化网页交互、端到端测试、抓取需要 JS 渲染的页面内容、或对页面元素进行精确定位与点击时。
---

# Playwright CLI（Ally 集成指南）

`playwright-cli`（npm 包 `@playwright/cli`）以命令行驱动浏览器，所有命令经 command 工具执行，shell 是 bash（Windows 上是 Git Bash）。

**完整命令与参数以 `playwright-cli --help`、`playwright-cli <command> --help` 为准**，版本会变，不要凭记忆猜参数。本 skill 只写 help 不会说明的东西：Ally 下的会话托管、成本控制、定位器策略、组件库实操和可复用流程。

> 实测基线：`playwright-cli` **0.1.22** / Windows + `msedge`（2026-10-21）。文中体积与超时都是本机实测值，不是承诺；换版本先 `--version`，再抽查一两条。

## 0. 五条铁律

1. **有头/无头按场景选**：用户允许占用屏幕——交互式探索、演示、要让用户看过程时加 `--headed`；纯抓取、批量回归、CI 用默认无头。有头窗口会抢焦点，纯后台任务别开。
2. **会话活不过一条命令**：`open` 所在的 command 一结束，浏览器就被回收。要么一条命令跑完整条链路，要么用 `service` 托管常驻会话。
3. **每条命令都带 `-s=<会话名>`**：包括 `resize`/`screenshot`/`state-save`/`eval`。漏了会落到 `default` 会话，报 `The browser 'default' is not open`。
4. **先问成本再读页面**：`eval` ≈ 几十字节 < `snapshot --depth=1..2` ≈ 0.2–0.5 KB < 弹窗快照 ≈ 0.6 KB < 小页面全页快照 ≈ 3 KB。**子树越大越没谱**：300 行表格的元素快照 39 KB、全页 42 KB，1200 行全页 171 KB——快照**不会截断**，只会把上下文撑爆。表格/列表一律先 `eval` 数数取行。
5. **ref 是快照的产物，不是稳定 id**：页面结构不变时 `e12` 能跨命令复用，但重渲染/导航后会报 `Ref ... not found in the current page snapshot`（导航后 ref 变成 `f2e12` 这类带帧前缀的形态）。能按 role/文本定位就别存 ref。

## 1. 安装检查

进入浏览器任务前先 `playwright-cli --version`。未安装时必须先用 `ask` 征得同意，再 `npm install -g @playwright/cli@latest`；`ask` 必须是该轮唯一的工具调用。安装失败如实报告，不要静默重试。

## 2. 浏览器选择

优先用系统已装的浏览器，避免下载 100–300 MB 的 Playwright 二进制：

- 系统浏览器（免下载）：`msedge`、`chrome`
- 需下载：`chromium`、`firefox`、`webkit`（`webkit` 不等于系统 Safari）

**Windows**：直接用 `--browser=msedge`（Win10+ 自带）。

**macOS / Linux**：探测一次再决定：

```bash
if command -v google-chrome >/dev/null 2>&1 || command -v google-chrome-stable >/dev/null 2>&1 || command -v chromium >/dev/null 2>&1; then echo "BROWSER=chrome"
elif command -v microsoft-edge >/dev/null 2>&1 || command -v msedge >/dev/null 2>&1; then echo "BROWSER=msedge"
elif [ -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ]; then echo "BROWSER=chrome"
elif [ -x "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" ]; then echo "BROWSER=msedge"
else echo "BROWSER=none"; fi
```

`BROWSER=none` 或 `open` 报 `Browser not installed` / `channel not found` → 先 `ask`，确认后 `playwright-cli install-browser` 再用 `--browser=chromium`。用户指定浏览器时从其要求。

## 3. 标准工作流（起服务 → 开窗口 → 点/填 → 看结果）

测前端 bug 走这一条就够。占位符只有三个：前端地址、会话名、service id。

### 3.1 起前后端：用 service，不要 `&` 后台跑

```
service start:  name=be  command=<后端启动命令>   cwd=<后端目录>
service start:  name=fe  command=npm run dev     cwd=<前端目录>
```

就绪用轮询，别睡固定秒数（命中即退，最多 60s）：

```bash
for i in $(seq 1 60); do curl -sf -o /dev/null "http://127.0.0.1:5173/" && break; sleep 1; done; echo READY
```

没 READY 就先 `service read <id>` 看启动日志——服务没起来时开浏览器只会白跑。

### 3.2 开一个有头常驻会话（一条命令，之后都能接管）

```
service start
  name: pw
  command: S=bug-test; playwright-cli -s=$S open "http://127.0.0.1:5173/" --browser=msedge --headed --idle-timeout=0 && echo READY && sleep 3600
  cwd: .tmp/pw
```

- 会话名先 `playwright-cli list` 看一眼，别撞名（会话是全局的）。
- `--headed` 让用户看得见过程；不想弹窗就去掉。
- 之后每条命令都带 `-s=bug-test`，cwd 保持一致（快照/截图都落 cwd）。

### 3.3 先摸结构，再动手（一条 eval，几十字节）

```bash
playwright-cli -s=bug-test --raw eval "() => ({
  path: location.pathname, title: document.title,
  buttons: [...document.querySelectorAll('button')].slice(0,20).map(b => b.innerText.trim()),
  inputs: [...document.querySelectorAll('input,textarea')].slice(0,20).map(e => e.placeholder || e.name || e.type),
  selects: document.querySelectorAll('.ant-select, .el-select').length,
  tables: document.querySelectorAll('table').length
})"
```

### 3.4 点 / 填 / 等

```bash
# 输入：placeholder 或 role 都行，别用 nth-child
playwright-cli -s=bug-test fill "input[placeholder='用户名']" admin
playwright-cli -s=bug-test fill "getByRole('textbox', { name: '密码' })" 'Passw0rd!'

# 点击：role + 文本；AntD 两字按钮渲染成「登 录」，用正则兜住
playwright-cli -s=bug-test click "getByRole('button', { name: /登\s*录/ })"

# 等异步结果：不要 sleep，用条件等待
playwright-cli -s=bug-test --raw run-code "async page => {
  await page.getByRole('button', { name: /查\s*询/ }).click();
  await page.waitForResponse(r => r.url().includes('/api/') && r.status() < 400, { timeout: 8000 }).catch(() => {});
  return await page.locator('tbody tr').count();
}"
```

点不动（报 `... intercepts pointer events`）：先 `focus()` + 键盘，再 `click({ force: true })`。动作超时是 **5s**，不是 30s。

### 3.5 看结果：先看数据，再看报错，最后截图

```bash
# 1) 页面事实：文案/条数/状态
playwright-cli -s=bug-test --raw eval "() => ({
  toast: [...document.querySelectorAll('.ant-message-notice-content, .el-message, #toast')].map(e => e.innerText).filter(Boolean),
  rows: document.querySelectorAll('tbody tr').length,
  firstRow: document.querySelector('tbody tr')?.innerText,
  text: document.body.innerText.slice(0, 300)
})"

# 2) 前端报错（组件库的表单校验也会 console.error，别一律当 bug）
playwright-cli -s=bug-test console error

# 3) 接口失败：filter 不带前导斜杠（Windows 下会被 Git Bash 改写，见 §14）
playwright-cli -s=bug-test requests --filter "api" | grep -E "=> \[(4|5)[0-9][0-9]\]" || echo "no 4xx/5xx"

# 4) 需要留证/看不清时再截图
playwright-cli -s=bug-test screenshot --filename=bug-01.png
```

### 3.6 一轮操作固化成脚本（要反复跑时）

```js
// .tmp/pw/check.js —— playwright-cli -s=bug-test run-code --filename=check.js
async page => {
  const errors = [];
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
  await page.goto(page.url(), { waitUntil: 'domcontentloaded' });   // 回到干净起点
  await page.getByRole('textbox', { name: '用户名' }).fill('admin');
  await page.getByRole('textbox', { name: '密码' }).fill('Passw0rd!');
  await page.getByRole('button', { name: /登\s*录/ }).click();
  await page.waitForTimeout(800);
  return { url: page.url(), toast: await page.locator('#toast').innerText().catch(() => ''), rows: await page.locator('tbody tr').count(), errors };
}
```

实测这类脚本从重载到取回结果约 **2s**，返回的 `errors` 里连 500 响应都会出现。

### 3.7 收尾

```bash
playwright-cli -s=bug-test close     # 只关自己的会话
# service stop <pw-id> <fe-id> <be-id>
```

`close-all` 会关掉本机所有会话，别用。

### 3.8 测 bug 时到底看什么

| 看什么 | 怎么拿 | 常见问题 |
|---|---|---|
| console 红字 | `console error` | 未捕获异常、读空对象、key 重复 |
| 接口 4xx/5xx | `requests --filter "api"` | 参数错、鉴权过期、CORS |
| 数据是否落地 | `eval` 读 `tbody tr` / 详情文案 | 点了保存但列表没刷新、金额 NaN、空列表 |
| 弹窗/浮层 | `isVisible()`、`snapshot "[role=dialog]"` | 该关没关、关了两层、遮罩挡住按钮 |
| 样式/布局 | `screenshot --filename=` | 错位、遮挡、溢出、暗色主题对比度 |

## 4. 两种会话模式（按任务性质选）

### A. 一次性链路 → 整条写进一条命令

步骤、选择器、断言事先确定时用（回归复查、检查某页是否报错/NaN/元素是否可见）。不占 service 槽位，跑完即净，命令可原样重跑；偶尔需要人看着跑就加 `--headed`。

```bash
S=demo
playwright-cli -s=$S open "$URL" --browser=msedge >/dev/null 2>&1
playwright-cli -s=$S fill 'input[placeholder="用户名"]' "$USER" >/dev/null 2>&1
playwright-cli -s=$S fill 'input[placeholder="密码"]' "$PASS" >/dev/null 2>&1
playwright-cli -s=$S click 'button[type="submit"]' >/dev/null 2>&1
sleep 3
playwright-cli -s=$S --raw eval "() => [...document.querySelectorAll('.menu-item')].map(e => e.textContent.trim())"
playwright-cli -s=$S close >/dev/null 2>&1
```

- 子命令一律 `>/dev/null 2>&1` 静音，只把需要的值 `--raw eval` 取出来；别把整棵 snapshot 倒进上下文。
- 用 `&&` 串联则前一步失败会中断；要「尽力继续」用 `;`。
- 每条 `command` 有超时，`click` 本身只等 **5 秒**（`TimeoutError: Timeout 5000ms exceeded`），卡住基本是定位器错或被遮挡，不是「需要更长的 sleep」。

### B. 交互式探索 → 用 `service` 托管常驻会话

需要「看一步、想一步」、要等异步渲染、要反复微调时用。`open` 之后拖住进程树，后续每条 command 都能接管同一浏览器，登录态与页面位置都在。

```
service start
  command: playwright-cli -s=work open "$URL" --browser=msedge --headed --idle-timeout=0 && echo READY && sleep 3600
  cwd: .tmp/pw            # 产物落 .playwright-cli/，放 scratch 目录不脏仓库
```

```bash
playwright-cli list                                    # 确认 work 会话 open
playwright-cli -s=work --raw eval "location.pathname"   # 后续命令继续操作同一页面
playwright-cli -s=work goto "$URL/next"
```

- `--idle-timeout` 的单位是**毫秒**：只有 `0` 表示永不退出；写 `--idle-timeout=3600` 是 3.6 秒后自尽（实测 `2000` 两秒即消失）。无头会话默认 1 小时，有头默认不退出。
- **会话名是全局的**：`list` 能看到别的任务、别的 cwd 起的会话（`--all` 只是跨 workspace 汇总），在另一个目录用 `-s=work eval` 照样接管。起名前先 `playwright-cli list`，名字带上任务前缀，避免撞名。
- 收尾：`playwright-cli -s=work close` + `service stop <id>`。**别顺手 `close-all`**：它会关掉本机所有会话（包括别的任务正在用的）；僵尸进程用 `kill-all` 兜底。
- service 槽位有限（`service list` 可见上限 32），别长期占着。
- 状态会累积（登录态、mock 路由、localStorage），同一会话做对照实验要留神。

### 判断口径

| 场景 | 选择 |
|---|---|
| 一次性验证、回归复查（步骤固定） | A 串完 |
| 需要按结果分支、等渲染、反复试 | B holder |
| 用户要接手看/操作 | B holder + `--headed` |
| 每次都串但不想重复登录 | A + `state-save` / `state-load` |

## 5. 读页面：成本梯度与用法

| 手段 | 实测体积（0.1.22） | 用途 |
|---|---|---|
| `--raw eval "() => ({...})"` | 37 B | 断言、取数、探测 DOM（**默认首选**） |
| `snapshot --depth=1` / `--depth=2` | 0.17 KB / 0.45 KB | 只看骨架，摸底永远从这两档开始 |
| `snapshot "[role=dialog]"` | 0.6 KB | 弹窗子树 |
| `find "文本"` / `find --regex` | 0.35 KB | 大页面定点找元素，顺带给可用 ref |
| `snapshot`（无参） | 3 KB 小页面 / 42 KB 300 行表格 / 171 KB 1200 行 | 仅首次摸底小页面 |
| `snapshot "table"` | **39 KB**（300 行） | 只对**小**表格用；大表格改 `eval` |
| `run-code` | 任意 | 多步交互 + 结构化返回 |

```bash
playwright-cli -s=work snapshot "table"              # 小表格可读；大表格会几十 KB，改用 eval
playwright-cli -s=work snapshot "[role=dialog]"       # 弹窗子树
playwright-cli -s=work snapshot --depth=2             # 只摸骨架（depth=4 在表格页就基本是全量）
playwright-cli -s=work find "保存"                    # 找元素，顺带刷新出可用 ref
playwright-cli -s=work --raw snapshot > page.yml      # 存文件后本地 diff（更推荐 `snapshot --filename=page.yml`）
```

注意：`--depth` 确有作用（同一张 300 行表格：depth=1 170 B、depth=2 447 B、depth=3 15 KB、depth=4 42 KB 全出来），但它按**树的层数**截，不按「你要的内容」截；要表格行内容就用 `eval` 读 `tbody tr`，别靠加深快照。`eval` / `find` / `snapshot` 都支持 `--filename=`，直接落盘不进上下文。

### 探测未知页面（开工前一条 eval）

不要猜类名。先花一条命令把结构问清楚：

```bash
playwright-cli -s=work --raw eval "() => ({
  path: location.pathname,
  buttons: [...document.querySelectorAll('button')].slice(0,15).map(b => ({t: b.innerText.trim(), aria: b.getAttribute('aria-label')})),
  inputs: [...document.querySelectorAll('input,textarea')].slice(0,15).map(e => e.placeholder),
  tables: document.querySelectorAll('table').length,
  contentClass: document.querySelector('main, .admin-content, #app')?.className
})"
```

## 6. 定位器策略

优先级：**role/文本 locator > 当次快照的 ref > CSS > 坐标**。

- **role/name 最稳**（跨重渲染有效）：`getByRole('button', { name: /保存/ })`、`getByRole('textbox', { name: '用户名' })`、`getByTestId('x')`。`name` 默认是**子串匹配**（忽略大小写），不是精确整串。
- **AntD 会往两字文案里塞空格**：`确定`→`确 定`、`登录`→`登 录`。空格夹在中间，子串匹配也救不了（实测 `{name:'登录'}` 与 `/登录/` 都是 0 命中），要么写 `'登 录'`，要么 `/登\s*录/`。只有 `type="link"/"text"` 的按钮不插空格（实测 `查询` 原样）。
- **图标按钮的名字会带上图标 aria-label**：实测 `search 查询`、`edit 编辑`。这类名字里「查询」是连续子串，`{ name: '查询' }`（默认非精确）就能命中，**不需要**正则；写成 `{ exact: true }` 才要求整串。
- ref 适合「刚 snapshot 完立刻点」：`snapshot` → `click f1e467`。中途插了别的命令就可能已失效。
- ref 想变成长效定位器：`playwright-cli -s=work --raw generate-locator <ref>`（返回 `locator('div').filter({ hasText: '密码' })` 一类 Playwright 表达式，可直接当 target 用）。
- 文本重复（AntD 下拉同时渲染 a11y `option` 和可见项）时加 `.last()` / `:visible` 收敛，或用 `run-code` 精确定位。
- **动作超时是 5 秒，不是 30 秒**：元素在但点不到会卡 5s 再报 `TimeoutError: Timeout 5000ms exceeded`，别误判成「页面慢」。

## 7. 等待：别依赖 networkidle

后台管理页普遍有轮询（未读数、站点信息），`waitUntil: 'networkidle'` 可能迟迟不达阈值。

```bash
# 推荐：domcontentloaded + 固定/条件等待
playwright-cli -s=work --raw run-code "async page => {
  await page.goto(URL, { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: /查\s*询/ }).click();
  await page.waitForResponse(r => r.url().includes('/api/') && r.ok());
  return await page.locator('tbody tr').count();
}"
```

- 等元素：`locator.waitFor({ state: 'visible' })`；等条件：`page.waitForFunction(...)`；等接口：`page.waitForResponse(...)`。
- `playwright-cli` 没有 `wait` 命令，纯等待只能 `sleep`，但能用条件等待就别 sleep。

## 8. 复杂控件实操（组件库是最大时间坑）

### 通用套路

1. 一条 `eval` 看控件真实类名（`ant-select` 还是 `ant-tree-select`、`el-select` 还是 `el-dialog`）。
2. 点击被拦截时（报 `... intercepts pointer events`），先 `focus()` + 键盘，再考虑 `click({ force: true })`。
3. 浮层（下拉/弹窗）渲染在 body 末尾的 portal 里，**不要在被限定在表单容器内的选择器里找**。
4. 组件式弹窗（如 `<a-modal>`）关闭后 DOM 常常还在（只是 `display:none`），判断「是否打开」用 `isVisible()`，不要用 `count()`；但静态方法弹的（如 `Modal.confirm`）可能整块移除，别硬套。

### Ant Design Vue（ant-design-vue 4.x 实测）

| 控件 | 现象 | 做法 |
|---|---|---|
| `a-select` / `a-tree-select` | `click ".ant-select"` 本身能展开；真正被拦的是内部 `input`（`.ant-select-selection-search-input`，报 `... .ant-select-selection-item intercepts pointer events`）；下拉是 toggle，且**关闭后 DOM 仍在**（`display:none`），直接点 `.ant-select-item-option` 会白等 5s 超时 | `locator('.ant-form-item').filter({hasText:'字段名'}).locator('input').focus()` → `keyboard.press('ArrowDown')` → `Enter`（实测一次成功） |
| `a-modal` | 组件式 `<a-modal>` 关闭后 DOM 仍在（`count()===1`、`isVisible()===false`） | 判断开关用 `isVisible()`；快照用 `snapshot "[role=dialog]"` |
| `Modal.confirm` | 静态方法关闭后**整块移除**（`count()===0`） | 与 `a-modal` 相反，别假设它还在 |
| `a-dropdown` | 默认点击触发；`hover()` 时 `.ant-dropdown` **连节点都不存在** | `click` 后取 `.ant-dropdown`（`display:block` 即展开） |
| 删除确认 | 不是 Popconfirm，而是 `Modal.confirm` | `.ant-modal-confirm` 里的 `button:has-text("删 除")` |
| 按钮文案 | primary/default/danger 的两汉字插空格（`确 定`/`删 除`/`取 消`），`type="link"/"text"` 不插 | 拿不准先 `eval` 读 `button.innerText`，或写 `/删\s*除/` |
| 提示消息 | 顶部 toast | `.ant-message-notice-content` 的 `innerText` |
| 表格 | 全页快照**不会截断行**，只会线性膨胀（300 行 42 KB、1200 行 171 KB） | `eval` 读 `tbody tr`，或只对小表格用 `snapshot "table"` |

一段可直接复用的「打开下拉 → 选中 → 提交」：

```bash
playwright-cli -s=work --raw run-code "async page => {
  const item = page.locator('.ant-form-item').filter({ hasText: '角色' });
  await item.locator('input').first().focus();
  await page.keyboard.press('ArrowDown');          // 展开
  await page.waitForTimeout(300);
  await page.keyboard.press('Enter');              // 选第一个匹配项
  await page.getByRole('button', { name: '确 定' }).click();
  await page.waitForTimeout(1200);
  return {
    toast: await page.locator('.ant-message-notice-content').allInnerTexts(),
    rows: await page.locator('tbody tr').allInnerTexts()
  };
}"
```

### Element Plus / 其他库

未在此实测，按下述思路探测：`el-select` 下拉在 `.el-select-dropdown`，弹窗是 `.el-dialog` / `.el-message-box`，提示是 `.el-message`；点击被拦截时同样先试 `focus + ArrowDown/Enter`。**先 eval 看类名，再决定选择器**，不要抄别的库的类名。

## 9. 登录态复用（省掉重复登录与验证码）

```bash
# 一次登录（人工或脚本）之后：
playwright-cli -s=work state-save auth.json          # 落 cookies + localStorage
# 新会话/新命令里：
playwright-cli -s=work2 open "$URL" --browser=msedge >/dev/null
playwright-cli -s=work2 state-load auth.json >/dev/null
playwright-cli -s=work2 goto "$URL/app" >/dev/null
playwright-cli -s=work2 --raw eval "location.pathname"      # 实测冷启动到已登录页面约 5s
```

- `state-save` **只含 cookies + localStorage，不含 sessionStorage**；refresh token 常放在 sessionStorage → access token 过期后仍需重登。
- 文件路径相对于命令 cwd；不同 cwd 之间不共享。
- **Token 型 SPA 可以跳过登录 UI**：从接口拿 token → `localstorage-set <key> "<value>"` 注入 → `goto`。只有真要测登录表单时才走 UI。
- **验证码**：人工登一次然后 `state-save` 是最省事的；若必须自动化且能触达存储（本地开发读 Redis / 数据库），读取答案再提交；页面自己的 `captcha_id` 用 `page.waitForResponse(r => r.url().includes('/captcha/'))` 抓，别靠猜。

## 10. 可复用脚本（run-code --filename）

步骤超过 ~5 步、或要在同一任务里反复跑，就写成文件，避免每次重敲长内联字符串：

```js
// .tmp/pw/smoke.js —— playwright-cli -s=work run-code --filename=smoke.js
async page => {
  const routes = ['/dashboard', '/system/users', '/system/roles'];
  const out = [];
  for (const r of routes) {
    await page.goto(page.url().split('/').slice(0, 3).join('/') + r, { waitUntil: 'domcontentloaded' }).catch(() => {});
    await page.waitForTimeout(900);
    out.push(await page.evaluate(route => {
      const t = document.body.innerText.replace(/\s+/g, ' ').trim();
      return { route, landed: location.pathname, ok: t.length > 20, head: t.slice(0, 40) };
    }, r));
  }
  const failed = out.filter(x => !x.ok);
  return { total: out.length, failed: failed.length, failures: failed };
}
```

- `run-code` 是**隔离上下文**：没有 `require` / `process`，复杂 DOM 读取放 `page.evaluate` 里。
- 返回值直接是 JSON，配合 `--raw` 可管道给 `python`/`jq` 做断言。
- 需要录一段「人工操作 → 代码」时用 `recording-start` / `recording-stop`；排查慢请求用 `requests --filter` + `request-body` / `response-body`。

## 11. 证据与调试

- **截图**：`playwright-cli -s=work screenshot --filename=page.png`，无头/有头都正常出图；Ally 的 `read` 可直接看图确认视觉（单图成本 ≈ 一张图片，别连续截）。有头时也可用 Ally 的 `screenshot` 工具抓窗口，但优先用命令截图（可限定元素、不受其他窗口遮挡）。
- 默认 viewport 是 **1280×720**，后台系统建议先 `playwright-cli -s=work resize 1600 900`（`resize` 也要带 `-s`）。
- **console**：`console error` 看报错，`console --clear` 清空后重测更干净；注意组件库把表单校验拒绝也 `console.error` 打出来，别当成 bug。
- **requests**：默认不含静态资源，但轮询页面累计仍可达上千条 → `requests --filter "api"`（**别写 `/api/`**，Windows 会被 Git Bash 改成路径，见 §14），分段 `requests --clear`。
- 需要人肉查看/标注时用 `playwright-cli -s=work show`（开窗口，用户已允许）；`show --annotate` 可让用户圈选并把标注回传。

## 12. 错误恢复速查

| 报错 | 原因 | 处理 |
|---|---|---|
| `command not found` | 未安装 | 回第 1 步 |
| `Browser '<name>' is not open` / `(no browsers)` | 会话随上一条命令被回收，或 `-s` 名字写错/漏写 | 合并到一条命令，或用 service holder；核对 `-s` |
| `Browser not installed` / `channel not found` | 系统浏览器未装 | `ask` 后 `install-browser`，用 `--browser=chromium` |
| `Ref ... not found in the current page snapshot` | ref 已失效 | 重新 `snapshot`，或改用 role 定位 |
| `does not match any elements` | 选择器错/浮层未展开 | 先 eval 看 DOM，再展开浮层后取元素快照 |
| `TimeoutError: Timeout 5000ms exceeded` | 元素在但被遮挡（日志里会写 `... intercepts pointer events`），或目标落在隐藏的旧浮层里 | `focus()` + 键盘；或 `run-code` 里 `click({ force: true })`；浮层先用 `.ant-select-open` / `isVisible()` 确认真开着 |
| 页面已加载但内容为空 | 用错容器类名 / 还在渲染 | eval 探测真实容器类名，加固定等待 |
| 其他参数错误 | 版本差异 | `playwright-cli <command> --help`，不要硬猜 |

## 13. 速查卡

```bash
S=work; U=http://localhost:5173

# 常驻会话（推荐用于需要思考的探索）
# service start: playwright-cli -s=$S open $U --headed --idle-timeout=0 && echo READY && sleep 3600

playwright-cli -s=$S open $U --browser=msedge          # 无头（批量用）；演示/交互加 --headed
playwright-cli -s=$S goto $U/path
playwright-cli -s=$S snapshot --depth=2                # 骨架
playwright-cli -s=$S snapshot "table"                  # 元素子树（只限小表格）
playwright-cli -s=$S find "保存"                        # 定点找
playwright-cli -s=$S --raw eval "() => document.title"
playwright-cli -s=$S click "getByRole('button', { name: /保存/ })"
playwright-cli -s=$S fill "getByRole('textbox', { name: '用户名' })" admin
playwright-cli -s=$S press Enter
playwright-cli -s=$S state-save auth.json
playwright-cli -s=$S state-load auth.json
playwright-cli -s=$S console error
playwright-cli -s=$S requests --filter "api"
playwright-cli -s=$S screenshot --filename=shot.png
playwright-cli -s=$S close
# playwright-cli close-all   # 全清本机所有会话（含别的任务），确认没人在用再执行
```

## 14. Ally 集成注意事项

- **cwd**：用专用 scratch 目录（如 `.tmp/pw`）；`.playwright-cli/` 快照、`auth.json`、截图都落在 cwd，别把产物写进仓库根。
- **路径**：一律 forward slash；Windows 绝对路径在 Git Bash 里写 `C:/...` 或 `/c/...`。
- **以 `/` 开头的参数会被 Git Bash 改写**（MSYS 路径转换）：`--filter "/api/"` 到 node 手上已变成 `<Git 安装目录>/api/`，于是静默 0 命中；`state-save /tmp/x.json` 同理。规避：参数不带前导斜杠（`--filter "api"`）、用 `C:/...` 形态，或整条命令前加 `MSYS_NO_PATHCONV=1`。
- **超时**：`command` 的 `timeout` 给 60–180s；playwright 单步动作只等 **5s**（导航另算，不可达地址实测 10s 左右就报 `ERR_*`）；一旦 `command` 超时会被提升为后台 service，**不要重跑**（端口/会话会冲突）。
- **清理**：任务结束只关自己的 `-s=<name> close`；`close-all` 是全局的（会关掉本机所有会话，包括别的任务在用的），`kill-all` 更要慎用。
- **不要**并发开一堆会话做同一件事（互相污染登录态、放大内存）。
- **网络条件**：`route` 可 mock 接口、`network-state-set offline` 可测断网态。
