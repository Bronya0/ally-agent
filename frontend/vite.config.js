import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import Components from 'unplugin-vue-components/vite';
import AutoImport from 'unplugin-auto-import/vite';
import { NaiveUiResolver } from 'unplugin-vue-components/resolvers';

function pad2(value) {
  return String(value).padStart(2, '0');
}

function buildVersion(date = new Date()) {
  return [
    'v',
    date.getFullYear(),
    pad2(date.getMonth() + 1),
    pad2(date.getDate()),
    '-',
    pad2(date.getHours()),
    pad2(date.getMinutes()),
    pad2(date.getSeconds()),
  ].join('');
}

const allyBuildVersion = process.env.ALLY_BUILD_VERSION || buildVersion();

export default defineConfig({
  plugins: [
    vue(),
    Components({
      resolvers: [NaiveUiResolver()],
    }),
    AutoImport({
      imports: [
        'vue',
        {
          'naive-ui': [
            'useMessage',
            'useDialog',
            'useNotification',
          ],
        },
      ],
      dts: false,
    }),
  ],
  define: {
    __ALLY_BUILD_VERSION__: JSON.stringify(allyBuildVersion),
    // 新版本首次启动要展示的更新日志：正文来自 release 事件（workflow 经
    // ALLY_RELEASE_NOTES_BODY 传下来），这里只做一次字面量注入，不读盘不联网。
    // “这一版该不该弹”由前端 releaseNotes.mjs 判定。
    __ALLY_RELEASE_NOTES__: JSON.stringify({
      version: allyBuildVersion,
      body: process.env.ALLY_RELEASE_NOTES_BODY || '',
    }),
  },
  build: {
    chunkSizeWarningLimit: 1200,
  },
});
