package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/dashboard"
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
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
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
		DB:      gormDB,
		Port:    port,
		Out:     cmd.OutOrStdout(),
		TLSCert: tlsCert,
		TLSKey:  tlsKey,
		RateLimit: dashboard.RateLimitConfig{
			Enabled:           rateLimitEnabled,
			RequestsPerMinute: rateLimitRPM,
		},
	})
}
