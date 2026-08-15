#!/bin/sh
# scripts/refresh.sh — один цикл обновления базы объявлений.
#
#   сбор с Циана  →  заливка в БД  →  гашение снятого с продажи
#
# Запускается из планировщика (cron/launchd, см. docs/cian-parser.md) или руками.
# Идемпотентен: и парсер, и загрузчик обновляют строки по внешнему id, а не
# задваивают. Переэмбеддинг идёт только по изменившимся объявлениям.
#
# Параллельные запуски невозможны: цикл берёт блокировку и молча выходит, если
# предыдущий ещё идёт. Иначе два парсера писали бы один CSV, а два пайплайна
# конкурировали бы за GPU/MPS.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CSV=${HABITUS_CSV:-$ROOT/listings.csv}
LOCK=${HABITUS_LOCK:-${TMPDIR:-/tmp}/habitus-refresh.lock}
LOG=${HABITUS_LOG:-$ROOT/data/refresh.log}
MAX_OFFERS=${HABITUS_MAX_OFFERS:-5000}
ROOMS=${HABITUS_ROOMS:-1,2,3,4}

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG"; }

mkdir -p "$(dirname -- "$LOG")"

# mkdir атомарен на всех POSIX-ФС — в отличие от `test -f && touch`, у которого
# между проверкой и созданием есть гонка. flock есть не везде (на macOS нет).
if ! mkdir "$LOCK" 2>/dev/null; then
    log "пропуск: предыдущий цикл ещё идёт ($LOCK)"
    exit 0
fi
trap 'rmdir "$LOCK" 2>/dev/null || true' EXIT INT TERM

log "цикл начат"

# 1. Сбор. Без прокси Циан отдаёт капчу — парсер сам об этом скажет и вернёт
#    ненулевой код. Тогда цикл прекращается: заливать нечего, а старый CSV
#    трогать нельзя, иначе гашение сочтёт все объявления пропавшими.
if [ "${HABITUS_SKIP_COLLECT:-0}" = "1" ]; then
    log "сбор пропущен (HABITUS_SKIP_COLLECT=1), заливаю имеющийся $CSV"
elif [ -z "${CIAN_PROXIES:-}" ] && [ -z "${HABITUS_PROXY_FILE:-}" ]; then
    # Без резидентного прокси Циан отдаёт капчу на любой запрос. Планировщику
    # незачем падать каждый цикл: пропускаем сбор, но заливку делаем — база
    # остаётся консистентной, а сбор начнётся сам, как только появятся прокси.
    log "прокси не настроены (CIAN_PROXIES/HABITUS_PROXY_FILE) — сбор пропущен"
else
    log "сбор объявлений"
    ( cd "$ROOT/backend" && go run ./cmd/cian-parser \
        --output "$CSV" \
        --rooms "$ROOMS" \
        --max-offers "$MAX_OFFERS" \
        ${HABITUS_PROXY_FILE:+--proxy-file "$HABITUS_PROXY_FILE"} ) >>"$LOG" 2>&1
fi

# 2. Заливка: upsert объявлений, гашение исчезнувших, обогащение,
#    пересчёт doc_text и эмбеддингов по изменившимся строкам.
log "заливка в БД"
( cd "$ROOT" && uv run habitus offline --csv "$CSV" --source cian ) >>"$LOG" 2>&1

log "цикл завершён"
