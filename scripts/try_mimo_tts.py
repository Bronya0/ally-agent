"""Generate a short Xiaomi MiMo TTS sample for local listening.

Set MIMO_API_KEY in the environment before running this script. The key is
never written to disk.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import sys
import urllib.error
import urllib.request
import wave
from pathlib import Path


ENDPOINT = "https://api.xiaomimimo.com/v1/chat/completions"
MODEL = "mimo-v2.5-tts"
VOICES = ["mimo_default", "冰糖", "茉莉", "苏打", "白桦", "Mia", "Chloe", "Milo", "Dean"]
DEFAULT_OUT_DIR = Path(__file__).resolve().parents[1] / ".tmp" / "mimo_tts_voices"
STYLE = (
    "请用中文普通话，以二次元动漫剧情解说的感觉朗读。声音清亮、年轻、有活力，"
    "语速稍稍快一些，节奏紧凑，前半段带悬念，结尾有戏剧张力。不要唱歌，"
    "不要添加原文以外的内容。"
)
NARRATION = (
    "晚霞刚刚落下，沉睡百年的星之塔突然亮起。少女凛在废弃车站捡到一枚会说话的怀表，"
    "表盘显示：月亮将在十分钟后坠落。下一刻，天空裂开银色缝隙，黑羽如暴雨落下。"
    "青梅竹马悠人拔剑赶来，却发现自己的影子也握着一把剑。原来入侵者早已藏在他们的记忆深处。"
    "钟声响起前，凛必须选择：救回失去的过去，还是守住眼前的伙伴？怀表停止跳动，"
    "月亮开始坠落——而凛的答案，究竟是什么？"
)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--out",
        type=Path,
        default=Path(__file__).resolve().parents[1] / ".tmp" / "mimo_anime_narration.wav",
        help="WAV output path (default: .tmp/mimo_anime_narration.wav)",
    )
    parser.add_argument("--voice", default="茉莉", help="MiMo preset voice (default: 茉莉)")
    parser.add_argument(
        "--all-voices",
        action="store_true",
        help="Generate one WAV for every documented preset voice",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=DEFAULT_OUT_DIR,
        help="Output directory used with --all-voices",
    )
    args = parser.parse_args()

    api_key = os.environ.get("MIMO_API_KEY", "").strip()
    if not api_key:
        print("Set MIMO_API_KEY in the environment first.", file=sys.stderr)
        return 2

    voices = VOICES if args.all_voices else [args.voice]
    failed = 0
    for index, voice in enumerate(voices, start=1):
        out_path = (
            args.out_dir / f"{index:02d}_{voice}.wav"
            if args.all_voices
            else args.out
        )
        payload = {
            "model": MODEL,
            "messages": [
                {"role": "user", "content": STYLE},
                {"role": "assistant", "content": NARRATION},
            ],
            "audio": {"format": "wav", "voice": voice},
            "stream": False,
        }
        request = urllib.request.Request(
            ENDPOINT,
            data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
            headers={"api-key": api_key, "Content-Type": "application/json"},
            method="POST",
        )

        try:
            with urllib.request.urlopen(request, timeout=180) as response:
                result = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            print(f"[{voice}] MiMo API returned HTTP {exc.code}: {detail}", file=sys.stderr)
            failed += 1
            continue
        except (urllib.error.URLError, TimeoutError) as exc:
            print(f"[{voice}] Could not reach MiMo API: {exc}", file=sys.stderr)
            failed += 1
            continue
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            print(f"[{voice}] MiMo API returned invalid JSON: {exc}", file=sys.stderr)
            failed += 1
            continue

        try:
            encoded_audio = result["choices"][0]["message"]["audio"]["data"]
            audio_bytes = base64.b64decode(encoded_audio, validate=True)
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            print(f"[{voice}] MiMo response did not contain audio data: {exc}", file=sys.stderr)
            failed += 1
            continue

        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_bytes(audio_bytes)
        try:
            with wave.open(str(out_path), "rb") as audio:
                duration = audio.getnframes() / audio.getframerate()
                print(f"Saved {out_path} ({duration:.1f}s, {len(audio_bytes):,} bytes)")
        except (wave.Error, EOFError):
            print(f"Saved {out_path} ({len(audio_bytes):,} bytes; WAV duration unavailable)")

    print(f"Finished: {len(voices) - failed}/{len(voices)} voices generated.")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
