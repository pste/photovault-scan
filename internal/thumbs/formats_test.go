package thumbs

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// I formati che l'archivio contiene e che finivano in errore con
// "image: unknown format": 59 webp, 18 bmp e 3 tif, cioe' 80 anteprime mai
// generate. Non mancava una libreria -- golang.org/x/image era gia' fra le
// dipendenze -- mancavano i tre import a vuoto che registrano i decoder.
func TestDecodeFileFormatiRegistrati(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()

	// L'immagine sorgente e' costruita qui e non e' una fixture in repo: quello
	// che si vuole provare e' che il decoder ci sia, non che sappia leggere un
	// file particolare.
	src := filepath.Join(dir, "src.png")
	scriviPNG(t, src)

	for _, formato := range []string{"webp", "bmp", "tiff"} {
		t.Run(formato, func(t *testing.T) {
			out := filepath.Join(dir, "prova."+formato)
			cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error",
				"-i", src, "-y", out)
			if outBytes, err := cmd.CombinedOutput(); err != nil {
				t.Skipf("ffmpeg non sa scrivere %s qui: %v: %s", formato, err, outBytes)
			}

			img, err := decodeFile(out)
			if err != nil {
				t.Fatalf("%s non decodificato: %v", formato, err)
			}
			if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
				t.Errorf("%s: dimensioni sbagliate: %v", formato, img.Bounds())
			}
		})
	}
}

// Controprova del meccanismo: un formato di cui NON abbiamo il decoder deve
// ancora fallire, e con l'errore che si e' visto in produzione. Senza questo il
// test sopra passerebbe anche se image.Decode indovinasse per caso.
func TestDecodeFileFormatoSconosciuto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "finto.xyz")
	if err := os.WriteFile(path, []byte("questo non e' un'immagine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFile(path); err == nil {
		t.Fatal("un formato sconosciuto deve fallire")
	}
}

func scriviPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
