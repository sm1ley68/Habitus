# habitus/online/cache.py — in-memory LRU по хэшу входного текста.
# Инвалидация не нужна: ключ детерминирован входом (спека 3.10).
from collections import OrderedDict
from typing import Any

from habitus.embed.document import content_hash


class LRUCache:
    def __init__(self, maxsize: int = 256):
        self.maxsize = maxsize
        self._d: OrderedDict[str, Any] = OrderedDict()

    def get(self, key_text: str) -> Any | None:
        k = content_hash(key_text)
        if k not in self._d:
            return None
        self._d.move_to_end(k)
        return self._d[k]

    def put(self, key_text: str, value) -> None:
        k = content_hash(key_text)
        self._d[k] = value
        self._d.move_to_end(k)
        if len(self._d) > self.maxsize:
            self._d.popitem(last=False)

    def clear(self) -> None:
        self._d.clear()


embed_cache = LRUCache()      # semantic_text → (dense, sparse)
parse_cache = LRUCache()      # текст запроса → ParsedQuery
explain_cache = LRUCache()    # запрос+ids выдачи → текст объяснения
