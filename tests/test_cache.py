from habitus.online.cache import LRUCache, embed_cache, explain_cache, parse_cache


def test_lru_get_put_by_text_hash():
    c = LRUCache(maxsize=2)
    assert c.get("нет") is None
    c.put("запрос", 42)
    assert c.get("запрос") == 42


def test_lru_evicts_oldest():
    c = LRUCache(maxsize=2)
    c.put("a", 1); c.put("b", 2)
    c.get("a")            # a — свежий
    c.put("c", 3)         # вытесняет b
    assert c.get("b") is None and c.get("a") == 1 and c.get("c") == 3


def test_lru_clear_and_singletons_exist():
    c = LRUCache()
    c.put("x", 1); c.clear()
    assert c.get("x") is None
    for cache in (embed_cache, parse_cache, explain_cache):
        assert isinstance(cache, LRUCache)
