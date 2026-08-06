package thumbs

import (
	"io"
	"log/slog"

	"github.com/evanoberholster/imagemeta"

	"github.com/pste/photovault-scan/internal/api"
)

// readExif estrae data di scatto, fotocamera, orientamento e GPS.
//
// Si usa imagemeta e non goexif: e' in Go puro (niente CGO, quindi il binario
// resta statico) ed e' l'unica libreria mantenuta che legge l'EXIF anche da
// HEIC, TIFF, CR2/CR3 e DNG, non solo da JPEG.
//
// Prende un file gia' aperto e lo riavvolge da solo: cosi' la stessa apertura
// serve sia all'EXIF sia alla decodifica dell'immagine, e su CIFS si paga una
// open per file invece di due.
//
// Non e' mai fatale: una foto senza EXIF, o con un EXIF malformato, resta in
// archivio con i soli dati del filesystem.
func readExif(meta *api.MediaMeta, file io.ReadSeeker, path string, log *slog.Logger) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return
	}
	defer file.Seek(0, io.SeekStart)

	data, err := imagemeta.Decode(file)
	if err != nil {
		// I PNG e i file senza EXIF finiscono qui: e' il caso normale, non un
		// errore da segnalare a livello warn.
		log.Debug("nessun exif", "path", path, "err", err)
		return
	}

	if captured := captureTS(data.OriginalDate()); captured != nil {
		meta.CaptureTS = captured
	}

	if make := cleanText(data.IFD0.Make); make != "" {
		meta.CameraMake = &make
	}
	if model := cleanText(data.IFD0.Model); model != "" {
		meta.CameraModel = &model
	}

	// L'orientamento serve subito qui sotto per raddrizzare l'immagine prima di
	// ridimensionarla. 1 significa "gia' dritta" e non vale la pena salvarlo.
	if orientation := int(data.IFD0.Orientation); orientation > 1 && orientation <= 8 {
		meta.Orientation = &orientation
	}

	// GPSInfo non espone un modo per distinguere "assente" da "zero", ma
	// latitudine e longitudine entrambe esattamente 0 cadono in mezzo
	// all'Atlantico: nessuna foto reale ha quelle coordinate.
	lat := data.GPS.Latitude()
	lon := data.GPS.Longitude()
	if lat != 0 || lon != 0 {
		meta.GpsLat = &lat
		meta.GpsLon = &lon
	}
}
