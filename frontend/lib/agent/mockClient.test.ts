import { createMockClient } from "./mockClient";
import type { AgentEvent } from "./types";

test("emits linguistic→geo→context→streaming→done in order and returns properties", async () => {
  const client = createMockClient({ speed: 0 }); // 0 = no artificial delay
  const events: AgentEvent[] = [];
  await new Promise<void>((resolve) => {
    client.run("тихий двор рядом со школой", {
      onEvent: (e) => events.push(e),
      onDone: (r) => { expect(r.properties.length).toBe(4); resolve(); },
    });
  });
  const agents = events.map((e) => `${e.agent}:${e.status}`);
  expect(agents[0]).toBe("linguistic:processing");
  expect(agents).toContain("geo:processing");
  expect(agents).toContain("context:processing");
  expect(agents.some((a) => a.startsWith("orchestrator:processing"))).toBe(true);
  expect(agents.at(-1)).toBe("orchestrator:done");
});

test("cancel stops further events", async () => {
  const client = createMockClient({ speed: 5 });
  const events: AgentEvent[] = [];
  const cancel = client.run("q", { onEvent: (e) => events.push(e), onDone: () => {} });
  cancel();
  const count = events.length;
  await new Promise((r) => setTimeout(r, 60));
  expect(events.length).toBe(count);
});
