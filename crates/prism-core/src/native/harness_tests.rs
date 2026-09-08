use super::*;
use crate::audit::AuditVerdict;
use serde_json::json;

fn payload(host: &str, tool: &str, input: Value) -> Value {
    match host {
        HOST_CURSOR => json!({
            "hook_event_name":"preToolUse", "conversation_id":"cursor-session",
            "generation_id":"generation", "cursor_version":"2026.09.02",
            "workspace_roots":["/home/u/proj", "/home/u/other"], "cwd":"/home/u/proj",
            "tool_use_id":"call-1", "tool_name":tool, "tool_input":input
        }),
        HOST_OPENCODE => json!({
            "hook_event_name":"PreToolUse", "session_id":"opencode-session",
            "cwd":"/home/u/proj", "tool_use_id":"call-1", "tool_name":tool, "tool_input":input
        }),
        HOST_GOOSE => json!({
            "event":"PreToolUseResult", "session_id":"goose-session",
            "working_dir":"/home/u/proj", "tool_call_id":"call-1", "decision":"allow",
            "policy_evaluated":false, "matcher_context":tool, "tool_name":tool, "tool_input":input
        }),
        HOST_ANTIGRAVITY => json!({
            "hook_event_name":"PostToolUse", "payload":{
                "toolCall":{"name":tool,"args":input}, "stepIdx":19,
                "conversationId":"ec33ebf9-0cba-4100-8142-c61503f6c587",
                "workspacePaths":["/home/u/proj", "/home/u/other"],
                "transcriptPath":"/private/transcript.jsonl", "modelName":"gemini-3.6-flash-medium"
            }
        }),
        _ => json!({
            "hook_event_name":"PreToolUse", "session_id":"legacy-session",
            "cwd":"/home/u/proj", "tool_use_id":"call-1", "tool_name":tool, "tool_input":input
        }),
    }
}

fn parse(host: &str, value: Value) -> Observation {
    parse_hook(host, value).unwrap().unwrap()
}

fn describe(observation: &Observation) -> (String, Option<&'static str>) {
    let tool = observation.policy_tool();
    let input = &observation.event.tool_input;
    let cwd = observation.event.cwd.as_deref().map(Path::new);
    let home = Some(Path::new("/home/u"));
    (
        subject(tool, input, cwd, home),
        shadow::evaluate(tool, input, cwd, home),
    )
}

#[test]
fn registry_uses_narrow_new_client_aliases_and_preserves_legacy_grouping() {
    for (name, host) in [
        ("Claude Code 2.1", HOST_CLAUDE_CODE),
        ("claude-code", HOST_CLAUDE_CODE),
        ("Codex CLI", HOST_CODEX),
        ("Cursor", HOST_CURSOR),
        ("cursor", HOST_CURSOR),
        ("OpenCode", HOST_OPENCODE),
        ("goose", HOST_GOOSE),
        ("goose-cli", HOST_GOOSE),
        ("goose-desktop", HOST_GOOSE),
        ("Antigravity", HOST_ANTIGRAVITY),
    ] {
        assert_eq!(harness_for_client_name(name), Some(host), "{name}");
        assert!(HOSTS.contains(&host));
        assert_eq!(harness_agent_id(host, None), format!("host:{host}"));
        assert_eq!(
            harness_agent_id(host, Some("remote")),
            format!("host:{host}@remote")
        );
    }
    for name in [
        "Cursorish",
        "Cursor Helper",
        "cur-sor",
        "OpenCode Agent",
        "gooseberry",
        "Goose Desktop Helper",
        "antigravity-proxy",
        "Gemini CLI",
        "gemini",
        "agy",
    ] {
        assert_eq!(harness_for_client_name(name), None, "{name}");
    }
    assert_eq!(harness_display_name(HOST_OPENCODE), "OpenCode");
    assert_eq!(harness_display_name(HOST_ANTIGRAVITY), "Antigravity");
    assert_eq!(HOSTS.len(), 6);
}

#[test]
fn goose_149_live_shell_fixture_uses_unprefixed_native_name() {
    // Reduced from the real Goose 1.49.0 local-model probe on 2026-09-08. The generated
    // observer delivered one result; this harmless command ran and its marker reached the
    // next model request. Session/temp-home values are replaced for a portable fixture.
    // Reproducer: apps/desktop/src-tauri/src/fixtures/harness_extra/goose-live.py.
    let body: Value = serde_json::from_str(include_str!("fixtures/goose-1.49-shell.json")).unwrap();
    let observation = parse(HOST_GOOSE, body.clone());
    assert_eq!(observation.event.tool_name, "shell");
    assert_eq!(observation.policy_tool(), "Bash");
    assert_eq!(
        observation.event.session_id.as_deref(),
        Some("goose-fixture-session")
    );
    assert_eq!(observation.call_id.as_deref(), Some("fixture-shell-call"));
    assert_eq!(observation.event.cwd.as_deref(), Some("/fixture/work"));
    assert!(matches!(observation.verdict, AuditVerdict::Allowed));
    assert_eq!(
        observation.event.tool_input,
        json!({"command":"printf PRISM_GOOSE_OBSERVER_OK"})
    );
    assert_eq!(
        describe(&observation),
        ("printf PRISM_GOOSE_OBSERVER_OK".into(), None)
    );
    assert!(!observation.matches_prism_tool(HOST_GOOSE, "developer", "shell"));
    let mut budget = EventBudget::default();
    assert!(budget.admit_event(HOST_GOOSE, &observation));
    assert!(!budget.admit_event(HOST_GOOSE, &parse(HOST_GOOSE, body.clone())));

    // A shadow hit remains meaningful for the same live native name, even when no Goose
    // policy hook evaluated the call. The observer's result is still the host's allow.
    let mut watched = body;
    watched["tool_input"]["command"] = json!("sudo printf PRISM_GOOSE_OBSERVER_OK");
    let watched = parse(HOST_GOOSE, watched);
    assert_eq!(describe(&watched).1, Some("sudo"));
    assert!(matches!(watched.verdict, AuditVerdict::Allowed));
}

#[test]
fn goose_149_live_advertised_file_schemas_use_unprefixed_names() {
    // These schemas were advertised in the SAME real model request as the executed shell
    // fixture. The test synthesizes proposals from those schemas; it does not claim the
    // live probe executed file operations. The excerpt is copied from the adapter's saved
    // fixtures/harness_extra/goose-1.49-tool-catalog.json, including all advertised names.
    let catalog: Value =
        serde_json::from_str(include_str!("fixtures/goose-1.49-developer-catalog.json")).unwrap();
    for (tool, required, args, expected_subject, reason) in [
        (
            "write",
            vec!["path", "content"],
            json!({"path":"/home/u/.zshrc", "content":"PRIVATE CONTENT"}),
            "~/.zshrc",
            Some("sensitive_write"),
        ),
        (
            "edit",
            vec!["path", "before", "after"],
            json!({"path":"../other/a.rs", "before":"PRIVATE BEFORE", "after":"PRIVATE AFTER"}),
            "../other/a.rs",
            Some("write_outside_cwd"),
        ),
        (
            "tree",
            vec!["path"],
            json!({"path":"/home/u/proj/src", "depth":2}),
            "src",
            None,
        ),
    ] {
        let schema = catalog["tools"]
            .as_array()
            .unwrap()
            .iter()
            .find(|entry| entry["name"] == tool)
            .unwrap();
        assert_eq!(schema["parameters"]["required"], json!(required));
        for key in required {
            assert_eq!(schema["parameters"]["properties"][key]["type"], "string");
            assert!(args.get(key).is_some());
        }
        let observation = parse(HOST_GOOSE, payload(HOST_GOOSE, tool, args));
        assert_eq!(observation.event.tool_name, tool);
        assert_eq!(
            observation.policy_tool(),
            if tool == "tree" { "Grep" } else { "Write" }
        );
        assert_eq!(describe(&observation), (expected_subject.into(), reason));
        assert!(!observation.event.tool_input.to_string().contains("PRIVATE"));
        assert!(!observation.matches_prism_tool(HOST_GOOSE, "developer", tool));
    }
    for unknown in [
        "text_editor",
        "file_edit",
        "read_file",
        "edit_file",
        "other__write",
        "other__edit",
    ] {
        assert!(!catalog["advertised_names"]
            .as_array()
            .unwrap()
            .contains(&json!(unknown)));
        let observation = parse(
            HOST_GOOSE,
            payload(
                HOST_GOOSE,
                unknown,
                json!({"path":"/home/u/.zshrc", "command":"view", "content":"PRIVATE"}),
            ),
        );
        assert!(observation.normalized_tool.is_none());
        assert_eq!(observation.event.tool_input, json!({}));
        assert_eq!(describe(&observation), (unknown.into(), None));
    }
}

#[test]
fn official_shell_envelopes_normalize_without_losing_host_tool_identity() {
    for (host, tool, input) in [
        (HOST_CLAUDE_CODE, "Bash", json!({"command":"sudo ls"})),
        (HOST_CODEX, "Bash", json!({"command":["sudo", "ls"]})),
        (
            HOST_CURSOR,
            "Shell",
            json!({"command":"sudo ls","working_directory":"/home/u/proj"}),
        ),
        (
            HOST_OPENCODE,
            "bash",
            json!({"command":"sudo ls", "description":"PRIVATE"}),
        ),
        (HOST_GOOSE, "developer__shell", json!({"command":"sudo ls"})),
        (HOST_GOOSE, "shell", json!({"command":"sudo ls"})),
        (
            HOST_ANTIGRAVITY,
            "run_command",
            json!({"CommandLine":"sudo ls", "Cwd":"/home/u/proj", "WaitMsBeforeAsync":5000}),
        ),
    ] {
        let observation = parse(host, payload(host, tool, input));
        assert_eq!(observation.event.tool_name, tool);
        assert_eq!(observation.event.cwd.as_deref(), Some("/home/u/proj"));
        assert_eq!(
            describe(&observation),
            ("sudo ls".into(), Some("sudo")),
            "{host}"
        );
        assert_eq!(observation.event.tool_input, json!({"command":"sudo ls"}));
    }
}

#[test]
fn verified_file_arguments_drop_all_contents_and_keep_shadow_rules() {
    for (host, tool, input, reason) in [
        (
            HOST_CURSOR,
            "Read",
            json!({"file_path":"/home/u/proj/.env", "content":"SECRET"}),
            "secret_read",
        ),
        (
            HOST_CURSOR,
            "Write",
            json!({"file_path":"/home/u/.zshrc", "content":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_OPENCODE,
            "read",
            json!({"filePath":"/home/u/proj/.env", "offset":1}),
            "secret_read",
        ),
        (
            HOST_OPENCODE,
            "write",
            json!({"filePath":"/home/u/.zshrc", "content":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_OPENCODE,
            "edit",
            json!({"filePath":"/home/u/.zshrc", "oldString":"SECRET", "newString":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_GOOSE,
            "developer__write",
            json!({"path":"/home/u/.zshrc", "content":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_GOOSE,
            "developer__edit",
            json!({"path":"/home/u/.zshrc", "before":"SECRET", "after":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_ANTIGRAVITY,
            "view_file",
            json!({"AbsolutePath":"/home/u/proj/.env"}),
            "secret_read",
        ),
        (
            HOST_ANTIGRAVITY,
            "write_to_file",
            json!({"TargetFile":"/home/u/.zshrc", "CodeContent":"SECRET", "Description":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_ANTIGRAVITY,
            "replace_file_content",
            json!({"TargetFile":"/home/u/.zshrc", "TargetContent":"SECRET", "ReplacementContent":"SECRET"}),
            "sensitive_write",
        ),
        (
            HOST_ANTIGRAVITY,
            "multi_replace_file_content",
            json!({"TargetFile":"/home/u/.zshrc", "ReplacementChunks":[{"ReplacementContent":"SECRET"}]}),
            "sensitive_write",
        ),
    ] {
        let observation = parse(host, payload(host, tool, input));
        assert_eq!(describe(&observation).1, Some(reason), "{host} {tool}");
        assert!(!observation.event.tool_input.to_string().contains("SECRET"));
        assert!(!describe(&observation).0.contains("SECRET"));
        assert_eq!(observation.event.tool_input.as_object().unwrap().len(), 1);
    }
}

#[test]
fn patches_keep_all_move_destinations_and_never_context_or_diff_content() {
    let patch = "*** Begin Patch\n*** Update File: src/a.rs\n*** Move to: /home/u/.zshrc\n@@\n-SECRET\n+SECRET\n *** Add File: fake-context-secret\n*** Delete File: src/b.rs\n*** Add File: src/c.rs\n+SECRET\n*** End Patch";
    for (host, input) in [
        (HOST_CODEX, json!({"command":patch})),
        (HOST_OPENCODE, json!({"patchText":patch})),
        // The V1 desktop adapter can forward just the headers of a large patch.
        (
            HOST_OPENCODE,
            json!({"patchText":patch_paths(patch).iter().map(|p| format!("*** Update File: {p}")).collect::<Vec<_>>().join("\n")}),
        ),
    ] {
        let observation = parse(host, payload(host, "apply_patch", input));
        assert_eq!(
            describe(&observation),
            (
                "src/a.rs, ~/.zshrc, src/b.rs, src/c.rs".into(),
                Some("sensitive_write")
            )
        );
        assert!(!observation.event.tool_input.to_string().contains("SECRET"));
        assert!(!observation.event.tool_input.to_string().contains("context"));
    }
    assert_eq!(
        patch_paths("*** Update File: src/a\n*** Move to: ../other/a").len(),
        2
    );
    let event = parse(
        HOST_OPENCODE,
        payload(
            HOST_OPENCODE,
            "apply_patch",
            json!({"patchText":"*** Update File: a\n*** Move to: ../other/a"}),
        ),
    );
    assert_eq!(describe(&event).1, Some("write_outside_cwd"));
}

#[test]
fn workspace_lists_never_invent_a_current_directory() {
    for roots in [
        json!([]),
        json!(["/home/u/proj"]),
        json!(["/home/u/other", "/home/u/proj"]),
    ] {
        let mut cursor = payload(HOST_CURSOR, "Write", json!({"file_path":"relative.rs"}));
        cursor.as_object_mut().unwrap().remove("cwd");
        cursor["workspace_roots"] = roots.clone();
        let cursor = parse(HOST_CURSOR, cursor);
        assert!(cursor.event.cwd.is_none());
        assert_eq!(describe(&cursor), ("relative.rs".into(), None));
        let mut agy = payload(
            HOST_ANTIGRAVITY,
            "write_to_file",
            json!({"TargetFile":"/home/u/proj/a.rs"}),
        );
        agy["payload"]["workspacePaths"] = roots;
        let agy = parse(HOST_ANTIGRAVITY, agy);
        assert!(agy.event.cwd.is_none());
        assert_eq!(describe(&agy), ("~/proj/a.rs".into(), None));
    }
    let cursor = parse(
        HOST_CURSOR,
        payload(
            HOST_CURSOR,
            "Shell",
            json!({"command":"rm -r .", "working_directory":"/home/u/other"}),
        ),
    );
    assert_eq!(cursor.event.cwd.as_deref(), Some("/home/u/other"));
    let opencode = parse(
        HOST_OPENCODE,
        payload(
            HOST_OPENCODE,
            "bash",
            json!({"command":"pwd", "workdir":"/home/u/other"}),
        ),
    );
    assert_eq!(opencode.event.cwd.as_deref(), Some("/home/u/other"));
}

#[test]
fn goose_requires_its_result_schema_and_retains_only_its_actual_verdict() {
    let mut denied = payload(HOST_GOOSE, "developer__shell", json!({"command":"echo hi"}));
    denied["decision"] = json!("deny");
    denied["reason"] = json!("PRIVATE-CREDENTIAL");
    denied["blocked_by"] = json!("/private/plugin");
    denied["cause"] = json!("policy_denial");
    let denied = parse(HOST_GOOSE, denied);
    assert!(matches!(denied.verdict, AuditVerdict::Denied));
    assert_eq!(describe(&denied), ("echo hi".into(), None));
    for key in [
        "event",
        "decision",
        "session_id",
        "working_dir",
        "tool_call_id",
        "tool_input",
    ] {
        let mut invalid = payload(HOST_GOOSE, "developer__shell", json!({"command":"ls"}));
        invalid.as_object_mut().unwrap().remove(key);
        assert!(parse_hook(HOST_GOOSE, invalid).is_err(), "{key}");
    }
    for invalid in ["block", "ask", "unknown", ""] {
        let mut body = payload(HOST_GOOSE, "developer__shell", json!({"command":"ls"}));
        body["decision"] = json!(invalid);
        assert!(parse_hook(HOST_GOOSE, body).is_err());
    }
    for host in [
        HOST_CLAUDE_CODE,
        HOST_CURSOR,
        HOST_OPENCODE,
        HOST_ANTIGRAVITY,
    ] {
        assert!(parse_hook(HOST_GOOSE, payload(host, "Bash", json!({"command":"ls"}))).is_err());
    }
    let mut mixed = payload(HOST_GOOSE, "shell", json!({"command":"ls"}));
    mixed["hook_event_name"] = json!("PreToolUse");
    assert!(parse_hook(HOST_GOOSE, mixed).is_err());
}

#[test]
fn antigravity_only_accepts_explicit_post_wrapper_and_discards_raw_errors() {
    let mut body = payload(
        HOST_ANTIGRAVITY,
        "run_command",
        json!({"CommandLine":"false", "Cwd":"/home/u/proj"}),
    );
    body["payload"]["error"] = json!("PRIVATE TOKEN stderr text");
    let observation = parse(HOST_ANTIGRAVITY, body.clone());
    assert!(matches!(observation.verdict, AuditVerdict::Error));
    assert_eq!(describe(&observation).0, "false");
    assert!(parse_hook(HOST_ANTIGRAVITY, body["payload"].clone()).is_err());
    for event in [
        "PreToolUse",
        "Stop",
        "PreInvocation",
        "PostInvocation",
        "future",
    ] {
        body["hook_event_name"] = json!(event);
        assert!(parse_hook(HOST_ANTIGRAVITY, body.clone())
            .unwrap()
            .is_none());
    }
}

#[test]
fn only_one_selected_lifecycle_is_accepted_per_harness() {
    for host in [
        HOST_CURSOR,
        HOST_OPENCODE,
        HOST_GOOSE,
        HOST_CLAUDE_CODE,
        HOST_CODEX,
    ] {
        let mut body = payload(host, "custom_tool", json!({"private":"SECRET"}));
        let key = if host == HOST_GOOSE {
            "event"
        } else {
            "hook_event_name"
        };
        for lifecycle in [
            "PostToolUse",
            "PostToolUseFailure",
            "Stop",
            "SessionStart",
            "beforeReadFile",
            "afterFileEdit",
            "postToolUse",
            "unknown",
        ] {
            body[key] = json!(lifecycle);
            assert!(
                parse_hook(host, body.clone()).unwrap().is_none(),
                "{host} {lifecycle}"
            );
        }
    }
    assert!(parse_hook("unknown-host", json!({})).is_err());
    for body in [Value::Null, json!([]), json!("payload"), json!({})] {
        assert!(parse_hook(HOST_CURSOR, body).is_err());
    }
}

#[test]
fn unknown_tool_arguments_are_not_classified_by_an_unrelated_suffix() {
    for host in [HOST_CURSOR, HOST_OPENCODE, HOST_GOOSE, HOST_ANTIGRAVITY] {
        let observation = parse(
            host,
            payload(
                host,
                "other__shell",
                json!({"command":"sudo ls", "content":"SECRET", "filePath":".env"}),
            ),
        );
        assert_eq!(describe(&observation), ("other__shell".into(), None));
        assert_eq!(observation.event.tool_input, json!({}));
    }
}

#[test]
fn mcp_dedupe_matches_full_harness_tool_conventions() {
    for (host, tool) in [
        (HOST_CLAUDE_CODE, "mcp__prism__ketch__search"),
        (HOST_CODEX, "mcp__prism__ketch__search"),
        (HOST_CURSOR, "MCP:ketch__search"),
        (HOST_OPENCODE, "prism_ketch__search"),
        (HOST_GOOSE, "prism__ketch__search"),
    ] {
        let mut body = payload(host, tool, json!({"query":"SECRET"}));
        if host == HOST_CURSOR {
            body["mcp_server_name"] = json!("prism");
        }
        let observation = parse(host, body);
        assert!(
            observation.matches_prism_tool(host, "ketch", "search"),
            "{host}"
        );
        assert!(!observation.matches_prism_tool(host, "other", "search"));
        assert_eq!(observation.event.tool_input, json!({}));
        for unrelated in [
            format!("other__{tool}"),
            format!("{tool}_more"),
            tool.replace("prism", "elsewhere"),
        ] {
            if unrelated == tool {
                continue;
            }
            let unrelated = parse(host, payload(host, &unrelated, json!({})));
            assert!(!unrelated.matches_prism_tool(host, "ketch", "search"));
        }
    }
    for tool in [
        "ketch__search",
        "MCP:other__ketch__search",
        "other_ketch__search",
        "mcp__notprism__ketch__search",
    ] {
        for host in [HOST_CURSOR, HOST_GOOSE, HOST_CODEX, HOST_OPENCODE] {
            let observation = parse(host, payload(host, tool, json!({})));
            assert!(
                !observation.matches_prism_tool(host, "ketch", "search"),
                "{host} {tool}"
            );
        }
    }
    let mut body = payload(HOST_CURSOR, "MCP:ketch__search", json!({}));
    assert!(!parse(HOST_CURSOR, body.clone()).matches_prism_tool(HOST_CURSOR, "ketch", "search"));
    body["mcp_server_name"] = Value::Null;
    assert!(!parse(HOST_CURSOR, body.clone()).matches_prism_tool(HOST_CURSOR, "ketch", "search"));
    body["mcp_server_name"] = json!("elsewhere");
    assert!(!parse(HOST_CURSOR, body).matches_prism_tool(HOST_CURSOR, "ketch", "search"));
    let oc = parse(
        HOST_OPENCODE,
        payload(HOST_OPENCODE, "prism_a_b__read_file", json!({})),
    );
    assert!(oc.matches_prism_tool(HOST_OPENCODE, "a.b", "read.file"));
    let agy = parse(
        HOST_ANTIGRAVITY,
        payload(HOST_ANTIGRAVITY, "mcp_prism_ketch__search", json!({})),
    );
    assert!(!agy.matches_prism_tool(HOST_ANTIGRAVITY, "ketch", "search"));
}

#[test]
fn retries_are_bounded_and_identical_calls_with_new_ids_are_not_collapsed() {
    let host = HOST_OPENCODE;
    let value = payload(host, "bash", json!({"command":"ls"}));
    let observation = parse(host, value.clone());
    let mut budget = EventBudget::default();
    assert!(budget.admit_event(host, &observation));
    for _ in 0..1100 {
        assert!(!budget.admit_event(host, &observation));
    }
    assert_eq!(budget.stamps.len(), 1);
    assert!(budget.admit_event(HOST_CODEX, &observation));
    let mut next = value.clone();
    next["tool_use_id"] = json!("call-2");
    assert!(budget.admit_event(host, &parse(host, next)));
    let mut next = value;
    next["session_id"] = json!("other-session");
    assert!(budget.admit_event(host, &parse(host, next)));
    budget.calls.front_mut().unwrap().0 = Instant::now() - Duration::from_secs(61);
    assert!(budget.admit_event(host, &observation));
    for i in 0..1100 {
        let mut next = payload(host, "bash", json!({"command":"ls"}));
        next["tool_use_id"] = json!(format!("call-{i}"));
        budget.admit_event(host, &parse(host, next));
    }
    assert_eq!(budget.stamps.len(), EVENTS_PER_MINUTE);
    assert!(budget.calls.len() <= EVENTS_PER_MINUTE);
}

#[test]
fn metadata_and_paths_have_explicit_bounds_and_types() {
    for (key, value) in [
        ("tool_name", json!("x".repeat(257))),
        ("tool_name", json!("bad\ntool")),
        ("session_id", json!("x".repeat(257))),
        ("tool_use_id", json!("x".repeat(257))),
        ("cwd", json!("relative")),
        ("cwd", json!(["/home/u/proj"])),
        ("cwd", json!(format!("/{}", "x".repeat(4096)))),
        ("cwd", json!("/home/u/\nsecret")),
        ("tool_input", json!("{\"command\":\"ls\"}")),
    ] {
        let mut value_body = payload(HOST_OPENCODE, "bash", json!({"command":"ls"}));
        value_body[key] = value;
        assert!(parse_hook(HOST_OPENCODE, value_body).is_err(), "{key}");
    }
    for input in [
        json!({"file_path":"/wrong/schema"}),
        json!({"filePath":123}),
        json!({"filePath":"bad\npath"}),
        json!({"filePath":"a".repeat(4097)}),
    ] {
        assert!(parse_hook(HOST_OPENCODE, payload(HOST_OPENCODE, "read", input)).is_err());
    }
    let patch = (0..513)
        .map(|i| format!("*** Add File: {i}"))
        .collect::<Vec<_>>()
        .join("\n");
    assert!(parse_hook(
        HOST_OPENCODE,
        payload(HOST_OPENCODE, "apply_patch", json!({"patchText":patch}))
    )
    .is_err());
}

#[test]
fn shell_redaction_covers_quoted_credentials_headers_urls_and_script_bodies() {
    let session = "ec33ebf9-0cba-4100-8142-c61503f6c587";
    assert_eq!(redact_identifier(session), session);
    let tool = "mcp__prism__long_server_namespace__read_file";
    assert_eq!(subject(tool, &json!({}), None, None), tool);
    assert_eq!(redact_identifier("sk-private-credential"), "***");
    for command in [
        "run --password 'PRIVATE multi word'",
        "export API_TOKEN='PRIVATE multi word' && run",
        "curl -H 'X-Api-Key: PRIVATE' https://example.test",
        "curl -H 'Authorization: Bearer PRIVATE' https://example.test",
        "curl -H 'Authorization: Basic PRIVATE' https://example.test",
        "curl --user 'someone:PRIVATE' https://example.test",
        "curl https://name:PRIVATE@example.test/a?token=PRIVATE",
        "curl https://example.test/a?token=PRIVATE&x=PRIVATE#PRIVATE",
        "echo sk-PRIVATE",
        "printf ghp_PRIVATE",
        "TOKEN=PRIVATE echo hello",
        "cat <<'EOF'\nPRIVATE file contents\nEOF",
    ] {
        let observation = parse(
            HOST_CURSOR,
            payload(HOST_CURSOR, "Shell", json!({"command":command})),
        );
        assert!(
            !describe(&observation).0.contains("PRIVATE"),
            "{command} => {}",
            describe(&observation).0
        );
    }
    let command = format!("echo {}", "é日 ".repeat(300));
    let observation = parse(
        HOST_CURSOR,
        payload(HOST_CURSOR, "Shell", json!({"command":command})),
    );
    assert_eq!(describe(&observation).0.chars().count(), SUBJECT_MAX_CHARS);
    for command in ["git", "echo café; sudo ls", "echo 日本語 && git status"] {
        let observation = parse(
            HOST_CURSOR,
            payload(HOST_CURSOR, "Shell", json!({"command":command})),
        );
        let result = describe(&observation);
        if command.contains("sudo") {
            assert_eq!(result.1, Some("sudo"));
        }
    }
}
