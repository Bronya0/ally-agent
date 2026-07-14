<template>
  <div class="html-render-card">
    <div v-if="msg.title" class="html-render-title">{{ msg.title }}</div>
    <div class="html-render-frame-wrapper">
      <iframe
        ref="frameRef"
        class="html-render-frame"
        sandbox="allow-same-origin"
        :style="{ minHeight: frameHeight + 'px' }"
        @load="adjustHeight"
      ></iframe>
    </div>
  </div>
</template>

<script setup>
import { ref, watch, nextTick, onMounted } from 'vue';

const props = defineProps({
  msg: { type: Object, required: true },
});

const frameRef = ref(null);
const frameHeight = ref(200);

function writeHTML() {
  const frame = frameRef.value;
  if (!frame) return;
  const doc = frame.contentDocument;
  if (!doc) return;
  const html = props.msg.htmlContent || '';
  const wrapped = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Noto Sans CJK SC", "Microsoft YaHei", sans-serif;
    background: transparent;
    color: #e5e5f0;
    padding: 12px;
    font-size: 14px;
    line-height: 1.5;
    overflow: hidden;
  }
  a { color: #a78bfa; text-decoration: none; }
  a:hover { text-decoration: underline; }
  table { border-collapse: collapse; width: 100%; margin: 8px 0; }
  th, td { border: 1px solid rgba(255,255,255,0.1); padding: 6px 10px; text-align: left; }
  th { background: rgba(255,255,255,0.05); font-weight: 500; }
  svg { max-width: 100%; height: auto; }
  pre { background: rgba(255,255,255,0.05); padding: 8px; border-radius: 4px; overflow-x: auto; }
  code { font-family: "SF Mono", "Fira Code", Consolas, monospace; font-size: 13px; }
  img { max-width: 100%; }
</style>
</head>
<body>${html}</body>
</html>`;
  doc.open();
  doc.write(wrapped);
  doc.close();
  nextTick(adjustHeight);
}

function adjustHeight() {
  const frame = frameRef.value;
  if (!frame) return;
  try {
    const doc = frame.contentDocument;
    if (!doc) return;
    const body = doc.body;
    if (!body) return;
    const height = Math.max(body.scrollHeight, body.offsetHeight, 100);
    frameHeight.value = Math.min(height + 24, 600);
  } catch (e) {
    // ignore cross-origin errors
  }
}

onMounted(() => {
  writeHTML();
});

watch(() => props.msg.htmlContent, () => {
  writeHTML();
});
</script>

<style scoped>
.html-render-card {
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  overflow: hidden;
  margin: 8px 0;
  background: rgba(255, 255, 255, 0.02);
}

.html-render-title {
  padding: 8px 12px;
  font-size: 13px;
  font-weight: 500;
  color: #a78bfa;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.html-render-frame-wrapper {
  width: 100%;
}

.html-render-frame {
  width: 100%;
  border: none;
  display: block;
  background: transparent;
}
</style>
