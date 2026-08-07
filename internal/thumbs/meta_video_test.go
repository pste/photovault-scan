package thumbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pste/photovault-scan/internal/api"
)

// Il caso vero: WhatsApp salva le note vocali in .3gp, che e' un contenitore
// video e sta nell'allowlist perche' i telefoni vecchi ci giravano i filmati.
// Dal nome non si distinguono, e su un archivio vero erano 48 file che
// fallivano l'anteprima a ogni giro senza poter mai riuscire.
func TestVideoMetaRiconosceUnAudioSenzaTracciaVideo(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "AUD-20130823-WA0000.3gp")
	run(t, "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono",
		"-t", "2", "-c:a", "aac", "-y", path)

	var meta api.MediaMeta
	if videoMeta(&meta, path, quietLogger()) {
		t.Fatal("un file di solo audio non deve risultare un filmato")
	}
	// La durata si legge lo stesso: serve a chi guardera' la riga fra i file
	// non gestiti.
	if meta.DurationS == nil || *meta.DurationS <= 0 {
		t.Errorf("durata non letta: %v", meta.DurationS)
	}
	if meta.Width != nil {
		t.Errorf("un audio non ha larghezza, invece: %v", *meta.Width)
	}
}

func TestVideoMetaRiconosceUnFilmato(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "VID.mp4")
	generateClip(t, path, "2")

	var meta api.MediaMeta
	if !videoMeta(&meta, path, quietLogger()) {
		t.Fatal("un filmato deve risultare tale")
	}
	if meta.Width == nil || *meta.Width != 320 {
		t.Errorf("larghezza non letta: %v", meta.Width)
	}
}

// In dubbio si risponde "sì": un file illeggibile non e' la prova che la
// traccia video manchi, e su un dubbio non si sposta un file fuori dalla
// libreria -- il danno di un falso positivo qui e' una foto che sparisce
// dalla griglia.
func TestVideoMetaInDubbioNonDeclassa(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "rotto.mp4")
	if err := os.WriteFile(path, []byte("non sono un video"), 0o644); err != nil {
		t.Fatal(err)
	}

	var meta api.MediaMeta
	if !videoMeta(&meta, path, quietLogger()) {
		t.Fatal("un file illeggibile non deve essere declassato")
	}
}

func run(t *testing.T, args ...string) {
	t.Helper()
	full := append([]string{"-nostdin", "-loglevel", "error"}, args...)
	if out, err := exec.Command("ffmpeg", full...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
	}
}
