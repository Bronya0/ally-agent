# Third-Party Notices

Ally is licensed under GPL-3.0-only. Third-party components are not relicensed by Ally and remain governed by their own license terms.

## ripgrep

Release packages bundle the unmodified `rg` executable from [BurntSushi/ripgrep](https://github.com/BurntSushi/ripgrep), version 15.2.0.

Copyright (c) 2015 Andrew Gallant.

ripgrep is distributed under either the MIT License or the Unlicense. Copies of both texts are included in `third_party/ripgrep/` and in every Ally release package.

## Direct Go parser dependencies

- `mvdan.cc/sh/v3 v3.13.1` — BSD-3-Clause; Bash shell AST parsing used by `internal/tools/command`.
- `github.com/go-git/go-git/v5 v5.19.2` — Apache-2.0; the `plumbing/format/gitignore` matcher is used for root `.gitignore` rules.
- `github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728` — BSD-3-Clause; PDF structure and text extraction used by `internal/tools/read`.

Their license texts and copyright notices are retained in the Go module distributions recorded by `go.mod` and `go.sum`.

## Font assets

Font files under `frontend/src/assets/fonts/` are distributed under the SIL Open Font License 1.1. The full license text is stored at `frontend/src/assets/fonts/OFL.txt`.

## Go and frontend dependencies

Ally uses open-source Go modules and npm packages, including Wails, Vue, Naive UI, Markdown-It, highlight.js, and Mermaid. Their copyright and license notices remain in their source distributions and package metadata. The dependency versions used for a build are recorded in `go.mod`, `go.sum`, `frontend/package.json`, and `frontend/package-lock.json`.
