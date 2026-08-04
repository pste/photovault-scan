package scanner

import (
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/pste/photovault-scan/internal/api"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// La fixture e' un JPEG con EXIF completo: Apple iPhone 11, orientamento 6,
// scatto del 10 aprile 2019, coordinate di Barcellona.
func TestReadExif(t *testing.T) {
	item := &api.MediaItem{MediaKind: "image", Ext: "jpg"}
	readExif(item, "testdata/exif_sample.jpg", quietLogger())

	if item.CaptureTS == nil {
		t.Fatal("data di scatto non letta")
	}
	if got := *item.CaptureTS; got != "2019-04-10T12:34:56Z" {
		t.Errorf("data di scatto = %q, attesa 2019-04-10T12:34:56Z", got)
	}

	if item.CameraMake == nil || *item.CameraMake != "Apple" {
		t.Errorf("marca = %v, attesa Apple", item.CameraMake)
	}
	if item.CameraModel == nil || *item.CameraModel != "iPhone 11" {
		t.Errorf("modello = %v, atteso iPhone 11", item.CameraModel)
	}

	// L'orientamento 6 significa "ruotata di 90 in senso orario": se non
	// arriva fin qui, il job thumbs non raddrizza l'immagine e meta' delle
	// foto verticali finisce storta nella griglia.
	if item.Orientation == nil || *item.Orientation != 6 {
		t.Errorf("orientamento = %v, atteso 6", item.Orientation)
	}

	if item.GpsLat == nil || item.GpsLon == nil {
		t.Fatal("coordinate GPS non lette")
	}
	// 41 24' 13" N, 2 10' 28" E
	if math.Abs(*item.GpsLat-41.4036) > 0.001 {
		t.Errorf("latitudine = %v, attesa ~41.4036", *item.GpsLat)
	}
	if math.Abs(*item.GpsLon-2.1744) > 0.001 {
		t.Errorf("longitudine = %v, attesa ~2.1744", *item.GpsLon)
	}
}

// Un file senza EXIF non deve essere un errore: entra in archivio con i soli
// dati del filesystem. E' il caso di ogni PNG e di ogni scansione.
func TestReadExifSenzaExif(t *testing.T) {
	item := &api.MediaItem{MediaKind: "image", Ext: "png"}
	readExif(item, "testdata/no_exif.png", quietLogger())

	if item.CaptureTS != nil || item.CameraMake != nil || item.Orientation != nil {
		t.Errorf("un file senza EXIF non deve valorizzare nulla: %+v", item)
	}
}

// enrich deve garantire sempre una data di scatto: senza EXIF si ripiega
// sull'mtime, altrimenti la griglia non avrebbe un criterio di ordinamento.
func TestEnrichRipiegaSuMtime(t *testing.T) {
	item := &api.MediaItem{
		MediaKind: "image",
		Ext:       "png",
		Modified:  "2021-01-01T00:00:00Z",
	}
	enrich(item, "testdata/no_exif.png", quietLogger())

	if item.CaptureTS == nil {
		t.Fatal("data di scatto assente anche dopo il ripiego")
	}
	if *item.CaptureTS != "2021-01-01T00:00:00Z" {
		t.Errorf("data di scatto = %q, attesa quella di modifica", *item.CaptureTS)
	}
}
