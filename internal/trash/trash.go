// Package trash sposta nel cestino i file che l'utente ha deciso di eliminare,
// e a scadenza li rimuove davvero.
//
// Sta nel pod scan perche' e' l'unico con la share montata in scrittura. L'API
// si limita a mettere in coda il lavoro: e' una garanzia strutturale che un bug
// nell'API non possa cancellare foto.
package trash

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
)

const privateDir = ".photovault"

type Trash struct {
	cfg    config.Config
	client *api.Client
	log    *slog.Logger
}

func New(cfg config.Config, client *api.Client, log *slog.Logger) *Trash {
	return &Trash{cfg: cfg, client: client, log: log}
}

func (t *Trash) thumbPath(mediaID int, size string) string {
	shard := fmt.Sprintf("%02x", mediaID%256)
	name := fmt.Sprintf("%d_%s.jpg", mediaID, size)
	return filepath.Join(t.cfg.MediaRoot, privateDir, "thumbs", shard, name)
}

// Apply sposta i file nel cestino.
//
// Non si usa mai os.Remove: una rename sulla stessa share e' atomica e
// istantanea, e rende ogni errore recuperabile con un mv. Lo svuotamento vero
// avviene solo dopo i giorni di ritenzione, col job trashpurge.
func (t *Trash) Apply(jobID int) (string, error) {
	moved, failed := 0, 0

	for {
		pending, err := t.client.GetPendingTrash(t.cfg.BatchSize)
		if err != nil {
			return "", fmt.Errorf("coda cestino: %w", err)
		}
		if len(pending) == 0 {
			break
		}

		progress := false
		for _, item := range pending {
			if err := t.moveOne(item); err != nil {
				t.log.Warn("spostamento fallito", "trash_id", item.TrashID, "err", err)
				if reportErr := t.client.CompleteTrash(item.TrashID, "error", err.Error()); reportErr != nil {
					return "", reportErr
				}
				failed++
			} else {
				if err := t.client.CompleteTrash(item.TrashID, "done", "spostato nel cestino"); err != nil {
					return "", err
				}
				moved++
			}
			// Ogni riga cambia stato, quindi la coda si accorcia comunque:
			// non c'e' rischio di ciclo infinito.
			progress = true
		}

		if err := t.client.Heartbeat(jobID); err != nil {
			return "", err
		}
		if !progress {
			break
		}
	}

	return fmt.Sprintf("%d spostati nel cestino, %d falliti", moved, failed), nil
}

func (t *Trash) moveOne(item api.TrashItem) error {
	src := filepath.Join(t.cfg.MediaRoot, item.RelPath, item.OriginalPath)
	dst := filepath.Join(t.cfg.MediaRoot, item.RelPath, item.TrashPath)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	if err := os.Rename(src, dst); err != nil {
		// Una cartella si sposta solo con la rename: il ripiego qui sotto copia
		// un file, e su una directory darebbe un errore fuorviante. Sulla
		// stessa share la rename non fallisce mai.
		if item.FolderID > 0 {
			return fmt.Errorf("rename della cartella: %w", err)
		}
		// La rename fallisce se sorgente e destinazione stanno su filesystem
		// diversi. Sulla stessa share non capita, ma in sviluppo si', quindi
		// si ripiega su copia piu' cancellazione.
		if copyErr := copyFile(src, dst); copyErr != nil {
			return fmt.Errorf("rename: %v; copia: %w", err, copyErr)
		}
		if rmErr := os.Remove(src); rmErr != nil {
			return fmt.Errorf("copia riuscita ma rimozione fallita: %w", rmErr)
		}
	}

	// La thumbnail di un singolo file NON si cancella qui: resta finche' il
	// file e' nel cestino, cosi' la pagina Cestino puo' mostrarla e si vede
	// cosa si sta per buttare. Sparisce col file, al trashpurge.
	//
	// Per una cartella invece si cancellano subito, e non e' un'incoerenza: gli
	// id dei media che conteneva esistono solo adesso, perche' l'API cancella
	// quelle righe insieme alla cartella. Al purge non ci sarebbe piu' modo di
	// sapere quali thumbnail togliere, e resterebbero orfane per sempre.
	t.removeThumbs(item.ThumbMediaIDs)
	return nil
}

func (t *Trash) removeThumbs(ids []int) {
	for _, id := range ids {
		for _, size := range []string{"s", "m"} {
			if err := os.Remove(t.thumbPath(id, size)); err != nil && !os.IsNotExist(err) {
				t.log.Warn("thumbnail non rimossa", "media_id", id, "size", size, "err", err)
			}
		}
	}
}

// Purge elimina definitivamente i file rimasti nel cestino oltre la ritenzione.
// E' l'unico punto di tutto photovault in cui un file viene davvero cancellato.
func (t *Trash) Purge(jobID int) (string, error) {
	removed, failed := 0, 0
	var freed int64

	for {
		expired, err := t.client.GetExpiredTrash(t.cfg.BatchSize)
		if err != nil {
			return "", fmt.Errorf("coda svuotamento: %w", err)
		}
		if len(expired) == 0 {
			break
		}

		for _, item := range expired {
			path := filepath.Join(t.cfg.MediaRoot, item.RelPath, item.TrashPath)
			// RemoveAll e non Remove: una riga di cartella punta a una
			// directory, che a questo punto e' scaduta con tutto il contenuto.
			// Su un file si comporta esattamente come Remove.
			err := os.RemoveAll(path)
			if err != nil && !os.IsNotExist(err) {
				t.log.Warn("eliminazione fallita", "trash_id", item.TrashID, "err", err)
				if reportErr := t.client.CompletePurge(item.TrashID, "error", err.Error()); reportErr != nil {
					return "", reportErr
				}
				failed++
				continue
			}
			// Un file gia' assente non e' un errore: l'obiettivo era che non
			// ci fosse, e ci si e' arrivati.
			if err := t.client.CompletePurge(item.TrashID, "purged", "eliminato definitivamente"); err != nil {
				return "", err
			}
			// Ora che il file non c'e' piu' se ne va anche l'anteprima, che
			// era rimasta apposta per far vedere nel cestino cosa c'era.
			if item.MediaID > 0 {
				t.removeThumbs([]int{item.MediaID})
			}
			removed++
			freed += item.FileSize
		}
		if err := t.client.Heartbeat(jobID); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%d eliminati (%.1f MB liberati), %d falliti",
		removed, float64(freed)/(1024*1024), failed), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
