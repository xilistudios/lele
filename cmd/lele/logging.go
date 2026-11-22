package main

import (
	"fmt"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
)

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
