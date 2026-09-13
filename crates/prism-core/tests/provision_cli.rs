use std::process::Command;

#[test]
fn cli_is_usable_without_a_display_and_rejects_bad_inputs_without_echoing_them() {
    let binary = env!("CARGO_BIN_EXE_prism-provision");
    let help = Command::new(binary)
        .arg("--help")
        .env_remove("DISPLAY")
        .output()
        .unwrap();
    assert!(help.status.success());
    assert!(String::from_utf8_lossy(&help.stdout).contains("--config"));
    let dir = tempfile::tempdir().unwrap();
    let manifest = dir.path().join("servers.json");
    std::fs::write(
        &manifest,
        r#"{"version":1,"servers":[{"id":"bad","auth":"secret-never-echo"}]}"#,
    )
    .unwrap();
    let failed = Command::new(binary)
        .arg("apply")
        .arg(manifest)
        .arg("--config")
        .arg(dir.path().join("profile/prism.json"))
        .output()
        .unwrap();
    assert!(!failed.status.success());
    assert!(!String::from_utf8_lossy(&failed.stderr).contains("secret-never-echo"));
    assert!(!dir.path().join("profile/prism.json").exists());
}

#[tokio::test]
async fn another_process_cannot_apply_to_a_running_profile() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    prism_core::PrismConfig {
        listen_port: 0,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    let gateway = prism_core::Gateway::start(&path, dir.path().join("audit.jsonl"))
        .await
        .unwrap();
    let manifest = dir.path().join("servers.json");
    std::fs::write(&manifest, r#"{"version":1,"servers":[]}"#).unwrap();
    let before = std::fs::read(&path).unwrap();
    let failed = Command::new(env!("CARGO_BIN_EXE_prism-provision"))
        .arg("apply")
        .arg(manifest)
        .arg("--config")
        .arg(&path)
        .output()
        .unwrap();
    assert!(!failed.status.success());
    assert!(String::from_utf8_lossy(&failed.stderr).contains("profile is in use"));
    assert_eq!(std::fs::read(path).unwrap(), before);
    gateway.shutdown().await;
}
