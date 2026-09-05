# Премиальный редизайн фронта Habitus — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Заменить дефолтный визуальный язык Next.js на редакционно-архивный, в котором цвет кодирует происхождение факта, и перебрать четыре момента: первый экран, ожидание поиска, карточку выдачи, досье.

**Architecture:** Снизу вверх. Сначала слой токенов и шрифтов, затем примитивы `components/ui/`, затем компонент `<Provenance>`, который выводит цвет из данных, и только потом четыре экрана. Каждая задача оставляет приложение работающим и коммитится отдельно.

**Tech Stack:** Next.js 15, React, TypeScript, Tailwind, framer-motion, vitest + @testing-library/react, Go (одна правка в шлюзе).

**Spec:** `docs/superpowers/specs/2026-09-05-frontend-premium-redesign-design.md`

## Global Constraints

- Палитра: `paper` `#F7F5F2`, `ink` `#1A1815`, `ink-muted` `#5B564E`, `ink-faint` `#8C857A`, `evidence` `#14493C`, `estimate` `#9A7534`, `compromise` `#A65A43`.
- Индиго `#6f7cc8` и `#7C8CFF` не остаётся нигде.
- Шрифты: Literata (заголовки, досье), Golos Text (интерфейс). Geist удаляется из зависимостей.
- Заголовок вкладки: `Habitus`.
- Скругления 4–8 px. Анимации 180–240 мс, ease-out, без пружин на карточках. `prefers-reduced-motion` уважается везде, где уже уважается.
- Цвет происхождения ставится ТОЛЬКО компонентом `<Provenance>` или через его хелпер. Руками `text-evidence` на факте не писать.
- Выдумывать факты запрещено: если данных для подписи нет, элемент не рисуется.
- Команды проверки перед каждым коммитом: `cd frontend && npm test && npx tsc --noEmit`.
- Коммиты: Conventional Commits на русском, без трейлеров и подписей (`CLAUDE.md`).

---

### Task 1: Токены, шрифты, название продукта

**Files:**
- Modify: `frontend/app/globals.css:19-25`
- Modify: `frontend/tailwind.config.ts`
- Modify: `frontend/app/layout.tsx:1-11,18`
- Modify: `frontend/package.json` (удалить зависимость `geist`)
- Test: `frontend/lib/tokens.test.ts` (создать)

**Interfaces:**
- Produces: CSS-переменные `--paper --ink --ink-muted --ink-faint --evidence --estimate --compromise --font-display --font-ui`; классы Tailwind `bg-paper text-ink text-ink-muted text-ink-faint text-evidence text-estimate text-compromise`; экспорт `PALETTE` из `frontend/lib/tokens.ts`.

- [ ] **Step 1: Написать падающий тест на палитру**

Создать `frontend/lib/tokens.test.ts`:

```ts
import { PALETTE } from "./tokens";

it("палитра не содержит индиго дефолтного комплекта", () => {
  const values = Object.values(PALETTE).map((v) => v.toLowerCase());
  expect(values).not.toContain("#6f7cc8");
  expect(values).not.toContain("#7c8cff");
});

it("цвета происхождения заданы и различимы", () => {
  expect(PALETTE.evidence).toBe("#14493C");
  expect(PALETTE.estimate).toBe("#9A7534");
  expect(PALETTE.compromise).toBe("#A65A43");
  const set = new Set([PALETTE.evidence, PALETTE.estimate, PALETTE.compromise]);
  expect(set.size).toBe(3);
});
```

- [ ] **Step 2: Прогнать тест, убедиться что падает**

Run: `cd frontend && npx vitest run lib/tokens.test.ts`
Expected: FAIL — `Cannot find module './tokens'`

- [ ] **Step 3: Завести источник правды по цвету**

Создать `frontend/lib/tokens.ts`:

```ts
/** Единственный источник правды по цвету. tailwind.config.ts и globals.css
 *  читают отсюда, чтобы палитра не разъехалась по трём файлам. */
export const PALETTE = {
  paper: "#F7F5F2",
  ink: "#1A1815",
  inkMuted: "#5B564E",
  inkFaint: "#8C857A",
  // Цвета происхождения факта: чем подтверждена величина, тем спокойнее цвет.
  evidence: "#14493C",
  estimate: "#9A7534",
  compromise: "#A65A43",
} as const;
```

- [ ] **Step 4: Прогнать тест, убедиться что проходит**

Run: `cd frontend && npx vitest run lib/tokens.test.ts`
Expected: PASS

- [ ] **Step 5: Подключить палитру в Tailwind**

В `frontend/tailwind.config.ts` заменить блок `colors`:

```ts
import { PALETTE } from "./lib/tokens";

// ...
      colors: {
        paper: PALETTE.paper,
        ink: { DEFAULT: PALETTE.ink, muted: PALETTE.inkMuted, faint: PALETTE.inkFaint },
        evidence: PALETTE.evidence,
        estimate: PALETTE.estimate,
        compromise: PALETTE.compromise,
      },
      fontFamily: {
        display: ["var(--font-display)", "Georgia", "serif"],
        sans: ["var(--font-ui)", "system-ui", "sans-serif"],
        mono: ["var(--font-mono)", "monospace"],
      },
```

- [ ] **Step 6: Заменить шрифты и название**

В `frontend/app/layout.tsx`: убрать импорты `geist/font/*`, добавить

```ts
import { Literata, Golos_Text } from "next/font/google";

const display = Literata({ subsets: ["cyrillic", "latin"], variable: "--font-display",
                           weight: ["400", "500", "600"], display: "swap" });
const ui = Golos_Text({ subsets: ["cyrillic", "latin"], variable: "--font-ui",
                        weight: ["400", "500", "600"], display: "swap" });
```

`metadata.title` — `"Habitus"`. В `<html>` подставить `${display.variable} ${ui.variable}`.

- [ ] **Step 7: Обновить `:root` в globals.css**

В `frontend/app/globals.css` заменить `--accent: #6f7cc8` и `--glow-color: #7C8CFF` на переменные из палитры; `--accent` оставить как алиас `var(--evidence)`, чтобы карта и пины (`.lmap-pin`, `.pin--top`) не сломались одним махом — они читают `--accent` в десятке мест.

- [ ] **Step 8: Убрать geist и проверить сборку**

```bash
cd frontend && npm uninstall geist && npm test && npx tsc --noEmit && npm run build
```
Expected: сборка проходит; если `next/font/google` не смог скачать гарнитуры — положить woff2 в `frontend/app/fonts/` и перейти на `next/font/local` (проверено 5 сентября: `fonts.googleapis.com` отвечает 200).

- [ ] **Step 9: Коммит**

```bash
git add frontend/lib/tokens.ts frontend/lib/tokens.test.ts frontend/tailwind.config.ts \
        frontend/app/layout.tsx frontend/app/globals.css frontend/package.json frontend/package-lock.json
git commit -m "feat: визуальные токены и типографика вместо дефолтного комплекта Next.js"
```

---

### Task 2: Компонент происхождения факта

**Files:**
- Create: `frontend/components/ui/Provenance.tsx`
- Create: `frontend/components/ui/Provenance.test.tsx`
- Modify: `frontend/components/ui/index.ts`

**Interfaces:**
- Consumes: `PALETTE` из Task 1.
- Produces: `<Provenance kind={ProvenanceKind} />`, тип `ProvenanceKind = "measured" | "estimate" | "compromise"`, и функции `provenanceOfSource(kind: string): ProvenanceKind` и `provenanceOfLeg(leg: {estimate_kind?: string | null}): ProvenanceKind`.

- [ ] **Step 1: Написать падающий тест**

```tsx
import { render, screen } from "@testing-library/react";
import Provenance, { provenanceOfSource, provenanceOfLeg } from "./Provenance";

it("источник-наблюдение — подтверждённая величина", () => {
  expect(provenanceOfSource("observation")).toBe("measured");
  expect(provenanceOfSource("computation")).toBe("measured");
  expect(provenanceOfSource("proxy")).toBe("estimate");
});

it("плечо по прямой — оценка, по сети — замер", () => {
  expect(provenanceOfLeg({ estimate_kind: "straight_line" })).toBe("estimate");
  expect(provenanceOfLeg({ estimate_kind: "model" })).toBe("estimate");
  expect(provenanceOfLeg({})).toBe("measured");
});

it("метка подписана словом, а не только цветом", () => {
  render(<Provenance kind="estimate" />);
  expect(screen.getByText("оценка")).toBeInTheDocument();
});
```

- [ ] **Step 2: Прогнать, убедиться что падает**

Run: `cd frontend && npx vitest run components/ui/Provenance.test.tsx`
Expected: FAIL — модуль не найден

- [ ] **Step 3: Реализовать**

```tsx
import { PALETTE } from "@/lib/tokens";

export type ProvenanceKind = "measured" | "estimate" | "compromise";

const LABEL: Record<ProvenanceKind, string> = {
  measured: "замер", estimate: "оценка", compromise: "компромисс",
};
const COLOR: Record<ProvenanceKind, string> = {
  measured: PALETTE.evidence, estimate: PALETTE.estimate, compromise: PALETTE.compromise,
};

/** kind источника блока досье (schema.BlockSource.kind) → происхождение. */
export function provenanceOfSource(kind: string): ProvenanceKind {
  return kind === "proxy" ? "estimate" : "measured";
}

/** Плечо маршрута: estimate_kind заполнен — величина не замерена. */
export function provenanceOfLeg(leg: { estimate_kind?: string | null }): ProvenanceKind {
  return leg.estimate_kind ? "estimate" : "measured";
}

/** Цвет здесь — усиление, носитель смысла — слово: дальтоник и печать
 *  на чёрно-белом принтере обязаны читать то же самое. */
export default function Provenance({ kind, className = "" }:
  { kind: ProvenanceKind; className?: string }) {
  return (
    <span style={{ color: COLOR[kind] }}
          className={`text-[11px] uppercase tracking-[0.08em] ${className}`}>
      {LABEL[kind]}
    </span>
  );
}
```

- [ ] **Step 4: Прогнать тест**

Run: `cd frontend && npx vitest run components/ui/Provenance.test.tsx`
Expected: PASS

- [ ] **Step 5: Экспортировать из барреля и закоммитить**

Добавить в `frontend/components/ui/index.ts`: `export { default as Provenance, provenanceOfSource, provenanceOfLeg } from "./Provenance";`

```bash
cd frontend && npm test && npx tsc --noEmit
git add frontend/components/ui/Provenance.tsx frontend/components/ui/Provenance.test.tsx frontend/components/ui/index.ts
git commit -m "feat: происхождение факта как компонент, а не как цвет руками"
```

---

### Task 3: Примитивы ui/ на новый язык

**Files:**
- Modify: `frontend/components/ui/Button.tsx`, `Card.tsx`, `Badge.tsx`, `Field.tsx`, `Input.tsx`, `Select.tsx`, `Dialog.tsx`, `Toast.tsx`
- Test: `frontend/components/ui/ui.test.tsx` (существующий — должен продолжать проходить)

**Interfaces:**
- Consumes: токены Task 1. Публичные пропсы примитивов НЕ меняются: экраны их не переписывают.

- [ ] **Step 1: Убедиться, что текущие тесты примитивов зелёные**

Run: `cd frontend && npx vitest run components/ui/ui.test.tsx`
Expected: PASS (снимок «до»)

- [ ] **Step 2: Перевести примитивы на токены**

Образец правки — `Badge.tsx`, остальные семь файлов приводятся тем же способом:

```tsx
import { PALETTE } from "@/lib/tokens";

// Тон совпадает с происхождением факта: один и тот же зелёный означает
// «подтверждено» и в бейдже статуса, и в досье — пользователь не переучивается.
const TONE: Record<BadgeTone, { bg: string; color: string }> = {
  neutral: { bg: "#EFECE7", color: PALETTE.inkMuted },
  ok:      { bg: "#E7EFEA", color: PALETTE.evidence },
  warn:    { bg: "#F5EEDF", color: PALETTE.estimate },
  danger:  { bg: "#F3E7E3", color: PALETTE.compromise },
};
```

В остальных файлах: сырые `zinc-*` и `#1c1d20` → `text-ink` / `text-ink-muted` / `text-ink-faint`, фон `bg-paper` или `bg-white`, границы `border-black/[0.08]`. Скругления к `rounded` (4 px) и `rounded-lg` (8 px); `rounded-full` остаётся только у переключателей слоёв карты. Публичные пропсы не трогать.

- [ ] **Step 3: Прогнать тесты примитивов**

Run: `cd frontend && npx vitest run components/ui/ui.test.tsx`
Expected: PASS — пропсы не менялись, тесты проверяют поведение и роли

- [ ] **Step 4: Прогнать весь фронт**

Run: `cd frontend && npm test && npx tsc --noEmit`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add frontend/components/ui
git commit -m "refactor: примитивы ui на токены вместо сырых zinc"
```

---

### Task 4: Первый экран

**Files:**
- Modify: `frontend/components/chat/EmptyState.tsx`
- Create: `frontend/components/chat/ScenarioCards.tsx`
- Create: `frontend/components/chat/ScenarioCards.test.tsx`
- Modify: `frontend/components/chat/ChatScreen.tsx` (прокинуть подстановку в композер)

**Interfaces:**
- Produces: `<ScenarioCards onPick={(text: string) => void} />` — три карточки сценариев.

- [ ] **Step 1: Написать падающий тест**

```tsx
import { render, screen, fireEvent } from "@testing-library/react";
import ScenarioCards from "./ScenarioCards";

it("показывает три сценария и отдаёт текст по клику", () => {
  const onPick = vi.fn();
  render(<ScenarioCards onPick={onPick} />);
  const cards = screen.getAllByRole("button");
  expect(cards).toHaveLength(3);
  fireEvent.click(cards[0]);
  // подставляется целая формулировка, а не ключевое слово: человек должен
  // увидеть, какого уровня подробности от него ждут
  expect(onPick.mock.calls[0][0].length).toBeGreaterThan(40);
});
```

- [ ] **Step 2: Прогнать, убедиться что падает**

Run: `cd frontend && npx vitest run components/chat/ScenarioCards.test.tsx`
Expected: FAIL — модуль не найден

- [ ] **Step 3: Реализовать карточки**

```tsx
const SCENARIOS = [
  { title: "Ребёнок ходит в школу сам",
    text: "Двушка до 25 млн, ребёнку в школу пешком без крупных дорог, мне на работу в Сити на метро" },
  { title: "Два офиса на разных концах",
    text: "Ищем компромисс между офисом в Сколково и офисом в Сити, двушка до 50 млн" },
  { title: "Работаю из дома, нужна тишина",
    text: "Тихая квартира с парком рядом, работаю удалённо, супруга ездит к МГУ" },
];
```

Каждая карточка — `<button type="button">` с заголовком и приглушённой строкой текста, `onClick={() => onPick(s.text)}`.

- [ ] **Step 4: Прогнать тест**

Run: `cd frontend && npx vitest run components/chat/ScenarioCards.test.tsx`
Expected: PASS

- [ ] **Step 5: Переписать EmptyState**

```tsx
export default function EmptyState({ onPick }: { onPick: (text: string) => void }) {
  return (
    <div className="mx-auto w-full max-w-[720px]">
      <h1 className="font-display text-[34px] leading-[1.15] tracking-tight text-ink">
        Расскажите, как вы живёте — подберём, где жить
      </h1>
      <p className="mt-3 max-w-[52ch] text-ink-muted">
        Не фильтры, а сценарий: кто в семье, куда ездите каждый день,
        что важно и чем готовы поступиться.
      </p>
      <ScenarioCards onPick={onPick} />
    </div>
  );
}
```

Строку доказательности («столько-то объявлений, дата данных») в этой задаче НЕ добавляем: числа берутся из `final_result.data_freshness`, а на первом экране поиска ещё не было — выдумывать их нельзя. Возвращаемся к ней, когда появится источник.

- [ ] **Step 6: Прогон и коммит**

```bash
cd frontend && npm test && npx tsc --noEmit
git add frontend/components/chat/EmptyState.tsx frontend/components/chat/ScenarioCards.tsx frontend/components/chat/ScenarioCards.test.tsx frontend/components/chat/ChatScreen.tsx
git commit -m "feat: первый экран приглашает сценарием, а не пустой строкой"
```

---

### Task 5: Ожидание поиска вместо радара

**Files:**
- Delete: `frontend/components/chat/GeoThinkingCanvas.tsx`
- Create: `frontend/components/chat/SearchWaiting.tsx`
- Create: `frontend/components/chat/SearchWaiting.test.tsx`
- Modify: `frontend/components/chat/MessageThread.tsx:2,13`

**Interfaces:**
- Produces: `<SearchWaiting />` — читает `useSession` сам, как это делал `GeoThinkingCanvas` (пропсов нет: остальные компоненты ветки устроены так же). Внутри переиспользует существующий `<StageCaption />`, а не заводит вторую карту стадий.
- Produces: `isThinking(stage: Stage): boolean` переезжает из удаляемого файла дословно вместе с константой `THINKING: Stage[] = ["linguistic", "geo", "context", "relaxation", "streaming"]`; `MessageThread.tsx:2` импортирует его уже из `SearchWaiting`.
- **Ослабление условий НЕ дублируем:** `RelaxationCard` уже рисуется в `MessageThread` при `stage === "relaxation"`. `SearchWaiting` его не повторяет — язык карточка получает в Task 3 через примитивы.

**Решение по объёму:** бриф показывает ТО, ЧТО ЕСТЬ — исходную просьбу человека, стадию человеческим языком и ослабление условий, когда оно приходит. Разобранных критериев в структурном виде фронту сейчас никто не отдаёт (`final_result` несёт объекты и диагностику, но не критерии), а синтезировать их на фронте запрещено. Растущий бриф из понятых критериев требует нового поля в SSE — это отдельная задача, вне этого плана.

- [ ] **Step 1: Написать падающий тест**

```tsx
import { render, screen, act } from "@testing-library/react";
import SearchWaiting, { isThinking } from "./SearchWaiting";
import { useSession } from "@/lib/store/session";

it("во время ожидания показывает просьбу человека", () => {
  act(() => useSession.setState({ stage: "geo" }));
  render(<SearchWaiting request="двушка до 25 млн, ребёнку в школу пешком" />);
  expect(screen.getByText(/ребёнку в школу пешком/)).toBeInTheDocument();
});

it("не изображает работу машины: никакого geo-engine", () => {
  act(() => useSession.setState({ stage: "geo" }));
  render(<SearchWaiting />);
  expect(screen.queryByText(/geo-engine/i)).not.toBeInTheDocument();
});

it("isThinking переехал без изменения поведения", () => {
  expect(isThinking("geo")).toBe(true);
  expect(isThinking("done")).toBe(false);
});
```

- [ ] **Step 2: Прогнать, убедиться что падает**

Run: `cd frontend && npx vitest run components/chat/SearchWaiting.test.tsx`
Expected: FAIL — модуль не найден

- [ ] **Step 3: Реализовать**

```tsx
export default function SearchWaiting({ request }: { request?: string }) {
  return (
    <div className="mx-auto w-full max-w-[540px] rounded-lg border border-black/[0.08] bg-white p-6">
      {request && (
        <p className="font-display text-lg leading-snug text-ink">«{request}»</p>
      )}
      <div className="mt-5 border-t border-black/[0.06] pt-4">
        <StageCaption />
      </div>
      {/* Одна тонкая линия вместо процентов: честного процента у нас нет —
          сколько осталось до ответа, пайплайн не знает. */}
      <div aria-hidden className="mt-4 h-px w-full overflow-hidden bg-black/[0.06]">
        <div className="h-full w-1/3 animate-[waiting_1.8s_ease-in-out_infinite] bg-ink-faint" />
      </div>
    </div>
  );
}
```

Никакой карты, никакой полосы-луча по ней, никакой мигающей точки, никакого моноширинного `geo-engine`. `request` `MessageThread` берёт из последнего сообщения пользователя в `useSession`; если его нет — блок цитаты не рисуется.

- [ ] **Step 4: Прогнать тест**

Run: `cd frontend && npx vitest run components/chat/SearchWaiting.test.tsx`
Expected: PASS

- [ ] **Step 5: Переключить MessageThread и удалить радар**

```bash
cd frontend && rm components/chat/GeoThinkingCanvas.tsx components/chat/GeoThinkingCanvas.test.tsx 2>/dev/null
```
В `MessageThread.tsx` заменить импорт и использование на `SearchWaiting`.

- [ ] **Step 6: Прогон и коммит**

```bash
cd frontend && npm test && npx tsc --noEmit
git add -A frontend/components/chat
git commit -m "feat: ожидание поиска показывает просьбу и компромиссы вместо радара"
```

---

### Task 6: Согласование числительного в тегах (Go)

**Files:**
- Modify: `backend/internal/service/display_fields.go:68-85`
- Test: `backend/internal/service/display_fields_test.go`

**Interfaces:**
- Produces: `pluralRu(n int, one, few, many string) string` в пакете `service`.

- [ ] **Step 1: Написать падающий тест**

```go
func TestBuildTagsAgreesNumeral(t *testing.T) {
	got := BuildTags(map[string]any{"walk_min_school": 2.0, "walk_min_metro": 1.0,
		"walk_min_park": 5.0})
	want := []string{"2 минуты до школы", "1 минута до метро", "5 минут до парка"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildTags() = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Прогнать, убедиться что падает**

Run: `cd backend && go test ./internal/service/ -run TestBuildTagsAgreesNumeral -v`
Expected: FAIL — сейчас «2 минут до школы»

- [ ] **Step 3: Реализовать**

```go
// Стандартное правило русского языка: последние две цифры 11-14 → форма
// «многих», иначе по последней цифре. Зеркало pluralRu из frontend/lib/format.ts.
func pluralRu(n int, one, few, many string) string {
	mod10, mod100 := n%10, n%100
	switch {
	case mod100 >= 11 && mod100 <= 14:
		return many
	case mod10 == 1:
		return one
	case mod10 >= 2 && mod10 <= 4:
		return few
	}
	return many
}
```

Заменить три `Sprintf` на форму `fmt.Sprintf("%d %s до школы", n, pluralRu(n, "минута", "минуты", "минут"))`, где `n := int(v)`.

- [ ] **Step 4: Прогнать тесты**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add backend/internal/service/display_fields.go backend/internal/service/display_fields_test.go
git commit -m "fix: числительное в тегах карточки согласовано с существительным"
```

---

### Task 7: Карточка выдачи

**Files:**
- Modify: `frontend/components/result/PropertyCard.tsx`
- Modify: `frontend/components/result/PropertyCard.test.tsx`

**Interfaces:**
- Consumes: `<Provenance>` из Task 2, токены Task 1.

**Решение по объёму (из спеки, раздел 2.3):** берём ФОЛБЭК. Подбор фактов под запрос и строка «почему» требуют разобранного запроса, который живёт на бэкенде, — это отдельная задача в `display_fields.go`, вне этого плана. Здесь фронт показывает существующие теги: ограничивает их числом, набирает как документ и красит по происхождению. Строка «почему» и «честный минус» НЕ рисуются: пустая строка честнее выдуманной.

**Происхождение тега** определяется его смыслом, а не текстом: пешие минуты (`walk_min_*`) — замер по сети, плотность баров — замер, шум — модель. Так как теги приходят строками, маппинг делается по префиксу в одном месте — `frontend/lib/factProvenance.ts` — и покрывается тестом.

- [ ] **Step 1: Написать падающий тест**

```tsx
it("показывает не больше трёх фактов: карточка не свалка", () => {
  const property = { ...PROPERTIES[0],
    tags: ["2 минуты до школы", "1 минута до метро", "3 минуты до парка", "баров рядом: 7"] };
  render(<PropertyCard property={property} index={0} onOpen={() => {}} />);
  expect(screen.getAllByTestId("card-fact").length).toBeLessThanOrEqual(3);
});
```

- [ ] **Step 2: Прогнать, убедиться что падает**

Run: `cd frontend && npx vitest run components/result/PropertyCard.test.tsx`
Expected: FAIL — сейчас рисуются все теги

- [ ] **Step 3: Реализовать**

```tsx
// frontend/lib/factProvenance.ts
import type { ProvenanceKind } from "@/components/ui/Provenance";

/** Теги приходят строками, поэтому происхождение выводится по смыслу факта.
 *  Шум — модельная величина (слой дорог + барный прокси), минуты и плотность
 *  баров посчитаны по данным. Неизвестный тег метки не получает вовсе. */
export function factProvenance(tag: string): ProvenanceKind | null {
  if (/^\d+\s+минут/.test(tag)) return "measured";
  if (tag.startsWith("баров рядом")) return "measured";
  if (tag.includes("шум")) return "estimate";
  return null;
}
```

В `PropertyCard.tsx`: факты получают `data-testid="card-fact"`, показываются первые три (порядок задаёт бэкенд — он ближе к запросу), четвёртый и далее не рисуются. Каждый факт — строка с волосяной линейкой и меткой `<Provenance>` там, где `factProvenance` вернул значение; пилюли уходят: документ, а не чат. Адрес антиквой (`font-display`), цена крупно, мета-строка `text-ink-faint`.

- [ ] **Step 4: Прогнать тесты карточки и весь фронт**

Run: `cd frontend && npx vitest run components/result && npm test && npx tsc --noEmit`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add frontend/components/result
git commit -m "feat: карточка выдачи набрана как документ, а не как лента тегов"
```

---

### Task 8: Досье

**Files:**
- Modify: `frontend/components/passport/dossier/Chapter.tsx:94-110`
- Create: `frontend/lib/metricLabels.ts`
- Create: `frontend/lib/metricLabels.test.ts`
- Modify: `frontend/components/passport/dossier/*` (типографика заголовков и источников)

**Interfaces:**
- Produces: `metricLabel(key: string): string | null` — человеческая подпись известной метрики, `null` для неизвестной.

- [ ] **Step 1: Написать падающий тест**

```ts
import { metricLabel } from "./metricLabels";

it("известный ключ получает человеческую подпись", () => {
  expect(metricLabel("longest_leg_minutes")).toBe("Самая долгая поездка дня");
  expect(metricLabel("bars_500m")).toBe("Баров и алкомаркетов в 500 м");
});

it("неизвестный ключ не показывается вовсе", () => {
  // Печатать пользователю сырое имя переменной нельзя; лучше не показать
  // ничего, чем показать longest_leg_minutes.
  expect(metricLabel("some_new_metric")).toBeNull();
});
```

- [ ] **Step 2: Прогнать, убедиться что падает**

Run: `cd frontend && npx vitest run lib/metricLabels.test.ts`
Expected: FAIL — модуль не найден

- [ ] **Step 3: Реализовать карту подписей**

```ts
const LABELS: Record<string, string> = {
  longest_leg_minutes: "Самая долгая поездка дня",
  bars_500m: "Баров и алкомаркетов в 500 м",
};

export function metricLabel(key: string): string | null {
  return LABELS[key] ?? null;
}
```

- [ ] **Step 4: Прогнать тест**

Run: `cd frontend && npx vitest run lib/metricLabels.test.ts`
Expected: PASS

- [ ] **Step 5: Применить в Chapter.tsx**

```tsx
// Chapter.tsx: было Object.entries(block.metrics)
const metrics = Object.entries(block.metrics ?? {})
  .map(([k, v]) => [metricLabel(k), v] as const)
  .filter((pair): pair is readonly [string, unknown] => pair[0] !== null);
```

Дальше в разметке `<dt>` печатает подпись, а не ключ. Заголовки глав — `font-display`, полоса набора описания `max-w-[62ch]`. Источники блока получают метку рядом с названием:

```tsx
<span className="flex items-center gap-2">
  {source.label}
  <Provenance kind={provenanceOfSource(source.kind)} />
</span>
```

- [ ] **Step 6: Прогон и коммит**

```bash
cd frontend && npm test && npx tsc --noEmit
git add frontend/components/passport frontend/lib/metricLabels.ts frontend/lib/metricLabels.test.ts
git commit -m "feat: досье набрано как документ, происхождение видно у источников"
```

---

### Task 9: Сквозная проверка на живом стенде

**Files:** правки по результатам осмотра.

- [ ] **Step 1: Пересобрать фронт**

```bash
docker compose up -d --no-deps --build frontend && docker compose ps frontend
```

- [ ] **Step 2: Осмотреть четыре экрана в браузере**

Открыть `http://localhost:3000`, выполнить запрос «двушка до 25 млн, ребёнку в школу пешком, мне на работу в Москва-Сити на метро» и **посмотреть глазами**: первый экран, ожидание, карточки, досье. Проверить, что индиго не осталось нигде, а цвет происхождения читается.

- [ ] **Step 3: Убрать мусор дефолтного комплекта**

```bash
git rm frontend/public/next.svg frontend/public/vercel.svg frontend/public/file.svg \
       frontend/public/globe.svg frontend/public/window.svg
```
Удалять только те, на которые нет ссылок: проверить `grep -rn "next.svg\|vercel.svg\|file.svg\|globe.svg\|window.svg" frontend --include=*.tsx`.

- [ ] **Step 4: Финальный прогон и коммит**

```bash
cd frontend && npm test && npx tsc --noEmit
cd ../backend && go test ./...
git add -A && git commit -m "chore: убран мусор дефолтного комплекта Next.js"
```
