/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

/**
 * 流式输出的"显示层缓动"。
 *
 * 上游（模型/网关 + 后端 64ms 节流）送来的是一批一批的字符：把"已接收长度"
 * 直接当"已显示长度"用，肉眼就是十几帧一次的整块跳字。这里把两者拆开——组件
 * 每帧调用 advanceShown 让显示长度朝已接收长度逼近，把一批字符摊到若干帧里
 * 连续输出，与上游的批次大小无关。
 *
 * 逐帧重解析必须成本有界，所以内容还要按"块边界"切成两段：
 *   committed —— 已完成的块，只在边界推进时解析一次；
 *   tail      —— 当前未完成的那一块，逐帧（长块降频）解析。
 * splitStreamContent 给出这个安全切点，shouldEagerParseTail 决定尾部节奏。
 *
 * 纯函数、无 DOM 依赖，便于单测。
 */

/** 每帧至少推进的字符数：差值收敛到 1 时不会无限趋近而卡住。 */
export const STREAM_MIN_STEP = 1;
/** 每帧追平剩余差值的比例（0.3 ≈ 4 帧消化一批）。 */
export const STREAM_EASE_RATIO = 0.3;
/** 显示落后超过该时长就直接跳到已接收长度（网络卡顿后不做长尾追赶）。 */
export const STREAM_MAX_LAG_MS = 1200;
/** 尾部不超过该长度时每帧重解析：短尾解析成本远低于一帧预算。 */
export const TAIL_EAGER_MAX_CHARS = 800;
/** 长尾部（长代码块/长列表）的解析间隔帧数，≈20FPS，保证单帧成本有界。 */
export const TAIL_SLOW_FRAME_INTERVAL = 3;

/**
 * 单帧推进后的显示长度。目标回退（重试丢弃、内容被截断）时直接对齐，不做
 * 回退动画。
 */
export function advanceShown(shown, target) {
  const current = clampCount(shown);
  const goal = clampCount(target);
  if (goal <= current) return goal;
  const gap = goal - current;
  return Math.min(goal, current + Math.max(STREAM_MIN_STEP, Math.ceil(gap * STREAM_EASE_RATIO)));
}

/**
 * 尾部是否可以逐帧解析。长尾部、以及含公式/图片语法的尾部返回 false，交给
 * 降频节奏：公式（katex）单次渲染可达毫秒级，图片节点每次重建都会重新解码。
 */
export function shouldEagerParseTail(tail) {
  const text = String(tail ?? '');
  if (text.length > TAIL_EAGER_MAX_CHARS) return false;
  return !text.includes('$') && !text.includes('![');
}

/**
 * 在 limit（已显示长度）之前找到最后一个"安全的块边界"。
 *
 * 边界只有一个来源：围栏代码块**之外**的空白行之后。原因是渲染被切成两个
 * 容器，块一旦被切开就会各自成块——段落中途切开会凭空多出一个段间距，围栏
 * 中途切开会把代码块截断，所以都排除。
 *
 * 返回 { committedEnd, openFence }：committedEnd 是提交给前一个容器的字符
 * 数，openFence 表示 limit 处是否仍在未闭合的围栏里（调用方据此判断要不要
 * 让尾部保持等宽）。
 *
 * 已知取舍：缩进 4 空格的代码块不参与围栏跟踪，松散列表（列表项之间有空行）
 * 可能在块完成前短暂渲染成两个列表。两者都只在流式过程中可见，块完成即恢复。
 */
export function splitStreamContent(content, limit) {
  const text = String(content ?? '');
  const bounded = Number.isFinite(limit) ? Math.floor(limit) : text.length;
  const upto = Math.max(0, Math.min(bounded, text.length));
  let committedEnd = 0;
  let fence = null;
  let offset = 0;
  while (offset < upto) {
    const newline = text.indexOf('\n', offset);
    const lineEnd = newline === -1 ? text.length : newline;
    const line = text.slice(offset, lineEnd);
    const nextOffset = newline === -1 ? text.length : newline + 1;
    if (fence) {
      if (isClosingFence(line, fence)) fence = null;
    } else {
      const opened = matchOpeningFence(line);
      if (opened) {
        fence = opened;
      } else if (line.trim() === '' && nextOffset <= upto) {
        committedEnd = nextOffset;
      }
    }
    offset = nextOffset;
  }
  return { committedEnd, openFence: fence !== null };
}

const FENCE_PATTERN = /^ {0,3}(`{3,}|~{3,})(.*)$/;

function matchOpeningFence(line) {
  const match = FENCE_PATTERN.exec(line);
  if (!match) return null;
  const marker = match[1];
  // 反引号围栏的信息串里不能再出现反引号（CommonMark）。
  if (marker[0] === '`' && match[2].includes('`')) return null;
  return { char: marker[0], length: marker.length };
}

function isClosingFence(line, fence) {
  const match = FENCE_PATTERN.exec(line);
  if (!match) return false;
  const marker = match[1];
  if (marker[0] !== fence.char || marker.length < fence.length) return false;
  return match[2].trim() === '';
}

function clampCount(value) {
  const count = Math.floor(Number(value));
  return Number.isFinite(count) && count > 0 ? count : 0;
}

/**
 * 流式渲染状态机：把“已接收内容 + 已显示长度 → (committedHtml, tailHtml)”
 * 的推进逻辑从 Vue 组件里拿出来。不含任何 Vue/DOM 依赖——组件只负责 rAF
 * 驱动与把结果写进模板，状态机本身可以在 node 里用假节拍跑完整流式序列。
 *
 * render 就是 markdown 渲染函数 `(text, streaming) => html`。
 *
 * 调用契约（回归护栏见 streamEase.test.mjs）：
 *   - committedHtml + tailHtml 永远是已接收内容的一个前缀，长度单调不回退；
 *   - 流式期间不整篇重解析：前缀只在块边界推进时解析，其余解析都落在尾部；
 *   - 收尾（streaming=false）整篇只解析一次。
 */
export function createStreamRenderState(render) {
  const renderFn = typeof render === 'function' ? render : () => '';
  let committedEnd = -1;
  let committedHtml = '';
  let tailHtml = '';
  let renderedStreaming = null;
  let renderedTail = '';
  let frameTick = 0;

  function renderAt(content, shown, streaming, force) {
    const full = String(content ?? '');
    const upto = Math.max(0, Math.min(clampCount(shown), full.length));
    const { committedEnd: boundary } = splitStreamContent(full, upto);
    const boundaryMoved = force || boundary !== committedEnd || streaming !== renderedStreaming;
    if (boundaryMoved) {
      committedHtml = boundary > 0 ? renderFn(full.slice(0, boundary), streaming) || '' : '';
      committedEnd = boundary;
      renderedStreaming = streaming;
      // 前缀变了：尾部必须同帧重算，否则旧尾部会和新前缀重叠、文字重复。
      renderedTail = '';
    }
    const tail = full.slice(committedEnd, upto);
    if (!boundaryMoved && tail === renderedTail) return;
    if (!boundaryMoved) {
      frameTick += 1;
      if (!shouldEagerParseTail(tail) && frameTick % TAIL_SLOW_FRAME_INTERVAL !== 0) return;
    }
    tailHtml = tail ? renderFn(tail, streaming) || '' : '';
    renderedTail = tail;
  }

  return {
    /**
     * 推进到指定的已显示长度并渲染两段。返回的字符串在无变化时与上次同一个
     * 引用/值，组件写回 ref 不会触发多余重渲染。
     */
    render(content, shown, streaming, force = false) {
      renderAt(content, shown, streaming, force === true);
      return { committedHtml, tailHtml };
    },
  };
}
