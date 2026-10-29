// Prepare the vendored rg for local builds and release packages. No network access.
import { chmodSync, copyFileSync, mkdtempSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { parseArgs } from 'node:util';

const root = fileURLToPath(new URL('../', import.meta.url));
const { values } = parseArgs({ options: {
  os: { type: 'string', default: process.platform === 'win32' ? 'windows' : process.platform },
  arch: { type: 'string', default: process.arch },
  'output-dir': { type: 'string', default: join(root, 'bin', 'tools') },
} });
const os = values.os;
const arch = values.arch === 'amd64' ? 'x64' : values.arch;
const archiveDir = join(root, 'tools', 'ripgrep');
const targets = {
  'windows/x64': 'x86_64-pc-windows-msvc.zip',
  'windows/arm64': 'aarch64-pc-windows-msvc.zip',
  'linux/x64': 'x86_64-unknown-linux-musl.tar.gz',
  'linux/arm64': 'aarch64-unknown-linux-musl.tar.gz',
  'darwin/x64': 'x86_64-apple-darwin.tar.gz',
  'darwin/arm64': 'aarch64-apple-darwin.tar.gz',
};

function select(cpu) {
  const suffix = targets[`${os}/${cpu}`];
  if (!suffix) throw new Error(`Unsupported ripgrep target: ${os}/${cpu}`);
  const matches = readdirSync(archiveDir).filter(name => name.startsWith('ripgrep-') && name.endsWith(`-${suffix}`));
  if (matches.length !== 1) throw new Error(`Expected exactly one ripgrep archive for ${os}/${cpu}, found ${matches.length}`);
  return matches[0];
}

function extract(name, dest) {
  const binary = os === 'windows' ? 'rg.exe' : 'rg';
  const member = `${name.replace(/\.(tar\.gz|zip)$/, '')}/${binary}`;
  const options = { cwd: archiveDir, maxBuffer: 32 * 1024 * 1024 };
  let contents;
  if (name.endsWith('.zip') && process.platform !== 'win32') {
    // GNU tar does not read ZIP. unzip exits with an error if the member is missing.
    contents = execFileSync('unzip', ['-p', name, member], options);
  } else {
    // Choose the Windows system bsdtar for ZIP even when Git/MSYS GNU tar is on PATH.
    const tar = name.endsWith('.zip')
      ? join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'tar.exe')
      : 'tar';
    // Relative archive names avoid GNU tar interpreting a Windows drive as a remote host.
    contents = execFileSync(tar, ['-xOf', name, member], options);
  }
  writeFileSync(dest, contents, { mode: 0o755 });
  console.log(`Extracted ${name}`);
}

const output = resolve(values['output-dir']);
mkdirSync(output, { recursive: true });
// Interrupted work must never leave temporary files inside a release or signed bundle.
const work = mkdtempSync(join(tmpdir(), 'ally-rg-'));
try {
  const binary = os === 'windows' ? 'rg.exe' : 'rg';
  const staged = join(work, binary);
  if (arch === 'universal' && os === 'darwin') {
    const arm = select('arm64');
    const x64 = select('x64');
    if (arm.split('-aarch64-')[0] !== x64.split('-x86_64-')[0]) throw new Error('Universal ripgrep archives must have the same version');
    extract(arm, join(work, 'arm64'));
    extract(x64, join(work, 'x64'));
    if (process.platform === 'darwin') {
      execFileSync('lipo', ['-create', join(work, 'arm64'), join(work, 'x64'), '-output', staged]);
    } else {
      execFileSync('wails3', ['tool', 'lipo', '-input', join(work, 'arm64'), '-input', join(work, 'x64'), '-output', staged]);
    }
  } else {
    extract(select(arch), staged);
  }
  const destination = join(output, binary);
  copyFileSync(staged, destination);
  chmodSync(destination, 0o755);
} finally {
  rmSync(work, { recursive: true, force: true });
}
