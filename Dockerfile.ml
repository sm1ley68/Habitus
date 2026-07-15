FROM python:3.12-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential curl \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir uv

# Copy dependency manifests first so rebuilds only re-run `uv sync` when they
# change — this stack pulls in torch/transformers/FlagEmbedding, a genuinely
# heavy image, so layer ordering matters for iteration speed.
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project

COPY habitus ./habitus
RUN uv sync --frozen --no-dev

ENV PATH="/app/.venv/bin:$PATH"

EXPOSE 8000
CMD ["uv", "run", "uvicorn", "habitus.online.service:app", "--host", "0.0.0.0", "--port", "8000"]
