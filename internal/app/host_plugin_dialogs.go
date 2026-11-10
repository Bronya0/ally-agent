// SPDX-License-Identifier: GPL-3.0-only
package app

import "ally-dev/locales"
import "github.com/egoist/mygo"

func (a *App) SelectPluginPackage() (string, error) {
	return a.selectFile(locales.Text("选择插件包 (.zip)"))
}
func (a *App) promptSavePluginPackage(name string) (string, error) {
	return mygo.Dialog.Save(mygo.SaveDialogOptions{Title: locales.Text("导出插件包"), DefaultPath: name})
}
