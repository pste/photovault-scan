package thumbs

import (
	"testing"
	"time"
)

// Il caso che ha fatto fallire il primo scan di un archivio vero: un EXIF con
// la data azzerata ("0000:00:00 00:00:00") viene normalizzato ad anno 0, mese
// 0, giorno 0, cioe' -0001-11-30. Quella data supera time.IsZero(), arriva a
// PostgreSQL e fa fallire l'INSERT dell'intero batch con SQLSTATE 22007.
func TestCaptureTS(t *testing.T) {
	cases := []struct {
		name string
		when time.Time
		want string // "" significa: da scartare
	}{
		{"exif azzerato", time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC), ""},
		{"zero value di Go", time.Time{}, ""},
		{"anno 1899", time.Date(1899, 12, 31, 23, 59, 59, 0, time.UTC), ""},
		{"orologio sballato in avanti", time.Now().AddDate(5, 0, 0), ""},
		{"scatto normale", time.Date(2019, 4, 10, 12, 34, 56, 0, time.UTC), "2019-04-10T12:34:56Z"},
		{"foto scansionata del 1950", time.Date(1950, 6, 1, 0, 0, 0, 0, time.UTC), "1950-06-01T00:00:00Z"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := captureTS(tc.when)
			if tc.want == "" {
				if got != nil {
					t.Errorf("captureTS(%v) = %q, atteso nil", tc.when, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("captureTS(%v) = nil, atteso %q", tc.when, tc.want)
			}
			if *got != tc.want {
				t.Errorf("captureTS(%v) = %q, atteso %q", tc.when, *got, tc.want)
			}
		})
	}
}

// La data che l'archivio vero ha prodotto, verificata cosi' com'e' uscita.
func TestCaptureTSScartaLaDataDelPrimoScan(t *testing.T) {
	broken := time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC)
	if formatted := broken.UTC().Format(time.RFC3339); formatted != "-0001-11-30T00:00:00Z" {
		t.Fatalf("la fixture non riproduce il caso reale: %q", formatted)
	}
	if captureTS(broken) != nil {
		t.Error("la data che ha rotto lo scan verrebbe ancora inviata all'API")
	}
}

// Il caso che ha fatto fallire il secondo scan dell'archivio vero: un campo
// EXIF con dentro il NUL di terminazione arriva a PostgreSQL e fa fallire
// l'INSERT dell'intero batch con SQLSTATE 22021.
func TestCleanText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"nul di terminazione", "Canon\x00", "Canon"},
		{"nul di padding", "NIKON D90\x00\x00\x00\x00", "NIKON D90"},
		{"solo nul", "\x00\x00", ""},
		{"latin-1 non valido in utf-8", "Caf\xe9", "Caf"},
		{"altri caratteri di controllo", "SM-G950F\r\n", "SM-G950F"},
		{"spazi di contorno", "  Samsung  ", "Samsung"},
		{"accentata valida", "Cámara", "Cámara"},
		{"gia' pulita", "Canon EOS 550D", "Canon EOS 550D"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanText(tc.in); got != tc.want {
				t.Errorf("cleanText(%q) = %q, atteso %q", tc.in, got, tc.want)
			}
		})
	}
}
