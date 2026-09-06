//! Preserve existing Codex hook state when unchanged handlers move within a group.
use std::{collections::HashSet, path::Path};
use toml_edit::{DocumentMut, Key};

/// Remap `(group, old_handler, new_handler)` entries; `None` removes a handler's state.
///
/// The caller must include every shifted or removed handler, using the exact hooks path
/// stored in Codex's state keys. Only unchanged handlers may retain state at a new index:
/// this function neither verifies hook contents nor creates trust. Same-index entries may
/// be omitted. Other paths, events, groups, and unmentioned handlers are left alone.
///
/// All validation precedes mutation. An occupied destination must also be a source in
/// this operation, even when the incoming handler has no state: otherwise stale trust
/// could be inherited. Missing state is never synthesized, and absent tables stay absent.
pub fn remap(
    config: &mut DocumentMut,
    hooks_path: &Path,
    moves: &[(usize, usize, Option<usize>)],
) -> Result<(), String> {
    if moves.is_empty() {
        return Ok(());
    }
    let path = hooks_path
        .to_str()
        .ok_or("Hook settings path must be valid UTF-8 to remap state")?;
    let state_key = |group, handler| format!("{path}:pre_tool_use:{group}:{handler}");
    let mut sources = HashSet::new();
    let mut destinations = HashSet::new();
    let mut plan = Vec::with_capacity(moves.len());
    for &(group, old, new) in moves {
        let source = state_key(group, old);
        if !sources.insert(source.clone()) {
            return Err(format!("Duplicate hook state source: {source}"));
        }
        let destination = new.map(|handler| state_key(group, handler));
        if let Some(destination) = &destination {
            if !destinations.insert(destination.clone()) {
                return Err(format!("Duplicate hook state destination: {destination}"));
            }
        }
        plan.push((source, destination));
    }

    let Some(hooks) = config.get("hooks") else {
        return Ok(());
    };
    let hooks = hooks.as_table_like().ok_or("hooks must be a table")?;
    let Some(state) = hooks.get("state") else {
        return Ok(());
    };
    let state = state.as_table_like().ok_or("hooks.state must be a table")?;
    for destination in &destinations {
        if state.contains_key(destination) && !sources.contains(destination) {
            return Err(format!(
                "Hook state destination is already occupied: {destination}"
            ));
        }
    }

    // Snapshot original items and key decoration before removing any source. Cloning an
    // Item preserves table positions, comments, unknown fields, and literal value syntax.
    let mut replacements = Vec::new();
    for (source, destination) in &plan {
        if destination.as_ref() == Some(source) {
            continue;
        }
        if let (Some(destination), Some((key, item))) = (destination, state.get_key_value(source)) {
            let key = Key::new(destination.clone())
                .with_leaf_decor(key.leaf_decor().clone())
                .with_dotted_decor(key.dotted_decor().clone());
            replacements.push((key, item.clone()));
        }
    }
    let state = config
        .get_mut("hooks")
        .and_then(|hooks| hooks.as_table_like_mut())
        .and_then(|hooks| hooks.get_mut("state"))
        .and_then(|state| state.as_table_like_mut())
        .expect("validated hooks.state table");
    for (source, destination) in &plan {
        if destination.as_ref() != Some(source) {
            state.remove(source);
        }
    }
    for (key, item) in replacements {
        // Every destination is now vacant; entry_format keeps the original key comments.
        state.entry_format(&key).or_insert(item);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    const PATH: &str = "/home/test/.codex/hooks.json";

    fn key(group: usize, handler: usize) -> String {
        format!("{PATH}:pre_tool_use:{group}:{handler}")
    }

    #[test]
    fn mixed_removals_preserve_survivor_values_and_comments_verbatim() {
        let prefix = "# user config\nmodel = 'custom'\n";
        let removed = format!(
            "\n[hooks.state.\"{}\"] # removed Prism\ntrusted_hash = 'sha256:prism'\n",
            key(0, 0)
        );
        let disabled = format!(
            "\n# keep this disabled hook\n[hooks.state.\"{}\"] # header comment\nenabled = false # user disabled it\ntrusted_hash = 'sha256:keep' # exact hash syntax\nunknown = {{ count = 0x2a, labels = ['a', 'b'] }}\n",
            key(0, 1)
        );
        let removed_again = format!("\n[hooks.state.\"{}\"]\nenabled = true\n", key(0, 2));
        let trusted = format!(
            "\n[hooks.state.\"{}\"]\ntrusted_hash = \"sha256:other\" # keep this too\n",
            key(0, 3)
        );
        let mut config: DocumentMut =
            format!("{prefix}{removed}{disabled}{removed_again}{trusted}")
                .parse()
                .unwrap();

        remap(
            &mut config,
            Path::new(PATH),
            &[(0, 0, None), (0, 1, Some(0)), (0, 2, None), (0, 3, Some(1))],
        )
        .unwrap();

        let expected = format!(
            "{prefix}{}{}",
            disabled.replace(&key(0, 1), &key(0, 0)),
            trusted.replace(&key(0, 3), &key(0, 1))
        );
        assert_eq!(config.to_string(), expected);
        let reparsed: DocumentMut = config.to_string().parse().unwrap();
        assert_eq!(
            reparsed["hooks"]["state"][&key(0, 0)]["enabled"].as_bool(),
            Some(false)
        );
        assert!(reparsed["hooks"]["state"].get(key(0, 3)).is_none());
    }

    #[test]
    fn multiple_shifts_and_swaps_use_original_sources_in_any_order() {
        let mut text = String::new();
        for (group, handler, hash) in [
            (0, 0, "removed"),
            (0, 1, "first"),
            (0, 2, "second"),
            (1, 0, "left"),
            (1, 1, "right"),
        ] {
            text.push_str(&format!(
                "[hooks.state.\"{}\"]\ntrusted_hash = '{hash}'\n",
                key(group, handler)
            ));
        }
        let moves = [
            (0, 2, Some(1)),
            (1, 1, Some(0)),
            (0, 1, Some(0)),
            (0, 0, None),
            (1, 0, Some(1)),
        ];
        for moves in [moves.to_vec(), moves.into_iter().rev().collect()] {
            let mut config: DocumentMut = text.parse().unwrap();
            remap(&mut config, Path::new(PATH), &moves).unwrap();
            for (group, handler, expected) in [
                (0, 0, "first"),
                (0, 1, "second"),
                (1, 0, "right"),
                (1, 1, "left"),
            ] {
                assert_eq!(
                    config["hooks"]["state"][&key(group, handler)]["trusted_hash"].as_str(),
                    Some(expected)
                );
            }
            assert!(config["hooks"]["state"].get(key(0, 2)).is_none());
        }
    }

    #[test]
    fn missing_state_never_inherits_removed_trust_or_creates_trust() {
        let mut config: DocumentMut = format!(
            "[hooks.state.\"{}\"]\ntrusted_hash = 'removed'\n[hooks.state.\"{}\"]\nenabled = false\n",
            key(0, 0), key(0, 2)
        ).parse().unwrap();
        remap(
            &mut config,
            Path::new(PATH),
            &[(0, 0, None), (0, 1, Some(0)), (0, 2, Some(1))],
        )
        .unwrap();
        let state = &config["hooks"]["state"];
        assert!(state.get(key(0, 0)).is_none());
        assert_eq!(state[&key(0, 1)]["enabled"].as_bool(), Some(false));
        assert!(state[&key(0, 1)].get("trusted_hash").is_none());
        assert!(!config.to_string().contains("trusted_hash"));

        for text in [
            "model = 'custom'\n",
            "[hooks]\n# no state yet\n",
            "[hooks.state]\n",
        ] {
            let mut config: DocumentMut = text.parse().unwrap();
            remap(&mut config, Path::new(PATH), &[(0, 1, Some(0))]).unwrap();
            assert_eq!(config.to_string(), text);
        }
    }

    #[test]
    fn unrelated_paths_events_groups_and_handlers_are_untouched() {
        let mut text = String::from("# other hook states\n");
        for name in [
            format!("{PATH}:post_tool_use:0:1"),
            format!("{PATH}.other:pre_tool_use:0:1"),
            format!("file:{PATH}:pre_tool_use:0:1"),
            key(1, 1),
            key(0, 7),
        ] {
            text.push_str(&format!(
                "\n[hooks.state.'{name}'] # untouched\nenabled = false\ntrusted_hash = 'keep'\n"
            ));
        }
        let moved = format!(
            "\n[hooks.state.\"{}\"]\ntrusted_hash = 'moved'\n",
            key(0, 1)
        );
        let mut config: DocumentMut = format!("{text}{moved}").parse().unwrap();
        remap(&mut config, Path::new(PATH), &[(0, 1, Some(0))]).unwrap();
        assert_eq!(
            config.to_string(),
            format!("{text}{}", moved.replace(&key(0, 1), &key(0, 0)))
        );
    }

    #[test]
    fn inline_state_preserves_key_comments_and_entire_values() {
        let mut config: DocumentMut = format!(
            "[hooks.state]\n# attached to the moved key\n\"{}\" = {{ enabled = false, trusted_hash = 'keep', extra = [1, 2] }} # trailing\n",
            key(0, 1)
        ).parse().unwrap();
        let expected = config.to_string().replace(&key(0, 1), &key(0, 0));
        remap(&mut config, Path::new(PATH), &[(0, 1, Some(0))]).unwrap();
        assert_eq!(config.to_string(), expected);

        let text = format!(
            "hooks = {{ state = {{ \"{}\" = {{ enabled = false }} }} }}\n",
            key(0, 1)
        );
        let mut config: DocumentMut = text.parse().unwrap();
        remap(&mut config, Path::new(PATH), &[(0, 1, Some(0))]).unwrap();
        assert_eq!(config.to_string(), text.replace(&key(0, 1), &key(0, 0)));
    }

    #[test]
    fn collisions_fail_before_any_mutation_even_with_missing_sources() {
        let text = format!(
            "# unchanged on failure\n[hooks.state.\"{}\"]\ntrusted_hash = 'zero'\n[hooks.state.\"{}\"]\nenabled = false\n[hooks.state.\"{}\"]\ntrusted_hash = 'stale'\n",
            key(0, 0), key(0, 1), key(0, 4)
        );
        for moves in [
            vec![(0, 0, None), (0, 0, Some(2))],    // Duplicate source.
            vec![(0, 0, Some(2)), (0, 1, Some(2))], // Duplicate destination.
            vec![(0, 0, None), (0, 1, Some(4))],    // Occupied destination.
            vec![(0, 0, None), (0, 1, Some(0)), (0, 2, Some(4))], // Missing source, stale destination.
            vec![(0, 0, Some(0)), (0, 1, Some(0))], // Cannot overwrite an identity move.
        ] {
            let mut config: DocumentMut = text.parse().unwrap();
            assert!(remap(&mut config, Path::new(PATH), &moves).is_err());
            assert_eq!(config.to_string(), text);
        }
    }

    #[test]
    fn invalid_state_containers_fail_without_changes() {
        for text in ["hooks = false\n", "[hooks]\nstate = 'invalid'\n"] {
            let mut config: DocumentMut = text.parse().unwrap();
            assert!(remap(&mut config, Path::new(PATH), &[(0, 1, Some(0))]).is_err());
            assert_eq!(config.to_string(), text);
        }
    }

    #[test]
    fn empty_and_identity_moves_are_exact_noops() {
        let text = format!(
            "[hooks.state.'{}'] # keep quoting\ntrusted_hash = 'keep'\n",
            key(0, 0)
        );
        let mut config: DocumentMut = text.parse().unwrap();
        remap(&mut config, Path::new(PATH), &[]).unwrap();
        remap(&mut config, Path::new(PATH), &[(0, 0, Some(0))]).unwrap();
        assert_eq!(config.to_string(), text);
    }
}
