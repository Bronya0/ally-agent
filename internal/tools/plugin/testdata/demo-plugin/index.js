/*
 * 演示插件（Ally 插件系统 v1）。
 *
 * 这个文件刻意不用任何打包工具、不引任何依赖：插件就是普通的 ES module，导出
 * mount(element, host) 即可。三件事在这里被验证：
 *   1. 自己的资源（style.css）通过 import.meta.url 相对加载 —— 模块 URL 与页面同源，
 *      所以相对路径天然可用；
 *   2. 主题靠 CSS 变量自动跟随（style.css 里全是 var(--ally-*)），JS 里要色值时用
 *      host.ui.theme.snapshot()/on()；
 *   3. 所有后端能力只经 host：存储、HTTP（受 plugin.json 的 permissions.http 约束）、
 *      工作区只读。
 */

const assetUrl = (relative) => new URL(relative, import.meta.url).toString();

function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

// 插件自己的样式表：fetch 后塞进 <style>，卸载时移除（返回清理函数）。
async function injectStylesheet() {
  const response = await fetch(assetUrl('./style.css'));
  if (!response.ok) throw new Error(`读取 style.css 失败：HTTP ${response.status}`);
  const tag = document.createElement('style');
  tag.dataset.plugin = 'demo-toolkit';
  tag.textContent = await response.text();
  document.head.appendChild(tag);
  return () => tag.remove();
}

function renderShell(element) {
  element.innerHTML = `
    <div class="demo-wrap">
      <div class="demo-title">
        演示插件 <span class="demo-sub">plugin v1 · 纯 JS，无依赖</span>
      </div>

      <div class="demo-card">
        <div class="demo-card-title">1. 主题跟随</div>
        <div class="demo-row">
          <span class="demo-swatch"></span>
          <span>当前主题：<b id="demo-theme-name">-</b> / 明暗：<b id="demo-theme-mode">-</b> / accent：<code id="demo-theme-accent">-</code></span>
        </div>
        <div class="demo-sub">去「设置 → 外观」换主题或明暗，这里（含这张卡片）会立刻跟着变。</div>
        <pre class="demo-pre" id="demo-theme-tokens"></pre>
      </div>

      <div class="demo-card">
        <div class="demo-card-title">2. 插件存储（host.store，落在插件自己的数据文件里）</div>
        <div class="demo-row">
          <button class="demo-btn" id="demo-store-read">读取</button>
          <button class="demo-btn primary" id="demo-store-write">写入 {hello, at}</button>
          <button class="demo-btn" id="demo-store-clear">清空</button>
        </div>
        <pre class="demo-pre" id="demo-store-out">-</pre>
      </div>

      <div class="demo-card">
        <div class="demo-card-title">3. 后端 HTTP 代理（只放行 plugin.json 里声明的域名）</div>
        <div class="demo-row">
          <input class="demo-input" id="demo-http-url" value="https://example.com/" />
          <button class="demo-btn primary" id="demo-http-send">请求</button>
          <button class="demo-btn" id="demo-http-denied">请求未声明域名（应被拒）</button>
        </div>
        <pre class="demo-pre" id="demo-http-out">-</pre>
      </div>

      <div class="demo-card">
        <div class="demo-card-title">4. 工作区（host.files，只读）</div>
        <div class="demo-row">
          <button class="demo-btn" id="demo-files-list">列出当前工作区根目录</button>
        </div>
        <pre class="demo-pre" id="demo-files-out">-</pre>
      </div>

      <div class="demo-card">
        <div class="demo-card-title">5. 宿主环境</div>
        <pre class="demo-pre" id="demo-env-out">-</pre>
      </div>
    </div>
  `;
}

export async function mount(element, host) {
  const removeStyles = await injectStylesheet();
  renderShell(element);

  const $ = (id) => element.querySelector(`#${id}`);
  const setOutput = (id, text, ok) => {
    const node = $(id);
    node.textContent = text;
    node.classList.toggle('demo-ok', ok === true);
    node.classList.toggle('demo-bad', ok === false);
  };

  // ── 1. 主题 ──
  const renderTheme = (snapshot) => {
    $('demo-theme-name').textContent = snapshot.name;
    $('demo-theme-mode').textContent = snapshot.mode;
    $('demo-theme-accent').textContent = snapshot.tokens['--ally-accent'] || '-';
    $('demo-theme-tokens').textContent = [
      '--ally-surface-panel : ' + snapshot.tokens['--ally-surface-panel'],
      '--ally-text-body     : ' + snapshot.tokens['--ally-text-body'],
      '--ally-accent        : ' + snapshot.tokens['--ally-accent'],
    ].join('\n');
  };
  renderTheme(host.ui.theme.snapshot());
  const offTheme = host.ui.theme.on(renderTheme);

  // ── 2. 存储 ──
  const showStore = async () => {
    const value = await host.store.get();
    setOutput('demo-store-out', value === null ? '（还没有数据）' : JSON.stringify(value, null, 2), true);
  };
  $('demo-store-read').addEventListener('click', () => {
    showStore().catch((err) => setOutput('demo-store-out', String(err.message || err), false));
  });
  $('demo-store-write').addEventListener('click', () => {
    host.store
      .set({ hello: 'world', at: new Date().toISOString() })
      .then(showStore)
      .catch((err) => setOutput('demo-store-out', String(err.message || err), false));
  });
  $('demo-store-clear').addEventListener('click', () => {
    host.store
      .set(null)
      .then(showStore)
      .catch((err) => setOutput('demo-store-out', String(err.message || err), false));
  });

  // ── 3. HTTP（白名单在宿主侧判定） ──
  const sendRequest = async (url) => {
    setOutput('demo-http-out', `请求 ${url} …`, null);
    try {
      const result = await host.http.request({ method: 'GET', url, maxBytes: 20000 });
      const body = String(result.body || '').slice(0, 600);
      setOutput(
        'demo-http-out',
        `HTTP ${result.status} ${result.statusText || ''} · ${result.bytesRead} bytes\nContent-Type: ${result.contentType || '-'}\n\n${body}`,
        result.status >= 200 && result.status < 300,
      );
    } catch (err) {
      setOutput('demo-http-out', `被拒或失败：${err.message || err}`, false);
    }
  };
  $('demo-http-send').addEventListener('click', () => sendRequest($('demo-http-url').value.trim()));
  $('demo-http-denied').addEventListener('click', () => sendRequest('https://not-declared.invalid/'));
  // 内网地址也应该被拒：白名单只声明了 example.com。
  $('demo-http-url').addEventListener('keydown', (event) => {
    if (event.key === 'Enter') sendRequest($('demo-http-url').value.trim());
  });

  // ── 4. 工作区只读 ──
  $('demo-files-list').addEventListener('click', async () => {
    try {
      const entries = await host.files.list('.', { limit: 50 });
      const lines = entries
        .slice(0, 30)
        .map((entry) => `${entry.dir ? '📁' : '📄'} ${entry.name}`);
      setOutput('demo-files-out', `${entries.length} 个条目：\n${lines.join('\n')}`, true);
    } catch (err) {
      setOutput('demo-files-out', String(err.message || err), false);
    }
  });

  // ── 5. 环境 ──
  setOutput(
    'demo-env-out',
    JSON.stringify(
      {
        appVersion: host.app.version,
        locale: host.app.locale,
        workspace: host.app.workspace,
        themeTokens: host.ui.theme.tokens().length,
      },
      null,
      2,
    ),
    true,
  );

  // 卸载清理：取消主题订阅、移除样式表（容器由宿主清空）。
  return () => {
    offTheme();
    removeStyles();
  };
}
