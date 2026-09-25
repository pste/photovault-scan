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

	valid := []api.TrashItem{
		{RelPath: "", OriginalPath: "2019/a.jpg", TrashPath: ".photovault/trash/20260925/7_a.jpg"},
		{RelPath: "FOTO", OriginalPath: "2019_01/", TrashPath: ".photovault/trash/20260925/3_2019_01"},
	}
	for _, item := range valid {
		if _, _, err := tr.paths(item); err != nil {
			t.Errorf("%+v: rifiutato: %v", item, err)
		}
	}

	// Ognuno di questi, al purge, avrebbe cancellato qualcosa che non e' nel
	// cestino: la root, la cartella di un giorno intero, o fuori dalla share.
	invalid := []api.TrashItem{
		{OriginalPath: "a.jpg", TrashPath: ""},
		{OriginalPath: "a.jpg", TrashPath: "."},
		{OriginalPath: "a.jpg", TrashPath: ".photovault/trash"},
		{OriginalPath: "a.jpg", TrashPath: ".photovault/trash/20260925"},
		{OriginalPath: "a.jpg", TrashPath: ".photovault/trash/../thumbs/x"},
		{OriginalPath: "a.jpg", TrashPath: ".photovault/trash/20260925/../../../2019"},
		{RelPath: "..", OriginalPath: "a.jpg", TrashPath: ".photovault/trash/20260925/1_a.jpg"},
		{RelPath: "../../etc", OriginalPath: "a.jpg", TrashPath: ".photovault/trash/20260925/1_a.jpg"},
		{OriginalPath: "", TrashPath: ".photovault/trash/20260925/1_a"},
		{OriginalPath: "../fuori.jpg", TrashPath: ".photovault/trash/20260925/1_a"},
		{OriginalPath: ".photovault/thumbs", TrashPath: ".photovault/trash/20260925/1_a"},
	}
	for _, item := range invalid {
		if _, _, err := tr.paths(item); err == nil {
			t.Errorf("%+v: accettato", item)
		}
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
