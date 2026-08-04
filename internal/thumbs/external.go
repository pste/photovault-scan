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
	if out, err := cmd.CombinedOutput(); err != nil {
		// Sotto i 3 secondi il seek finisce oltre la fine del video: si
		// ritenta dal primo fotogramma.
		if fallbackErr := t.videoFirstFrame(path, tmpPath); fallbackErr != nil {
			return nil, fmt.Errorf("ffmpeg: %v: %s", err, string(out))
		}
	}

	return decodeFile(tmpPath)
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
