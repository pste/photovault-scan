// Package scanner costruisce l'alberatura in database: cartelle e file con i
// metadati di base. Non calcola hash e non genera thumbnail -- se ne occupano
// rispettivamente photovault-dedup e il job "thumbs" -- perche' deve restare
// veloce: e' l'unico pezzo che cammina l'intera share a ogni giro.
package scanner

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
)

type Scanner struct {
	cfg    config.Config
	client *api.Client
	log    *slog.Logger
}

func New(cfg config.Config, client *api.Client, log *slog.Logger) *Scanner {
	return &Scanner{cfg: cfg, client: client, log: log}
}

// Run cammina tutte le root abilitate.
//
// Si mandano all'API TUTTI i file incontrati, non solo quelli cambiati: e'
// l'ON CONFLICT lato API a distinguere i due casi. Cosi' questo pod non deve
// interrogare il database per fare change detection, e il flusso resta
// "cammina, leggi, spedisci".
func (s *Scanner) Run(jobID int) (string, error) {
	startedAt := time.Now().UTC()

	roots, err := s.client.GetRoots()
	if err != nil {
		return "", fmt.Errorf("lettura delle root: %w", err)
	}

	// Primo avvio: nessuna root configurata, se ne crea una dall'ambiente.
	if len(roots) == 0 {
		root, err := s.client.CreateRoot(s.cfg.RootName, s.cfg.RootRelPath)
		if err != nil {
			return "", fmt.Errorf("creazione della root: %w", err)
		}
		s.log.Info("root creata", "name", root.Name, "rel_path", root.RelPath)
		roots = []api.Root{*root}
	}

	summary := make([]string, 0, len(roots))
	for _, root := range roots {
		count, err := s.scanRoot(root, startedAt, jobID)
		if err != nil {
			return "", err
		}
		summary = append(summary, fmt.Sprintf("%s: %d file", root.Name, count))
	}
	return strings.Join(summary, "; "), nil
}

func (s *Scanner) scanRoot(root api.Root, startedAt time.Time, jobID int) (int, error) {
	base := filepath.Join(s.cfg.MediaRoot, root.RelPath)
	s.log.Info("scansione avviata", "root", root.Name, "path", base)

	if _, err := os.Stat(base); err != nil {
		return 0, fmt.Errorf("root %q non raggiungibile: %w", root.Name, err)
	}

	// Gli id delle cartelle si memorizzano man mano: una cartella con 500 foto
	// non deve generare 500 chiamate di registrazione.
	folderIDs := make(map[string]int)
	batch := make([]api.MediaItem, 0, s.cfg.BatchSize)
	others := make([]api.OtherFile, 0, s.cfg.BatchSize)
	total := 0
	totalOthers := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.client.SendMedia(batch); err != nil {
			return err
		}
		total += len(batch)
		batch = batch[:0]
		return s.client.Heartbeat(jobID)
	}

	// I file non gestiti hanno una coda a parte: non entrano in media, quindi
	// non hanno una pipeline, e non devono rallentare quella dei media.
	flushOthers := func() error {
		if len(others) == 0 {
			return nil
		}
		if err := s.client.SendOthers(others); err != nil {
			return err
		}
		totalOthers += len(others)
		others = others[:0]
		return s.client.Heartbeat(jobID)
	}

	// WalkDir e non Walk: WalkDir usa il tipo restituito da readdir ed evita una
	// Lstat per voce. Su CIFS e' la differenza tra una passata di 30 secondi e
	// una di 5 minuti su decine di migliaia di file.
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Un permesso negato su una sottocartella non deve far fallire
			// l'intera scansione: si annota e si prosegue.
			s.log.Warn("voce non leggibile", "path", path, "err", err)
			return nil
		}

		if d.IsDir() {
			if path != base && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}

		ext := strings.TrimPrefix(filepath.Ext(d.Name()), ".")
		kind, ok := kindOf(ext)

		info, err := d.Info()
		if err != nil {
			s.log.Warn("info non leggibili", "path", path, "err", err)
			return nil
		}

		relDir, err := filepath.Rel(base, filepath.Dir(path))
		if err != nil {
			return nil
		}
		folderPath := toFolderPath(relDir)

		// Quello che l'allowlist non riconosce non e' un media, ma esiste: si
		// registra a parte, con il percorso come testo. Non si chiede un
		// folder_id perche' una cartella di soli file non gestiti non deve
		// comparire nell'albero di Sfoglia come cartella vuota.
		if !ok {
			others = append(others, api.OtherFile{
				RootID:   root.RootID,
				Path:     folderPath,
				FileName: d.Name(),
				Ext:      strings.ToLower(ext),
				FileSize: info.Size(),
				Modified: info.ModTime().UTC().Format(time.RFC3339),
			})
			if len(others) >= s.cfg.BatchSize {
				return flushOthers()
			}
			return nil
		}

		folderID, ok := folderIDs[folderPath]
		if !ok {
			folder, err := s.client.RegisterFolder(root.RootID, folderPath)
			if err != nil {
				return fmt.Errorf("registrazione cartella %q: %w", folderPath, err)
			}
			folderID = folder.FolderID
			folderIDs[folderPath] = folderID
		}

		modified := info.ModTime().UTC().Format(time.RFC3339)
		item := api.MediaItem{
			FolderID:  folderID,
			FileName:  d.Name(),
			MediaKind: kind,
			Ext:       strings.ToLower(ext),
			FileSize:  info.Size(),
			Modified:  modified,
		}

		// Data di scatto provvisoria: l'mtime. Quella vera la legge il job
		// thumbs dall'EXIF, nella stessa apertura in cui decodifica il file.
		// Senza questo ripiego i file nuovi resterebbero in fondo alla griglia
		// -- media_capture_idx ordina capture_ts DESC NULLS LAST -- fino alla
		// generazione dell'anteprima.
		item.CaptureTS = &modified

		batch = append(batch, item)
		if len(batch) >= s.cfg.BatchSize {
			return flush()
		}
		return nil
	})

	if walkErr != nil {
		return total, walkErr
	}
	if err := flush(); err != nil {
		return total, err
	}
	if err := flushOthers(); err != nil {
		return total, err
	}

	outcome, err := s.client.Reconcile(root.RootID, startedAt)
	if err != nil {
		return total, fmt.Errorf("reconcile: %w", err)
	}
	// Il rifiuto e' deliberato: una share mezza montata e' indistinguibile da
	// "l'utente ha cancellato tutto", quindi l'API non tocca niente e il job
	// deve finire in errore per rendere il fatto visibile.
	if outcome.Refused {
		return total, fmt.Errorf("%s", outcome.Reason)
	}

	s.log.Info("scansione conclusa",
		"root", root.Name, "file", total, "non_gestiti", totalOthers,
		"visti", outcome.Seen, "mancanti", outcome.Missing)
	return total, nil
}

// toFolderPath normalizza il percorso di una cartella come lo vuole l'API:
// relativo alla root, separatori a slash, slash finale, vuoto per la radice.
func toFolderPath(relDir string) string {
	if relDir == "." || relDir == "" {
		return ""
	}
	p := filepath.ToSlash(relDir)
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}
