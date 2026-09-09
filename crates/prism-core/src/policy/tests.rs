use super::tests::{agent, read_only, rule};
use super::*;
use crate::config::{Posture, RuleDecision};
use serde_json::{json, Value};
use std::path::PathBuf;

fn action(tool: &str, arguments: Value) -> Action {
    Action::from_mcp("agt", "srv", tool, arguments, None, Utc::now())
}

fn check(condition: Value, action: &Action) -> bool {
    Condition::parse(&condition).unwrap().matches(action)
}

#[test]
fn paths_by_shape_name_resolution_and_access() {
    let a = action(
        "read_file",
        json!({
            "shape": ["/a/b/../c", "~/Projects/x", "./x/../y", "../other", "C:\\work\\a\\..\\b"],
            "FiLe_PaTh": "named.txt", "pattern": "src/*.rs", "ignored": "ordinary text",
            "dest": "target.txt", "a/b~c": "/escaped"
        }),
    )
    .with_cwd(PathBuf::from("/workspace/repo"));
    let paths: Vec<_> = a.facets.paths.iter().map(|p| p.path.as_str()).collect();
    for expected in [
        "/a/c",
        "/workspace/repo/y",
        "/workspace/other",
        "C:/work/b",
        "/workspace/repo/named.txt",
        "/workspace/repo/src/*.rs",
        "/workspace/repo/target.txt",
    ] {
        assert!(paths.contains(&expected), "missing {expected}: {paths:?}");
    }
    assert!(paths.contains(&action::home().unwrap().join("Projects/x").to_str().unwrap()));
    assert_eq!(
        a.facets
            .paths
            .iter()
            .find(|p| p.argument == "/dest")
            .unwrap()
            .access,
        PathAccess::Write
    );
    assert!(a.facets.paths.iter().any(|p| p.argument == "/a~1b~0c"));
    assert_eq!(
        action("tool", json!({"pattern":"*.rs"})).facets.paths.len(),
        0
    );
    for name in [
        "path",
        "file",
        "filepath",
        "file_path",
        "dir",
        "directory",
        "cwd",
        "target",
        "source",
        "destination",
        "dest",
        "src",
        "output",
        "input",
    ] {
        assert_eq!(
            action("tool", json!({name: "foo"})).facets.paths.len(),
            1,
            "{name}"
        );
    }
    for tool in [
        "write", "create", "edit", "update", "move", "rename", "copy", "mkdir", "save", "put",
        "set",
    ] {
        assert_eq!(
            action(tool, json!({"path":"/x"})).facets.paths[0].access,
            PathAccess::Write,
            "{tool}"
        );
    }
    for tool in [
        "delete",
        "remove",
        "rm",
        "unlink",
        "trash",
        "delete_and_write",
    ] {
        assert_eq!(
            action(tool, json!({"path":"/x"})).facets.paths[0].access,
            PathAccess::Delete
        );
    }
    let ro = Action::from_mcp(
        "agt",
        "srv",
        "read",
        json!({"path":"/x"}),
        Some(read_only()),
        Utc::now(),
    );
    assert_eq!(ro.facets.paths[0].access, PathAccess::Read);
    assert_eq!(
        action("tool", json!({"path":"/x"})).facets.paths[0].access,
        PathAccess::Unknown
    );
    assert_eq!(
        resolve_path("../../x", Some(std::path::Path::new("/a"))),
        "/x"
    );
    assert_eq!(resolve_path("../../x", None), "../../x");
    assert_eq!(
        resolve_path("../x", Some(std::path::Path::new("C:\\work\\repo"))),
        "C:/work/x"
    );
    assert_eq!(
        resolve_path("../../../../x", Some(std::path::Path::new("C:/work/repo"))),
        "C:/x"
    );
    assert_eq!(
        action("write", json!({"host":"/repo/file"}))
            .facets
            .paths
            .len(),
        1
    );
    assert_eq!(resolve_path("C:\\..\\x", None), "C:/x");
}

#[test]
fn hosts_schemes_names_scopes_and_redaction() {
    for scheme in [
        "http", "https", "ws", "wss", "ftp", "ssh", "git", "postgres", "mysql", "redis",
    ] {
        let a = action(
            "tool",
            json!({"target":format!("{scheme}://user:secret@api.github.com:8443/secret?token=hidden#secret")}),
        );
        assert_eq!(a.facets.hosts[0].host, "api.github.com", "{scheme}");
        assert_eq!(a.facets.hosts[0].port, Some(8443));
        assert_eq!(a.facets.hosts[0].scheme.as_deref(), Some(scheme));
        assert!(a.facets.paths.is_empty());
        assert_eq!(a.facets_summary(), ["host: api.github.com (public)"]);
    }
    for name in [
        "url", "uri", "host", "hostname", "endpoint", "server", "base_url", "HOST",
    ] {
        assert_eq!(
            action("tool", json!({name:"API.GITHUB.COM"})).facets.hosts[0].host,
            "api.github.com"
        );
    }
    assert_eq!(
        action("tool", json!({"host":"api.github.com"}))
            .facets
            .hosts[0]
            .port,
        None
    );
    assert_eq!(
        action("tool", json!({"host":"api.github.com:8080"}))
            .facets
            .hosts[0]
            .port,
        Some(8080)
    );
    for (host, scope) in [
        ("127.9.2.1", HostScope::Loopback),
        ("::1", HostScope::Loopback),
        ("LOCALHOST", HostScope::Loopback),
        ("10.2.3.4", HostScope::Private),
        ("172.16.0.1", HostScope::Private),
        ("192.168.1.1", HostScope::Private),
        ("169.254.4.5", HostScope::Private),
        ("fe80::123", HostScope::Private),
        ("fd01::a", HostScope::Private),
        ("fc00::1", HostScope::Private),
        ("printer.local", HostScope::Private),
        ("::ffff:192.168.1.1", HostScope::Private),
        ("8.8.8.8", HostScope::Public),
        ("2001:4860::8888", HostScope::Public),
        ("api.github.com", HostScope::Public),
        ("not a host?password=secret", HostScope::Unknown),
        ("https://", HostScope::Unknown),
    ] {
        let a = action("tool", json!({"host":host}));
        assert_eq!(a.facets.hosts[0].scope, scope, "{host}");
        if scope == HostScope::Unknown {
            assert_eq!(a.facets.hosts[0].host, "");
        }
    }
}

#[test]
fn extraction_depth_and_total_count_are_bounded() {
    let mut v = json!("/x");
    for _ in 0..8 {
        v = json!([v]);
    }
    assert_eq!(action("tool", v.clone()).facets.paths.len(), 1);
    assert!(action("tool", json!([v])).facets.paths.is_empty());
    let a = action(
        "write",
        Value::Array(
            (0..200)
                .map(|i| {
                    json!(if i % 2 == 0 {
                        format!("/a/{i}")
                    } else {
                        format!("https://host{i}.com")
                    })
                })
                .collect(),
        ),
    );
    assert_eq!(a.facets.paths.len() + a.facets.hosts.len(), 64);
    assert_eq!(a.facets_summary().len(), 4);
}

#[test]
fn path_predicates_bind_to_the_same_facet() {
    let a = action(
        "tool",
        json!({"source":"/repo/input", "output":"/elsewhere/output"}),
    )
    .with_cwd("/repo".into());
    assert!(check(json!({"path":{"under":"/repo"}}), &a));
    assert!(!check(json!({"path":{"under":"/rep"}}), &a));
    assert!(check(json!({"path":{"outside_cwd":true}}), &a));
    assert!(check(json!({"path":{"outside_cwd":false}}), &a));
    assert!(!check(
        json!({"path":{"under":"/repo", "access":["write", "delete"]}}),
        &a
    ));
    assert!(check(
        json!({"path":{"under":"/elsewhere", "outside_cwd":true,"access":["write"]}}),
        &a
    ));
    assert!(!check(
        json!({"path":{"outside_cwd":true}}),
        &action("write", json!({"path":"/elsewhere"}))
    ));
    assert!(check(json!({"path":{"under":"./sub/.."}}), &a));
    assert!(check(
        json!({"path":{"under":"~/Projects"}}),
        &action("write", json!({"path":"~/Projects/x"}))
    ));
    assert!(!check(
        json!({"path":{"under":"/repo"}}),
        &action("write", json!({"path":"/repo/../etc/passwd"}))
    ));
}

#[test]
fn host_predicates_bind_to_the_same_facet() {
    let a = action(
        "fetch",
        json!({"urls":["https://API.GITHUB.COM/", "http://localhost"]}),
    );
    assert!(check(
        json!({"host":{"in":["*.GITHUB.COM"], "scope":["public"]}}),
        &a
    ));
    assert!(check(json!({"host":{"in":["api.github.com"]}}), &a));
    assert!(!check(
        json!({"host":{"in":["api.github.com"], "scope":["loopback"]}}),
        &a
    ));
    assert!(check(json!({"host":{"scope":["loopback"]}}), &a));
    assert!(!check(json!({"host":{"in":["example.com"]}}), &a));
}

#[test]
fn arguments_commands_and_boolean_predicates() {
    let a = action(
        "run",
        json!({"repo":"1broseidon/prism", "name":"release-1", "null":null, "n":3, "a/b":["x"], "cmd":"/usr/bin/git status"}),
    );
    for c in [
        json!({"arg":{"pointer":"/repo","equals":"1broseidon/prism"}}),
        json!({"arg":{"pointer":"/repo","starts_with":"1broseidon/"}}),
        json!({"arg":{"pointer":"/name","matches":"release-*"}}),
        json!({"arg":{"pointer":"/null","equals":null}}),
        json!({"arg":{"pointer":"/n","equals":3}}),
        json!({"arg":{"pointer":"/a~1b/0","equals":"x"}}),
        json!({"command":{"program_in":["git", "rm"]}}),
    ] {
        assert!(check(c, &a));
    }
    for c in [
        json!({"arg":{"pointer":"/missing","equals":null}}),
        json!({"arg":{"pointer":"/n","starts_with":"3"}}),
        json!({"arg":{"pointer":"/repo","matches":"release-*"}}),
        json!({"arg":{"pointer":"/repo","equals":"x"}}),
        json!({"command":{"program_in":["rm"]}}),
        json!({"tag":"write_outside_cwd"}),
    ] {
        assert!(!check(c, &a));
    }
    for args in [
        json!({"command":"rm -rf /tmp/x"}),
        json!({"nested":{"args":["/bin/rm", "x"]}}),
        json!({"CMD":"rm x"}),
    ] {
        assert!(check(
            json!({"command":{"program_in":["rm"]}}),
            &action("run", args)
        ));
    }
    assert!(!check(
        json!({"command":{"program_in":["rm"]}}),
        &action("run", json!({"args":["git", "rm"]}))
    ));
    let yes = json!({"command":{"program_in":["git"]}});
    let no = json!({"tag":"future"});
    assert!(check(json!({"all":[yes.clone(), {"not":no.clone()}]}), &a));
    assert!(!check(json!({"all":[yes.clone(), no.clone()]}), &a));
    assert!(check(json!({"any":[no.clone(), yes.clone()]}), &a));
    assert!(!check(json!({"any":[no.clone()]}), &a));
    assert!(!check(json!({"not":yes}), &a));
    assert!(check(json!({"not":no}), &a));
    assert!(check(json!({"all":[]}), &a));
    assert!(!check(json!({"any":[]}), &a));
}

#[test]
fn malformed_conditions_are_inert_even_below_not_or_any() {
    let a = action("tool", json!({"path":"/repo/a"}));
    for c in [
        json!({"unknown":true}),
        json!({"path":{"under":"/repo", "typo":true}}),
        json!({"path":{}}),
        json!({"host":{}}),
        json!({"path":{"under":null,"access":["write"]}}),
        json!({"path":{"access":["execute"]}}),
        json!({"host":{"scope":["internet"]}}),
        json!({"all":"wrong"}),
        json!({"arg":{"pointer":"/x"}}),
        json!({"arg":{"pointer":"x", "equals":1}}),
        json!({"arg":{"pointer":"/bad~2", "equals":1}}),
        json!({"arg":{"pointer":"/x","equals":1,"matches":"*"}}),
        json!({"path":{"under":"/repo"},"tag":"x"}),
    ] {
        for value in [
            c.clone(),
            json!({"not":c.clone()}),
            json!({"any":[{"path":{"under":"/repo"}},c]}),
        ] {
            assert!(Condition::parse(&value).is_err(), "{value}");
            let mut r = rule("bad", None, None, None, RuleDecision::Allow);
            r.condition = Some(value);
            assert_eq!(
                evaluate(&[r], &agent(Posture::Supervised), &a, Utc::now()).verdict,
                Verdict::Ask
            );
        }
    }
}

#[test]
fn condition_precedence_exactness_and_strictness() {
    let a = action("tool", json!({"path":"/repo/file"}));
    let mut conditioned = rule(
        "conditioned",
        Some("agt"),
        Some("srv"),
        Some("t*"),
        RuleDecision::Allow,
    );
    conditioned.condition = Some(json!({"path":{"under":"/repo"}}));
    let exact = rule(
        "exact",
        Some("agt"),
        Some("srv"),
        Some("tool"),
        RuleDecision::Deny,
    );
    let eval = evaluate(
        &[exact.clone(), conditioned.clone()],
        &agent(Posture::Supervised),
        &a,
        Utc::now(),
    );
    assert_eq!(eval.verdict, Verdict::Allow);
    assert!(eval.matched_condition);
    let mut conditioned_exact = exact.clone();
    conditioned_exact.condition = conditioned.condition.clone();
    assert_eq!(
        evaluate(
            &[conditioned.clone(), conditioned_exact],
            &agent(Posture::Supervised),
            &a,
            Utc::now()
        )
        .verdict,
        Verdict::Deny
    );
    let mut same = conditioned.clone();
    same.decision = RuleDecision::Ask;
    assert_eq!(
        evaluate(
            &[conditioned.clone(), same.clone()],
            &agent(Posture::Supervised),
            &a,
            Utc::now()
        )
        .verdict,
        Verdict::Ask
    );
    same.decision = RuleDecision::Deny;
    assert_eq!(
        evaluate(
            &[conditioned.clone(), same],
            &agent(Posture::Supervised),
            &a,
            Utc::now()
        )
        .verdict,
        Verdict::Deny
    );
    conditioned.condition = Some(json!({"path":{"under":"/elsewhere"}}));
    assert_eq!(
        evaluate(
            &[conditioned, exact],
            &agent(Posture::Supervised),
            &a,
            Utc::now()
        )
        .verdict,
        Verdict::Deny
    );
    assert!(!glob_match("a**a", "a"));
    assert!(glob_match("é**é", "éé"));
}

#[test]
fn offers_are_bounded_and_use_cwd_or_parent() {
    let a = action(
        "write_file",
        json!({"path":"/repo/sub/file", "url":"https://user:pass@example.com/x?secret=1"}),
    )
    .with_cwd("/repo".into());
    let offers = a.offers();
    assert_eq!(offers.len(), 2);
    assert_eq!(offers[0].value, "/repo");
    assert_eq!(offers[1].value, "example.com");
    assert_eq!(
        action("delete_file", json!({"path":"/a/b/c"})).offers()[0].value,
        "/a/b"
    );
    assert!(action("write_file", json!({"path":"/file"}))
        .offers()
        .is_empty());
    assert!(action("read_file", json!({"path":"/a/b"}))
        .offers()
        .is_empty());
}

#[test]
fn modifiers_and_old_audit_explanations() {
    let a = action("tool", json!({}));
    let mut e = evaluate(&[], &agent(Posture::Trusted), &a, Utc::now());
    e.apply_tripwire(true);
    assert_eq!((e.verdict, e.decider), (Verdict::Ask, Decider::Tripwire));
    let mut e = evaluate(&[], &agent(Posture::Supervised), &a, Utc::now());
    e.apply_tripwire(true);
    assert_eq!(e.decider, Decider::Posture(Posture::Supervised));
    let mut entry: crate::AuditEntry = serde_json::from_value(json!({"id":"old","at":Utc::now(),"agent_id":"agt","agent_name":"Claude Code","server_id":"github","tool":"create_issue","verdict":"allowed","source":{"kind":"posture","posture":"first_use"},"duration_ms":1,"error":null})).unwrap();
    assert!(entry.facets.is_empty());
    assert!(!serde_json::to_value(&entry)
        .unwrap()
        .as_object()
        .unwrap()
        .contains_key("facets"));
    assert_eq!(explain(&entry, &[]), "First-use posture");
    for (decider, expected) in [
        (Decider::Tripwire, "Rate tripwire"),
        (Decider::DoNotDisturb, "Do not disturb"),
        (Decider::Timeout, "Nobody answered in time"),
    ] {
        entry.source = (&decider).into();
        assert_eq!(explain(&entry, &[]), expected);
        assert_eq!(
            serde_json::from_value::<crate::AuditEntry>(serde_json::to_value(&entry).unwrap())
                .unwrap(),
            entry
        );
    }
    let mut r = rule(
        "r",
        Some("agt"),
        Some("github"),
        Some("create_*"),
        RuleDecision::Allow,
    );
    r.condition = Some(json!({"path":{"under":"~/Projects","access":["write"]}}));
    entry.source = crate::AuditSource::Rule {
        rule_id: "r".into(),
    };
    assert_eq!(
        explain(&entry, &[r]),
        "Rule: Claude Code · github · create_* · when path under ~/Projects (write) · Allow"
    );
    assert!(explain(&entry, &[]).starts_with("Rule:"));
}

#[test]
fn evaluation_timing_10000_calls_200_rules() {
    let a = action(
        "write_file",
        json!({"path":"/repo/src/lib.rs", "url":"https://api.github.com", "repo":"prism"}),
    )
    .with_cwd("/repo".into());
    let agent = agent(Posture::Supervised);
    let now = Utc::now();
    let mut rules: Vec<_> = (0..200).map(|i| {
        let server = if i % 20 == 0 { "srv".into() } else { format!("server{}", i % 20) };
        let mut r = rule(&format!("r{i}"), Some("agt"), Some(&server), Some("write_*"), RuleDecision::Allow);
        r.condition = Some(match (i / 20) % 5 {
            0 => json!({"path":{"under":format!("/other/{i}"),"access":["write"]}}),
            1 => json!({"host":{"in":[format!("host{i}.example.com")],"scope":["public"]}}),
            2 => json!({"arg":{"pointer":"/repo","equals":format!("repo{i}")}}),
            3 => json!({"any":[{"tag":"future"},{"command":{"program_in":["rm"]}}]}),
            _ => json!({"all":[{"path":{"under":"/repo"}},{"arg":{"pointer":"/repo","equals":format!("repo{i}")}}]}),
        });
        r
    }).collect();
    // A matching grant sits among other servers' rules and nonmatching conditions.
    rules[100].condition = Some(json!({"path":{"under":"/repo", "access":["write"]}}));
    assert_eq!(evaluate(&rules, &agent, &a, now).verdict, Verdict::Allow);
    let start = std::time::Instant::now();
    for _ in 0..10_000 {
        std::hint::black_box(evaluate(std::hint::black_box(&rules), &agent, &a, now));
    }
    let elapsed = start.elapsed();
    println!(
        "10,000 evaluations × 200 mixed rules: {:.2} ms ({})",
        elapsed.as_secs_f64() * 1000.0,
        if cfg!(debug_assertions) {
            "debug"
        } else {
            "release"
        }
    );
    let limit = if cfg!(debug_assertions) {
        std::time::Duration::from_secs(2)
    } else {
        std::time::Duration::from_millis(50)
    };
    assert!(elapsed < limit, "{elapsed:?}");
}
