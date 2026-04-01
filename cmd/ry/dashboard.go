package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/dashboard"
	"gorm.io/gorm"
)

func newDashboardCmd() *cobra.Command {
	var (
		configPath       string
		port             int
		tlsCert          string
		tlsKey           string
		rateLimitEnabled bool
		rateLimitRPM     int
	)

	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Start the read-only web dashboard",
		Long:  "Launches a local web dashboard for monitoring Railyard status in real-time.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDashboard(cmd, configPath, port, tlsCert, tlsKey, rateLimitEnabled, rateLimitRPM)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "port to listen on")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "path to TLS certificate file (enables HTTPS)")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "path to TLS private key file (enables HTTPS)")
	cmd.Flags().BoolVar(&rateLimitEnabled, "rate-limit", false, "enable per-IP rate limiting")
	cmd.Flags().IntVar(&rateLimitRPM, "rate-limit-rpm", 120, "max requests per minute per IP (when rate limiting enabled)")
	return cmd
}

func runDashboard(cmd *cobra.Command, configPath string, port int, tlsCert, tlsKey string, rateLimitEnabled bool, rateLimitRPM int) error {
	// Retry DB connection to tolerate the database starting up (e.g. in K8s
	// where the dashboard pod may start before the database is ready).
	var gormDB *gorm.DB
	var projectName string
	const maxRetries = 30
	for i := range maxRetries {
		cfg, db, err := connectFromConfig(configPath)
		if err == nil {
			gormDB = db
			projectName = cfg.Project
			break
		}
		// Config load errors are permanent — don't retry.
		if strings.Contains(err.Error(), "load config") {
			return err
		}
		if i == maxRetries-1 {
			return fmt.Errorf("database not ready after %d attempts: %w", maxRetries, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Waiting for database (%d/%d): %v\n", i+1, maxRetries, err)
		time.Sleep(2 * time.Second)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(cmd.OutOrStdout(), "\nReceived %s, shutting down...\n", sig)
		cancel()
	}()

	return dashboard.Start(ctx, dashboard.StartOpts{
		DB:          gormDB,
		Port:        port,
		Out:         cmd.OutOrStdout(),
		TLSCert:     tlsCert,
		TLSKey:      tlsKey,
		ProjectName: projectName,
		RateLimit: dashboard.RateLimitConfig{
			Enabled:           rateLimitEnabled,
			RequestsPerMinute: rateLimitRPM,
		},
	})
}
