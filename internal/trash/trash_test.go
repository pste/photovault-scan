package trash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
)

func TestPaths(t *testing.T) {
	tr := &Trash{cfg: config.Config{MediaRoot: "/data/photos"}}

	// Una riga da spostare, com'e' in coda per trashapply.
	valid := []api.TrashItem{
		{RelPath: "", OriginalPath: "2019/a.jpg", TrashPath: ".photovault/trash/20260925/7_a.jpg"},
		{RelPath: "FOTO", OriginalPath: "2019_01/", TrashPath: ".photovault/trash/20260925/3_2019_01"},
	}
	for _, item := range valid {
		if _, err := tr.source(item); err != nil {
			t.Errorf("%+v: sorgente rifiutata: %v", item, err)
		}
		if _, err := tr.trashTarget(item); err != nil {
			t.Errorf("%+v: destinazione rifiutata: %v", item, err)
		}
	}

	// Ognuno di questi, al purge, avrebbe cancellato qualcosa che non e' nel
	// cestino: la root, la cartella di un giorno intero, o fuori dalla share.
	badTarget := []api.TrashItem{
		{TrashPath: ""},
		{TrashPath: "."},
		{TrashPath: ".photovault/trash"},
		{TrashPath: ".photovault/trash/20260925"},
		{TrashPath: ".photovault/trash/../thumbs/x"},
		{TrashPath: ".photovault/trash/20260925/../../../2019"},
		{RelPath: "..", TrashPath: ".photovault/trash/20260925/1_a.jpg"},
		{RelPath: "../../etc", TrashPath: ".photovault/trash/20260925/1_a.jpg"},
	}
	for _, item := range badTarget {
		if _, err := tr.trashTarget(item); err == nil {
			t.Errorf("%+v: destinazione accettata", item)
		}
	}

	badSource := []api.TrashItem{
		{OriginalPath: ""},
		{OriginalPath: "../fuori.jpg"},
		{OriginalPath: ".photovault/thumbs"},
		{RelPath: "..", OriginalPath: "a.jpg"},
	}
	for _, item := range badSource {
		if _, err := tr.source(item); err == nil {
			t.Errorf("%+v: sorgente accettata", item)
		}
	}
}

// La riga da svuotare come la manda davvero l'API (getExpiredTrash): niente
// original_path, che al purge non serve. Il 2026-09-25 un controllo unico la
// rifiutava, e 774 file scaduti sono rimasti nel cestino in stato 'error'.
func TestPurgeAccettaLaRigaDellAPI(t *testing.T) {
	tr := &Trash{cfg: config.Config{MediaRoot: "/data/photos"}}
	item := api.TrashItem{TrashID: 1, MediaID: 1295, RelPath: "",
		TrashPath: ".photovault/trash/20260806/1295_sognare-un-mostro-1.jpg"}
	if _, err := tr.trashTarget(item); err != nil {
		t.Fatalf("riga del purge rifiutata: %v", err)
	}
}

// Una rename rifiutata per permessi non si "aggira" copiando: prima la copia
// riusciva, la rimozione dell'originale no, e nel cestino restava una copia
// che nessuna riga conosceva -- il purge guarda solo le righe 'done'.
func TestMoveSenzaPermessiNonLasciaCopie(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("da root i permessi non fermano la rename")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "2019")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("foto"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	tr := &Trash{cfg: config.Config{MediaRoot: root}}
	item := api.TrashItem{OriginalPath: "2019/a.jpg", TrashPath: ".photovault/trash/20260925/1_a.jpg"}
	if err := tr.moveOne(item); err == nil {
		t.Fatal("la rename senza permessi deve fallire")
	}
	if _, err := os.Stat(filepath.Join(root, item.TrashPath)); !os.IsNotExist(err) {
		t.Fatalf("nel cestino e' rimasta una copia: %v", err)
	}
}
