package thumbs

import (
	"image"
	"io"
	"log/slog"
	"math"
	"os"
	"testing"

	"github.com/pste/photovault-scan/internal/api"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// readExifDaFile apre la fixture come fa il job vero.
func readExifDaFile(t *testing.T, path string) *api.MediaMeta {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("apertura della fixture fallita: %v", err)
	}
	defer file.Close()

	meta := &api.MediaMeta{}
	readExif(meta, file, path, quietLogger())
	return meta
}

// La fixture e' un JPEG con EXIF completo: Apple iPhone 11, orientamento 6,
// scatto del 10 aprile 2019, coordinate di Barcellona.
func TestReadExif(t *testing.T) {
	meta := readExifDaFile(t, "testdata/exif_sample.jpg")

	if meta.CaptureTS == nil {
		t.Fatal("data di scatto non letta")
	}
	if got := *meta.CaptureTS; got != "2019-04-10T12:34:56Z" {
		t.Errorf("data di scatto = %q, attesa 2019-04-10T12:34:56Z", got)
	}

	if meta.CameraMake == nil || *meta.CameraMake != "Apple" {
		t.Errorf("marca = %v, attesa Apple", meta.CameraMake)
	}
	if meta.CameraModel == nil || *meta.CameraModel != "iPhone 11" {
		t.Errorf("modello = %v, atteso iPhone 11", meta.CameraModel)
	}

	// L'orientamento 6 significa "ruotata di 90 in senso orario": se non
	// arriva fin qui, l'immagine non viene raddrizzata e meta' delle foto
	// verticali finisce storta nella griglia.
	if meta.Orientation == nil || *meta.Orientation != 6 {
		t.Errorf("orientamento = %v, atteso 6", meta.Orientation)
	}

	if meta.GpsLat == nil || meta.GpsLon == nil {
		t.Fatal("coordinate GPS non lette")
	}
	// 41 24' 13" N, 2 10' 28" E
	if math.Abs(*meta.GpsLat-41.4036) > 0.001 {
		t.Errorf("latitudine = %v, attesa ~41.4036", *meta.GpsLat)
	}
	if math.Abs(*meta.GpsLon-2.1744) > 0.001 {
		t.Errorf("longitudine = %v, attesa ~2.1744", *meta.GpsLon)
	}
}

// Un file senza EXIF non deve essere un errore: resta in archivio con i soli
// dati del filesystem. E' il caso di ogni PNG e di ogni scansione.
func TestReadExifSenzaExif(t *testing.T) {
	meta := readExifDaFile(t, "testdata/no_exif.png")

	if meta.CaptureTS != nil || meta.CameraMake != nil || meta.Orientation != nil {
		t.Errorf("un file senza EXIF non deve valorizzare nulla: %+v", meta)
	}
}

// L'invariante su cui si regge la lettura in una sola apertura: dopo l'EXIF il
// file deve essere riavvolto, perche' la stessa apertura serve subito dopo a
// image.Decode. Un offset lasciato in mezzo al file darebbe "image: unknown
// format" su ogni JPEG con EXIF, cioe' su quasi tutto l'archivio.
func TestReadExifRiavvolgeIlFile(t *testing.T) {
	path := "testdata/exif_sample.jpg"
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("apertura della fixture fallita: %v", err)
	}
	defer file.Close()

	readExif(&api.MediaMeta{}, file, path, quietLogger())

	if _, _, err := image.Decode(file); err != nil {
		t.Fatalf("decodifica dopo readExif fallita: %v", err)
	}
}
