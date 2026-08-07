// Package thumbs genera le due dimensioni di anteprima per i media che il
// database segnala come pendenti.
//
// Sta nel pod scan e non in un servizio a se' per due ragioni: e' l'unico pod
// con la share montata in scrittura, ed e' l'unico che ha gia' il necessario
// per decodificare immagini e video.
package thumbs

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// Import a vuoto: registrano il decoder in image.Decode, che sceglie da se'
	// in base ai primi byte del file. Non c'e' altro da scrivere.
	//
	// Sono tre formati che l'archivio contiene davvero e che finivano in errore
	// con "image: unknown format": 59 webp, 18 bmp e 3 tif. La dipendenza
	// golang.org/x/image c'era gia' -- serviva al ridimensionamento -- quindi
	// non si aggiunge niente all'immagine.
	//
	// Che siano solo decoder non e' un limite: le thumbnail si scrivono sempre
	// in JPEG, quindi di un encoder webp o tiff non sapremmo che farcene.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
)

const privateDir = ".photovault"

// statusNotMedia non e' uno stato che finisce in database: e' il modo in cui
// process() dice a Run() che questa riga non va aggiornata ma spostata.
const statusNotMedia = "not-media"

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
func (t *Thumbnailer) Run(jobID int) (string, error) {
	done, failed, skipped, moved := 0, 0, 0, 0

	for {
		pending, err := t.client.GetPending("thumb", t.cfg.BatchSize)
		if err != nil {
			return "", fmt.Errorf("lettura della coda: %w", err)
		}
		if len(pending) == 0 {
			break
		}

		results := make([]api.ThumbResult, 0, len(pending))
		notMedia := make([]int, 0)
		for _, item := range pending {
			result := t.process(item)
			// Chi va riclassificato resta fuori dai risultati: la sua riga in
			// media sta per sparire, e aggiornarla prima di cancellarla
			// sarebbe lavoro buttato.
			if result.ThumbStatus == statusNotMedia {
				notMedia = append(notMedia, item.MediaID)
				moved++
				continue
			}
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

		if len(results) > 0 {
			if err := t.client.SendThumbs(results); err != nil {
				return "", fmt.Errorf("invio dei risultati: %w", err)
			}
		}
		// Un errore qui e' fatale e deve esserlo: se la riclassificazione non
		// passa, quelle righe restano 'pending' e il giro dopo la coda le
		// ripropone identiche -- cioe' un ciclo infinito.
		if len(notMedia) > 0 {
			if err := t.client.SendNotMedia(notMedia); err != nil {
				return "", fmt.Errorf("riclassificazione: %w", err)
			}
		}
		// Un 409 qui dice che il job non e' piu' nostro: si smette subito.
		// Continuare vorrebbe dire rifare il lavoro di un altro pod.
		if err := t.client.Heartbeat(jobID); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%d generate, %d non supportate, %d fallite, %d spostate fra i file non gestiti",
		done, skipped, failed, moved), nil
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

// errNotMedia dice che il file non e' quello che l'estensione prometteva, e che
// quindi non va riprovato: va tolto dalla libreria e messo fra i file non
// gestiti. Ritentarlo all'infinito sarebbe l'unica alternativa, e non porta a
// niente perche' la traccia video non comparira' domani.
var errNotMedia = errors.New("nessuna traccia video: non e' un filmato")

func (t *Thumbnailer) process(item api.PendingMedia) api.ThumbResult {
	result := api.ThumbResult{MediaID: item.MediaID, ThumbStatus: "error"}

	// I metadati escono dalla stessa apertura della decodifica, e vengono
	// restituiti anche quando la thumbnail fallisce: un RAW resta senza
	// anteprima, ma la sua data di scatto e le sue coordinate sono valide.
	src, err := t.decode(item, &result.MediaMeta)
	if err != nil {
		if errors.Is(err, errNotMedia) {
			result.ThumbStatus = statusNotMedia
			t.log.Info("non e' un filmato, passa fra i file non gestiti",
				"media_id", item.MediaID, "file", item.FileName)
		} else if strings.Contains(err.Error(), "non supportato") {
			result.ThumbStatus = "unsupported"
			t.log.Debug("formato non supportato", "media_id", item.MediaID, "file", item.FileName)
		} else {
			t.log.Warn("decodifica fallita", "media_id", item.MediaID, "file", item.FileName, "err", err)
		}
		return result
	}

	// L'orientamento EXIF va applicato PRIMA di ridimensionare: dimenticarlo
	// significa meta' delle foto verticali storte nella griglia, che e' il bug
	// piu' visibile che ci sia. Arriva dal file appena letto e non dal
	// database, quindi questo job non dipende da cosa sapeva lo scan.
	src = applyOrientation(src, result.Orientation)

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

// decode riempie i metadati e restituisce l'immagine da ridurre.
//
// Le due cose stanno insieme perche' vengono dalla stessa lettura del file: nel
// caso normale -- JPEG e PNG, cioe' quasi tutto l'archivio -- una sola
// os.Open serve sia all'EXIF sia alla decodifica. Su CIFS una apertura in meno
// per file, su 146.000 file, e' la ragione di questa forma.
func (t *Thumbnailer) decode(item api.PendingMedia, meta *api.MediaMeta) (image.Image, error) {
	path := t.sourcePath(item)

	// I video non hanno EXIF: durata, dimensioni e data vengono da ffprobe, e
	// il fotogramma da ffmpeg. Nessuna delle due passa da un file aperto qui.
	if item.MediaKind == "video" {
		if !videoMeta(meta, path, t.log) {
			return nil, errNotMedia
		}
		return t.decodeVideo(path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// L'EXIF si legge sempre, anche per i formati di cui non sappiamo fare
	// l'anteprima: imagemeta legge HEIC e RAW, e per un RAW la data di scatto
	// e le coordinate sono meta' del valore del file.
	readExif(meta, file, path, t.log)

	switch {
	case item.Ext == "heic" || item.Ext == "heif" || item.Ext == "avif":
		return t.decodeHeif(path)
	case item.MediaKind == "raw":
		// I RAW arrivano in fase successiva: dcraw_emu -e estrae l'anteprima
		// gia' presente nel file senza demosaicizzare.
		return nil, fmt.Errorf("formato non supportato: raw")
	default:
		img, _, err := image.Decode(file)
		return img, err
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
