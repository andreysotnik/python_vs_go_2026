# mailer — email-рассылки по расписанию (Go)

Сервис читает `jobs.yaml`, по cron-расписанию (5 полей, с таймзоной на задачу)
собирает письма (UTF-8, RFC 2047, Message-ID, Date, вложения) и отправляет их
через SMTP с повторами. Состояние «задача X отправлена за слот T» хранится в
`STATE_DIR`, поэтому перезапуски и повторные `once` не создают дублей.

## Быстрый старт

Все команды выполняются из каталога проекта (`.../mini/mailer/go`).

```bash
# Тесты (модульные + интеграционный с фейковым SMTP, без Docker) и линтеры
go test -race ./...
gofmt -l cmd internal && go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # опционально

# Проверка конфига
go run ./cmd/mailer validate --config examples/jobs.yaml
```

### Локальная проверка в Docker (Mailpit)

```bash
docker compose up --build -d          # mailpit + демон рассылки (project: mini_mailer_go)
docker compose exec mailer mailer once --config /etc/mailer/jobs.yaml --at 2026-11-19T09:00:00+03:00
curl -s http://localhost:28025/api/v1/messages   # письмо видно в API Mailpit (UI: http://localhost:28025)
docker compose down -v
```

Порты хоста: `21025` — SMTP Mailpit, `28025` — UI/API Mailpit.

### `once` на хосте без Docker (одна строка)

Нужен любой SMTP-сервер; ниже — Mailpit из compose на порту 21025
(`docker compose up -d mailpit`):

```bash
SMTP_HOST=localhost SMTP_PORT=21025 SMTP_TLS=none SMTP_FROM=mailer@example.com STATE_DIR=./.state go run ./cmd/mailer once --config examples/jobs.yaml --at 2026-11-19T09:00:00+03:00
```

Повторный запуск той же команды писем не отправит (лог `already sent for this slot, skipping`).
Чтобы отправить заново — `rm -rf ./.state`.

Собрать бинарь: `go build -o mailer ./cmd/mailer`.

## Команды CLI

| Команда | Что делает | Код выхода |
|---|---|---|
| `validate --config jobs.yaml` | проверяет YAML (неизвестные поля — ошибка), cron, таймзоны, адреса, вложения; печатает все ошибки сразу и ближайший запуск каждой задачи | 0 — ок, 2 — ошибки |
| `once --config jobs.yaml [--at RFC3339]` | отправляет все задачи, срабатывающие в минуту `--at` (по умолчанию — текущая), и выходит | 0 — ок, 1 — хотя бы одна задача не отправлена, 2 — ошибка конфига/окружения/аргументов |
| `run --config jobs.yaml` | демон: в начале каждой минуты отправляет задачи по расписанию | 0 после SIGTERM/SIGINT |

`--config` по умолчанию `jobs.yaml`.

## Конфигурация

### jobs.yaml

```yaml
jobs:
  - name: daily-report              # [A-Za-z0-9._-], уникально
    schedule: "0 9 * * 1-5"         # cron, 5 полей (мин час день месяц день_недели)
    timezone: "Europe/Moscow"       # IANA; пусто — UTC
    to: ["team@example.com", "Boss <boss@example.com>"]
    cc: []
    subject: "Ежедневный отчёт за {{date}}"
    body: |
      Привет! Отчёт за {{date}} во вложении.
    html: false                     # true — text/html
    attachments: ["./files/report.pdf"]   # необязательно; относительно каталога jobs.yaml
```

Шаблоны в `subject` и `body`:

| Плейсхолдер | Значение (в таймзоне задачи) | Пример |
|---|---|---|
| `{{date}}` | дата слота, `YYYY-MM-DD` | `2026-11-19` |
| `{{datetime}}` | дата и время слота со смещением | `2026-11-19 09:00 +03:00` |
| `{{job}}` | имя задачи | `daily-report` |

Пробелы внутри скобок допустимы (`{{ date }}`), неизвестные плейсхолдеры остаются как есть.

Пример со всеми возможностями: [`examples/jobs.yaml`](examples/jobs.yaml).

### Переменные окружения

| Переменная | Обязательна | Описание |
|---|---|---|
| `SMTP_HOST` | да | хост SMTP |
| `SMTP_PORT` | нет | по умолчанию 587 (starttls), 465 (tls), 25 (none) |
| `SMTP_USER` | нет | если задан — AUTH PLAIN (только по TLS или на localhost) |
| `SMTP_PASSWORD` | нет | пароль |
| `SMTP_FROM` | да | отправитель, можно `Имя <addr@host>` |
| `SMTP_TLS` | нет | `none` \| `starttls` (по умолчанию) \| `tls` |
| `STATE_DIR` | да | каталог состояния (создаётся при необходимости) |
| `LOG_LEVEL` | нет | `debug` \| `info` (по умолчанию) \| `warn` \| `error` |

## Поведение и надёжность

- **Письмо**: UTF-8, тело в quoted-printable, вложения base64 (multipart/mixed),
  `Subject` и имена в адресах кодируются по RFC 2047, имя файла вложения — по RFC 2231.
  Заголовки `Date`, `Message-ID`, `MIME-Version`. Окончания строк CRLF.
- **Повторы**: 3 попытки, задержки 1 с и 2 с (экспоненциально). Повторяются
  4xx-ответы и сетевые ошибки (отказ соединения, таймаут, обрыв). Ответы 5xx,
  ошибки TLS-сертификата и прочие ошибки — постоянные, не повторяются.
- **Без дублей**: после успешной отправки атомарно (temp + fsync + rename)
  пишется `STATE_DIR/sent/<job>/<слот UTC>.json`. Перед отправкой маркер
  проверяется — и в `once`, и в `run`.
- **Неудачная задача** не помечается отправленной: её можно дослать вручную
  `once --at <слот>`. Демон её в следующую минуту не повторяет.
- **Пропуски**: демон хранит `STATE_DIR/last_tick`; при старте все слоты между
  последней обработанной минутой и текущей логируются как `missed run ... not sending`
  (WARN) и не отправляются. То же — если процесс «проспал» минуты (сон ноутбука и т.п.).
  Текущая минута при старте обрабатывается (с дедупликацией).
- **SIGTERM/SIGINT**: новые задачи не начинаются, текущее письмо (включая его
  повторы) дописывается, после чего процесс завершается с кодом 0. Повторный сигнал —
  немедленный выход (130). В compose `stop_grace_period: 90s`.
- **Логи**: JSON (`log/slog`) в stdout, по строке на событие. Вывод `validate` —
  человекочитаемый (stdout/stderr).

## Docker

- `Dockerfile`: multi-stage (`golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot`),
  статический бинарь, пользователь `65532`, база таймзон встроена в бинарь (`time/tzdata`),
  зависимости из `vendor/` (сборка без сети).
- `docker-compose.yml` (project `mini_mailer_go`): `mailpit` (`axllent/mailpit:v1.21.8`,
  21025/28025) и `mailer` (демон; `./examples` монтируется в `/etc/mailer`, состояние в томе
  `mailer-state`).

## Структура

```
cmd/mailer           CLI (validate / once / run), сигналы, коды выхода
internal/config      загрузка и валидация jobs.yaml и SMTP_* окружения
internal/schedule    cron (robfig/cron/v3 parser) + таймзона, проверка «сработает ли в минуту»
internal/tmpl        {{date}} {{datetime}} {{job}}
internal/mailer      сборка MIME-письма, SMTP-клиент, классификация ошибок, повторы
internal/state       маркеры отправки и last_tick на диске
internal/runner      once (RunSlot) и демон (Daemon), пропуски, graceful shutdown
internal/smtptest    фейковый SMTP-сервер для тестов
examples/            пример jobs.yaml и вложение
```
