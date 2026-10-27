//go:build ignore

// cmd/genassets/main.go generates:
// 1. internal/assets/app.ico (Multi-resolution 256, 64, 48, 32, 16 Merah Putih ICO)
// 2. internal/assets/AppIcon.icns (macOS ICNS container for Reverse.Local.app & .dmg)
// 3. rsrc_windows_amd64.syso (Windows PE COFF resource object embedding RT_ICON, RT_GROUP_ICON, RT_MANIFEST with requireAdministrator, and RT_VERSION)
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"unicode/utf16"
)

func main() {
	sizes := []int{256, 64, 48, 32, 16}
	pngBlobs := make([][]byte, len(sizes))

	for i, sz := range sizes {
		img := renderMerahPutihIcon(sz)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic(err)
		}
		pngBlobs[i] = buf.Bytes()
	}

	// 1. Write internal/assets/app.ico
	icoBytes := buildICO(sizes, pngBlobs)
	if err := os.MkdirAll(filepath.Join("internal", "assets"), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join("internal", "assets", "app.ico"), icoBytes, 0644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join("internal", "assets", "app.png"), pngBlobs[0], 0644); err != nil {
		panic(err)
	}

	// 2. Write internal/assets/AppIcon.icns for macOS .app and .dmg
	icnsBytes := buildICNS(pngBlobs[0], pngBlobs[1], pngBlobs[3], pngBlobs[4])
	if err := os.WriteFile(filepath.Join("internal", "assets", "AppIcon.icns"), icnsBytes, 0644); err != nil {
		panic(err)
	}

	// 3. Write rsrc_windows_amd64.syso (PE COFF .rsrc section with Icon + requireAdministrator Manifest + VersionInfo)
	manifestXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity version="1.2.0.0" processorArchitecture="amd64" name="HansenDusenov.ReverseLocal" type="win32"/>
  <description>Reverse.Local - Local Reverse Proxy, SSH Tunnel and LAN DNS Manager</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="requireAdministrator" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
    </windowsSettings>
  </application>
</assembly>`

	sysoBytes := buildWindowsCoffSyso(sizes, pngBlobs, []byte(manifestXML), buildVSVersionInfo())
	if err := os.WriteFile("rsrc_windows_amd64.syso", sysoBytes, 0644); err != nil {
		panic(err)
	}

	fmt.Printf("[OK] Generated internal/assets/app.ico (%d bytes), AppIcon.icns (%d bytes), and rsrc_windows_amd64.syso (%d bytes)\n",
		len(icoBytes), len(icnsBytes), len(sysoBytes))
}

// renderMerahPutihIcon draws a crisp Swiss-precision Crimson Red (#dc2626) & White (#ffffff) app icon.
func renderMerahPutihIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fSize := float64(size)

	crimson := color.RGBA{R: 220, G: 38, B: 38, A: 255}     // #dc2626
	deepCrimson := color.RGBA{R: 185, G: 28, B: 28, A: 255} // #b91c1c
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}     // #ffffff
	darkInk := color.RGBA{R: 24, G: 24, B: 27, A: 255}      // #18181b

	pad := fSize * 0.06
	radius := fSize * 0.22
	borderW := math.Max(1.0, fSize*0.045)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx := float64(x) + 0.5
			fy := float64(y) + 0.5

			dist := roundedBoxSDF(fx, fy, pad, pad, fSize-pad, fSize-pad, radius)
			if dist <= 0 {
				// Inside rounded icon body
				if dist > -borderW {
					img.Set(x, y, darkInk)
				} else if dist > -borderW*2.1 {
					img.Set(x, y, white)
				} else {
					// Top 58% Crimson Red (Merah), Bottom 42% Crisp White (Putih) with subtle divider
					splitY := fSize * 0.56
					if fy < splitY {
						if fy < fSize*0.28 {
							img.Set(x, y, crimson)
						} else {
							img.Set(x, y, deepCrimson)
						}
					} else if math.Abs(fy-splitY) < math.Max(1.0, fSize*0.02) {
						img.Set(x, y, darkInk)
					} else {
						img.Set(x, y, white)
					}
				}
			}
		}
	}

	// Draw bold "RL" glyphs on a 16x16 logical grid mapped inside the icon
	grid := []string{
		"................",
		"................",
		"..RRRRR...LL....",
		"..RR..RR..LL....",
		"..RR..RR..LL....",
		"..RRRRR...LL....",
		"..RRRR....LL....",
		"..RR.RR...LL....",
		"..RR..RR..LLLLLL",
		"................",
		"..DDDDDDDDDDDD..",
		"....>>>>>>>>....",
		"..DDDDDDDDDDDD..",
		"................",
		"................",
		"................",
	}

	cell := (fSize * 0.68) / 16.0
	originX := fSize * 0.16
	originY := fSize * 0.16

	for gy, row := range grid {
		for gx, ch := range row {
			if ch == '.' {
				continue
			}
			x0 := int(originX + float64(gx)*cell)
			y0 := int(originY + float64(gy)*cell)
			x1 := int(originX + float64(gx+1)*cell)
			y1 := int(originY + float64(gy+1)*cell)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}

			var fill color.RGBA
			switch ch {
			case 'R', 'L':
				fill = white
			case 'D':
				fill = darkInk
			case '>':
				fill = crimson
			}

			for py := y0; py < y1; py++ {
				for px := x0; px < x1; px++ {
					if px >= 0 && px < size && py >= 0 && py < size {
						img.Set(px, py, fill)
					}
				}
			}
		}
	}

	return img
}

func roundedBoxSDF(px, py, x0, y0, x1, y1, r float64) float64 {
	cx := (x0 + x1) * 0.5
	cy := (y0 + y1) * 0.5
	hx := (x1-x0)*0.5 - r
	hy := (y1-y0)*0.5 - r
	dx := math.Max(math.Abs(px-cx)-hx, 0)
	dy := math.Max(math.Abs(py-cy)-hy, 0)
	return math.Hypot(dx, dy) - r
}

func buildICO(sizes []int, pngBlobs [][]byte) []byte {
	var buf bytes.Buffer
	count := uint16(len(sizes))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, count)

	offset := uint32(6 + 16*int(count))
	for i, sz := range sizes {
		w := byte(sz)
		h := byte(sz)
		if sz >= 256 {
			w, h = 0, 0
		}
		buf.WriteByte(w)
		buf.WriteByte(h)
		buf.WriteByte(0)
		buf.WriteByte(0)
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngBlobs[i])))
		_ = binary.Write(&buf, binary.LittleEndian, offset)
		offset += uint32(len(pngBlobs[i]))
	}
	for _, b := range pngBlobs {
		buf.Write(b)
	}
	return buf.Bytes()
}

func buildICNS(png256, png64, png32, png16 []byte) []byte {
	type chunk struct {
		ostype string
		data   []byte
	}
	chunks := []chunk{
		{ostype: "ic08", data: png256}, // 256x256 PNG
		{ostype: "icp6", data: png64},  // 64x64 PNG
		{ostype: "icp5", data: png32},  // 32x32 PNG
		{ostype: "icp4", data: png16},  // 16x16 PNG
	}
	totalLen := uint32(8)
	for _, c := range chunks {
		totalLen += uint32(8 + len(c.data))
	}
	var buf bytes.Buffer
	buf.WriteString("icns")
	_ = binary.Write(&buf, binary.BigEndian, totalLen)
	for _, c := range chunks {
		buf.WriteString(c.ostype)
		_ = binary.Write(&buf, binary.BigEndian, uint32(8+len(c.data)))
		buf.Write(c.data)
	}
	return buf.Bytes()
}

// buildWindowsCoffSyso constructs a valid x86_64 COFF (.syso) object file with a .rsrc section
// containing RT_ICON (3), RT_GROUP_ICON (14), RT_VERSION (16), and RT_MANIFEST (24).
func buildWindowsCoffSyso(sizes []int, pngBlobs [][]byte, manifest []byte, versionInfo []byte) []byte {
	// GRPICONDIR structure for RT_GROUP_ICON
	var grpBuf bytes.Buffer
	_ = binary.Write(&grpBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&grpBuf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&grpBuf, binary.LittleEndian, uint16(len(sizes)))
	for i, sz := range sizes {
		w, h := byte(sz), byte(sz)
		if sz >= 256 {
			w, h = 0, 0
		}
		grpBuf.WriteByte(w)
		grpBuf.WriteByte(h)
		grpBuf.WriteByte(0)
		grpBuf.WriteByte(0)
		_ = binary.Write(&grpBuf, binary.LittleEndian, uint16(1))
		_ = binary.Write(&grpBuf, binary.LittleEndian, uint16(32))
		_ = binary.Write(&grpBuf, binary.LittleEndian, uint32(len(pngBlobs[i])))
		_ = binary.Write(&grpBuf, binary.LittleEndian, uint16(i+1)) // nID = 1..5
	}
	grpIconBytes := grpBuf.Bytes()

	// Resource types in ascending ID order:
	// Type 3  = RT_ICON (IDs 1..len(sizes))
	// Type 14 = RT_GROUP_ICON (ID 1)
	// Type 16 = RT_VERSION (ID 1)
	// Type 24 = RT_MANIFEST (ID 1)
	type resPayload struct {
		typeID uint32
		nameID uint32
		data   []byte
	}
	var items []resPayload
	for i, b := range pngBlobs {
		items = append(items, resPayload{typeID: 3, nameID: uint32(i + 1), data: b})
	}
	items = append(items,
		resPayload{typeID: 14, nameID: 1, data: grpIconBytes},
		resPayload{typeID: 16, nameID: 1, data: versionInfo},
		resPayload{typeID: 24, nameID: 1, data: manifest},
	)

	numItems := len(items) // 5 icons + 1 group + 1 version + 1 manifest = 8 items
	numTypes := 4

	// Directory layout:
	// Level 1 (Root): 16 + 4*8 = 48 bytes (offset 0)
	// Level 2 (4 type dirs):
	//   Type 3 dir:  16 + 5*8 = 56 bytes
	//   Type 14 dir: 16 + 1*8 = 24 bytes
	//   Type 16 dir: 16 + 1*8 = 24 bytes
	//   Type 24 dir: 16 + 1*8 = 24 bytes
	// Level 3 (8 lang dirs, each 16 + 1*8 = 24 bytes): 8 * 24 = 192 bytes
	// Data Entries (8 * 16 bytes): 128 bytes
	l1Size := 16 + numTypes*8
	l2Offsets := []int{
		l1Size,
		l1Size + (16 + len(sizes)*8),
		l1Size + (16 + len(sizes)*8) + 24,
		l1Size + (16 + len(sizes)*8) + 48,
	}
	l3Start := l2Offsets[3] + 24
	dataEntryStart := l3Start + numItems*24
	rawDataStart := align8(dataEntryStart + numItems*16)

	var rsrc bytes.Buffer
	// Write Level 1 Root Directory
	writeResDirHeader(&rsrc, uint16(numTypes))
	typeIDs := []uint32{3, 14, 16, 24}
	for i, tid := range typeIDs {
		writeResDirEntry(&rsrc, tid, uint32(l2Offsets[i])|0x80000000)
	}

	// Write Level 2 Type Directories
	itemIdx := 0
	for _, tid := range typeIDs {
		count := 1
		if tid == 3 {
			count = len(sizes)
		}
		writeResDirHeader(&rsrc, uint16(count))
		for c := 0; c < count; c++ {
			l3Off := l3Start + itemIdx*24
			writeResDirEntry(&rsrc, items[itemIdx].nameID, uint32(l3Off)|0x80000000)
			itemIdx++
		}
	}

	// Write Level 3 Language Directories (Language 0x0409 = en-US)
	for i := 0; i < numItems; i++ {
		writeResDirHeader(&rsrc, 1)
		deOff := dataEntryStart + i*16
		writeResDirEntry(&rsrc, 0x0409, uint32(deOff))
	}

	// Calculate data offsets & write Data Entries
	dataOffsets := make([]int, numItems)
	curOff := rawDataStart
	for i, it := range items {
		dataOffsets[i] = curOff
		curOff = align8(curOff + len(it.data))
	}

	for i, it := range items {
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(dataOffsets[i])) // RVA (relocated by COFF reloc)
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(len(it.data)))
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0))
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0))
	}

	// Pad up to rawDataStart
	for rsrc.Len() < rawDataStart {
		rsrc.WriteByte(0)
	}

	// Write raw resource payloads
	for _, it := range items {
		rsrc.Write(it.data)
		for rsrc.Len()%8 != 0 {
			rsrc.WriteByte(0)
		}
	}

	rsrcBytes := rsrc.Bytes()

	// Build COFF header + 1 section header (.rsrc) + relocations + symbol table
	// COFF Header = 20 bytes
	// Section Header = 40 bytes
	// Raw section data starts at 60
	// Relocations follow raw section data (numItems * 10 bytes)
	// Symbol table (1 symbol for .rsrc = 18 bytes) + 4-byte empty string table
	rawSectionOffset := uint32(20 + 40)
	relocOffset := rawSectionOffset + uint32(len(rsrcBytes))
	symtabOffset := relocOffset + uint32(numItems*10)

	var coff bytes.Buffer
	// IMAGE_FILE_HEADER (20 bytes)
	_ = binary.Write(&coff, binary.LittleEndian, uint16(0x8664)) // Machine = IMAGE_FILE_MACHINE_AMD64
	_ = binary.Write(&coff, binary.LittleEndian, uint16(1))      // NumberOfSections = 1
	_ = binary.Write(&coff, binary.LittleEndian, uint32(0))      // TimeDateStamp
	_ = binary.Write(&coff, binary.LittleEndian, symtabOffset)   // PointerToSymbolTable
	_ = binary.Write(&coff, binary.LittleEndian, uint32(1))      // NumberOfSymbols = 1
	_ = binary.Write(&coff, binary.LittleEndian, uint16(0))      // SizeOfOptionalHeader = 0
	_ = binary.Write(&coff, binary.LittleEndian, uint16(0))      // Characteristics = 0

	// IMAGE_SECTION_HEADER (40 bytes)
	var secName [8]byte
	copy(secName[:], ".rsrc")
	coff.Write(secName[:])
	_ = binary.Write(&coff, binary.LittleEndian, uint32(0))                // VirtualSize
	_ = binary.Write(&coff, binary.LittleEndian, uint32(0))                // VirtualAddress
	_ = binary.Write(&coff, binary.LittleEndian, uint32(len(rsrcBytes)))   // SizeOfRawData
	_ = binary.Write(&coff, binary.LittleEndian, rawSectionOffset)         // PointerToRawData
	_ = binary.Write(&coff, binary.LittleEndian, relocOffset)              // PointerToRelocations
	_ = binary.Write(&coff, binary.LittleEndian, uint32(0))                // PointerToLinenumbers
	_ = binary.Write(&coff, binary.LittleEndian, uint16(numItems))         // NumberOfRelocations
	_ = binary.Write(&coff, binary.LittleEndian, uint16(0))                // NumberOfLinenumbers
	_ = binary.Write(&coff, binary.LittleEndian, uint32(0x40000040))       // IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ

	// Section raw data
	coff.Write(rsrcBytes)

	// Relocations (each Data Entry's first 4 bytes is IMAGE_REL_AMD64_ADDR32NB = 3 referencing symbol 0)
	for i := 0; i < numItems; i++ {
		rvaFieldOffset := uint32(dataEntryStart + i*16)
		_ = binary.Write(&coff, binary.LittleEndian, rvaFieldOffset)
		_ = binary.Write(&coff, binary.LittleEndian, uint32(0)) // SymbolTableIndex = 0
		_ = binary.Write(&coff, binary.LittleEndian, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}

	// Symbol 0 (.rsrc section symbol, 18 bytes)
	coff.Write(secName[:])
	_ = binary.Write(&coff, binary.LittleEndian, uint32(0))  // Value = 0
	_ = binary.Write(&coff, binary.LittleEndian, int16(1))   // SectionNumber = 1
	_ = binary.Write(&coff, binary.LittleEndian, uint16(0))  // Type = 0
	coff.WriteByte(3)                                        // StorageClass = IMAGE_SYM_CLASS_STATIC (3)
	coff.WriteByte(0)                                        // NumberOfAuxSymbols = 0

	// Empty string table (size = 4)
	_ = binary.Write(&coff, binary.LittleEndian, uint32(4))

	return coff.Bytes()
}

func writeResDirHeader(w *bytes.Buffer, idEntries uint16) {
	_ = binary.Write(w, binary.LittleEndian, uint32(0)) // Characteristics
	_ = binary.Write(w, binary.LittleEndian, uint32(0)) // TimeDateStamp
	_ = binary.Write(w, binary.LittleEndian, uint16(0)) // MajorVersion
	_ = binary.Write(w, binary.LittleEndian, uint16(0)) // MinorVersion
	_ = binary.Write(w, binary.LittleEndian, uint16(0)) // NumberOfNamedEntries
	_ = binary.Write(w, binary.LittleEndian, idEntries) // NumberOfIdEntries
}

func writeResDirEntry(w *bytes.Buffer, id, offset uint32) {
	_ = binary.Write(w, binary.LittleEndian, id)
	_ = binary.Write(w, binary.LittleEndian, offset)
}

func align8(n int) int {
	return (n + 7) &^ 7
}

func align4Buf(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

func writeUTF16Z(b *bytes.Buffer, s string) {
	for _, u := range utf16.Encode([]rune(s)) {
		_ = binary.Write(b, binary.LittleEndian, u)
	}
	_ = binary.Write(b, binary.LittleEndian, uint16(0))
}

func buildVSVersionInfo() []byte {
	// Build StringTable children
	kvPairs := [][2]string{
		{"CompanyName", "Hansen Dusenov"},
		{"FileDescription", "Reverse.Local - Local Reverse Proxy & SSH Tunnel Manager"},
		{"FileVersion", "1.2.0.0"},
		{"InternalName", "reverse-local.exe"},
		{"LegalCopyright", "Copyright (c) 2026 Hansen Dusenov. All rights reserved."},
		{"OriginalFilename", "reverse-local.exe"},
		{"ProductName", "Reverse.Local"},
		{"ProductVersion", "1.2.0.0"},
	}

	var strTableChildren bytes.Buffer
	for _, kv := range kvPairs {
		align4Buf(&strTableChildren)
		var entry bytes.Buffer
		_ = binary.Write(&entry, binary.LittleEndian, uint16(0)) // wLength placeholder
		_ = binary.Write(&entry, binary.LittleEndian, uint16(len(kv[1])+1))
		_ = binary.Write(&entry, binary.LittleEndian, uint16(1)) // wType = text
		writeUTF16Z(&entry, kv[0])
		align4Buf(&entry)
		writeUTF16Z(&entry, kv[1])
		eb := entry.Bytes()
		binary.LittleEndian.PutUint16(eb[0:2], uint16(len(eb)))
		strTableChildren.Write(eb)
	}

	var strTable bytes.Buffer
	_ = binary.Write(&strTable, binary.LittleEndian, uint16(0))
	_ = binary.Write(&strTable, binary.LittleEndian, uint16(0))
	_ = binary.Write(&strTable, binary.LittleEndian, uint16(1))
	writeUTF16Z(&strTable, "040904B0")
	align4Buf(&strTable)
	strTable.Write(strTableChildren.Bytes())
	stBytes := strTable.Bytes()
	binary.LittleEndian.PutUint16(stBytes[0:2], uint16(len(stBytes)))

	var sfi bytes.Buffer
	_ = binary.Write(&sfi, binary.LittleEndian, uint16(0))
	_ = binary.Write(&sfi, binary.LittleEndian, uint16(0))
	_ = binary.Write(&sfi, binary.LittleEndian, uint16(1))
	writeUTF16Z(&sfi, "StringFileInfo")
	align4Buf(&sfi)
	sfi.Write(stBytes)
	sfiBytes := sfi.Bytes()
	binary.LittleEndian.PutUint16(sfiBytes[0:2], uint16(len(sfiBytes)))

	// VarFileInfo
	var vfiChild bytes.Buffer
	_ = binary.Write(&vfiChild, binary.LittleEndian, uint16(0))
	_ = binary.Write(&vfiChild, binary.LittleEndian, uint16(4))
	_ = binary.Write(&vfiChild, binary.LittleEndian, uint16(0))
	writeUTF16Z(&vfiChild, "Translation")
	align4Buf(&vfiChild)
	_ = binary.Write(&vfiChild, binary.LittleEndian, uint16(0x0409))
	_ = binary.Write(&vfiChild, binary.LittleEndian, uint16(0x04B0))
	vcBytes := vfiChild.Bytes()
	binary.LittleEndian.PutUint16(vcBytes[0:2], uint16(len(vcBytes)))

	var vfi bytes.Buffer
	_ = binary.Write(&vfi, binary.LittleEndian, uint16(0))
	_ = binary.Write(&vfi, binary.LittleEndian, uint16(0))
	_ = binary.Write(&vfi, binary.LittleEndian, uint16(1))
	writeUTF16Z(&vfi, "VarFileInfo")
	align4Buf(&vfi)
	vfi.Write(vcBytes)
	vfiBytes := vfi.Bytes()
	binary.LittleEndian.PutUint16(vfiBytes[0:2], uint16(len(vfiBytes)))

	// Root VS_VERSION_INFO
	var root bytes.Buffer
	_ = binary.Write(&root, binary.LittleEndian, uint16(0))  // wLength placeholder
	_ = binary.Write(&root, binary.LittleEndian, uint16(52)) // sizeof(VS_FIXEDFILEINFO)
	_ = binary.Write(&root, binary.LittleEndian, uint16(0))  // wType = binary
	writeUTF16Z(&root, "VS_VERSION_INFO")
	align4Buf(&root)

	// VS_FIXEDFILEINFO (52 bytes)
	_ = binary.Write(&root, binary.LittleEndian, uint32(0xFEEF04BD)) // dwSignature
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00010000)) // dwStrucVersion
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00010002)) // dwFileVersionMS (1.2)
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00000000)) // dwFileVersionLS (0.0)
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00010002)) // dwProductVersionMS (1.2)
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00000000)) // dwProductVersionLS (0.0)
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x3F))       // dwFileFlagsMask
	_ = binary.Write(&root, binary.LittleEndian, uint32(0))          // dwFileFlags
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00040004)) // VOS_NT_WINDOWS32
	_ = binary.Write(&root, binary.LittleEndian, uint32(0x00000001)) // VFT_APP
	_ = binary.Write(&root, binary.LittleEndian, uint32(0))          // dwFileSubtype
	_ = binary.Write(&root, binary.LittleEndian, uint32(0))          // dwFileDateMS
	_ = binary.Write(&root, binary.LittleEndian, uint32(0))          // dwFileDateLS
	align4Buf(&root)

	root.Write(sfiBytes)
	align4Buf(&root)
	root.Write(vfiBytes)

	out := root.Bytes()
	binary.LittleEndian.PutUint16(out[0:2], uint16(len(out)))
	return out
}

