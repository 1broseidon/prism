/// Redact recognizable credentials without evaluating shell syntax. Quoted values remain one
/// unit, so a multiword password cannot leak through whitespace splitting. This is a display
/// summary, not a shell sanitizer; raw file/edit/patch bodies are discarded by the wire parser.
pub fn redact(text: &str) -> String {
    let mut out = Vec::new();
    let mut secret_next = false;
    for raw in words(text) {
        let trail_at = raw.trim_end_matches(['\'', '"', ';', ')', ',', '`']).len();
        let (token, trail) = raw.split_at(trail_at);
        let bare = token.trim_start_matches(['\'', '"', '`']);
        let lower = bare.to_ascii_lowercase();
        let next = matches!(lower.as_str(), "bearer" | "basic" | "--user" | "-u")
            || (lower.starts_with('-') && !lower.contains('=') && secret_key(&lower));
        let replaced = if secret_next {
            "***".to_owned()
        } else if let Some((key, value)) = token
            .split_once('=')
            .filter(|(key, _)| !key.contains("://"))
        {
            if secret_key(key) {
                format!("{key}=***")
            } else {
                format!("{key}={}", redact_value(value))
            }
        } else if let Some((key, value)) = token.split_once(':').filter(|(key, _)| {
            secret_key(key)
                || key
                    .trim_start_matches(['\'', '"'])
                    .eq_ignore_ascii_case("authorization")
        }) {
            let scheme = value.split_whitespace().next().filter(|word| {
                word.eq_ignore_ascii_case("bearer") || word.eq_ignore_ascii_case("basic")
            });
            match scheme {
                Some(scheme) => format!("{key}: {scheme} ***"),
                None => format!("{key}: ***"),
            }
        } else {
            redact_value(token)
        };
        secret_next = next;
        out.push(format!("{replaced}{trail}"));
    }
    out.join(" ")
}

fn secret_key(key: &str) -> bool {
    let key = key.to_ascii_lowercase();
    [
        "key",
        "token",
        "secret",
        "password",
        "passwd",
        "pwd",
        "credential",
    ]
    .iter()
    .any(|needle| key.contains(needle))
}

fn redact_value(value: &str) -> String {
    if let Some((scheme, rest)) = value.split_once("://") {
        // Queries and fragments routinely carry credentials, including signed URLs.
        let end = rest.find(['?', '#']).unwrap_or(rest.len());
        let (rest, query) = rest.split_at(end);
        let authority_end = rest.find('/').unwrap_or(rest.len());
        let rest = match rest[..authority_end].rfind('@') {
            Some(at) => format!("***@{}", &rest[at + 1..]),
            None => rest.to_owned(),
        };
        return format!(
            "{scheme}://{rest}{}",
            if query.is_empty() { "" } else { "?***" }
        );
    }
    let bare = value.trim_matches(['\'', '"', '`']);
    let known_token = credential_prefix(bare);
    let opaque = bare.len() >= 32
        && !bare.starts_with(['/', '~', '.'])
        && bare
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '/' | '=' | '-' | '_'));
    if known_token || opaque {
        "***".to_owned()
    } else {
        value.to_owned()
    }
}

fn credential_prefix(value: &str) -> bool {
    [
        "sk-",
        "sk_",
        "ghp_",
        "gho_",
        "github_pat_",
        "xoxb-",
        "xoxp-",
        "AKIA",
    ]
    .iter()
    .any(|prefix| value.starts_with(prefix))
}

/// Validated, bounded identifiers routinely include UUIDs and long MCP namespaces. Preserve
/// those for correlation; an opaque command argument and a typed identifier are different data.
pub(crate) fn redact_identifier(value: &str) -> String {
    if credential_prefix(value) {
        "***".to_owned()
    } else {
        value.to_owned()
    }
}

fn words(text: &str) -> Vec<&str> {
    let mut words = Vec::new();
    let mut start = None;
    let mut quote = None;
    let mut escaped = false;
    for (i, ch) in text.char_indices() {
        if escaped {
            escaped = false;
        } else if ch == '\\' && quote != Some('\'') {
            escaped = true;
        } else if matches!(ch, '\'' | '"') {
            if quote == Some(ch) {
                quote = None;
            } else if quote.is_none() {
                quote = Some(ch);
            }
        } else if ch.is_whitespace() && quote.is_none() {
            if let Some(begin) = start.take() {
                words.push(&text[begin..i]);
            }
            continue;
        }
        start.get_or_insert(i);
    }
    if let Some(begin) = start {
        words.push(&text[begin..]);
    }
    words
}
