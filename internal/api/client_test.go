package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Il 409 sul battito significa "questo job non e' piu' tuo", e deve fermare il
// pod. Prima non arrivava nemmeno: do() scartava il 409 senza restituire errore
// -- la condizione lo escludeva apposta, per il reconcile -- quindi il pod
// continuava a lavorare su un job che l'API aveva gia' dato per perso. Il
// 2026-08-07 due pod hanno rigenerato le stesse thumbnail per quattro ore e mezza.
func TestHeartbeatSul409DiceDiFermarsi(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"job non in esecuzione"}`)
	}))
	defer server.Close()

	err := newTestClient(server.URL).Heartbeat(42)
	if !errors.Is(err, ErrJobLost) {
		t.Fatalf("atteso ErrJobLost, ottenuto %v", err)
	}
}

func TestHeartbeatAccettatoNonFermaNiente(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := newTestClient(server.URL).Heartbeat(42); err != nil {
		t.Fatalf("un battito accettato non deve dare errore: %v", err)
	}
}

// Un errore di rete o un 500 non sono la stessa cosa di un 409: il job resta
// nostro, il lavoro e' salvato, e fermarsi per un errore passeggero
// significherebbe abbandonare una coda che nessun altro sta lavorando.
func TestHeartbeatNonSiFermaSuUnErrorePasseggero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if err := newTestClient(server.URL).Heartbeat(42); err != nil {
		t.Fatalf("un 500 non deve fermare il job: %v", err)
	}

	// Server irraggiungibile: stesso trattamento.
	if err := newTestClient("http://127.0.0.1:1").Heartbeat(42); err != nil {
		t.Fatalf("un server irraggiungibile non deve fermare il job: %v", err)
	}
}

// Il 409 del reconcile invece non e' un errore: e' un rifiuto deliberato del
// guard anti perdita dati, e il chiamante deve poterne leggere il corpo.
// Questa e' la ragione per cui do() escludeva il 409, ed e' la cosa da non
// rompere correggendo il battito.
func TestReconcileLeggeIlCorpoDel409(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w,
			`{"refused":true,"reason":"visti 10 media su 1000 noti","seen":10,"known":1000}`)
	}))
	defer server.Close()

	outcome, err := newTestClient(server.URL).Reconcile(1, time.Now())
	if err != nil {
		t.Fatalf("il rifiuto del reconcile non e' un errore di trasporto: %v", err)
	}
	if !outcome.Refused || outcome.Seen != 10 || outcome.Known != 1000 {
		t.Fatalf("corpo del 409 non letto: %+v", outcome)
	}
}

func newTestClient(url string) *Client {
	return New(url, "token-di-prova", slog.New(slog.NewTextHandler(io.Discard, nil)))
}
