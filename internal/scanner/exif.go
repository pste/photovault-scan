package scanner

import (
	"log/slog"
	"os"
	"time"

	"github.com/evanoberholster/imagemeta"

	"github.com/pste/photovault-scan/internal/api"
)

// readExif estrae data di scatto, fotocamera, orientamento e GPS.
//
// Si usa imagemeta e non goexif: e' in Go puro (niente CGO, quindi il binario
// resta statico) ed e' l'unica libreria mantenuta che legge l'EXIF anche da
// HEIC, TIFF, CR2/CR3 e DNG, non solo da JPEG.
//
// Non e' mai fatale: una foto senza EXIF, o con un EXIF malformato, entra
// comunque in archivio con i soli dati del filesystem.
func readExif(item *api.MediaItem, path string, log *slog.Logger) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	data, err := imagemeta.Decode(file)
	if err != nil {
		// I PNG e i file senza EXIF finiscono qui: e' il caso normale, non un
		// errore da segnalare a livello warn.
		log.Debug("nessun exif", "path", path, "err", err)
		return
	}

	if when := data.OriginalDate(); !when.IsZero() {
		captured := when.UTC().Format(time.RFC3339)
		item.CaptureTS = &captured
	}

	if make := data.IFD0.Make; make != "" {
		item.CameraMake = &make
	}
	if model := data.IFD0.Model; model != "" {
		item.CameraModel = &model
	}

	// L'orientamento serve al job thumbs per raddrizzare l'immagine prima di
	// ridimensionarla. 1 significa "gia' dritta" e non vale la pena salvarlo.
	if orientation := int(data.IFD0.Orientation); orientation > 1 && orientation <= 8 {
		item.Orientation = &orientation
	}

	// GPSInfo non espone un modo per distinguere "assente" da "zero", ma
	// latitudine e longitudine entrambe esattamente 0 cadono in mezzo
	// all'Atlantico: nessuna foto reale ha quelle coordinate.
	lat := data.GPS.Latitude()
	lon := data.GPS.Longitude()
	if lat != 0 || lon != 0 {
		item.GpsLat = &lat
		item.GpsLon = &lon
	}
}
