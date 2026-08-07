// Comando photovault-scan.
//
// Il pod parte, prende in carico un job alla volta finche' la coda dei job che
// sa gestire e' vuota, poi esce. E' un CronJob Kubernetes: non resta in ascolto.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pste/photovault-scan/internal/api"
	"github.com/pste/photovault-scan/internal/config"
	"github.com/pste/photovault-scan/internal/scanner"
	"github.com/pste/photovault-scan/internal/thumbs"
	"github.com/pste/photovault-scan/internal/trash"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	client := api.New(cfg.APIURL, cfg.APIToken, log)
	scan := scanner.New(cfg, client, log)
	thumbnailer := thumbs.New(cfg, client, log)
	bin := trash.New(cfg, client, log)

	// I nomi di questa mappa sono anche l'elenco che il pod dichiara al claim:
	// e' cosi' che non si prende i job destinati al pod Python o a quello dedup.
	//
	// I due job del cestino stanno qui perche' questo e' l'unico pod con la
	// share montata in scrittura.
	// Gli handler ricevono il job id perche' devono mandarne il battito a ogni
	// blocco di lavoro: e' cosi' che l'API distingue un job lento da uno il cui
	// pod e' sparito.
	handlers := map[string]func(int) (string, error){
		"scan":       scan.Run,
		"thumbs":     thumbnailer.Run,
		"trashapply": bin.Apply,
		"trashpurge": bin.Purge,
		// Non fa lavoro sulla share: chiede all'API di ricalcolare gli
		// accoppiamenti delle Live Photo. Sta qui e non altrove perche' deve
		// girare dopo thumbs, che e' l'unico a scrivere la durata dei video, e
		// perche' cosi' compare fra i job come tutto il resto.
		"livephoto": func(int) (string, error) {
			coppie, err := client.PairLivePhotos()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%d coppie foto/video riconosciute", coppie), nil
		},
	}

	names := make([]string, 0, len(handlers))
	for name := range handlers {
		names = append(names, name)
	}

	log.Info("pod scan avviato", "handlers", names, "media_root", cfg.MediaRoot)

	// Su Kubernetes la schedulazione la fa il CronJob, non questo pod: e' lui
	// che, svegliandosi, mette in coda il lavoro da fare. Senza, il pod
	// troverebbe la coda vuota e uscirebbe subito.
	for _, name := range cfg.EnqueueOnStart {
		if _, ok := handlers[name]; !ok {
			log.Error("ENQUEUE_ON_START contiene un job che questo pod non sa eseguire", "name", name)
			os.Exit(1)
		}
		if err := client.EnqueueJob(name, time.Now()); err != nil {
			log.Error("accodamento fallito", "name", name, "err", err)
			os.Exit(1)
		}
		log.Info("job accodato all'avvio", "name", name)
	}

	executed := 0
	for {
		job, err := client.ClaimJob(names)
		if err != nil {
			log.Error("claim fallito", "err", err)
			os.Exit(1)
		}
		if job == nil {
			break
		}

		log.Info("job preso in carico", "job_id", job.JobID, "name", job.Name)
		result, err := handlers[job.Name](job.JobID)

		// Un job che non e' piu' nostro non si tocca: scriverci sopra
		// significherebbe raccontare l'esito di un lavoro che sta facendo
		// qualcun altro. Si esce e basta.
		if errors.Is(err, api.ErrJobLost) {
			log.Error("job perso", "job_id", job.JobID, "name", job.Name, "err", err)
			break
		}

		status := "done"
		if err != nil {
			status = "error"
			result = err.Error()
			log.Error("job fallito", "job_id", job.JobID, "name", job.Name, "err", err)
		} else {
			log.Info("job concluso", "job_id", job.JobID, "name", job.Name, "result", result)
		}

		if err := client.UpdateJob(job.JobID, status, result); err != nil {
			log.Error("aggiornamento job fallito", "job_id", job.JobID, "err", err)
		}

		// Dopo una scansione le thumbnail da generare ci sono di sicuro:
		// si accoda subito, e il ciclo qui sopra la esegue in questo stesso run.
		if job.Name == "scan" && status == "done" {
			if err := client.EnqueueJob("thumbs", time.Now()); err != nil {
				log.Error("accodamento thumbs fallito", "err", err)
			}
		}

		// Le durate dei video le ha appena scritte thumbs: prima di adesso
		// l'accoppiamento non avrebbe potuto distinguere una Live Photo da un
		// filmino con lo stesso nome.
		if job.Name == "thumbs" && status == "done" {
			if err := client.EnqueueJob("livephoto", time.Now()); err != nil {
				log.Error("accodamento livephoto fallito", "err", err)
			}
		}

		executed++
	}

	log.Info("pod scan concluso", "job_eseguiti", executed)
}
