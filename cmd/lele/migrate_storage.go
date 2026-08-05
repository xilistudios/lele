// Lele - Ultra-lightweight personal AI agent
// migrate-storage: migrate JSON legacy data to SQLite
//
// Copyright (c) 2026 Lele contributors
// License: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func migrateStorageCmd() {
	args := os.Args[2:]
	dryRun := false
	rollback := false

	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		case "--rollback":
			rollback = true
		case "--help", "-h":
			migrateStorageHelp()
			return
		default:
			fmt.Printf("Unknown flag: %s\n", arg)
			migrateStorageHelp()
			os.Exit(1)
		}
	}

	leleDir := config.GetLeleDir()

	if rollback {
		migrateStorageRollback(leleDir)
		return
	}

	migrateStorageForward(leleDir, dryRun)
}

func migrateStorageHelp() {
	fmt.Println("\nMigrate legacy JSON storage to SQLite")
	fmt.Println()
	fmt.Println("Usage: lele migrate-storage [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --dry-run    Show what would be migrated without writing to DB")
	fmt.Println("  --rollback   Restore JSON files from backup and delete the DB")
	fmt.Println("  --help       Show this help")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  lele migrate-storage              Migrate all JSON data to SQLite")
	fmt.Println("  lele migrate-storage --dry-run    Preview migration without changes")
	fmt.Println("  lele migrate-storage --rollback   Restore from backup")
}

// migrateStorageForward performs the forward migration from JSON to SQLite.
func migrateStorageForward(leleDir string, dryRun bool) {
	dbPath := filepath.Join(leleDir, "lele.db")

	if dryRun {
		fmt.Println("=== DRY RUN — no changes will be made ===")
	}

	// Open the store (creates DB + runs migrations).
	dbStore, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer dbStore.Close()

	totalSessions := 0
	totalMessages := 0
	totalCron := 0
	totalGoals := 0
	totalGroups := 0
	totalAuth := 0
	totalClients := 0

	// ── Sessions ──
	sessionsDir := filepath.Join(leleDir, "sessions")
	if info, err := os.Stat(sessionsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(sessionsDir)
		for _, e := range entries {
			if e.IsDir() || e.Name() == "_index.json" || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			key := strings.TrimSuffix(e.Name(), ".json")
			sessionPath := filepath.Join(sessionsDir, e.Name())
			data, err := os.ReadFile(sessionPath)
			if err != nil {
				fmt.Printf("  [sessions] skip %s: %v\n", key, err)
				continue
			}

			var raw struct {
				Key             string          `json:"key"`
				Name            string          `json:"name"`
				Mode            string          `json:"mode"`
				Summary         string          `json:"summary"`
				VerboseLevel    string          `json:"verbose_level"`
				Model           string          `json:"model"`
				ThinkingLevel   string          `json:"thinking_level"`
				InputTokens     int             `json:"input_tokens"`
				OutputTokens    int             `json:"output_tokens"`
				CompactionCount int             `json:"compaction_count"`
				Created         time.Time       `json:"created"`
				Updated         time.Time       `json:"updated"`
				Messages        json.RawMessage `json:"messages"`
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				fmt.Printf("  [sessions] skip %s: parse error: %v\n", key, err)
				continue
			}

			if raw.Key == "" {
				raw.Key = key
			}

			if !dryRun {
				meta := store.SessionMeta{
					Key:             raw.Key,
					Name:            raw.Name,
					Mode:            raw.Mode,
					Summary:         raw.Summary,
					VerboseLevel:    raw.VerboseLevel,
					Model:           raw.Model,
					ThinkingLevel:   raw.ThinkingLevel,
					InputTokens:     raw.InputTokens,
					OutputTokens:    raw.OutputTokens,
					CompactionCount: raw.CompactionCount,
					CreatedAt:       raw.Created,
					UpdatedAt:       raw.Updated,
				}
				if err := dbStore.Sessions().UpsertSession(meta); err != nil {
					fmt.Printf("  [sessions] skip %s: upsert: %v\n", key, err)
					continue
				}

				// Parse messages array.
				var messages []json.RawMessage
				if raw.Messages != nil {
					json.Unmarshal(raw.Messages, &messages)
				}
				for i, msgRaw := range messages {
					// Extract role for the content column.
					var roleHolder struct {
						Role              string `json:"role"`
						ExcludeFromContext bool   `json:"exclude_from_context"`
					}
					json.Unmarshal(msgRaw, &roleHolder)

					// Extract plain text content for the content column.
					var contentHolder struct {
						Content string `json:"content"`
					}
					json.Unmarshal(msgRaw, &contentHolder)

					textContent := contentHolder.Content
					if textContent == "" {
						// Try ContentParts extraction (array of {type,text}).
						var cpHolder struct {
							Content []struct {
								Type string `json:"type"`
								Text string `json:"text"`
							} `json:"content"`
						}
						if json.Unmarshal(msgRaw, &cpHolder) == nil {
							for _, p := range cpHolder.Content {
								if p.Type == "text" && p.Text != "" {
									if textContent != "" {
										textContent += "\n"
									}
									textContent += p.Text
								}
							}
						}
					}

					if err := dbStore.Sessions().InsertMessage(raw.Key, i, roleHolder.Role, textContent, string(msgRaw), roleHolder.ExcludeFromContext); err != nil {
						fmt.Printf("  [sessions] %s msg %d: insert: %v\n", key, i, err)
					} else {
						totalMessages++
					}
				}
			}
			totalSessions++
			fmt.Printf("  [sessions] %s (%d messages)\n", raw.Key, len(func() []json.RawMessage {
				var m []json.RawMessage
				if raw.Messages != nil {
					json.Unmarshal(raw.Messages, &m)
				}
				return m
			}()))
		}
	} else {
		fmt.Println("  [sessions] directory not found, skipping")
	}

	// ── Cron ──
	cronPath := filepath.Join(leleDir, "cron", "jobs.json")
	if data, err := os.ReadFile(cronPath); err == nil {
		var cronStore struct {
			Jobs []struct {
				ID          string          `json:"id"`
				Name        string          `json:"name"`
				Enabled     bool            `json:"enabled"`
				Schedule    json.RawMessage `json:"schedule"`
				Payload     json.RawMessage `json:"payload"`
				State       json.RawMessage `json:"state"`
				Scope       string          `json:"scope"`
				CreatedAtMS int64           `json:"createdAtMs"`
				UpdatedAtMS int64           `json:"updatedAtMs"`
			} `json:"jobs"`
		}
		if err := json.Unmarshal(data, &cronStore); err == nil {
			for _, job := range cronStore.Jobs {
				if !dryRun {
					row := &store.CronJobRow{
						ID:          job.ID,
						Name:        job.Name,
						Enabled:     job.Enabled,
						Schedule:    string(job.Schedule),
						Payload:     string(job.Payload),
						State:       string(job.State),
						Scope:       job.Scope,
						CreatedAtMS: job.CreatedAtMS,
						UpdatedAtMS: job.UpdatedAtMS,
					}
					if err := dbStore.Cron().UpsertCronJob(row); err != nil {
						fmt.Printf("  [cron] %s: upsert: %v\n", job.ID, err)
						continue
					}
				}
				totalCron++
				fmt.Printf("  [cron] %s (%s)\n", job.ID, job.Name)
			}
		} else {
			fmt.Printf("  [cron] parse error: %v\n", err)
		}
	} else {
		fmt.Println("  [cron] jobs.json not found, skipping")
	}

	// ── Goals ──
	goalsDir := filepath.Join(leleDir, "goals")
	if info, err := os.Stat(goalsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(goalsDir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			sessionKey := strings.TrimSuffix(e.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(goalsDir, e.Name()))
			if err != nil {
				continue
			}
			if !dryRun {
				if err := dbStore.Goals().SetGoal(sessionKey, string(data)); err != nil {
					fmt.Printf("  [goals] %s: %v\n", sessionKey, err)
					continue
				}
			}
			totalGoals++
			fmt.Printf("  [goals] %s\n", sessionKey)
		}
	} else {
		fmt.Println("  [goals] directory not found, skipping")
	}

	// ── Groups ──
	groupsDir := filepath.Join(leleDir, "groups")
	if info, err := os.Stat(groupsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(groupsDir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			groupID := strings.TrimSuffix(e.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(groupsDir, e.Name()))
			if err != nil {
				continue
			}
			if !dryRun {
				if err := dbStore.Groups().SetGroupState(groupID, string(data)); err != nil {
					fmt.Printf("  [groups] %s: %v\n", groupID, err)
					continue
				}
			}
			totalGroups++
			fmt.Printf("  [groups] %s\n", groupID)
		}
	} else {
		fmt.Println("  [groups] directory not found, skipping")
	}

	// ── Auth ──
	authPath := filepath.Join(leleDir, "auth.json")
	if data, err := os.ReadFile(authPath); err == nil {
		var authStore struct {
			Credentials map[string]json.RawMessage `json:"credentials"`
		}
		if err := json.Unmarshal(data, &authStore); err == nil {
			for key, cred := range authStore.Credentials {
				if !dryRun {
					if err := dbStore.Auth().SetCredential(key, string(cred)); err != nil {
						fmt.Printf("  [auth] %s: %v\n", key, err)
						continue
					}
				}
				totalAuth++
				fmt.Printf("  [auth] %s\n", key)
			}
		}
	} else {
		fmt.Println("  [auth] auth.json not found, skipping")
	}

	// ── Native clients ──
	clientsPath := filepath.Join(leleDir, "native_clients.json")
	if data, err := os.ReadFile(clientsPath); err == nil {
		var clientStore struct {
			Clients map[string]json.RawMessage `json:"clients"`
		}
		if err := json.Unmarshal(data, &clientStore); err == nil {
			for id, clientJSON := range clientStore.Clients {
				if !dryRun {
					if err := dbStore.NativeClients().SetClient(id, string(clientJSON)); err != nil {
						fmt.Printf("  [native-clients] %s: %v\n", id, err)
						continue
					}
				}
				totalClients++
				fmt.Printf("  [native-clients] %s\n", id)
			}
		}
	} else {
		fmt.Println("  [native-clients] native_clients.json not found, skipping")
	}

	// ── Telegram offset ──
	offsetPath := filepath.Join(leleDir, "telegram_offset.txt")
	if data, err := os.ReadFile(offsetPath); err == nil {
		offset := strings.TrimSpace(string(data))
		if _, err := strconv.Atoi(offset); err == nil {
			if !dryRun {
				dbStore.KV().Set("telegram:offset", offset)
			}
			fmt.Printf("  [kv] telegram:offset = %s\n", offset)
		}
	} else {
		fmt.Println("  [kv] telegram_offset.txt not found, skipping")
	}

	// ── Summary ──
	fmt.Println()
	fmt.Printf("=== Migration Summary ===\n")
	fmt.Printf("  Sessions:  %d (%d messages)\n", totalSessions, totalMessages)
	fmt.Printf("  Cron:      %d\n", totalCron)
	fmt.Printf("  Goals:     %d\n", totalGoals)
	fmt.Printf("  Groups:    %d\n", totalGroups)
	fmt.Printf("  Auth:      %d\n", totalAuth)
	fmt.Printf("  Clients:   %d\n", totalClients)
	fmt.Println()

	if dryRun {
		fmt.Println("=== DRY RUN — no changes were made ===")
		return
	}

	// ── Backup legacy files ──
	timestamp := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(leleDir, "backup-json-"+timestamp)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		fmt.Printf("Error creating backup dir: %v\n", err)
		os.Exit(1)
	}

	moveToBackup := func(src string, name string) {
		if _, err := os.Stat(src); err != nil {
			return
		}
		dst := filepath.Join(backupDir, name)
		if err := os.Rename(src, dst); err != nil {
			fmt.Printf("  ⚠ could not move %s: %v\n", name, err)
		} else {
			fmt.Printf("  ✓ moved %s → backup\n", name)
		}
	}

	moveToBackup(sessionsDir, "sessions")
	moveToBackup(filepath.Join(leleDir, "cron"), "cron")
	moveToBackup(goalsDir, "goals")
	moveToBackup(groupsDir, "groups")
	moveToBackup(authPath, "auth.json")
	moveToBackup(clientsPath, "native_clients.json")
	moveToBackup(offsetPath, "telegram_offset.txt")

	// Write migration marker.
	dbStore.KV().Set("migrated_from_json", time.Now().Format(time.RFC3339))

	fmt.Printf("\n✅ Migration complete. Backup at: %s\n", backupDir)
}

// migrateStorageRollback restores JSON files from the latest backup.
func migrateStorageRollback(leleDir string) {
	// Find latest backup.
	entries, err := os.ReadDir(leleDir)
	if err != nil {
		fmt.Printf("Error reading lele dir: %v\n", err)
		os.Exit(1)
	}

	var latestBackup string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "backup-json-") {
			candidate := filepath.Join(leleDir, e.Name())
			if latestBackup == "" || candidate > latestBackup {
				latestBackup = candidate
			}
		}
	}

	if latestBackup == "" {
		fmt.Println("No backup found. Nothing to rollback.")
		return
	}

	fmt.Printf("Restoring from: %s\n", latestBackup)

	// Delete the DB.
	dbPath := filepath.Join(leleDir, "lele.db")
	if _, err := os.Stat(dbPath); err == nil {
		if err := os.Remove(dbPath); err != nil {
			fmt.Printf("Error deleting DB: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("  ✓ Deleted lele.db")
	}

	// Restore backed-up items.
	restoreFromBackup := func(name string, dest string) {
		src := filepath.Join(latestBackup, name)
		if _, err := os.Stat(src); err != nil {
			return
		}
		if err := os.Rename(src, dest); err != nil {
			fmt.Printf("  ⚠ could not restore %s: %v\n", name, err)
		} else {
			fmt.Printf("  ✓ restored %s\n", name)
		}
	}

	restoreFromBackup("sessions", filepath.Join(leleDir, "sessions"))
	restoreFromBackup("cron", filepath.Join(leleDir, "cron"))
	restoreFromBackup("goals", filepath.Join(leleDir, "goals"))
	restoreFromBackup("groups", filepath.Join(leleDir, "groups"))
	restoreFromBackup("auth.json", filepath.Join(leleDir, "auth.json"))
	restoreFromBackup("native_clients.json", filepath.Join(leleDir, "native_clients.json"))
	restoreFromBackup("telegram_offset.txt", filepath.Join(leleDir, "telegram_offset.txt"))

	fmt.Println("\n✅ Rollback complete.")
}
