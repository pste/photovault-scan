package scanner

import "strings"

// Allowlist, non blocklist: le blocklist perdono sempre contro .AAE, .XMP,
// .THM, .LRV e compagnia. Quello che non e' qui dentro non entra in archivio.
//
// L'elenco dei video e' cresciuto dopo il primo giro su un archivio vero: 127
// .mpg, piu' webm, vob, wmv, f4v, m2t, flv, mxv e un rm erano finiti fra i
// "file non gestiti" per ~7,7 GB di filmati veri, invisibili nella libreria.
//
// Restano deliberatamente FUORI i file di servizio AVCHD -- .cpi, .mpl, .bdm,
// .tdt, .tid -- che stanno accanto ai video ma non contengono video, e i
// formati audio (.mp3, .m4a, .wav, .ac3): photovault gestisce immagini e
// video, e un file audio in una griglia di anteprime non ha senso.
var kinds = map[string]string{
	// immagini
	"jpg": "image", "jpeg": "image", "png": "image", "gif": "image",
	"webp": "image", "heic": "image", "heif": "image", "avif": "image",
	"tif": "image", "tiff": "image", "bmp": "image",
	// video
	"mp4": "video", "mov": "video", "m4v": "video", "avi": "video",
	"mkv": "video", "mts": "video", "m2ts": "video", "3gp": "video",
	"3g2": "video", "mpg": "video", "mpeg": "video", "mpe": "video",
	"m2v": "video", "m2t": "video", "vob": "video", "wmv": "video",
	"asf": "video", "flv": "video", "f4v": "video", "mxv": "video",
	"webm": "video", "ogv": "video", "divx": "video", "rm": "video",
	"dv": "video", "mod": "video", "tod": "video",
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

// Estensioni di directory che sono in realta' il pacchetto interno di
// un'applicazione: dentro c'e' la sua cache, non le foto dell'utente.
//
// Il caso che le ha motivate: "Lightroom 5 Catalog Smart Previews.lrdata"
// conteneva 386 DNG da 387 kB in media, sparsi su 383 sottocartelle. Sono le
// Smart Preview del catalogo -- proxy che Lightroom rigenera da solo -- e in
// libreria comparivano come 386 foto senza anteprima possibile, perche' un DNG
// photovault non lo sa decodificare. Non e' un problema di formato: e' che quei
// file non sono fotografie dell'utente, esattamente come non lo sono le
// thumbnail dentro .photovault.
//
// Sull'estensione e non sul nome perche' il nome del pacchetto contiene quello
// del catalogo, che cambia da persona a persona.
var skipDirSuffixes = []string{
	".lrdata",         // cache di un catalogo Lightroom
	".lrlibrary",      // catalogo Lightroom CC
	".photoslibrary",  // libreria Foto di macOS
	".aplibrary",      // libreria Aperture
	".migratedaplibrary",
}

func skipDir(name string) bool {
	if skipDirs[name] {
		return true
	}
	lower := strings.ToLower(name)
	for _, suffix := range skipDirSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	// Le directory nascoste non contengono foto dell'utente, e su macOS/NAS
	// ne compaiono parecchie.
	return strings.HasPrefix(name, ".")
}
