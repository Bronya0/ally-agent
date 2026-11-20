# Model catalog source

`model_catalog.json` was generated from the latest `https://models.dev/api.json` snapshot downloaded on 2026-11-20. It follows the quick-configuration catalog filters used by Ally: supported OpenAI-compatible, OpenAI, and Anthropic SDK providers with a known endpoint; non-deprecated models with tool calling and text output; and API format values mapped to those accepted by Ally Native.

Regenerate it with `python scripts/gen_model_catalog.py` (add `--input snapshot.json` to reuse a downloaded snapshot).

The catalog is generated metadata embedded in the application. Provider and model names, endpoints, and model capabilities remain data values; the UI does not translate or rewrite them.
