use crate::{AgentConfig, Error, Result};

pub(crate) const MAX: usize = 80;

pub(crate) fn display(raw: &str) -> Result<String> {
    let name = raw.trim();
    if name.is_empty() || name.chars().count() > MAX || name.chars().any(char::is_control) {
        return Err(Error::Invalid(
            "choose a name with 1 to 80 characters and no control characters".into(),
        ));
    }
    Ok(name.into())
}

pub(crate) fn same(left: &str, right: &str) -> bool {
    unicase::UniCase::new(left) == unicase::UniCase::new(right)
}

pub(crate) fn agent(raw: &str, agents: &[AgentConfig], replacing: Option<&str>) -> Result<String> {
    let name = display(raw)?;
    if agents
        .iter()
        .any(|agent| Some(agent.id.as_str()) != replacing && same(&agent.name, &name))
    {
        return Err(Error::AlreadyExists(
            "an agent already uses that name".into(),
        ));
    }
    Ok(name)
}

pub(crate) fn unique(raw: &str, agents: &[AgentConfig], excluding: Option<&str>) -> String {
    let base: String = raw
        .trim()
        .chars()
        .filter(|c| !c.is_control())
        .take(MAX)
        .collect();
    let base = if base.trim().is_empty() {
        "unknown"
    } else {
        base.trim()
    };
    let mut name = base.to_string();
    let mut n = 2;
    while agents
        .iter()
        .any(|agent| Some(agent.id.as_str()) != excluding && same(&agent.name, &name))
    {
        let suffix = format!(" ({n})");
        let prefix: String = base
            .chars()
            .take(MAX.saturating_sub(suffix.chars().count()))
            .collect();
        name = format!("{}{suffix}", prefix.trim_end());
        n += 1;
    }
    name
}

pub(crate) fn server(raw: &str) -> Result<String> {
    let name = display(raw)?;
    if name.contains("__") || name.ends_with('_') {
        return Err(Error::Invalid("server names cannot contain double underscores or end with an underscore; these separate tool names".into()));
    }
    Ok(name)
}

#[cfg(test)]
#[path = "name_tests.rs"]
mod tests;
