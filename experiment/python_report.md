# Финальный отчёт Python-агента (выжимка)

Метрики харнесса: duration_ms=5030621 (83.8 мин), tool_uses=116, subagent_tokens=289109 (размер контекста на финише).
Метрики из транскрипта: см. python_metrics.json (output 207 734 токенов, всего с кешем 21.6 млн, 116 API-вызовов).

## Критерии приёмки
- ✅ compose up + seed + test; полный 1М seed только через tmpfs-override (VM-диск Docker забит), named volume проверен на 20k
- ✅ Swagger /docs, OpenAPI 3.1 генерируется из кода, тест на дрейф
- ✅ make explain: 40 запросов на 1М без Seq Scan/OFFSET
- ✅ created/updated by/at (+ usernames), /history с diff из помесячного audit_log

## Тесты
- 139 passed (70 unit + 69 integration, testcontainers PG16) за 8.6 с; покрытие 96.97%; ruff чисто, mypy --strict 0 issues

## Seed / нагрузка
- 5.75 млн строк за 52.0 с: COPY 15.5 с (371k rows/s), индексы+FK 24.1 с, VACUUM ANALYZE 12.1 с
- k6 64 VU 60 с, 95% чтение/5% запись: 560 rps, 0% ошибок, p50 95 / p95 266 / p99 412 мс
- p95 по эндпоинтам: card 164, list 181, filter 246, fts 238, author_books 229, search 476 мс
- Шум: параллельно работал стек Go-агента

## Итерации и проблемы
- ~7 итераций до зелёных тестов (testcontainers на Docker Desktop, ISBN в тестах, mypy); 4 итерации производительности (31→40→10→560 rps)
- Та же ошибка ON ONLY при пересборке индексов на партициях
- PyPI через корп. прокси сломан → uv без HTTPS_PROXY
- PGDATA на bind mount (virtiofs) в 50× медленнее → tmpfs; shared_buffers 4GB → swap, снижено до 1GB
- Generic plans медленные → LIMIT литералом, force custom plan, прогрев соединений

## Упрощения
- /search ранжирует ≤1000 самых новых совпадений на тип
- фильтры title/name от 3 символов; auth — доверенный X-User-Id
- добавлены /api/v1/users и версионирование авторов (вне ТЗ)
- make test/lint требуют uv на хосте
