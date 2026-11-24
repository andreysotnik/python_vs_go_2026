# Load test (k6)

VUs: 8, duration: 45s, requests: 14757, RPS: 321, failed: 0.00%

| endpoint | p50 ms | p95 ms | p99 ms | max ms |
|---|---:|---:|---:|---:|
| **all** | 5.1 | 128.7 | 442.6 | 785.3 |
| author_books | 10.0 | 15.7 | 19.6 | 66.7 |
| author_get | 0.7 | 1.1 | 2.2 | 5.2 |
| authors_list | 1.2 | 2.1 | 3.6 | 14.1 |
| book_get | 1.4 | 2.6 | 4.6 | 23.3 |
| book_history | 1.6 | 2.7 | 4.3 | 16.0 |
| books_filter | 14.2 | 19.6 | 24.6 | 78.2 |
| books_fts | 12.4 | 17.0 | 19.9 | 48.6 |
| books_fuzzy | 97.7 | 158.5 | 178.5 | 213.1 |
| books_list | 5.7 | 8.2 | 10.1 | 21.9 |
| books_page2 | 5.7 | 8.7 | 11.7 | 25.2 |
| search | 145.3 | 600.1 | 654.9 | 785.3 |
