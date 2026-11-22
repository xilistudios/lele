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

type State struct {
	Phase     Phase     `json:"phase"`
	Progress  float64   `json:"progress"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

type Options struct {
	Version  string
	Restart  bool
	Progress func(State)
}

type UpdateInfo struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest"`
	UpdateAvailable bool      `json:"update_available"`
	Changelog       string    `json:"changelog,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	HTMLURL         string    `json:"html_url,omitempty"`
}

type Updater struct {
	Checker    *Checker
	Downloader *Downloader
	Installer  *Installer
	Restarter  *Restarter

	CurrentVersion string

	mu    sync.Mutex
	state State
	busy  bool
}

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

func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

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
		return u.CurrentVersion, nil
	}

	var lastPct float64
	u.emit(opts, State{Phase: PhaseDownloading, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	result, err := u.Downloader.Download(ctx, rel, func(downloaded, total int64) {
		if total <= 0 {
			return
		}
		pct := float64(downloaded) / float64(total) * 100
		if pct-lastPct >= 1 || pct >= 100 {
			lastPct = pct
			u.emit(opts, State{Phase: PhaseDownloading, Progress: pct, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
		}
	})
	if err != nil {
		u.fail(opts, err)
		return "", err
	}
	defer os.RemoveAll(result.TempDir)

	u.emit(opts, State{Phase: PhaseVerifying, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	if err := validateBinary(result.BinaryPath, rel.Version()); err != nil {
		u.fail(opts, fmt.Errorf("binary validation failed: %w", err))
		return "", err
	}

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

	if opts.Restart {
		u.emit(opts, State{Phase: PhaseRestarting, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
		if _, err := u.Restarter.Restart(); err != nil {
			u.emit(opts, State{Phase: PhaseDone, From: u.CurrentVersion, To: rel.Version(), Error: "restart failed: " + err.Error(), StartedAt: started})
			return rel.Version(), nil
		}
	}

	u.emit(opts, State{Phase: PhaseDone, From: u.CurrentVersion, To: rel.Version(), StartedAt: started})
	return rel.Version(), nil
}

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
	if _, err := u.Installer.Install(backup, targetPath, u.CurrentVersion+"-rollback"); err != nil {
		return "", err
	}
	return backup, nil
}

func (u *Updater) fail(opts Options, err error) {
	u.emit(opts, State{Phase: PhaseFailed, From: u.CurrentVersion, Error: err.Error()})
}

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
