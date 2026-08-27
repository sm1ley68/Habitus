# Search Workspace And Chat Implementation Plan

**Goal:** После поиска объединить список квартир, карту и продолжающий поиск чат в одном рабочем экране, одновременно убрать моргание маркеров и обновить стиль индикатора.

**Approach:** Добавить в Zustand отдельный режим уточнения существующего поиска, который сохраняет текущую выдачу до полного ответа сервера. Вынести общий трёхколоночный workspace для экранов «Результаты» и «Карта», а обновление viewport применять атомарно вместе с новыми картографическими данными.

**Key tools/dependencies:** React, Zustand, Google Maps JavaScript API, Framer Motion, существующий SSE `AgentClient`, Tailwind CSS.

---

## Task 1: Сохранить историю поиска и поддержать уточнение из workspace

**Files:**
- Modify: `frontend/lib/store/session.ts`

1. Добавить тип `SearchMessage` с полями `id`, `role: "user" | "assistant"`, `text`.
2. Добавить состояния `searchMessages: SearchMessage[]` и `searchUpdating: boolean`.
3. В `startQuery` записывать пользовательскую реплику, а после `finish` — накопленный `answer` как ответ ассистента.
4. Добавить `refineQuery(client, query)`: продолжать текущий `chatId`, не очищать `properties`, `mapListings`, `layerData` и не уходить с workspace.
5. После полного результата уточнения одним обновлением заменять выдачу, зону и подпись; при ошибке оставлять старые результаты и показывать сообщение в чат-панели.

## Task 2: Применять viewport только после полной загрузки данных

**Files:**
- Modify: `frontend/lib/store/session.ts`
- Modify: `frontend/components/map/MapCanvas.tsx`
- Modify: `frontend/app/globals.css`

1. Изменить `setViewport`: не записывать новый viewport до ответа `fetchListings` и `fetchLayers`.
2. В `refreshViewport` атомарно записывать `viewport`, `mapListings` и `layerData` только для последнего request id.
3. Удалить приглушение маркеров во время `mapUpdating`; существующие точки остаются с opacity 1 до полного ответа.
4. После атомарной замены дать новым маркерам короткое появление `220ms ease`, не создавая промежуточного пустого состояния.

## Task 3: Создать чат продолжения поиска

**Files:**
- Create: `frontend/components/result/SearchWorkspaceChat.tsx`

1. Показать компактный заголовок «Уточнить поиск» и пояснение, что сообщения обновляют подборку.
2. Вывести сохранённые реплики пользователя и ассистента отдельными пузырями.
3. Во время SSE показывать текущий `answer` и неблокирующий статус обработки.
4. Добавить компактный composer; блокировать повторную отправку только пока `searchUpdating` true.
5. Отправлять сообщения через `createSearchClient()` и `refineQuery()`.

## Task 4: Собрать общий workspace списка, карты и чата

**Files:**
- Create: `frontend/components/result/SearchWorkspace.tsx`
- Modify: `frontend/components/result/ResultScreen.tsx`
- Modify: `frontend/components/map/MapScreen.tsx`

1. Создать desktop-grid `minmax(270px,0.72fr) minmax(420px,1.8fr) minmax(280px,0.8fr)`.
2. Расположить `PropertyList` слева, `MapCanvas` в центре, `SearchWorkspaceChat` справа.
3. Над картой сохранить `ZoneChip` и `LayerToggles`.
4. На узких экранах расположить карту первой, список второй, чат третьим.
5. Использовать workspace в `ResultScreen`; в `MapScreen` показывать его, если текущий поиск содержит квартиры, иначе оставить полноэкранный обзор карты.

## Task 5: Увеличить и перекрасить индикатор карты

**Files:**
- Modify: `frontend/components/map/MapUpdateIndicator.tsx`

1. Увеличить высоту, внутренние отступы, spinner и текст примерно в 1.5 раза.
2. Использовать полупрозрачный сиреневый фон и сиреневую границу.
3. Оставить текст полностью непрозрачным тёмно-сиреневым.
4. Сохранить `pointer-events-none`, правый верхний угол и отсутствие общего оверлея.

## Task 6: Проверить интеграцию без изменения тестов

**Files:**
- Verify only; тестовые файлы не создавать, не изменять и не запускать согласно `AGENTS.md`.

1. Запустить `npm run build` в `frontend`.
2. Выполнить `git diff --check` и убедиться, что тестовые файлы не изменены.
3. Пересобрать только Docker frontend без удаления volumes, если Docker CLI доступен.
4. В браузере проверить: три колонки после поиска, отправку уточнения, сохранение старых маркеров до ответа, мягкую замену и новую сиреневую плашку.
