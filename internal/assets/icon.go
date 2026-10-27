package assets

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
)

var (
	iconOnce sync.Once
	pngIcon  []byte
	icoIcon  []byte
)

// GetAppIcons generates and returns valid 32x32 PNG and ICO byte slices.
func GetAppIcons() (pngBytes []byte, icoBytes []byte) {
	iconOnce.Do(func() {
		const size = 32
		img := image.NewRGBA(image.Rect(0, 0, size, size))

		// Draw dark background with rounded corners / circle
		emerald := color.RGBA{R: 16, G: 185, B: 129, A: 255}   // #10b981
		darkBg := color.RGBA{R: 11, G: 15, B: 25, A: 255}       // #0b0f19
		cyan := color.RGBA{R: 6, G: 182, B: 212, A: 255}        // #06b6d4

		// Fill rounded dark square
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				dx := x - size/2
				dy := y - size/2
				distSq := dx*dx + dy*dy
				if distSq < (size/2)*(size/2)-2 {
					img.Set(x, y, darkBg)
				}
			}
		}

		// Draw inner emerald shield/circle ring
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				dx := x - size/2
				dy := y - size/2
				distSq := dx*dx + dy*dy
				if distSq >= (size/2-4)*(size/2-4) && distSq <= (size/2-2)*(size/2-2) {
					img.Set(x, y, emerald)
				}
			}
		}

		// Draw stylized "R" and "L" pixel pattern in the center
		drawPixelLetter(img, 8, 9, 'R', emerald)
		drawPixelLetter(img, 18, 9, 'L', cyan)

		// Encode PNG
		var pngBuf bytes.Buffer
		_ = png.Encode(&pngBuf, img)
		pngIcon = pngBuf.Bytes()

		// Build Windows ICO container wrapping the PNG data
		var icoBuf bytes.Buffer
		// 1. ICO Header (6 bytes)
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint16(0)) // Reserved
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint16(1)) // Type 1 = Icon
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint16(1)) // 1 Image count

		// 2. Directory Entry (16 bytes)
		icoBuf.WriteByte(byte(size))                                          // Width
		icoBuf.WriteByte(byte(size))                                          // Height
		icoBuf.WriteByte(0)                                                   // Color count
		icoBuf.WriteByte(0)                                                   // Reserved
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint16(1))             // Color planes
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint16(32))            // Bits per pixel
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint32(len(pngIcon)))  // PNG image size
		_ = binary.Write(&icoBuf, binary.LittleEndian, uint32(22))             // Image offset (6 + 16 = 22)

		// 3. PNG payload
		icoBuf.Write(pngIcon)
		icoIcon = icoBuf.Bytes()
	})

	return pngIcon, icoIcon
}

// drawPixelLetter draws a 5x9 pixel letter 'R' or 'L'
func drawPixelLetter(dst draw.Image, startX, startY int, char rune, c color.Color) {
	var pattern []string
	if char == 'R' {
		pattern = []string{
			"11110",
			"10001",
			"10001",
			"11110",
			"11000",
			"10100",
			"10010",
			"10001",
			"10001",
		}
	} else if char == 'L' {
		pattern = []string{
			"10000",
			"10000",
			"10000",
			"10000",
			"10000",
			"10000",
			"10000",
			"11111",
			"11111",
		}
	}

	for row, line := range pattern {
		for col, ch := range line {
			if ch == '1' {
				dst.Set(startX+col, startY+row, c)
			}
		}
	}
}
