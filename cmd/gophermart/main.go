package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gradis/ya-pr_diploma-1/internal/accrual"
	"github.com/gradis/ya-pr_diploma-1/internal/auth"
	"github.com/gradis/ya-pr_diploma-1/internal/config"
	"github.com/gradis/ya-pr_diploma-1/internal/database"
	"github.com/gradis/ya-pr_diploma-1/internal/handler"
	"github.com/gradis/ya-pr_diploma-1/internal/logger"
	postgresrepository "github.com/gradis/ya-pr_diploma-1/internal/repository/postgres"
	"github.com/gradis/ya-pr_diploma-1/internal/service"
	"go.uber.org/zap"
)

const shutdownTimeout = 10 * time.Second

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 1 << 20
)

func main() {
	logg, err := logger.New()
	if err != nil {
		panic(err)
	}

	err = run(logg)
	if err != nil {
		logg.Error("gophermart stopped with an error", zap.Error(err))
	}

	_ = logg.Sync()
	if err != nil {
		os.Exit(1)
	}
}

func run(logg *zap.Logger) error {
	if _, configured := os.LookupEnv(gin.EnvGinMode); !configured {
		gin.SetMode(gin.ReleaseMode)
	}

	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}

	if err := database.RunMigrations(cfg.DatabaseURI); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}

	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopSignals()

	db, err := database.New(context.Background(), cfg.DatabaseURI)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer db.Close()

	repo := postgresrepository.New(db)
	loyaltyService := service.New(repo)
	authManager := auth.NewManager(cfg.AuthSecret, auth.WithSecureCookie(cfg.CookieSecure))
	httpHandler := handler.New(loyaltyService, authManager, logg)
	serverBaseContext, cancelServerBaseContext := context.WithCancel(context.Background())
	defer cancelServerBaseContext()

	server := &http.Server{
		Addr:              cfg.RunAddress,
		Handler:           httpHandler.Router(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return serverBaseContext
		},
	}

	listener, err := net.Listen("tcp", cfg.RunAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.RunAddress, err)
	}

	accrualClient := accrual.NewClient(cfg.AccrualSystemAddress, nil, accrual.WithLogger(logg))
	accrualWorker := accrual.NewWorker(repo, accrualClient, logg)
	workerContext, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		accrualWorker.Run(workerContext)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	logg.Info(
		"gophermart started",
		zap.String("run_address", cfg.RunAddress),
		zap.String("accrual_system_address", cfg.AccrualSystemAddress),
	)

	select {
	case <-signalContext.Done():
		logg.Info("shutdown signal received")
	case serverError := <-serverErrors:
		if !errors.Is(serverError, http.ErrServerClosed) {
			cancelWorker()
			cancelServerBaseContext()
			<-workerDone
			return fmt.Errorf("serve HTTP: %w", serverError)
		}
	}

	cancelWorker()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()

	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		cancelServerBaseContext()
		<-workerDone
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	cancelServerBaseContext()
	<-workerDone
	return nil
}
