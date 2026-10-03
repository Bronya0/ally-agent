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
/** 长尾部（长代码块/长列表）的最短解析间隔帧数，≈20FPS。 */
export const TAIL_SLOW_FRAME_INTERVAL = 3;
/**
 * 长尾部每帧允许解析的字符预算：尾部越长、间隔按它放大，把“每秒解析的字符数”
 * 封成常数。固定间隔挡不住这个——一个没闭合的代码围栏可以让尾部涨到几 MB，
 * 面每个间隙都要把这几 MB 重新解析一遍并重建整棵 DOM。
 */
export const TAIL_FRAME_CHAR_BUDGET = 8192;
/** 解析间隔上限（≈2s @60fps）：再长就等于停止刷新了。 */
export const TAIL_MAX_FRAME_INTERVAL = 120;

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
 * 尾部这一帧要不要重解析：返回间隔帧数，1 表示每帧都可以。
 *
 * 频率不再是一个固定值：间隔随尾部长度增长，使“每秒解析的字符数”有界，
 * 而不是让固定间隔一直去解析一个几 MB 的未完成代码块。
 */
export function tailParseFrameInterval(tail) {
  if (shouldEagerParseTail(tail)) return 1;
  const text = String(tail ?? '');
  const steps = Math.ceil(text.length / TAIL_FRAME_CHAR_BUDGET);
  return Math.min(TAIL_MAX_FRAME_INTERVAL, Math.max(TAIL_SLOW_FRAME_INTERVAL, steps));
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
      } else if (newline !== -1 && line.trim() === '' && nextOffset <= upto) {
        // newline !== -1：没有换行符收尾的那一行不提交——它还会继续长（模型正在
        // 同一行上打字），提交出去后这条边界就落到行中间了，那个块单独渲染会丢掉
        // 缩进这类行首语义（4 空格代码块被切成两半）。增量版同样不提交，两边取舍
        // 一致。
        committedEnd = nextOffset;
      }
    }
    offset = nextOffset;
  }
  return { committedEnd, openFence: fence !== null };
}

/**
 * splitStreamContent 的增量版：一帧一帧地喂进更长的 upto，已扫过的**完整行**
 * 不再重扫。逐帧 O(全文) 的扫描在长回答下和 O(n²) 的前缀重解析一样贵：
 * 1MB 约 0.9ms/帧，还要为每一行分配一个字符串。
 *
 * 与全量版逐帧等价（护栏见 streamEase.test.mjs 的对拍用例，逐字符推进比对）：
 *   - 只有后面确实跟着 \n 的行才固化进状态；文本末行（还没有换行符）每帧现算，
 *     它的内容会随 upto 增长而变，固化了就会漏掉后来才成立的边界（全量版同样
 *     不提交这一行）；
 *   - 一行**必须被完整显示**（upto 越过它的换行符）才会被越过：显示位置落在行
 *     中间时本轮停在它前面。越过就再也回不来——它若是空白行，那个可切分点当场
 *     永久丢掉，之后 upto 涨上来也没人回头补；
 *   - upto 回退（重试丢弃）时从头重扫；
 *   - reset() 由调用方在“内容可能整个换过”的强制对齐（挂载/收尾/截断）时调用。
 */
export function createStreamScanner() {
  let offset = 0;
  let committedEnd = 0;
  let fence = null;
  let lastLimit = 0;

  function reset() {
    offset = 0;
    committedEnd = 0;
    fence = null;
    lastLimit = 0;
  }

  function scan(text, limit) {
    const source = String(text ?? '');
    const bounded = Number.isFinite(limit) ? Math.floor(limit) : source.length;
    const upto = Math.max(0, Math.min(bounded, source.length));
    if (upto < lastLimit) reset();
    lastLimit = upto;
    // 尾部那一行的换行符位置：复用循环里最后一次扫描的结果，别为同一行再扫一遍
    // 全文（单行大 body 时那是每帧几 MB 的白扫）。
    let tailNewline = -1;
    while (offset < upto) {
      const newline = source.indexOf('\n', offset);
      tailNewline = newline;
      // 末行还没结束：它的内容还会变，留给下面那段每帧现算。
      if (newline === -1) break;
      const nextOffset = newline + 1;
      // 这一行还没被完整显示（显示位置落在它中间）：本轮停在它前面。越过它就再
      // 也回不来——它若是空白行，那个可切分点当场永久丢掉；全量版每帧从头重扫，
      // 所以它不会丢，只在这一步省略就会分叉。
      if (nextOffset > upto) break;
      const line = source.slice(offset, newline);
      if (fence) {
        if (isClosingFence(line, fence)) fence = null;
      } else {
        const opened = matchOpeningFence(line);
        if (opened) fence = opened;
        // 上面那道 break 已保证 nextOffset <= upto 成立。
        else if (line.trim() === '') committedEnd = nextOffset;
      }
      offset = nextOffset;
    }
    // 显示位置所在的那一行只用于判断“当前在不在围栏里”，结果不写回固化状态。
    // 条件必须是 offset < upto（显示位置确实落在这行上）：写成 < source.length 时
    // upto=0 也会去看下一行，凭空报出“在围栏里”，而全量版那时什么都还没看。
    let tailFence = fence;
    if (offset < upto) {
      // 只取这一行：围栏判据是单行正则，喂进多行会静默判成“不是围栏”。这里用的
      // 是循环里那次扫描的结果（tailNewline === -1 就是走到了没有换行的末行）。
      const line = tailNewline === -1 ? source.slice(offset) : source.slice(offset, tailNewline);
      if (tailFence) {
        if (isClosingFence(line, tailFence)) tailFence = null;
      } else {
        const opened = matchOpeningFence(line);
        if (opened) tailFence = opened;
      }
    }
    return { committedEnd, openFence: tailFence !== null };
  }

  return { scan, reset };
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
 * 流式渲染状态机：把“已接收内容 + 已显示长度 → (committedBlocks, tailHtml)”
 * 的推进逻辑从 Vue 组件里拿出来。不含任何 Vue/DOM 依赖——组件只负责 rAF
 * 驱动与把结果写进模板，状态机本身可以在 node 里用假节拍跑完整流式序列。
 *
 * render 就是 markdown 渲染函数 `(text, streaming) => html`。
 *
 * 调用契约（回归护栏见 streamEase.test.mjs）：
 *   - committedBlocks.join('') + tailHtml 覆盖的内容永远是已接收内容的一个前缀，
 *     长度单调不回退；
 *   - 流式期间不整篇重解析：前缀按**块**增量追加（只渲染新提交的那一段，并作为
 *     一个新元素挂到已提交列表末尾），其余解析都落在尾部；
 *   - 首次、收尾、流式状态翻转、内容被截断导致边界回退这四种情况整篇重渲染一次，
 *     保证最终结果与“整篇解析”一致（分块渲染在链接引用定义这类跨块语法上会不同，
 *     收尾那一次会把它纠正回来）。
 *
 * 前缀返回的是**块数组**而不是一整段 HTML：拼成单串再交给 v-html，每次追加都会让
 * 浏览器把整棵已提交子树（长回答可达几千个节点、里面还有渲染好的 mermaid 图）
 * 重建一遍；一块一个节点时，新增块只插一个新节点，已有的块原地不动。
 */
export function createStreamRenderState(render) {
  const renderFn = typeof render === 'function' ? render : () => '';
  const scanner = createStreamScanner();
  let committedEnd = -1;
  let committedBlocks = [];
  let tailHtml = '';
  let renderedStreaming = null;
  let renderedTail = '';
  let frameTick = 0;

  function renderAt(content, shown, streaming, force) {
    const full = String(content ?? '');
    const upto = Math.max(0, Math.min(clampCount(shown), full.length));
    if (force) scanner.reset();
    const { committedEnd: boundary } = scanner.scan(full, upto);
    const streamingChanged = streaming !== renderedStreaming;
    // 前缀只在“边界向前推进、且流式状态没变”时增量追加：每个提交段都是一段独立
    // 完成的块，前面已经算好的 HTML 再算一遍就是长回答的 O(n²) 来源。
    const needsFullRender = force || streamingChanged || committedEnd < 0 || boundary < committedEnd;
    let prefixChanged = false;
    if (needsFullRender) {
      const html = boundary > 0 ? renderFn(full.slice(0, boundary), streaming) || '' : '';
      committedBlocks = html ? [html] : [];
      committedEnd = boundary;
      renderedStreaming = streaming;
      // 前缀变了：尾部必须同帧重算，否则旧尾部会和新前缀重叠、文字重复。
      renderedTail = '';
      prefixChanged = true;
    } else if (boundary > committedEnd) {
      const html = renderFn(full.slice(committedEnd, boundary), streaming) || '';
      // 建新数组而不是就地 push：组件用 shallowRef 接这个值，靠引用变化触发重渲，
      // 就地 push 不会惊动它。块数很小（一次流式几十到几百块），复制引用可忽略。
      if (html) committedBlocks = [...committedBlocks, html];
      committedEnd = boundary;
      renderedTail = '';
      prefixChanged = true;
    }
    const tail = full.slice(committedEnd, upto);
    if (!prefixChanged && tail === renderedTail) return;
    if (!prefixChanged) {
      frameTick += 1;
      const interval = tailParseFrameInterval(tail);
      if (interval > 1 && frameTick % interval !== 0) return;
    }
    tailHtml = tail ? renderFn(tail, streaming) || '' : '';
    renderedTail = tail;
  }

  return {
    /**
     * 推进到指定的已显示长度并渲染两段。无变化时返回的两段与上次同一个引用
     * （数组与字符串都不重新分配），组件写回 ref 不会触发多余重渲染。
     */
    render(content, shown, streaming, force = false) {
      renderAt(content, shown, streaming, force === true);
      return { committedBlocks, tailHtml };
    },
  };
}
