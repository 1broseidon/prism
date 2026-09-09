use std::cell::RefCell;
use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::action::{resolved_path, under, walk};
use super::{glob_match, Action, HostScope, PathAccess};

/// Externally tagged to preserve the JSON file grammar. Unknown keys are never ignored.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case", deny_unknown_fields)]
pub enum Condition {
    All(Vec<Condition>),
    Any(Vec<Condition>),
    Not(Box<Condition>),
    Tag(String),
    Path(PathCondition),
    Host(HostCondition),
    Arg(ArgCondition),
    Command(CommandCondition),
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PathCondition {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub under: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub outside_cwd: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub access: Option<Vec<PathAccess>>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct HostCondition {
    #[serde(rename = "in", default, skip_serializing_if = "Option::is_none")]
    pub hosts: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub scope: Option<Vec<HostScope>>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ArgCondition {
    pub pointer: String,
    #[serde(
        default,
        deserialize_with = "present_value",
        skip_serializing_if = "Option::is_none"
    )]
    pub equals: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub starts_with: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub matches: Option<String>,
}

fn present_value<'de, D: serde::Deserializer<'de>>(d: D) -> Result<Option<Value>, D::Error> {
    Value::deserialize(d).map(Some)
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct CommandCondition {
    pub program_in: Vec<String>,
}

impl Condition {
    pub fn parse(value: &Value) -> Result<Self, String> {
        // Bound recursive deserialization even for hand-edited, deeply nested values.
        fn bounded(v: &Value, depth: usize, budget: &mut usize) -> bool {
            if depth > 32 || *budget == 0 {
                return false;
            }
            *budget -= 1;
            match v {
                Value::Object(m) => m.values().all(|v| bounded(v, depth + 1, budget)),
                Value::Array(a) => a.iter().all(|v| bounded(v, depth + 1, budget)),
                _ => true,
            }
        }
        if !bounded(value, 0, &mut 1024) {
            return Err("Condition exceeds size or depth limit".into());
        }
        fn nonnull_fields(v: &Value) -> bool {
            let Some(c) = v.as_object() else {
                return false;
            };
            c.iter().all(|(key, v)| match key.as_str() {
                "all" | "any" => v.as_array().is_some_and(|a| a.iter().all(nonnull_fields)),
                "not" => nonnull_fields(v),
                "path" | "host" | "arg" | "command" => v
                    .as_object()
                    .is_some_and(|o| o.iter().all(|(k, v)| k == "equals" || !v.is_null())),
                _ => true,
            })
        }
        if !nonnull_fields(value) {
            return Err("Malformed condition predicate".into());
        }
        let parsed: Self = serde_json::from_value(value.clone())
            .map_err(|_| "Unknown or malformed condition".to_string())?;
        if !parsed.valid() {
            return Err("Malformed condition predicate".into());
        }
        Ok(parsed)
    }

    fn valid(&self) -> bool {
        match self {
            Self::All(c) | Self::Any(c) => c.iter().all(Self::valid),
            Self::Not(c) => c.valid(),
            Self::Tag(_) | Self::Command(_) => true,
            Self::Path(p) => {
                p.under.as_ref().is_none_or(|p| !p.is_empty())
                    && (p.under.is_some() || p.outside_cwd.is_some() || p.access.is_some())
            }
            Self::Host(h) => h.hosts.is_some() || h.scope.is_some(),
            Self::Arg(a) => {
                let operations = usize::from(a.equals.is_some())
                    + usize::from(a.starts_with.is_some())
                    + usize::from(a.matches.is_some());
                operations == 1
                    && (a.pointer.is_empty() || a.pointer.starts_with('/'))
                    && a.pointer
                        .split('~')
                        .skip(1)
                        .all(|s| s.starts_with('0') || s.starts_with('1'))
            }
        }
    }

    pub fn matches(&self, action: &Action) -> bool {
        match self {
            Self::All(c) => c.iter().all(|c| c.matches(action)),
            Self::Any(c) => c.iter().any(|c| c.matches(action)),
            Self::Not(c) => !c.matches(action),
            Self::Tag(_) => false,
            Self::Path(p) => {
                let prefix = p
                    .under
                    .as_ref()
                    .map(|p| resolved_path(p, action.context.cwd.as_deref()));
                let cwd = p
                    .outside_cwd
                    .and(action.context.cwd.as_ref())
                    .map(|p| p.to_string_lossy());
                action.facets.paths.iter().any(|f| {
                    let path = resolved_path(&f.path, action.context.cwd.as_deref());
                    prefix.as_ref().is_none_or(|p| under(&path, p))
                        && p.outside_cwd.is_none_or(|outside| {
                            cwd.as_ref().is_some_and(|cwd| {
                                under(&path, &resolved_path(cwd, None)) != outside
                            })
                        })
                        && p.access.as_ref().is_none_or(|a| a.contains(&f.access))
                })
            }
            Self::Host(h) => action.facets.hosts.iter().any(|f| {
                h.hosts.as_ref().is_none_or(|hosts| {
                    !f.host.is_empty()
                        && hosts.iter().any(|h| {
                            glob_match(&h.to_ascii_lowercase(), &f.host.to_ascii_lowercase())
                        })
                }) && h.scope.as_ref().is_none_or(|s| s.contains(&f.scope))
            }),
            Self::Arg(a) => action.arguments.pointer(&a.pointer).is_some_and(|v| {
                a.equals.as_ref().is_none_or(|e| v == e)
                    && a.starts_with
                        .as_ref()
                        .is_none_or(|s| v.as_str().is_some_and(|v| v.starts_with(s)))
                    && a.matches
                        .as_ref()
                        .is_none_or(|s| v.as_str().is_some_and(|v| glob_match(s, v)))
            }),
            Self::Command(c) => {
                let mut found = false;
                walk(&action.arguments, "", 0, &mut |value, pointer| {
                    let name = pointer.rsplit('/').next().unwrap_or("");
                    let named = name.eq_ignore_ascii_case("command")
                        || name.eq_ignore_ascii_case("cmd")
                        || pointer.to_ascii_lowercase().ends_with("/args/0");
                    if named {
                        // This is a program hint, not a shell parser or shell-safety claim.
                        let first = if pointer.to_ascii_lowercase().ends_with("/args/0") {
                            Some(value)
                        } else {
                            value.split_whitespace().next()
                        };
                        let program = first
                            .unwrap_or("")
                            .trim_matches(['\'', '"'])
                            .rsplit(['/', '\\'])
                            .next()
                            .unwrap_or("");
                        found = c.program_in.iter().any(|p| p == program);
                    }
                    !found
                });
                found
            }
        }
    }

    pub fn clause(&self) -> String {
        match self {
            Self::Path(p) => {
                let mut s = p
                    .under
                    .as_ref()
                    .map(|p| format!("path under {p}"))
                    .unwrap_or_else(|| {
                        if p.outside_cwd == Some(true) {
                            "path outside cwd".into()
                        } else {
                            "path".into()
                        }
                    });
                if let Some(a) = &p.access {
                    s.push_str(&format!(
                        " ({})",
                        a.iter().map(|a| a.label()).collect::<Vec<_>>().join(", ")
                    ));
                }
                s
            }
            Self::Host(h) => format!(
                "host {}",
                h.hosts.as_ref().map(|h| h.join(", ")).unwrap_or_else(|| h
                    .scope
                    .as_ref()
                    .unwrap()
                    .iter()
                    .map(|s| s.label())
                    .collect::<Vec<_>>()
                    .join(", "))
            ),
            Self::Tag(t) => t.clone(),
            Self::All(c) => c.iter().map(Self::clause).collect::<Vec<_>>().join(" and "),
            Self::Any(c) => c.iter().map(Self::clause).collect::<Vec<_>>().join(" or "),
            Self::Not(c) => format!("not ({})", c.clause()),
            Self::Arg(a) => format!("argument {}", a.pointer),
            Self::Command(c) => format!("program {}", c.program_in.join(", ")),
        }
    }
}

thread_local! {
    // Value equality keys the cache, so in-place config edits cannot leave stale compiled rules.
    static CONDITIONS: RefCell<HashMap<Value, Result<Condition, String>>> = RefCell::new(HashMap::new());
}

pub(super) fn condition_matches(value: &Value, action: &Action) -> bool {
    CONDITIONS.with(|cache| {
        let mut cache = cache.borrow_mut();
        if let Some(condition) = cache.get(value) {
            return condition.as_ref().is_ok_and(|c| c.matches(action));
        }
        if cache.len() >= 256 {
            cache.clear();
        }
        let condition = Condition::parse(value);
        let matched = condition.as_ref().is_ok_and(|c| c.matches(action));
        cache.insert(value.clone(), condition);
        matched
    })
}
