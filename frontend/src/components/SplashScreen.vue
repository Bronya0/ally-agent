<template>
  <div :class="['splash-screen', { leaving }]" @click="finish">
    <canvas ref="canvasRef" class="splash-canvas"></canvas>
    <div class="splash-content">
      <AllyWordmark class="splash-brand" />
      <div class="build-version">{{ buildVersion }}</div>
      <div class="splash-line">{{ $t('splash.loading') }}</div>
    </div>
  </div>
</template>

<script setup>
import { nextTick, onMounted, onUnmounted, ref } from 'vue';
import AllyWordmark from './AllyWordmark.vue';
import { buildVersion } from '../utils/buildVersion';

const emit = defineEmits(['done']);

const canvasRef = ref(null);
const leaving = ref(false);

let raf = 0;
let timer = 0;
let resizeHandler = null;
let pointerHandler = null;
let finished = false;

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

    const particles = [];
    const pointer = { x: 0, y: 0, active: false };
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

    const resetParticles = () => {
      particles.length = 0;
      const count = Math.max(90, Math.min(180, Math.floor((width * height) / 9000)));
      for (let i = 0; i < count; i++) {
        particles.push({
          x: width / 2 + (Math.random() - 0.5) * 80,
          y: height / 2 + (Math.random() - 0.5) * 80,
          tx: Math.random() * width,
          ty: Math.random() * height,
          vx: (Math.random() - 0.5) * 0.5,
          vy: (Math.random() - 0.5) * 0.5,
          size: 1 + Math.random() * 2.2,
          hue: 190 + Math.random() * 95,
          phase: Math.random() * Math.PI * 2,
        });
      }
    };

    resizeHandler = () => {
      resize();
      resetParticles();
    };
    pointerHandler = (event) => {
      pointer.x = event.clientX;
      pointer.y = event.clientY;
      pointer.active = true;
    };

    resize();
    resetParticles();
    window.addEventListener('resize', resizeHandler);
    window.addEventListener('pointermove', pointerHandler);

    const draw = (time) => {
      const elapsed = time - startedAt;
      const progress = Math.min(1, elapsed / 2200);
      ctx.clearRect(0, 0, width, height);

      const bg = ctx.createLinearGradient(0, 0, width, height);
      bg.addColorStop(0, '#080b10');
      bg.addColorStop(0.55, '#111827');
      bg.addColorStop(1, '#151515');
      ctx.fillStyle = bg;
      ctx.fillRect(0, 0, width, height);

      for (const p of particles) {
        const ease = 1 - Math.pow(1 - progress, 3);
        const driftX = Math.cos(time * 0.0012 + p.phase) * 18;
        const driftY = Math.sin(time * 0.001 + p.phase) * 14;
        p.x += (p.tx + driftX - p.x) * (0.018 + ease * 0.018) + p.vx;
        p.y += (p.ty + driftY - p.y) * (0.018 + ease * 0.018) + p.vy;

        if (pointer.active) {
          const dx = p.x - pointer.x;
          const dy = p.y - pointer.y;
          const distSq = dx * dx + dy * dy;
          if (distSq < 13000 && distSq > 1) {
            const force = (13000 - distSq) / 13000;
            p.x += dx * force * 0.035;
            p.y += dy * force * 0.035;
          }
        }
      }

      for (let i = 0; i < particles.length; i++) {
        const a = particles[i];
        for (let j = i + 1; j < particles.length; j++) {
          const b = particles[j];
          const dx = a.x - b.x;
          const dy = a.y - b.y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          if (dist < 115) {
            ctx.strokeStyle = `rgba(148, 197, 255, ${0.18 * (1 - dist / 115)})`;
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(a.x, a.y);
            ctx.lineTo(b.x, b.y);
            ctx.stroke();
          }
        }
      }

      for (const p of particles) {
        ctx.fillStyle = `hsla(${p.hue}, 92%, 72%, 0.86)`;
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
        ctx.fill();
      }

      if (!finished && !leaving.value) {
        raf = requestAnimationFrame(draw);
      }
    };

    raf = requestAnimationFrame(draw);
    timer = window.setTimeout(finish, 3600);
  });
}

function cleanup() {
  if (raf) cancelAnimationFrame(raf);
  if (timer) window.clearTimeout(timer);
  if (resizeHandler) window.removeEventListener('resize', resizeHandler);
  if (pointerHandler) window.removeEventListener('pointermove', pointerHandler);
  raf = 0;
  timer = 0;
  resizeHandler = null;
  pointerHandler = null;
}

function finish() {
  if (finished || leaving.value) return;
  leaving.value = true;
  cleanup();
  window.setTimeout(() => {
    finished = true;
    emit('done');
  }, 520);
}

onMounted(start);
onUnmounted(cleanup);
</script>
