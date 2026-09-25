// Package trash sposta nel cestino i file che l'utente ha deciso di eliminare,
// e a scadenza li rimuove davvero.
//
// Sta nel pod scan perche' e' l'unico con la share montata in scrittura. L'API
// si limita a mettere in coda il lavoro: e' una garanzia strutturale che un bug
// nell'API non possa cancellare foto.
package trash

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
	"github.com/pste/photovault-scan/internal/layout"
)

type Trash struct {
	cfg    config.Config
	client *api.Client
	log    *slog.Logger
}

func New(cfg config.Config, client *api.Client, log *slog.Logger) *Trash {
	return &Trash{cfg: cfg, client: client, log: log}
}

func (t *Trash) thumbPath(mediaID int, size string) string {
	return layout.ThumbPath(t.cfg.MediaRoot, mediaID, size)
}

// within dice se path sta strettamente dentro base: non base stessa, non fuori.
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// paths ricava sorgente e destinazione di una riga di cestino, e rifiuta tutto
// quello che non ha la forma attesa.
//
// I percorsi arrivano dall'API, e questo e' l'unico pod che puo' spostare e
// cancellare: la garanzia scritta in cima al package vale solo se un dato
// sbagliato non puo' trasformarsi in una RemoveAll fuori posto. Un trash_path
// vuoto, o fatto di "..", o un rel_path che esce da MEDIA_ROOT, farebbero
// cancellare al purge una root intera o qualcosa fuori dalla share.
//
// La destinazione deve stare almeno due livelli sotto il cestino
// (.photovault/trash/<data>/<nome>): un livello solo sarebbe la cartella di un
// giorno intero.
func (t *Trash) paths(item api.TrashItem) (src, dst string, err error) {
	root := filepath.Join(t.cfg.MediaRoot, item.RelPath)
	if root != filepath.Clean(t.cfg.MediaRoot) && !within(t.cfg.MediaRoot, root) {
		return "", "", fmt.Errorf("rel_path fuori da MEDIA_ROOT: %q", item.RelPath)
	}

	bin := filepath.Join(root, layout.PrivateDir, "trash")
	dst = filepath.Join(root, item.TrashPath)
	rel, relErr := filepath.Rel(bin, dst)
	if !within(bin, dst) || relErr != nil || !strings.Contains(rel, string(filepath.Separator)) {
		return "", "", fmt.Errorf("trash_path fuori dal cestino: %q", item.TrashPath)
	}

	src = filepath.Join(root, item.OriginalPath)
	if !within(root, src) || within(filepath.Join(root, layout.PrivateDir), src) {
		return "", "", fmt.Errorf("original_path non valido: %q", item.OriginalPath)
	}
	return src, dst, nil
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
	src, dst, err := t.paths(item)
	if err != nil {
		return err
	}

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
		// si ripiega su copia piu' cancellazione -- solo in quel caso: per
		// qualsiasi altro errore copiare non risolverebbe niente.
		if !errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("rename: %w", err)
		}
		// Ogni fallimento a meta' toglie la copia. Una copia parziale
		// resterebbe nel cestino senza che nessuna riga la conosca, e una
		// copia completa col file di partenza ancora al suo posto finirebbe
		// in una riga 'error', che il purge non guarda: spazio occupato per
		// sempre.
		if copyErr := copyFile(src, dst); copyErr != nil {
			os.Remove(dst)
			return fmt.Errorf("rename: %v; copia: %w", err, copyErr)
		}
		if rmErr := os.Remove(src); rmErr != nil {
			os.Remove(dst)
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
			// RemoveAll e non Remove: una riga di cartella punta a una
			// directory, che a questo punto e' scaduta con tutto il contenuto.
			// Su un file si comporta esattamente come Remove.
			_, path, err := t.paths(item)
			if err == nil {
				err = os.RemoveAll(path)
			}
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

	// Close si controlla: su una share di rete e' spesso li' che arriva
	// l'errore di una scrittura non andata a buon fine.
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
