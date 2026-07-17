import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Куда проксировать API. В compose это http://backend:8080 (имя сервиса),
// локально — localhost:8080.
const BACKEND_ORIGIN = process.env.BACKEND_ORIGIN ?? "http://localhost:8080";

/** @type {import('next').NextConfig} */
const nextConfig = {
  outputFileTracingRoot: __dirname,
  output: "standalone",
  // Встроенный gzip Next буферизует ответ целиком — это ломает SSE-стриминг
  // поиска (события копятся и приходят разом в конце). Отключаем сжатие, чтобы
  // прокси отдавал поисковый поток инкрементально (backend уже шлёт
  // X-Accel-Buffering: no и флашит каждый кадр).
  compress: false,
  async rewrites() {
    return [
      // Same-origin проксирование: кука сессии ходит без CORS и SameSite-плясок.
      { source: "/api/v1/:path*", destination: `${BACKEND_ORIGIN}/api/v1/:path*` },
      // display_fields.go отдаёт cover_image как "/static/placeholder-cover.svg" —
      // относительный путь, который иначе ушёл бы в Next и вернул 404.
      { source: "/static/:path*", destination: `${BACKEND_ORIGIN}/static/:path*` },
    ];
  },
};

export default nextConfig;
