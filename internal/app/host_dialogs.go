// SPDX-License-Identifier: GPL-3.0-only
package app

import "github.com/wailsapp/wails/v3/pkg/application"

// Callers validate the desktop host before creating a dialog. Keep window
// ownership here so every file picker and export has the same native parent.
func (a *App) openFileDialog() *application.OpenFileDialogStruct {
	dialog := a.wails.app.Dialog.OpenFile()
	if a.wails.window != nil {
		dialog.AttachToWindow(a.wails.window)
	}
	return dialog
}

func (a *App) saveFileDialog() *application.SaveFileDialogStruct {
	dialog := a.wails.app.Dialog.SaveFile()
	if a.wails.window != nil {
		dialog.AttachToWindow(a.wails.window)
	}
	return dialog
}
