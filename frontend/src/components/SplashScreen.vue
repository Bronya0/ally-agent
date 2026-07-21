<template>
  <div :class="['splash-screen', { leaving }]" @click="finish">
    <canvas ref="canvasRef" class="splash-canvas"></canvas>
    <div class="splash-hud">
      <div class="build-version">{{ buildVersion }}</div>
      <div class="splash-line">{{ $t('splash.loading') }}</div>
    </div>
  </div>
</template>

<script setup>
import { nextTick, onMounted, onUnmounted, ref } from 'vue';
import { buildVersion } from '../utils/buildVersion';

const emit = defineEmits(['done']);

const canvasRef = ref(null);
const leaving = ref(false);

let raf = 0;
let timer = 0;
let resizeHandler = null;
let finished = false;

const duration = 2800;

function clamp(value, min = 0, max = 1) {
  return Math.min(max, Math.max(min, value));
}

function smoothstep(value) {
  const t = clamp(value);
  return t * t * (3 - 2 * t);
}

function drawRoundRect(ctx, x, y, width, height, radius) {
  const r = Math.min(radius, width / 2, height / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + width, y, x + width, y + height, r);
  ctx.arcTo(x + width, y + height, x, y + height, r);
  ctx.arcTo(x, y + height, x, y, r);
  ctx.arcTo(x, y, x + width, y, r);
  ctx.closePath();
}

function drawBackground(ctx, width, height, progress, pulse) {
  const bg = ctx.createRadialGradient(width * 0.5, height * 0.42, 0, width * 0.5, height * 0.42, Math.max(width, height));
  bg.addColorStop(0, '#151922');
  bg.addColorStop(0.42, '#080a0f');
  bg.addColorStop(1, '#020305');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, width, height);

  ctx.save();
  ctx.globalAlpha = 0.06 + pulse * 0.05;
  ctx.strokeStyle = '#9ca3af';
  ctx.lineWidth = 1;
  const grid = Math.max(28, height / 18);
  for (let x = ((progress * 90) % grid) - grid; x < width + grid; x += grid) {
    ctx.beginPath();
    ctx.moveTo(x, 0);
    ctx.lineTo(x - height * 0.16, height);
    ctx.stroke();
  }
  for (let y = 0; y < height; y += grid) {
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y + width * 0.05);
    ctx.stroke();
  }
  ctx.restore();

  ctx.save();
  ctx.globalAlpha = 0.08;
  ctx.fillStyle = '#ffffff';
  for (let y = 0; y < height; y += 6) {
    ctx.fillRect(0, y, width, 1);
  }
  ctx.restore();
}

function drawVoid(ctx, x, y, radius, progress, pulse) {
  const r = Math.max(1, radius);
  ctx.save();
  ctx.translate(x, y);
  ctx.globalCompositeOperation = 'lighter';

  const halo = ctx.createRadialGradient(0, 0, r * 0.12, 0, 0, r * 1.9);
  halo.addColorStop(0, `rgba(241, 245, 249, ${0.18 + pulse * 0.12})`);
  halo.addColorStop(0.2, `rgba(224, 164, 88, ${0.2 + pulse * 0.2})`);
  halo.addColorStop(0.56, 'rgba(66, 71, 84, 0.16)');
  halo.addColorStop(1, 'rgba(0, 0, 0, 0)');
  ctx.fillStyle = halo;
  ctx.beginPath();
  ctx.arc(0, 0, r * 1.9, 0, Math.PI * 2);
  ctx.fill();

  ctx.globalCompositeOperation = 'source-over';
  ctx.fillStyle = '#010203';
  ctx.beginPath();
  ctx.arc(0, 0, r * (0.78 + pulse * 0.04), 0, Math.PI * 2);
  ctx.fill();

  ctx.strokeStyle = `rgba(224, 164, 88, ${0.4 + pulse * 0.3})`;
  ctx.lineWidth = Math.max(1.2, r * 0.018);
  for (let i = 0; i < 3; i++) {
    ctx.save();
    ctx.rotate(progress * Math.PI * (0.7 + i * 0.18) + i * 2.1);
    ctx.beginPath();
    ctx.ellipse(0, 0, r * (1.06 + i * 0.18), r * (0.4 + i * 0.07), 0, Math.PI * 0.12, Math.PI * 1.72);
    ctx.stroke();
    ctx.restore();
  }
  ctx.restore();
}

function drawPipe(ctx, sx, sy, ex, ey, bend, progress, delay, pulse) {
  const flow = (progress * 2.4 + delay) % 1;
  const c1x = sx + bend;
  const c1y = sy;
  const c2x = ex - bend * 0.58;
  const c2y = ey;
  ctx.save();
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';

  ctx.strokeStyle = 'rgba(82, 86, 96, 0.62)';
  ctx.lineWidth = 8;
  ctx.beginPath();
  ctx.moveTo(sx, sy);
  ctx.bezierCurveTo(c1x, c1y, c2x, c2y, ex, ey);
  ctx.stroke();

  ctx.strokeStyle = 'rgba(11, 13, 18, 0.88)';
  ctx.lineWidth = 4;
  ctx.beginPath();
  ctx.moveTo(sx, sy);
  ctx.bezierCurveTo(c1x, c1y, c2x, c2y, ex, ey);
  ctx.stroke();

  ctx.globalCompositeOperation = 'lighter';
  ctx.strokeStyle = `rgba(224, 164, 88, ${0.25 + pulse * 0.22})`;
  ctx.lineWidth = 2;
  ctx.setLineDash([18, 32]);
  ctx.lineDashOffset = -flow * 150;
  ctx.beginPath();
  ctx.moveTo(sx, sy);
  ctx.bezierCurveTo(c1x, c1y, c2x, c2y, ex, ey);
  ctx.stroke();
  ctx.restore();
}

function drawEntity(ctx, x, y, size, progress, pulse) {
  const s = size / 180;
  ctx.save();
  ctx.translate(x, y);
  ctx.shadowColor = `rgba(224, 164, 88, ${0.18 + pulse * 0.18})`;
  ctx.shadowBlur = 24;

  ctx.fillStyle = '#090c12';
  ctx.strokeStyle = '#343a45';
  ctx.lineWidth = 2 * s;
  ctx.beginPath();
  ctx.moveTo(-48 * s, -56 * s);
  ctx.lineTo(28 * s, -72 * s);
  ctx.lineTo(68 * s, -28 * s);
  ctx.lineTo(54 * s, 52 * s);
  ctx.lineTo(-34 * s, 70 * s);
  ctx.lineTo(-74 * s, 10 * s);
  ctx.closePath();
  ctx.fill();
  ctx.stroke();

  ctx.fillStyle = '#171b22';
  drawRoundRect(ctx, -38 * s, -36 * s, 84 * s, 72 * s, 8 * s);
  ctx.fill();

  ctx.globalCompositeOperation = 'lighter';
  ctx.strokeStyle = `rgba(224, 164, 88, ${0.42 + pulse * 0.42})`;
  ctx.lineWidth = 2.2 * s;
  ctx.beginPath();
  ctx.moveTo(-24 * s, -5 * s);
  ctx.lineTo(1 * s, -24 * s);
  ctx.lineTo(32 * s, -16 * s);
  ctx.lineTo(13 * s, 4 * s);
  ctx.lineTo(42 * s, 18 * s);
  ctx.stroke();

  ctx.fillStyle = `rgba(241, 245, 249, ${0.78 + pulse * 0.22})`;
  drawRoundRect(ctx, -54 * s, -15 * s, 21 * s, 10 * s, 3 * s);
  ctx.fill();
  drawRoundRect(ctx, -18 * s, 34 * s, 42 * s, 7 * s, 3 * s);
  ctx.fill();

  ctx.globalAlpha = 0.55 + pulse * 0.28;
  ctx.strokeStyle = '#d7dde8';
  ctx.lineWidth = 1 * s;
  ctx.beginPath();
  ctx.moveTo(-58 * s, -44 * s);
  ctx.lineTo(-72 * s, -66 * s);
  ctx.moveTo(44 * s, -58 * s);
  ctx.lineTo(58 * s, -82 * s);
  ctx.stroke();
  ctx.restore();
}

function drawEnergy(ctx, fromX, fromY, toX, toY, size, progress, pulse) {
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.lineCap = 'round';
  for (let i = 0; i < 8; i++) {
    const offset = (i - 3.5) * size * 0.018;
    const wave = Math.sin(progress * 19 + i) * size * 0.055;
    ctx.strokeStyle = `rgba(224, 164, 88, ${0.1 + pulse * 0.13 + i * 0.018})`;
    ctx.lineWidth = i % 3 === 0 ? 2.4 : 1.2;
    ctx.beginPath();
    ctx.moveTo(fromX, fromY + offset);
    ctx.bezierCurveTo(fromX + size * 0.5, fromY - size * 0.18 + wave, toX - size * 0.45, toY + size * 0.16 - wave, toX, toY + offset * 0.3);
    ctx.stroke();
  }
  ctx.restore();
}

function drawWordmark(ctx, width, height, progress, alpha) {
  const fontSize = Math.max(48, Math.min(102, width * 0.085));
  const x = width * 0.5;
  const y = height * 0.78;
  ctx.save();
  ctx.globalAlpha = alpha;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.font = `700 ${fontSize}px Inter, system-ui, sans-serif`;
  const gradient = ctx.createLinearGradient(x - fontSize * 2, y, x + fontSize * 2, y);
  gradient.addColorStop(0, '#f8fafc');
  gradient.addColorStop(0.36, '#e0a458');
  gradient.addColorStop(0.68, '#d7dde8');
  gradient.addColorStop(1, '#f8fafc');
  ctx.fillStyle = gradient;
  ctx.shadowColor = 'rgba(224,164,88,0.2)';
  ctx.shadowBlur = 18;
  ctx.fillText('Ally', x, y);

  const scanX = x - fontSize * 1.6 + ((progress * 2.2) % 1) * fontSize * 3.2;
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = alpha * 0.18;
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(scanX, y - fontSize * 0.42, 4, fontSize * 0.84);
  ctx.restore();
}

function start() {
  const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches;
  if (reduceMotion) {
    emit('done');
    return;
  }

  nextTick(() => {
    if (finished || leaving.value) return;
    const canvas = canvasRef.value;
    const ctx = canvas?.getContext?.('2d');
    if (!canvas || !ctx) {
      emit('done');
      return;
    }

    let width = 0;
    let height = 0;
    let dpr = 1;
    const startedAt = performance.now();

    const resize = () => {
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      width = window.innerWidth;
      height = window.innerHeight;
      canvas.width = Math.max(1, Math.floor(width * dpr));
      canvas.height = Math.max(1, Math.floor(height * dpr));
      canvas.style.width = `${width}px`;
      canvas.style.height = `${height}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };

    resizeHandler = resize;
    resize();
    window.addEventListener('resize', resizeHandler);

    const draw = (time) => {
      const elapsed = time - startedAt;
      const progress = clamp(elapsed / duration);
      const reveal = smoothstep(progress / 0.22);
      const drain = smoothstep((progress - 0.18) / 0.44);
      const exitFlash = smoothstep((progress - 0.78) / 0.14);
      const pulse = 0.5 + 0.5 * Math.sin(progress * Math.PI * 18);
      const size = Math.max(118, Math.min(190, width * 0.16));
      const entityX = width * 0.38;
      const entityY = height * 0.43 + Math.sin(progress * Math.PI * 8) * 5;
      const voidX = width * 0.68;
      const voidY = height * 0.4;
      const voidRadius = Math.max(58, Math.min(112, width * 0.09));

      ctx.clearRect(0, 0, width, height);
      drawBackground(ctx, width, height, progress, pulse);
      drawVoid(ctx, voidX, voidY, voidRadius * reveal, progress, pulse * drain);

      const pipeCount = 7;
      for (let i = 0; i < pipeCount; i++) {
        const lane = (i - (pipeCount - 1) / 2) / pipeCount;
        const sx = entityX - size * (0.38 + (i % 2) * 0.18);
        const sy = entityY - size * 0.28 + lane * size * 1.15;
        const ex = voidX - voidRadius * (0.72 + (i % 2) * 0.12);
        const ey = voidY + lane * voidRadius * 1.55;
        drawPipe(ctx, sx, sy, ex, ey, size * (0.45 + i * 0.02), progress, i * 0.13, pulse * drain);
      }

      drawEnergy(ctx, entityX + size * 0.18, entityY, voidX - voidRadius * 0.25, voidY, size, progress, drain * (0.45 + pulse * 0.55));
      drawEntity(ctx, entityX, entityY, size * (0.9 + reveal * 0.1), progress, pulse * drain);
      drawWordmark(ctx, width, height, progress, clamp((progress - 0.16) / 0.24) * (1 - clamp((progress - 0.86) / 0.1)));

      if (exitFlash > 0) {
        ctx.save();
        ctx.globalCompositeOperation = 'lighter';
        ctx.globalAlpha = exitFlash * 0.28;
        const flash = ctx.createLinearGradient(0, 0, width, 0);
        flash.addColorStop(0, 'rgba(224,164,88,0)');
        flash.addColorStop(0.5, 'rgba(224,164,88,0.16)');
        flash.addColorStop(1, 'rgba(241,245,249,0.22)');
        ctx.fillStyle = flash;
        ctx.fillRect(0, 0, width, height);
        ctx.restore();
      }

      if (!finished && !leaving.value) {
        raf = requestAnimationFrame(draw);
      }
    };

    raf = requestAnimationFrame(draw);
    timer = window.setTimeout(finish, duration + 120);
  });
}

function cleanup() {
  if (raf) cancelAnimationFrame(raf);
  if (timer) window.clearTimeout(timer);
  if (resizeHandler) window.removeEventListener('resize', resizeHandler);
  raf = 0;
  timer = 0;
  resizeHandler = null;
}

function finish() {
  if (finished || leaving.value) return;
  leaving.value = true;
  cleanup();
  window.setTimeout(() => {
    finished = true;
    emit('done');
  }, 420);
}

onMounted(start);
onUnmounted(cleanup);
</script>
