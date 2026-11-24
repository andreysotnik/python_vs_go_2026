"""Собирает второй вариант доклада в стиле PowerPoint: presentation.pptx и presentation_ppt.html.

Оба файла строятся из одного описания слайдов (координаты в дюймах, холст 13,333 × 7,5),
поэтому выглядят одинаково. Запуск из корня проекта:
    experiment/deck/.venv/bin/python -I experiment/deck/build.py
"""
import html
import sys
from pathlib import Path

from lxml import etree
from pptx import Presentation
from pptx.chart.data import CategoryChartData
from pptx.dml.color import RGBColor
from pptx.enum.chart import XL_CHART_TYPE, XL_LABEL_POSITION, XL_LEGEND_POSITION
from pptx.enum.shapes import MSO_SHAPE
from pptx.enum.text import MSO_ANCHOR, MSO_AUTO_SIZE, PP_ALIGN
from pptx.oxml.ns import qn
from pptx.util import Emu, Inches, Pt

ROOT = Path(__file__).resolve().parents[2]
W, H = 13.333, 7.5
FONT = "Arial"

BG, CARD, CARD2, LINE = "1B1D23", "262A33", "313642", "3A3F4B"
TEXT, MUTED = "F2F3F5", "A3A8B4"
GO, PY, OK, BAD, ACC = "3CC4E8", "F4B740", "4CC38A", "F0625F", "A78BFA"

X0, CW = 0.6, 12.13          # левое поле и ширина контента
TOTAL = 16


# ---------- описание слайдов ----------

def R(text, color=None, bold=False, size=None, link=None, italic=False):
    return {"text": text, "color": color, "bold": bold, "size": size, "link": link, "italic": italic}


def P(*runs, bullet=False, size=None, after=0, align=None, color=None):
    runs = [R(r) if isinstance(r, str) else r for r in runs]
    return {"runs": runs, "bullet": bullet, "size": size, "after": after, "align": align, "color": color}


class Slide:
    def __init__(self, title=None, lead=None, notes=""):
        self.items, self.notes, self.title = [], notes, title
        if title:
            self.text(X0, 0.45, CW, 0.75, [P(R(title, bold=True))], size=32, valign="m")
        if lead:
            self.text(X0, 1.2, CW, 0.55, [P(lead)], size=16, color=MUTED, valign="t")

    def text(self, x, y, w, h, paras, size=16, color=TEXT, align="l", valign="t"):
        paras = [P(p) if isinstance(p, str) else p for p in paras]
        self.items.append(dict(t="text", x=x, y=y, w=w, h=h, paras=paras, size=size, color=color,
                               align=align, valign=valign))

    def rect(self, x, y, w, h, fill=CARD, radius=0.12, shape="rect"):
        self.items.append(dict(t="rect", x=x, y=y, w=w, h=h, fill=fill, radius=radius, shape=shape))

    def card(self, x, y, w, h, title=None, body=(), size=15, title_color=TEXT, pad=0.25, fill=CARD):
        self.rect(x, y, w, h, fill=fill)
        paras = []
        if title:
            paras.append(P(R(title, bold=True, color=title_color), size=size + 3, after=8))
        paras += [P(b) if isinstance(b, str) else b for b in body]
        self.text(x + pad, y + pad - 0.03, w - 2 * pad, h - 2 * pad + 0.03, paras, size=size)

    def table(self, x, y, cols, rows, row_h=0.45, size=15, heights=None):
        self.items.append(dict(t="table", x=x, y=y, cols=cols, rows=rows, row_h=row_h, size=size,
                               heights=heights or [row_h] * len(rows)))

    def bars(self, x, y, w, h, cats, series, fmt="#,##0"):
        self.items.append(dict(t="bars", x=x, y=y, w=w, h=h, cats=cats, series=series, fmt=fmt))

    def image(self, x, y, w, h, path, alt=""):
        self.items.append(dict(t="img", x=x, y=y, w=w, h=h, path=path, alt=alt))


def C(*runs, align="l", sub=None):
    """Ячейка таблицы: runs + необязательная вторая строка мелким шрифтом."""
    runs = [R(r) if isinstance(r, str) else r for r in runs]
    return {"runs": runs, "align": align, "sub": sub}


def pair(py, go, win=None, bold=False):
    """«Python / Go» с цветами языков; win = 'py' | 'go' выделяет победителя."""
    return C(R(py, PY, bold=bold or win == "py"), R(" / ", MUTED), R(go, GO, bold=bold or win == "go"), align="r")


def build_slides():
    s = []

    # 1. Титул
    t = Slide()
    t.text(0.8, 1.6, 6.3, 4.3, [
        P(R("Go", GO, bold=True), R(" vs ", bold=True), R("Python", PY, bold=True), R(":", bold=True), size=48, after=10),
        P(R("два AI-агента пишут один backend", bold=True), size=40),
    ], valign="m")
    t.image(7.55, 1.05, 5.2, 5.2, ROOT / "assets/author.jpg", "Автор доклада")
    s.append(t)

    # 2. Методика
    t = Slide("Методика", notes="Метрики сняты из транскриптов агентов, а не с их слов. "
              "Старт обоих агентов одновременно, в 12:57 UTC; отдельные каталоги, порты и compose-проекты.")
    t.rect(X0, 1.6, 5.8, 4.9)
    t.text(X0 + 0.4, 1.95, 5.0, 4.3, [
        P(R("МОДЕЛЬ", MUTED, bold=True), size=13, after=4),
        P(R("Claude Opus 5.5", bold=True), size=30, after=28),
        P(R("ПРОМТ", MUTED, bold=True), size=13, after=4),
        P(R("Один и тот же, отличается только язык: ", bold=True), R("Go", GO, bold=True),
          R(" или ", bold=True), R("Python", PY, bold=True), size=24),
    ])
    sx, sw = 6.75, 5.98
    t.card(sx, 1.6, sw, 2.3, "Go", ["chi · pgx · squirrel · goose", "образ distroless"], size=17, title_color=GO)
    t.card(sx, 4.2, sw, 2.3, "Python", ["FastAPI · asyncpg · Pydantic v2 · SQLAlchemy/Alembic · uv",
                                        "образ python-slim"], size=17, title_color=PY)
    t.text(X0, 6.6, CW, 0.3, ["Стеки агенты выбрали сами."], size=12, color=MUTED)
    s.append(t)

    # 3. Хронология
    t = Slide("Хронология", "Go-агент закончил на 10,6 минуты раньше. Каждый ревьюер работал около 5–6 минут.")
    tx, tw, span = 2.3, 10.43, 93.0
    for lbl, m in [("12:57", 0), ("13:30", 33), ("14:00", 63), ("14:30", 93)]:
        xx = tx + tw * m / span
        t.rect(xx - 0.005, 2.05, 0.01, 2.35, fill=LINE, radius=0)
        t.text(xx - 0.5, 4.45, 1.0, 0.3, [lbl], size=12, color=MUTED, align="c")
    rows = [("Go", GO, 0, 73.2, "разработка · 73,2 мин", BG), ("Python", PY, 0, 83.8, "разработка · 83,8 мин", BG),
            ("Ревью", ACC, 84, 90, "", BG)]
    for i, (name, col, a, b, lbl, fg) in enumerate(rows):
        yy = 2.15 + i * 0.75
        t.text(X0, yy, 1.6, 0.5, [P(R(name, col, bold=True))], size=18, valign="m")
        t.rect(tx, yy, tw, 0.5, fill=CARD, radius=0.08)
        t.rect(tx + tw * a / span, yy, tw * (b - a) / span, 0.5, fill=col, radius=0.08)
        if lbl:
            t.text(tx + 0.2, yy, 4, 0.5, [P(R(lbl, fg, bold=True))], size=15, valign="m")
    cw = (CW - 3 * 0.3) / 4
    for i, (tm, col, txt) in enumerate([("12:57", TEXT, "старт обоих агентов"), ("14:10", GO, "Go сдал работу"),
                                        ("14:21", PY, "Python сдал работу, запуск ревью"),
                                        ("14:27", ACC, "обе оценки готовы")]):
        t.card(X0 + i * (cw + 0.3), 5.05, cw, 1.45, None,
               [P(R(tm, col, bold=True), size=26, after=4), P(txt, size=15)])
    s.append(t)

    # 4. Скорость и стоимость
    t = Slide("Скорость и стоимость", "Python работал дольше, но потратил на 30% меньше токенов: меньше вызовов, короче контекст.",
              notes="Больше 95% «всех токенов» — повторное чтение контекста из кеша на каждом шаге. Поэтому стоимость "
                    "определяют число шагов и длина контекста, а не объём написанного кода. Ревью: Go — 4,4 млн токенов "
                    "и 40 вызовов, Python — 3,6 млн токенов и 31 вызов.")
    g = lambda v, w=False: C(R(v, GO, bold=w), align="r")
    p = lambda v, w=False: C(R(v, PY, bold=w), align="r")
    t.table(X0, 1.95, [3.9, 2.0, 2.0], [
        [C(R("Метрика", bold=True)), C(R("Go", GO, bold=True), align="r"), C(R("Python", PY, bold=True), align="r")],
        [C("Время разработки"), g("73,2 мин", True), p("83,8 мин")],
        [C("Вызовы API модели"), g("135"), p("116", True)],
        [C("Сгенерировано токенов"), g("262 025"), p("207 734", True)],
        [C("Записано в кеш"), g("684 371"), p("508 411", True)],
        [C("Прочитано из кеша"), g("29,9 млн"), p("20,9 млн", True)],
        [C(R("Всего токенов", bold=True)), g("30,9 млн", True), p("21,6 млн", True)],
        [C("Контекст на финише"), g("351 тыс."), p("289 тыс.")],
        [C(R("Стоимость по ценам API*", bold=True)), g("≈ $14,7", True), p("≈ $10,9", True)],
    ], row_h=0.46, size=15)
    rx, rw = 8.85, 3.88
    t.card(rx, 1.95, rw, 2.0, None, [P(R("−30%", PY, bold=True), size=44, after=4),
                                     P("токенов у Python: 21,6 млн против 30,9 млн", size=15)])
    t.card(rx, 4.15, rw, 2.0, None, [P(R("$10,9", PY, bold=True), R("  vs  ", MUTED), R("$14,7", GO, bold=True), size=34, after=6),
                                     P(">95% токенов — дешёвое чтение из кеша", size=15)])
    t.text(X0, 6.35, CW, 0.5, ["* Прайс Claude Opus 5.5 на ноябрь 2026, за 1 млн токенов: вход $4, генерация $20, "
                                "запись в кеш $5, чтение из кеша $0,20. В среднем ≈ $0,5 за 1 млн токенов."],
           size=12, color=MUTED)
    s.append(t)

    # 5. Объём кода
    t = Slide("Объём кода", "Go длиннее на 27% в коде и на 47% в тестах: явная обработка ошибок и маппинг строк.")
    t.bars(X0, 1.9, 7.4, 4.55, ["Рабочий код", "Тесты", "SQL-миграции"],
           [("Go", GO, [4350, 1821, 176]), ("Python", PY, [3424, 1241, 201])])
    t.text(X0, 6.5, 7.4, 0.35, ["Непустые строки без комментариев; vendor/, .venv и lock-файлы исключены."],
           size=12, color=MUTED)
    fx, fw = 8.35, 4.38
    t.card(fx, 1.9, fw, 1.4, "Файлов исходников", [P(R("46 .go", GO, bold=True), R("  ·  ", MUTED), R("58 .py", PY, bold=True))])
    t.card(fx, 3.5, fw, 1.6, "openapi.yaml", [P(R("Go: ", GO, bold=True), "651 строка вручную"),
                                             P(R("Python: ", PY, bold=True), "2 996, из кода")])
    t.card(fx, 5.3, fw, 1.4, "Документация", ["~11 тыс. строк Markdown у каждого"])
    s.append(t)

    # 6. Тесты и проверки
    t = Slide("Тесты и проверки", "Оба агента довели всё до зелёного. Ревьюеры перезапустили проверки сами и получили те же результаты.")
    t.table(X0, 1.95, [3.2, 4.46, 4.47], [
        [C(R("Проверка", bold=True)), C(R("Go", GO, bold=True)), C(R("Python", PY, bold=True))],
        [C("Тесты"), C("50 функций + 6 подтестов, 10 пакетов ok"), C("139 passed (70 unit + 69 integration), 8,6 с")],
        [C("Покрытие бизнес-логики"), C(R("92,5%", GO, bold=True)), C(R("97,0%", PY, bold=True))],
        [C("Линтеры"), C("golangci-lint · go vet: 0 замечаний"), C("ruff + mypy --strict: 0 замечаний")],
        [C("EXPLAIN на 1 млн книг"), C("40/40 без Seq Scan"), C("40/40 без Seq Scan")],
        [C("Повтор у ревьюера"), C(R("✓ прошло", OK, bold=True)), C(R("✓ прошло", OK, bold=True))],
        [C("Итераций до зелёного"), C("seed ×3, explain ×5, integration ×5"), C("~7 по тестам + 4 по производительности")],
    ], row_h=0.5, size=15)
    t.card(X0, 5.65, CW, 1.2, None, [P(R("Общая ловушка. ", ACC, bold=True),
                                       "Для партиционированной таблицы pg_get_indexdef возвращает ON ONLY, и индексы, "
                                       "пересобранные после seed, оставались пустыми. Оба агента нашли и исправили это сами.")],
           size=15)
    s.append(t)

    # 7. Производительность
    t = Slide("Производительность", "Цифры показательные, но напрямую не сравнимы: разные профили нагрузки, общий перегруженный хост.")
    lw = 5.6
    t.text(X0, 1.9, lw, 0.4, [P(R("Seed: ~6 млн строк", bold=True))], size=18)
    t.table(X0, 2.4, [2.4, 1.6, 1.6], [
        [C(""), C(R("Go", GO, bold=True), align="r"), C(R("Python", PY, bold=True), align="r")],
        [C("COPY"), C(R("9,0 с", GO, bold=True), align="r"), C(R("15,5 с", PY), align="r")],
        [C("Индексы + FK"), C(R("33 с", GO), align="r"), C(R("24,1 с", PY, bold=True), align="r")],
        [C("VACUUM ANALYZE"), C(R("25 с", GO), align="r"), C(R("12,1 с", PY, bold=True), align="r")],
        [C(R("Итого", bold=True)), C(R("~68 с", GO), align="r"), C(R("52,0 с*", PY, bold=True), align="r")],
    ], row_h=0.45, size=15)
    t.text(X0, 4.85, lw, 1.0, ["* Python — PGDATA в tmpfs (RAM), Go — на Docker-томе. Сам COPY у Go в 1,8 раза быстрее."],
           size=12, color=MUTED)
    kx, kw = 6.65, 6.08
    t.text(kx, 1.9, kw, 0.4, [P(R("k6, нагрузка на API", bold=True))], size=18)
    t.table(kx, 2.4, [1.79, 1.43, 1.43, 1.43], [
        [C(""), C(R("Go", GO, bold=True), align="r"), C(R("Go", GO, bold=True), align="r"), C(R("Python", PY, bold=True), align="r")],
        [C("Пользователи"), C("8", align="r"), C("32", align="r"), C("64", align="r")],
        [C("RPS"), C("321", align="r"), C("365", align="r"), C(R("560", PY, bold=True), align="r")],
        [C("p50"), C(R("5,1 мс", GO, bold=True), align="r"), C("13,4 мс", align="r"), C("95 мс", align="r")],
        [C("p95"), C("129 мс", align="r"), C("508 мс", align="r"), C("266 мс", align="r")],
        [C("p99"), C("443 мс", align="r"), C("1 526 мс", align="r"), C("412 мс", align="r")],
        [C("Ошибки"), C("0%", align="r"), C("0%", align="r"), C("0%", align="r")],
    ], row_h=0.42, size=15)
    t.text(kx, 5.5, kw, 1.0, ["Хост Go был перегружен (load average 20–190), Python гонял БД в RAM. "
                              "Хвосты p95/p99 у обоих упираются в /search и CPU."], size=12, color=MUTED)
    s.append(t)

    # 8. Эксплуатация
    t = Slide("Эксплуатация")
    t.rect(X0, 1.6, 5.0, 4.95)
    t.text(X0 + 0.4, 1.9, 4.2, 4.4, [
        P(R("DOCKER-ОБРАЗ", MUTED, bold=True), size=13, after=14),
        P(R("16 МБ", GO, bold=True), size=60, after=0),
        P(R("против", MUTED), size=16, after=0),
        P(R("255 МБ", PY, bold=True), size=60, after=14),
        P("distroless static против python-slim + venv: разница в 16 раз", size=15),
    ])
    ex, ew = 5.95, 6.78
    t.card(ex, 1.6, ew, 2.45, "Общее у обоих", [
        P("Multi-stage, non-root, healthcheck", bullet=True, after=4),
        P("Отдельные сервисы migrate и seed в compose", bullet=True, after=4),
        P("/healthz, /readyz, /metrics, JSON-логи", bullet=True, after=4),
        P("Ошибки в формате RFC 7807, X-User-Id через middleware", bullet=True)])
    t.card(ex, 4.3, ew, 2.25, "Различия", [
        P(R("Go: ", GO, bold=True), "vendor/ для офлайн-сборки, линтер в Docker", bullet=True, after=4),
        P(R("Python: ", PY, bold=True), "OpenAPI из кода, тест ловит расхождения", bullet=True, after=4),
        P(R("Python: ", PY, bold=True), "make test/lint требуют uv на хосте", bullet=True)])
    s.append(t)

    # 9. Мини-задачи
    t = Slide("Мини-задачи: условия и результаты",
              "Три небольших скрипта, одинаковые ТЗ и данные. Обе реализации каждой задачи прошли скрытую проверку.")
    hd = lambda x: C(R(x, bold=True), align="r")
    t.table(X0, 1.95, [3.4, 1.55, 1.65, 1.75, 1.75, 2.03], [
        [C(R("Задача", bold=True)), hd("Время, мин"), hd("Токены, млн"), hd("Стоимость, $"), hd("Код, строк"), hd("Проверка")],
        [C(R("Excel → графики", bold=True), sub="400 «грязных» строк, 5 PNG + отчёт"),
         pair("6,7", "22,2", "py"), pair("1,65", "5,0", "py"), pair("1,68", "3,10", "py"), pair("810", "1 429"),
         C(R("75/75 · 75/75", OK, bold=True), align="r")],
        [C(R("Рассылка по расписанию", bold=True), sub="cron, таймзоны, повторы SMTP"),
         pair("12,7", "11,4"), pair("2,69", "2,53"), pair("2,17", "2,27"), pair("984", "1 331"),
         C(R("11/11 · 11/11", OK, bold=True), align="r")],
        [C(R("Мат-фильтр на regexp", bold=True), sub="обход маскировки, без ложных"),
         pair("11,9", "13,7"), pair("2,30", "2,30"), pair("2,20", "2,43"), pair("477", "707"),
         C(R("F1 "), R("0,983", PY), R(" / ", MUTED), R("0,992", GO, bold=True), align="r")],
        [C(R("Итого", bold=True)), pair("31,3", "47,3", bold=True), pair("6,64", "9,83", bold=True),
         pair("6,05", "7,80", bold=True), pair("2 271", "3 467", bold=True), C(R("Go: +53% кода"), align="r")],
    ], heights=[0.5, 0.85, 0.85, 0.85, 0.5], size=15)
    t.text(X0, 5.85, CW, 0.9, [
        P(R("В ячейках: "), R("Python", PY, bold=True), R(" / "), R("Go", GO, bold=True),
          ". Скрытые проверки агенты не видели: эталонный summary.json (75 значений), 11 проверок писем через "
          "API Mailpit, 120 размеченных фраз. Стоимость — по прайсу Claude Opus 5.5.")], size=12, color=MUTED)
    s.append(t)

    # 10. Что повлияло
    t = Slide("Мини-задачи: что повлияло", "Разницу делает не язык как таковой, а готовые библиотеки под задачу.")
    hw = (CW - 0.3) / 2
    t.card(X0, 1.95, hw, 4.6, "Excel: Python в 3,3 раза быстрее и в 3 раза дешевле", [
        P("openpyxl и matplotlib закрыли задачу «из коробки»: 29 вызовов инструментов, 6,7 минуты", bullet=True, after=10),
        P("В gonum/plot нет кольцевой диаграммы, Go-агент рисовал её сам. Плюс паника на пустых данных и ручная "
          "вёрстка подписей: 67 вызовов", bullet=True, after=10),
        P("Минус Go из-за окружения: локальный golangci-lint несовместим с go1.27", bullet=True)],
           size=16, title_color=PY)
    t.card(X0 + hw + 0.3, 1.95, hw, 4.6, "Рассылка: паритет · Мат-фильтр: Go чуть точнее", [
        P("Оба сервиса прошли 11/11 проверок. Go-агент нашёл у себя гонку благодаря go test -race", bullet=True, after=10),
        P("В regexp Go (RE2) нет lookahead/lookbehind, агент проверял слово целиком — и всё равно поймал «бл@ть», "
          "которое пропустил Python", bullet=True, after=10),
        P("Скорость фильтра: Go ~220 тыс. строк/с, Python ~75 тыс.", bullet=True)],
           size=16, title_color=GO)
    s.append(t)

    # 11–12. Исследования
    def research(title, lead, cards, color, note=None):
        t = Slide(title, lead)
        cw2, ch = (CW - 0.3) / 2, 2.05
        for i, (h, body, links) in enumerate(cards):
            x, y = X0 + (i % 2) * (cw2 + 0.3), 1.95 + (i // 2) * (ch + 0.2)
            src = []
            for j, (name, url) in enumerate(links):
                if j:
                    src.append(R(" · ", MUTED))
                src.append(R(name, ACC, link=url))
            t.card(x, y, cw2, ch, h, [P(body, after=6), P(*src, size=12)], size=14, title_color=color)
        if note:
            t.text(X0, 6.35, CW, 0.55, [note], size=12, color=MUTED)
        return t

    s.append(research(
        "Исследования: за Python", "Почему AI-агенту в среднем проще писать на Python: публичные данные 2022–2026.", [
            ("Больше обучающих данных",
             "The Stack v2 после дедупликации: Python — 233 ГБ, Go — 55 ГБ, в 4,3 раза меньше. В v1: 80 против 35 ГБ.",
             [("StarCoder2 / The Stack v2", "https://arxiv.org/abs/2402.19173"),
              ("The Stack v1", "https://arxiv.org/abs/2211.15533")]),
            ("Выше доля решённых реальных задач",
             "Multi-SWE-bench (NeurIPS 2025): Python и Java обгоняют Go и Rust. SWE-bench Multilingual: на Go решено 31% задач.",
             [("Multi-SWE-bench", "https://arxiv.org/abs/2504.02605"),
              ("SWE-bench Multilingual", "https://www.swebench.com/multilingual.html")]),
            ("Меньше токенов на решение",
             "Менее популярные языки стоят дороже Python при той же доле успеха. С Claude Code Python экономнее в 6 из 12 задач, Go — в 3.",
             [("Tokenmaxxing, SPLASH 2026", "https://arxiv.org/abs/2607.22807"),
              ("llms-token-perf", "https://github.com/vikas-t/llms-token-perf")]),
            ("Бенчмарки заточены под Python",
             "HumanEval, MBPP и SWE-bench изначально только на Python. Это одна из причин разрыва по Multi-SWE-bench.",
             [("MultiPL-E", "https://arxiv.org/abs/2208.08227")]),
        ], PY))

    s.append(research(
        "Исследования: за Go", "Где Python уступает: ошибки, которые компилятор ловит до запуска, и проблемы экосистемы.", [
            ("Типы ловят основные ошибки LLM",
             "94% ошибок компиляции в коде от LLM — провалы проверки типов (PLDI 2025). В Python они всплывают только при запуске.",
             [("Type-Constrained Generation", "https://arxiv.org/abs/2504.09246"),
              ("GitHub Octoverse 2025", "https://github.blog/ai-and-ml/llms/why-ai-is-pushing-developers-toward-typed-languages/")]),
            ("Компилятор как страховочная сетка",
             "Google: статическая типизация Go — «страховочная сетка для агентного кода». Плюс быстрая сборка, gofmt и «один способ».",
             [("Google Developers Blog", "https://developers.googleblog.com/why-go-is-an-ideal-language-for-ai-assisted-software-engineering/"),
              ("Bruin", "https://getbruin.com/blog/go-is-the-best-language-for-agents/")]),
            ("Устаревшие API в Python-библиотеках",
             "Модели берут устаревшие API в 25–38% случаев (ICSE 2025). Гарантия совместимости Go 1 это смягчает (наш вывод).",
             [("ICSE 2025", "https://arxiv.org/abs/2406.09834"), ("arXiv 2604.09515", "https://arxiv.org/abs/2604.09515")]),
            ("Выдуманные пакеты",
             "19,7% пакетов, рекомендованных моделями, не существуют (Python и JS). Модули Go привязаны к URL и go.sum.",
             [("USENIX Security 2025",
               "https://www.usenix.org/system/files/conference/usenixsecurity25/sec25cycle1-prepub-742-spracklen.pdf")]),
        ], GO, note="Слабые места Go для агентов: многословность (больше кода и токенов) и гонки данных — 41% гонок в Go-коде "
                    "Uber — захват переменной в горутине, компилятор их не ловит (PLDI 2022)."))

    # 13. Сверка
    t = Slide("Сверка с нашим экспериментом", "Часть выводов исследований подтвердилась, часть — нет: сильная модель 2026 года сокращает разрыв.")
    yes, no, part = C(R("да", OK, bold=True), align="c"), C(R("нет*", BAD, bold=True), align="c"), C(R("частично", PY, bold=True), align="c")
    t.table(X0, 1.95, [3.9, 6.53, 1.7], [
        [C(R("Тезис из исследований", bold=True)), C(R("Что увидели мы", bold=True)), C(R("Совпало?", bold=True), align="c")],
        [C("На Python агент тратит меньше токенов"), C("Python: 21,6 млн, Go: 30,9 млн (+43%)"), yes],
        [C("На Go код многословнее"), C("Код Go на 27% длиннее, тесты — на 47%"), yes],
        [C("На Go агенты справляются хуже"), C("Оценки ревьюеров 8,0 (Go) и 7,8 (Python), Go закончил на 10 мин раньше"), no],
        [C("Типы ловят ошибки до запуска"), C("Python: ошибки 500 на граничном вводе. Go: логическая ошибка лимита курсора, её типы не ловят"), part],
        [C("У Go проще эксплуатация"), C("Образ 16 МБ против 255 МБ, офлайн-сборка из vendor/"), yes],
    ], heights=[0.5, 0.6, 0.6, 0.75, 0.75, 0.6], size=15)
    t.text(X0, 5.95, CW, 0.8, ["* Бенчмарки 2024–25 мерили более слабые модели на чужих репозиториях. У нас новый проект с нуля, "
                                "одна модель и n = 1, поэтому вывод предварительный. Источники — experiment/RESEARCH.md."],
           size=12, color=MUTED)
    s.append(t)

    # 14. MVP
    t = Slide("Почему Python удобнее для MVP", "На этапе MVP скорость изменений и простота отладки важнее размера образа и задержек.",
              notes="Интерпретатор в контейнере: docker compose exec app python -c \"…\", разовая миграция данных, починка записи, "
                    "выгрузка CSV. Go-образ (distroless) не содержит даже shell: healthcheck пришлось встроить в бинарник. "
                    "Octoverse 2025: на Python почти половина новых AI-репозиториев (582 тыс.), +850 тыс. контрибьюторов за год.")
    cw3, ch3 = (CW - 0.6) / 3, 2.0
    mvp = [("Читаемый и короткий код", "Нет if err != nil и ручного маппинга. Код на 27% короче, его легче читать на ревью."),
           ("Интерпретатор в контейнере", "Любой скрипт или REPL без пересборки образа. В distroless-образе Go нет даже shell."),
           ("Быстрый цикл правок", "Нет компиляции: правка → uvicorn ‑‑reload → результат за секунды."),
           ("OpenAPI и валидация из коробки", "FastAPI и Pydantic генерируют Swagger из кода. Go-агент писал YAML вручную."),
           ("Экосистема для данных и AI", "pandas, numpy, SDK всех LLM, Jupyter. Аналитика и LLM-функции — без второго языка."),
           ("Проще найти людей и помощь", "Python в двойке самых популярных языков GitHub, и у моделей на нём больше данных.")]
    for i, (h, body) in enumerate(mvp):
        t.card(X0 + (i % 3) * (cw3 + 0.3), 1.95 + (i // 3) * (ch3 + 0.2), cw3, ch3, h, [body], size=14, title_color=PY)
    t.text(X0, 6.5, CW, 0.4, [P(R("Когда переходить на Go: ", GO, bold=True),
                                  "MVP подтвердился и упёрся в производительность, память или размер образа.")], size=14)
    s.append(t)

    # 15. Выводы
    t = Slide("Выводы", notes="Артефакты: go_impl/, python_impl/, журнал experiment/LOG.md, метрики experiment/*_metrics.json, "
              "ревью experiment/review_*.md, исследования experiment/RESEARCH.md, мини-задачи experiment/MINI_RESULTS.md.")
    concl = [("Язык почти не влияет на результат", "Оценки 8,0 и 7,8, архитектура совпадает. Решают модель и ТЗ."),
             ("Go быстрее, Python дешевле", "Go на 13% быстрее, Python на 30% дешевле по токенам. Код Go длиннее на 27–53%."),
             ("Мелкие скрипты — за Python", "С готовыми библиотеками (Excel, графики) в 3 раза быстрее и дешевле."),
             ("У Go — плюсы эксплуатации", "Образ 16 МБ против 255 МБ, быстрее COPY, ниже медианная задержка."),
             ("Python сильнее в тестах и типах", "Покрытие 97%, mypy --strict, OpenAPI из кода с тестом на расхождения."),
             ("Ревью обязательно", "Оба отчитались «всё зелёное», ревьюеры за 5–6 минут нашли баги с 500/400."),
             ("10 млрд — с оговорками", "Поиск одним PostgreSQL не масштабируется. Оба вынесли это в документацию.")]
    colw = (CW - 0.4) / 2
    for i, (h, body) in enumerate(concl):
        col, row = (0, i) if i < 4 else (1, i - 4)
        x, y = X0 + col * (colw + 0.4), 1.55 + row * 1.32
        t.rect(x, y + 0.05, 0.55, 0.55, fill=ACC, shape="oval")
        t.text(x, y + 0.05, 0.55, 0.55, [P(R(str(i + 1), BG, bold=True))], size=18, align="c", valign="m")
        t.text(x + 0.8, y, colw - 0.8, 1.2, [P(R(h, bold=True), size=18, after=4), P(body, color=MUTED)], size=15)
    s.append(t)

    # 16. Как сделана презентация
    t = Slide("Как сделана презентация", "Оба варианта, .pptx и HTML, собирает один скрипт на Python из общего описания слайдов.",
              notes="Пересборка: experiment/deck/.venv/bin/python -I experiment/deck/build.py. "
                    "Проверка: рендер в PDF через LibreOffice и скриншоты HTML в Chrome.")
    t.rect(X0, 1.95, 4.0, 4.6)
    t.text(X0 + 0.4, 2.3, 3.2, 4.0, [
        P(R("ЯЗЫК", MUTED, bold=True), size=13, after=4),
        P(R("Python 3.12", PY, bold=True), size=36, after=24),
        P(R("СКРИПТ", MUTED, bold=True), size=13, after=4),
        P(R("experiment/deck/build.py", bold=True), size=17, after=6),
        P("~700 строк: описание 16 слайдов и два рендерера, в PowerPoint и в HTML", size=15, color=MUTED),
    ])
    t.table(X0 + 4.3, 1.95, [2.4, 1.3, 4.13], [
        [C(R("Библиотека", bold=True)), C(R("Версия", bold=True), align="r"), C(R("Зачем", bold=True))],
        [C(R("python-pptx", PY, bold=True)), C("1.0.2", align="r"), C("слайды, таблицы, диаграмма, заметки")],
        [C(R("lxml", PY, bold=True)), C("6.0.2", align="r"), C("правка XML темы и маркеров списков")],
        [C(R("Pillow", PY, bold=True)), C("12.3.0", align="r"), C("вставка фото на титул")],
        [C(R("XlsxWriter", PY, bold=True)), C("3.2.9", align="r"), C("данные нативной диаграммы")],
        [C(R("typing_extensions", PY, bold=True)), C("4.15.0", align="r"), C("зависимость python-pptx")],
        [C(R("стандартная", PY, bold=True)), C("—", align="r"), C("html, pathlib, sys: HTML-версия")],
    ], heights=[0.5] + [0.65] * 6, size=15)
    t.text(X0, 6.65, CW, 0.3, ["HTML-версия — без внешних библиотек: шаблон и листание на чистом JavaScript."],
           size=12, color=MUTED)
    s.append(t)
    return s


# ---------- рендер в PPTX ----------

def rgb(h):
    return RGBColor.from_string(h)


def fill_paragraphs(tf, paras, size, color, align):
    al = {"l": PP_ALIGN.LEFT, "c": PP_ALIGN.CENTER, "r": PP_ALIGN.RIGHT}
    for i, para in enumerate(paras):
        p = tf.paragraphs[0] if i == 0 else tf.add_paragraph()
        p.alignment = al[para.get("align") or align]
        p.line_spacing = 1.05
        if para["after"]:
            p.space_after = Pt(para["after"])
        if para["bullet"]:
            pPr = p._p.get_or_add_pPr()
            pPr.set("marL", str(Emu(Inches(0.25))))
            pPr.set("indent", str(-Emu(Inches(0.25))))
            etree.SubElement(pPr, qn("a:buFont")).set("typeface", FONT)
            etree.SubElement(pPr, qn("a:buChar")).set("char", "•")
        for r in para["runs"]:
            run = p.add_run()
            run.text = r["text"]
            f = run.font
            f.name = FONT
            f.size = Pt(r["size"] or para["size"] or size)
            f.bold = r["bold"]
            f.italic = r["italic"]
            f.color.rgb = rgb(r["color"] or para["color"] or color)
            if r["link"]:
                run.hyperlink.address = r["link"]


def render_pptx(slides, out):
    prs = Presentation()
    prs.slide_width, prs.slide_height = Inches(W), Inches(H)
    blank = prs.slide_layouts[6]
    for n, sd in enumerate(slides, 1):
        sl = prs.slides.add_slide(blank)
        sl.background.fill.solid()
        sl.background.fill.fore_color.rgb = rgb(BG)
        items = list(sd.items)
        if n > 1:
            items.append(dict(t="text", x=X0, y=7.0, w=6, h=0.3, paras=[P("Go vs Python · AI-агенты")], size=11,
                              color=MUTED, align="l", valign="m"))
            items.append(dict(t="text", x=W - X0 - 2, y=7.0, w=2, h=0.3, paras=[P(f"{n} / {TOTAL}")], size=11,
                              color=MUTED, align="r", valign="m"))
        for it in items:
            x, y = Inches(it["x"]), Inches(it["y"])
            if it["t"] == "text":
                tb = sl.shapes.add_textbox(x, y, Inches(it["w"]), Inches(it["h"]))
                tf = tb.text_frame
                tf.word_wrap = True
                tf.auto_size = MSO_AUTO_SIZE.NONE
                tf.margin_left = tf.margin_right = tf.margin_top = tf.margin_bottom = 0
                tf.vertical_anchor = {"t": MSO_ANCHOR.TOP, "m": MSO_ANCHOR.MIDDLE, "b": MSO_ANCHOR.BOTTOM}[it["valign"]]
                fill_paragraphs(tf, it["paras"], it["size"], it["color"], it["align"])
            elif it["t"] == "rect":
                kind = MSO_SHAPE.OVAL if it["shape"] == "oval" else (
                    MSO_SHAPE.ROUNDED_RECTANGLE if it["radius"] else MSO_SHAPE.RECTANGLE)
                sh = sl.shapes.add_shape(kind, x, y, Inches(it["w"]), Inches(it["h"]))
                sh.fill.solid()
                sh.fill.fore_color.rgb = rgb(it["fill"])
                sh.line.fill.background()
                sh.shadow.inherit = False
                if kind == MSO_SHAPE.ROUNDED_RECTANGLE:
                    sh.adjustments[0] = min(0.5, it["radius"] / min(it["w"], it["h"]))
            elif it["t"] == "img":
                pic = sl.shapes.add_picture(str(it["path"]), x, y, Inches(it["w"]), Inches(it["h"]))
                pic._element.nvPicPr.cNvPr.set("descr", it["alt"])
            elif it["t"] == "table":
                rows, cols = it["rows"], it["cols"]
                gf = sl.shapes.add_table(len(rows), len(cols), x, y, Inches(sum(cols)), Inches(sum(it["heights"])))
                tbl = gf.table
                tbl.first_row = tbl.horz_banding = False
                tbl._tbl.tblPr.find(qn("a:tableStyleId")).text = "{2D5ABB26-0587-4C30-8999-92F81FD0307C}"
                for j, cwid in enumerate(cols):
                    tbl.columns[j].width = Inches(cwid)
                for i, row in enumerate(rows):
                    tbl.rows[i].height = Inches(it["heights"][i])
                    for j, cell_d in enumerate(row):
                        cell = tbl.cell(i, j)
                        cell.fill.solid()
                        cell.fill.fore_color.rgb = rgb(CARD2 if i == 0 else (CARD if i % 2 else BG))
                        cell.margin_left = cell.margin_right = Inches(0.14)
                        cell.margin_top = cell.margin_bottom = Inches(0.04)
                        cell.vertical_anchor = MSO_ANCHOR.MIDDLE
                        tf = cell.text_frame
                        tf.word_wrap = True
                        paras = [P(*cell_d["runs"])]
                        if cell_d["sub"]:
                            paras.append(P(R(cell_d["sub"], MUTED), size=12))
                        fill_paragraphs(tf, paras, it["size"], TEXT, cell_d["align"])
            elif it["t"] == "bars":
                cd = CategoryChartData()
                cd.categories = it["cats"]
                for name, _, vals in it["series"]:
                    cd.add_series(name, vals)
                gf = sl.shapes.add_chart(XL_CHART_TYPE.BAR_CLUSTERED, x, y, Inches(it["w"]), Inches(it["h"]), cd)
                ch = gf.chart
                ch.font.name, ch.font.size, ch.font.color.rgb = FONT, Pt(14), rgb(TEXT)
                ch.has_legend = True
                ch.legend.position = XL_LEGEND_POSITION.TOP
                ch.legend.include_in_layout = False
                plot = ch.plots[0]
                plot.gap_width, plot.overlap = 45, -8
                plot.has_data_labels = True
                dl = plot.data_labels
                dl.number_format, dl.number_format_is_linked = it["fmt"], False
                dl.position = XL_LABEL_POSITION.OUTSIDE_END
                dl.font.size, dl.font.bold, dl.font.color.rgb = Pt(14), True, rgb(TEXT)
                for ser, (_, col, _) in zip(plot.series, it["series"]):
                    ser.format.fill.solid()
                    ser.format.fill.fore_color.rgb = rgb(col)
                ca = ch.category_axis
                ca.reverse_order = True
                ca.format.line.color.rgb = rgb(LINE)
                ca.tick_labels.font.size, ca.tick_labels.font.color.rgb = Pt(15), rgb(TEXT)
                va = ch.value_axis
                va.visible = False
                va.has_major_gridlines = False
        if sd.notes:
            sl.notes_slide.notes_text_frame.text = sd.notes
    set_link_colors(prs)
    prs.save(out)


def set_link_colors(prs):
    """Гиперссылки в PowerPoint берут цвет из темы (hlink), а не из run — красим тему."""
    theme = next(r.target_part for r in prs.slide_master.part.rels.values() if r.reltype.endswith("/theme"))
    root = etree.fromstring(theme.blob)
    for tag in ("a:hlink", "a:folHlink"):
        el = root.find(".//" + qn(tag))
        for ch in list(el):
            el.remove(ch)
        etree.SubElement(el, qn("a:srgbClr")).set("val", ACC)
    theme._blob = etree.tostring(root, xml_declaration=True, encoding="UTF-8", standalone=True)


# ---------- рендер в HTML ----------

PX = 96  # пикселей на дюйм: 13,333 × 7,5 дюйма = 1280 × 720


def css_pos(it):
    return f"left:{it['x'] * PX:.1f}px;top:{it['y'] * PX:.1f}px;width:{it['w'] * PX:.1f}px;height:{it['h'] * PX:.1f}px"


def html_runs(runs, size, color):
    out = []
    for r in runs:
        st = [f"color:#{r['color'] or color}"]
        if r["bold"]:
            st.append("font-weight:700")
        if r["size"]:
            st.append(f"font-size:{r['size']}pt")
        t = html.escape(r["text"])
        if r["link"]:
            out.append(f'<a href="{html.escape(r["link"])}" target="_blank" rel="noopener" style="{";".join(st)}">{t}</a>')
        else:
            out.append(f'<span style="{";".join(st)}">{t}</span>')
    return "".join(out)


def html_paras(paras, size, color, align):
    out = []
    for p in paras:
        cls = ' class="b"' if p["bullet"] else ""
        st = f"font-size:{p['size'] or size}pt;margin-bottom:{p['after']}pt;text-align:{ {'l': 'left', 'c': 'center', 'r': 'right'}[p.get('align') or align]}"
        out.append(f'<p{cls} style="{st}">{html_runs(p["runs"], size, p["color"] or color)}</p>')
    return "".join(out)


def render_html(slides, out):
    parts = []
    for n, sd in enumerate(slides, 1):
        els = []
        for it in sd.items:
            if it["t"] == "text":
                jc = {"t": "flex-start", "m": "center", "b": "flex-end"}[it["valign"]]
                els.append(f'<div class="tx" style="{css_pos(it)};justify-content:{jc}">'
                           f'{html_paras(it["paras"], it["size"], it["color"], it["align"])}</div>')
            elif it["t"] == "rect":
                rad = "50%" if it["shape"] == "oval" else f"{it['radius'] * PX:.0f}px"
                els.append(f'<div class="rc" style="{css_pos(it)};background:#{it["fill"]};border-radius:{rad}"></div>')
            elif it["t"] == "img":
                src = Path(it["path"]).relative_to(ROOT).as_posix()
                els.append(f'<img class="im" src="{src}" alt="{html.escape(it["alt"])}" style="{css_pos(it)}">')
            elif it["t"] == "table":
                cols = "".join(f'<col style="width:{c * PX:.1f}px">' for c in it["cols"])
                trs = []
                for i, row in enumerate(it["rows"]):
                    bg = CARD2 if i == 0 else (CARD if i % 2 else BG)
                    tds = []
                    for cd in row:
                        sub = f'<div class="sub">{html.escape(cd["sub"])}</div>' if cd["sub"] else ""
                        al = {"l": "left", "c": "center", "r": "right"}[cd["align"]]
                        tds.append(f'<td style="text-align:{al}">{html_runs(cd["runs"], it["size"], TEXT)}{sub}</td>')
                    trs.append(f'<tr style="background:#{bg};height:{it["heights"][i] * PX:.1f}px">{"".join(tds)}</tr>')
                els.append(f'<table class="tb" style="left:{it["x"] * PX:.1f}px;top:{it["y"] * PX:.1f}px;'
                           f'width:{sum(it["cols"]) * PX:.1f}px;font-size:{it["size"]}pt"><colgroup>{cols}</colgroup>'
                           f'{"".join(trs)}</table>')
            elif it["t"] == "bars":
                mx = max(v for _, _, vals in it["series"] for v in vals)
                legend = "".join(f'<span><i style="background:#{c}"></i>{html.escape(nm)}</span>' for nm, c, _ in it["series"])
                groups = []
                for k, cat in enumerate(it["cats"]):
                    bars = "".join(
                        f'<div class="bar"><div class="fill" style="width:{vals[k] / mx * 82:.1f}%;background:#{c}"></div>'
                        f'<b>{vals[k]:,}</b></div>'.replace(",", " ") for _, c, vals in it["series"])
                    groups.append(f'<div class="grp"><div class="cat">{html.escape(cat)}</div><div class="bs">{bars}</div></div>')
                els.append(f'<div class="ch" style="{css_pos(it)}"><div class="lg">{legend}</div>{"".join(groups)}</div>')
        if n > 1:
            els.append(f'<div class="ft">Go vs Python · AI-агенты</div><div class="ft r">{n} / {TOTAL}</div>')
        parts.append(f'<section class="slide" aria-label="Слайд {n}">{"".join(els)}</section>')

    doc = TEMPLATE.replace("%SLIDES%", "\n".join(parts)).replace("%TOTAL%", str(TOTAL))
    for k, v in dict(BG=BG, CARD=CARD, TEXT=TEXT, MUTED=MUTED, ACC=ACC).items():
        doc = doc.replace(f"%{k}%", v)
    out.write_text(doc, encoding="utf-8")


TEMPLATE = """<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Go vs Python: слайды</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
html,body{margin:0;height:100%;background:#0F1014;color:#%TEXT%;font-family:Arial,Helvetica,sans-serif;overflow:hidden}
#stage{position:fixed;inset:0;display:flex;align-items:center;justify-content:center}
#deck{position:relative;width:1280px;height:720px;transform-origin:center center;flex:none}
.slide{position:absolute;inset:0;background:#%BG%;display:none;border-radius:4px;box-shadow:0 10px 40px rgba(0,0,0,.5)}
.slide.on{display:block}
.slide>*{position:absolute}
.tx{display:flex;flex-direction:column;line-height:1.18}
.tx p{margin:0}
.tx p.b{padding-left:24px;position:relative}
.tx p.b::before{content:"•";position:absolute;left:0}
.tx a{text-decoration:none}.tx a:hover{text-decoration:underline}
.im{object-fit:cover}
.tb{border-collapse:collapse;table-layout:fixed;line-height:1.18}
.tb td{padding:4px 13px;vertical-align:middle}
.tb .sub{font-size:12pt;color:#%MUTED%;margin-top:2px}
.ch{display:flex;flex-direction:column;justify-content:space-around;font-size:15pt}
.ch .lg{display:flex;gap:22px;justify-content:center;font-size:14pt}
.ch .lg i{display:inline-block;width:12px;height:12px;margin-right:7px;vertical-align:middle}
.ch .grp{display:grid;grid-template-columns:150px 1fr;align-items:center;gap:12px}
.ch .bar{display:flex;align-items:center;gap:8px;margin:5px 0}
.ch .fill{height:34px}
.ch .bar b{font-size:14pt}
.ft{left:57.6px;top:672px;width:576px;height:29px;font-size:11pt;color:#%MUTED%;display:flex;align-items:center}
.ft.r{left:auto;right:57.6px;width:192px;justify-content:flex-end}
#ctl{position:fixed;left:50%;transform:translateX(-50%);bottom:10px;opacity:.35;transition:opacity .2s;display:flex;gap:8px;align-items:center;font-size:14px;color:#%MUTED%}
#ctl button{background:#%CARD%;color:#%TEXT%;border:0;border-radius:6px;padding:7px 12px;font-size:15px;cursor:pointer}
#ctl button:hover{background:#3A3F4B}
#ctl:hover,#ctl:focus-within{opacity:1}
</style>
</head>
<body>
<div id="stage"><div id="deck">
%SLIDES%
</div></div>
<div id="ctl"><button id="prev" aria-label="Назад">←</button><span id="cnt">1 / %TOTAL%</span><button id="next" aria-label="Вперёд">→</button><button id="fs" aria-label="Полный экран">⛶</button></div>
<script>
const slides=[...document.querySelectorAll('.slide')], deck=document.getElementById('deck'), cnt=document.getElementById('cnt');
let i=Math.max(0,Math.min(slides.length-1,(parseInt(location.hash.slice(1))||1)-1));
function show(k){i=Math.max(0,Math.min(slides.length-1,k));slides.forEach((s,n)=>s.classList.toggle('on',n===i));
  cnt.textContent=(i+1)+' / '+slides.length;history.replaceState(null,'','#'+(i+1));}
function fit(){const s=Math.min(innerWidth/1280,(innerHeight-8)/720);deck.style.transform='scale('+s+')';}
document.getElementById('prev').onclick=()=>show(i-1);
document.getElementById('next').onclick=()=>show(i+1);
document.getElementById('fs').onclick=()=>document.fullscreenElement?document.exitFullscreen():document.documentElement.requestFullscreen();
addEventListener('keydown',e=>{
  if(['ArrowRight','PageDown',' ','Enter'].includes(e.key)){e.preventDefault();show(i+1)}
  else if(['ArrowLeft','PageUp','Backspace'].includes(e.key)){e.preventDefault();show(i-1)}
  else if(e.key==='Home')show(0); else if(e.key==='End')show(slides.length-1);
  else if(e.key==='f'||e.key==='F')document.getElementById('fs').click();
});
deck.addEventListener('click',e=>{if(e.target.closest('a'))return;show(i+(e.clientX<innerWidth/3?-1:1))});
addEventListener('resize',fit);fit();show(i);
</script>
</body>
</html>
"""


def main():
    slides = build_slides()
    assert len(slides) == TOTAL, len(slides)
    for n, sd in enumerate(slides, 1):
        for it in sd.items:
            if "w" in it and (it["x"] + it["w"] > W + 1e-6 or it["y"] + it.get("h", 0) > H + 1e-6):
                sys.exit(f"слайд {n}: элемент {it['t']} выходит за границы")
    render_pptx(slides, ROOT / "presentation.pptx")
    render_html(slides, ROOT / "presentation_ppt.html")
    print("ok:", ROOT / "presentation.pptx", ROOT / "presentation_ppt.html")


if __name__ == "__main__":
    main()
