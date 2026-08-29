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
	// app menu, so "Margo" leads and carries Settings… (Cmd+,).
	//
	// Supplying any custom menu replaces the whole menu bar, including
	// the standard Edit menu. On macOS that menu is not decoration: it
	// is where Cut/Copy/Paste/Select All get their key equivalents, so
	// without it Cmd+C, Cmd+V and Cmd+X are dead everywhere in the
	// webview, including the message composer. menu.EditMenu() restores
	// the whole set (Undo, Redo, Cut, Copy, Paste, Paste and Match
	// Style, Delete, Select All) bound to the standard responder
	// selectors. WindowMenu() likewise restores Minimize and Zoom.
	//
	// The app menu is hand-built rather than menu.AppMenu(). That role
	// is all-or-nothing and has no Settings slot, so using it would
	// push Settings… out of the app menu, where macOS users expect it.
	// The cost is small: Wails' own "About" is an informational NSAlert
	// (see WailsContext.m), not the rich About panel, so an equivalent
	// dialog costs nothing in fidelity, and Hide is a runtime call.
	// Hide Others and Show All are the only losses — Wails exposes no
	// Go binding for either, and both are rarely used.
	appMenu := menu.NewMenu()
	margoMenu := appMenu.AddSubmenu("Margo")
	margoMenu.AddText("About Margo", nil, func(_ *menu.CallbackData) {
		app.showAbout()
	})
	margoMenu.AddSeparator()
	margoMenu.AddText("Settings…", keys.CmdOrCtrl(","), func(_ *menu.CallbackData) {
		app.openSettings()
	})
	margoMenu.AddSeparator()
	margoMenu.AddText("Hide Margo", keys.CmdOrCtrl("h"), func(_ *menu.CallbackData) {
		app.hide()
	})
	margoMenu.AddText("Quit Margo", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		app.quit()
	})
	appMenu.Append(menu.EditMenu())
	appMenu.Append(menu.WindowMenu())

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
