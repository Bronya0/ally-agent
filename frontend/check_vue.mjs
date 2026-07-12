const fs = require('fs');
const sfc = require('vue/compiler-sfc');
const src = fs.readFileSync('src/App.vue', 'utf8');
try {
  const parsed = sfc.parse(src);
  console.log('Parse OK');
} catch(e) {
  console.error('Error:', e.message);
  if (e.loc) {
    console.error('Location:', JSON.stringify(e.loc));
    const lines = src.split('\n');
    const start = Math.max(0, e.loc.start.line - 8);
    const end = Math.min(lines.length, e.loc.start.line + 3);
    for(let i = start; i < end; i++) {
      console.error((i+1) + ': ' + lines[i]);
    }
  }
}
