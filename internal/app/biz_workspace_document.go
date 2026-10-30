package app

import (
	"ally-dev/internal/documentpreview"
	toolerrors "ally-dev/internal/tools/shared"
	"context"
)

func (a *App) ReadWorkspaceDocument(ctx context.Context, req WorkspacePathRequest) (*documentpreview.Document, error) {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return nil, err
	}
	return documentpreview.Load(ctx, resolved)
}

// These are expected editor limitations, not failed user operations. Keep the
// model/tool error envelope intact and classify only at the desktop boundary.
func WorkspaceFilePreviewNote(err error) string {
	switch toolerrors.Code(err) {
	case "E_BINARY_FILE":
		return "此文件以属性方式查看，可使用系统应用打开。"
	case "E_FILE_TOO_LARGE":
		return "文件较大，已显示属性，可使用系统应用打开。"
	case "E_NOT_REGULAR_FILE", "E_IS_DIRECTORY":
		return "此路径以属性方式查看。"
	}
	return ""
}
