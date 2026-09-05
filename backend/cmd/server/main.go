package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"cosmo/backend/internal/config"
	"cosmo/backend/internal/database"
	"cosmo/backend/internal/httpapi"
	"cosmo/backend/internal/modelgateway"
	"cosmo/backend/internal/runs"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database startup failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	models := modelgateway.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMSystemPrompt, cfg.LLMRequestTimeout)
	api, err := httpapi.New(ctx, cfg, db, models, logger)
	if err != nil {
		logger.Error("API startup failed", "error", err)
		os.Exit(1)
	}
	if err := api.BootstrapAdmin(ctx); err != nil {
		logger.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	worker := runs.NewWorker(db, logger, runs.WorkerOptions{})
	var chatWorkers sync.WaitGroup
	chatWorkers.Add(1)
	go func() { defer chatWorkers.Done(); api.RunKnowledgeSnapshotCleanup(workerCtx) }()
	for i := 0; i < cfg.ChatWorkers; i++ {
		chatWorkers.Add(1)
		go func() {
			defer chatWorkers.Done()
			_ = api.RunChatWorker(workerCtx, httpapi.ChatWorkerOptions{Timeout: cfg.ChatTimeout})
		}()
	}
	go func() {
		if err := worker.Run(workerCtx); err != nil {
			logger.Error("run worker stopped", "error", err)
		}
	}()

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           api.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		logger.Info("Cosmo API listening", "address", cfg.Address, "entra_enabled", cfg.EntraEnabled(), "model_configured", cfg.LLMEnabled())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	stopWorker()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	chatWorkers.Wait()
}
