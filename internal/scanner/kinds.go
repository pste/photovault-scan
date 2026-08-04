package scanner

import "strings"

// Allowlist, non blocklist: le blocklist perdono sempre contro .AAE, .XMP,
// .THM, .LRV e compagnia. Quello che non e' qui dentro non entra in archivio.
var kinds = map[string]string{
	// immagini
	"jpg": "image", "jpeg": "image", "png": "image", "gif": "image",
	"webp": "image", "heic": "image", "heif": "image", "avif": "image",
	"tif": "image", "tiff": "image", "bmp": "image",
	// video
	"mp4": "video", "mov": "video", "m4v": "video", "avi": "video",
	"mkv": "video", "mts": "video", "m2ts": "video", "3gp": "video",
	// raw: la riga viene creata, ma la thumbnail resta 'unsupported' in fase 2
	"cr2": "raw", "cr3": "raw", "nef": "raw", "arw": "raw",
	"dng": "raw", "raf": "raw", "orf": "raw", "rw2": "raw",
}

// Directory da saltare: la nostra area privata, i cestini e le cartelle di
// servizio dei NAS, e qualunque directory nascosta.
var skipDirs = map[string]bool{
	".photovault":          true,
	"@eaDir":               true,
	"@Recycle":             true,
	"#recycle":             true,
	"#snapshot":            true,
	"@Recently-Snapshot":   true,
	"$RECYCLE.BIN":         true,
	"System Volume Information": true,
}

func kindOf(ext string) (string, bool) {
	kind, ok := kinds[strings.ToLower(ext)]
	return kind, ok
}

func skipDir(name string) bool {
	if skipDirs[name] {
		return true
	}
	// Le directory nascoste non contengono foto dell'utente, e su macOS/NAS
	// ne compaiono parecchie.
	return strings.HasPrefix(name, ".")
}
