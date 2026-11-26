// Load scenario for the World Library API (k6). Run via `make bench`.
// Mix of the key read endpoints + a small share of writes, against a seeded database.
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://app:8080';
const VUS = parseInt(__ENV.VUS || '32');
const DURATION = __ENV.DURATION || '60s';

const ENDPOINTS = [
  'books_list', 'books_sort_title_page2', 'books_filter_lang_genre', 'books_filter_year', 'books_q',
  'book_get', 'author_get', 'author_books', 'authors_list_country', 'search_all', 'book_patch',
];

const thresholds = { http_req_failed: ['rate<0.01'] };
for (const n of ENDPOINTS) thresholds[`http_req_duration{name:${n}}`] = ['p(95)<1000'];

export const options = {
  scenarios: { mixed: { executor: 'constant-vus', vus: VUS, duration: DURATION } },
  thresholds,
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const books = http.get(`${BASE}/api/v1/books?limit=100&sort=title`).json('items');
  const authors = http.get(`${BASE}/api/v1/authors?limit=100&country=RU`).json('items');
  const page = http.get(`${BASE}/api/v1/books?limit=20&sort=title`).json();
  const words = ['garden', 'river', 'storm', 'dragon', 'shadow', 'город', 'nacht', 'jardín', '物語'];
  return {
    bookIds: books.map((b) => b.id),
    authorIds: authors.map((a) => a.id),
    cursor: page.next_cursor,
    words,
  };
}

const pick = (a) => a[Math.floor(Math.random() * a.length)];
const langs = ['en', 'ru', 'de', 'fr', 'es', 'zh', 'ja', 'ar'];
const genres = ['novel', 'poetry', 'fantasy', 'history', 'science', 'romance'];
const countries = ['RU', 'US', 'FR', 'JP', 'DE', 'CN'];

function get(url, name) {
  const r = http.get(url, { tags: { name } });
  check(r, { [`${name} 200`]: (x) => x.status === 200 });
  return r;
}

export default function (data) {
  const x = Math.random();
  if (x < 0.15) get(`${BASE}/api/v1/books?limit=20`, 'books_list');
  else if (x < 0.25) get(`${BASE}/api/v1/books?limit=20&sort=title&cursor=${data.cursor}`, 'books_sort_title_page2');
  else if (x < 0.35) get(`${BASE}/api/v1/books?language=${pick(langs)}&genre=${pick(genres)}&limit=20`, 'books_filter_lang_genre');
  else if (x < 0.42) {
    const y = 1900 + Math.floor(Math.random() * 120);
    get(`${BASE}/api/v1/books?year_from=${y}&year_to=${y + 5}&sort=-publication_year&limit=20`, 'books_filter_year');
  } else if (x < 0.50) get(`${BASE}/api/v1/books?q=${encodeURIComponent(pick(data.words))}&limit=20`, 'books_q');
  else if (x < 0.65) get(`${BASE}/api/v1/books/${pick(data.bookIds)}`, 'book_get');
  else if (x < 0.72) get(`${BASE}/api/v1/authors/${pick(data.authorIds)}`, 'author_get');
  else if (x < 0.80) get(`${BASE}/api/v1/authors/${pick(data.authorIds)}/books?limit=20`, 'author_books');
  else if (x < 0.87) get(`${BASE}/api/v1/authors?country=${pick(countries)}&sort=full_name&limit=20`, 'authors_list_country');
  else if (x < 0.97) get(`${BASE}/api/v1/search?q=${encodeURIComponent(pick(data.words))}&limit=20`, 'search_all');
  else {
    // write path: read version, then PATCH with If-Match (409 on races is expected and fine)
    const id = pick(data.bookIds);
    const cur = http.get(`${BASE}/api/v1/books/${id}`, { tags: { name: 'book_get' } });
    if (cur.status === 200) {
      const r = http.patch(`${BASE}/api/v1/books/${id}`, JSON.stringify({ edition: `bench-${Math.floor(Math.random() * 1e6)}` }), {
        headers: { 'Content-Type': 'application/json', 'X-User-Id': '1', 'If-Match': `"${cur.json('version')}"` },
        tags: { name: 'book_patch' },
        responseCallback: http.expectedStatuses(200, 409),
      });
      check(r, { 'book_patch 200/409': (x) => x.status === 200 || x.status === 409 });
    }
  }
}

export function handleSummary(data) {
  const fmt = (v) => (v === undefined ? '-' : v.toFixed(2));
  let md = '# Load test (k6)\n\n';
  md += `VUs: ${VUS}, duration: ${DURATION}, base: ${BASE}\n\n`;
  const reqs = data.metrics.http_reqs.values;
  md += `Total requests: ${reqs.count}, throughput: ${reqs.rate.toFixed(1)} req/s, failed: ${(data.metrics.http_req_failed.values.rate * 100).toFixed(3)}%\n\n`;
  md += '| endpoint | p50, ms | p95, ms | p99, ms | max, ms |\n|---|---:|---:|---:|---:|\n';
  const all = data.metrics.http_req_duration.values;
  md += `| **all** | ${fmt(all.med)} | ${fmt(all['p(95)'])} | ${fmt(all['p(99)'])} | ${fmt(all.max)} |\n`;
  for (const n of ENDPOINTS) {
    const m = data.metrics[`http_req_duration{name:${n}}`];
    if (!m) continue;
    const v = m.values;
    md += `| ${n} | ${fmt(v.med)} | ${fmt(v['p(95)'])} | ${fmt(v['p(99)'])} | ${fmt(v.max)} |\n`;
  }
  return {
    '/results/k6-report.md': md,
    '/results/k6-summary.json': JSON.stringify(data, null, 2),
    stdout: md,
  };
}
