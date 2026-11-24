# salesreport

Консольная утилита на Go: читает Excel-файл с продажами (`Sales` + `Targets`),
чистит данные по правилам из ТЗ, считает агрегаты и строит отчёт —
`summary.json`, пять PNG-графиков, `report.html` и лог пропущенных строк.

## Быстрый старт

Точная команда запуска (из каталога проекта):

```bash
go run ./cmd/salesreport --input data/sales.xlsx --out out/
```

Результат появится в `out/`:

| Файл | Содержимое |
|---|---|
| `summary.json` | агрегаты строго по структуре ТЗ (ключи словарей отсортированы, суммы округлены до 2 знаков) |
| `revenue_by_month.png` | линия выручки по месяцам |
| `revenue_by_region.png` | столбцы по регионам |
| `category_share.png` | кольцевая диаграмма долей категорий |
| `top_products.png` | горизонтальные столбцы, топ-10 товаров |
| `plan_vs_fact.png` | сгруппированные столбцы «план/факт» по регионам с % выполнения |
| `report.html` | одна самодостаточная страница: ключевые цифры, все графики (встроены как data URI), таблицы |
| `skipped.csv` | пропущенные строки: `row,order_id,reason,detail` (номер строки — как в Excel, заголовок = 1) |

В stderr пишется короткий структурированный лог (`log/slog`): сколько строк
прочитано, валидных и пропущенных. С флагом `-v` каждая пропущенная строка
дополнительно логируется в stderr с причиной и номером строки.

## Установка

Требуется Go 1.27+ (проверено на go1.27.3, macOS arm64).

```bash
# зависимости (корпоративный прокси недоступен — используется публичный)
env -u HTTPS_PROXY -u HTTP_PROXY GOPROXY=https://proxy.golang.org,direct go mod download

# бинарник
go build -o bin/salesreport ./cmd/salesreport
./bin/salesreport --input data/sales.xlsx --out out/
```

Флаги:

```
--input string   путь к .xlsx (обязательный)
--out string     каталог для результатов (по умолчанию "out", создаётся при необходимости)
-v               логировать каждую пропущенную строку в stderr
```

Коды выхода: `0` — успех, `1` — ошибка обработки (нет файла, нет листа/колонки,
битый план), `2` — неверные аргументы.

## Примеры

```bash
make run          # то же, что go run ./cmd/salesreport --input data/sales.xlsx --out out/
make test         # go test -race -cover ./...
make lint         # gofmt + go vet + golangci-lint v2.15.0 (собирается текущим Go)

# подробный лог пропусков
go run ./cmd/salesreport --input data/sales.xlsx --out out/ -v 2>&1 | grep "row skipped" | head

# причины пропусков
cut -d, -f3 out/skipped.csv | sort | uniq -c
```

Фактический результат на `data/sales.xlsx`:

```
valid_rows=4833 skipped_rows=200 total_revenue=207149040.50
пропуски: 33 duplicate_order_id, 33 invalid_order_date, 101 invalid_quantity, 33 invalid_unit_price
```

Фрагмент `summary.json`:

```json
{
  "valid_rows": 4833,
  "skipped_rows": 200,
  "total_revenue": 207149040.50,
  "revenue_by_month": { "2025-01": 17481031.50, "...": 0 },
  "plan_vs_fact": [
    { "region": "Екатеринбург", "fact": 36125409.00, "target": 31870000.00, "completion_pct": 113.35 }
  ]
}
```

## Правила очистки

1. Полностью пустые строки игнорируются (не валидные и не пропущенные).
2. Дубликаты `order_id` (после обрезки пробелов): обрабатывается только первое
   вхождение, даже если оно невалидно; остальные — `duplicate_order_id`.
3. `order_date` — серийный номер даты Excel или текст `DD.MM.YYYY`, иначе `invalid_order_date`.
4. `quantity` — целое > 0, иначе `invalid_quantity`.
5. `unit_price` — число > 0, разделитель `.` или `,` (`"1234,50"`), иначе `invalid_unit_price`.
6. `discount` — доля 0..1 или `"15%"`; пусто → 0; иное → `invalid_discount`.
7. `region` обрезается по краям.
8. Выручка = `quantity * unit_price * (1 - discount)`; суммирование с компенсацией
   ошибки (Neumaier), округление до 2 знаков только при выводе.

Дополнительные решения (пустой `region`/`category`/`product`, формат чисел в JSON и т.п.)
описаны в [DECISIONS.md](DECISIONS.md).

## Структура

```
cmd/salesreport/      точка входа (флаги, коды выхода)
internal/app/         конвейер: чтение → очистка → агрегаты → файлы; интеграционный тест
internal/source/      чтение .xlsx (excelize), сопоставление колонок по заголовку
internal/sales/       модель, правила очистки, агрегаты
internal/report/      summary.json, skipped.csv, report.html (html/template)
internal/charts/      PNG-графики (gonum/plot, шрифт Liberation Sans с кириллицей)
internal/numfmt/      форматирование чисел: «1 234 567,89», «12,5 млн»
```

Библиотеки: [`github.com/xuri/excelize/v2`](https://github.com/xuri/excelize) —
чтение Excel, [`gonum.org/v1/plot`](https://github.com/gonum/plot) — графики.
Остальное — стандартная библиотека.
