import threading

import psycopg
from habitus.config import settings
from habitus.embed.document import content_hash

# размер словаря BGE-M3 (XLM-RoBERTa). Должен совпадать с sparsevec(...) в schema.sql.
SPARSE_DIM = 250002

# pgvector индексирует sparsevec только до 1000 ненулевых элементов
# (HNSW_MAX_NNZ в src/hnsw.h). Длинное объявление BGE-M3 раскладывает в больший
# словарь, и такая строка не даёт создать/наполнить HNSW — а без индекса
# sparse-канал ходит по всей таблице последовательным сканом на каждый поиск.
# Держим top-N по весу: отбрасываются самые слабые лексические сигналы, на
# ранжирование это влияет пренебрежимо (официально рекомендованное лечение —
# «prune the sparse vector»).
SPARSE_MAX_NNZ = 1000

# Раздельные локи инференса. HuggingFace fast-токенизаторы НЕ потокобезопасны:
# два конкурентных запроса, перенастраивающих усечение одного и того же
# токенизатора, роняют "RuntimeError: Already borrowed". Но у эмбеддера
# (BGE-M3) и реранкера — РАЗНЫЕ токенизаторы разных моделей, и общий лок на
# оба сериализовал больше, чем нужно: кодирование запроса второго пользователя
# ждало реранк первого, хотя друг другу они не мешают. EMBED_LOCK держит
# encode_texts здесь, RERANK_LOCK — rerank() в habitus/online/rerank.py.
EMBED_LOCK = threading.Lock()
RERANK_LOCK = threading.Lock()
# Публичное имя сохранено алиасом: на него ссылаются существующие импорты.
INFERENCE_LOCK = EMBED_LOCK

_model = None


def get_model():
    global _model
    if _model is None:
        import torch
        from FlagEmbedding import BGEM3FlagModel
        # fp16 стабилен и выгоден только на CUDA. На MPS (Apple Metal) half-precision
        # в torch роняет forward на длинных текстах, из-за чего внутренний авто-шринк
        # batch_size в FlagEmbedding доходит до 0 → tokenizer.pad([]) → IndexError.
        # На CPU fp16 всё равно не задействуется. Поэтому включаем только под CUDA.
        use_fp16 = torch.cuda.is_available()
        _model = BGEM3FlagModel(settings.embed_model, use_fp16=use_fp16)
    return _model


def prune_sparse(sparse: dict[int, float],
                 max_nnz: int = SPARSE_MAX_NNZ) -> dict[int, float]:
    """Оставить top-N весов, чтобы вектор был индексируемым.

    Тай-брейк по индексу токена — при равных весах результат детерминирован,
    иначе один и тот же документ давал бы разные векторы между прогонами.
    Ключи возвращаются отсортированными: to_sparsevec_literal ждёт порядок.
    """
    if len(sparse) <= max_nnz:
        return sparse
    kept = sorted(sparse.items(), key=lambda kv: (-kv[1], kv[0]))[:max_nnz]
    return dict(sorted(kept))


def encode_texts(texts: list[str], model=None) -> list[dict]:
    m = model or get_model()
    # Умеренный batch_size: дефолтные 256 текстов BGE-M3 на MPS перегружают память
    # (реальные объявления — проза до ~3 тыс. символов, не короткий структурный doc_text).
    with EMBED_LOCK:
        out = m.encode(texts, batch_size=settings.embed_batch_size,
                       return_dense=True, return_sparse=True,
                       return_colbert_vecs=False)
    results = []
    for dense, lex in zip(out["dense_vecs"], out["lexical_weights"]):
        # pgvector sparsevec требует индексы в диапазоне 1..dim. BGE-M3 отбрасывает
        # спец-токены (id 0–3), поэтому id 0 не появляется; фильтр — защита от
        # редкого id=0, чтобы невалидный индекс не ронял UPDATE всего батча.
        sparse = {int(k): float(v) for k, v in lex.items() if int(k) >= 1}
        results.append({"dense": list(map(float, dense)),
                        "sparse": prune_sparse(sparse)})
    return results


def to_sparsevec_literal(sparse: dict[int, float], dim: int) -> str:
    if not sparse:
        return f"{{}}/{dim}"
    items = ",".join(f"{k}:{v}" for k, v in sorted(sparse.items()))
    return f"{{{items}}}/{dim}"


def embed_pending(conn: psycopg.Connection, model=None) -> int:
    # берём все строки с doc_text и их сохранённый хэш; изменившиеся — те,
    # у кого hash(doc_text) != content_hash (в т.ч. NULL при первом прогоне).
    with conn.cursor() as cur:
        cur.execute("""SELECT external_id, doc_text, content_hash FROM listings
                       WHERE doc_text IS NOT NULL;""")
        rows = cur.fetchall()
    to_do = [(eid, txt) for eid, txt, stored in rows
             if stored != content_hash(txt)]
    if not to_do:
        return 0
    encoded = encode_texts([t for _, t in to_do], model=model)
    with conn.cursor() as cur:
        for (eid, txt), emb in zip(to_do, encoded):
            cur.execute(
                """UPDATE listings SET embedding=%s, sparse_embedding=%s::sparsevec,
                          content_hash=%s, updated_at=now() WHERE external_id=%s;""",
                (emb["dense"], to_sparsevec_literal(emb["sparse"], SPARSE_DIM),
                 content_hash(txt), eid))
    conn.commit()
    return len(to_do)
