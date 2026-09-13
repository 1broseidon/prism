use std::{path::PathBuf, process::ExitCode};

fn main() -> ExitCode {
    let args: Vec<_> = std::env::args_os().skip(1).collect();
    if args.len() == 1 && matches!(args[0].to_str(), Some("--help" | "-h")) {
        println!("Usage: prism-provision apply MANIFEST --config PROFILE/prism.json\nQuit Prism first. Credential environment variables resolve into the OS credential store.");
        return ExitCode::SUCCESS;
    }
    if args.len() != 4 || args[0] != "apply" || args[2] != "--config" {
        eprintln!("Usage: prism-provision apply MANIFEST --config PROFILE/prism.json");
        return ExitCode::from(2);
    }
    let manifest = PathBuf::from(&args[1]);
    let config = PathBuf::from(&args[3]);
    match prism_core::provision::ProvisionManifest::read(&manifest)
        .and_then(|manifest| prism_core::provision::apply_manifest(&config, manifest))
    {
        Ok(report) => {
            println!(
                "{}",
                serde_json::to_string(&report).expect("report serializes")
            );
            ExitCode::SUCCESS
        }
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}
