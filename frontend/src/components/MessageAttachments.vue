<template>
  <div v-if="attachments.length" class="attachment-grid">
    <div v-for="att in attachments" :key="att.id || att.name" class="attachment-item">
      <img v-if="att.kind === 'image' && attachmentSource(att)" :src="attachmentSource(att)" :alt="att.name" />
      <video v-else-if="att.kind === 'video' && attachmentSource(att)" :src="attachmentSource(att)" controls preload="metadata"></video>
      <audio v-else-if="att.kind === 'audio' && attachmentSource(att)" :src="attachmentSource(att)" controls></audio>
      <div v-else class="attachment-file">{{ attachmentIcon(att) }}</div>
      <div class="attachment-meta">
        <span class="attachment-name" :title="attachmentTitle(att)">{{ att.name }}</span>
        <span class="attachment-size">{{ attachmentState(att) }}</span>
      </div>
      <pre v-if="att.kind === 'text' && att.text" class="attachment-text-preview">{{ textPreview(att.text) }}</pre>
    </div>
  </div>
</template>

<script setup>
defineProps({
  attachments: { type: Array, default: () => [] },
});

function attachmentIcon(att) {
  if (att.kind === 'image') return 'IMG';
  if (att.kind === 'video') return 'VID';
  if (att.kind === 'audio') return 'AUD';
  if (att.kind === 'text') return 'TXT';
  return 'FILE';
}

function attachmentSource(att) {
  return att.previewUrl || att.url || att.dataUrl || '';
}

function attachmentState(att) {
  const parts = [fmtBytes(att.size)];
  if (att.text) parts.push('文本');
  if (att.truncated) parts.push('已裁剪');
  return parts.filter(Boolean).join(' · ');
}

function attachmentTitle(att) {
  return [att.name, att.type, att.error].filter(Boolean).join('\n');
}

function textPreview(text) {
  const source = String(text || '').trim();
  return source.length > 600 ? `${source.slice(0, 600)}\n...` : source;
}

function fmtBytes(size) {
  const n = Number(size || 0);
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
</script>
