package main

import (
	"fmt"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
)

// setupFileLogging enables file logging using the configured logs path.
// It must be called after loadConfig so that the -c/--config-dir flag
// (LELE_CONFIG_DIR) and the logs.path setting are honored. Calling
// EnableMultiFileLogging overrides the logger basePath that was resolved at
// package init time (before LELE_CONFIG_DIR was set), fixing the case where
// logs always landed in ~/.lele/logs regardless of -c.
func setupFileLogging(cfg *config.Config) {
	if !cfg.Logs.Enabled {
		return
	}
	logsPath := cfg.LogsPath()
	if err := logger.EnableMultiFileLogging(logsPath); err != nil {
		fmt.Printf("Warning: could not enable file logging: %v\n", err)
		return
	}
	if cfg.Logs.MaxDays > 0 {
		if err := logger.CleanupOldLogs(cfg.Logs.MaxDays); err != nil {
			fmt.Printf("Warning: could not cleanup old logs: %v\n", err)
		}
	}
}
