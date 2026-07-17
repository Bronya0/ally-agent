import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const sourcePath = path.join(root, 'docs', 'model_api.json');
const outputPath = path.join(root, 'frontend', 'src', 'data', 'modelCatalog.json');
const source = JSON.parse(fs.readFileSync(sourcePath, 'utf8'));
const supportedPackages = new Set([
  '@ai-sdk/openai-compatible',
  '@ai-sdk/openai',
  '@ai-sdk/anthropic',
]);
const officialBaseUrls = {
  anthropic: 'https://api.anthropic.com',
  openai: 'https://api.openai.com/v1',
};

function apiFormat(provider) {
  if (provider.npm === '@ai-sdk/anthropic') return 'anthropic_messages';
  if (provider.id === 'openai') return 'openai_responses';
  return 'openai_chat';
}

function naturalCompare(left, right) {
  return String(left || '').localeCompare(String(right || ''), 'en', {
    numeric: true,
    sensitivity: 'base',
  });
}

const providers = Object.values(source)
  .filter((provider) => provider && supportedPackages.has(provider.npm))
  .map((provider) => {
    const models = Object.values(provider.models || {})
      .filter((model) => model
        && model.status !== 'deprecated'
        && model.tool_call === true
        && Array.isArray(model.modalities?.output)
        && model.modalities.output.includes('text'))
      .map((model) => {
        const interleaved = model.interleaved && typeof model.interleaved === 'object'
          ? model.interleaved
          : null;
        return {
          id: String(model.id || '').trim(),
          name: String(model.name || model.id || '').trim(),
          contextWindow: Number(model.limit?.context) || 0,
          maxTokens: Number(model.limit?.output) || 0,
          reasoningTag: String(interleaved?.field || '').trim(),
        };
      })
      .filter((model) => model.id)
      .sort((left, right) => naturalCompare(left.name, right.name) || naturalCompare(left.id, right.id));
    return {
      id: String(provider.id || '').trim(),
      name: String(provider.name || provider.id || '').trim(),
      apiFormat: apiFormat(provider),
      baseUrl: String(provider.api || officialBaseUrls[provider.id] || '').trim(),
      doc: String(provider.doc || '').trim(),
      models,
    };
  })
  .filter((provider) => provider.id && provider.baseUrl && provider.models.length)
  .sort((left, right) => naturalCompare(left.name, right.name));

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, `${JSON.stringify({ formatVersion: 1, providers })}\n`, 'utf8');
console.log(`Generated ${providers.length} providers and ${providers.reduce((sum, provider) => sum + provider.models.length, 0)} models at ${path.relative(root, outputPath)}`);
