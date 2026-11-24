DROP TABLE IF EXISTS audit_log, author_books, book_authors, book_isbns, books, authors, users CASCADE;
DROP FUNCTION IF EXISTS ensure_audit_partitions(date, integer);
DROP FUNCTION IF EXISTS next_id();
DROP SEQUENCE IF EXISTS id_seq;
DROP FUNCTION IF EXISTS f_unaccent(text);
