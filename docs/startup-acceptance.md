# Startup acceptance

Task-76 stays open until this checklist passes on installed macOS, Windows and Linux builds. CI compiles the native code, runs automated tests and produces installers. Use a desktop session on each OS for the login and reboot checks below.

## Build to test

Use the **preview build** workflow, selecting the test branch and one of `windows`, `macos-arm64`, `macos-x86_64` or `linux`. Download the `prism-preview-<target>-<commit>` artifact from that run; artifacts expire after seven days. The run must contain startup implementation `9cec00c` and its subsequent fixes. Record the exact commit, not just the displayed application version: previews can share a version with a published release.

Install the Windows setup executable or MSI in a permanent location. On macOS, copy Prism from the DMG into Applications before launching it. On Linux, install the package or put the AppImage in a permanent path and make it executable. Test the package format you use; record which one it is. Use an installed release build, since development builds intentionally cannot enable startup.

Before replacing an existing installation, quit Prism and retain a copy of its profile configuration. Server secrets live in the OS credential store and are not included in that file. Use a test profile/user if resetting startup state would disturb your normal setup. Do not remove the existing installation between the two upgrade checks below.

## Desktop checks

Run these on the Windows VM, Mac mini and a Linux desktop. Record pass/fail and any exact error for every row.

| Check | Steps | Expected result |
| --- | --- | --- |
| Initial state | With no prior Prism startup entry, launch the installed app and open Settings. | Start at login is off. Opening Settings does not enable it. |
| Enable | Turn Start at login on, close and reopen Settings, then inspect the OS entry below. | The switch and OS registration both show enabled, with the installed executable and `--autostart`. |
| Login | Quit Prism, log out and sign in again. | Exactly one Prism instance runs in the tray. No panel or browser opens solely because Prism launched. |
| Reboot | Reboot, then sign in. | The same single, quiet startup occurs. |
| Gateway | After automatic startup, connect a configured client and list tools from an enabled server. | The configured endpoint works with existing credentials and permissions. A pending approval still follows its configured attention setting. |
| Manual relaunch | Launch Prism again while its automatic instance is running. | The existing panel opens; no duplicate gateway or port-conflict notice appears. |
| Disable | Turn startup off, reopen Settings, then log out/in and repeat after a reboot. | The switch stays off and Prism stays stopped. |
| OS override | Enable in Prism, disable the entry using the OS startup controls where available, and reopen/focus Settings. | Settings reads the disabled state. Merely opening or updating Prism does not re-enable it. Explicitly enabling in Prism can enable it again. |
| Repeated changes | Toggle on/off several times and inspect the OS entry. | At most one owned entry exists; disabling leaves no active entry. |
| Upgrade while off | Disable startup, replace the installation with the next candidate through the same package path, launch manually and log out/in. | Startup stays off; no stale active entry is added. Record both commits and package versions. |
| Upgrade while on | Enable startup, replace the installation with the next candidate through the same package path, and log out/in. | One instance of the replacement starts quietly. If its path changed, Settings reports Repair instead of claiming the old target is correct. |
| Credential recovery | In a test account where supported, start with the credential store locked or unavailable, then unlock it. | The tray and Settings remain usable. A gateway startup failure offers Retry; an affected server offers recovery/restart. Recovery restores tool access without adding the server again. If this condition cannot be induced, record it as untested. |
| Removal | Turn startup off before uninstalling or removing the test app. | No active Prism startup entry remains. |

## OS registration

| OS | Entry and inspection |
| --- | --- |
| Windows | Current-user registry value `dev.prism.gateway` under `Software\Microsoft\Windows\CurrentVersion\Run`; Task Manager → Startup apps supplies the enabled/disabled override. The Run command quotes the installed executable and appends `--autostart`. |
| macOS | `~/Library/LaunchAgents/dev.prism.gateway.plist`. Inspect with `plutil -p` and `launchctl print-disabled gui/$(id -u)`; the latter reports a persisted disabled override. ProgramArguments should identify the installed app executable plus `--autostart`. |
| Linux | `$XDG_CONFIG_HOME/autostart/dev.prism.gateway.desktop`, or `~/.config/autostart/dev.prism.gateway.desktop` when XDG_CONFIG_HOME is unset. Inspect the desktop's Startup Applications UI if available. An AppImage entry must reference its permanent AppImage path. |

Settings can report an unknown state with an error when an entry is malformed or inaccessible. Record that error; an unknown state must not appear as a confirmed off switch. Custom Linux `OnlyShowIn`, `NotShowIn` or `TryExec` restrictions are reported as unknown instead of guessing whether startup will happen.

## Result to record in Brainfile

```text
Task: task-76
Tester / date:
OS version / architecture:
Package type / installation path:
Preview run URL / exact commit:
Previous commit + package version for upgrade checks:
Initial state:
Enable / repeated changes:
Login / reboot:
Quiet tray / one process / manual relaunch:
Gateway and tool listing:
Disable / OS override:
Upgrade off / upgrade on:
Credential recovery:
Removal:
Errors, untested cases, supporting screenshots:
```

Keep tokens, headers and raw provider responses out of the result. Task-74 has an additional requirement: reproduce the original Windows configuration/startup report from GitHub #4 or establish its verified cause. A passing startup checklist alone does not establish that cause.
