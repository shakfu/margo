package main

import (
	"embed"
	"flag"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Parse CLI flags before Wails grabs argv. Only one user-facing flag
	// today: -workspace <dir> opens (or creates) a workspace bound to
	// that directory. Resolved to an absolute path so the frontend's
	// "find workspace by dir" lookup is unambiguous. (7.1.e)
	workspaceDir := flag.String("workspace", "", "Open margo with a workspace bound to this directory")
	flag.Parse()
	startupDir := ""
	if *workspaceDir != "" {
		if abs, err := filepath.Abs(*workspaceDir); err == nil {
			startupDir = abs
		} else {
			startupDir = *workspaceDir
		}
	}

	// Create an instance of the app structure
	app := NewApp()
	app.startupWorkspaceDir = startupDir

	// Application menu. On macOS the first submenu becomes the named
	// app menu (Margo). Today the only useful entry is Settings…
	// (Cmd+,) which fires a frontend event; the OS-standard items
	// (About, Hide, Quit, Edit menu, Window menu) are handled by Wails
	// defaults when no custom menu is provided. Adding a custom menu
	// suppresses some defaults — only Quit (Cmd+Q) is preserved
	// implicitly. Acceptable trade-off: users get Cmd+Q for free, and
	// Cmd+, opens settings; richer menu work is its own slice.
	appMenu := menu.NewMenu()
	margoMenu := appMenu.AddSubmenu("Margo")
	margoMenu.AddText("Settings…", keys.CmdOrCtrl(","), func(_ *menu.CallbackData) {
		app.openSettings()
	})

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "Margo",
		Width:  1024,
		Height: 768,
		Menu:   appMenu,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
