//! Per-user login registration. OS state is authoritative; launches never enable it.
use serde::Serialize;
use std::{
    path::{Path, PathBuf},
    sync::Mutex,
};
use tauri::AppHandle;
#[cfg(not(windows))]
use tauri::Manager;

const ID: &str = "dev.prism.gateway";
pub const ARG: &str = "--autostart";
type Result<T> = std::result::Result<T, String>;

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct StartupStatus {
    pub enabled: Option<bool>,
    pub needs_repair: bool,
    pub can_enable: bool,
    pub error: Option<String>,
}

pub struct Startup {
    executable: Result<PathBuf>,
    #[cfg(not(windows))]
    entry: Result<PathBuf>,
    mutation: Mutex<()>,
}

pub fn is_automatic(args: impl IntoIterator<Item = impl AsRef<std::ffi::OsStr>>) -> bool {
    args.into_iter().any(|arg| arg.as_ref() == ARG)
}

impl Startup {
    pub fn new(app: &AppHandle) -> Self {
        let executable =
            std::env::current_exe().map_err(|_| "Cannot locate the installed executable.".into());
        #[cfg(target_os = "linux")]
        let executable = app
            .env()
            .appimage
            .map(PathBuf::from)
            .map(Ok)
            .unwrap_or(executable);
        #[cfg(target_os = "linux")]
        let entry = app
            .path()
            .config_dir()
            .map(|p| p.join("autostart").join(format!("{ID}.desktop")))
            .map_err(|_| "Cannot locate the startup directory.".into());
        #[cfg(target_os = "macos")]
        let entry = app
            .path()
            .home_dir()
            .map(|p| p.join("Library/LaunchAgents").join(format!("{ID}.plist")))
            .map_err(|_| "Cannot locate the login agent directory.".into());
        #[cfg(windows)]
        let _ = app;
        Self {
            executable,
            #[cfg(not(windows))]
            entry,
            mutation: Mutex::new(()),
        }
    }

    pub fn status(&self) -> StartupStatus {
        let _guard = self.mutation.lock().expect("startup lock poisoned");
        self.read_status()
    }

    fn read_status(&self) -> StartupStatus {
        let can_enable = !cfg!(debug_assertions);
        match self.read() {
            Ok((enabled, needs_repair)) => StartupStatus {
                enabled: Some(enabled),
                needs_repair,
                can_enable,
                error: None,
            },
            Err(error) => StartupStatus {
                enabled: None,
                needs_repair: false,
                can_enable,
                error: Some(error),
            },
        }
    }

    pub fn set(&self, enabled: bool) -> StartupStatus {
        let _guard = self.mutation.lock().expect("startup lock poisoned");
        let result = if enabled && cfg!(debug_assertions) {
            Err("Start at login is available in installed release builds.".into())
        } else {
            self.write(enabled)
        };
        let mut status = self.read_status();
        if let Err(error) = result {
            status.error = Some(error);
        } else if status.enabled != Some(enabled) || (enabled && status.needs_repair) {
            status.error = Some(
                "The OS did not confirm the startup change. Check its startup settings and retry."
                    .into(),
            );
        }
        status
    }

    fn executable(&self) -> Result<&Path> {
        let path = self.executable.as_ref().map_err(Clone::clone)?;
        if !path.is_absolute() || !path.is_file() {
            return Err("The installed executable is unavailable. Move Prism to a permanent location and try again.".into());
        }
        Ok(path)
    }

    #[cfg(not(windows))]
    fn read(&self) -> Result<(bool, bool)> {
        let entry = self.entry.as_ref().map_err(Clone::clone)?;
        let Some(bytes) = read_entry(entry)? else {
            return Ok((false, false));
        };
        #[cfg(target_os = "linux")]
        {
            linux_state(&bytes, self.executable()?)
        }
        #[cfg(target_os = "macos")]
        {
            let (enabled, repair) = macos_state(&bytes, self.executable()?)?;
            Ok((enabled && !macos_disabled()?, repair))
        }
    }

    #[cfg(not(windows))]
    fn write(&self, enabled: bool) -> Result<()> {
        let entry = self.entry.as_ref().map_err(Clone::clone)?;
        if !enabled {
            return remove_entry(entry);
        }
        #[cfg(target_os = "linux")]
        let bytes = linux_entry(self.executable()?)?.into_bytes();
        #[cfg(target_os = "macos")]
        let bytes = macos_entry(self.executable()?)?;
        prism_core::write_client_config(entry, &bytes).map_err(|_| {
            "Could not save the startup entry. Check directory permissions and retry.".to_string()
        })?;
        #[cfg(target_os = "macos")]
        macos_enable()?;
        Ok(())
    }

    #[cfg(windows)]
    fn read(&self) -> Result<(bool, bool)> {
        windows::read(&windows_command(self.executable()?)?)
    }

    #[cfg(windows)]
    fn write(&self, enabled: bool) -> Result<()> {
        let command = if enabled {
            Some(windows_command(self.executable()?)?)
        } else {
            None
        };
        windows::write(command.as_deref())
    }
}

#[cfg(not(windows))]
fn read_entry(path: &Path) -> Result<Option<Vec<u8>>> {
    use std::io::Read;
    let metadata = match std::fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => {
            return Err(
                "Cannot read the OS startup registration. Check permissions and retry.".into(),
            )
        }
    };
    if !metadata.is_file() || metadata.len() > 64 * 1024 {
        return Err(
            "The startup entry is not a supported regular file. Turn startup off to remove it."
                .into(),
        );
    }
    let mut bytes = Vec::new();
    std::fs::File::open(path)
        .and_then(|file| file.take(64 * 1024 + 1).read_to_end(&mut bytes))
        .map_err(|_| "Cannot read the OS startup registration.".to_string())?;
    if bytes.len() > 64 * 1024 {
        return Err("The startup entry is too large.".into());
    }
    Ok(Some(bytes))
}

#[cfg(not(windows))]
fn remove_entry(path: &Path) -> Result<()> {
    match std::fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => {
            Err("Could not remove the startup entry. Check directory permissions and retry.".into())
        }
    }
}

fn path_text(path: &Path) -> Result<&str> {
    path.to_str()
        .filter(|p| !p.is_empty() && !p.chars().any(char::is_control))
        .ok_or_else(|| "This installation path cannot be used for startup.".into())
}

#[cfg(any(target_os = "linux", test))]
fn linux_exec(path: &Path) -> Result<String> {
    let path = path_text(path)?;
    if path.contains(['=', '%']) {
        return Err(
            "Move Prism to a path without percent or equals signs before enabling startup.".into(),
        );
    }
    let mut quoted = String::from("\"");
    for c in path.chars() {
        match c {
            '\\' => quoted.push_str("\\\\\\\\"),
            '"' | '`' | '$' => {
                quoted.push_str("\\\\");
                quoted.push(c);
            }
            '%' => quoted.push_str("%%"),
            _ => quoted.push(c),
        }
    }
    Ok(format!("{quoted}\" {ARG}"))
}

#[cfg(any(target_os = "linux", test))]
fn linux_entry(path: &Path) -> Result<String> {
    Ok(format!("[Desktop Entry]\nType=Application\nVersion=1.0\nName=Prism\nExec={}\nTerminal=false\nStartupNotify=false\nX-Prism-Managed=true\n", linux_exec(path)?))
}

#[cfg(any(target_os = "linux", test))]
fn linux_state(bytes: &[u8], executable: &Path) -> Result<(bool, bool)> {
    let text = std::str::from_utf8(bytes)
        .map_err(|_| "The startup entry is not valid text.".to_string())?;
    let mut keys = std::collections::BTreeMap::new();
    let mut section = false;
    for line in text
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty() && !line.starts_with('#'))
    {
        if line.starts_with('[') {
            section = line == "[Desktop Entry]";
            continue;
        }
        if !section {
            continue;
        }
        let (key, value) = line
            .split_once('=')
            .ok_or_else(|| "The startup entry is malformed.".to_string())?;
        if keys.insert(key.trim(), value.trim()).is_some() {
            return Err("The startup entry contains conflicting values.".into());
        }
    }
    if keys.get("Hidden") == Some(&"true")
        || keys.get("X-GNOME-Autostart-enabled") == Some(&"false")
    {
        return Ok((false, false));
    }
    if keys.get("Type") != Some(&"Application")
        || !keys.contains_key("Exec")
        || ["OnlyShowIn", "NotShowIn", "TryExec"]
            .iter()
            .any(|key| keys.contains_key(key))
    {
        return Err("The startup entry has custom restrictions. Check OS startup settings, or turn it off and enable it again.".into());
    }
    Ok((
        true,
        keys.get("Exec").copied() != Some(linux_exec(executable)?.as_str()),
    ))
}

#[cfg(any(target_os = "macos", test))]
fn macos_entry(path: &Path) -> Result<Vec<u8>> {
    let mut dictionary = plist::Dictionary::new();
    dictionary.insert("Label".into(), ID.into());
    dictionary.insert(
        "ProgramArguments".into(),
        plist::Value::Array(vec![path_text(path)?.into(), ARG.into()]),
    );
    dictionary.insert("RunAtLoad".into(), true.into());
    dictionary.insert("LimitLoadToSessionType".into(), "Aqua".into());
    let mut bytes = Vec::new();
    plist::Value::Dictionary(dictionary)
        .to_writer_xml(&mut bytes)
        .map_err(|_| "Could not build the login agent.".to_string())?;
    Ok(bytes)
}

#[cfg(any(target_os = "macos", test))]
fn macos_state(bytes: &[u8], path: &Path) -> Result<(bool, bool)> {
    let value = plist::Value::from_reader(std::io::Cursor::new(bytes)).map_err(|_| {
        "The login agent is malformed. Turn startup off and enable it again.".to_string()
    })?;
    let dictionary = value
        .as_dictionary()
        .ok_or_else(|| "The login agent is malformed.".to_string())?;
    if dictionary.get("Label").and_then(plist::Value::as_string) != Some(ID) {
        return Err("The login agent has an unexpected label.".into());
    }
    if dictionary
        .get("Disabled")
        .and_then(plist::Value::as_boolean)
        == Some(true)
        || dictionary
            .get("RunAtLoad")
            .and_then(plist::Value::as_boolean)
            != Some(true)
    {
        return Ok((false, false));
    }
    let expected = plist::Value::Array(vec![path_text(path)?.into(), ARG.into()]);
    Ok((true, dictionary.get("ProgramArguments") != Some(&expected)))
}

#[cfg(target_os = "macos")]
fn launchctl(args: &[&str]) -> Result<String> {
    use std::{
        io::Read,
        process::{Command, Stdio},
        time::{Duration, Instant},
    };
    let mut child = Command::new("/bin/launchctl")
        .args(args)
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|_| "Cannot reach the login service.".to_string())?;
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        if let Some(status) = child
            .try_wait()
            .map_err(|_| "Cannot read login service status.".to_string())?
        {
            if !status.success() {
                return Err(
                    "The login service rejected the change. Check System Settings and retry."
                        .into(),
                );
            }
            let mut output = String::new();
            child
                .stdout
                .take()
                .unwrap()
                .take(64 * 1024)
                .read_to_string(&mut output)
                .map_err(|_| "Cannot read login service status.".to_string())?;
            return Ok(output);
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            return Err(
                "The login service did not respond. Retry after login has finished.".into(),
            );
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

#[cfg(target_os = "macos")]
fn macos_disabled() -> Result<bool> {
    let domain = format!("gui/{}", unsafe { libc::geteuid() });
    let output = launchctl(&["print-disabled", &domain])?;
    macos_disabled_state(&output)
}

/// launchctl changed booleans to enabled/disabled in Ventura. Its human-readable output
/// is not a stable API: reject unfamiliar or incomplete output rather than assume enabled.
#[cfg(any(target_os = "macos", test))]
fn macos_disabled_state(output: &str) -> Result<bool> {
    let invalid = || {
        "The login service returned an unrecognized startup state. Check System Settings and retry."
            .to_string()
    };
    let mut lines = output
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty());
    if lines.next() != Some("disabled services = {") {
        return Err(invalid());
    }
    let mut disabled = None;
    while let Some(line) = lines.next() {
        if line == "}" {
            return if lines.next().is_none() {
                Ok(disabled.unwrap_or(false))
            } else {
                Err(invalid())
            };
        }
        let (label, value) = line.split_once("=>").ok_or_else(invalid)?;
        let label = label
            .trim()
            .strip_prefix('"')
            .and_then(|s| s.strip_suffix('"'))
            .filter(|s| !s.is_empty())
            .ok_or_else(invalid)?;
        let value = match value.trim().trim_end_matches([';', ',']).trim() {
            "true" | "disabled" => true,
            "false" | "enabled" => false,
            _ => return Err(invalid()),
        };
        if label == ID && disabled.replace(value).is_some() {
            return Err(invalid());
        }
    }
    Err(invalid())
}

#[cfg(target_os = "macos")]
fn macos_enable() -> Result<()> {
    let service = format!("gui/{}/{ID}", unsafe { libc::geteuid() });
    launchctl(&["enable", &service]).map(|_| ())
}

#[cfg(any(windows, test))]
fn windows_command(path: &Path) -> Result<String> {
    let path = path_text(path)?;
    if path.contains('"') {
        return Err("The executable path contains a quote.".into());
    }
    let command = format!("\"{path}\" {ARG}");
    if command.encode_utf16().count() > 260 {
        return Err("Move Prism to a shorter path before enabling startup.".into());
    }
    Ok(command)
}

#[cfg(any(windows, test))]
fn windows_approved(bytes: &[u8]) -> Result<bool> {
    if bytes.len() < 12 {
        return Err("Windows reports an incomplete startup approval state.".into());
    }
    match bytes[0] {
        2 | 6 => Ok(true),
        3 | 7 => Ok(false),
        _ => Err("Windows reports an unknown startup approval state.".into()),
    }
}

#[cfg(windows)]
mod windows {
    use super::*;
    use winreg::{
        enums::{HKEY_CURRENT_USER, KEY_READ, REG_BINARY},
        RegKey, RegValue,
    };
    const RUN: &str = "Software\\Microsoft\\Windows\\CurrentVersion\\Run";
    const APPROVED: &str =
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\StartupApproved\\Run";
    fn error(_: std::io::Error) -> String {
        "Cannot access Windows startup settings. Check permissions and retry.".into()
    }
    fn optional<T>(value: std::io::Result<T>) -> Result<Option<T>> {
        match value {
            Ok(value) => Ok(Some(value)),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(error(e)),
        }
    }
    pub fn read(expected: &str) -> Result<(bool, bool)> {
        let user = RegKey::predef(HKEY_CURRENT_USER);
        let Some(run) = optional(user.open_subkey_with_flags(RUN, KEY_READ))? else {
            return Ok((false, false));
        };
        let Some(command) = optional(run.get_value::<String, _>(ID))? else {
            return Ok((false, false));
        };
        if let Some(approved) = optional(user.open_subkey_with_flags(APPROVED, KEY_READ))? {
            if let Some(value) = optional(approved.get_raw_value(ID))? {
                if value.vtype != REG_BINARY {
                    return Err("Windows reports an invalid startup approval value.".into());
                }
                if !windows_approved(&value.bytes)? {
                    return Ok((false, false));
                }
            }
        }
        Ok((true, command != expected))
    }
    pub fn write(command: Option<&str>) -> Result<()> {
        let user = RegKey::predef(HKEY_CURRENT_USER);
        if let Some(command) = command {
            let (run, _) = user.create_subkey(RUN).map_err(error)?;
            run.set_value(ID, &command).map_err(error)?;
            let (approved, _) = user.create_subkey(APPROVED).map_err(error)?;
            let mut bytes = vec![0; 12];
            bytes[0] = 2;
            approved
                .set_raw_value(
                    ID,
                    &RegValue {
                        vtype: REG_BINARY,
                        bytes,
                    },
                )
                .map_err(error)?;
        } else {
            if let Some(run) =
                optional(user.open_subkey_with_flags(RUN, winreg::enums::KEY_SET_VALUE))?
            {
                optional(run.delete_value(ID))?;
            }
            if let Some(approved) =
                optional(user.open_subkey_with_flags(APPROVED, winreg::enums::KEY_SET_VALUE))?
            {
                optional(approved.delete_value(ID))?;
            }
        }
        Ok(())
    }
}

#[cfg(test)]
#[path = "startup_tests.rs"]
mod tests;
