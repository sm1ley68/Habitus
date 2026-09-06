# Fullscreen Property List Implementation Plan

**Goal:** перенести на актуальный `main` полноэкранный список квартир, сохранив новый командный редизайн и исключив локальный mock-режим.

**Approach:** состояние расширения остаётся локальным в `SearchWorkspace`. `PropertyList` получает управляемые props, показывает Radix-иконку в существующем заголовке и переключает вертикальный список на адаптивную CSS Grid. При расширении карта и чат не размонтируются из общего состояния приложения, но временно не рендерятся в рабочей области; Escape возвращает обычный режим.

**Key tools/dependencies:** React state/effect, Tailwind CSS Grid, Framer Motion, `@radix-ui/react-icons`, Next.js production build. Автотесты не создаются и не запускаются по правилу `AGENTS.md`.

---

## Task 1. Preserve current-team behavior

**Files:** read `frontend/components/auth/AuthGate.tsx`, `frontend/lib/store/auth.ts`, `frontend/components/result/PropertyCard.tsx`; do not modify.

- [x] Подтвердить, что `AuthGate` уже вызывает `ensureSession()` и создаёт гостевую сессию через текущий auth-store.
- [x] Подтвердить, что `PropertyCard` уже содержит `shrink-0` и не сжимается в вертикальном списке.
- [x] Исключить из публикации `NEXT_PUBLIC_USE_MOCK`, `frontend/lib/mock/*` и mock-ветки API, потому что этот режим был согласован только для локальной машины пользователя.

## Task 2. Add the fullscreen control

**Files:** modify `frontend/package.json`, `frontend/package-lock.json`, `frontend/components/result/PropertyList.tsx`.

- [x] Установить `@radix-ui/react-icons@^1.3.2` существующим package manager.
- [x] Добавить props `expanded?: boolean` и `onExpandedChange?: (expanded: boolean) => void`.
- [x] В заголовке рядом с индикатором обновления добавить кнопку 32x32 с `EnterFullScreenIcon`/`ExitFullScreenIcon`, `aria-label`, `title`, focus-ring и active-feedback.
- [x] В расширенном режиме применить сетку `grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4`; в обычном режиме сохранить текущий вертикальный список.
- [x] Для empty-state в расширенном режиме применить `col-span-full`.

## Task 3. Own expansion state in the workspace

**Files:** modify `frontend/components/result/SearchWorkspace.tsx`.

- [x] Добавить локальный `listExpanded` и передать его в `PropertyList`.
- [x] При `listExpanded=true` скрывать карту и `SearchWorkspaceChat`, чтобы список занимал всю рабочую область.
- [x] Добавить обработчик Escape с обязательным cleanup через `removeEventListener`.
- [x] Не менять Zustand-состояние поиска, порядок объектов и логику `rankVisibleProperties`.

## Task 4. Verify and publish

**Files:** inspect all staged files; do not modify tests.

- [x] Запустить `npm run build` в `frontend` и получить exit code 0.
- [x] Проверить `git diff --check` и убедиться, что mock-файлы не попали в diff.
- [x] Проверить видимые строки и accessibility: кнопка имеет разные подписи для expand/collapse, Escape закрывает режим, empty/loading states сохранены.
- [ ] Создать Conventional Commit на русском и отправить обычным fast-forward push в `origin/main`.
