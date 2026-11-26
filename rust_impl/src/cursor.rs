//! Opaque keyset cursors: base64url(JSON{sort key, direction, last sort value, last id}).
//!
//! The cursor is bound to the sort it was produced for; using it with another sort
//! (or another endpoint) is rejected with 400 instead of silently returning garbage.

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::error::AppError;

const MAX_CURSOR_LEN: usize = 2048;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Cursor {
    /// Sort key the cursor belongs to (e.g. `books:title`).
    #[serde(rename = "k")]
    pub key: String,
    /// true = descending.
    #[serde(rename = "d")]
    pub desc: bool,
    /// Last seen value of the sort expression (string / number / array).
    #[serde(rename = "v")]
    pub value: Value,
    /// Last seen id (tie-breaker), as a string to survive JS clients.
    #[serde(rename = "i")]
    pub id: String,
}

impl Cursor {
    pub fn new(key: impl Into<String>, desc: bool, value: Value, id: i64) -> Self {
        Self { key: key.into(), desc, value, id: id.to_string() }
    }

    pub fn encode(&self) -> String {
        URL_SAFE_NO_PAD.encode(serde_json::to_vec(self).unwrap_or_default())
    }

    pub fn decode(raw: &str) -> Result<Self, AppError> {
        if raw.is_empty() || raw.len() > MAX_CURSOR_LEN {
            return Err(AppError::bad_param("cursor", "malformed cursor"));
        }
        let bytes = URL_SAFE_NO_PAD
            .decode(raw.trim_end_matches('='))
            .map_err(|_| AppError::bad_param("cursor", "malformed cursor (not base64url)"))?;
        let c: Cursor = serde_json::from_slice(&bytes).map_err(|_| AppError::bad_param("cursor", "malformed cursor (bad payload)"))?;
        if c.id_value().is_none() {
            return Err(AppError::bad_param("cursor", "malformed cursor (bad id)"));
        }
        Ok(c)
    }

    /// Decodes and checks that the cursor matches the requested sort.
    pub fn decode_for(raw: &str, key: &str, desc: bool) -> Result<Self, AppError> {
        let c = Self::decode(raw)?;
        if c.key != key || c.desc != desc {
            return Err(AppError::bad_param("cursor", "cursor was issued for a different sort order or endpoint"));
        }
        Ok(c)
    }

    pub fn id_value(&self) -> Option<i64> {
        crate::ids::parse_id(&self.id)
    }

    pub fn value_str(&self) -> Result<&str, AppError> {
        self.value.as_str().ok_or_else(|| AppError::bad_param("cursor", "malformed cursor (value)"))
    }

    pub fn value_i64(&self) -> Result<i64, AppError> {
        self.value.as_i64().ok_or_else(|| AppError::bad_param("cursor", "malformed cursor (value)"))
    }

    pub fn value_f64(&self) -> Result<f64, AppError> {
        self.value.as_f64().ok_or_else(|| AppError::bad_param("cursor", "malformed cursor (value)"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn roundtrip() {
        let c = Cursor::new("books:title", false, json!("Война и мир 東京"), 1234567890123);
        let enc = c.encode();
        assert!(!enc.contains('=') && !enc.contains('+') && !enc.contains('/'));
        let dec = Cursor::decode(&enc).unwrap();
        assert_eq!(dec, c);
        assert_eq!(dec.id_value(), Some(1234567890123));
        assert_eq!(dec.value_str().unwrap(), "Война и мир 東京");
    }

    #[test]
    fn float_roundtrip_is_exact() {
        let r = 0.123_456_789_012_345_67_f64;
        let c = Cursor::new("search:all", true, json!(r), 1);
        let dec = Cursor::decode(&c.encode()).unwrap();
        assert_eq!(dec.value_f64().unwrap().to_bits(), r.to_bits());
    }

    #[test]
    fn rejects_garbage() {
        assert!(Cursor::decode("").is_err());
        assert!(Cursor::decode("!!!").is_err());
        assert!(Cursor::decode(&URL_SAFE_NO_PAD.encode(b"not json")).is_err());
        assert!(Cursor::decode(&URL_SAFE_NO_PAD.encode(br#"{"k":"a","d":false,"v":1,"i":"x"}"#)).is_err());
        assert!(Cursor::decode(&"a".repeat(5000)).is_err());
    }

    #[test]
    fn padded_input_is_accepted() {
        let c = Cursor::new("k", true, json!(5), 9);
        let padded = format!("{}==", c.encode());
        assert_eq!(Cursor::decode(&padded).unwrap(), c);
    }

    #[test]
    fn sort_mismatch_rejected() {
        let enc = Cursor::new("books:title", false, json!("a"), 1).encode();
        assert!(Cursor::decode_for(&enc, "books:title", false).is_ok());
        assert!(Cursor::decode_for(&enc, "books:title", true).is_err());
        assert!(Cursor::decode_for(&enc, "books:created_at", false).is_err());
    }

    #[test]
    fn typed_accessors() {
        let c = Cursor::new("k", false, json!(1999), 1);
        assert_eq!(c.value_i64().unwrap(), 1999);
        assert!(c.value_str().is_err());
        let c = Cursor::new("k", false, json!("x"), 1);
        assert!(c.value_i64().is_err());
        assert!(c.value_f64().is_err());
    }
}
