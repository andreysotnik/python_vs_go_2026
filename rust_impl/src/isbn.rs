//! ISBN-10 / ISBN-13 normalization, validation, conversion and generation.

/// Removes hyphens and spaces, upper-cases a trailing `x`.
pub fn normalize(raw: &str) -> String {
    raw.chars().filter(|c| !matches!(c, '-' | ' ')).map(|c| c.to_ascii_uppercase()).collect()
}

/// Check digit for the first 12 digits of an ISBN-13.
pub fn isbn13_check_digit(first12: &[u8]) -> u8 {
    let sum: u32 = first12.iter().enumerate().map(|(i, d)| u32::from(*d) * if i % 2 == 0 { 1 } else { 3 }).sum();
    ((10 - (sum % 10)) % 10) as u8
}

/// Check character for the first 9 digits of an ISBN-10 (`0`-`9` or `X`).
pub fn isbn10_check_char(first9: &[u8]) -> char {
    let sum: u32 = first9.iter().enumerate().map(|(i, d)| u32::from(*d) * (10 - i as u32)).sum();
    match (11 - (sum % 11)) % 11 {
        10 => 'X',
        v => char::from(b'0' + v as u8),
    }
}

fn digits(s: &str) -> Option<Vec<u8>> {
    s.bytes().map(|b| b.is_ascii_digit().then(|| b - b'0')).collect()
}

/// Validates a normalized ISBN-13 (13 digits, 978/979 prefix, correct check digit).
pub fn is_valid_isbn13(s: &str) -> bool {
    if s.len() != 13 || !(s.starts_with("978") || s.starts_with("979")) {
        return false;
    }
    match digits(s) {
        Some(d) => isbn13_check_digit(&d[..12]) == d[12],
        None => false,
    }
}

/// Validates a normalized ISBN-10.
pub fn is_valid_isbn10(s: &str) -> bool {
    if s.len() != 10 {
        return false;
    }
    match digits(&s[..9]) {
        Some(d) => s.chars().nth(9) == Some(isbn10_check_char(&d)),
        None => false,
    }
}

/// Converts a valid ISBN-10 into its ISBN-13 (978 prefix).
pub fn isbn10_to_13(s: &str) -> Option<String> {
    if !is_valid_isbn10(s) {
        return None;
    }
    let mut d = vec![9, 7, 8];
    d.extend(digits(&s[..9])?);
    let check = isbn13_check_digit(&d);
    d.push(check);
    Some(d.iter().map(|x| char::from(b'0' + x)).collect())
}

/// Converts a valid 978-prefixed ISBN-13 into ISBN-10 (979 has no ISBN-10 form).
pub fn isbn13_to_10(s: &str) -> Option<String> {
    if !is_valid_isbn13(s) || !s.starts_with("978") {
        return None;
    }
    let d = digits(&s[3..12])?;
    let mut out: String = d.iter().map(|x| char::from(b'0' + x)).collect();
    out.push(isbn10_check_char(&d));
    Some(out)
}

/// Builds a valid ISBN-13 from a 3-digit prefix (978/979) and a 9-digit body number.
pub fn make_isbn13(prefix: u16, body: u64) -> String {
    let base = format!("{prefix:03}{:09}", body % 1_000_000_000);
    let d = digits(&base).unwrap_or_default();
    format!("{base}{}", isbn13_check_digit(&d))
}

/// Normalizes any user-supplied ISBN (10 or 13) to ISBN-13, or `None` if invalid.
pub fn to_isbn13(raw: &str) -> Option<String> {
    let n = normalize(raw);
    match n.len() {
        13 if is_valid_isbn13(&n) => Some(n),
        10 => isbn10_to_13(&n),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn known_isbns() {
        // "The Pragmatic Programmer"
        assert!(is_valid_isbn13("9780201616224"));
        assert!(is_valid_isbn10("020161622X"));
        assert_eq!(isbn10_to_13("020161622X").as_deref(), Some("9780201616224"));
        assert_eq!(isbn13_to_10("9780201616224").as_deref(), Some("020161622X"));
        assert!(is_valid_isbn13("9791032305690"));
        assert_eq!(isbn13_to_10("9791032305690"), None);
    }

    #[test]
    fn invalid_isbns() {
        assert!(!is_valid_isbn13("9780201616225"));
        assert!(!is_valid_isbn13("1234567890123"));
        assert!(!is_valid_isbn13("978020161622"));
        assert!(!is_valid_isbn13("97802016162a4"));
        assert!(!is_valid_isbn10("0201616221"));
        assert!(!is_valid_isbn10("02016162"));
        assert!(!is_valid_isbn10("X201616221"));
        assert_eq!(isbn10_to_13("0201616221"), None);
    }

    #[test]
    fn normalization() {
        assert_eq!(normalize("978-0-201-61622-4"), "9780201616224");
        assert_eq!(normalize("0 201 61622 x"), "020161622X");
        assert_eq!(to_isbn13("0-201-61622-X").as_deref(), Some("9780201616224"));
        assert_eq!(to_isbn13("978-0-201-61622-4").as_deref(), Some("9780201616224"));
        assert_eq!(to_isbn13("12345"), None);
    }

    #[test]
    fn generated_isbns_are_valid() {
        for body in [0_u64, 1, 42, 999_999_999, 123_456_789, 5_000_000_000] {
            let s = make_isbn13(978, body);
            assert!(is_valid_isbn13(&s), "{s}");
            assert!(isbn13_to_10(&s).is_some_and(|t| is_valid_isbn10(&t)));
            assert!(is_valid_isbn13(&make_isbn13(979, body)));
        }
    }

    #[test]
    fn check_char_x() {
        // sum such that check is 10 -> 'X'
        assert_eq!(isbn10_check_char(&[0, 2, 0, 1, 6, 1, 6, 2, 2]), 'X');
    }
}
