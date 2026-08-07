package thumbs

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
)

// decodeVideo estrae un fotogramma con ffmpeg.
//
// -ss PRIMA di -i e' un seek in input: ffmpeg salta direttamente al punto
// richiesto invece di decodificare tutto quello che viene prima. Metterlo dopo
// -i su un video lungo costa ordini di grandezza in piu'.
func (t *Thumbnailer) decodeVideo(path string) (image.Image, error) {
	tmp, err := os.CreateTemp("", "photovault-frame-*.jpg")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("ffmpeg",
		"-nostdin",
		"-loglevel", "error",
		"-ss", "3",
		"-i", path,
		"-frames:v", "1",
		"-q:v", "3",
		"-y", tmpPath,
	)
	out, err := cmd.CombinedOutput()

	// Sotto i 3 secondi il seek finisce oltre la fine del video e non esce
	// nessun fotogramma. Non basta guardare il codice di uscita: in questo caso
	// ffmpeg esce **0** e si limita a non scrivere niente -- verificato su un
	// .MOV da 2,6 secondi. Fidandosi solo dell'errore si finiva a decodificare
	// il file temporaneo vuoto, e il fallimento arrivava travestito da
	// "image: unknown format", che di ffmpeg non parla nemmeno. Erano 586 video
	// senza anteprima sull'archivio vero.
	if err != nil || !hasContent(tmpPath) {
		if fallbackErr := t.videoFirstFrame(path, tmpPath); fallbackErr != nil {
			return nil, fmt.Errorf("ffmpeg: %v: %s", fallbackErr, string(out))
		}
		if !hasContent(tmpPath) {
			return nil, fmt.Errorf("ffmpeg: nessun fotogramma estratto: %s", string(out))
		}
	}

	return decodeFile(tmpPath)
}

// hasContent dice se il file esiste e non e' vuoto. Un file assente e uno da
// zero byte sono la stessa cosa per chi deve decodificarlo, e qui capitano
// entrambi: os.CreateTemp lascia un file vuoto, ffmpeg puo' non crearlo affatto.
func hasContent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func (t *Thumbnailer) videoFirstFrame(path, outPath string) error {
	cmd := exec.Command("ffmpeg",
		"-nostdin",
		"-loglevel", "error",
		"-i", path,
		"-frames:v", "1",
		"-q:v", "3",
		"-y", outPath,
	)
	return cmd.Run()
}

// decodeHeif passa da heif-convert (pacchetto libheif-tools) invece che da
// ffmpeg: il supporto HEIC di ffmpeg e' inaffidabile, mentre libheif e'
// l'implementazione di riferimento e costa un solo pacchetto apk.
func (t *Thumbnailer) decodeHeif(path string) (image.Image, error) {
	dir, err := os.MkdirTemp("", "photovault-heif-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	outPath := filepath.Join(dir, "out.jpg")
	cmd := exec.Command("heif-convert", "-q", "90", path, outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("heif-convert: %v: %s", err, string(out))
	}

	return decodeFile(outPath)
}
