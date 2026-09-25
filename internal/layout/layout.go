// Package layout descrive dove photovault tiene i propri file sulla share.
//
// Sta in un posto solo perche' thumbs scrive le anteprime e trash le toglie:
// se le due formule divergessero, il cestino lascerebbe thumbnail orfane senza
// alcun errore. Lo stesso percorso e' calcolato anche da photovault-dedup e da
// photovault-api, che stanno in altri repo e devono restare allineati a questo.
package layout

import (
	"fmt"
	"path/filepath"
)

// PrivateDir e' la cartella di photovault dentro la share: anteprime e cestino.
const PrivateDir = ".photovault"

// ThumbPath restituisce il percorso della thumbnail di un media.
//
// Le thumbnail sono distribuite su 256 sottocartelle in base al media_id: CIFS
// degrada male oltre qualche migliaio di file per directory, e due livelli di
// shard costerebbero 65.000 mkdir per nulla.
func ThumbPath(mediaRoot string, mediaID int, size string) string {
	shard := fmt.Sprintf("%02x", mediaID%256)
	name := fmt.Sprintf("%d_%s.jpg", mediaID, size)
	return filepath.Join(mediaRoot, PrivateDir, "thumbs", shard, name)
}
