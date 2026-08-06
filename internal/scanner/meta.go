package scanner

import (
	"encoding/json"
	"image"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	// Registrano i decoder: servono a image.DecodeConfig, che legge solo
	// l'intestazione e non decodifica i pixel.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/pste/photovault-scan/internal/api"
)

// enrich aggiunge al record i metadati ricavabili dal file.
// Non e' mai fatale: un file che non si riesce a interpretare entra comunque in
// archivio con i soli dati del filesystem, e la thumbnail dira' il resto.
func enrich(item *api.MediaItem, path string, log *slog.Logger) {
	switch item.MediaKind {
	case "image", "raw":
		// Prima l'EXIF: e' quello che porta data di scatto, fotocamera,
		// orientamento e GPS. Le dimensioni vengono dopo, come ripiego.
		readExif(item, path, log)
		if item.Width == nil {
			imageMeta(item, path, log)
		}
	case "video":
		videoMeta(item, path, log)
	}

	// Senza data di scatto si ripiega sull'mtime: e' sempre meglio di niente
	// per ordinare la griglia, ed e' il caso normale per i file senza EXIF.
	if item.CaptureTS == nil {
		modified := item.Modified
		item.CaptureTS = &modified
	}
}

// captureTS accetta una data solo se e' plausibile, e restituisce nil altrimenti.
//
// time.IsZero() NON basta: e' vero solo per l'esatto 0001-01-01. Un EXIF con la
// data azzerata -- "0000:00:00 00:00:00", frequentissimo nelle immagini passate
// da WhatsApp -- viene normalizzato ad anno 0, mese 0, giorno 0, cioe'
// -0001-11-30. Quella data supera IsZero(), arriva a PostgreSQL e fa fallire
// l'INSERT dell'intero batch con SQLSTATE 22007. Non e' un caso di scuola: e'
// successo al primo scan di un archivio vero, dopo 9000 file.
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
// contenere il byte zero, nemmeno in un varchar -- e rifiuta l'INSERT
// dell'INTERO batch con SQLSTATE 22021. E' successo al secondo scan
// dell'archivio vero, dopo 16.000 file.
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

func imageMeta(item *api.MediaItem, path string, log *slog.Logger) {
	file, err := os.Open(path)
	if err != nil {
		log.Warn("apertura fallita", "path", path, "err", err)
		return
	}
	defer file.Close()

	// DecodeConfig legge solo l'intestazione: e' il modo economico di sapere le
	// dimensioni senza pagare la decodifica completa dell'immagine.
	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		// HEIC, AVIF e i RAW non sono gestiti dalla libreria standard: le
		// dimensioni arriveranno dal job thumbs, che li decodifica davvero.
		log.Debug("dimensioni non leggibili", "path", path, "err", err)
		return
	}
	item.Width = &cfg.Width
	item.Height = &cfg.Height
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
// invocazione di ffprobe.
func videoMeta(item *api.MediaItem, path string, log *slog.Logger) {
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
		return
	}

	var probe probeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		log.Warn("output di ffprobe illeggibile", "path", path, "err", err)
		return
	}

	if seconds, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && seconds > 0 {
		item.DurationS = &seconds
	}

	for _, stream := range probe.Streams {
		if stream.CodecType == "video" && stream.Width > 0 {
			width, height := stream.Width, stream.Height
			item.Width = &width
			item.Height = &height
			break
		}
	}

	if raw, ok := probe.Format.Tags["creation_time"]; ok {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			if captured := captureTS(parsed); captured != nil {
				item.CaptureTS = captured
			}
		}
	}
}
