package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/arkosh/tg2max/internal/bot"
	"gopkg.in/yaml.v3"
)

type Config struct {
	TelegramToken  string  `yaml:"telegram_token"`
	MaxToken       string  `yaml:"max_token"`
	RateLimitRPS   float64 `yaml:"rate_limit_rps"`
	TempDir        string  `yaml:"temp_dir"`
	TGAPIEndpoint  string  `yaml:"tg_api_endpoint"`
	TGAPIFilesDir  string  `yaml:"tg_api_files_dir"`
	AllowedUserIDs []int64 `yaml:"allowed_user_ids"`
	DBPath         string  `yaml:"db_path"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	verbose := flag.Bool("verbose", false, "debug logging")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	cfg := loadConfig(*configPath, log)

	b, err := bot.New(bot.Config{
		TelegramToken:  cfg.TelegramToken,
		MaxToken:       cfg.MaxToken,
		RateLimitRPS:   cfg.RateLimitRPS,
		TempDir:        cfg.TempDir,
		TGAPIEndpoint:  cfg.TGAPIEndpoint,
		TGAPIFilesDir:  cfg.TGAPIFilesDir,
		AllowedUserIDs: cfg.AllowedUserIDs,
		DBPath:         cfg.DBPath,
	}, log)
	if err != nil {
		log.Error("failed to create bot", "error", err)
		os.Exit(1)
	}
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutting down...")
		cancel()
	}()

	if err := b.Run(ctx); err != nil {
		log.Error("bot stopped with error", "error", err)
		os.Exit(1)
	}
}

func loadConfig(path string, log *slog.Logger) Config {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		log.Info("config file not found, using env vars", "path", path)
	} else {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			log.Error("failed to parse config", "error", err)
			os.Exit(1)
		}
	}

	// Env vars override config file
	if v := os.Getenv("TELEGRAM_TOKEN"); v != "" {
		cfg.TelegramToken = v
	}
	if v := os.Getenv("MAX_TOKEN"); v != "" {
		cfg.MaxToken = v
	}
	if v := os.Getenv("TG_API_ENDPOINT"); v != "" {
		cfg.TGAPIEndpoint = v
	}
	if v := os.Getenv("TG_API_FILES_DIR"); v != "" {
		cfg.TGAPIFilesDir = v
	}

	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}

	if v := os.Getenv("ALLOWED_USER_IDS"); v != "" {
		for _, s := range strings.Split(v, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err == nil {
				cfg.AllowedUserIDs = append(cfg.AllowedUserIDs, id)
			}
		}
	}

	if cfg.TelegramToken == "" {
		log.Error("telegram_token is required (config or TELEGRAM_TOKEN env)")
		os.Exit(1)
	}
	if cfg.MaxToken == "" {
		log.Error("max_token is required (config or MAX_TOKEN env)")
		os.Exit(1)
	}

	return cfg
}
