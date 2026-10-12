// markdown-it 插件：让紧贴中文的 `*` / `**` 能正确开闭加粗、斜体。
//
// 问题出在 CommonMark 的开闭判定：它只看标记两侧的字符是不是空白或标点。
// 「今年的主线是**「部侧生成」**」里，开标记 `**` 前是汉字（既非空白也非标点），
// 后面紧跟全角「，于是它被判成"不能开"，整段加粗失效，星号原样露出。
//
// 修法：只对 `*` 放宽，把汉字当成与空白同等的"可断开边界"。
// `_` 保持 CommonMark 原规则（它另有词内限制，不在这次范围内）。
// 字符分类全部复用 markdown-it 自带的 utils，不另起一套标点表。

import emphasisRule from 'markdown-it/lib/rules_inline/emphasis.mjs';

const ASTERISK = 0x2a;
const CJK_RE = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u;

// 标记前一个码点（处理代理对）；行首视为空。
function charBefore(src, index) {
  if (index <= 0) return '';
  const low = src.charCodeAt(index - 1);
  return index >= 2 && low >= 0xdc00 && low <= 0xdfff ? src.slice(index - 2, index) : src[index - 1];
}

// 标记后一个码点；超出可解析范围（posMax）视为行尾，与库的口径一致。
function charAfter(state, index) {
  if (index >= state.posMax) return '';
  return String.fromCodePoint(state.src.codePointAt(index));
}

export function cjkEmphasisPlugin(md) {
  const { isWhiteSpace, isPunctChar, isMdAsciiPunct } = md.utils;
  const isSpace = (ch) => ch === '' || isWhiteSpace(ch.codePointAt(0));
  const isPunct = (ch) => ch !== '' && (isMdAsciiPunct(ch.codePointAt(0)) || isPunctChar(ch));
  const isCjk = (ch) => CJK_RE.test(ch);

  const tokenizeOriginal = emphasisRule.tokenize;

  md.inline.ruler.at('emphasis', (state, silent) => {
    const start = state.pos;
    const delimitersBefore = state.delimiters.length;
    const matched = tokenizeOriginal(state, silent);
    if (!matched || state.src.charCodeAt(start) !== ASTERISK) return matched;

    const prev = charBefore(state.src, start);
    const next = charAfter(state, state.pos);
    const prevSpace = isSpace(prev);
    const nextSpace = isSpace(next);
    const prevPunct = isPunct(prev);
    const nextPunct = isPunct(next);

    // 与库的 scanDelims 同构（`*` 允许词内，故 can_open = left、can_close = right），
    // 唯一差别：汉字在被标点卡住的一侧也算边界。
    const canOpen = !nextSpace && (!nextPunct || prevSpace || prevPunct || isCjk(prev));
    const canClose = !prevSpace && (!prevPunct || nextSpace || nextPunct || isCjk(next));

    for (let i = delimitersBefore; i < state.delimiters.length; i++) {
      state.delimiters[i].open = canOpen;
      state.delimiters[i].close = canClose;
    }
    return matched;
  });
  return md;
}
