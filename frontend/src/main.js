import { createApp } from 'vue';
import App from './App.vue';
import { locale, t } from './i18n.mjs';
import { initTheme } from './utils/theme.mjs';
import './style.css';

document.documentElement.lang = locale;
initTheme();

const app = createApp(App);
app.config.globalProperties.$t = t;
app.mount('#app');
