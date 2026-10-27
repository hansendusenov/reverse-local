package assets

import (
	"bytes"
	"image/png"
	"testing"
)

func TestGetAppIcons(t *testing.T) {
	pngBytes, icoBytes := GetAppIcons()
	if len(pngBytes) == 0 {
		t.Fatalf("expected non-empty png bytes")
	}
	if len(icoBytes) == 0 {
		t.Fatalf("expected non-empty ico bytes")
	}

	// Verify valid PNG
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("failed to decode png: %v", err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Errorf("expected 32x32 image, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}

	// Verify ICO header: 0, 1, 1 (first 4 bytes: 0x00, 0x00, 0x01, 0x00)
	if icoBytes[2] != 1 || icoBytes[3] != 0 {
		t.Errorf("invalid ICO header type: %v", icoBytes[:4])
	}
}
