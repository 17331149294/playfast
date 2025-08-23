package path

import (
	"os"
	"path/filepath"
	"runtime"
)

func Path() string {
	path := ""
	switch runtime.GOOS {
	case "windows":
		path = filepath.Join(os.Getenv("APPDATA"), "PlayFast")
	case "darwin":
		path = "/Library/Application Support/PlayFast"
	default:
		path = filepath.Join(os.Getenv("HOME"), ".PlayFast")
	}
	_ = os.MkdirAll(path, 0644)
	return path
}
