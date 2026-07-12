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
  },
  build: {
    chunkSizeWarningLimit: 1200,
  },
});
