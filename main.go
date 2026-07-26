package main

// Test edit by agent

import (
	"embed"

	backend "ally-dev/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := backend.NewApp()

	err := wails.Run(&options.App{
		Title:     "Ally",
		Width:     backend.DefaultWindowWidth,
		Height:    backend.DefaultWindowHeight,
		MinWidth:  backend.MinWindowWidth,
		MinHeight: backend.MinWindowHeight,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 12, G: 12, B: 12, A: 255},
		Windows: &windows.Options{
			Theme:           windows.Dark,
			WindowClassName: backend.WindowsWindowClassName,
		},
		OnStartup: app.Startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
