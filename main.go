package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"nacos-config-sync/config"
	"nacos-config-sync/logger"
	"nacos-config-sync/syncer"
)

func main() {
	log := logger.New()

	wd, err := os.Getwd()
	if err != nil {
		log.Error("getwd failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	nc, err := config.LoadNacosConfig(wd)
	if err != nil {
		log.Error("load nacos.ini failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := syncer.New(log, nc)
	if err != nil {
		log.Error("init syncer failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	runErr := s.Run(ctx)
	s.Stop()

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		log.Error("run failed", map[string]interface{}{"error": runErr.Error()})
		os.Exit(1)
	}

	log.Info("stopped cleanly", map[string]interface{}{})
}
