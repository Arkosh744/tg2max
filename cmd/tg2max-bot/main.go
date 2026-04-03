package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"crypto/rand"
	"encoding/hex"

	"github.com/arkosh/tg2max/internal/admin"
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
	AdminUserIDs   []int64 `yaml:"admin_user_ids"`
	DBPath         string  `yaml:"db_path"`
	AdminEnabled   bool    `yaml:"admin_enabled"`
	AdminAddr      string  `yaml:"admin_addr"`
	AdminPassword  string  `yaml:"admin_password"`
	AdminSecret    string  `yaml:"admin_secret"`

	// Clone flow (MTProto userbot)
	TGAppID           int    `yaml:"tg_app_id"`
	TGAppHash         string `yaml:"tg_app_hash"`
	UserbotSessionKey string `yaml:"userbot_session_key"`

	// Admin Mini App
	AdminWebAppURL string `yaml:"admin_webapp_url"`
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
		TelegramToken:     cfg.TelegramToken,
		MaxToken:          cfg.MaxToken,
		RateLimitRPS:      cfg.RateLimitRPS,
		TempDir:           cfg.TempDir,
		TGAPIEndpoint:     cfg.TGAPIEndpoint,
		TGAPIFilesDir:     cfg.TGAPIFilesDir,
		AllowedUserIDs:    cfg.AllowedUserIDs,
		AdminUserIDs:      cfg.AdminUserIDs,
		DBPath:            cfg.DBPath,
		TGAppID:           cfg.TGAppID,
		TGAppHash:         cfg.TGAppHash,
		UserbotSessionKey: cfg.UserbotSessionKey,
		AdminWebAppURL:    cfg.AdminWebAppURL,
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
		sig := <-sigCh
		log.Info("received signal, initiating graceful shutdown", "signal", sig)
		cancel()
		// Give active migration up to 30s to save cursor before force exit
		select {
		case sig = <-sigCh:
			log.Warn("second signal received, forcing exit", "signal", sig)
			os.Exit(1)
		case <-time.After(30 * time.Second):
			log.Warn("graceful shutdown timeout, forcing exit")
			os.Exit(1)
		}
	}()

	// Start admin web UI if enabled
	if cfg.AdminEnabled {
		if cfg.AdminPassword == "" {
			log.Error("admin_password is required when admin_enabled is true")
			os.Exit(1)
		}
		if cfg.AdminSecret == "" {
			buf := make([]byte, 32)
			rand.Read(buf)
			cfg.AdminSecret = hex.EncodeToString(buf)
		}
		if cfg.AdminAddr == "" {
			cfg.AdminAddr = ":8080"
		}
		adminSrv := admin.New(b.Storage(), b, admin.Config{
			Addr:         cfg.AdminAddr,
			Password:     cfg.AdminPassword,
			Secret:       cfg.AdminSecret,
			BotToken:     cfg.TelegramToken,
			AdminUserIDs: cfg.AdminUserIDs,
		}, log)
		go func() {
			if err := adminSrv.ListenAndServe(ctx); err != nil && err != http.ErrServerClosed {
				log.Error("admin server error", "error", err)
			}
		}()
	}

	// Standalone health endpoint when admin panel is disabled
	if !cfg.AdminEnabled {
		healthAddr := ":8081"
		if v := os.Getenv("HEALTH_ADDR"); v != "" {
			healthAddr = v
		}
		healthMux := http.NewServeMux()
		healthMux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"ok","uptime":"%s"}`, b.Uptime().Round(time.Second))
		})
		go func() {
			srv := &http.Server{Addr: healthAddr, Handler: healthMux}
			go func() { <-ctx.Done(); srv.Close() }()
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("health server error", "error", err)
			}
		}()
		log.Info("health endpoint started", "addr", healthAddr)
	}

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
	if v := os.Getenv("ADMIN_ENABLED"); v == "true" || v == "1" {
		cfg.AdminEnabled = true
	}
	if v := os.Getenv("ADMIN_ADDR"); v != "" {
		cfg.AdminAddr = v
	}
	if v := os.Getenv("ADMIN_PASSWORD"); v != "" {
		cfg.AdminPassword = v
	}
	if v := os.Getenv("ADMIN_SECRET"); v != "" {
		cfg.AdminSecret = v
	}

	if v := os.Getenv("ALLOWED_USER_IDS"); v != "" {
		for _, s := range strings.Split(v, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err == nil {
				cfg.AllowedUserIDs = append(cfg.AllowedUserIDs, id)
			}
		}
	}
	if v := os.Getenv("ADMIN_USER_IDS"); v != "" {
		for _, s := range strings.Split(v, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err == nil {
				cfg.AdminUserIDs = append(cfg.AdminUserIDs, id)
			}
		}
	}

	// Clone flow env vars
	if v := os.Getenv("TG_APP_ID"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			cfg.TGAppID = id
		}
	}
	if v := os.Getenv("TG_APP_HASH"); v != "" {
		cfg.TGAppHash = v
	}
	if v := os.Getenv("USERBOT_SESSION_KEY"); v != "" {
		cfg.UserbotSessionKey = v
	}
	if v := os.Getenv("ADMIN_WEBAPP_URL"); v != "" {
		cfg.AdminWebAppURL = v
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
