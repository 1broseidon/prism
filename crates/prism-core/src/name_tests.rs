use super::*;

fn named(name: &str) -> AgentConfig {
    let mut agent = AgentConfig::harness(
        "codex",
        None,
        crate::AgentStatus::Approved,
        chrono::Utc::now(),
    );
    agent.name = name.into();
    agent
}

#[test]
fn operator_names_are_bounded_and_unicode_case_insensitively_unique() {
    let existing = named("Maße");
    assert_eq!(
        agent("  New name  ", std::slice::from_ref(&existing), None).unwrap(),
        "New name"
    );
    assert!(agent("MASSE", std::slice::from_ref(&existing), None).is_err());
    assert_eq!(
        agent("MASSE", std::slice::from_ref(&existing), Some(&existing.id)).unwrap(),
        "MASSE"
    );
    for name in [
        "".into(),
        " \n ".into(),
        "a\nb".into(),
        "a\u{7f}b".into(),
        "字".repeat(81),
    ] {
        assert!(display(&name).is_err(), "{name:?}");
    }
    assert!(display(&"字".repeat(80)).is_ok());
    assert_eq!(server("  My server  ").unwrap(), "My server");
    for name in ["ambiguous__route", "trailing_", "__"] {
        assert!(server(name).is_err());
    }
    assert!(server("single_under score").is_ok());
}

#[test]
fn automatic_labels_stay_unique_and_within_the_same_bound() {
    let base = "字".repeat(MAX);
    let first = named(&base);
    let second = unique(&base, std::slice::from_ref(&first), None);
    assert_eq!(second.chars().count(), MAX);
    assert!(second.ends_with(" (2)"));
    let third = unique(&base, &[first, named(&second)], None);
    assert_eq!(third.chars().count(), MAX);
    assert!(third.ends_with(" (3)"));
    assert_eq!(unique("MASSE", &[named("Maße")], None), "MASSE (2)");
    assert_eq!(unique(" \n\t ", &[], None), "unknown");
    assert_eq!(unique("a\nb", &[], None), "ab");
}
