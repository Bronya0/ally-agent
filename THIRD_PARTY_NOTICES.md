# Third-Party Notices

Ally is licensed under GPL-3.0-only. Third-party components and artwork retain their respective licenses.

## Native UI and Go dependencies

The native desktop uses `github.com/egoist/mygo v0.7.1`. Go module versions are recorded in `go.mod` and `go.sum`; dependency license texts and copyright notices remain in their module distributions. Agent dependencies continue to include `mvdan.cc/sh/v3` (BSD-3-Clause) and `github.com/go-git/go-git/v5` (Apache-2.0).

## Icons

The sidebar and GitHub SVG paths are extracted from the original frontend's `@vicons/antd 0.13.0`, which integrates Ant Design Icons under the MIT license. See `resources/icons/LICENSE`, `resources/icons/NOTICE.txt`, and `resources/icons/antd/NOTICE.txt`.

## Avatar artwork

Character images are preserved from the original Ally frontend. Each character's source, attribution, and license conditions remain in its directory under `resources/avatar/`. These include artwork with noncommercial and share-alike conditions. The per-character notices must accompany redistribution; the artwork is not relicensed by the application's GPL license. The `resources` package embeds the accompanying notices along with the images.

## ripgrep

Official ripgrep release archives are retained under `tools/ripgrep/`. Copyright (c) 2015 Andrew Gallant. Ripgrep is available under the MIT License or the Unlicense; both texts are retained in `third_party/ripgrep/`. See `tools/ripgrep/README.md` for archive provenance.

## Inter fonts and HTML preview charts

Inter weights 400/500/600/700 are preserved from the original frontend and converted from WOFF2 to TrueType for native font registration. Copyright 2016 The Inter Project Authors. Distributed under SIL Open Font License 1.1; see `resources/fonts/LICENSE.txt` and `resources/fonts/NOTICE.txt`.

The interactive HTML preview embeds Apache ECharts from the original frontend. Apache-2.0 license: `resources/preview/ECHARTS-LICENSE.txt`; the bundled JavaScript retains its own notices.

Mermaid diagram previews embed Mermaid 12.2.0 (MIT). License: `resources/preview/MERMAID-LICENSE.txt`; the bundled JavaScript retains dependency notices.
