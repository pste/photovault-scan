// Package api parla con photovault-api. Questo pod non conosce PostgreSQL:
// tutta la lettura e la scrittura di stato passa dalle rotte /api/internal,
// protette da bearer token.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		// Timeout generoso: il reconcile a fine scansione tocca l'intera
		// tabella media e su un archivio grande non e' istantaneo.
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

type Job struct {
	JobID  int    `json:"job_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Root struct {
	RootID  int    `json:"root_id"`
	Name    string `json:"name"`
	RelPath string `json:"rel_path"`
	Enabled bool   `json:"enabled"`
}

type Folder struct {
	FolderID int    `json:"folder_id"`
	Path     string `json:"path"`
}

// MediaMeta sono i metadati che si ricavano solo aprendo il file, quindi solo
// nel job thumbs: lo scan si limita a camminare la share.
//
// I puntatori distinguono "assente" da "zero": una foto senza GPS non e' una
// foto a latitudine 0. E' anche cio' che permette all'API di aggiornare in
// COALESCE, senza cancellare un valore gia' noto.
type MediaMeta struct {
	Width       *int     `json:"width,omitempty"`
	Height      *int     `json:"height,omitempty"`
	DurationS   *float64 `json:"duration_s,omitempty"`
	Orientation *int     `json:"orientation,omitempty"`
	CaptureTS   *string  `json:"capture_ts,omitempty"`
	CameraMake  *string  `json:"camera_make,omitempty"`
	CameraModel *string  `json:"camera_model,omitempty"`
	GpsLat      *float64 `json:"gps_lat,omitempty"`
	GpsLon      *float64 `json:"gps_lon,omitempty"`
}

// MediaItem e' una riga inviata dallo scan: solo cio' che si sa da readdir,
// senza aprire il file. L'unico campo di MediaMeta valorizzato e' CaptureTS, e
// vale l'mtime finche' il job thumbs non legge la data vera dall'EXIF.
type MediaItem struct {
	FolderID  int    `json:"folder_id"`
	FileName  string `json:"file_name"`
	MediaKind string `json:"media_kind"`
	Ext       string `json:"ext"`
	FileSize  int64  `json:"file_size"`
	Modified  string `json:"modified"`
	MediaMeta
}

type PendingMedia struct {
	MediaID    int    `json:"media_id"`
	FileName   string `json:"file_name"`
	MediaKind  string `json:"media_kind"`
	Ext        string `json:"ext"`
	FolderPath string `json:"folder_path"`
	RelPath    string `json:"rel_path"`
}

// ThumbResult riporta l'esito della generazione e, insieme, i metadati letti
// nella stessa apertura del file. Viaggiano insieme perche' sono prodotti dallo
// stesso lavoro: una seconda passata costerebbe una seconda lettura su CIFS.
type ThumbResult struct {
	MediaID     int    `json:"media_id"`
	ThumbStatus string `json:"thumb_status"`
	MediaMeta
}

type ReconcileOutcome struct {
	Refused bool   `json:"refused"`
	Reason  string `json:"reason"`
	Seen    int    `json:"seen"`
	Known   int    `json:"known"`
	Missing int    `json:"missing"`
}

func (c *Client) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	// Il 409 del reconcile non e' un errore di trasporto: e' un rifiuto
	// deliberato, e il chiamante deve poterne leggere il corpo.
	if res.StatusCode >= 400 && res.StatusCode != http.StatusConflict {
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, string(payload))
	}
	if out != nil && len(payload) > 0 {
		return json.Unmarshal(payload, out)
	}
	return nil
}

// ClaimJob prende in carico un job tra quelli che questo pod sa eseguire.
// L'elenco dei nomi e' obbligatorio: senza, il pod si prenderebbe anche i job
// degli altri pod, non troverebbe l'handler e li marcherebbe in errore.
func (c *Client) ClaimJob(names []string) (*Job, error) {
	var job *Job
	err := c.do("POST", "/api/internal/jobs/claim", map[string]any{"names": names}, &job)
	return job, err
}

func (c *Client) UpdateJob(jobID int, status, result string) error {
	body := map[string]any{"status": status, "result": result}
	return c.do("PATCH", fmt.Sprintf("/api/internal/jobs/%d", jobID), body, nil)
}

func (c *Client) EnqueueJob(name string, when time.Time) error {
	body := map[string]any{"name": name, "when": when.UTC().Format(time.RFC3339)}
	return c.do("POST", "/api/internal/jobs", body, nil)
}

func (c *Client) GetParameters() (map[string]any, error) {
	var out map[string]any
	err := c.do("GET", "/api/internal/parameters", nil, &out)
	return out, err
}

func (c *Client) GetRoots() ([]Root, error) {
	var out []Root
	err := c.do("GET", "/api/internal/scan/roots", nil, &out)
	return out, err
}

func (c *Client) CreateRoot(name, relPath string) (*Root, error) {
	var out Root
	body := map[string]any{"name": name, "rel_path": relPath}
	err := c.do("POST", "/api/internal/scan/root", body, &out)
	return &out, err
}

func (c *Client) RegisterFolder(rootID int, path string) (*Folder, error) {
	var out Folder
	body := map[string]any{"root_id": rootID, "path": path}
	err := c.do("POST", "/api/internal/scan/folder", body, &out)
	return &out, err
}

// OtherFile e' un file che photovault non gestisce: ne' immagine, ne' video,
// ne' RAW. Non entra in media perche' non ha una pipeline da percorrere; serve
// a sapere cosa c'e' sulla share oltre alla libreria, e a poterlo cestinare.
type OtherFile struct {
	RootID   int    `json:"root_id"`
	Path     string `json:"path"`
	FileName string `json:"file_name"`
	Ext      string `json:"ext"`
	FileSize int64  `json:"file_size"`
	Modified string `json:"modified"`
}

func (c *Client) SendOthers(items []OtherFile) error {
	return c.do("POST", "/api/internal/scan/other/batch", map[string]any{"items": items}, nil)
}

func (c *Client) SendMedia(items []MediaItem) error {
	return c.do("POST", "/api/internal/scan/media/batch", map[string]any{"items": items}, nil)
}

func (c *Client) Reconcile(rootID int, startedAt time.Time) (*ReconcileOutcome, error) {
	var out ReconcileOutcome
	body := map[string]any{
		"root_id":    rootID,
		"started_at": startedAt.UTC().Format(time.RFC3339),
	}
	err := c.do("POST", "/api/internal/scan/reconcile", body, &out)
	return &out, err
}

func (c *Client) GetPending(stage string, limit int) ([]PendingMedia, error) {
	var out []PendingMedia
	err := c.do("GET", fmt.Sprintf("/api/internal/pending/%s?limit=%d", stage, limit), nil, &out)
	return out, err
}

func (c *Client) SendThumbs(items []ThumbResult) error {
	return c.do("POST", "/api/internal/thumb/batch", map[string]any{"items": items}, nil)
}

// TrashItem e' un file da spostare nel cestino (o da eliminare, se scaduto).
// I percorsi sono relativi: il mount e' una proprieta' del pod.
type TrashItem struct {
	TrashID      int    `json:"trash_id"`
	MediaID      int    `json:"media_id"`
	OriginalPath string `json:"original_path"`
	TrashPath    string `json:"trash_path"`
	RelPath      string `json:"rel_path"`
	// ,string perche' node-postgres serializza i bigint come STRINGA JSON:
	// lo fa apposta, un bigint puo' eccedere il numero sicuro di JavaScript.
	// Senza questo tag l'unmarshal fallisce con "cannot unmarshal string".
	FileSize int64 `json:"file_size,string"`
}

func (c *Client) GetPendingTrash(limit int) ([]TrashItem, error) {
	var out []TrashItem
	err := c.do("GET", fmt.Sprintf("/api/internal/trash/pending?limit=%d", limit), nil, &out)
	return out, err
}

func (c *Client) CompleteTrash(trashID int, status, result string) error {
	body := map[string]any{"status": status, "result": result}
	return c.do("POST", fmt.Sprintf("/api/internal/trash/%d/done", trashID), body, nil)
}

// GetExpiredTrash restituisce i file nel cestino da piu' giorni della
// ritenzione configurata: la soglia la applica l'API, che conosce i parametri.
func (c *Client) GetExpiredTrash(limit int) ([]TrashItem, error) {
	var out []TrashItem
	err := c.do("GET", fmt.Sprintf("/api/internal/trash/expired?limit=%d", limit), nil, &out)
	return out, err
}

func (c *Client) CompletePurge(trashID int, status, result string) error {
	body := map[string]any{"status": status, "result": result}
	return c.do("POST", fmt.Sprintf("/api/internal/trash/%d/purged", trashID), body, nil)
}
