// k6 load test: mixed read workload over the key endpoints of a seeded database.
// Run: make bench  (BENCH_VUS, BENCH_DUR override concurrency / duration)
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:28002';
const VUS = parseInt(__ENV.VUS || '32');
const DURATION = __ENV.DURATION || '60s';

const endpoints = ['book_get', 'books_list', 'books_filter', 'books_page2', 'books_fts', 'books_fuzzy',
  'author_get', 'author_books', 'authors_list', 'search', 'book_history'];
const thresholds = { http_req_failed: ['rate<0.01'] };
for (const e of endpoints) {
  thresholds[`http_req_duration{ep:${e}}`] = ['p(95)<500'];
}

export const options = {
  scenarios: { mixed: { executor: 'constant-vus', vus: VUS, duration: DURATION } },
  thresholds,
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
};

const words = ['shadow', 'river', 'night', 'garden', 'secret', 'winter', 'война', 'город', 'ombre', 'Reise', 'sombra', 'memory'];
const langs = ['en', 'ru', 'zh', 'ja', 'ar', 'es', 'fr', 'de'];
const genres = ['fiction', 'fantasy', 'history', 'poetry', 'science', 'classics'];
const sorts = ['title', '-title', 'publication_year', '-publication_year', 'created_at', '-created_at'];
const pick = (a) => a[Math.floor(Math.random() * a.length)];

export function setup() {
  const books = [];
  const authors = [];
  let cursor = '';
  for (let i = 0; i < 5; i++) {
    const r = http.get(`${BASE}/api/v1/books?limit=100&sort=title${cursor ? '&cursor=' + cursor : ''}`);
    const body = r.json();
    for (const b of body.items) {
      books.push(b.id);
      for (const a of b.authors) authors.push(a.author_id);
    }
    cursor = body.next_cursor;
  }
  const page = http.get(`${BASE}/api/v1/books?limit=20&sort=-created_at`).json();
  return { books, authors, cursor: page.next_cursor };
}

function get(url, ep) {
  const r = http.get(url, { tags: { ep } });
  check(r, { [`${ep} 200`]: (x) => x.status === 200 });
  return r;
}

export default function (d) {
  const x = Math.random();
  if (x < 0.20) get(`${BASE}/api/v1/books/${pick(d.books)}`, 'book_get');
  else if (x < 0.30) get(`${BASE}/api/v1/books?limit=20&sort=${pick(sorts)}`, 'books_list');
  else if (x < 0.42) get(`${BASE}/api/v1/books?limit=20&language=${pick(langs)}&genre=${pick(genres)}&sort=${pick(['title', '-created_at', 'publication_year'])}`, 'books_filter');
  else if (x < 0.48) get(`${BASE}/api/v1/books?limit=20&sort=-created_at&cursor=${d.cursor}`, 'books_page2');
  else if (x < 0.56) get(`${BASE}/api/v1/books?limit=20&q=${encodeURIComponent(pick(words))}`, 'books_fts');
  else if (x < 0.60) get(`${BASE}/api/v1/books?limit=20&sort=title&title=${encodeURIComponent(pick(words))}`, 'books_fuzzy');
  else if (x < 0.70) get(`${BASE}/api/v1/authors/${pick(d.authors)}`, 'author_get');
  else if (x < 0.80) get(`${BASE}/api/v1/authors/${pick(d.authors)}/books?limit=20`, 'author_books');
  else if (x < 0.86) get(`${BASE}/api/v1/authors?limit=20&sort=${pick(['full_name', '-birth_date', '-created_at'])}`, 'authors_list');
  else if (x < 0.94) get(`${BASE}/api/v1/search?limit=20&type=${pick(['books', 'authors', 'all'])}&q=${encodeURIComponent(pick(words))}`, 'search');
  else get(`${BASE}/api/v1/books/${pick(d.books)}/history`, 'book_history');
}

export function handleSummary(data) {
  const rows = [];
  for (const [k, v] of Object.entries(data.metrics)) {
    const m = k.match(/^http_req_duration\{ep:(.+)\}$/);
    if (m) rows.push({ ep: m[1], ...v.values });
  }
  rows.sort((a, b) => a.ep.localeCompare(b.ep));
  const all = data.metrics.http_req_duration.values;
  const fmt = (n) => (n === undefined ? '-' : n.toFixed(1));
  let md = `# Load test (k6)\n\nVUs: ${VUS}, duration: ${DURATION}, requests: ${data.metrics.http_reqs.values.count}, ` +
    `RPS: ${data.metrics.http_reqs.values.rate.toFixed(0)}, failed: ${(data.metrics.http_req_failed.values.rate * 100).toFixed(2)}%\n\n` +
    `| endpoint | p50 ms | p95 ms | p99 ms | max ms |\n|---|---:|---:|---:|---:|\n` +
    `| **all** | ${fmt(all.med)} | ${fmt(all['p(95)'])} | ${fmt(all['p(99)'])} | ${fmt(all.max)} |\n`;
  for (const r of rows) md += `| ${r.ep} | ${fmt(r.med)} | ${fmt(r['p(95)'])} | ${fmt(r['p(99)'])} | ${fmt(r.max)} |\n`;
  return {
    stdout: md,
    '/scripts/results/summary.md': md,
    '/scripts/results/summary.json': JSON.stringify(data, null, 1),
  };
}
