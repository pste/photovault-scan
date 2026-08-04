package thumbs

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// Qualita' JPEG delle anteprime. A 320 px il WebP risparmierebbe circa 4 KB per
// file, in cambio di un encoder WebP in Go (x/image/webp e' solo decode):
// image/jpeg sta nella libreria standard e non costa nulla.
const jpegQuality = 82

// write ridimensiona mantenendo le proporzioni e scrive un JPEG.
// Se l'immagine e' gia' piu' piccola del lato richiesto non viene ingrandita:
// ingrandire produce solo file piu' grandi senza aggiungere dettaglio.
func (t *Thumbnailer) write(src image.Image, path string, longEdge int) error {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return os.ErrInvalid
	}

	targetW, targetH := width, height
	if width > longEdge || height > longEdge {
		if width >= height {
			targetW = longEdge
			targetH = height * longEdge / width
		} else {
			targetH = longEdge
			targetW = width * longEdge / height
		}
	}
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	// CatmullRom: piu' lento di ApproxBiLinear ma nettamente migliore sulle
	// riduzioni forti, che e' esattamente il caso di una thumbnail.
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Scrittura su file temporaneo e rename: una thumbnail troncata da un pod
	// ucciso a meta' verrebbe poi servita come immagine rotta, e il database
	// la crederebbe valida.
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if err := jpeg.Encode(file, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		file.Close()
		os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, path)
}

// applyOrientation raddrizza l'immagine secondo il tag EXIF Orientation.
// Gli otto casi della specifica; 0 e 1 non richiedono nulla.
func applyOrientation(src image.Image, orientation *int) image.Image {
	if orientation == nil || *orientation <= 1 || *orientation > 8 {
		return src
	}

	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// I casi da 5 a 8 comportano una rotazione di 90 gradi: larghezza e altezza
	// si scambiano.
	swapped := (*orientation >= 5)
	outW, outH := width, height
	if swapped {
		outW, outH = height, width
	}

	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var nx, ny int
			switch *orientation {
			case 2: // specchiata orizzontalmente
				nx, ny = width-1-x, y
			case 3: // ruotata di 180
				nx, ny = width-1-x, height-1-y
			case 4: // specchiata verticalmente
				nx, ny = x, height-1-y
			case 5: // trasposta
				nx, ny = y, x
			case 6: // ruotata di 90 in senso orario
				nx, ny = height-1-y, x
			case 7: // trasversa
				nx, ny = height-1-y, width-1-x
			case 8: // ruotata di 90 in senso antiorario
				nx, ny = y, width-1-x
			}
			dst.Set(nx, ny, src.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return dst
}
