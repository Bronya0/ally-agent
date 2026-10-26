// SPDX-License-Identifier: GPL-3.0-only
//
// 位图形象的唯一映射表：欢迎页底图、动作姿势、设置页缩略图都从这里取，
// 新增形象只改这一处（eye 是内联 SVG、custom 是用户上传，都不走这里）。
//
// poses 里是同一角色、同一画布高度（320）的静态立绘，用于「换姿势」：
// 角色每隔几秒做一个动作再回到 idle。它不是连续帧动画，别当成逐帧播放。
//
// 注意：本文件 import 了图片资源（PNG / WebP），只能被 Vite 构建的前端代码引入，
// 不要在 node 单测里 import（`avatarAppearance.mjs` / `avatarPose.mjs` 才是纯逻辑入口）。
import whale from './whale/delighted-320.webp';
// 整段动画的单一 WebP（无姿势集），靠自身帧循环播放。
import botMotion from './bot/motion.webp';

import gptIdle from './gpt/idle.webp';
import gptProud from './gpt/proud.webp';
import gptStretch from './gpt/stretch.webp';
import gptHappy from './gpt/happy.webp';

import claudeIdle from './claude/idle.webp';
import claudeProud from './claude/proud.webp';
import claudeStretch from './claude/stretch.webp';
import claudeHappy from './claude/happy.webp';

import deepseekIdle from './deepseek/idle.webp';
import deepseekProud from './deepseek/proud.webp';
import deepseekStretch from './deepseek/stretch.webp';
import deepseekHappy from './deepseek/happy.webp';

import geminiIdle from './gemini/idle.webp';
import geminiProud from './gemini/proud.webp';
import geminiStretch from './gemini/stretch.webp';
import geminiHappy from './gemini/happy.webp';

import glmIdle from './glm/idle.webp';
import glmProud from './glm/proud.webp';
import glmStretch from './glm/stretch.webp';
import glmHappy from './glm/happy.webp';

import qwenIdle from './qwen/idle.webp';
import qwenProud from './qwen/proud.webp';
import qwenStretch from './qwen/stretch.webp';
import qwenHappy from './qwen/happy.webp';

// 待机时轮播哪些姿势（名字要同时存在于下面每个角色的 poses 里）。
export const AVATAR_IDLE_ACTIONS = ['proud', 'stretch'];

// 说话时用哪张：闭眼笑的那张，和台词气泡是同一个反应。
export const AVATAR_SPEAK_POSE = 'happy';

export const AVATAR_ARTWORK = {
  whale: {
    image: whale,
    aria: 'avatar.whaleAria',
    title: 'avatar.whaleSakuraToggle',
  },
  bot: {
    image: botMotion,
    aria: 'avatar.botAria',
    title: 'avatar.botSakuraToggle',
  },
  gpt: {
    image: gptIdle,
    poses: { proud: gptProud, stretch: gptStretch, happy: gptHappy },
    aria: 'avatar.gptAria',
    title: 'avatar.gptSakuraToggle',
  },
  claude: {
    image: claudeIdle,
    poses: { proud: claudeProud, stretch: claudeStretch, happy: claudeHappy },
    aria: 'avatar.claudeAria',
    title: 'avatar.claudeSakuraToggle',
  },
  deepseek: {
    image: deepseekIdle,
    poses: { proud: deepseekProud, stretch: deepseekStretch, happy: deepseekHappy },
    aria: 'avatar.deepseekAria',
    title: 'avatar.deepseekSakuraToggle',
  },
  gemini: {
    image: geminiIdle,
    poses: { proud: geminiProud, stretch: geminiStretch, happy: geminiHappy },
    aria: 'avatar.geminiAria',
    title: 'avatar.geminiSakuraToggle',
  },
  glm: {
    image: glmIdle,
    poses: { proud: glmProud, stretch: glmStretch, happy: glmHappy },
    aria: 'avatar.glmAria',
    title: 'avatar.glmSakuraToggle',
  },
  qwen: {
    image: qwenIdle,
    poses: { proud: qwenProud, stretch: qwenStretch, happy: qwenHappy },
    aria: 'avatar.qwenAria',
    title: 'avatar.qwenSakuraToggle',
  },
};

export function avatarArtwork(style) {
  return AVATAR_ARTWORK[style] || null;
}
