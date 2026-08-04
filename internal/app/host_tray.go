package app

import (
	"bytes"
	"image"
	"image/png"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// System tray support (Wails v3). The tray keeps Ally running when the main
// window is hidden and offers Show / Hide / Quit actions. Tray menu labels
// are plain English because the tray is an OS-level surface outside the
// frontend i18n system.

const trayIconSize = 32

// SetTrayIcon stores the app icon bytes injected by main.go for the system
// tray. The icon must be a PNG; it is downscaled to a tray-friendly size.
// No-op when empty or undecodable (the tray then falls back to the platform
// default icon).
func (a *App) SetTrayIcon(pngBytes []byte) {
	a.trayIconMu.Lock()
	defer a.trayIconMu.Unlock()
	a.trayIcon = pngBytes
}

// trayIconPNG32 returns the stored icon downscaled to trayIconSize pixels,
// or nil when no usable icon is available.
func (a *App) trayIconPNG32() []byte {
	a.trayIconMu.Lock()
	data := a.trayIcon
	a.trayIconMu.Unlock()
	if len(data) == 0 {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	scaled := scaleImageNearest(img, trayIconSize, trayIconSize)
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return nil
	}
	return buf.Bytes()
}

// scaleImageNearest downscales src to width x height with nearest-neighbour
// sampling. Good enough for tray icons and keeps the stdlib dependency only.
func scaleImageNearest(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW <= 0 || srcH <= 0 {
		return dst
	}
	for y := 0; y < height; y++ {
		srcY := sb.Min.Y + (y*srcH)/height
		for x := 0; x < width; x++ {
			srcX := sb.Min.X + (x*srcW)/width
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

// setupSystemTray creates the tray icon, click-to-toggle behavior, and the
// Show / Hide / Quit menu. Called from ServiceStartup. A failed tray setup
// (nil tray) leaves the old close-quits behavior intact.
func (a *App) setupSystemTray() {
	if a.wails == nil || a.wails.app == nil {
		return
	}
	tray := a.wails.app.SystemTray.New()
	if tray == nil {
		return
	}
	a.wails.tray = tray
	if icon := a.trayIconPNG32(); len(icon) > 0 {
		tray.SetIcon(icon)
	}
	tray.SetTooltip("Ally")
	tray.OnClick(func() { a.toggleMainWindow() })

	menu := a.wails.app.NewMenu()
	menu.Add("Show Ally").OnClick(func(ctx *application.Context) { a.showMainWindow() })
	menu.Add("Hide").OnClick(func(ctx *application.Context) { a.hideMainWindow() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(ctx *application.Context) { a.quitApp() })
	tray.SetMenu(menu)
	tray.Show()
}

// setupCloseToTrayHook intercepts the window close request. When the user has
// not explicitly disabled close-to-tray, closing hides the window instead of
// quitting the app, so Agent work (MCP, scheduled tasks, services) keeps
// running. An explicit quit (tray Quit, self-update) bypasses the hook.
func (a *App) setupCloseToTrayHook() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	window := a.wails.window
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if a.wails.quitForced.Load() {
			return // explicit quit requested via quitApp()
		}
		cfg, err := a.getConfig()
		if err != nil {
			return
		}
		if !cfg.closeToTrayEnabled() {
			return
		}
		e.Cancel()
		window.Hide()
	})
}

// toggleMainWindow shows the window when hidden and hides it when visible.
// Used by tray click.
func (a *App) toggleMainWindow() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	if a.wails.window.IsVisible() {
		a.wails.window.Hide()
	} else {
		a.wails.window.Show()
		a.wails.window.Focus()
	}
}

// showMainWindow displays and focuses the main window. Used by the tray menu
// and the second-instance activation callback.
func (a *App) showMainWindow() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	a.wails.window.Show()
	a.wails.window.Focus()
}

// hideMainWindow hides the main window without quitting. Used by the tray
// menu.
func (a *App) hideMainWindow() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	a.wails.window.Hide()
}
