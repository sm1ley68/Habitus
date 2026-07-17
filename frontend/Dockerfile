FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:22-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# Карта не обязательна для работы приложения: без ключа MapLibre-канвасы
# честно показывают заглушку, остальной UI работает.
ARG NEXT_PUBLIC_MAPTILER_KEY=""
ENV NEXT_PUBLIC_MAPTILER_KEY=$NEXT_PUBLIC_MAPTILER_KEY
# С output: "standalone" адрес прокси зашивается в билд, поэтому BACKEND_ORIGIN
# нужен на этапе сборки, а не только в рантайме. В compose это http://backend:8080.
ARG BACKEND_ORIGIN="http://backend:8080"
ENV BACKEND_ORIGIN=$BACKEND_ORIGIN
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
COPY --from=builder /app/public ./public
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
EXPOSE 3000
CMD ["node", "server.js"]
