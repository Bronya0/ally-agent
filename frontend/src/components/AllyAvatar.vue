<template>
  <div class="ally-avatar" role="img" :aria-label="$t('avatar.aria')">
    <svg viewBox="0 0 160 116" aria-hidden="true">
      <ellipse class="avatar-glow" cx="80" cy="62" rx="54" ry="46" />
      <g class="avatar-wings">
        <path class="avatar-wing avatar-wing-left" d="M43 45c-10-11-22-10-27 1 8-2 13 1 16 7-8-1-13 3-13 11 9-5 17-3 24 5" />
        <path class="avatar-wing avatar-wing-right" d="M117 45c10-11 22-10 27 1-8-2-13 1-16 7 8-1 13 3 13 11-9-5-17-3-24 5" />
      </g>
      <path class="avatar-shoulders" d="M35 108c5-18 20-27 45-27s40 9 45 27" />
      <path class="avatar-neck" d="M68 80v13h24V80" />
      <g class="avatar-halo">
        <ellipse class="avatar-halo-glow" cx="80" cy="15" rx="24" ry="7" />
        <ellipse class="avatar-halo-ring" cx="80" cy="15" rx="20" ry="5" />
      </g>
      <g class="avatar-head">
        <path class="avatar-ear" d="M42 48h-7v23h7M118 48h7v23h-7" />
        <rect class="avatar-shell" x="42" y="27" width="76" height="62" rx="21" />
        <rect class="avatar-face" x="51" y="40" width="58" height="34" rx="13" />
        <g class="avatar-eyes">
          <rect class="avatar-eye" x="61" y="50" width="11" height="10" rx="5" />
          <rect class="avatar-eye" x="88" y="50" width="11" height="10" rx="5" />
        </g>
        <path class="avatar-mouth" d="M70 66c6 5 14 5 20 0" />
        <circle class="avatar-cheek" cx="58" cy="65" r="2.2" />
        <circle class="avatar-cheek" cx="102" cy="65" r="2.2" />
      </g>
    </svg>
  </div>
</template>

<style scoped>
.ally-avatar {
  width: 164px;
  height: 116px;
  color: #a9c9f5;
  filter: drop-shadow(0 12px 28px rgba(94, 143, 210, 0.16));
}

.ally-avatar svg {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}

.avatar-glow {
  fill: rgba(103, 155, 224, 0.07);
}

.avatar-shoulders {
  fill: rgba(143, 212, 180, 0.08);
  stroke: rgba(143, 212, 180, 0.52);
  stroke-width: 2;
  stroke-linecap: round;
}

.avatar-neck,
.avatar-shell,
.avatar-ear,
.avatar-mouth {
  fill: none;
  stroke: currentColor;
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.avatar-wing {
  fill: rgba(143, 212, 180, 0.035);
  stroke: rgba(143, 212, 180, 0.58);
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
  transform-box: fill-box;
}

.avatar-wing-left {
  transform-origin: right center;
  animation: ally-avatar-wing-left 4.8s ease-in-out infinite;
}

.avatar-wing-right {
  transform-origin: left center;
  animation: ally-avatar-wing-right 4.8s ease-in-out infinite;
}

.avatar-halo {
  transform-origin: 80px 15px;
  animation: ally-avatar-halo-float 3.8s ease-in-out infinite;
}

.avatar-halo-ring {
  fill: rgba(215, 191, 114, 0.05);
  stroke: #d7bf72;
  stroke-width: 2;
}

.avatar-halo-glow {
  fill: rgba(215, 191, 114, 0.09);
  filter: blur(2px);
  animation: ally-avatar-halo-glow 2.8s ease-in-out infinite;
}

.avatar-neck {
  fill: #20262a;
  stroke: rgba(169, 201, 245, 0.55);
}

.avatar-shell {
  fill: #20262a;
}

.avatar-face {
  fill: #151a1d;
  stroke: rgba(169, 201, 245, 0.38);
  stroke-width: 1.5;
}

.avatar-head {
  transform-origin: 80px 62px;
  animation: ally-avatar-turn var(--avatar-turn-duration, 9s) ease-in-out infinite;
}

.avatar-eyes {
  transform-origin: 80px 55px;
  animation: ally-avatar-gaze var(--avatar-gaze-duration, 9s) ease-in-out infinite;
}

.avatar-eye {
  fill: #8fd4b4;
  transform-box: fill-box;
  transform-origin: center;
  animation: ally-avatar-blink var(--avatar-blink-duration, 5.8s) ease-in-out infinite;
}

.avatar-mouth {
  stroke: #8fd4b4;
  stroke-width: 1.8;
}

.avatar-cheek {
  fill: rgba(232, 154, 170, 0.48);
}

@keyframes ally-avatar-blink {
  0%, 42%, 46%, 72%, 76%, 100% { transform: scaleY(1); }
  44%, 74% { transform: scaleY(0.08); }
}

@keyframes ally-avatar-turn {
  0%, 48%, 100% { transform: rotate(0deg) translateX(0); }
  58%, 66% { transform: rotate(-5deg) translateX(-2px); }
  76%, 84% { transform: rotate(5deg) translateX(2px); }
  92% { transform: rotate(0deg) translateX(0); }
}

@keyframes ally-avatar-gaze {
  0%, 48%, 100% { transform: translateX(0); }
  58%, 66% { transform: translateX(-2px); }
  76%, 84% { transform: translateX(2px); }
  92% { transform: translateX(0); }
}

@keyframes ally-avatar-halo-float {
  0%, 100% { transform: translateY(0) rotate(-1deg); }
  50% { transform: translateY(-2px) rotate(1deg); }
}

@keyframes ally-avatar-halo-glow {
  0%, 100% { opacity: 0.45; transform: scale(0.94); }
  50% { opacity: 1; transform: scale(1.08); }
}

@keyframes ally-avatar-wing-left {
  0%, 100% { transform: rotate(0deg) translateX(0); }
  50% { transform: rotate(-4deg) translateX(-1px); }
}

@keyframes ally-avatar-wing-right {
  0%, 100% { transform: rotate(0deg) translateX(0); }
  50% { transform: rotate(4deg) translateX(1px); }
}

@media (prefers-reduced-motion: reduce) {
  .avatar-head,
  .avatar-eyes,
  .avatar-eye,
  .avatar-halo,
  .avatar-halo-glow,
  .avatar-wing-left,
  .avatar-wing-right {
    animation: none;
  }
}
</style>
