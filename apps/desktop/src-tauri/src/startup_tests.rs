use super::*;

#[test]
fn executable_paths_are_quoted_without_shell_or_desktop_field_expansion() {
    let path = Path::new("/Applications/Prism $HOME `touch bad` \\ \" &.AppImage");
    let entry = linux_entry(path).unwrap();
    assert!(entry.contains(
        r#"Exec="/Applications/Prism \\$HOME \\`touch bad\\` \\\\ \\" &.AppImage" --autostart"#
    ));
    assert_eq!(linux_state(entry.as_bytes(), path).unwrap(), (true, false));
    assert!(linux_entry(Path::new("/path/with=equals")).is_err());
    assert!(linux_entry(Path::new("/path/with%percent")).is_err());
    assert!(linux_entry(Path::new("/path/with\nnewline")).is_err());
    assert_eq!(
        windows_command(Path::new(r"C:\Program Files\Prism\prism-desktop.exe")).unwrap(),
        r#""C:\Program Files\Prism\prism-desktop.exe" --autostart"#
    );
    assert!(windows_command(Path::new(&format!("C:\\{}.exe", "a".repeat(260)))).is_err());
}

#[test]
fn macos_xml_round_trips_paths_and_disabled_registrations() {
    let path = Path::new("/Applications/Prism & Co.app/Contents/MacOS/prism-desktop");
    let bytes = macos_entry(path).unwrap();
    assert!(String::from_utf8_lossy(&bytes).contains("&amp;"));
    assert_eq!(macos_state(&bytes, path).unwrap(), (true, false));
    assert_eq!(
        macos_state(&bytes, Path::new("/new/Prism")).unwrap(),
        (true, true)
    );
    let mut value = plist::Value::from_reader(std::io::Cursor::new(bytes)).unwrap();
    value
        .as_dictionary_mut()
        .unwrap()
        .insert("Disabled".into(), true.into());
    let mut bytes = Vec::new();
    value.to_writer_xml(&mut bytes).unwrap();
    assert_eq!(macos_state(&bytes, path).unwrap(), (false, false));
}

#[test]
fn linux_disabled_custom_and_stale_entries_report_truthfully() {
    let path = Path::new("/opt/Prism/prism-desktop");
    let entry = linux_entry(path).unwrap();
    assert_eq!(
        linux_state(format!("{entry}Hidden=true\n").as_bytes(), path).unwrap(),
        (false, false)
    );
    assert_eq!(
        linux_state(
            format!("{entry}X-GNOME-Autostart-enabled=false\n").as_bytes(),
            path
        )
        .unwrap(),
        (false, false)
    );
    assert_eq!(
        linux_state(entry.as_bytes(), Path::new("/new/Prism")).unwrap(),
        (true, true)
    );
    assert!(linux_state(format!("{entry}OnlyShowIn=GNOME;\n").as_bytes(), path).is_err());
    assert!(linux_state(format!("{entry}Exec=evil\n").as_bytes(), path).is_err());
    assert!(linux_state(b"not an entry", path).is_err());
}

#[cfg(target_os = "linux")]
#[test]
fn registration_is_idempotent_and_external_disabling_survives_reopening() {
    let dir = tempfile::tempdir().unwrap();
    let executable = dir.path().join("Prism with spaces.AppImage");
    std::fs::write(&executable, "fixture").unwrap();
    let entry = dir
        .path()
        .join("xdg-config/autostart/dev.prism.gateway.desktop");
    let manager = Startup {
        executable: Ok(executable.clone()),
        entry: Ok(entry.clone()),
        mutation: Mutex::new(()),
    };
    assert_eq!(manager.status().enabled, Some(false));
    manager.write(true).unwrap();
    let original = std::fs::read(&entry).unwrap();
    manager.write(true).unwrap();
    assert_eq!(std::fs::read(&entry).unwrap(), original);
    assert_eq!(manager.status().enabled, Some(true));
    std::fs::write(
        &entry,
        format!("{}Hidden=true\n", String::from_utf8(original).unwrap()),
    )
    .unwrap();
    let reopened = Startup {
        executable: Ok(executable),
        entry: Ok(entry.clone()),
        mutation: Mutex::new(()),
    };
    assert_eq!(reopened.status().enabled, Some(false));
    assert!(std::fs::read_to_string(&entry)
        .unwrap()
        .contains("Hidden=true"));
    reopened.write(false).unwrap();
    reopened.write(false).unwrap();
    assert_eq!(reopened.status().enabled, Some(false));
}

#[cfg(target_os = "linux")]
#[test]
fn unreadable_registration_is_unknown_and_failed_changes_report_actual_state() {
    let dir = tempfile::tempdir().unwrap();
    let executable = dir.path().join("prism");
    std::fs::write(&executable, "fixture").unwrap();
    let entry = dir.path().join("entry");
    std::fs::create_dir(&entry).unwrap();
    let manager = Startup {
        executable: Ok(executable),
        entry: Ok(entry),
        mutation: Mutex::new(()),
    };
    assert_eq!(manager.status().enabled, None);
    assert!(manager.write(true).is_err());
    let failed = manager.set(false);
    assert_eq!(failed.enabled, None);
    assert!(failed.error.is_some());
}

#[test]
fn automatic_launch_marker_is_exact_and_manual_launches_remain_distinct() {
    assert!(is_automatic(["Prism", ARG]));
    assert!(!is_automatic(["Prism", "--autostart=false"]));
    assert!(!is_automatic(["Prism"]));
}

#[test]
fn windows_disabled_overrides_and_unknown_states_are_not_reported_as_enabled() {
    let mut bytes = [0; 12];
    for kind in [2, 6] {
        bytes[0] = kind;
        assert!(windows_approved(&bytes).unwrap());
    }
    for kind in [3, 7] {
        bytes[0] = kind;
        assert!(!windows_approved(&bytes).unwrap());
    }
    bytes[0] = 99;
    assert!(windows_approved(&bytes).is_err());
    assert!(windows_approved(&[2]).is_err());
}

#[cfg(target_os = "linux")]
#[test]
#[ignore = "requires the native GIO desktop launcher"]
fn gio_launches_the_quoted_executable_with_only_the_autostart_argument() {
    use std::os::unix::fs::PermissionsExt;
    let dir = tempfile::tempdir().unwrap();
    let executable = dir.path().join("Prism $HOME `x` \\ \" &.sh");
    std::fs::write(
        &executable,
        "#!/bin/sh\nprintf '%s' \"$#:$1\" > \"$0.result\"\n",
    )
    .unwrap();
    std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
    let entry = dir.path().join("prism.desktop");
    std::fs::write(&entry, linux_entry(&executable).unwrap()).unwrap();
    let output = std::process::Command::new("gio")
        .arg("launch")
        .arg(&entry)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let result = PathBuf::from(format!("{}.result", executable.display()));
    for _ in 0..100 {
        if result.exists() {
            break;
        }
        std::thread::sleep(std::time::Duration::from_millis(20));
    }
    assert_eq!(std::fs::read_to_string(result).unwrap(), "1:--autostart");
}
