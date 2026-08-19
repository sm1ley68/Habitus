-- Намерение реплики многоходового чата (Task 3 ML: TurnIntent —
-- new_search/refine/followup). Без значения по умолчанию: если ответ ML не
-- содержит intent, колонка остаётся NULL, а не молчаливым "new_search".
ALTER TABLE chat_searches ADD COLUMN intent TEXT;
