package scanner

import (
	"encoding/json"
	"image"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"time"

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
			captured := parsed.UTC().Format(time.RFC3339)
			item.CaptureTS = &captured
		}
	}
}
