package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Phase describes the current stage of an update.
type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseChecking    Phase = "checking"
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseInstalling  Phase = "installing"
	PhaseRestarting  Phase = "restarting"
	PhaseDone        Phase = "done"
	PhaseFailed      Phase = "failed"
)

// State is a snapshot of the update pipeline.
type State struct {
	Phase     Phase     `json:"phase"`
	Progress  float64   `json:"progress"` // 0-100 during download
	From      string    `json:"from"`
	To        string    `json:"to"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// Options control an Apply run.
type Options struct {
	// Version pins a specific version ("" = latest).
	Version string
	// Restart controls whether the service is restarted after install.
	Restart bool
	// Progress receives state updates (optional).
	Progress func(State)
}

// UpdateInfo is the result of a check.
type UpdateInfo struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest"`
	UpdateAvailable bool      `json:"update_available"`
	Changelog       string    `json:"changelog,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	HTMLURL         string    `json:"html_url,omitempty"`
}

// Updater orchestrates check/download/install/restart.
// It is safe for concurrent use; only one update runs at a time.
type Updater struct {
	Checker    *Checker
	Downloader *Downloader
	Installer  *Installer
	Restarter  *Restarter

	// CurrentVersion is the running version (from build ldflags).
	CurrentVersion string

	mu    sync.Mutex
	state State
	busy  bool
}

// NewUpdater creates an Updater with all components wired.
func NewUpdater(repo, backupDir, currentVersion string) *Updater {
	return &Updater{
		Checker:        NewChecker(repo),
		Downloader:     NewDownloader(),
		Installer:      NewInstaller(backupDir),
		Restarter:      NewRestarter(),
		CurrentVersion: currentVersion,
		state:          State{Phase: PhaseIdle},
	}
}

// State returns a copy of the current pipeline state.
func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

// Busy reports whether an update is in progress.
func (u *Updater) Busy() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.busy
}

func (u *Updater) setState(s State) {
	u.mu.Lock()
	u.state = s
	u.mu.Unlock()
}

func (u *Updater) emit(opts Options, s State) {
	u.setState(s)
	if opts.Progress != nil {
		opts.Progress(s)
	}
}

// Check queries the latest release and compares with the current version.
func (u *Updater) Check(ctx context.Context) (*UpdateInfo, error) {
	rel, err := u.Checker.Latest(ctx)
	if err != nil {
		return nil, err
	}
	return &UpdateInfo{
		Current:         u.CurrentVersion,
		Latest:          rel.Version(),
		UpdateAvailable: NewerVersion(u.CurrentVersion, rel.Tag),
		Changelog:       rel.Body,
		PublishedAt:     rel.PublishedAt,
		HTMLURL:         rel.HTMLURL,
	}, nil
}

// Apply runs the full update pipeline. Returns the new version on success.
func (u *Updater) Apply(ctx context.Context, opts Options) (string, error) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return "", fmt.Errorf("an update is already in progress")
	}
	u.busy = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()

	started := time.Now()

	// 1. Resolve target release.
	u.emit(opts, State{Phase: PhaseChecking, From: u.CurrentVersion, StartedAt: started})
	var rel *Release
	var err error
	if opts.Version != "" {
		rel, err = u.Checker.ByTag(ctx, opts.Version)
	} else {
		rel, err = u.Checker.Latest(ctx)
	}
	if err != nil {
		u.fail(opts, err)
		return "", err
	}

	if !NewerVersion(u.CurrentVersion, rel.Tag) && opts.Version == "" {
		u.emit(opts, State{Phase: PhaseDone, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
		return u.CurrentVersion, nil // already up to date
	}

	// 2. Download + verify + extract.
	var lastPct float64
	u.emit(opts, State{Phase: PhaseDownloading, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	result, err := u.Downloader.Download(ctx, rel, func(downloaded, total int64) {
		if total <= 0 {
			return
		}
		pct := float64(downloaded) / float64(total) * 100
		if pct-lastPct >= 1 || pct >= 100 { // throttle callbacks
			lastPct = pct
			u.emit(opts, State{Phase: PhaseDownloading, Progress: pct, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
		}
	})
	if err != nil {
		u.fail(opts, err)
		return "", err
	}
	defer os.RemoveAll(result.TempDir)

	// 3. Validate the new binary before touching the real one.
	u.emit(opts, State{Phase: PhaseVerifying, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	if err := validateBinary(result.BinaryPath, rel.Version()); err != nil {
		u.fail(opts, fmt.Errorf("binary validation failed: %w", err))
		return "", err
	}

	// 4. Install.
	u.emit(opts, State{Phase: PhaseInstalling, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	targetPath, err := CurrentBinaryPath()
	if err != nil {
		u.fail(opts, err)
		return "", err
	}
	if _, err := u.Installer.Install(result.BinaryPath, targetPath, u.CurrentVersion); err != nil {
		u.fail(opts, err)
		return "", err
	}

	// 5. Restart (optional).
	//
	// Note on the self-exec path: Restarter.Restart spawns the replacement
	// process, runs OnRestart (the gateway wires its shutdown coordinator) and
	// then terminates this process, so the lines below may never be reached —
	// that is intended, the new binary reports the updated version. The systemd
	// paths return normally because systemd SIGTERMs us.
	if opts.Restart {
		u.emit(opts, State{Phase: PhaseRestarting, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
		if _, err := u.Restarter.Restart(); err != nil {
			// Install succeeded; restart failed. Not fatal for the update itself.
			u.emit(opts, State{Phase: PhaseDone, From: u.CurrentVersion, To: rel.Version(), Error: "restart failed: " + err.Error(), StartedAt: started})
			return rel.Version(), nil
		}
	}

	u.emit(opts, State{Phase: PhaseDone, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	return rel.Version(), nil
}

// Rollback restores the most recent backup over the current binary.
func (u *Updater) Rollback(ctx context.Context) (string, error) {
	backup, err := u.Installer.LatestBackup()
	if err != nil {
		return "", err
	}
	if backup == "" {
		return "", fmt.Errorf("no backup available")
	}
	targetPath, err := CurrentBinaryPath()
	if err != nil {
		return "", err
	}
	// Install the backup as the "new" binary (this also backs up the
	// current one, so rollback is itself reversible).
	if _, err := u.Installer.Install(backup, targetPath, u.CurrentVersion+"-rollback"); err != nil {
		return "", err
	}
	return backup, nil
}

func (u *Updater) fail(opts Options, err error) {
	u.emit(opts, State{Phase: PhaseFailed, From: u.CurrentVersion, Error: err.Error()})
}

// validateBinary runs "<binary> version" and checks it reports the
// expected version. This catches corrupt or wrong-arch binaries early.
func validateBinary(path, expectedVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("running new binary: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), expectedVersion) {
		return fmt.Errorf("new binary reports %q, expected version %q", strings.TrimSpace(string(out)), expectedVersion)
	}
	return nil
}
