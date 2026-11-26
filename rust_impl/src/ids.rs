//! Snowflake-like 64-bit identifiers: 41 bits of milliseconds since a custom epoch,
//! 10 bits of node id, 12 bits of per-millisecond sequence. They are monotonic per
//! node (B-tree friendly) and globally unique across nodes without coordination.
//!
//! On the wire ids are serialized as JSON strings, because values above 2^53 cannot
//! be represented exactly by JavaScript numbers.

use std::sync::Mutex;

use chrono::{DateTime, TimeZone, Utc};
use serde::{Deserialize, Deserializer, Serializer, de};

/// 2020-01-01T00:00:00Z in unix milliseconds.
pub const EPOCH_MS: i64 = 1_577_836_800_000;
pub const NODE_BITS: u32 = 10;
pub const SEQ_BITS: u32 = 12;
pub const MAX_NODE_ID: u16 = (1 << NODE_BITS) - 1;
const MAX_SEQ: i64 = (1 << SEQ_BITS) - 1;

/// Thread-safe id generator owned by the application state (no globals).
#[derive(Debug)]
pub struct IdGenerator {
    node: i64,
    state: Mutex<(i64, i64)>, // (last_ms, seq)
}

impl IdGenerator {
    pub fn new(node: u16) -> Self {
        assert!(node <= MAX_NODE_ID, "node id out of range");
        Self { node: i64::from(node), state: Mutex::new((0, 0)) }
    }

    pub fn next_id(&self) -> i64 {
        self.next_at(Utc::now().timestamp_millis())
    }

    fn next_at(&self, now_ms: i64) -> i64 {
        let mut st = self.state.lock().unwrap_or_else(std::sync::PoisonError::into_inner);
        let mut ms = now_ms.max(st.0); // never go backwards on clock skew
        if ms == st.0 {
            st.1 += 1;
            if st.1 > MAX_SEQ {
                // sequence exhausted for this millisecond: borrow the next one
                ms += 1;
                st.1 = 0;
            }
        } else {
            st.1 = 0;
        }
        st.0 = ms;
        compose(ms, self.node, st.1)
    }
}

/// Builds an id from its components (also used by the seeder).
pub fn compose(unix_ms: i64, node: i64, seq: i64) -> i64 {
    ((unix_ms - EPOCH_MS) << (NODE_BITS + SEQ_BITS)) | ((node & i64::from(MAX_NODE_ID)) << SEQ_BITS) | (seq & MAX_SEQ)
}

/// Extracts the creation timestamp embedded in an id.
pub fn timestamp_of(id: i64) -> DateTime<Utc> {
    let ms = (id >> (NODE_BITS + SEQ_BITS)) + EPOCH_MS;
    Utc.timestamp_millis_opt(ms).single().unwrap_or_default()
}

/// Parses an id coming from a URL path or a query parameter.
pub fn parse_id(s: &str) -> Option<i64> {
    let v: i64 = s.trim().parse().ok()?;
    (v > 0).then_some(v)
}

/// serde helpers: i64 <-> JSON string (accepts numbers on input too).
pub mod as_string {
    use super::*;

    pub fn serialize<S: Serializer>(v: &i64, s: S) -> Result<S::Ok, S::Error> {
        s.collect_str(v)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<i64, D::Error> {
        #[derive(Deserialize)]
        #[serde(untagged)]
        enum Raw {
            S(String),
            N(i64),
        }
        match Raw::deserialize(d)? {
            Raw::N(n) if n > 0 => Ok(n),
            Raw::S(s) => parse_id(&s).ok_or_else(|| de::Error::custom("id must be a positive 64-bit integer")),
            Raw::N(_) => Err(de::Error::custom("id must be a positive 64-bit integer")),
        }
    }
}

/// serde helpers for `Option<i64>` ids.
pub mod as_string_opt {
    use super::*;

    pub fn serialize<S: Serializer>(v: &Option<i64>, s: S) -> Result<S::Ok, S::Error> {
        match v {
            Some(v) => s.collect_str(v),
            None => s.serialize_none(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ids_are_monotonic_and_unique() {
        let g = IdGenerator::new(7);
        let mut prev = 0;
        for _ in 0..10_000 {
            let id = g.next_id();
            assert!(id > prev);
            prev = id;
        }
    }

    #[test]
    fn sequence_overflow_borrows_next_ms() {
        let g = IdGenerator::new(1);
        let t = EPOCH_MS + 1000;
        let mut last = 0;
        for _ in 0..5000 {
            let id = g.next_at(t);
            assert!(id > last);
            last = id;
        }
        assert!(timestamp_of(last).timestamp_millis() > t);
    }

    #[test]
    fn clock_going_backwards_is_tolerated() {
        let g = IdGenerator::new(1);
        let a = g.next_at(EPOCH_MS + 5000);
        let b = g.next_at(EPOCH_MS + 1000);
        assert!(b > a);
    }

    #[test]
    fn compose_roundtrip() {
        let ms = EPOCH_MS + 123_456_789;
        let id = compose(ms, 3, 9);
        assert_eq!(timestamp_of(id).timestamp_millis(), ms);
        assert_eq!((id >> SEQ_BITS) & i64::from(MAX_NODE_ID), 3);
        assert_eq!(id & MAX_SEQ, 9);
    }

    #[test]
    fn parse_id_rules() {
        assert_eq!(parse_id("42"), Some(42));
        assert_eq!(parse_id("0"), None);
        assert_eq!(parse_id("-1"), None);
        assert_eq!(parse_id("abc"), None);
        assert_eq!(parse_id("99999999999999999999"), None);
    }

    #[test]
    fn serde_string_ids() {
        #[derive(Deserialize, serde::Serialize)]
        struct X {
            #[serde(with = "as_string")]
            id: i64,
        }
        let x: X = serde_json::from_str(r#"{"id":"123"}"#).unwrap();
        assert_eq!(x.id, 123);
        let x: X = serde_json::from_str(r#"{"id":124}"#).unwrap();
        assert_eq!(x.id, 124);
        assert!(serde_json::from_str::<X>(r#"{"id":"x"}"#).is_err());
        assert!(serde_json::from_str::<X>(r#"{"id":-5}"#).is_err());
        assert_eq!(serde_json::to_string(&X { id: 5 }).unwrap(), r#"{"id":"5"}"#);
    }
}
