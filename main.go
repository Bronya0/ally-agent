package main

import (
	"embed"
	"os"

	backend "ally-dev/internal/app"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIconPNG []byte

func main() {
	if handled, err := backend.RunUpdateRelaunchHelper(os.Args[1:]); handled {
		if err != nil {
			println("Update relaunch helper error:", err.Error())
		}
		return
	}
	app := backend.NewApp()

	wailsApp := application.New(application.Options{
		Name:        "Ally",
		Description: "Ally — AI coding agent desktop",
		Services:    []application.Service{application.NewService(app)},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			WndClass:        backend.WindowsWindowClassName,
			UseVisualHosting: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "io.github.bronya0.ally",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				// Second launch: focus the existing window instead of starting
				// a second process that would fight over config/MCP/schedules.
				app.ShowMainWindow()
			},
		},
	})

	mainWindow := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Ally",
		Width:            backend.DefaultWindowWidth,
		Height:           backend.DefaultWindowHeight,
		MinWidth:         backend.MinWindowWidth,
		MinHeight:        backend.MinWindowHeight,
		Frameless:        true,
		BackgroundColour: application.NewRGBA(12, 12, 12, 255),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
		URL: "/",
	})

	app.SetApp(wailsApp)
	app.SetWindow(mainWindow)
	app.SetTrayIcon(appIconPNG)

	if err := wailsApp.Run(); err != nil {
		println("Error:", err.Error())
	}
}
