import "@testing-library/jest-dom/vitest";

// jsdom lacks matchMedia; default to "no reduced motion".
window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: false, media: query, onchange: null,
  addEventListener: () => {}, removeEventListener: () => {},
  addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

// jsdom lacks IntersectionObserver, which framer-motion's `whileInView` needs.
class IntersectionObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() { return []; }
  root = null;
  rootMargin = "";
  thresholds = [];
}
window.IntersectionObserver = window.IntersectionObserver || (IntersectionObserverStub as unknown as typeof IntersectionObserver);
globalThis.IntersectionObserver = globalThis.IntersectionObserver || (IntersectionObserverStub as unknown as typeof IntersectionObserver);
