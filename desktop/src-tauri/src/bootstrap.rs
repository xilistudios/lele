//! Desktop token bootstrap: generate once, persist in the OS keychain.
//!
//! The desktop shell and the gateway share an auth token so that the web UI
//! served by the gateway can trust requests originating from the desktop
//! window. The token is generated on first run and stored in the system
//! keychain; it is reused on subsequent launches.

use anyhow::{Context, Result};

/// Keychain service name for the desktop token entry.
const SERVICE: &str = "lele-desktop";
/// Keychain account (user) name for the desktop token entry.
const USER: &str = "gateway-token";

/// Return the persisted desktop token, generating and storing a fresh
/// 256-bit random token (hex-encoded, 64 chars) on first run.
///
/// Falls back to regenerating if the keychain entry is missing or corrupted.
/// The token is 64 hex characters (32 random bytes).
pub fn get_or_create_token() -> Result<String> {
    let entry = keyring::Entry::new(SERVICE, USER).context("failed to open keychain entry")?;

    // keyring v3 returns `Err(NoEntry)` when the entry does not exist; on any
    // error (missing or corrupt) we simply regenerate a fresh token.
    if let Ok(existing) = entry.get_password() {
        if !existing.is_empty() {
            return Ok(existing);
        }
    }

    let token = generate_token();
    entry
        .set_password(&token)
        .context("failed to persist desktop token in keychain")?;
    Ok(token)
}

/// Generate a 256-bit random token hex-encoded as 64 lowercase hex chars.
fn generate_token() -> String {
    use rand::RngCore;
    let mut bytes = [0u8; 32];
    rand::thread_rng().fill_bytes(&mut bytes);
    hex::encode(bytes)
}

/// Lele state directory (`~/.lele`), respecting the `LELE_CONFIG_DIR`
/// override so the desktop app and CLI agree on where state lives.
pub fn data_dir() -> std::path::PathBuf {
    if let Ok(dir) = std::env::var("LELE_CONFIG_DIR") {
        if !dir.is_empty() {
            return std::path::PathBuf::from(dir);
        }
    }
    dirs::home_dir()
        .unwrap_or_else(|| std::path::PathBuf::from("."))
        .join(".lele")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_generate_token_length_and_hex() {
        let token = generate_token();
        assert_eq!(token.len(), 64, "token must be 64 hex chars");
        assert!(
            token.bytes().all(|b| b.is_ascii_hexdigit()),
            "token must contain only hex chars"
        );
    }

    #[test]
    fn test_generate_token_is_random() {
        let a = generate_token();
        let b = generate_token();
        assert_ne!(a, b, "two generated tokens must differ");
    }

    // NOTE: both env-var tests are combined into one because tests run in
    // parallel threads and `LELE_CONFIG_DIR` is process-global state.
    #[test]
    fn test_data_dir_env_handling() {
        // Override wins when set.
        std::env::set_var("LELE_CONFIG_DIR", "/tmp/lele-test-config");
        let dir = data_dir();
        assert_eq!(dir, std::path::PathBuf::from("/tmp/lele-test-config"));

        // Falls back to ~/.lele when unset.
        std::env::remove_var("LELE_CONFIG_DIR");
        let dir = data_dir();
        assert_eq!(dir.file_name().and_then(|n| n.to_str()), Some(".lele"));
    }
}