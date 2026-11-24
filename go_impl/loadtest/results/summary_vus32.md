# Load test (k6)

VUs: 32, duration: 60s, requests: 22333, RPS: 365, failed: 0.00%

| endpoint | p50 ms | p95 ms | p99 ms | max ms |
|---|---:|---:|---:|---:|
| **all** | 13.4 | 507.8 | 1526.0 | 3359.9 |
| author_books | 27.0 | 73.7 | 120.9 | 956.6 |
| author_get | 1.1 | 8.7 | 14.5 | 48.6 |
| authors_list | 2.2 | 16.6 | 37.9 | 169.1 |
| book_get | 3.6 | 17.2 | 31.1 | 80.3 |
| book_history | 4.3 | 30.9 | 77.1 | 224.4 |
| books_filter | 48.0 | 99.2 | 130.5 | 989.7 |
| books_fts | 40.6 | 86.1 | 125.4 | 833.5 |
| books_fuzzy | 325.6 | 763.2 | 939.0 | 1280.8 |
| books_list | 14.3 | 49.4 | 86.6 | 1129.7 |
| books_page2 | 14.8 | 54.0 | 97.6 | 151.1 |
| search | 497.6 | 1980.6 | 2633.3 | 3359.9 |
