package thumbs

import (
	"encoding/json"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pste/photovault-scan/internal/api"
)

// captureTS accetta una data solo se e' plausibile, e restituisce nil altrimenti.
//
// time.IsZero() NON basta: e' vero solo per l'esatto 0001-01-01. Un EXIF con la
// data azzerata -- "0000:00:00 00:00:00", frequentissimo nelle immagini passate
// da WhatsApp -- viene normalizzato ad anno 0, mese 0, giorno 0, cioe'
// -0001-11-30. Quella data supera IsZero(), arriva a PostgreSQL e fa fallire
// l'UPDATE con SQLSTATE 22007. Non e' un caso di scuola: e' successo al primo
// scan di un archivio vero, dopo 9000 file.
//
// Il limite superiore serve contro il problema opposto: una fotocamera con
// l'orologio sballato in avanti resterebbe in cima alla griglia per sempre.
func captureTS(when time.Time) *string {
	if when.Year() < 1900 || when.After(time.Now().AddDate(1, 0, 0)) {
		return nil
	}
	formatted := when.UTC().Format(time.RFC3339)
	return &formatted
}

// cleanText rende una stringa EXIF memorizzabile, e restituisce "" se non
// resta niente di utile.
//
// I campi ASCII dell'EXIF sono terminati da NUL e alcune fotocamere ci
// scrivono dentro anche il padding: la stringa che arriva dal decoder puo'
// quindi contenere 0x00. PostgreSQL non sa memorizzarlo -- nessun testo puo'
// contenere il byte zero, nemmeno in un varchar -- e rifiuta la scrittura con
// SQLSTATE 22021. E' successo al secondo scan dell'archivio vero, dopo 16.000
// file.
//
// Stesso trattamento per le sequenze UTF-8 non valide, che danno lo stesso
// errore: l'EXIF non dichiara una codifica, e una fotocamera che scrive il
// nome del modello in Latin-1 e' del tutto legittima.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// Struttura minima dell'output di ffprobe: si leggono solo i campi utili.
type probeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType string            `json:"codec_type"`
		Width     int               `json:"width"`
		Height    int               `json:"height"`
		Tags      map[string]string `json:"tags"`
	} `json:"streams"`
}

// videoMeta ricava durata, dimensioni e data di creazione con una sola
// invocazione di ffprobe. Non e' mai fatale: un video illeggibile resta in
// archivio con i soli dati del filesystem.
//
// Restituisce se il file contiene davvero una traccia video. Non e' una
// domanda oziosa: WhatsApp salva le note vocali in .3gp, che e' un contenitore
// video a tutti gli effetti e sta nell'allowlist perche' i telefoni vecchi ci
// giravano i filmati veri. Dal nome non si distinguono; da ffprobe si'.
//
// In dubbio si risponde "sì": un ffprobe fallito o illeggibile non e' una
// prova che la traccia video non ci sia, e sulla base di un dubbio non si
// sposta un file fuori dalla libreria.
func videoMeta(meta *api.MediaMeta, path string, log *slog.Logger) bool {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		log.Warn("ffprobe fallito", "path", path, "err", err)
		return true
	}

	var probe probeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		log.Warn("output di ffprobe illeggibile", "path", path, "err", err)
		return true
	}

	if seconds, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && seconds > 0 {
		meta.DurationS = &seconds
	}

	hasVideo := false
	for _, stream := range probe.Streams {
		if stream.CodecType != "video" {
			continue
		}
		hasVideo = true
		if stream.Width > 0 {
			width, height := stream.Width, stream.Height
			meta.Width = &width
			meta.Height = &height
			break
		}
	}

	if raw, ok := probe.Format.Tags["creation_time"]; ok {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			if captured := captureTS(parsed); captured != nil {
				meta.CaptureTS = captured
			}
		}
	}

	return hasVideo
}
