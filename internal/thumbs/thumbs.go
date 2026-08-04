// Package thumbs genera le due dimensioni di anteprima per i media che il
// database segnala come pendenti.
//
// Sta nel pod scan e non in un servizio a se' per due ragioni: e' l'unico pod
// con la share montata in scrittura, ed e' l'unico che ha gia' il necessario
// per decodificare immagini e video.
package thumbs

import (
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
)

const privateDir = ".photovault"

type Thumbnailer struct {
	cfg    config.Config
	client *api.Client
	log    *slog.Logger
}

func New(cfg config.Config, client *api.Client, log *slog.Logger) *Thumbnailer {
	return &Thumbnailer{cfg: cfg, client: client, log: log}
}

// Run svuota la coda delle thumbnail: chiede all'API cosa manca, genera, riferisce.
// La coda e' la colonna thumb_status, quindi il lavoro riprende da solo dopo un
// crash senza bisogno di alcun checkpoint.
func (t *Thumbnailer) Run() (string, error) {
	done, failed, skipped := 0, 0, 0

	for {
		pending, err := t.client.GetPending("thumb", t.cfg.BatchSize)
		if err != nil {
			return "", fmt.Errorf("lettura della coda: %w", err)
		}
		if len(pending) == 0 {
			break
		}

		results := make([]api.ThumbResult, 0, len(pending))
		for _, item := range pending {
			result := t.process(item)
			results = append(results, result)
			switch result.ThumbStatus {
			case "done":
				done++
			case "unsupported":
				skipped++
			default:
				failed++
			}
		}

		if err := t.client.SendThumbs(results); err != nil {
			return "", fmt.Errorf("invio dei risultati: %w", err)
		}
	}

	return fmt.Sprintf("%d generate, %d non supportate, %d fallite", done, skipped, failed), nil
}

// shard distribuisce le thumbnail su 256 sottocartelle: CIFS degrada male oltre
// qualche migliaio di file per directory, e due livelli di shard costerebbero
// 65.000 mkdir per nulla.
func shard(mediaID int) string {
	return fmt.Sprintf("%02x", mediaID%256)
}

func (t *Thumbnailer) thumbPath(mediaID int, size string) string {
	name := fmt.Sprintf("%d_%s.jpg", mediaID, size)
	return filepath.Join(t.cfg.MediaRoot, privateDir, "thumbs", shard(mediaID), name)
}

func (t *Thumbnailer) sourcePath(item api.PendingMedia) string {
	return filepath.Join(t.cfg.MediaRoot, item.RelPath, item.FolderPath, item.FileName)
}

func (t *Thumbnailer) process(item api.PendingMedia) api.ThumbResult {
	result := api.ThumbResult{MediaID: item.MediaID, ThumbStatus: "error"}

	src, err := t.decode(item)
	if err != nil {
		if strings.Contains(err.Error(), "non supportato") {
			result.ThumbStatus = "unsupported"
			t.log.Debug("formato non supportato", "media_id", item.MediaID, "file", item.FileName)
		} else {
			t.log.Warn("decodifica fallita", "media_id", item.MediaID, "file", item.FileName, "err", err)
		}
		return result
	}

	// L'orientamento EXIF va applicato PRIMA di ridimensionare: dimenticarlo
	// significa meta' delle foto verticali storte nella griglia, che e' il bug
	// piu' visibile che ci sia.
	src = applyOrientation(src, item.Orientation)

	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Una sola decodifica produce entrambe le dimensioni.
	if err := t.write(src, t.thumbPath(item.MediaID, "m"), t.cfg.ThumbMedium); err != nil {
		t.log.Warn("scrittura thumbnail m fallita", "media_id", item.MediaID, "err", err)
		return result
	}
	if err := t.write(src, t.thumbPath(item.MediaID, "s"), t.cfg.ThumbSmall); err != nil {
		t.log.Warn("scrittura thumbnail s fallita", "media_id", item.MediaID, "err", err)
		return result
	}

	result.ThumbStatus = "done"
	result.Width = &width
	result.Height = &height
	return result
}

func (t *Thumbnailer) decode(item api.PendingMedia) (image.Image, error) {
	path := t.sourcePath(item)

	switch {
	case item.MediaKind == "video":
		return t.decodeVideo(path)
	case item.Ext == "heic" || item.Ext == "heif" || item.Ext == "avif":
		return t.decodeHeif(path)
	case item.MediaKind == "raw":
		// I RAW arrivano in fase successiva: dcraw_emu -e estrae l'anteprima
		// gia' presente nel file senza demosaicizzare.
		return nil, fmt.Errorf("formato non supportato: raw")
	default:
		return decodeFile(path)
	}
}

func decodeFile(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}
	return img, nil
}
