//go:build ignore

// make_dmg.go builds a macOS Universal (arm64 + amd64 Mach-O Fat Binary) Reverse.Local.app bundle
// and packages it into dist/ReverseLocal-1.2.0-macOS.dmg (ISO9660 + Rock Ridge + Apple UDIF koly trailer)
// so it can be produced directly from Windows, Linux, or macOS without requiring Xcode on the build host.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type dmgFileEntry struct {
	relPath string // e.g. "Reverse.Local.app/Contents/Info.plist"
	mode    uint32 // POSIX mode (e.g. 0755 or 0644)
	data    []byte
}

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		fatalf("getwd: %v", err)
	}
	distDir := filepath.Join(repoRoot, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		fatalf("mkdir dist: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "revlocal_macbuild_*")
	if err != nil {
		fatalf("mkdirtemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	arm64Bin := filepath.Join(tmpDir, "reverse-local-darwin-arm64")
	amd64Bin := filepath.Join(tmpDir, "reverse-local-darwin-amd64")

	fmt.Println("[1/4] Cross-compiling macOS arm64 (Apple Silicon) binary...")
	buildDarwinBinary(repoRoot, "arm64", arm64Bin)

	fmt.Println("[2/4] Cross-compiling macOS amd64 (Intel) binary...")
	buildDarwinBinary(repoRoot, "amd64", amd64Bin)

	fmt.Println("[3/4] Assembling Universal Mach-O Fat Binary (0xCAFEBABE) & Reverse.Local.app bundle...")
	universalBytes, err := buildUniversalMachO(arm64Bin, amd64Bin)
	if err != nil {
		fatalf("universal binary: %v", err)
	}

	infoPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDisplayName</key>
	<string>Reverse.Local</string>
	<key>CFBundleExecutable</key>
	<string>reverse-local</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon.icns</string>
	<key>CFBundleIdentifier</key>
	<string>local.reverse.daemon</string>
	<key>CFBundleName</key>
	<string>Reverse.Local</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>1.2.0</string>
	<key>CFBundleVersion</key>
	<string>1.2.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
`

	launcherScript := `#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
exec "$DIR/reverse-local-bin" -control "$@"
`

	installCommand := `#!/usr/bin/env bash
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
echo "Installing Reverse.Local.app to /Applications..."
sudo rm -rf "/Applications/Reverse.Local.app"
sudo cp -R "$DIR/Reverse.Local.app" "/Applications/Reverse.Local.app"
sudo chmod -R 755 "/Applications/Reverse.Local.app/Contents/MacOS"
echo "Done! Opening Reverse.Local..."
open "/Applications/Reverse.Local.app"
`

	readmeTxt := `Reverse.Local v1.2.0 for macOS (Universal: Apple Silicon & Intel)
=================================================================

1. Drag "Reverse.Local.app" to your /Applications folder, OR
   double-click "Install Reverse.Local.command".
2. Launch Reverse.Local.app. It will prompt for Administrator privileges
   so it can bind ports 80/443, sync /etc/hosts, and trust the local Root CA.
3. Access the Workbench at https://reverse.local
`

	icnsPath := filepath.Join(repoRoot, "internal", "assets", "AppIcon.icns")
	icnsBytes, err := os.ReadFile(icnsPath)
	if err != nil {
		fatalf("read AppIcon.icns: %v", err)
	}

	entries := []dmgFileEntry{
		{relPath: ".VolumeIcon.icns", mode: 0644, data: icnsBytes},
		{relPath: "Reverse.Local.app/Contents/Info.plist", mode: 0644, data: []byte(infoPlist)},
		{relPath: "Reverse.Local.app/Contents/Resources/AppIcon.icns", mode: 0644, data: icnsBytes},
		{relPath: "Reverse.Local.app/Contents/MacOS/reverse-local", mode: 0755, data: []byte(launcherScript)},
		{relPath: "Reverse.Local.app/Contents/MacOS/reverse-local-bin", mode: 0755, data: universalBytes},
		{relPath: "Install Reverse.Local.command", mode: 0755, data: []byte(installCommand)},
		{relPath: "README.txt", mode: 0644, data: []byte(readmeTxt)},
	}

	dmgPath := filepath.Join(distDir, "ReverseLocal-1.2.0-macOS.dmg")
	fmt.Printf("[4/4] Packaging macOS Disk Image (%s)...\n", dmgPath)
	if err := writeHybridUDIFDMG(dmgPath, "Reverse.Local", entries); err != nil {
		fatalf("write dmg: %v", err)
	}
	fmt.Printf("[OK] Built macOS Universal Disk Image: %s\n", dmgPath)
}

func buildDarwinBinary(repoRoot, arch, outPath string) {
	cmd := exec.Command("go", "build", "-o", outPath, ".")
	cmd.Dir = repoRoot
	env := os.Environ()
	filtered := make([]string, 0, len(env)+3)
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOOS=") || strings.HasPrefix(kv, "GOARCH=") || strings.HasPrefix(kv, "CGO_ENABLED=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered, "GOOS=darwin", "GOARCH="+arch, "CGO_ENABLED=0")
	cmd.Env = filtered
	out, err := cmd.CombinedOutput()
	if err != nil {
		fatalf("go build darwin/%s failed: %v\n%s", arch, err, string(out))
	}
}

// buildUniversalMachO combines an amd64 Mach-O and arm64 Mach-O into a standard macOS Universal Fat Binary (0xCAFEBABE).
func buildUniversalMachO(arm64Path, amd64Path string) ([]byte, error) {
	armData, err := os.ReadFile(arm64Path)
	if err != nil {
		return nil, err
	}
	amdData, err := os.ReadFile(amd64Path)
	if err != nil {
		return nil, err
	}
	if len(armData) < 8 || len(amdData) < 8 {
		return nil, fmt.Errorf("invalid Mach-O slice")
	}

	const alignBits = 14 // 16KB page alignment (2^14 = 16384), required by macOS arm64
	const pageSize = 1 << alignBits

	amdOffset := uint32(pageSize)
	armOffset := alignUp(amdOffset+uint32(len(amdData)), pageSize)
	totalSize := int(armOffset) + len(armData)

	buf := make([]byte, totalSize)
	// fat_header: magic (0xCAFEBABE), nfat_arch (2)
	binary.BigEndian.PutUint32(buf[0:4], 0xCAFEBABE)
	binary.BigEndian.PutUint32(buf[4:8], 2)

	// Slice 1: x86_64 (CPU_TYPE_X86_64 = 0x01000007, CPU_SUBTYPE_X86_64_ALL = 3)
	amdCpuType := binary.LittleEndian.Uint32(amdData[4:8])
	amdCpuSub := binary.LittleEndian.Uint32(amdData[8:12])
	binary.BigEndian.PutUint32(buf[8:12], amdCpuType)
	binary.BigEndian.PutUint32(buf[12:16], amdCpuSub)
	binary.BigEndian.PutUint32(buf[16:20], amdOffset)
	binary.BigEndian.PutUint32(buf[20:24], uint32(len(amdData)))
	binary.BigEndian.PutUint32(buf[24:28], alignBits)

	// Slice 2: arm64 (CPU_TYPE_ARM64 = 0x0100000C, CPU_SUBTYPE_ARM64_ALL = 0)
	armCpuType := binary.LittleEndian.Uint32(armData[4:8])
	armCpuSub := binary.LittleEndian.Uint32(armData[8:12])
	binary.BigEndian.PutUint32(buf[28:32], armCpuType)
	binary.BigEndian.PutUint32(buf[32:36], armCpuSub)
	binary.BigEndian.PutUint32(buf[36:40], armOffset)
	binary.BigEndian.PutUint32(buf[40:44], uint32(len(armData)))
	binary.BigEndian.PutUint32(buf[44:48], alignBits)

	copy(buf[amdOffset:], amdData)
	copy(buf[armOffset:], armData)
	return buf, nil
}

func alignUp(v, align uint32) uint32 {
	return (v + align - 1) &^ (align - 1)
}

// writeHybridUDIFDMG writes an ISO9660 + Rock Ridge filesystem image wrapped with an Apple UDIF (koly) trailer
// so macOS DiskImageMounter mounts it natively with full POSIX permissions and mixed-case filenames.
func writeHybridUDIFDMG(outPath, volName string, files []dmgFileEntry) error {
	isoBytes := buildRockRidgeISO(volName, files)

	// Pad ISO payload to 2048-byte sector boundary
	if rem := len(isoBytes) % 2048; rem != 0 {
		isoBytes = append(isoBytes, make([]byte, 2048-rem)...)
	}

	sectorCount := uint64(len(isoBytes) / 512)
	dataForkLen := uint64(len(isoBytes))

	// Build UDIF blkx table (1 raw uncompressed run covering all 512-byte sectors + terminator run)
	blkx := make([]byte, 204+2*40)
	binary.BigEndian.PutUint32(blkx[0:4], 0x6D697368) // 'mish' signature
	binary.BigEndian.PutUint32(blkx[4:8], 1)          // version 1
	binary.BigEndian.PutUint64(blkx[8:16], 0)         // first sector
	binary.BigEndian.PutUint64(blkx[16:24], sectorCount)
	binary.BigEndian.PutUint64(blkx[24:32], 0) // data start offset
	binary.BigEndian.PutUint32(blkx[32:36], uint32(sectorCount))
	binary.BigEndian.PutUint32(blkx[36:40], 1)          // blocks descriptor
	binary.BigEndian.PutUint32(blkx[64:68], 2)          //Checksum type: CRC32
	binary.BigEndian.PutUint32(blkx[68:72], 32)         //Checksum bits
	dataCRC := crc32.ChecksumIEEE(isoBytes)
	binary.BigEndian.PutUint32(blkx[72:76], dataCRC)
	binary.BigEndian.PutUint32(blkx[200:204], 2) // 2 chunk runs

	// Run 0: BLOCK_RAW (0x00000001)
	run0 := blkx[204:244]
	binary.BigEndian.PutUint32(run0[0:4], 0x00000001)
	binary.BigEndian.PutUint32(run0[4:8], 0)
	binary.BigEndian.PutUint64(run0[8:16], 0)
	binary.BigEndian.PutUint64(run0[16:24], sectorCount)
	binary.BigEndian.PutUint64(run0[24:32], 0)
	binary.BigEndian.PutUint64(run0[32:40], dataForkLen)

	// Run 1: BLOCK_TERMINATOR (0xFFFFFFFF)
	run1 := blkx[244:284]
	binary.BigEndian.PutUint32(run1[0:4], 0xFFFFFFFF)
	binary.BigEndian.PutUint32(run1[4:8], 0)
	binary.BigEndian.PutUint64(run1[8:16], sectorCount)
	binary.BigEndian.PutUint64(run1[16:24], 0)
	binary.BigEndian.PutUint64(run1[24:32], dataForkLen)
	binary.BigEndian.PutUint64(run1[32:40], 0)

	plistXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>resource-fork</key>
	<dict>
		<key>blkx</key>
		<array>
			<dict>
				<key>Attributes</key>
				<string>0x0050</string>
				<key>Data</key>
				<data>%s</data>
				<key>ID</key>
				<string>-1</string>
				<key>Name</key>
				<string>whole disk (ISO9660 : 0)</string>
			</dict>
		</array>
	</dict>
</dict>
</plist>
`, encodeBase64(blkx))

	xmlBytes := []byte(plistXML)
	xmlOffset := dataForkLen
	xmlLength := uint64(len(xmlBytes))

	// 512-byte UDIF koly trailer
	koly := make([]byte, 512)
	binary.BigEndian.PutUint32(koly[0:4], 0x6B6F6C79) // 'koly'
	binary.BigEndian.PutUint32(koly[4:8], 4)          // version 4
	binary.BigEndian.PutUint32(koly[8:12], 512)       // header size 512
	binary.BigEndian.PutUint32(koly[12:16], 1)        // flags: flattened
	binary.BigEndian.PutUint64(koly[16:24], 0)        // running data fork offset
	binary.BigEndian.PutUint64(koly[24:32], 0)        // data fork offset
	binary.BigEndian.PutUint64(koly[32:40], dataForkLen)
	binary.BigEndian.PutUint64(koly[40:48], 0) // rsrc fork offset
	binary.BigEndian.PutUint64(koly[48:56], 0) // rsrc fork length
	binary.BigEndian.PutUint32(koly[56:60], 1) // segment number
	binary.BigEndian.PutUint32(koly[60:64], 1) // segment count
	// Segment GUID
	copy(koly[64:80], []byte{0x8F, 0x9E, 0x4A, 0x2D, 0x7B, 0x3C, 0x4E, 0x1A, 0x9D, 0x5F, 0x2C, 0x6B, 0x8A, 0x1E, 0x3F, 0x40})
	binary.BigEndian.PutUint32(koly[80:84], 2)           // Data checksum type: CRC32
	binary.BigEndian.PutUint32(koly[84:88], 32)          // Data checksum bits
	binary.BigEndian.PutUint32(koly[88:92], dataCRC)     // Data CRC32
	binary.BigEndian.PutUint64(koly[216:224], xmlOffset) // XML offset
	binary.BigEndian.PutUint64(koly[224:232], xmlLength) // XML length
	masterCRC := crc32.ChecksumIEEE(blkx[72:76])
	binary.BigEndian.PutUint32(koly[352:356], 2)         // Master checksum type: CRC32
	binary.BigEndian.PutUint32(koly[356:360], 32)        // Master checksum bits
	binary.BigEndian.PutUint32(koly[360:364], masterCRC) // Master CRC32
	binary.BigEndian.PutUint32(koly[488:492], 1)         // Image variant: UDIF device image
	binary.BigEndian.PutUint64(koly[492:500], sectorCount)

	var out bytes.Buffer
	out.Grow(len(isoBytes) + len(xmlBytes) + 512)
	out.Write(isoBytes)
	out.Write(xmlBytes)
	out.Write(koly)

	return os.WriteFile(outPath, out.Bytes(), 0644)
}

type isoNode struct {
	name     string
	isoName  string
	isDir    bool
	mode     uint32
	data     []byte
	children []*isoNode
	parent   *isoNode
	sector   uint32
	size     uint32
}

func buildRockRidgeISO(volName string, entries []dmgFileEntry) []byte {
	const blockSize = 2048
	root := &isoNode{name: "", isoName: "\x00", isDir: true, mode: 040755}
	root.parent = root

	for _, e := range entries {
		parts := strings.Split(strings.Trim(e.relPath, "/"), "/")
		cur := root
		for i, p := range parts {
			isLast := i == len(parts)-1
			if isLast {
				cur.children = append(cur.children, &isoNode{
					name:    p,
					isoName: toISOFileName(p, false, len(cur.children)+1),
					isDir:   false,
					mode:    0100000 | (e.mode & 0777),
					data:    e.data,
					parent:  cur,
					size:    uint32(len(e.data)),
				})
			} else {
				var found *isoNode
				for _, ch := range cur.children {
					if ch.isDir && ch.name == p {
						found = ch
						break
					}
				}
				if found == nil {
					found = &isoNode{
						name:    p,
						isoName: toISOFileName(p, true, len(cur.children)+1),
						isDir:   true,
						mode:    040755,
						parent:  cur,
					}
					cur.children = append(cur.children, found)
				}
				cur = found
			}
		}
	}

	// Collect all directories in BFS order
	var dirs []*isoNode
	var files []*isoNode
	queue := []*isoNode{root}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		dirs = append(dirs, d)
		sort.Slice(d.children, func(i, j int) bool {
			return d.children[i].isoName < d.children[j].isoName
		})
		for _, ch := range d.children {
			if ch.isDir {
				queue = append(queue, ch)
			} else {
				files = append(files, ch)
			}
		}
	}

	// Sector layout:
	// 0..15: System area (zeros)
	// 16: Primary Volume Descriptor
	// 17: Volume Descriptor Set Terminator
	// 18: Path Table (L)
	// 19: Path Table (M)
	// 20+: Directory extents, then file data extents
	nextSector := uint32(20)
	for _, d := range dirs {
		d.sector = nextSector
		d.size = blockSize
		nextSector++
	}
	for _, f := range files {
		f.sector = nextSector
		blocks := (uint32(len(f.data)) + blockSize - 1) / blockSize
		if blocks == 0 {
			blocks = 1
		}
		nextSector += blocks
	}

	totalSectors := nextSector + 1
	img := make([]byte, totalSectors*blockSize)

	// Build directory sectors
	for _, d := range dirs {
		dirBuf := buildDirSector(d, d == root)
		copy(img[d.sector*blockSize:], dirBuf)
	}

	// Write file data
	for _, f := range files {
		copy(img[f.sector*blockSize:], f.data)
	}

	// Build L & M Path Tables
	pathTableL := buildPathTable(dirs, false)
	pathTableM := buildPathTable(dirs, true)
	copy(img[18*blockSize:], pathTableL)
	copy(img[19*blockSize:], pathTableM)

	// Primary Volume Descriptor at sector 16
	pvd := img[16*blockSize : 17*blockSize]
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	pvd[6] = 1
	padRightASCII(pvd[8:40], "APPLE MACOS")
	padRightASCII(pvd[40:72], strings.ToUpper(strings.ReplaceAll(volName, ".", "_")))
	putBothEndian32(pvd[80:88], totalSectors)
	putBothEndian16(pvd[120:124], 1)
	putBothEndian16(pvd[124:128], 1)
	putBothEndian16(pvd[128:132], blockSize)
	putBothEndian32(pvd[132:140], uint32(len(pathTableL)))
	binary.LittleEndian.PutUint32(pvd[140:144], 18)
	binary.BigEndian.PutUint32(pvd[148:152], 19)

	rootRecord := buildIsoDirRecord("\x00", root.sector, root.size, true, nil)
	copy(pvd[156:156+len(rootRecord)], rootRecord)

	padRightASCII(pvd[190:318], "REVERSE_LOCAL_MACOS")
	padRightASCII(pvd[318:446], "HANSEN DUSENOV")
	nowStr := time.Now().UTC().Format("20060102150405") + "00"
	copy(pvd[813:829], nowStr)
	copy(pvd[830:846], nowStr)
	pvd[881] = 1

	// Volume Descriptor Set Terminator at sector 17
	vdst := img[17*blockSize : 18*blockSize]
	vdst[0] = 255
	copy(vdst[1:6], "CD001")
	vdst[6] = 1

	return img
}

func buildDirSector(d *isoNode, isRoot bool) []byte {
	sec := make([]byte, 2048)
	offset := 0

	var selfSU []byte
	if isRoot {
		// SUSP 'SP' + 'RR' indicator in root '.' record
		selfSU = []byte{'S', 'P', 7, 1, 0xBE, 0xEF, 0, 'R', 'R', 5, 1, 0x89}
	}
	dot := buildIsoDirRecord("\x00", d.sector, d.size, true, selfSU)
	copy(sec[offset:], dot)
	offset += len(dot)

	dotdot := buildIsoDirRecord("\x01", d.parent.sector, d.parent.size, true, nil)
	copy(sec[offset:], dotdot)
	offset += len(dotdot)

	for _, ch := range d.children {
		su := buildRockRidgeSU(ch.name, ch.mode, ch.isDir)
		rec := buildIsoDirRecord(ch.isoName, ch.sector, ch.size, ch.isDir, su)
		if offset+len(rec) > 2048 {
			break
		}
		copy(sec[offset:], rec)
		offset += len(rec)
	}
	return sec
}

func buildRockRidgeSU(realName string, mode uint32, isDir bool) []byte {
	var su bytes.Buffer
	// PX: POSIX file attributes (36 bytes)
	px := make([]byte, 36)
	px[0], px[1], px[2], px[3] = 'P', 'X', 36, 1
	putBothEndian32(px[4:12], mode)
	nlink := uint32(1)
	if isDir {
		nlink = 2
	}
	putBothEndian32(px[12:20], nlink)
	putBothEndian32(px[20:28], 0) // uid 0
	putBothEndian32(px[28:36], 0) // gid 0
	su.Write(px)

	// NM: Alternate Name (mixed-case filename)
	nmBytes := []byte(realName)
	nm := make([]byte, 5+len(nmBytes))
	nm[0], nm[1], nm[2], nm[3], nm[4] = 'N', 'M', byte(5 + len(nmBytes)), 1, 0
	copy(nm[5:], nmBytes)
	su.Write(nm)
	return su.Bytes()
}

func buildIsoDirRecord(isoName string, extent, size uint32, isDir bool, su []byte) []byte {
	nameBytes := []byte(isoName)
	baseLen := 33 + len(nameBytes)
	if len(nameBytes)%2 == 0 {
		baseLen++
	}
	totalLen := baseLen + len(su)
	if totalLen%2 != 0 {
		totalLen++
	}
	rec := make([]byte, totalLen)
	rec[0] = byte(totalLen)
	putBothEndian32(rec[2:10], extent)
	putBothEndian32(rec[10:18], size)
	now := time.Now().UTC()
	rec[18] = byte(now.Year() - 1900)
	rec[19] = byte(now.Month())
	rec[20] = byte(now.Day())
	rec[21] = byte(now.Hour())
	rec[22] = byte(now.Minute())
	rec[23] = byte(now.Second())
	if isDir {
		rec[25] = 2
	}
	putBothEndian16(rec[28:32], 1)
	rec[32] = byte(len(nameBytes))
	copy(rec[33:], nameBytes)
	copy(rec[baseLen:], su)
	return rec
}

func buildPathTable(dirs []*isoNode, bigEndian bool) []byte {
	var buf bytes.Buffer
	idxMap := make(map[*isoNode]uint16, len(dirs))
	for i, d := range dirs {
		idxMap[d] = uint16(i + 1)
	}
	for i, d := range dirs {
		name := d.isoName
		if i == 0 {
			name = "\x00"
		}
		hdr := make([]byte, 8+len(name))
		hdr[0] = byte(len(name))
		parentIdx := idxMap[d.parent]
		if bigEndian {
			binary.BigEndian.PutUint32(hdr[2:6], d.sector)
			binary.BigEndian.PutUint16(hdr[6:8], parentIdx)
		} else {
			binary.LittleEndian.PutUint32(hdr[2:6], d.sector)
			binary.LittleEndian.PutUint16(hdr[6:8], parentIdx)
		}
		copy(hdr[8:], name)
		buf.Write(hdr)
		if len(name)%2 != 0 {
			buf.WriteByte(0)
		}
	}
	return buf.Bytes()
}

func toISOFileName(name string, isDir bool, idx int) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'a' && r <= 'z' {
			return r - ('a' - 'A')
		}
		return '_'
	}, name)
	if len(clean) > 6 {
		clean = clean[:6]
	}
	if isDir {
		return fmt.Sprintf("%s%02d", clean, idx)
	}
	return fmt.Sprintf("%s%02d.BIN;1", clean, idx)
}

func putBothEndian16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b[0:2], v)
	binary.BigEndian.PutUint16(b[2:4], v)
}

func putBothEndian32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b[0:4], v)
	binary.BigEndian.PutUint32(b[4:8], v)
}

func padRightASCII(dst []byte, s string) {
	for i := range dst {
		dst[i] = ' '
	}
	copy(dst, []byte(s))
}

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encodeBase64(src []byte) string {
	var out strings.Builder
	for i := 0; i < len(src); i += 3 {
		rem := len(src) - i
		var b0, b1, b2 byte
		b0 = src[i]
		if rem > 1 {
			b1 = src[i+1]
		}
		if rem > 2 {
			b2 = src[i+2]
		}
		out.WriteByte(b64Alphabet[b0>>2])
		out.WriteByte(b64Alphabet[((b0&0x03)<<4)|(b1>>4)])
		if rem > 1 {
			out.WriteByte(b64Alphabet[((b1&0x0f)<<2)|(b2>>6)])
		} else {
			out.WriteByte('=')
		}
		if rem > 2 {
			out.WriteByte(b64Alphabet[b2&0x3f])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[FATAL] "+format+"\n", args...)
	os.Exit(1)
}

