<template>
  <div class="ally-avatar" role="img" :aria-label="$t('avatar.aria')">
    <svg class="ally-eye" viewBox="0 0 164 164" aria-hidden="true">
      <defs>
        <radialGradient id="ally-eye-iris" cx="50%" cy="50%" r="55%">
          <stop offset="0" stop-color="#ffd896"/>
          <stop offset="0.32" stop-color="#d49050"/>
          <stop offset="0.7" stop-color="#6b3a18"/>
          <stop offset="1" stop-color="#1a1208"/>
        </radialGradient>
        <radialGradient id="ally-eye-glow" cx="50%" cy="50%" r="54%">
          <stop offset="0" stop-color="#e0a458" stop-opacity="0.34"/>
          <stop offset="0.58" stop-color="#e0a458" stop-opacity="0.12"/>
          <stop offset="1" stop-color="#e0a458" stop-opacity="0"/>
        </radialGradient>
        <linearGradient id="ally-vortex-grad" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0" stop-color="#fff3d6"/>
          <stop offset="0.5" stop-color="#e0a458"/>
          <stop offset="1" stop-color="#fff3d6"/>
        </linearGradient>
        <!-- Lens-shaped vertical pupil (cat/snake eye slit): pointed at top
             and bottom, widest at the middle. -->
        <path id="ally-pupil-shape" d="M82 34 Q 71 82 82 130 Q 93 82 82 34 Z"/>
        <clipPath id="ally-eye-pupil-clip">
          <use href="#ally-pupil-shape"/>
        </clipPath>
      </defs>

      <ellipse cx="82" cy="88" rx="72" ry="62" fill="url(#ally-eye-glow)"/>
      <ellipse cx="82" cy="140" rx="42" ry="7" fill="#000" opacity="0.25"/>
      <path class="ally-eye-shell" d="M17 82c14-28 38-43 65-43s51 15 65 43c-14 28-38 43-65 43S31 110 17 82Z"/>
      <ellipse cx="82" cy="82" rx="47" ry="47" fill="url(#ally-eye-iris)" stroke="#f2c078" stroke-width="2.2"/>
      <path d="M40 69c22-21 62-22 84 0" fill="none" stroke="#fff3d6" stroke-width="2" opacity="0.34" stroke-linecap="round"/>

      <use href="#ally-pupil-shape" fill="#05070a"/>
      <g clip-path="url(#ally-eye-pupil-clip)">
        <!-- Vortex: a vertical swirl made of two opposing S-curves plus
             concentric lens-shaped rings, rotating to read as a whirlpool. -->
        <g class="ally-eye-vortex">
          <path d="M82 54 Q 88 66 82 78 Q 76 90 82 102 Q 88 114 82 126"
                fill="none" stroke="url(#ally-vortex-grad)" stroke-width="1.6" stroke-linecap="round" opacity="0.9"/>
          <path d="M82 54 Q 76 66 82 78 Q 88 90 82 102 Q 76 114 82 126"
                fill="none" stroke="url(#ally-vortex-grad)" stroke-width="1.2" stroke-linecap="round" opacity="0.65"/>
          <ellipse cx="82" cy="82" rx="3.5" ry="24" fill="none" stroke="#fff3d6" stroke-width="0.9" opacity="0.55"/>
        </g>
        <g class="ally-eye-vortex ally-eye-vortex-slow">
          <ellipse cx="82" cy="82" rx="6.5" ry="36" fill="none" stroke="#e0a458" stroke-width="1.2" opacity="0.42"/>
          <ellipse cx="82" cy="82" rx="2" ry="14" fill="none" stroke="#f8fafc" stroke-width="0.8" opacity="0.5"/>
        </g>
        <!-- Lightning bolt running down the slit. -->
        <path class="ally-eye-lightning" d="M85 50 L 78 76 L 83 76 L 75 114 L 91 70 L 84 70 Z" fill="#fff3d6"/>
      </g>

      <path d="M26 82c13 18 32 28 56 28s43-10 56-28" fill="none" stroke="#e0a458" stroke-width="2" opacity="0.32" stroke-linecap="round"/>
    </svg>
  </div>
</template>

<style scoped>
.ally-avatar {
  position: relative;
  width: 164px;
  height: 164px;
}

.ally-eye {
  display: block;
  width: 100%;
  height: 100%;
  user-select: none;
  pointer-events: none;
}

.ally-eye-shell {
  fill: #0b0e13;
  stroke: #343a45;
  stroke-width: 2;
}

.ally-eye-vortex {
  transform-origin: 82px 82px;
  animation: ally-eye-spin 18s linear infinite;
}

.ally-eye-vortex-slow {
  animation-duration: 27s;
  animation-direction: reverse;
}

.ally-eye-lightning {
  opacity: 0;
  filter: none;
  animation: ally-eye-flash 5.6s steps(1, end) infinite;
}

@keyframes ally-eye-spin {
  to { transform: rotate(360deg); }
}

@keyframes ally-eye-flash {
  0%, 76%, 100% { opacity: 0; }
  77% { opacity: 0.82; }
  78% { opacity: 0.18; }
  79% { opacity: 0.64; }
  80% { opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
  .ally-eye-vortex,
  .ally-eye-lightning {
    animation: none;
  }
}
</style>
