Ты — senior backend-инженер с опытом high-load систем и PostgreSQL на масштабе миллиардов строк.
Твоя задача — спроектировать, реализовать, протестировать и упаковать в Docker backend-приложение
«Мировая библиотека» — каталог всех книг и авторов мира.

## 1. Стек
- Язык: Go (локально установлен go1.27.3)  (Go: chi/echo + pgx + sqlc/squirrel; Python: FastAPI + asyncpg/SQLAlchemy 2 async + Pydantic v2)
- БД: PostgreSQL 16+ (расширения: pg_trgm, btree_gin, unaccent).
- Миграции: goose / golang-migrate (Go) или Alembic (Python).
- Контейнеризация: Docker (multi-stage, non-root, минимальный образ) + docker-compose.
- Документация API: OpenAPI 3.1 (Swagger UI доступен по /docs, спецификация — /openapi.yaml и файл в репозитории).
- Никаких внешних поисковых движков в обязательной части: поиск реализуется средствами PostgreSQL.
  В README опиши, когда и как подключать OpenSearch/Elasticsearch (CDC через outbox/логическую репликацию).

## 2. Нефункциональные требования (масштаб — ключевое)
Целевой объём: 10+ млрд книг, сотни миллионов–миллиарды авторов, связи книга↔автор — десятки млрд.
Каждое архитектурное решение принимай с учётом этого и кратко обосновывай в docs/ARCHITECTURE.md:
- Идентификаторы: bigint (снаружи — snowflake-подобные) или UUIDv7 (монотонные, дружат с B-tree). Никаких UUIDv4 в PK.
- Партиционирование: декларативное, hash по id крупных таблиц (books, book_authors) — например 64–256 партиций;
  схема должна позволять позже вынести партиции на шарды (Citus / ручной шардинг) без смены API.
- Пагинация ТОЛЬКО keyset (cursor-based): непрозрачный base64-курсор (значение сортировки + id для tie-break).
  OFFSET запрещён. Общий total не считается через COUNT(*) — по запросу отдаётся приблизительный (pg_class.reltuples / EXPLAIN-оценка)
  с флагом `total_is_estimate: true`.
- Сортировка разрешена только по полям, у которых есть подходящий составной индекс (whitelist), всегда с id как вторым ключом.
- Поиск:
  - по названию/имени — полнотекстовый (tsvector, сгенерированная колонка + GIN, мультиязычный конфиг 'simple' + unaccent)
    и нечёткий/префиксный (pg_trgm GIN);
  - по точным полям (ISBN, язык, год, издатель, жанр) — B-tree;
  - для комбинированных фильтров — продумай, какие индексы реально нужны, избегай индексного «зоопарка», учти стоимость записи.
- Все запросы обязаны иметь лимит (max page size = 100) и statement_timeout.
- Пул соединений, готовность к PgBouncer (transaction mode — без session-level фич).
- Запись: массовая загрузка через COPY; одиночные вставки — обычный INSERT в транзакции.
- Наблюдаемость: структурные JSON-логи, /healthz (liveness), /readyz (проверка БД), Prometheus-метрики /metrics.
- Конфигурация только через переменные окружения (12-factor).

## 3. Модель данных (минимум; расширяй обоснованно)
- users: id, username, email, created_at — кто добавлял/изменял данные.
- authors: id, full_name, original_name (на родном языке/письменности), birth_date, death_date, country_code,
  biography, external_ids (jsonb: VIAF, ISNI, Wikidata), created_at, created_by, updated_at, updated_by, deleted_at (soft delete).
- books: id, title, original_title, subtitle, description, isbn_10, isbn_13, language_code, publication_year,
  publisher, page_count, genres (text[] или справочник), edition, external_ids (jsonb), search_vector (generated),
  created_at, created_by, updated_at, updated_by, deleted_at, version (optimistic locking).
- book_authors: book_id, author_id, role (author/co-author/translator/editor/illustrator), ordinal.
  Учти, что связь нужна в обе стороны («книги автора» и «авторы книги») — продумай партиционирование/индексы для обоих направлений.
- audit_log (партиционирование по времени, range по месяцу): entity_type, entity_id, action, changed_by, changed_at, diff (jsonb).
  Отвечает на вопрос «кто и когда добавил/изменил запись».
Ограничения целостности: CHECK на коды языков/стран и диапазоны годов, UNIQUE по isbn_13 (с учётом партиционирования —
опиши решение, если глобальная уникальность на партиционированной таблице невозможна напрямую).

## 4. API (REST, JSON, версия /api/v1)
Книги:
- GET    /api/v1/books — список: фильтры (title, author, author_id, isbn, language, year_from, year_to, publisher, genre,
         created_by, created_from, created_to), q — полнотекстовый поиск, sort (title|publication_year|created_at, ±),
         limit, cursor.
- GET    /api/v1/books/{id} — карточка с авторами и аудит-полями.
- POST   /api/v1/books, PUT/PATCH /api/v1/books/{id} (с If-Match/version), DELETE (soft).
- GET    /api/v1/books/{id}/history — история изменений из audit_log.
Авторы:
- GET    /api/v1/authors — список: фильтры (name, country, born_from, born_to), q, sort (full_name|birth_date|created_at), limit, cursor.
- GET    /api/v1/authors/{id}, GET /api/v1/authors/{id}/books (пагинированно), POST/PUT/PATCH/DELETE.
Общий поиск:
- GET    /api/v1/search?q=...&type=books|authors|all — релевантность (ts_rank + similarity), keyset по (rank, id).
Пользователь-автор изменения определяется заголовком X-User-Id (упрощённая аутентификация; оформи через middleware,
чтобы её можно было заменить на JWT).
Единый формат ответа списка: { "items": [...], "next_cursor": "...|null", "limit": N, "total": N|null, "total_is_estimate": bool }.
Единый формат ошибок: RFC 7807 (application/problem+json). Валидация входных данных с понятными сообщениями.

## 5. Моковые данные
- Отдельная команда/скрипт `seed` (в том же образе или отдельный сервис compose): генерирует реалистичные данные
  (многоязычные названия и имена, включая кириллицу, CJK, арабский; валидные ISBN-13 с контрольной цифрой;
  распределение «длинного хвоста» — у малого числа авторов много книг).
- Объём параметризуется: SEED_BOOKS, SEED_AUTHORS (по умолчанию 1 млн книг / 200 тыс. авторов), детерминированность через SEED_RANDOM.
- Загрузка через COPY пачками, индексы/FK — по возможности после загрузки; выводить прогресс и скорость (rows/s).
- Опиши в README, как экстраполировать на 10 млрд (оценка объёма диска, времени загрузки, размеров индексов).

## 6. Тесты
- Unit-тесты: валидация, кодирование/декодирование курсора, построение запросов, генерация ISBN.
- Интеграционные тесты на реальном PostgreSQL (testcontainers или compose-профиль test):
  CRUD, аудит (created_by/updated_by/history), keyset-пагинация (нет дублей и пропусков при обходе всех страниц,
  стабильность при одинаковых значениях сортировки), каждая сортировка, каждый фильтр, полнотекстовый и нечёткий поиск,
  optimistic locking (409), soft delete, ошибки 400/404/422.
- Тест производительности запросов: на засеянной БД прогнать EXPLAIN (ANALYZE, BUFFERS) для ключевых запросов
  и автоматически проверить, что нет Seq Scan по большим таблицам; результаты сохранить в docs/QUERY_PLANS.md.
- Простой нагрузочный сценарий (k6 или vegeta) с отчётом p50/p95/p99.
- Покрытие ≥ 80% для бизнес-логики. Линтеры: golangci-lint / ruff + mypy --strict.

## 7. Docker и запуск
- Dockerfile (multi-stage), docker-compose.yml: postgres (с тюнингом под нагрузку через конфиг), app, seed, migrate.
- Makefile: make up, make migrate, make seed, make test, make lint, make bench, make down.
- Приложение стартует одной командой `docker compose up --build`, миграции применяются отдельным шагом/сервисом.

## 8. Артефакты на выходе
- Исходный код со слоистой структурой (handlers → service → repository), без глобального состояния.
- migrations/, openapi.yaml (полная спецификация: схемы, примеры, коды ошибок, параметры пагинации/сортировки/фильтров).
- README.md: запуск, переменные окружения, примеры curl.
- docs/ARCHITECTURE.md: схема БД, партиционирование, индексы и их обоснование, стратегия пагинации и поиска,
  путь к шардингу и внешнему поисковому движку, известные ограничения.

## 9. Порядок работы
1. Сначала коротко опиши архитектуру и схему БД, затем реализуй.
2. После реализации действительно выполни: сборку Docker-образа, миграции, seed, все тесты, линтер, EXPLAIN-проверку.
3. Исправляй ошибки, пока всё не станет зелёным. Не помечай задачу выполненной, если что-то не запускалось.
4. В конце дай отчёт: что сделано, результаты тестов (с выводом), время загрузки seed, p95 ключевых эндпоинтов,
   компромиссы и что улучшить дальше.

## Критерии приёмки
- `docker compose up --build` + `make seed` + `make test` проходят на чистой машине.
- Swagger UI открывается и соответствует реальному поведению API.
- Ни один списочный запрос не использует OFFSET и не делает Seq Scan по books/authors на засеянных данных.
- У каждой книги и автора видно, кто и когда их создал и изменил; история изменений доступна через API.
