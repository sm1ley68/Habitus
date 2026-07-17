// Единственная точка настройки доступа к бэку. Приложение всегда ходит в
// реальный Go-шлюз — моков нет.
//
// Путь относительный: next.config.mjs проксирует /api/v1/* на BACKEND_ORIGIN,
// поэтому запросы уходят same-origin и сессионная кука habitus_session
// (HTTPOnly, SameSite=Lax) долетает без CORS.
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "/api/v1";
