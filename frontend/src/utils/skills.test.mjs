import test from 'node:test';
import assert from 'node:assert/strict';
import {isSkillActive, normalizeSkillName} from './skills.mjs';

test('skill identity preserves Unicode and punctuation', () => {
  for (const [name, other] of [['中文技能', '另一技能'], ['my_skill', 'myskill'], ['my.skill', 'myskill']]) {
    assert.notEqual(normalizeSkillName(name), normalizeSkillName(other));
    assert.equal(isSkillActive(name, [other]), false);
    assert.equal(isSkillActive(name, [name]), true);
  }
  assert.equal(isSkillActive(' My_Skill ', ['my_skill']), true);
  assert.equal(isSkillActive('', ['']), false);
});
