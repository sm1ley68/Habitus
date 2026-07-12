#!/usr/bin/env python
"""Детерминированная нарезка московского среза из Kaggle-датасета.

Воспроизводит `data/kaggle/moscow_5k.csv`, к которому привязаны relevant_ids
в `habitus/eval/queries.yaml` (external_id — хэш содержимого строки, стабилен).

Использование:
    uv run python scripts/build_moscow_subset.py \
        --src data/kaggle/all_v2.csv --out data/kaggle/moscow_5k.csv --n 5000

Датасет: Kaggle mrdaniilak/russia-real-estate-20182021 (region 3 = Москва).
"""
import argparse
from pathlib import Path

import pandas as pd

SEED = 42
MOSCOW_REGION = 3  # см. habitus/config.py: city_region_code


def build(src: Path, out: Path, n: int) -> int:
    parts = []
    for chunk in pd.read_csv(src, chunksize=500_000):
        parts.append(chunk[chunk["region"] == MOSCOW_REGION])
    df = pd.concat(parts)
    # базовый отсев явного мусора до сэмпла — те же пороги, что при исходной нарезке
    df = df[(df["price"] > 1_000_000) & (df["price"] < 300_000_000)
            & (df["area"] > 10) & (df["area"] < 400)]
    sub = df.sample(n=n, random_state=SEED)
    out.parent.mkdir(parents=True, exist_ok=True)
    sub.to_csv(out, index=False)
    return len(sub)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", type=Path, default=Path("data/kaggle/all_v2.csv"))
    ap.add_argument("--out", type=Path, default=Path("data/kaggle/moscow_5k.csv"))
    ap.add_argument("--n", type=int, default=5000)
    args = ap.parse_args()
    written = build(args.src, args.out, args.n)
    print(f"записано {written} строк -> {args.out} (seed={SEED}, region={MOSCOW_REGION})")
