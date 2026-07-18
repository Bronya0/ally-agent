<template>
  <div class="diff-view" :class="{ expanded: !collapsed }">
    <!-- Header: stats + ... more -->
    <div v-if="showHeader && diffHeader" class="diff-header" :class="{ clickable: !collapsed }" @click="onToggle">
      <span v-if="diffHeader.added > 0" class="diff-stat-added">+{{ diffHeader.added }}</span>
      <span v-if="diffHeader.removed > 0" class="diff-stat-removed">-{{ diffHeader.removed }}</span>
      <span class="diff-header-path">{{ diffHeader.filePath }}</span>
    </div>

    <!-- Body -->
    <div class="diff-body-scroll">
      <pre v-if="rawFallbackText" class="diff-raw-fallback">{{ rawFallbackText }}</pre>
      <template v-else>
        <div
          v-for="(line, li) in displayLines"
          :key="li"
          :class="['diff-row', ...lineClasses(line)]"
        >
          <!-- Separator line -->
          <template v-if="line.isSeparator">
            <span class="diff-separator">{{ line.separatorText }}</span>
          </template>

          <!-- Context / add / delete lines -->
          <template v-else>
            <span class="diff-gutter">{{ padGutter(line.lineNum) }}</span>
            <span class="diff-marker">{{ line.kind === 'add' ? '+' : line.kind === 'delete' ? '-' : ' ' }}</span>
            <span class="diff-code">{{ line.code }}</span>
          </template>
        </div>
      </template>
    </div>

    <!-- Expand/collapse toggle -->
    <div v-if="collapsed && hasMore" class="diff-expand-hint" @click="onToggle">
      {{ $t('tools.showFullDiff') }}
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  oldText: { type: String, default: '' },
  newText: { type: String, default: '' },
  filePath: { type: String, default: '' },
  diffLines: { type: Array, default: null },
  diffText: { type: String, default: '' },
  collapsed: { type: Boolean, default: true },
  addedCount: { type: Number, default: 0 },
  removedCount: { type: Number, default: 0 },
  showHeader: { type: Boolean, default: true },
  isIncomplete: { type: Boolean, default: false },
})

const emit = defineEmits(['toggle'])

const DEFAULT_CONTEXT_LINES = 3
const DEFAULT_MAX_LINES = 20

// If pre-computed diffLines are provided (from buildClusteredDisplay), use them.
// Otherwise parse diffText if provided (unified diff from backend).
// Otherwise compute from oldText/newText.
import { computeDiffLines, buildClusters, buildClusteredDisplay } from '../utils/diff.js'

function parseDiffText(text) {
  if (!text) return []
  const lines = text.split('\n')
  const result = []
  let oldLine = 1
  let newLine = 1
  for (const line of lines) {
    if (line.length === 0) continue
    if (line.startsWith('diff --git ') || line.startsWith('index ') || line.startsWith('--- ') || line.startsWith('+++ ')) {
      continue
    }
    if (line.startsWith('@@')) {
      const match = line.match(/^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/)
      if (match) {
        oldLine = Number(match[1]) || 1
        newLine = Number(match[2]) || 1
      }
      continue
    }
    const marker = line[0]
    const content = line.slice(1)
    if (marker === '+') {
      result.push({ kind: 'add', lineNum: newLine, code: content })
      newLine++
    } else if (marker === '-') {
      result.push({ kind: 'delete', lineNum: oldLine, code: content })
      oldLine++
    } else if (marker === ' ') {
      result.push({ kind: 'context', lineNum: newLine, code: content })
      oldLine++
      newLine++
    } else {
      result.push({ kind: 'context', lineNum: newLine, code: line })
      oldLine++
      newLine++
    }
  }
  return result
}

// Cache the raw diff lines separately from the display slice. The previous
// single computed re-ran the O(mn) LCS DP every time `collapsed` flipped,
// even though the diff itself only depends on (oldText, newText, diffText,
// isIncomplete). Splitting the two lets collapsed toggles skip the heavy
// pass and only re-slice the cached raw lines.
const rawDiffLines = computed(() => {
  if (props.diffLines) return props.diffLines
  if (props.diffText) return parseDiffText(props.diffText)
  if (!props.oldText && !props.newText) return []
  return computeDiffLines(props.oldText, props.newText, 1, 1, props.isIncomplete)
})

const localDiffLines = computed(() => {
  const raw = rawDiffLines.value
  if (!raw || raw.length === 0) return raw || []
  return buildClusteredDisplay(raw, DEFAULT_CONTEXT_LINES, props.collapsed ? DEFAULT_MAX_LINES : undefined)
})

const displayLines = computed(() => {
  return localDiffLines.value
})

const rawFallbackText = computed(() => {
  if (displayLines.value.length > 0) return ''
  const text = String(props.diffText || '').trim()
  if (!text) return ''
  return text
})

const hasMore = computed(() => {
  // If we have a truncated marker, there's more
  return localDiffLines.value.some(l => l.isSeparator && l.separatorText?.includes('more change'))
})

const diffHeader = computed(() => {
  // If caller provided explicit counts, use them
  if (props.addedCount > 0 || props.removedCount > 0) {
    return {
      added: props.addedCount || 0,
      removed: props.removedCount || 0,
      filePath: props.filePath || '',
    }
  }
  // Otherwise auto-count from the computed diff lines
  let added = 0
  let removed = 0
  for (const line of localDiffLines.value) {
    if (line.isSeparator) continue
    if (line.kind === 'add') added++
    else if (line.kind === 'delete') removed++
  }
  return { added, removed, filePath: props.filePath || '' }
})

function lineClasses(line) {
  if (line.isSeparator) return ['diff-sep-row']
  if (line.kind === 'add') return ['diff-add']
  if (line.kind === 'delete') return ['diff-del']
  return ['diff-ctx']
}

function padGutter(num) {
  return String(num).padStart(4)
}

function onToggle() {
  emit('toggle')
}
</script>
