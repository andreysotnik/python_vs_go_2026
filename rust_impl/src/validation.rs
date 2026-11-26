//! Input normalization, validation, PATCH merging and audit diffs.

use std::collections::HashSet;

use chrono::NaiveDate;
use serde::Serialize;
use serde_json::{Map, Value};

use crate::{
    error::{AppError, FieldError},
    isbn,
    models::{AuthorData, AuthorPatch, BookData, BookPatch, ROLES, UserInput},
};

pub const MAX_AUTHORS_PER_BOOK: usize = 100;
pub const MAX_GENRES: usize = 20;
pub const MIN_YEAR: i32 = -3000;
pub const MAX_YEAR: i32 = 2100;

/// Collects field errors so the client gets all problems at once.
#[derive(Default)]
struct Errors(Vec<FieldError>);

impl Errors {
    fn add(&mut self, field: &str, msg: impl Into<String>) {
        self.0.push(FieldError::new(field, msg));
    }

    fn finish(self) -> Result<(), AppError> {
        if self.0.is_empty() { Ok(()) } else { Err(AppError::Validation(self.0)) }
    }
}

fn clean_required(errs: &mut Errors, field: &str, v: &mut String, max: usize) {
    *v = v.trim().to_string();
    if v.is_empty() {
        errs.add(field, "must not be empty");
    } else if v.chars().count() > max {
        errs.add(field, format!("must be at most {max} characters"));
    }
}

/// Trims an optional string; empty strings become null.
fn clean_optional(errs: &mut Errors, field: &str, v: &mut Option<String>, max: usize) {
    if let Some(s) = v.take() {
        let t = s.trim().to_string();
        if t.chars().count() > max {
            errs.add(field, format!("must be at most {max} characters"));
        }
        if !t.is_empty() {
            *v = Some(t);
        }
    }
}

pub fn is_language_code(s: &str) -> bool {
    (2..=3).contains(&s.len()) && s.bytes().all(|b| b.is_ascii_lowercase())
}

pub fn is_country_code(s: &str) -> bool {
    s.len() == 2 && s.bytes().all(|b| b.is_ascii_uppercase())
}

pub fn is_genre(s: &str) -> bool {
    !s.is_empty() && s.chars().count() <= 64 && s.chars().all(|c| c.is_alphanumeric() || matches!(c, ' ' | '-' | '&'))
}

fn validate_external_ids(errs: &mut Errors, v: &Value) {
    match v {
        Value::Object(m) => {
            if m.len() > 20 {
                errs.add("external_ids", "at most 20 identifiers allowed");
            }
            for (k, val) in m {
                let key_ok = !k.is_empty() && k.len() <= 32 && k.bytes().all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'_');
                if !key_ok {
                    errs.add("external_ids", format!("invalid key '{k}' (expected [a-z0-9_]{{1,32}}, e.g. viaf, isni, wikidata)"));
                }
                if !val.as_str().is_some_and(|s| !s.is_empty() && s.len() <= 256) {
                    errs.add("external_ids", format!("value of '{k}' must be a non-empty string up to 256 chars"));
                }
            }
        }
        _ => errs.add("external_ids", "must be a JSON object"),
    }
}

/// Normalizes and validates a complete book state in place.
pub fn validate_book(d: &mut BookData) -> Result<(), AppError> {
    let mut e = Errors::default();
    clean_required(&mut e, "title", &mut d.title, 1000);
    clean_optional(&mut e, "original_title", &mut d.original_title, 1000);
    clean_optional(&mut e, "subtitle", &mut d.subtitle, 1000);
    clean_optional(&mut e, "description", &mut d.description, 20000);
    clean_optional(&mut e, "publisher", &mut d.publisher, 512);
    clean_optional(&mut e, "edition", &mut d.edition, 200);

    d.language_code = d.language_code.trim().to_ascii_lowercase();
    if !is_language_code(&d.language_code) {
        e.add("language_code", "must be an ISO 639-1/639-3 code (2-3 latin letters), e.g. 'en', 'ru', 'zho'");
    }
    if let Some(y) = d.publication_year {
        if !(MIN_YEAR..=MAX_YEAR).contains(&y) {
            e.add("publication_year", format!("must be between {MIN_YEAR} and {MAX_YEAR}"));
        }
    }
    if let Some(p) = d.page_count {
        if !(1..=100_000).contains(&p) {
            e.add("page_count", "must be between 1 and 100000");
        }
    }

    // ISBN: normalize, validate check digits, derive the missing form.
    let i13 = d.isbn_13.as_deref().map(isbn::normalize).filter(|s| !s.is_empty());
    let i10 = d.isbn_10.as_deref().map(isbn::normalize).filter(|s| !s.is_empty());
    let mut ok = true;
    if let Some(s) = &i13 {
        if !isbn::is_valid_isbn13(s) {
            e.add("isbn_13", "invalid ISBN-13 (13 digits, 978/979 prefix, valid check digit)");
            ok = false;
        }
    }
    if let Some(s) = &i10 {
        if !isbn::is_valid_isbn10(s) {
            e.add("isbn_10", "invalid ISBN-10 (9 digits + check digit 0-9 or X)");
            ok = false;
        }
    }
    if ok {
        match (&i13, &i10) {
            (Some(a), Some(b)) if isbn::isbn10_to_13(b).as_deref() != Some(a.as_str()) => {
                e.add("isbn_10", "isbn_10 and isbn_13 refer to different books");
            }
            (None, Some(b)) => d.isbn_13 = isbn::isbn10_to_13(b),
            (Some(a), None) => d.isbn_10 = isbn::isbn13_to_10(a),
            _ => {}
        }
        if i13.is_some() {
            d.isbn_13 = i13;
        }
        if i10.is_some() {
            d.isbn_10 = i10;
        }
    }

    let mut seen = HashSet::new();
    let mut genres = Vec::with_capacity(d.genres.len());
    for g in &d.genres {
        let g = g.trim().to_lowercase();
        if !is_genre(&g) {
            e.add("genres", format!("invalid genre '{g}' (letters, digits, space, '-', '&'; max 64 chars)"));
        } else if seen.insert(g.clone()) {
            genres.push(g);
        }
    }
    if genres.len() > MAX_GENRES {
        e.add("genres", format!("at most {MAX_GENRES} genres allowed"));
    }
    d.genres = genres;

    validate_external_ids(&mut e, &d.external_ids);

    if d.authors.len() > MAX_AUTHORS_PER_BOOK {
        e.add("authors", format!("at most {MAX_AUTHORS_PER_BOOK} authors per book"));
    }
    let mut seen = HashSet::new();
    for (i, a) in d.authors.iter_mut().enumerate() {
        a.role = a.role.trim().to_lowercase();
        if !ROLES.contains(&a.role.as_str()) {
            e.add(&format!("authors[{i}].role"), format!("must be one of {}", ROLES.join(", ")));
        }
        let ord = a.ordinal.unwrap_or(i16::try_from(i + 1).unwrap_or(i16::MAX));
        if !(1..=1000).contains(&ord) {
            e.add(&format!("authors[{i}].ordinal"), "must be between 1 and 1000");
        }
        a.ordinal = Some(ord);
        if !seen.insert((a.author_id, a.role.clone())) {
            e.add(&format!("authors[{i}]"), "duplicate (author_id, role) pair");
        }
    }
    e.finish()
}

fn min_date() -> NaiveDate {
    NaiveDate::from_ymd_opt(MIN_YEAR, 1, 1).unwrap_or(NaiveDate::MIN)
}

fn max_date() -> NaiveDate {
    NaiveDate::from_ymd_opt(MAX_YEAR, 12, 31).unwrap_or(NaiveDate::MAX)
}

pub fn validate_author(d: &mut AuthorData) -> Result<(), AppError> {
    let mut e = Errors::default();
    clean_required(&mut e, "full_name", &mut d.full_name, 512);
    clean_optional(&mut e, "original_name", &mut d.original_name, 512);
    clean_optional(&mut e, "biography", &mut d.biography, 20000);
    if let Some(c) = d.country_code.take() {
        let c = c.trim().to_ascii_uppercase();
        if !is_country_code(&c) {
            e.add("country_code", "must be an ISO 3166-1 alpha-2 code, e.g. 'RU', 'JP'");
        }
        d.country_code = Some(c);
    }
    for (field, v) in [("birth_date", d.birth_date), ("death_date", d.death_date)] {
        if let Some(v) = v {
            if v < min_date() || v > max_date() {
                e.add(field, format!("must be between {MIN_YEAR}-01-01 and {MAX_YEAR}-12-31"));
            }
        }
    }
    if let (Some(b), Some(dd)) = (d.birth_date, d.death_date) {
        if dd < b {
            e.add("death_date", "must not be earlier than birth_date");
        }
    }
    validate_external_ids(&mut e, &d.external_ids);
    e.finish()
}

pub fn validate_user(u: &mut UserInput) -> Result<(), AppError> {
    let mut e = Errors::default();
    u.username = u.username.trim().to_string();
    u.email = u.email.trim().to_string();
    let uname_ok =
        (3..=64).contains(&u.username.len()) && u.username.bytes().all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'));
    if !uname_ok {
        e.add("username", "3-64 characters: latin letters, digits, '_', '.', '-'");
    }
    let parts: Vec<&str> = u.email.split('@').collect();
    let email_ok =
        u.email.len() <= 254 && parts.len() == 2 && !parts[0].is_empty() && !parts[1].is_empty() && !u.email.contains(char::is_whitespace);
    if !email_ok {
        e.add("email", "must be a valid e-mail address");
    }
    e.finish()
}

/// Applies a PATCH to a book state. Non-nullable fields reject explicit nulls.
pub fn apply_book_patch(mut d: BookData, p: BookPatch) -> Result<BookData, AppError> {
    let mut e = Errors::default();
    match p.title {
        Some(Some(v)) => d.title = v,
        Some(None) => e.add("title", "cannot be null"),
        None => {}
    }
    match p.language_code {
        Some(Some(v)) => d.language_code = v,
        Some(None) => e.add("language_code", "cannot be null"),
        None => {}
    }
    if let Some(v) = p.original_title {
        d.original_title = v;
    }
    if let Some(v) = p.subtitle {
        d.subtitle = v;
    }
    if let Some(v) = p.description {
        d.description = v;
    }
    // ISBN forms are linked: changing one resets the other so it gets re-derived.
    match (p.isbn_13, p.isbn_10) {
        (Some(a), Some(b)) => {
            d.isbn_13 = a;
            d.isbn_10 = b;
        }
        (Some(a), None) => {
            d.isbn_13 = a;
            d.isbn_10 = None;
        }
        (None, Some(b)) => {
            d.isbn_10 = b;
            d.isbn_13 = None;
        }
        (None, None) => {}
    }
    if let Some(v) = p.publication_year {
        d.publication_year = v;
    }
    if let Some(v) = p.publisher {
        d.publisher = v;
    }
    if let Some(v) = p.page_count {
        d.page_count = v;
    }
    if let Some(v) = p.genres {
        d.genres = v.unwrap_or_default();
    }
    if let Some(v) = p.edition {
        d.edition = v;
    }
    if let Some(v) = p.external_ids {
        d.external_ids = v.unwrap_or_else(|| Value::Object(Map::new()));
    }
    if let Some(v) = p.authors {
        d.authors = v.unwrap_or_default();
    }
    e.finish()?;
    Ok(d)
}

pub fn apply_author_patch(mut d: AuthorData, p: AuthorPatch) -> Result<AuthorData, AppError> {
    match p.full_name {
        Some(Some(v)) => d.full_name = v,
        Some(None) => return Err(AppError::validation_one("full_name", "cannot be null")),
        None => {}
    }
    if let Some(v) = p.original_name {
        d.original_name = v;
    }
    if let Some(v) = p.birth_date {
        d.birth_date = v;
    }
    if let Some(v) = p.death_date {
        d.death_date = v;
    }
    if let Some(v) = p.country_code {
        d.country_code = v;
    }
    if let Some(v) = p.biography {
        d.biography = v;
    }
    if let Some(v) = p.external_ids {
        d.external_ids = v.unwrap_or_else(|| Value::Object(Map::new()));
    }
    Ok(d)
}

/// Field-level diff `{field: {"old": .., "new": ..}}` of two serializable states.
pub fn diff<T: Serialize>(old: &T, new: &T) -> Value {
    let (Ok(Value::Object(a)), Ok(Value::Object(b))) = (serde_json::to_value(old), serde_json::to_value(new)) else {
        return Value::Object(Map::new());
    };
    let mut out = Map::new();
    for (k, nv) in &b {
        let ov = a.get(k).unwrap_or(&Value::Null);
        if ov != nv {
            out.insert(k.clone(), serde_json::json!({ "old": ov, "new": nv }));
        }
    }
    Value::Object(out)
}

/// Snapshot used as the diff of a `create` audit entry (only non-null fields).
pub fn snapshot<T: Serialize>(v: &T) -> Value {
    match serde_json::to_value(v) {
        Ok(Value::Object(m)) => Value::Object(m.into_iter().filter(|(_, v)| !v.is_null()).collect()),
        _ => Value::Object(Map::new()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::AuthorLink;
    use serde_json::json;

    fn book() -> BookData {
        BookData {
            title: "  War and Peace ".into(),
            original_title: Some("Война и мир".into()),
            subtitle: Some("   ".into()),
            description: None,
            isbn_10: None,
            isbn_13: Some("978-0-201-61622-4".into()),
            language_code: "RU".into(),
            publication_year: Some(1869),
            publisher: Some("Ruskii Vestnik".into()),
            page_count: Some(1225),
            genres: vec!["Novel".into(), "novel".into(), "Historical Fiction".into()],
            edition: None,
            external_ids: json!({"wikidata": "Q161531"}),
            authors: vec![AuthorLink { author_id: 1, role: "Author".into(), ordinal: None }],
        }
    }

    #[test]
    fn book_normalization() {
        let mut b = book();
        validate_book(&mut b).unwrap();
        assert_eq!(b.title, "War and Peace");
        assert_eq!(b.subtitle, None);
        assert_eq!(b.language_code, "ru");
        assert_eq!(b.isbn_13.as_deref(), Some("9780201616224"));
        assert_eq!(b.isbn_10.as_deref(), Some("020161622X"));
        assert_eq!(b.genres, vec!["novel", "historical fiction"]);
        assert_eq!(b.authors[0].role, "author");
        assert_eq!(b.authors[0].ordinal, Some(1));
    }

    #[test]
    fn isbn10_derives_isbn13() {
        let mut b = book();
        b.isbn_13 = None;
        b.isbn_10 = Some("0-201-61622-X".into());
        validate_book(&mut b).unwrap();
        assert_eq!(b.isbn_13.as_deref(), Some("9780201616224"));
    }

    #[test]
    fn book_errors_are_collected() {
        let mut b = book();
        b.title = " ".into();
        b.language_code = "english".into();
        b.publication_year = Some(3000);
        b.page_count = Some(0);
        b.isbn_13 = Some("9780201616225".into());
        b.isbn_10 = Some("123".into());
        b.genres = vec!["bad/genre".into()];
        b.external_ids = json!({"Bad Key": 5});
        b.authors = vec![
            AuthorLink { author_id: 1, role: "boss".into(), ordinal: Some(0) },
            AuthorLink { author_id: 1, role: "boss".into(), ordinal: Some(2) },
        ];
        let Err(AppError::Validation(errs)) = validate_book(&mut b) else { panic!("expected validation error") };
        let fields: Vec<&str> = errs.iter().map(|e| e.field.as_str()).collect();
        for f in [
            "title",
            "language_code",
            "publication_year",
            "page_count",
            "isbn_13",
            "isbn_10",
            "genres",
            "external_ids",
            "authors[0].role",
            "authors[0].ordinal",
            "authors[1]",
        ] {
            assert!(fields.contains(&f), "missing {f} in {fields:?}");
        }
    }

    #[test]
    fn mismatched_isbns() {
        let mut b = book();
        b.isbn_10 = Some("0306406152".into());
        let Err(AppError::Validation(errs)) = validate_book(&mut b) else { panic!() };
        assert_eq!(errs[0].field, "isbn_10");
    }

    #[test]
    fn too_many_things() {
        let mut b = book();
        b.genres = (0..25).map(|i| format!("g{i}")).collect();
        b.authors = (0..101).map(|i| AuthorLink { author_id: i + 1, role: "author".into(), ordinal: None }).collect();
        b.external_ids = json!([1]);
        b.title = "x".repeat(1001);
        let Err(AppError::Validation(errs)) = validate_book(&mut b) else { panic!() };
        let fields: Vec<&str> = errs.iter().map(|e| e.field.as_str()).collect();
        assert!(fields.contains(&"genres") && fields.contains(&"authors") && fields.contains(&"external_ids") && fields.contains(&"title"));
    }

    #[test]
    fn author_validation() {
        let mut a = AuthorData {
            full_name: " Leo Tolstoy ".into(),
            original_name: Some("Лев Толстой".into()),
            birth_date: NaiveDate::from_ymd_opt(1828, 9, 9),
            death_date: NaiveDate::from_ymd_opt(1910, 11, 20),
            country_code: Some("ru".into()),
            biography: None,
            external_ids: json!({"viaf": "96987389"}),
        };
        validate_author(&mut a).unwrap();
        assert_eq!(a.full_name, "Leo Tolstoy");
        assert_eq!(a.country_code.as_deref(), Some("RU"));

        a.death_date = NaiveDate::from_ymd_opt(1800, 1, 1);
        a.country_code = Some("RUS".into());
        a.full_name = String::new();
        let Err(AppError::Validation(errs)) = validate_author(&mut a) else { panic!() };
        assert_eq!(errs.len(), 3);

        a.full_name = "X".into();
        a.country_code = None;
        a.death_date = None;
        a.birth_date = NaiveDate::from_ymd_opt(2500, 1, 1);
        assert!(validate_author(&mut a).is_err());
    }

    #[test]
    fn user_validation() {
        let mut u = UserInput { username: " alice ".into(), email: "a@b.c".into() };
        validate_user(&mut u).unwrap();
        assert_eq!(u.username, "alice");
        let mut u = UserInput { username: "a b".into(), email: "nope".into() };
        let Err(AppError::Validation(errs)) = validate_user(&mut u) else { panic!() };
        assert_eq!(errs.len(), 2);
    }

    #[test]
    fn book_patch_semantics() {
        let mut base = book();
        validate_book(&mut base).unwrap();
        let p: BookPatch =
            serde_json::from_value(json!({"subtitle": "Vol. 1", "publisher": null, "genres": null, "isbn_10": "0306406152"})).unwrap();
        let mut d = apply_book_patch(base.clone(), p).unwrap();
        validate_book(&mut d).unwrap();
        assert_eq!(d.subtitle.as_deref(), Some("Vol. 1"));
        assert_eq!(d.publisher, None);
        assert!(d.genres.is_empty());
        assert_eq!(d.isbn_13.as_deref(), Some("9780306406157"));
        assert_eq!(d.title, base.title);

        let p: BookPatch = serde_json::from_value(json!({"title": null, "language_code": null})).unwrap();
        let Err(AppError::Validation(errs)) = apply_book_patch(base.clone(), p) else { panic!() };
        assert_eq!(errs.len(), 2);

        let p: BookPatch = serde_json::from_value(json!({
            "original_title": null, "description": "d", "isbn_13": null, "publication_year": 1900,
            "page_count": null, "edition": "2nd", "external_ids": null, "authors": []
        }))
        .unwrap();
        let d = apply_book_patch(base, p).unwrap();
        assert_eq!(d.isbn_13, None);
        assert_eq!(d.isbn_10, None);
        assert_eq!(d.publication_year, Some(1900));
        assert_eq!(d.external_ids, json!({}));
        assert!(d.authors.is_empty());
    }

    #[test]
    fn author_patch_semantics() {
        let base = AuthorData {
            full_name: "A".into(),
            original_name: None,
            birth_date: None,
            death_date: None,
            country_code: None,
            biography: None,
            external_ids: json!({}),
        };
        let p: AuthorPatch = serde_json::from_value(json!({"full_name": null})).unwrap();
        assert!(apply_author_patch(base.clone(), p).is_err());
        let p: AuthorPatch = serde_json::from_value(json!({
            "full_name": "B", "original_name": "Б", "birth_date": "1900-01-02", "death_date": null,
            "country_code": "FR", "biography": "bio", "external_ids": {"isni": "1"}
        }))
        .unwrap();
        let d = apply_author_patch(base, p).unwrap();
        assert_eq!(d.full_name, "B");
        assert_eq!(d.birth_date, NaiveDate::from_ymd_opt(1900, 1, 2));
        assert_eq!(d.external_ids, json!({"isni": "1"}));
    }

    #[test]
    fn diff_and_snapshot() {
        let mut a = book();
        validate_book(&mut a).unwrap();
        let mut b = a.clone();
        b.title = "New".into();
        b.subtitle = Some("S".into());
        let d = diff(&a, &b);
        assert_eq!(d["title"]["old"], "War and Peace");
        assert_eq!(d["title"]["new"], "New");
        assert_eq!(d["subtitle"]["old"], Value::Null);
        assert!(d.get("publisher").is_none());
        let s = snapshot(&a);
        assert!(s.get("subtitle").is_none());
        assert_eq!(s["title"], "War and Peace");
    }

    #[test]
    fn code_helpers() {
        assert!(is_language_code("en") && is_language_code("zho") && !is_language_code("EN") && !is_language_code("e"));
        assert!(is_country_code("JP") && !is_country_code("jp") && !is_country_code("JPN"));
        assert!(is_genre("sci-fi") && is_genre("фантастика") && !is_genre("") && !is_genre("a/b"));
    }
}
