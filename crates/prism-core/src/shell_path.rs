//! Adopt the login shell's PATH when the app was launched from a desktop menu.
//!
//! Desktop launchers on macOS and Linux start apps with the session PATH, which lacks whatever the
//! user's shell profile adds (`~/go/bin`, Homebrew, cargo, pnpm). Servers are resolved by name
//! against that PATH, so `ketch` works from a terminal and fails from the menu. This asks the
//! user's shell for its PATH once at startup and merges it in front of the inherited one.

use std::ffi::OsString;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::Duration;

/// Sentinel around the shell's answer so rc-file chatter cannot be mistaken for a path.
const MARK: &str = "\u{1f}";

/// Replace this process's PATH with the login shell's PATH merged in front of it. No-op on
/// Windows, where the installer PATH is the real one, and when the shell cannot be asked.
pub fn adopt_login_shell_path() {
    if cfg!(windows) {
        return;
    }
    let Some(shell) = std::env::var_os("SHELL").filter(|s| !s.is_empty()) else {
        return;
    };
    let Some(shell_path) = query_shell_path(Path::new(&shell)) else {
        return;
    };
    let current = std::env::var_os("PATH").unwrap_or_default();
    let merged = merge_paths(&shell_path, &current);
    if merged != current {
        tracing::info!(shell = %shell.to_string_lossy(), "adopted the login shell PATH");
        // The gateway has not started and no server has been spawned yet, so this is the only
        // thread reading the environment.
        std::env::set_var("PATH", merged);
    }
}

fn query_shell_path(shell: &Path) -> Option<String> {
    let script = format!("printf '%s%s%s' '{MARK}' \"$PATH\" '{MARK}'");
    let mut child = Command::new(shell)
        .args(["-ilc", &script])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    let mut stdout = child.stdout.take()?;
    let reader = std::thread::spawn(move || {
        let mut buf = Vec::new();
        std::io::Read::read_to_end(&mut stdout, &mut buf).ok();
        buf
    });
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    loop {
        match child.try_wait() {
            Ok(Some(_)) => break,
            Ok(None) if std::time::Instant::now() < deadline => {
                std::thread::sleep(Duration::from_millis(25));
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                tracing::warn!("login shell did not report PATH in time; keeping the session PATH");
                return None;
            }
        }
    }
    let output = String::from_utf8_lossy(&reader.join().ok()?).into_owned();
    extract(&output)
}

fn extract(output: &str) -> Option<String> {
    let start = output.find(MARK)? + MARK.len();
    let end = output[start..].find(MARK)? + start;
    let path = output[start..end].trim();
    (!path.is_empty()).then(|| path.to_string())
}

/// Shell entries first, then whatever the session had that the shell did not, without duplicates.
fn merge_paths(shell: &str, current: &std::ffi::OsStr) -> OsString {
    let current = current.to_string_lossy();
    let mut seen = std::collections::HashSet::new();
    let mut out: Vec<&str> = Vec::new();
    for entry in shell.split(':').chain(current.split(':')) {
        if !entry.is_empty() && seen.insert(entry) {
            out.push(entry);
        }
    }
    OsString::from(out.join(":"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_between_marks_and_ignores_rc_chatter() {
        let out = format!("Welcome!\n{MARK}/a:/b{MARK}\nbye\n");
        assert_eq!(extract(&out).as_deref(), Some("/a:/b"));
        assert_eq!(extract("no marks"), None);
        assert_eq!(extract(&format!("{MARK}{MARK}")), None);
    }

    /// Talks to the real login shell. `cargo test -p prism-core login_shell -- --ignored`.
    #[test]
    #[ignore]
    #[cfg(unix)]
    fn login_shell_reports_a_path() {
        let shell = std::env::var_os("SHELL").expect("SHELL set");
        let path = query_shell_path(Path::new(&shell)).expect("shell answered");
        assert!(path.contains("/bin"), "{path}");
    }

    #[test]
    fn merge_prefers_shell_order_and_dedupes() {
        let merged = merge_paths(
            "/home/u/go/bin:/usr/bin",
            std::ffi::OsStr::new("/usr/bin:/bin:"),
        );
        assert_eq!(merged, OsString::from("/home/u/go/bin:/usr/bin:/bin"));
    }
}
