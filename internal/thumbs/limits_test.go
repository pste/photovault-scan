package thumbs

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
)

// Un PNG che nell'intestazione dichiara 30.000 x 30.000 pixel: decodificarlo
// davvero chiederebbe gigabyte, e il pod verrebbe ucciso per memoria. Il file
// vero pesa un centinaio di byte, perche' a decidere basta l'intestazione.
func TestDecodeRifiutaImmaginiEnormi(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// IHDR: 8 byte di firma, 4 di lunghezza, 4 di tipo, poi larghezza e
	// altezza. Il CRC copre tipo e dati del chunk.
	binary.BigEndian.PutUint32(data[16:], 30000)
	binary.BigEndian.PutUint32(data[20:], 30000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))

	path := filepath.Join(t.TempDir(), "enorme.png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := decodeFile(path)
	if err == nil || !strings.Contains(err.Error(), "troppo grande") {
		t.Fatalf("atteso il rifiuto per dimensione, invece: %v", err)
	}
}

// Un decoder che va in panic -- su un file corrotto capita -- non deve
// abbattere il pod: il file finisce in errore e il giro continua.
func TestProcessSopravviveAlPanic(t *testing.T) {
	image.RegisterFormat("panico", "PVPANIC",
		func(io.Reader) (image.Image, error) { panic("decoder rotto") },
		func(io.Reader) (image.Config, error) { panic("decoder rotto") })

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rotto.jpg"), []byte("PVPANIC e poi niente"), 0o644); err != nil {
		t.Fatal(err)
	}

	th := &Thumbnailer{
		cfg: config.Config{MediaRoot: root, ThumbSmall: 32, ThumbMedium: 64},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	result := th.process(api.PendingMedia{MediaID: 7, FileName: "rotto.jpg", MediaKind: "image", Ext: "jpg"})
	if result.ThumbStatus != "error" || result.MediaID != 7 {
		t.Fatalf("atteso un errore sul media 7, invece: %+v", result)
	}
}
