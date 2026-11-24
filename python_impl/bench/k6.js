// k6 load scenario for the World Library API.
// Run via `make bench` (docker compose profile "bench", targets http://app:8000).
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:18001';
const NAMES = ['books_list', 'books_filter', 'books_cursor', 'books_fts', 'book_card', 'author_books',
  'authors_list', 'search', 'book_create', 'book_patch'];

export const options = {
  scenarios: {
    mixed: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 64),
      duration: __ENV.DURATION || '60s',
    },
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: Object.assign(
    { http_req_failed: ['rate<0.01'], http_req_duration: ['p(95)<500'] },
    // per-endpoint sub-metrics so they show up in the summary
    ...NAMES.map((n) => ({ [`http_req_duration{name:${n}}`]: ['p(95)<1000'] })),
  ),
};

export function setup() {
  const page = http.get(`${BASE}/api/v1/books?limit=100`).json();
  const books = page.items.map((b) => ({ id: b.id, user: b.created_by }));
  const authors = [];
  for (const b of page.items) for (const a of b.authors) authors.push(a.id);
  const names = http.get(`${BASE}/api/v1/authors?limit=50&sort=-created_at`).json().items.map((a) => a.full_name);
  return { books, authors, names };
}

const LANGS = ['en', 'ru', 'zh', 'de', 'fr', 'es', 'ar', 'ja'];
const WORDS = ['shadow', 'river', 'война', 'мир', 'garden', 'amor', 'nuit', '春', 'stone', 'memory'];
const SORTS = ['title', '-title', 'publication_year', '-publication_year', 'created_at', '-created_at'];
const pick = (arr) => arr[Math.floor(Math.random() * arr.length)];

function get(url, name) {
  const res = http.get(url, { tags: { name } });
  check(res, { [`${name} 200`]: (r) => r.status === 200 });
  return res;
}

export default function (data) {
  const r = Math.random();
  if (r < 0.15) {
    get(`${BASE}/api/v1/books?limit=20&sort=${pick(SORTS)}`, 'books_list');
  } else if (r < 0.30) {
    const y = 1950 + Math.floor(Math.random() * 60);
    get(`${BASE}/api/v1/books?language=${pick(LANGS)}&year_from=${y}&year_to=${y + 10}&sort=-created_at`, 'books_filter');
  } else if (r < 0.42) {
    let res = get(`${BASE}/api/v1/books?limit=20&sort=${pick(SORTS)}`, 'books_list');
    for (let i = 0; i < 3 && res.status === 200 && res.json().next_cursor; i++) {
      res = get(`${BASE}/api/v1/books?limit=20&sort=${res.json().items.length ? res.url.match(/sort=([^&]+)/)[1] : 'title'}&cursor=${res.json().next_cursor}`, 'books_cursor');
    }
  } else if (r < 0.52) {
    get(`${BASE}/api/v1/books?q=${encodeURIComponent(pick(WORDS))}`, 'books_fts');
  } else if (r < 0.70) {
    get(`${BASE}/api/v1/books/${pick(data.books).id}`, 'book_card');
  } else if (r < 0.78) {
    get(`${BASE}/api/v1/authors/${pick(data.authors)}/books`, 'author_books');
  } else if (r < 0.85) {
    get(`${BASE}/api/v1/authors?country=${pick(['RU', 'US', 'JP', 'DE', 'CN'])}&sort=full_name`, 'authors_list');
  } else if (r < 0.95) {
    const q = Math.random() < 0.5 ? pick(data.names) : pick(WORDS);
    get(`${BASE}/api/v1/search?q=${encodeURIComponent(q)}&type=all`, 'search');
  } else {
    const user = pick(data.books).user;
    const headers = { 'Content-Type': 'application/json', 'X-User-Id': user };
    const body = JSON.stringify({ title: `k6 book ${Math.random()}`, language_code: 'en', genres: ['novel'],
      authors: [{ author_id: pick(data.authors) }] });
    const res = http.post(`${BASE}/api/v1/books`, body, { headers, tags: { name: 'book_create' } });
    check(res, { 'create 201': (x) => x.status === 201 });
    if (res.status === 201) {
      const id = res.json().id;
      const p = http.patch(`${BASE}/api/v1/books/${id}`, JSON.stringify({ subtitle: 'patched' }),
        { headers: Object.assign({ 'If-Match': '"1"' }, headers), tags: { name: 'book_patch' } });
      check(p, { 'patch 200': (x) => x.status === 200 });
    }
  }
}
