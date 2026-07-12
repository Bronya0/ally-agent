const COMMAND_WORDS = new Set([
  'awk',
  'bash',
  'bun',
  'cargo',
  'cat',
  'cd',
  'chmod',
  'chown',
  'cp',
  'curl',
  'docker',
  'echo',
  'find',
  'git',
  'go',
  'grep',
  'ls',
  'make',
  'mkdir',
  'mv',
  'node',
  'npm',
  'npx',
  'pnpm',
  'rg',
  'rm',
  'sed',
  'sh',
  'ssh',
  'tar',
  'wails',
  'yarn',
  'zsh',
]);

const OPERATORS = ['<<<', '>>', '<<', '&&', '||', '|&', ';;', ';&', ';;&', '>|', '|', ';', '&', '(', ')', '<', '>'];

export function isShellLanguage(lang) {
  return ['bash', 'console', 'shell', 'sh', 'terminal', 'zsh'].includes(String(lang || '').toLowerCase());
}

export function looksLikeShellCommand(source) {
  const text = String(source || '').trim();
  if (!text || !/\s/.test(text)) return false;
  if (/(?:&&|\|\||[|;<>])/.test(text)) return true;
  if (/\s-{1,2}[A-Za-z0-9?]/.test(text)) return true;
  const firstWord = text.match(/^(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*(\S+)/)?.[1] || '';
  return COMMAND_WORDS.has(stripCommandPrefix(firstWord)) || /^\.\//.test(firstWord);
}

export function highlightShellCommand(source) {
  const text = String(source || '');
  let html = '';
  let i = 0;
  let expectingCommand = true;

  while (i < text.length) {
    const ch = text[i];

    if (ch === '\r' || ch === '\n') {
      html += escapeHtml(ch);
      i += 1;
      expectingCommand = true;
      continue;
    }

    if (/\s/.test(ch)) {
      html += escapeHtml(ch);
      i += 1;
      continue;
    }

    if (ch === '#') {
      const end = readUntilNewline(text, i);
      html += wrap('shell-comment', text.slice(i, end));
      i = end;
      continue;
    }

    const operator = readOperator(text, i);
    if (operator) {
      html += wrap('shell-operator', operator);
      i += operator.length;
      expectingCommand = isCommandSeparator(operator);
      continue;
    }

    if (ch === '\'' || ch === '"' || ch === '`') {
      const end = readQuoted(text, i, ch);
      html += wrap('shell-string', text.slice(i, end));
      i = end;
      expectingCommand = false;
      continue;
    }

    const end = readWordEnd(text, i);
    const word = text.slice(i, end);
    const className = shellWordClass(word, expectingCommand);
    html += className ? wrap(className, word) : escapeHtml(word);
    if (!isAssignment(word)) expectingCommand = false;
    i = end;
  }

  return html;
}

function shellWordClass(word, expectingCommand) {
  if (isFlag(word)) return 'shell-param';
  if (isAssignment(word) || word.startsWith('$')) return 'shell-param';
  if (expectingCommand || COMMAND_WORDS.has(stripCommandPrefix(word))) return 'shell-command-word';
  return '';
}

function stripCommandPrefix(word) {
  return String(word || '').replace(/^\.\//, '');
}

function isFlag(word) {
  return /^-{1,2}[A-Za-z0-9?][A-Za-z0-9:.,=?/+@%_-]*$/.test(word);
}

function isAssignment(word) {
  return /^[A-Za-z_][A-Za-z0-9_]*=/.test(word);
}

function readUntilNewline(text, start) {
  let i = start;
  while (i < text.length && text[i] !== '\n' && text[i] !== '\r') i += 1;
  return i;
}

function readQuoted(text, start, quote) {
  let i = start + 1;
  while (i < text.length) {
    if ((quote === '"' || quote === '`') && text[i] === '\\') {
      i += 2;
      continue;
    }
    if (text[i] === quote) return i + 1;
    i += 1;
  }
  return i;
}

function readOperator(text, start) {
  return OPERATORS.find((operator) => text.startsWith(operator, start)) || '';
}

function isCommandSeparator(operator) {
  return ['&&', '||', '|', '|&', ';', '&', ';;', ';&', ';;&', '('].includes(operator);
}

function readWordEnd(text, start) {
  let i = start;
  while (i < text.length) {
    if (/\s/.test(text[i]) || text[i] === '\'' || text[i] === '"' || text[i] === '`' || readOperator(text, i)) {
      break;
    }
    i += 1;
  }
  return i;
}

function wrap(className, text) {
  return `<span class="${className}">${escapeHtml(text)}</span>`;
}

function escapeHtml(text) {
  return String(text || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}
