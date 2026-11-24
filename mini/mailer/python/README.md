# mailer — email-рассылки по расписанию (Python 3.12)

Сервис читает `jobs.yaml`, по cron-расписанию (в таймзоне каждой задачи) собирает письмо
(UTF-8, RFC 2047 Subject, Message-ID, Date, вложения) и отправляет его по SMTP с повторами.
Отправленные слоты фиксируются в `STATE_DIR`, поэтому перезапуск и повторный `once` не дают дублей.
Логи — JSON, по одному объекту на строку, в stdout.

## Быстрый старт (всё из каталога проекта)

```bash
# зависимости (корпоративный прокси ломает PyPI — снимаем его для uv)
env -u HTTPS_PROXY -u HTTP_PROXY uv sync

# тесты и линтер
uv run pytest -q
uv run ruff check . && uv run ruff format --check .

# проверить конфиг
uv run mailer validate --config examples/jobs.yaml
```

### `once` на хосте без Docker

Нужен любой SMTP-сервер. Например, Mailpit из compose (`docker compose up -d mailpit`, SMTP на 11025):

```bash
SMTP_HOST=127.0.0.1 SMTP_PORT=11025 SMTP_TLS=none SMTP_FROM=mailer@example.com STATE_DIR=./state uv run mailer once --config examples/jobs.yaml --at 2026-11-19T09:00:00+03:00
```

Письмо `daily-report` («Ежедневный отчёт за 2026-11-19», вложение `report.pdf`) появится в
Mailpit: UI http://localhost:18025, API `curl http://localhost:18025/api/v1/messages`.
Повторный запуск той же команды ничего не отправит (в логе `already sent for this slot, skipping`).

Демон на хосте: та же строка, но `uv run mailer run --config examples/jobs.yaml`.

### Docker

```bash
docker compose up --build -d          # mailpit (11025 SMTP, 18025 UI/API) + демон mailer
docker compose exec mailer mailer once --config /app/config/jobs.yaml --at 2026-11-19T09:00:00+03:00
curl -s http://localhost:18025/api/v1/messages | python3 -m json.tool | head -40
docker compose down -v
```

Compose project name — `mini_mailer_python`. Каталог `examples/` монтируется в контейнер как
`/app/config` (read-only), состояние — в volume `mailer-state` (`/var/lib/mailer`).
Образ multi-stage, процесс работает под непривилегированным пользователем uid 10001.

## Команды

| Команда | Что делает | Код выхода |
|---|---|---|
| `mailer validate --config F` | cron, таймзоны, адреса, плейсхолдеры, существование вложений, дубли имён | 0 — ок, 2 — ошибки (все сразу, в stderr) |
| `mailer once --config F --at ISO8601` | отправляет задачи, срабатывающие в минуту `--at` (смещение обязательно, секунды отбрасываются), и выходит | 0 — ок/нечего слать/уже отправлено, 1 — ошибка отправки, 2 — ошибка конфига/окружения |
| `mailer run --config F` | демон: в начале каждой минуты отправляет задачи по расписанию; SIGTERM/SIGINT — дождаться текущего письма и выйти | 0 |

Также `python -m mailer ...`.

## Конфигурация

```yaml
jobs:
  - name: daily-report                # [A-Za-z0-9._-], уникально; используется в STATE_DIR
    schedule: "0 9 * * 1-5"           # cron, 5 полей
    timezone: "Europe/Moscow"         # IANA
    to: ["team@example.com", "Начальник <boss@example.com>"]
    cc: []                            # необязательно
    subject: "Ежедневный отчёт за {{date}}"
    body: |
      Привет! Отчёт за {{date}} во вложении.
    html: false                       # необязательно, true → text/html
    attachments: ["./files/report.pdf"]   # необязательно; относительные пути — от каталога jobs.yaml
```

Cron: `*`, `N`, `A-B`, `*/S`, `A-B/S`, `A/S`, списки через запятую, имена `jan..dec`, `sun..sat`,
`7` = воскресенье. Если ограничены и день месяца, и день недели — срабатывает при совпадении любого (как в Vixie cron).

Плейсхолдеры: `{{date}}` — `YYYY-MM-DD` слота в таймзоне задачи, `{{datetime}}` — ISO 8601 слота
в таймзоне задачи (`2026-11-19T09:00:00+03:00`), `{{job}}` — имя задачи. Неизвестный плейсхолдер — ошибка `validate`.

Переменные окружения:

| Переменная | Обязательна | По умолчанию |
|---|---|---|
| `SMTP_HOST` | да | — |
| `SMTP_PORT` | нет | 25 / 587 / 465 для none / starttls / tls |
| `SMTP_TLS` | нет | `starttls` (`none`, `starttls`, `tls`) |
| `SMTP_USER`, `SMTP_PASSWORD` | нет | без AUTH, если `SMTP_USER` пуст |
| `SMTP_FROM` | да | — (`addr` или `Имя <addr>`) |
| `SMTP_TIMEOUT` | нет | 30 (секунд) |
| `STATE_DIR` | нет | `./state` (в образе `/var/lib/mailer`) |
| `LOG_LEVEL` | нет | `INFO` |

## Надёжность

- **Повторы**: 3 попытки, задержки 1 с и 2 с. Повторяются сетевые ошибки, таймауты, обрывы и ответы 4xx;
  ответы 5xx, ошибка проверки TLS-сертификата и отсутствие STARTTLS — постоянные, без повторов.
- **Без дублей**: перед отправкой берётся `flock` на задачу, проверяется маркер
  `STATE_DIR/sent/<job>/<YYYYMMDDTHHMMZ>.json`; после успешной отправки маркер пишется атомарно
  (tmp + fsync + rename). Ошибочная отправка маркер не ставит — её можно повторить `once`.
- **Пропущенные слоты** (простой, сон хоста, скачок часов) не досылаются: демон хранит `last_tick`
  и при старте/пробуждении пишет в лог `missed slot during downtime, not sending` для каждого пропущенного слота (окно до 7 дней).
- **SIGTERM/SIGINT**: текущее письмо (включая его повторы) дописывается, оставшиеся задачи слота
  логируются как `shutdown requested, job not sent`, процесс выходит с кодом 0.
- **DST**: несуществующее локальное время (перевод вперёд) не срабатывает; повторяющееся (перевод назад) — один раз.

## Структура

```
src/mailer/   cron.py  templates.py  config.py  message.py  smtp.py  state.py  scheduler.py  logs.py  cli.py
tests/        модульные (cron/таймзоны/DST, шаблоны, конфиг, повторы, дедупликация, демон с фейковыми часами)
              + интеграционные (aiosmtpd в процессе, реальный smtplib, CLI, SIGTERM подпроцессу)
examples/     jobs.yaml и files/report.pdf
```
