import psycopg
from habitus.config import settings
from habitus.embed.document import content_hash

# размер словаря BGE-M3 (XLM-RoBERTa). Должен совпадать с sparsevec(...) в schema.sql.
SPARSE_DIM = 250002

_model = None


def get_model():
    global _model
    if _model is None:
        from FlagEmbedding import BGEM3FlagModel
        _model = BGEM3FlagModel(settings.embed_model, use_fp16=True)
    return _model


def encode_texts(texts: list[str], model=None) -> list[dict]:
    m = model or get_model()
    out = m.encode(texts, return_dense=True, return_sparse=True,
                   return_colbert_vecs=False)
    results = []
    for dense, lex in zip(out["dense_vecs"], out["lexical_weights"]):
        sparse = {int(k): float(v) for k, v in lex.items()}
        results.append({"dense": list(map(float, dense)), "sparse": sparse})
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
