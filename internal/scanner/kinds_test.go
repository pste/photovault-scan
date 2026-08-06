package scanner

import "testing"

// Le estensioni video sono cresciute dopo il primo scan di un archivio vero,
// dove 7,7 GB di filmati erano finiti fra i file non gestiti. Il test fissa
// anche cio' che deve restare FUORI: i file di servizio AVCHD stanno accanto
// ai video ma non contengono video, e un .mod di troppo li' dentro produrrebbe
// migliaia di righe con la thumbnail in errore.
func TestKindOf(t *testing.T) {
	cases := []struct {
		ext  string
		kind string // "" significa: non deve entrare in archivio
	}{
		{"jpg", "image"}, {"HEIC", "image"}, {"png", "image"},
		{"mp4", "video"}, {"mov", "video"}, {"3gp", "video"},
		// Le nove trovate sull'archivio vero.
		{"mpg", "video"}, {"webm", "video"}, {"vob", "video"},
		{"wmv", "video"}, {"f4v", "video"}, {"m2t", "video"},
		{"flv", "video"}, {"mxv", "video"}, {"rm", "video"},
		{"cr2", "raw"}, {"dng", "raw"},
		// File di servizio AVCHD: accanto ai video, ma non sono video.
		{"cpi", ""}, {"mpl", ""}, {"bdm", ""}, {"tdt", ""}, {"tid", ""},
		// Audio: photovault gestisce immagini e video.
		{"mp3", ""}, {"m4a", ""}, {"wav", ""}, {"ac3", ""},
		// Il solito corredo da scartare.
		{"aae", ""}, {"thm", ""}, {"lrprev", ""}, {"psd", ""}, {"db", ""},
	}

	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			kind, ok := kindOf(tc.ext)
			if tc.kind == "" {
				if ok {
					t.Errorf("kindOf(%q) = %q, non doveva entrare in archivio", tc.ext, kind)
				}
				return
			}
			if !ok {
				t.Fatalf("kindOf(%q) non riconosciuta, atteso %q", tc.ext, tc.kind)
			}
			if kind != tc.kind {
				t.Errorf("kindOf(%q) = %q, atteso %q", tc.ext, kind, tc.kind)
			}
		})
	}
}
