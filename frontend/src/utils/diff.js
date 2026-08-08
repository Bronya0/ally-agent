/**
 * Diff utilities — LCS-based diff algorithm.
 *
 * computeDiffLines: computes diff lines using LCS DP
 * buildClusters: groups changed lines into clusters with context
 */

/** @typedef {'context' | 'add' | 'delete'} DiffLineKind */

/**
 * @typedef {Object} DiffLine
 * @property {DiffLineKind} kind
 * @property {number} lineNum - line number in the new (or old) file
 * @property {string} code
 */

/**
 * Compute diff lines between oldText and newText using LCS DP.
 *
 * @param {string} oldText
 * @param {string} newText
 * @param {number} [oldStart=1]
 * @param {number} [newStart=1]
 * @param {boolean} [isIncomplete=false]
 * @returns {DiffLine[]}
 */
export function computeDiffLines(oldText, newText, oldStart = 1, newStart = 1, isIncomplete = false) {
  const oldLines = oldText ? oldText.split('\n') : [];
  const newLines = newText ? newText.split('\n') : [];
  const m = oldLines.length;
  const n = newLines.length;

  // 50K cell threshold (~200KB) — above this, LCS DP on the main thread causes
  // noticeable UI jank. The fallback is a prefix/suffix scan that is O(m+n).
  // Previous 1M threshold let a 1000x1000 line diff allocate 4MB and block for
  // hundreds of milliseconds.
  if ((m + 1) * (n + 1) > 50_000) {
    return computeLargeDiffFallback(oldLines, newLines, oldStart, newStart, isIncomplete);
  }

  // LCS DP table
  const dp = Array.from({ length: m + 1 }, () => new Uint32Array(n + 1));
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      if (oldLines[i - 1] === newLines[j - 1]) {
        dp[i][j] = dp[i - 1][j - 1] + 1;
      } else {
        dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
      }
    }
  }

  // Backtrack to reconstruct diff
  const reversed = [];
  let i = m;
  let j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      reversed.push({ kind: 'context', lineNum: newStart + j - 1, code: newLines[j - 1] });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      reversed.push({ kind: 'add', lineNum: newStart + j - 1, code: newLines[j - 1] });
      j--;
    } else {
      reversed.push({ kind: 'delete', lineNum: oldStart + i - 1, code: oldLines[i - 1] });
      i--;
    }
  }

  const result = [];
  for (let k = reversed.length - 1; k >= 0; k--) {
    result.push(reversed[k]);
  }

  // Suppress trailing deletes when streaming (incomplete)
  if (isIncomplete && result.length > 0) {
    let lastNonDelete = result.length - 1;
    while (lastNonDelete >= 0 && result[lastNonDelete].kind === 'delete') {
      lastNonDelete--;
    }
    if (lastNonDelete >= 0) {
      result.length = lastNonDelete + 1;
    } else {
      result.length = 0;
    }
  }

  return result;
}

function computeLargeDiffFallback(oldLines, newLines, oldStart, newStart, isIncomplete) {
  const oldLength = oldLines.length;
  const newLength = newLines.length;
  let prefix = 0;
  while (prefix < oldLength && prefix < newLength && oldLines[prefix] === newLines[prefix]) prefix++;

  let suffix = 0;
  while (
    suffix < oldLength - prefix
    && suffix < newLength - prefix
    && oldLines[oldLength - suffix - 1] === newLines[newLength - suffix - 1]
  ) {
    suffix++;
  }

  const result = [];
  for (let i = 0; i < prefix; i++) {
    result.push({ kind: 'context', lineNum: newStart + i, code: newLines[i] });
  }
  for (let i = prefix; i < oldLength - suffix; i++) {
    result.push({ kind: 'delete', lineNum: oldStart + i, code: oldLines[i] });
  }
  for (let i = prefix; i < newLength - suffix; i++) {
    result.push({ kind: 'add', lineNum: newStart + i, code: newLines[i] });
  }
  for (let i = suffix; i > 0; i--) {
    const newIndex = newLength - i;
    result.push({ kind: 'context', lineNum: newStart + newIndex, code: newLines[newIndex] });
  }

  if (isIncomplete && suffix === 0 && newLength === prefix) {
    return result.slice(0, prefix);
  }
  return result;
}

/**
 * @typedef {Object} Cluster
 * @property {number} start - start index in diffLines
 * @property {number} end - end index in diffLines
 */

/**
 * Group changed lines into clusters, each surrounded by contextLines of context.
 *
 * @param {DiffLine[]} diffLines
 * @param {number} contextLines
 * @returns {{ clusters: Cluster[], changedCount: number, addedCount: number, removedCount: number }}
 */
export function buildClusters(diffLines, contextLines) {
  const changeIndices = [];
  let added = 0;
  let removed = 0;
  for (const [i, line] of diffLines.entries()) {
    if (line.kind === 'add') { added++; changeIndices.push(i); }
    else if (line.kind === 'delete') { removed++; changeIndices.push(i); }
  }

  const clusters = [];
  if (changeIndices.length === 0) {
    return { clusters, changedCount: 0, addedCount: added, removedCount: removed };
  }

  const mergeGap = 2 * contextLines;
  let groupStart = changeIndices[0];
  let groupEnd = changeIndices[0];
  for (let i = 1; i < changeIndices.length; i++) {
    const idx = changeIndices[i];
    if (idx - groupEnd <= mergeGap) {
      groupEnd = idx;
    } else {
      clusters.push({
        start: Math.max(0, groupStart - contextLines),
        end: Math.min(diffLines.length - 1, groupEnd + contextLines),
      });
      groupStart = idx;
      groupEnd = idx;
    }
  }
  clusters.push({
    start: Math.max(0, groupStart - contextLines),
    end: Math.min(diffLines.length - 1, groupEnd + contextLines),
  });

  return { clusters, changedCount: changeIndices.length, addedCount: added, removedCount: removed };
}

/**
 * Compute edit stats from diff lines.
 *
 * @param {string} oldText
 * @param {string} newText
 * @returns {{ added: number, removed: number }}
 */
export function computeEditStats(oldText, newText, isIncomplete = false) {
  if (!oldText && !newText) return { added: 0, removed: 0 };
  const diff = computeDiffLines(oldText, newText, 1, 1, isIncomplete);
  let added = 0;
  let removed = 0;
  for (const line of diff) {
    if (line.kind === 'add') added++;
    else if (line.kind === 'delete') removed++;
  }
  return { added, removed };
}

/**
 * Format edit stats for display.
 *
 * @param {{ added: number, removed: number }} stats
 * @returns {string}
 */
export function formatEditStats(stats) {
  const parts = [];
  if (stats.added > 0) parts.push(`+${stats.added}`);
  if (stats.removed > 0) parts.push(`-${stats.removed}`);
  return parts.join(' ');
}

/**
 * Build a flat array of display lines for a clustered diff.
 * Each line is { kind, lineNum, code, isSeparator?, separatorText? }
 *
 * @param {DiffLine[]} diffLines
 * @param {number} contextLines
 * @param {number} [maxLines]
 * @returns {Array<{ kind: string, lineNum?: number, code?: string, isSeparator?: boolean, separatorText?: string }>}
 */
export function buildClusteredDisplay(diffLines, contextLines, maxLines) {
  const { clusters, changedCount, addedCount, removedCount } = buildClusters(diffLines, contextLines);
  /** @type {Array<{ kind: string, lineNum?: number, code?: string, isSeparator?: boolean, separatorText?: string }>} */
  const output = [];

  if (clusters.length === 0) return output;

  const cap = maxLines != null && maxLines >= 0 ? maxLines : Number.POSITIVE_INFINITY;
  let body = 0;
  let prevEnd = -1;
  let truncated = false;
  let shownChanges = 0;

  outer: for (const cluster of clusters) {
    if (body >= cap) { truncated = true; break; }
    if (prevEnd >= 0) {
      const gap = cluster.start - prevEnd - 1;
      if (gap > 0) {
        if (body + 1 > cap) { truncated = true; break; }
        output.push({ isSeparator: true, separatorText: `… ${gap} unchanged line${gap > 1 ? 's' : ''} …` });
        body++;
      }
    }
    for (let i = cluster.start; i <= cluster.end; i++) {
      if (body >= cap) { truncated = true; break outer; }
      const line = diffLines[i];
      output.push({ kind: line.kind, lineNum: line.lineNum, code: line.code });
      body++;
      if (line.kind !== 'context') shownChanges++;
      prevEnd = i;
    }
  }

  if (truncated) {
    const hidden = changedCount - shownChanges;
    if (hidden > 0) {
      output.push({
        isSeparator: true,
        separatorText: `… ${hidden} more change${hidden > 1 ? 's' : ''} hidden (click header to expand)`,
      });
    }
  }

  return output;
}
