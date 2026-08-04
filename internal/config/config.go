package config

import (
	"log/slog"
	"os"
	"strconv"
)

// Config raccoglie tutto quello che il pod prende dall'ambiente.
// Nessun percorso assoluto arriva dal database: il mount e' una proprieta' del
// pod, quindi vive qui e non in una tabella.
type Config struct {
	APIURL      string
	APIToken    string
	MediaRoot   string
	LogLevel    slog.Level
	Workers     int
	BatchSize   int
	ThumbSmall  int
	ThumbMedium int
	RootName    string
	RootRelPath string
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func level(name string) slog.Level {
	switch name {
	case "trace", "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func Load() Config {
	return Config{
		APIURL:    env("API_URL", "http://localhost:3000"),
		APIToken:  os.Getenv("API_TOKEN"),
		MediaRoot: env("MEDIA_ROOT", "/data/photos"),
		LogLevel:  level(env("LOG_LEVEL", "info")),
		Workers:   envInt("SCAN_WORKERS", 3),
		// 100 righe per blocco: circa 100 KB di corpo, ben sotto il limite di 1 MB
		// di Fastify, che non va alzato.
		BatchSize:   envInt("BATCH_SIZE", 100),
		ThumbSmall:  envInt("THUMB_SMALL_PX", 320),
		ThumbMedium: envInt("THUMB_MEDIUM_PX", 1280),
		RootName:    env("ROOT_NAME", "Foto"),
		RootRelPath: os.Getenv("ROOT_REL_PATH"),
	}
}
