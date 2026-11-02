//go:build darwin

package desktop

// A menu bar icon is 18 points tall. 36 pixels is its Retina representation, and
// MyGo hands the image to AppKit at 18 points, so the display picks that one.
const trayMenuBarPixels = 36

// systemTrayIcon returns the menu bar icon and whether macOS should tint it:
// a template image follows the menu bar's appearance instead of keeping a fixed
// colour, which is what macOS expects of a menu bar extra.
func systemTrayIcon() ([]byte, bool, error) {
	icon, err := trayTemplatePNG(trayMenuBarPixels)
	return icon, true, err
}
