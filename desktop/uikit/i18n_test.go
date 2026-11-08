package uikit

import "github.com/egoist/mygo/ui"

// The established UI-kit fixtures use Chinese labels; specify their locale
// explicitly rather than relying on the tester's default language.
func chineseTester(view func(*ui.Context), width, height int) *ui.Tester {
	tester := ui.NewTester(view, width, height)
	tester.SetLocale("zh-Hans")
	return tester
}
