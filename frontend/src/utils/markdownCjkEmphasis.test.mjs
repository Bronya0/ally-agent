import { test } from 'node:test';
import assert from 'node:assert/strict';
import MarkdownIt from 'markdown-it';
import { cjkEmphasisPlugin } from './markdownCjkEmphasis.mjs';

const md = new MarkdownIt({ html: false, linkify: true, breaks: true }).use(cjkEmphasisPlugin);
const render = (src) => md.render(src).trim();

test('中文句中紧贴全角引号的 ** 能开加粗（用户反馈的原句）', () => {
  const out = render('今年的主线是**「部侧生成 K1 → 集团离线送回」**。任务和包是两件事。');
  assert.equal(out, '<p>今年的主线是<strong>「部侧生成 K1 → 集团离线送回」</strong>。任务和包是两件事。</p>');
});

test('闭标记后紧跟汉字也能收尾', () => {
  assert.equal(render('**「包」**是关键'), '<p><strong>「包」</strong>是关键</p>');
});

test('紧贴括号、双引号的开标记同样生效', () => {
  assert.equal(render('是**(部侧)**。'), '<p>是<strong>(部侧)</strong>。</p>');
  assert.equal(render('是**“部侧”**。'), '<p>是<strong>“部侧”</strong>。</p>');
});

test('单个 * 的斜体同样生效', () => {
  assert.equal(render('是*「包」*。'), '<p>是<em>「包」</em>。</p>');
});

test('原本就能工作的写法不回退', () => {
  assert.equal(render('**任务**和**包**挂钩'), '<p><strong>任务</strong>和<strong>包</strong>挂钩</p>');
  assert.equal(render('关键是 **「包」** 。'), '<p>关键是 <strong>「包」</strong> 。</p>');
});

test('拉丁字母紧贴标点时仍遵守 CommonMark，不误加粗', () => {
  assert.equal(render('a**(b)**c'), '<p>a**(b)**c</p>');
});

test('下划线 __ 保持 CommonMark 原规则，不随中文放宽', () => {
  assert.equal(render('是__「包」__。'), '<p>是__「包」__。</p>');
});

test('乘号与孤立的 * 不被误判为强调', () => {
  assert.equal(render('2 * 3 * 4'), '<p>2 * 3 * 4</p>');
  assert.equal(render('价格是*（注）'), '<p>价格是*（注）</p>');
});
