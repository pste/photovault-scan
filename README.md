# photovault-scan

Cron di scansione della share, in Go. Gira come CronJob Kubernetes.

Ha due responsabilità distinte, tenute in due job separati:

- **`scan`** — costruisce l'alberatura in DB: cartelle e file, con i metadati di base. Non
  calcola hash e non genera thumbnail: deve restare veloce.
- **`thumbs`** — prende dal DB i media con `thumb_status='pending'` e genera le loro
  thumbnail.

È l'unico componente con la share montata in **scrittura**, quindi è anche l'unico che sposta
file (job `trashapply`).

## Job gestiti

| Job | Cosa fa |
|---|---|
| `scan` | walk incrementale, rileva file nuovi o modificati (mtime + size) |
| `fullscan` | uguale ma ignora mtime: rilegge tutto |
| `thumbs` | genera le thumbnail mancanti |
| `trashapply` | sposta in `.photovault/trash/<yyyymmdd>/` i file che l'utente ha deciso di eliminare |
| `trashpurge` | elimina davvero i file rimasti nel cestino oltre i giorni di ritenzione |

I due job del cestino stanno qui perché questo è l'unico pod con la share montata in
scrittura. L'API si limita ad accodare il lavoro: è una garanzia strutturale che un bug
nell'API non possa cancellare foto.

`trashapply` non usa mai `os.Remove`: fa una rename, che sulla stessa share è atomica e
istantanea e rende ogni errore recuperabile con un `mv`. Rimuove anche le due thumbnail del
file, che altrimenti resterebbero orfane a occupare spazio.

`trashpurge` è **l'unico punto di tutto photovault in cui un file viene davvero cancellato**.

Il pod parte, chiama `POST /api/internal/jobs/claim` con la lista dei nomi qui sopra, esegue,
richiama il claim finché la coda è vuota, poi esce. Dopo un `scan` completato si riaccoda da
solo leggendo `parameters.cron_scan` (`github.com/robfig/cron/v3`).

## Requisiti

- La share montata in lettura-scrittura su `MEDIA_ROOT`
- `photovault-api` raggiungibile
- **Go non serve installato**: build e test girano in Docker (vedi sotto)

## Variabili d'ambiente

```
API_URL=http://localhost:3000
API_TOKEN=
MEDIA_ROOT=/data/photos
LOG_LEVEL=trace
SCAN_WORKERS=3
GOMEMLIMIT=800MiB
```

`GOMEMLIMIT` è obbligatorio, non un'ottimizzazione: il garbage collector di Go non conosce i
limiti cgroup e cresce oltre il limite del pod finché non viene OOMKillato a metà scansione.
Un'immagine da 24 MP decodificata in RGBA occupa 96 MB, e con 3 worker più l'headroom del GC
si arriva vicino al mezzo giga.

## Sviluppo senza Go installato

```bash
sh private/go.sh build ./...
sh private/go.sh test ./...
```

Lo script esegue il toolchain Go in un container montando due volumi di cache (moduli e
build). **I volumi di cache sono determinanti**: senza, ogni build riscarica i moduli e
ricompila tutto da capo.

Prova end-to-end contro una cartella locale:

```bash
docker build -f .docker/Dockerfile -t photovault-scan:dev .
docker run --rm --network host \
  --env-file .env \
  -v /percorso/delle/foto:/data/photos \
  photovault-scan:dev
```

Le credenziali stanno nel `.env` locale (copiato da `.env.dist`, gitignorato) e non vanno
mai scritte sulla riga di comando: finirebbero nella cronologia della shell.

## Scelte implementative

**`filepath.WalkDir`, non `filepath.Walk`.** WalkDir usa il tipo restituito da readdir ed
evita una `Lstat` per file: su CIFS è la differenza tra una passata di 30 secondi e una di 5
minuti su 50.000 file.

**Change detection su mtime + size, con 1 secondo di tolleranza.** Non si usa l'hash per
rilevare le modifiche: una passata di sha256 su mezzo terabyte costa oltre un'ora anche a
velocità di rete piena. L'hash lo calcola `photovault-dedup`, quando serve.

**Allowlist di estensioni, non blocklist.** Le blocklist perdono sempre contro `.AAE`, `.XMP`,
`.THM`, `.LRV`:

```
immagini: jpg jpeg png gif webp heic heif tif tiff bmp
video   : mp4 mov m4v avi mkv mts m2ts 3gp
raw     : cr2 cr3 nef arw dng raf orf rw2   → riga creata, thumb_status='unsupported'
```

Vanno saltate le directory `.photovault/`, `@eaDir`, `@Recycle`, `#recycle`, `#snapshot`,
`@Recently-Snapshot` e ogni directory che inizia per punto.

**EXIF con `github.com/evanoberholster/imagemeta`**: Go puro, senza CGO, e legge l'EXIF
direttamente da JPEG, HEIC, TIFF, CR2/CR3 e DNG. `rwcarlsen/goexif` non è più mantenuto e
copre solo JPEG/TIFF; `go-exiftool` si porta dietro Perl e ~80 MB di immagine.

Non si salva il blob EXIF grezzo in JSONB: sarebbero ~1 GB di dati mai interrogati su 50k
foto. Si salvano le dodici colonne tipizzate che servono davvero.

Per i video basta un `ffprobe -v quiet -print_format json -show_format -show_streams`: durata,
codec, dimensioni e `creation_time` in un solo sottoprocesso.

**Thumbnail — approccio ibrido:**

| Input | Metodo |
|---|---|
| jpeg/png/gif/webp/bmp/tiff | Go puro: decode → `ApproxBiLinear` a 1280 → `CatmullRom` a 320 → `image/jpeg` q82 |
| heic/heif | `heif-convert` (pacchetto `libheif-tools`) verso un JPEG temporaneo, poi il percorso Go |
| video | `ffmpeg -ss 3 -i in -frames:v 1 ...`, con **`-ss` prima di `-i`** (seek in input, niente decodifica completa); sotto i 3 secondi di durata si prende il frame 0 |
| raw | rimandato: `thumb_status='unsupported'` |

Una sola decodifica produce entrambe le dimensioni. HEIC passa da libheif e non da ffmpeg,
il cui supporto HEIC è inaffidabile.

**L'orientamento EXIF va applicato prima di scrivere la thumbnail.** Otto casi, una trentina
di righe di rotate/flip. Dimenticarlo significa metà delle foto verticali storte nella
griglia: il bug più visibile che ci sia.

**Layout delle thumbnail**, due dimensioni per media:

```
.photovault/thumbs/<media_id % 256 in hex>/<media_id>_s.jpg    320 px, griglia
.photovault/thumbs/<media_id % 256 in hex>/<media_id>_m.jpg   1280 px, lightbox e input CLIP
```

256 sottocartelle: CIFS degrada male oltre qualche migliaio di file per directory, e due
livelli di shard costerebbero 65.000 `mkdir` inutili. Non si specchia l'albero di origine —
si romperebbe a ogni rinomina di cartella.

**Batch verso l'API da 100 record.** L'idempotenza è gratis grazie alla chiave naturale
`UNIQUE(folder_id, filename)`: l'API fa un `INSERT ... ON CONFLICT DO UPDATE` per riga, quindi
rigiocare un batch non ha effetti. Un pod che muore rifà semplicemente la coda non confermata
al giro dopo, perché "non è in DB oppure mtime è cambiato" **è** la definizione dell'unità di
lavoro. Per questo non esiste nessuna tabella di checkpoint: non aggiungerebbe niente.

**Il guard del reconcile.** A fine scansione il job chiama `POST /api/internal/scan/reconcile`
con il numero di media visti. L'API marca `missing_since` sulle righe non viste **solo se ne
ha visti almeno il 90% di quelli noti**; sotto quella soglia rifiuta e il job va in `error`
senza toccare niente.

Il motivo: una share CIFS irraggiungibile o montata a metà è indistinguibile, a livello di
syscall, da "l'utente ha cancellato tutto". E in nessun caso lo scan cancella righe: esiste
solo `missing_since`, e la rimozione definitiva è un'azione esplicita dell'utente.

**Il cestino, mai `unlink`.** `trashapply` sposta in `.photovault/trash/<yyyymmdd>/`. Una
rename sulla stessa share è atomica e istantanea, e rende ogni errore recuperabile con un
`mv`.

## Build

```dockerfile
FROM golang:1.24-alpine AS build   # CGO_ENABLED=0, build statica
FROM alpine:3.21                   # + ffmpeg, libheif-tools, tzdata
```
