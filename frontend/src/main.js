import { createApp } from 'vue';
import App from './App.vue';
import { locale, t } from './i18n.mjs';
import './style.css';

document.documentElement.lang = locale;

const app = createApp(App);
app.config.globalProperties.$t = t;
app.mount('#app');
