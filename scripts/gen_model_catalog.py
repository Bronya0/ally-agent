#!/usr/bin/env python3
"""Regenerate desktop/model_catalog.json from the models.dev provider snapshot.

The catalog feeds the "供应商快速配置" list in the model editor. Only providers that
models.dev reaches over one of the three SDKs Ally implements are kept, and only
models that can hold a tool-calling text conversation.

    python scripts/gen_model_catalog.py                    # download + rewrite
    python scripts/gen_model_catalog.py --input snap.json   # reuse a snapshot
"""
import argparse
from datetime import date
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
CATALOG = ROOT / 'desktop' / 'model_catalog.json'
SOURCE = 'https://models.dev/api.json'

# models.dev names the SDK each provider is reached with; Ally ships these three
# wire formats only, so anything else has no quick-configuration target.
API_FORMATS = {
    '@ai-sdk/openai-compatible': 'openai_chat',
    '@ai-sdk/openai': 'openai_responses',
    '@ai-sdk/anthropic': 'anthropic_messages',
}

# models.dev leaves `api` empty for the first-party providers; these are the SDK
# defaults, matching internal/app/prov_model.go.
DEFAULT_BASE_URLS = {
    'openai': 'https://api.openai.com/v1',
    'anthropic': 'https://api.anthropic.com',
}


def model_entries(provider):
    models = provider.get('models') or {}
    if isinstance(models, dict):
        return sorted((key, value) for key, value in models.items())
    return sorted((model.get('id', ''), model) for model in models)


def reasoning_tag(model):
    interleaved = model.get('interleaved')
    if isinstance(interleaved, dict) and interleaved.get('field'):
        return interleaved['field']
    return 'reasoning_content' if model.get('reasoning') else ''


def vision_capable(model):
    inputs = (model.get('modalities') or {}).get('input') or []
    return bool(model.get('attachment')) and 'image' in inputs


def usable(model):
    outputs = (model.get('modalities') or {}).get('output') or []
    return bool(model.get('tool_call')) and 'text' in outputs


def build_catalog(snapshot, retrieved):
    providers = []
    for provider_id, provider in sorted(snapshot.items()):
        api_format = API_FORMATS.get(provider.get('npm'))
        base_url = (provider.get('api') or DEFAULT_BASE_URLS.get(provider_id) or '').strip()
        if not api_format or not base_url:
            continue
        models = []
        for key, model in model_entries(provider):
            if not usable(model):
                continue
            limit = model.get('limit') or {}
            model_id = model.get('id') or key
            models.append({
                'id': model_id,
                'name': model.get('name') or model_id,
                'contextWindow': int(limit.get('context') or 0),
                'maxTokens': int(limit.get('output') or 0),
                'reasoningTag': reasoning_tag(model),
                'visionCapable': vision_capable(model),
            })
        if not models:
            continue
        providers.append({
            'id': provider_id,
            'name': provider.get('name') or provider_id,
            'apiFormat': api_format,
            'baseUrl': base_url,
            'doc': provider.get('doc') or '',
            'models': models,
        })
    return {
        'formatVersion': 1,
        'source': SOURCE,
        'retrievedAt': retrieved,
        'providers': providers,
    }


def load_snapshot(args):
    if args.input:
        return json.loads(Path(args.input).read_text(encoding='utf-8'))
    with urllib.request.urlopen(SOURCE, timeout=120) as response:
        return json.loads(response.read().decode('utf-8'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', help='local models.dev snapshot instead of downloading')
    parser.add_argument('--retrieved', default=date.today().isoformat(),
                        help='retrievedAt value, defaults to today')
    args = parser.parse_args()

    catalog = build_catalog(load_snapshot(args), args.retrieved)
    CATALOG.write_text(
        json.dumps(catalog, ensure_ascii=False, separators=(',', ':')) + '\n',
        encoding='utf-8',
        newline='\n',
    )
    formats = {}
    for provider in catalog['providers']:
        formats[provider['apiFormat']] = formats.get(provider['apiFormat'], 0) + 1
    total = sum(len(provider['models']) for provider in catalog['providers'])
    print(f"wrote {CATALOG.relative_to(ROOT)}: {len(catalog['providers'])} providers, "
          f"{total} models, {', '.join(f'{k}={v}' for k, v in sorted(formats.items()))}")


if __name__ == '__main__':
    main()
