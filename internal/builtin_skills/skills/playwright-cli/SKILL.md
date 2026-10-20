---
name: playwright-cli
description: 用 playwright-cli 操作浏览器、复现和验证页面 bug。以 service 启动前后端和可见浏览器，完成登录、点击、输入、等待页面结果、检查接口及截图；包含 Ally 会话保活、低成本页面探测与组件定位。
type: browser-automation
whenToUse: 用户要求操作浏览器、模拟登录点击输入、复现或验证网页问题、端到端测试、抓取需要 JS 渲染的内容时。
---

# Playwright CLI（Ally 集成指南）

通过 Ally 的 `command` 执行 `playwright-cli`（npm 包 `@playwright/cli`），使用 bash；Windows 上是 Git Bash。`service`、`ask`、`read` 是 Ally 工具，不是 shell 命令。

常见任务直接走第三章。参数以 `playwright-cli --help` 和 `playwright-cli <command> --help` 为准。核验基线：**0.1.22 / Windows / msedge，2026-10-21**；动作超时、输出大小和组件行为会随版本及项目配置变化。

## 0. 操作原则

1. **交互排错默认有头**：用户要求开窗口、模拟操作、查看页面时使用 `--headed`；后台抓取、批量回归可无头。可见窗口不代表已经检查过页面，关键结果还要读取或截图查看。
2. **Ally Windows 中跨 command 探索要保活**：command 结束会清理其后代进程，CLI daemon 也会被回收。浏览器用 `service` 托住；步骤完全确定时也可在同一条 command 内完成并关闭。这是宿主行为，不是 CLI 在所有终端上的限制。
3. **每条浏览器命令显式带 `-s=<会话名>`，保持 cwd 一致**：包括 eval、截图、存取登录态。每次 command 的 shell 变量不共享，不能假定上一条定义的 `$S`、`$URL` 还在。
4. **动作完成不等于业务成功**：先定义本次预期；点击后等目标页面条件，需要接口证据时精确匹配请求。超时、接口错误和断言失败必须保留，不能吞掉后继续报成功。
5. **按需读页面**：先有限字段的 eval 或浅层快照，表格只读总数及少量行；布局问题必须看截图。ref 来自当前快照，不是稳定业务 id，页面变化后重新获取或改用语义 locator。

## 1. 安装检查

先执行 `playwright-cli --version`，不要为了操作页面主动升级已可用版本。未安装且尚未获准安装时，先用 Ally `ask` 征得同意（按工具要求单独调用），再运行 `npm install -g @playwright/cli@latest`。安装失败保留错误并报告，不盲目重试。

## 2. 浏览器选择

优先使用已安装的系统浏览器：Windows 先试 `--browser=msedge`，已有 Chrome 可用 `--browser=chrome`；其他系统先确认相应浏览器确实安装。发现系统 `chromium` 可执行文件不代表 `chrome` channel 可用。

没有可用浏览器时，先查看 `open --help`、`install-browser --help` 和当前配置，再选择下载方案；未授权的安装先 ask。下载 Chromium 可显式 `playwright-cli install-browser chromium`，通过配置选择它，不凭记忆拼 channel。0.1.22 的 `open --help` 列出的是 chrome、msedge、firefox、webkit；使用下载的 Chromium 可写配置文件 `browser.json`：

```json
{"browser":{"browserName":"chromium"}}
```

然后 `open ... --config=browser.json`，不要同时传系统 channel；配置路径相对 command cwd。webkit 是 Playwright WebKit，不是系统 Safari。用户指定浏览器时遵从其要求。

## 3. 标准工作流（起服务 → 开窗口 → 点/填 → 验证结果）

示例约定：前端 `http://127.0.0.1:5173`、浏览器会话 `ally-user-bug-01`、产物目录 `.tmp/pw`。**端口、启动命令、登录信息、路由、接口和定位器都要按当前项目确认**，示例账号与页面不是通用事实。

### 3.1 确认环境，启动前后端

先读项目启动说明、package scripts、后端配置，调用 service 的 `{"action":"list"}` 检查已有服务。可复用的就复用，记录归属；不要重复启动或停止别人已有的服务。确认用于复现的账号、数据，以及每一步的预期结果。

通过 command 在工作区根创建目录：

```bash
mkdir -p .tmp/pw
```

再调用 Ally service，每个服务一次调用；description 必填，cwd 必须已经存在且相对工作区根：

```json
{"action":"start","name":"backend","description":"启动开发后端，供页面登录和接口排错","command":"<项目实际后端启动命令>","cwd":"<后端目录>"}
```

```json
{"action":"start","name":"frontend","description":"启动前端开发服务，供可见浏览器复现页面问题","command":"npm run dev","cwd":"frontend"}
```

保存返回的 id。start 返回 running 只代表进程启动，不代表应用就绪。用 `{"action":"read","id":"<实际服务 id>"}` 看日志，确认实际 URL、端口和启动错误。Vite 可能自动换端口；不能只因旧端口能访问就认定访问的是本次启动的应用。

### 3.2 前后端分别检查就绪，失败停止后续步骤

把下面脚本作为一条 command 执行，timeout 设 90 秒。替换两个 URL，后端用实际健康接口；没有健康接口就选已知状态码和响应内容的轻量接口，不猜 `/health`。

```bash
set -e
deadline=$((SECONDS + 60))
wait_http() {
  local label="$1" url="$2" remaining attempt status
  while (( SECONDS < deadline )); do
    remaining=$((deadline - SECONDS))
    attempt=$((remaining < 2 ? remaining : 2))
    if status=$(curl --noproxy '*' --connect-timeout 1 --max-time "$attempt" -sS -o /dev/null -w '%{http_code}' "$url" 2>/dev/null) && [[ "$status" == 2?? ]]; then
      printf '%s READY: %s\n' "$label" "$url"
      return 0
    fi
    if (( SECONDS < deadline )); then sleep 1; fi
  done
  printf '%s NOT_READY: %s\n' "$label" "$url" >&2
  return 1
}
wait_http backend 'http://127.0.0.1:8080/health'
wait_http frontend 'http://127.0.0.1:5173/'
```

这是共用约 60 秒期限、只接受 2xx 的 HTTP 可达性检查；项目若预期返回重定向或 401，要显式修改状态码判据。HTTP 200 错误页仍可能通过，必要时还要校验响应内容。探测失败先读相应 service 日志，解决启动问题后再开浏览器。

前端首页可访问仍不代表 API 代理配置正确；首次登录或查询时检查实际目标请求。整个任务固定使用同一 origin，别在 localhost / 127.0.0.1 之间切换导致 cookies、localStorage 和回调地址不一致。

### 3.3 开有头会话，并从另一条 command 验证接管

先在 **cwd `.tmp/pw`** 执行 `playwright-cli list`，选择未占用的任务会话名。然后调用 Ally service：

```json
{"action":"start","name":"browser-user-bug","description":"保持可见 Edge 浏览器供本次页面交互排错","command":"playwright-cli -s=ally-user-bug-01 open http://127.0.0.1:5173/ --browser=msedge --headed --idle-timeout=0 && printf 'BROWSER_READY\n' && while :; do sleep 3600; done","cwd":".tmp/pw"}
```

记录浏览器 service id。循环保持父进程存活，直到显式 stop；单独 sleep 3600 会在一小时后退出，不是持续保活。idle-timeout=0 只关闭 CLI 的空闲退出，不能阻止宿主清理。

随后用另一条 command（同样 cwd `.tmp/pw`）执行：

```bash
playwright-cli -s=ally-user-bug-01 --raw eval '() => ({ url: location.href, title: document.title })'
```

若提示会话不存在，先读浏览器 service 日志、核对 cwd 和会话名，不连续重开窗口。需要调整页面尺寸时：`playwright-cli -s=ally-user-bug-01 resize 1600 900`；测响应式问题则使用问题实际发生的尺寸。

后续示例的 command cwd 均为 `.tmp/pw`，不必每次重开浏览器。

### 3.4 探测页面，然后登录、点击、输入

先获取少量结构信息，**不读取输入框的 value，避免输出密码**：

```bash
playwright-cli -s=ally-user-bug-01 --raw eval '() => ({
  url: location.href, title: document.title,
  buttons: [...document.querySelectorAll("button")].slice(0,20).map(e => ({text:e.innerText.trim().slice(0,80),aria:e.getAttribute("aria-label")})),
  inputs: [...document.querySelectorAll("input,textarea")].slice(0,20).map(e => ({type:e.type,id:e.id,name:e.name,placeholder:e.placeholder,aria:e.getAttribute("aria-label"),labels:[...(e.labels || [])].map(l=>l.innerText.trim().slice(0,80))})),
  tables: document.querySelectorAll("table").length
})'
```

需要语义结构再 `snapshot --depth=2`，或对小型登录表单、当前可见弹窗取子树。上述 eval 只探测 DOM，不代替可见性和唯一性检查；有多个匹配时按表单、弹窗、标签收窄。

确认实际 placeholder 后才填：

```bash
playwright-cli -s=ally-user-bug-01 fill "getByPlaceholder('用户名', { exact: true })" '<测试用户名>'
playwright-cli -s=ally-user-bug-01 fill "getByPlaceholder('密码', { exact: true })" '<测试密码>'
```

有 label 时优先 `getByLabel('密码', { exact: true })`；有正确 accessible name 时可用 role。**placeholder、HTML name 和 accessible name 不等价**，不要仅看到 placeholder 就照抄 getByRole 的 name。

点击登录并等真实登录结果，示例要求跳转 `/dashboard` 且显示“工作台”标题；当前项目的路由和标识必须替换：

```bash
playwright-cli -s=ally-user-bug-01 --raw run-code 'async page => {
  await page.getByRole("button", { name: /登\s*录/ }).click();
  await page.waitForURL(url => url.pathname === "/dashboard", { timeout: 10000 });
  await page.getByRole("heading", { name: "工作台", exact: true }).waitFor({ state: "visible", timeout: 10000 });
  return { loggedIn: true, url: page.url() };
}'
```

条件不满足就保留失败，检查校验提示、目标登录请求和 console。验证码/二次认证需要人工时保留可见窗口；已有登录态且本次不测登录，可验证登录身份后直接进入目标页。

### 3.5 等本次操作完成，验证页面事实

**只需 UI 结果时，优先等明确的 UI 条件**，不必每次强加接口等待。需要验证接口时，先注册等待再点击；按实际 origin、路径、方法和必要的查询参数/请求体匹配，避免被轮询或另一次请求满足。

下面示例约定：当前已进入用户列表且初始加载完成，存在测试记录 `ally-e2e-001`；“搜索用户”输入框、GET `/api/users?keyword=...` 和表格行均已从源码或页面确认。初始列表必须不同于目标结果，避免旧行直接满足断言：

```bash
playwright-cli -s=ally-user-bug-01 --raw run-code 'async page => {
  const keyword = "ally-e2e-001";
  const origin = new URL(page.url()).origin;
  const oldRows = await page.locator("tbody tr").allInnerTexts();
  if (oldRows.length === 1 && oldRows[0].includes(keyword)) throw new Error("当前已是目标结果，请先建立不同的列表基线或选择另一测试记录");
  await page.getByPlaceholder("搜索用户", { exact: true }).fill(keyword);
  const [response] = await Promise.all([
    page.waitForResponse(r => {
      const u = new URL(r.url());
      return u.origin === origin && u.pathname === "/api/users" &&
        u.searchParams.get("keyword") === keyword && r.request().method() === "GET";
    }, { timeout: 10000 }),
    page.getByRole("button", { name: /查\s*询/ }).click()
  ]);
  if (!response.ok()) throw new Error(`查询接口 HTTP ${response.status()}`);
  await page.waitForFunction(expected => {
    const rows = [...document.querySelectorAll("tbody tr")];
    return rows.length === 1 && rows[0].innerText.includes(expected);
  }, keyword, { timeout: 10000 });
  return { status: response.status(), rows: await page.locator("tbody tr").count(), firstRow: (await page.locator("tbody tr").first().innerText()).slice(0,300) };
}'
```

前后端分离直连接口时，把 origin 改为实际 API origin。HTTP 200 若包含业务错误码，要按实际响应结构额外断言；不能把任意 2xx 当作业务成功。waitForResponse 收到响应头即可完成，也不代表响应体处理或页面渲染完毕。

行数会受空态占位行、隐藏表格、固定列副本、分页和虚拟列表影响；实际项目应限定目标表格，并检查预期文案、字段值、加载结束状态或结果版本。旧页面已经满足条件时，增加能证明**本次**变化的条件。不能用“任意 API 成功”“行数大于 0”“页面有文字”作为通用成功判据。

### 3.6 检查错误、持久化和视觉结果

操作前按需保存已有日志，再 `console --clear` / `requests --clear`，让本轮证据易于识别。操作后分开执行并先确认命令成功：

```bash
playwright-cli -s=ally-user-bug-01 console error
playwright-cli -s=ally-user-bug-01 requests --filter 'api'
```

需要细节时用返回的请求编号执行 `request <编号>` / `response-body <编号>`，只取相关请求；不要把所有请求体或认证头倒进上下文。

- 区分 HTTP 4xx/5xx、网络失败（如 CORS / ERR_*）、HTTP 200 业务错误和页面渲染错误。不要用 `grep ... || echo no errors`，它会把 CLI 失败与没有匹配混为一谈。
- console 报错不保证包含所有接口错误；表单校验也可能产生 console.error，要结合步骤判断。需要完整诊断时见第十章的事件采集。
- 保存、编辑、删除后不仅检查 toast，还要检查实际字段/列表；需确认落库时刷新或重新打开详情，验证数据仍正确，避免把本地乐观更新当作持久化成功。
- 短暂 toast 应在提交前安排等待，或与提交及读取放在同一次 run-code，不要隔很多条命令才查。toast 通常只作辅助证据。
- 页面结果确认后截图：`playwright-cli -s=ally-user-bug-01 screenshot --filename=bug-01.png`，再用 Ally read 查看。布局、遮挡、溢出、主题和鼠标点击问题必须看实际画面，DOM 文本无法替代。

报告要说明复现步骤、预期/实际、相关接口或错误、截图位置；没等到结果就如实说未验证成功，不能只因命令执行过就说通过。

### 3.7 重复验证与收尾

需反复执行时用 run-code --filename，模板见第十章。明确初始 URL、登录态和测试数据；刷新当前页面不等于恢复初始状态。非幂等提交后先查结果，不能因等待超时就重复提交。

结束时先 `playwright-cli -s=ally-user-bug-01 close`，再对本次创建的浏览器、前端、后端 service **逐个**调用：

```json
{"action":"stop","id":"<本次创建的实际 service id>"}
```

复用的已有服务不停止；用户需要继续操作或查看时，按其要求保留窗口及所需服务，交代会话名、service id 与停止方式。不用 close-all / kill-all 做例行清理。

## 4. 会话作用域与一次性模式

- **保持 cwd 一致**。0.1.22 从 cwd 向上查找 `.playwright` 目录作为 CLI workspace；没有找到时可能共享默认范围。因此会话名既不是一律全局，也不是每个 cwd 独立。list 查当前范围，list --all 才跨 workspace 汇总。不要在中途新增/移动 `.playwright` 改变会话归属。
- open 同名已存在会话可能重启它；先 list，使用任务前缀。close-all 会批量关闭当前 CLI 范围内会话，共享范围可能包含其他任务；kill-all 范围更广。
- idle-timeout 单位是**毫秒**，0 禁用空闲退出；无头默认一小时、有头默认禁用。它不替代第三章的 holder。
- service 活跃上限为全局 32；记录并释放自己的服务，别重复占槽。

步骤已确定的一次性检查可以在**一条 command** 中完成：

```bash
set -e
trap 'playwright-cli -s=ally-smoke-01 close' EXIT
playwright-cli -s=ally-smoke-01 open http://127.0.0.1:5173/ --browser=msedge
playwright-cli -s=ally-smoke-01 run-code --filename=check.js
```

check.js 必须有真实页面断言，非零退出即失败。不要把所有子命令 `>/dev/null 2>&1` 后继续执行；需要减少输出时保留失败日志和退出码。多次 command 的交互探索使用第三章的 service 模式。

## 5. 读页面的成本控制

以下是旧实测规模参考，不是输出上限：

| 手段 | 典型输出 | 使用场景 |
|---|---|---|
| 有限字段的 --raw eval | 数十字节至数 KB | URL、状态、计数、少量文案；大小取决于返回值 |
| snapshot --depth=1 / --depth=2 | 小页面约 0.2–0.5 KB | 骨架与语义定位 |
| 可见弹窗子树 | 约 0.6 KB 起 | 对话框按钮、字段 |
| 小页面全量 snapshot | 约 3 KB 起 | 页面确实很小时 |
| 大表格 snapshot | 300 行约 42 KB，1200 行约 171 KB | 避免默认全量读取 |

浅层快照按树深度截，不按行数截；snapshot table 也可能几十 KB。先 eval 总数和 slice(0,5)，大内容用 `snapshot --filename=page.yml` 落盘再按需读。浅层快照没显示目标时再定点查找，不反复扩大整页深度。

```bash
playwright-cli -s=ally-user-bug-01 snapshot --depth=2
playwright-cli -s=ally-user-bug-01 snapshot '[role=dialog]:visible'
playwright-cli -s=ally-user-bug-01 find '保存'
playwright-cli -s=ally-user-bug-01 --raw eval '() => ({ count: document.querySelectorAll("tbody tr").length, sample: [...document.querySelectorAll("tbody tr")].slice(0,5).map(e=>e.innerText.slice(0,200)) })'
```

## 6. 定位器与点击失败

- 优先实际 label、role/name、placeholder、test id，再用当前快照 ref 或稳定 CSS；避免长层级 CSS、坐标、随意 first()/last()。重复时限定目标容器、可见状态和业务文本，不能靠索引碰巧点中。
- role 的 name 字符串默认是非精确匹配，需要整串时加 exact: true；正则默认区分大小写，要忽略时显式 `/.../i`。
- AntD 两字按钮在某些类型/配置下会插空格，先读真实文字，必要时 `/登\s*录/`。图标也可能进入 accessible name；不要把某个项目的按钮配置当组件库定律。
- ref 只在对应快照仍有效时使用；导航、重渲染、重新生成快照后可能失效。generate-locator 可生成候选，但仍要检查唯一性和语义，生成的 CSS 不自动变得稳定。
- CLI 基线动作超时约 5 秒，导航/自定义等待另算，配置可改。超时可能是不存在、隐藏、禁用、重复或被遮挡，不只是“页面慢”。先看错误中的定位与 actionability 日志。
- intercepts pointer events：先截图和识别遮挡者，确认浮层、加载遮罩与控件状态。组件支持的键盘路径可以用，但测鼠标点击问题时不能据此宣布修复。
- click({ force: true }) 只用于已理解原因的诊断或明确需要的操作；它绕过部分检查，不保证点击到目标，不能作为普通失败重试策略。

## 7. 等待规则

- 导航通常用 domcontentloaded，然后等页面专属的就绪标识；不要依赖 networkidle，后台轮询可能让它一直不满足。
- 等显示/消失：`locator.waitFor({ state: 'visible' / 'hidden', timeout: ... })`；等 URL：`page.waitForURL(...)`；等 DOM 事实：`page.waitForFunction(fn, arg, { timeout: ... })`。
- isVisible()、count()、即时 eval 是状态读取，不是等待业务完成。加载前就不存在的 spinner 立即满足 hidden，也不能证明请求已经开始并结束。
- 事件型等待（response、popup、download 等）在触发动作**之前注册**；使用第三章的 Promise.all([等待, 动作])，再检查具体结果。response 匹配不要只写 /api/ 或仅筛成功状态。
- CLI 没有通用 wait 子命令；在 run-code 中使用条件等待。固定 sleep / waitForTimeout 只用于已说明的时间行为诊断，不作成功判据。不要 `.catch(() => {})` 吞掉导航、等待或断言失败。

## 8. 组件控件、浮层与其他页面

先探测当前项目 DOM 和触发配置，再定位控件。portal 浮层可能位于 body 下，不属于原来的表单容器；关闭后可能只隐藏，检查当前可见浮层，而不是只 count 节点。

| 控件 | 操作与验证 |
|---|---|
| AntD Select / TreeSelect | 先确认角色、可见触发器与下拉状态；正常点击触发器或按组件支持的键盘展开；等可见浮层后按实际选项文本选中，再断言字段值。不要无条件 ArrowDown + Enter 选第一个。 |
| AntD Modal / confirm | 用当前可见 dialog 限定按钮。关闭时可能隐藏或移除，等待 hidden 即可；不要假定所有项目的删除确认都是 Modal.confirm。 |
| AntD Dropdown | hover、click 或 contextmenu 取决于 trigger 配置；按真实配置触发，不套用“默认点击”。 |
| 表格 | 限定目标表格，排除空态/隐藏副本；虚拟列表只能读取已渲染部分，不能用 DOM 行数冒充全部数据。 |
| Element Plus 等 | el-select、el-dialog、el-message 仅作探测线索，按实际结构、标签和可见性决定定位器。 |

例如已确认是单个可见 AntD 下拉、目标选项为“管理员”时：

```bash
playwright-cli -s=ally-user-bug-01 --raw run-code 'async page => {
  const field = page.locator(".ant-form-item").filter({ hasText: "角色" });
  await field.locator(".ant-select-selector").click();
  const option = page.locator(".ant-select-dropdown:visible .ant-select-item-option").filter({ hasText: /^管理员$/ });
  await option.waitFor({ state: "visible", timeout: 5000 });
  await option.click();
  await field.locator(".ant-select-selection-item").filter({ hasText: /^管理员$/ }).waitFor({ state: "visible", timeout: 5000 });
  return { selected: "管理员" };
}'
```

如果存在多个表单项、已展开浮层或不同组件结构，先收窄/分支，不盲抄选择器。提交和提交后验证另按第三章处理。

新标签页用 tab-list 确认后 tab-select；iframe 内元素用 run-code 的 frameLocator。原生 JS alert/confirm 不在普通 DOM 中，按实际提示用 dialog-accept / dialog-dismiss，不要当成组件弹窗找。

## 9. 登录态复用

不测登录时可以复用已确认的登录态；测登录、权限或会话失效时，必须明确初始状态，避免旧身份掩盖问题。

```bash
playwright-cli -s=ally-user-bug-01 state-save auth.json
```

新会话先按第三章建立 holder，再在同一 cwd 执行 state-load auth.json，然后导航目标 URL 并验证当前身份。一次性模式则 open、load、goto、断言全部放在同一条 command。不要加载状态后不重新导航就期待页面自行更新。

- state 文件可保存 cookies / localStorage；sessionStorage 不随普通 storage state 保存，IndexedDB 是否保存取决于版本和选项。不要假定站点的全部认证状态都能还原。
- state 文件路径相对 command cwd，含登录凭据，不提交仓库或输出正文；使用专用 scratch 目录，按任务需要处理留存。
- 接口 token 注入只适合已知存储协议、且不测登录 UI 的开发场景，注入后仍须验证身份。不要因为注入成功就说登录页面通过。
- 验证码/二次认证可让用户在可见窗口完成，再保存可复用状态；本地测试项目已有受支持的测试验证码流程时遵从项目约定。

## 10. 可重复脚本与错误采集

多步或反复操作写入 `.tmp/pw/check.js`，内容是一个 async page 函数表达式，不是模块，不写 require / process / module.exports。执行时 cwd `.tmp/pw`：

```bash
playwright-cli -s=ally-user-bug-01 --raw run-code --filename=check.js
```

下面是**复用已有登录态、只读检查用户列表**的模板。前提：已登录、/system/users 和查询契约与第三章一致、测试记录已存在；页面初始表格已就绪，且不等于筛选后的目标结果（若有异步初始加载，应在读取基线前加其就绪条件）。每次明确导航同一业务起点，不执行新增/删除；若跳回登录页就失败，不自动掩盖认证问题。

```js
async page => {
  const issues = { console: [], pageErrors: [], network: [], http: [] };
  const keep = (bucket, value) => { if (bucket.length < 50) bucket.push(value); };
  const onConsole = m => { if (m.type() === 'error') keep(issues.console, m.text().slice(0,500)); };
  const onPageError = e => keep(issues.pageErrors, e.message.slice(0,500));
  const onRequestFailed = r => keep(issues.network, { url: r.url().split('?')[0], error: r.failure()?.errorText });
  const onResponse = r => { if (r.status() >= 400) keep(issues.http, { url: r.url().split('?')[0], status: r.status() }); };
  page.on('console', onConsole);
  page.on('pageerror', onPageError);
  page.on('requestfailed', onRequestFailed);
  page.on('response', onResponse);
  try {
    const origin = new URL(page.url()).origin;
    const keyword = 'ally-e2e-001';
    await page.goto(origin + '/system/users', { waitUntil: 'domcontentloaded' });
    await page.getByPlaceholder('搜索用户', { exact: true }).waitFor({ state: 'visible', timeout: 10000 });
    if (new URL(page.url()).pathname !== '/system/users') throw new Error('未进入目标用户页');
    const oldRows = await page.locator('tbody tr').allInnerTexts();
    if (oldRows.length === 1 && oldRows[0].includes(keyword)) throw new Error('当前已是目标结果，请先建立不同的列表基线或选择另一测试记录');
    await page.getByPlaceholder('搜索用户', { exact: true }).fill(keyword);
    const [response] = await Promise.all([
      page.waitForResponse(r => {
        const u = new URL(r.url());
        return u.origin === origin && u.pathname === '/api/users' &&
          u.searchParams.get('keyword') === keyword && r.request().method() === 'GET';
      }, { timeout: 10000 }),
      page.getByRole('button', { name: /查\s*询/ }).click()
    ]);
    if (!response.ok()) throw new Error(`查询接口 HTTP ${response.status()}`);
    await page.waitForFunction(expected => {
      const rows = [...document.querySelectorAll('tbody tr')];
      return rows.length === 1 && rows[0].innerText.includes(expected);
    }, keyword, { timeout: 10000 });
    return { url: page.url(), matched: true, rows: await page.locator('tbody tr').count(), issues };
  } finally {
    page.off('console', onConsole);
    page.off('pageerror', onPageError);
    page.off('requestfailed', onRequestFailed);
    page.off('response', onResponse);
  }
}
```

matched 只表示模板中的结果断言通过，issues 仍要逐项判读。失败直接抛错，CLI 的 console / requests 继续用于诊断；需逐页汇总时可捕获并返回明确的 ok:false 与错误，调用方必须检查，不能吞错。

监听器在每次运行结束后解绑，采集数组限长；HTTP 错误、网络失败、未捕获 JS 异常分别采集，不能只监听 console。导航不会清空 cookies/localStorage 或后端数据；要重置时明确使用测试身份与项目支持的数据准备流程。

## 11. 证据与调试

- 截图后实际用 Ally read 看图。可指定单个目标截图；需要看溢出/遮挡时包含相关周边，避免局部截图裁掉问题。
- console / requests 会随操作累积；清理前保留相关证据，清理后再复现。不要仅凭已有旧错误认定本次动作失败。
- requests --filter api 是 URL 正则筛选，不保证覆盖所有业务接口；按项目路径选择。网络记录过多时分段清理，失败时进一步读目标请求。
- show 打开的是 **Playwright Dashboard**，不等价于把原无头浏览器改成 headed；用户需要原浏览器窗口时从一开始用 --headed。show --annotate 会阻塞等待用户标注，只在确实需要反馈时使用。
- 复杂问题可用 tracing-start / tracing-stop，人工示范可用 recording-start / recording-stop；按需使用，不必每轮开启。

## 12. 错误恢复速查

| 现象 | 下一步 |
|---|---|
| command not found | 按第一章处理安装，不凭空替换成另一套工具。 |
| 会话不存在 | 查自己的浏览器 service 状态/日志、会话名、cwd / workspace；确认 holder 活着后再操作。 |
| E_CWD_INVALID / description is required | 先建目录；service start 补齐 description，按真实工具参数调用。 |
| channel not found / browser not installed | 按第二章确认 channel 与安装情况，不循环启动。 |
| ref not found | 重新获取小快照或改用已确认的 locator。 |
| strict mode violation / 多个匹配 | 按目标容器、可见性与语义收窄，不随便 first/last。 |
| intercepts pointer events | 查遮挡者与页面状态，保留截图；不要默认 force。 |
| 动作/条件超时 | 区分定位、登录重定向、禁用、接口失败与 UI 未更新；先看证据，不重复点击提交。 |
| 页面空白 / 结果没变 | 查实际 URL、console、目标请求及容器；条件等待，不能固定睡几秒就报通过。 |
| 参数不支持 | 查当前子命令 --help 和配置，保留原错误。 |

## 13. 速查卡（已建 holder，同一 cwd）

```bash
playwright-cli list
playwright-cli -s=ally-user-bug-01 --raw eval '() => ({ url: location.href, title: document.title })'
playwright-cli -s=ally-user-bug-01 snapshot --depth=2
playwright-cli -s=ally-user-bug-01 find '保存'
playwright-cli -s=ally-user-bug-01 click "getByRole('button', { name: /保\s*存/ })"
playwright-cli -s=ally-user-bug-01 run-code --filename=check.js
playwright-cli -s=ally-user-bug-01 console error
playwright-cli -s=ally-user-bug-01 requests --filter 'api'
playwright-cli -s=ally-user-bug-01 screenshot --filename=result.png
```

这些是独立命令的写法，不是一条可无条件串完的测试。点击后先按第三章验证结果；需要新页面时 goto 实际完整 URL，不再次 open 重启已有会话。收尾见 3.7。

## 14. Git Bash 与 Ally 运行约束

- **路径**：forward slash，Windows 绝对路径可用 C:/...；产物和状态文件放专用 scratch 目录，注意仓库实际的 ignore 规则，不把认证文件提交。
- **MSYS 路径转换**：传给 Windows node 的前导 / 参数可能被 Git Bash 改写，即使有引号。例如 `--filter '/api/'`；用 `--filter 'api'`，或在该命令前设 `MSYS_NO_PATHCONV=1`。文件尽量用相对路径或 C:/...，不要把 shell /tmp 与 Windows 路径混用。
- **JS 引号**：内联代码优先 Bash 单引号包裹、JS 内部用双引号；复杂逻辑写 js 文件。不要让 `$()`、反引号、$变量 被 Bash 解释；JS 文件里的正则只写一层 `\s`，不要复制其他语言字符串的转义。
- **超时**：command 的 timeout 应覆盖导航与整段条件等待。超时被 Ally 收编为 service 后，先记录返回的 service id、read/list 诊断；不要马上重跑导致重复提交、端口或会话冲突。
- **清理归属**：只关闭自己的 session、停止自己创建的服务；close-all、kill-all、delete-data 不作常规恢复手段。mock route、离线状态等会在会话内影响后续实验，记录并在结束前恢复。

参数依据：[CLI 命令](https://playwright.dev/agent-cli/capabilities)、[会话与作用域](https://playwright.dev/agent-cli/sessions)、[Playwright 响应等待](https://playwright.dev/docs/api/class-page#page-wait-for-response)。版本冲突时以本机 help、配置和实际输出为准。
