# Third-Party Notices

Ally is licensed under GPL-3.0-only. Third-party components are not relicensed by Ally and remain governed by their own license terms.

## ripgrep

Release packages bundle the unmodified `rg` executable from [BurntSushi/ripgrep](https://github.com/BurntSushi/ripgrep), version 15.1.0.

Copyright (c) 2015 Andrew Gallant.

ripgrep is distributed under either the MIT License or the Unlicense. Copies of both texts are included in `third_party/ripgrep/` and in every Ally release package.

## Font assets

Font files under `frontend/src/assets/fonts/` are distributed under the SIL Open Font License 1.1. The full license text is stored at `frontend/src/assets/fonts/OFL.txt`.

## Go and frontend dependencies

Ally uses open-source Go modules and npm packages, including Wails, Vue, Naive UI, Markdown-It, highlight.js, and Mermaid. Their copyright and license notices remain in their source distributions and package metadata. The dependency versions used for a build are recorded in `go.mod`, `go.sum`, `frontend/package.json`, and `frontend/package-lock.json`.
