//! Edit only changed JSON/JSONC nodes; keep the user's comments and layout.
use jsonc_parser::{
    cst::{CstInputValue, CstNode, CstRootNode},
    ParseOptions,
};
use serde_json::Value;
use std::path::Path;

pub(super) fn parse(bytes: Option<&[u8]>, path: &Path) -> Result<CstRootNode, String> {
    let text = std::str::from_utf8(bytes.unwrap_or(b"{}"))
        .map_err(|_| format!("{} must be UTF-8", path.display()))?;
    let root = CstRootNode::parse(text, &ParseOptions::default())
        .map_err(|_| format!("Fix invalid JSON in {} first", path.display()))?;
    let node = root.value().ok_or("Settings must contain a JSON object")?;
    if node.as_object().is_none() || !unambiguous(&node) {
        return Err(format!(
            "{} must be an object without duplicate keys",
            path.display()
        ));
    }
    Ok(root)
}

fn unambiguous(node: &CstNode) -> bool {
    if let Some(object) = node.as_object() {
        let properties = object.properties();
        let count = object
            .to_serde_value()
            .and_then(|v| v.as_object().map(|m| m.len()));
        count == Some(properties.len())
            && properties
                .iter()
                .all(|p| p.value().is_some_and(|n| unambiguous(&n)))
    } else if let Some(array) = node.as_array() {
        array.elements().iter().all(unambiguous)
    } else {
        node.to_serde_value().is_some()
    }
}

pub(super) fn value(bytes: Option<&[u8]>, path: &Path) -> Result<Value, String> {
    parse(bytes, path)?
        .to_serde_value()
        .ok_or_else(|| "Invalid settings value".into())
}

pub(super) fn update(
    bytes: Option<&[u8]>,
    path: &Path,
    desired: &Value,
) -> Result<Vec<u8>, String> {
    let root = parse(bytes, path)?;
    if !sync(&root.value().unwrap(), desired) {
        return Err("Settings must remain an object".into());
    }
    let text = root.to_string();
    if value(Some(text.as_bytes()), path)? != *desired {
        return Err("Could not safely update settings".into());
    }
    Ok(text.into_bytes())
}

fn input(value: &Value) -> CstInputValue {
    match value {
        Value::Null => CstInputValue::Null,
        Value::Bool(v) => CstInputValue::Bool(*v),
        Value::Number(v) => CstInputValue::Number(v.to_string()),
        Value::String(v) => CstInputValue::String(v.clone()),
        Value::Array(v) => CstInputValue::Array(v.iter().map(input).collect()),
        Value::Object(v) => {
            CstInputValue::Object(v.iter().map(|(k, v)| (k.clone(), input(v))).collect())
        }
    }
}

fn sync(node: &CstNode, desired: &Value) -> bool {
    if node.to_serde_value().as_ref() == Some(desired) {
        return true;
    }
    if let (Some(object), Some(values)) = (node.as_object(), desired.as_object()) {
        for prop in object.properties() {
            let Some(name) = prop.name().and_then(|n| n.decoded_value().ok()) else {
                return false;
            };
            match values.get(&name) {
                None => prop.remove(),
                Some(value) => {
                    if !prop.value().is_some_and(|n| sync(&n, value)) {
                        prop.set_value(input(value));
                    }
                }
            }
        }
        for (name, value) in values {
            if object.get(name).is_none() {
                object.append(name, input(value));
            }
        }
        return true;
    }
    if let (Some(array), Some(values)) = (node.as_array(), desired.as_array()) {
        for (i, value) in values.iter().enumerate() {
            let nodes = array.elements();
            let Some(current) = nodes.get(i) else {
                array.append(input(value));
                continue;
            };
            if current.to_serde_value().as_ref() == Some(value) {
                continue;
            }
            // Retain matching nodes, including comments inside other hooks.
            if let Some(offset) = nodes[i + 1..]
                .iter()
                .position(|n| n.to_serde_value().as_ref() == Some(value))
            {
                for n in &nodes[i..=i + offset] {
                    n.clone().remove();
                }
            } else if current
                .to_serde_value()
                .is_some_and(|v| values[i + 1..].contains(&v))
            {
                array.insert(i, input(value));
            } else if !sync(current, value) {
                current.clone().remove();
                array.insert(i, input(value));
            }
        }
        for n in array.elements().into_iter().skip(values.len()) {
            n.remove();
        }
        return true;
    }
    false
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn preserves_comments_and_rejects_ambiguous_files() {
        let path = Path::new("settings.jsonc");
        let old = b"{\n // user preference\n \"theme\": \"dark\",\n \"hooks\": [{\"command\":\"prism\"}, { /* keep inside */ \"command\": \"other\" }],\n}\n";
        let wanted = json!({"theme":"dark","hooks":[{"command":"other"}]});
        let new = update(Some(old), path, &wanted).unwrap();
        let text = String::from_utf8(new.clone()).unwrap();
        assert!(text.contains("// user preference"));
        assert!(text.contains("/* keep inside */"));
        assert_eq!(update(Some(&new), path, &wanted).unwrap(), new);
        assert!(value(Some(br#"{"mcp":{},"mcp":{}}"#), path).is_err());
        assert!(value(Some(br#"{"mcp":{"prism":{},"prism":{}}}"#), path).is_err());
    }
}
