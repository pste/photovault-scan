package thumbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Il caso che il 2026-08-07 ha lasciato 586 filmati senza anteprima: ffmpeg
// esce con codice 0 e non scrive nessun fotogramma, perche' il seek a 3 secondi
// e' finito oltre la fine del video.
//
// L'ffmpeg vero non serve, e anzi qui sarebbe dannoso: la 6.1.2 dell'immagine
// esce 0, la 8.1.2 di alpine esce 234, quindi un test su ffmpeg reale
// passerebbe senza la correzione a seconda di dove lo si esegue -- verificato.
// Un finto ffmpeg riproduce la condizione di produzione ovunque.
func TestDecodeVideoSenzaFotogrammaMaSenzaErrore(t *testing.T) {
	fakeFFmpeg(t, "exit 0")

	path := filepath.Join(t.TempDir(), "corto.mp4")
	if err := os.WriteFile(path, []byte("finto"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (&Thumbnailer{}).decodeVideo(path)
	if err == nil {
		t.Fatal("un fotogramma mai scritto deve dare errore, non un'immagine")
	}
	// Il difetto non era solo il fallimento: era che arrivava travestito da
	// "image: unknown format", che manda a cercare nel posto sbagliato.
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("l'errore deve nominare ffmpeg, invece: %v", err)
	}
}

// Con ffmpeg vero: un video piu' corto del seek deve comunque dare un'anteprima,
// passando dal fallback sul primo fotogramma.
func TestDecodeVideoClipPiuCortoDelSeek(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "corto.mp4")
	generateClip(t, path, "2")

	img, err := (&Thumbnailer{}).decodeVideo(path)
	if err != nil {
		t.Fatalf("nessun fotogramma da un video da 2 secondi: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Fatalf("fotogramma di dimensione nulla: %v", img.Bounds())
	}
}

// Il caso normale deve continuare a passare dal seek, senza fallback.
func TestDecodeVideoClipPiuLungoDelSeek(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "lungo.mp4")
	generateClip(t, path, "5")

	img, err := (&Thumbnailer{}).decodeVideo(path)
	if err != nil {
		t.Fatalf("nessun fotogramma da un video da 5 secondi: %v", err)
	}
	if img.Bounds().Dx() == 0 {
		t.Fatalf("fotogramma di dimensione nulla: %v", img.Bounds())
	}
}

// Un file che non e' un video deve dare un errore che nomina ffmpeg, non un
// generico "unknown format" che manda a cercare nel posto sbagliato.
func TestDecodeVideoFileNonVideo(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "finto.mp4")
	if err := os.WriteFile(path, []byte("non sono un video"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := (&Thumbnailer{}).decodeVideo(path); err == nil {
		t.Fatal("un file non video dovrebbe fallire")
	}
}

func TestHasContent(t *testing.T) {
	dir := t.TempDir()

	vuoto := filepath.Join(dir, "vuoto.jpg")
	if err := os.WriteFile(vuoto, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pieno := filepath.Join(dir, "pieno.jpg")
	if err := os.WriteFile(pieno, []byte{0xff, 0xd8}, 0o644); err != nil {
		t.Fatal(err)
	}

	casi := map[string]bool{
		vuoto:                         false,
		pieno:                         true,
		filepath.Join(dir, "assente"): false,
	}
	for path, atteso := range casi {
		if got := hasContent(path); got != atteso {
			t.Errorf("hasContent(%s) = %v, atteso %v", filepath.Base(path), got, atteso)
		}
	}
}

// fakeFFmpeg mette in testa al PATH un ffmpeg che fa quello che gli si dice.
func fakeFFmpeg(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg non disponibile")
	}
}

// Un video sintetico della durata voluta: non serve una fixture in repo, e la
// durata e' l'unica cosa che questi test guardano.
func generateClip(t *testing.T, path, seconds string) {
	t.Helper()
	cmd := exec.Command("ffmpeg",
		"-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10",
		"-t", seconds, "-pix_fmt", "yuv420p", "-y", path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generazione del video di prova fallita: %v: %s", err, out)
	}
}
