package thumbs

import (
	"image"
	"image/color"
	"testing"
)

// Un'immagine 2000x1000 meta' rossa (sinistra) e meta' blu (destra), con
// orientamento 6: raddrizzata e' verticale, col rosso in alto. Ruotare dopo il
// ridimensionamento deve dare lo stesso risultato che ruotare prima.
func TestRenderRaddrizzaDopoIlRidimensionamento(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 1000 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	six := 6
	medium, small := render(src, &six, 1280, 320)

	if b := medium.Bounds(); b.Dx() != 640 || b.Dy() != 1280 {
		t.Fatalf("media: attesa 640x1280, ottenuta %dx%d", b.Dx(), b.Dy())
	}
	if b := small.Bounds(); b.Dx() != 160 || b.Dy() != 320 {
		t.Fatalf("piccola: attesa 160x320, ottenuta %dx%d", b.Dx(), b.Dy())
	}
	for _, img := range []image.Image{medium, small} {
		b := img.Bounds()
		top, _, _, _ := img.At(b.Dx()/2, b.Dy()/4).RGBA()
		_, _, bottom, _ := img.At(b.Dx()/2, b.Dy()*3/4).RGBA()
		if top < 0xf000 || bottom < 0xf000 {
			t.Fatalf("orientamento sbagliato: rosso in alto %x, blu in basso %x", top, bottom)
		}
	}
}

// Per tutti e otto gli orientamenti, ruotare dopo il ridimensionamento deve
// dare la stessa immagine che ruotare prima, com'era fatto fino al 2026-09-25.
// L'immagine e' asimmetrica (gradienti diversi sui due assi), quindi un caso
// sbagliato si vede.
func TestRenderEquivaleARuotarePrima(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1500, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1500; x++ {
			src.Set(x, y, color.RGBA{uint8(x * 255 / 1500), uint8(y * 255 / 900), 128, 255})
		}
	}
	for o := 1; o <= 8; o++ {
		orientation := o
		before := resize(applyOrientation(src, &orientation), 300)
		after, _ := render(src, &orientation, 300, 100)
		if before.Bounds() != after.Bounds() {
			t.Fatalf("orientamento %d: %v contro %v", o, before.Bounds(), after.Bounds())
		}
		var diff, n float64
		b := before.Bounds()
		for y := 0; y < b.Dy(); y += 7 {
			for x := 0; x < b.Dx(); x += 7 {
				r1, g1, _, _ := before.At(x, y).RGBA()
				r2, g2, _, _ := after.At(x, y).RGBA()
				diff += abs(int(r1)-int(r2)) + abs(int(g1)-int(g2))
				n += 2
			}
		}
		if mean := diff / n / 257; mean > 2 {
			t.Errorf("orientamento %d: differenza media %.1f livelli su 255", o, mean)
		}
	}
}

func abs(v int) float64 {
	if v < 0 {
		return float64(-v)
	}
	return float64(v)
}
