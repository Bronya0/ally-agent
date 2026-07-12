package main

// Test edit by agent

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

const (
	defaultWindowWidth     = 1180
	defaultWindowHeight    = 760
	minWindowWidth         = 860
	minWindowHeight        = 600
	windowsWindowClassName = "AllyMainWindow"
)

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Ally",
		Width:     defaultWindowWidth,
		Height:    defaultWindowHeight,
		MinWidth:  minWindowWidth,
		MinHeight: minWindowHeight,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 12, G: 12, B: 12, A: 255},
		Windows: &windows.Options{
			Theme:           windows.Dark,
			WindowClassName: windowsWindowClassName,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
