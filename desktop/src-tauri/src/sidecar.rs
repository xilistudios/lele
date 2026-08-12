//! Sidecar lifecycle: spawn the lele gateway, wait for the ready signal,
//! and shut it down gracefully.
//!
//! The gateway is spawned as a child process with the `--desktop` flag and is
//! expected to emit a single `LELE_READY` line on stdout once it is ready to
//! serve requests. All human-readable logs go to stderr (inherited so they
//! show up in the console/log pipeline).

use std::io::{BufRead, BufReader};
use std::path::Path;
use std::process::{Child, Command, Stdio};
use std::sync::mpsc::channel;
use std::thread;
use std::time::{Duration, Instant};

use serde::Deserialize;

/// How long (in seconds) the graceful shutdown waits for the child to exit
/// before a forced kill is issued by callers that use the default.
pub const DEFAULT_GRACE: Duration = Duration::from_secs(5);

/// Ready-line prefix emitted by the gateway on stdout.
const READY_PREFIX: &str = "LELE_READY ";
/// Error-line prefix emitted by the gateway on stdout.
const ERROR_PREFIX: &str = "LELE_ERROR ";

/// Parsed from the gateway's `LELE_READY` line.
#[derive(Debug, Clone, Deserialize)]
pub struct ReadyInfo {
    pub url: String,
    pub port: u16,
}

/// Parsed from the gateway's `LELE_ERROR` line.
#[derive(Debug, Clone, Deserialize, Default)]
pub(crate) struct ErrorInfo {
    #[serde(default)]
    code: String,
    #[serde(default)]
    pid: Option<u32>,
    #[serde(default)]
    error: Option<String>,
}

/// Errors produced while starting the sidecar.
#[derive(Debug)]
pub enum SidecarError {
    /// The child process could not be spawned at all.
    SpawnFailed(String),
    /// Another gateway instance holds the lock. Contains its PID.
    AlreadyRunning(u32),
    /// Gateway reported a startup error (`lock_failed`, `bind_failed`, ...).
    StartupFailed(String),
    /// No ready signal arrived within the configured timeout.
    Timeout(String),
}

impl std::fmt::Display for SidecarError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            SidecarError::SpawnFailed(msg) => write!(f, "failed to spawn sidecar: {msg}"),
            SidecarError::AlreadyRunning(pid) => {
                write!(f, "another gateway instance is already running (pid {pid})")
            }
            SidecarError::StartupFailed(msg) => write!(f, "sidecar startup failed: {msg}"),
            SidecarError::Timeout(msg) => write!(f, "sidecar did not become ready in time: {msg}"),
        }
    }
}

impl std::error::Error for SidecarError {}

/// A running sidecar process.
#[derive(Debug)]
pub struct SidecarHandle {
    child: Child,
    url: String,
    port: u16,
    started_at: Instant,
}

impl SidecarHandle {
    /// The gateway's base URL (e.g. `http://127.0.0.1:PORT`).
    pub fn url(&self) -> &str {
        &self.url
    }

    /// The port the gateway is listening on.
    pub fn port(&self) -> u16 {
        self.port
    }

    /// The process ID of the sidecar child.
    pub fn pid(&self) -> u32 {
        self.child.id()
    }

    /// Seconds elapsed since the sidecar reported ready.
    pub fn uptime_secs(&self) -> u64 {
        self.started_at.elapsed().as_secs()
    }

    /// True if the child process is still running (a non-blocking check).
    pub fn is_alive(&mut self) -> bool {
        self.child.try_wait().map_or(true, |status| status.is_none())
    }
}

/// Parse the JSON payload of a `LELE_READY` line.
fn parse_ready_line(line: &str) -> Option<ReadyInfo> {
    let payload = line.strip_prefix(READY_PREFIX)?;
    serde_json::from_str(payload).ok()
}

/// Parse the JSON payload of a `LELE_ERROR` line.
fn parse_error_line(line: &str) -> Option<ErrorInfo> {
    let payload = line.strip_prefix(ERROR_PREFIX)?;
    serde_json::from_str(payload).ok()
}

/// Spawn the gateway sidecar and block until it reports ready.
///
/// * `bin_path` — path to the lele binary (sidecar bundled or system).
/// * `token` — desktop auth token passed via `LELE_DESKTOP_TOKEN`.
/// * `timeout` — how long to wait for the ready line (recommend 30s).
///
/// The token is never included in any error message or log.
pub fn spawn_sidecar(bin_path: &Path, token: &str, timeout: Duration) -> Result<SidecarHandle, SidecarError> {
    let mut child = Command::new(bin_path)
        .args(["gateway", "--desktop", "--port", "0"])
        .env("LELE_DESKTOP", "1")
        .env("LELE_DESKTOP_TOKEN", token)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .spawn()
        .map_err(|e| SidecarError::SpawnFailed(e.to_string()))?;

    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| SidecarError::SpawnFailed("stdout not captured".to_string()))?;

    // A reader thread drains stdout line-by-line and forwards them over a
    // channel so we can wait with a deadline without blocking forever.
    let (tx, rx) = channel::<String>();
    let reader_thread = thread::Builder::new()
        .name("lele-sidecar-stdout".to_string())
        .spawn(move || {
            for line in BufReader::new(stdout).lines() {
                match line {
                    Ok(l) => {
                        if tx.send(l).is_err() {
                            break;
                        }
                    }
                    Err(_) => break, // stdout closed / read error: stop draining.
                }
            }
        })
        .expect("failed to spawn stdout reader thread");

    let deadline = Instant::now() + timeout;
    loop {
        let now = Instant::now();
        if now >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            let _ = reader_thread.join();
            return Err(SidecarError::Timeout("no ready signal received".to_string()));
        }

        // Wait for the next line, bounded by the remaining time.
        let remaining = deadline.saturating_duration_since(now);
        match rx.recv_timeout(remaining) {
            Ok(line) => {
                if let Some(info) = parse_ready_line(&line) {
                    // Keep the stdout reader alive so the gateway never
                    // deadlocks on a full pipe if it writes more output.
                    // Detach the thread; it will exit when stdout closes.
                    let _reader_thread = reader_thread;
                    return Ok(SidecarHandle {
                        child,
                        url: info.url,
                        port: info.port,
                        started_at: Instant::now(),
                    });
                }

                if let Some(err) = parse_error_line(&line) {
                    let _ = child.kill();
                    let _ = child.wait();
                    let _ = reader_thread.join();
                    if err.code == "already_running" {
                        return Err(SidecarError::AlreadyRunning(err.pid.unwrap_or(0)));
                    }
                    let detail = match err.error {
                        Some(e) => format!("{}: {}", err.code, e),
                        None => err.code,
                    };
                    return Err(SidecarError::StartupFailed(detail));
                }

                // Any other line is unexpected on stdout; ignore it and keep
                // waiting for the real ready/error signal.
            }
            Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {
                // Loop re-checks the deadline above.
            }
            Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => {
                // Reader thread ended: stdout closed, meaning the child exited.
                let _ = child.wait();
                return Err(SidecarError::SpawnFailed(
                    "sidecar exited before reporting ready".to_string(),
                ));
            }
        }
    }
}

/// Gracefully stop the sidecar: send `SIGTERM` (Unix) / `kill` (Windows),
/// wait up to `grace`, then force kill. Returns `true` if it exited
/// gracefully (i.e. the first signal was sufficient).
///
/// On Unix this uses the system `kill` binary (no extra crates needed) to
/// deliver `SIGTERM`, which the gateway handles to flush and shut down.
pub fn stop_sidecar(handle: &mut SidecarHandle, grace: Duration) -> bool {
    // Signal the child to terminate gracefully.
    #[cfg(unix)]
    {
        let _ = Command::new("kill")
            .args(["-TERM", &handle.pid().to_string()])
            .status();
    }
    #[cfg(windows)]
    {
        let _ = handle.child.kill();
    }

    // Poll for a graceful exit within the grace period. `try_wait` reaps the
    // child when it returns `Some(status)`, so no further `wait()` is needed.
    let deadline = Instant::now() + grace;
    loop {
        if let Some(status) = handle.child.try_wait().unwrap_or(None) {
            return status.success();
        }
        if Instant::now() >= deadline {
            break;
        }
        thread::sleep(Duration::from_millis(100));
    }

    // Grace period elapsed: force kill and reap.
    let _ = handle.child.kill();
    let _ = handle.child.wait();
    false
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::os::unix::fs::PermissionsExt;
    use std::path::PathBuf;

    #[test]
    fn test_parse_ready_line_valid() {
        let line = r#"LELE_READY {"url":"http://127.0.0.1:59999","port":59999}"#;
        let info = parse_ready_line(line).expect("should parse ready line");
        assert_eq!(info.url, "http://127.0.0.1:59999");
        assert_eq!(info.port, 59999);
    }

    #[test]
    fn test_parse_ready_line_bad_payload() {
        assert!(parse_ready_line("LELE_READY not-json").is_none());
        assert!(parse_ready_line("LELE_READY").is_none());
        assert!(parse_ready_line("garbage").is_none());
    }

    #[test]
    fn test_parse_error_line_already_running() {
        let line = r#"LELE_ERROR {"code":"already_running","pid":4242}"#;
        let err = parse_error_line(line).expect("should parse error line");
        assert_eq!(err.code, "already_running");
        assert_eq!(err.pid, Some(4242));
        assert_eq!(err.error, None);
    }

    #[test]
    fn test_parse_error_line_bind_failed() {
        let line = r#"LELE_ERROR {"code":"bind_failed","error":"addr in use"}"#;
        let err = parse_error_line(line).expect("should parse error line");
        assert_eq!(err.code, "bind_failed");
        assert_eq!(err.error.as_deref(), Some("addr in use"));
    }

    #[test]
    fn test_parse_error_line_partial() {
        // Missing fields fall back to defaults.
        let line = r#"LELE_ERROR {"code":"lock_failed"}"#;
        let err = parse_error_line(line).expect("should parse error line");
        assert_eq!(err.code, "lock_failed");
        assert_eq!(err.pid, None);
        assert_eq!(err.error, None);
    }

    /// Write an executable shell stub into the temp dir and return its path.
    fn write_stub(name: &str, body: &str) -> PathBuf {
        let path = std::env::temp_dir().join(format!("lele_sidecar_test_{name}_{}.sh", std::process::id()));
        fs::write(&path, body).expect("write stub");
        let mut perms = fs::metadata(&path).expect("metadata").permissions();
        perms.set_mode(0o755);
        fs::set_permissions(&path, perms).expect("chmod stub");
        path
    }

    #[test]
    #[cfg(unix)]
    fn test_spawn_and_stop_gracefully() {
        // The stub mimics the gateway: it handles SIGTERM (trap) and exits 0,
        // which is what `lele gateway --desktop` does on graceful shutdown.
        // `sleep` runs in the background with `wait` so the trap fires
        // immediately (a foreground command would delay trap execution).
        let stub = write_stub(
            "ready",
            "#!/bin/sh\necho 'LELE_READY {\"url\":\"http://127.0.0.1:59999\",\"port\":59999}'\ntrap 'exit 0' TERM\nsleep 30 &\nwait\n",
        );

        let mut handle = spawn_sidecar(&stub, "tok", Duration::from_secs(5))
            .expect("spawn should succeed");
        assert_eq!(handle.url(), "http://127.0.0.1:59999");
        assert_eq!(handle.port(), 59999);
        assert!(handle.pid() > 0);
        assert!(handle.is_alive());

        // Graceful stop within 2s (the stub traps SIGTERM and exits 0).
        let graceful = stop_sidecar(&mut handle, Duration::from_secs(2));
        assert!(graceful, "SIGTERM should terminate the stub gracefully");
        assert!(!handle.is_alive());

        let _ = fs::remove_file(&stub);
    }

    #[test]
    #[cfg(unix)]
    fn test_spawn_error_already_running() {
        let stub = write_stub(
            "already_running",
            "#!/bin/sh\necho 'LELE_ERROR {\"code\":\"already_running\",\"pid\":4242}'\nexit 1\n",
        );

        let err = spawn_sidecar(&stub, "tok", Duration::from_secs(5)).expect_err("should fail");
        match err {
            SidecarError::AlreadyRunning(pid) => assert_eq!(pid, 4242),
            other => panic!("expected AlreadyRunning, got {other:?}"),
        }

        let _ = fs::remove_file(&stub);
    }

    #[test]
    #[cfg(unix)]
    fn test_spawn_error_lock_failed() {
        let stub = write_stub(
            "lock_failed",
            "#!/bin/sh\necho 'LELE_ERROR {\"code\":\"lock_failed\",\"error\":\"pid file exists\"}'\nexit 1\n",
        );

        let err = spawn_sidecar(&stub, "tok", Duration::from_secs(5)).expect_err("should fail");
        match err {
            SidecarError::StartupFailed(msg) => {
                assert!(msg.contains("lock_failed"), "unexpected msg: {msg}")
            }
            other => panic!("expected StartupFailed, got {other:?}"),
        }

        let _ = fs::remove_file(&stub);
    }

    #[test]
    #[cfg(unix)]
    fn test_spawn_exits_before_ready() {
        let stub = write_stub("no_output", "#!/bin/sh\nexit 0\n");

        let err =
            spawn_sidecar(&stub, "tok", Duration::from_secs(5)).expect_err("should fail");
        match err {
            SidecarError::SpawnFailed(msg) => {
                assert!(msg.contains("exited"), "unexpected msg: {msg}")
            }
            other => panic!("expected SpawnFailed, got {other:?}"),
        }

        let _ = fs::remove_file(&stub);
    }
}