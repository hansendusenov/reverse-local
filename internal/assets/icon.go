package assets

import (
	_ "embed"
)

//go:embed app.png
var embeddedPNG []byte

//go:embed app.ico
var embeddedICO []byte

// GetAppIcons returns the Merah Putih application PNG and multi-resolution ICO byte slices.
func GetAppIcons() (pngBytes []byte, icoBytes []byte) {
	return embeddedPNG, embeddedICO
}
