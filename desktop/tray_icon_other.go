//go:build !windows && !darwin

package desktop

// Linux panels place the icon themselves and do not tint it, so the artwork
// keeps its plate: the letter alone would vanish on a dark panel. Panels draw
// these at 22 to 32 pixels.
const trayPanelPixels = 32

func systemTrayIcon() ([]byte, bool, error) {
	icon, err := trayMarkPNG(trayPanelPixels)
	return icon, false, err
}
