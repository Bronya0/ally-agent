// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"path/filepath"
	"strings"

	backend "ally-dev/internal/app"
	"ally-dev/internal/documentpreview"
)

type workspaceFileKind uint8

const (
	workspaceFileText workspaceFileKind = iota
	workspaceFileMarkdown
	workspaceFileHTML
	workspaceFileImage
	workspaceFileSVG
	workspaceFileAudio
	workspaceFileVideo
	workspaceFilePDF
	workspaceFileDocument
	workspaceFileProperties
)

// Keep extension routing in one place. The document preview package owns its
// supported Office formats; all other UI paths use this table.
var workspaceFileKindsByExtension = map[string]workspaceFileKind{
	".md": workspaceFileMarkdown, ".markdown": workspaceFileMarkdown,
	".html": workspaceFileHTML, ".htm": workspaceFileHTML,
	".png": workspaceFileImage, ".jpg": workspaceFileImage, ".jpeg": workspaceFileImage,
	".gif": workspaceFileImage, ".webp": workspaceFileImage, ".bmp": workspaceFileImage,
	".ico": workspaceFileImage, ".svg": workspaceFileSVG,
	".mp3": workspaceFileAudio, ".wav": workspaceFileAudio, ".m4a": workspaceFileAudio,
	".aac": workspaceFileAudio, ".flac": workspaceFileAudio, ".ogg": workspaceFileAudio,
	".oga": workspaceFileAudio, ".opus": workspaceFileAudio,
	".mp4": workspaceFileVideo, ".m4v": workspaceFileVideo, ".mov": workspaceFileVideo,
	".webm": workspaceFileVideo, ".ogv": workspaceFileVideo,
	".pdf": workspaceFilePDF,
	".doc": workspaceFileProperties, ".xls": workspaceFileProperties,
	".ppt": workspaceFileProperties, ".pptx": workspaceFileProperties,
	".zip": workspaceFileProperties, ".7z": workspaceFileProperties,
	".rar": workspaceFileProperties, ".gz": workspaceFileProperties,
	".tar": workspaceFileProperties, ".exe": workspaceFileProperties,
	".dll": workspaceFileProperties, ".bin": workspaceFileProperties,
	".avi": workspaceFileProperties, ".mkv": workspaceFileProperties,
	".wmv": workspaceFileProperties, ".flv": workspaceFileProperties,
}

func workspaceFileKindForPath(name string) workspaceFileKind {
	if kind, ok := workspaceFileKindsByExtension[strings.ToLower(filepath.Ext(name))]; ok {
		return kind
	}
	if documentpreview.Supported(name) {
		return workspaceFileDocument
	}
	return workspaceFileText
}

func (kind workspaceFileKind) isMedia() bool {
	return kind == workspaceFileImage || kind == workspaceFileSVG || kind == workspaceFileAudio || kind == workspaceFileVideo
}

func (kind workspaceFileKind) isDirectWebViewFile() bool {
	return kind == workspaceFileImage || kind == workspaceFileAudio || kind == workspaceFileVideo || kind == workspaceFilePDF
}

func (kind workspaceFileKind) isPreviewOnly() bool {
	return kind.isDirectWebViewFile() || kind == workspaceFileSVG || kind == workspaceFileDocument
}

func (kind workspaceFileKind) hasWebViewPreview() bool {
	return kind.isPreviewOnly() || kind == workspaceFileMarkdown || kind == workspaceFileHTML
}

func (kind workspaceFileKind) hasMarkupToggle() bool {
	return kind == workspaceFileMarkdown || kind == workspaceFileHTML
}

func (a *desktop) setExplorerOpen(open bool) {
	if a.explorer == open {
		return
	}
	a.explorer = open
	if !open {
		a.releaseFilePreview()
	}
}

func (a *desktop) openSystemFile(path string) {
	if t := a.current(); t != nil {
		req := backend.WorkspacePathRequest{Workspace: t.Workspace, Path: path}
		a.job(func() error { return a.core.OpenWorkspaceFile(req) })
	}
}
