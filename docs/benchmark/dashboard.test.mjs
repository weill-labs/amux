import assert from "node:assert/strict";
import test from "node:test";

import { buildSuiteCharts, toggleChartFullscreen } from "./dashboard.mjs";

function makeClassList(initial = []) {
  const values = new Set(initial);
  return {
    add(name) {
      values.add(name);
    },
    remove(name) {
      values.delete(name);
    },
    contains(name) {
      return values.has(name);
    },
    toArray() {
      return [...values].sort();
    },
  };
}

test("buildSuiteCharts sorts each suite chronologically and aligns missing points", () => {
  const april2 = Date.UTC(2026, 3, 2) / 1000;
  const april3 = Date.UTC(2026, 3, 3) / 1000;
  const april4 = Date.UTC(2026, 3, 4) / 1000;
  const suites = buildSuiteCharts(
    [
      {
        Date: april4,
        Suites: [
          {
            Benchmarks: [
              { Name: "BenchmarkRender/small", NsPerOp: 2_000_000 },
              { Name: "BenchmarkRender/large", NsPerOp: 4_000_000 },
            ],
          },
        ],
      },
      {
        Date: april2,
        Suites: [
          {
            Benchmarks: [{ Name: "BenchmarkRender/small", NsPerOp: 1_000_000 }],
          },
        ],
      },
      {
        Date: april3,
        Suites: [
          {
            Benchmarks: [
              { Name: "BenchmarkRender/small", NsPerOp: 1_500_000 },
              { Name: "BenchmarkRender/large", NsPerOp: 3_000_000 },
            ],
          },
        ],
      },
    ],
    { locale: "en-US", timeZone: "UTC" },
  );

  assert.deepEqual(suites.Render.timestamps, [april2, april3, april4]);
  assert.deepEqual(suites.Render.labels, ["4/2/2026", "4/3/2026", "4/4/2026"]);
  assert.deepEqual(suites.Render.datasets, [
    { label: "small", data: [1, 1.5, 2] },
    { label: "large", data: [null, 3, 4] },
  ]);
});

test("toggleChartFullscreen uses the Fullscreen API when available", async () => {
  let requestCount = 0;
  let exitCount = 0;
  const doc = {
    fullscreenElement: null,
    async exitFullscreen() {
      exitCount++;
      this.fullscreenElement = null;
    },
  };
  const container = {
    classList: makeClassList(),
    async requestFullscreen() {
      requestCount++;
      doc.fullscreenElement = container;
    },
  };

  await toggleChartFullscreen(container, { document: doc });
  assert.equal(requestCount, 1);
  assert.equal(doc.fullscreenElement, container);

  await toggleChartFullscreen(container, { document: doc });
  assert.equal(exitCount, 1);
  assert.equal(doc.fullscreenElement, null);
});

test("toggleChartFullscreen falls back to a modal-style class toggle", async () => {
  const body = { classList: makeClassList() };
  const doc = { fullscreenElement: null, body };
  const container = { classList: makeClassList() };

  await toggleChartFullscreen(container, { document: doc });
  assert.deepEqual(container.classList.toArray(), ["is-expanded"]);
  assert.deepEqual(body.classList.toArray(), ["chart-modal-open"]);

  await toggleChartFullscreen(container, { document: doc });
  assert.deepEqual(container.classList.toArray(), []);
  assert.deepEqual(body.classList.toArray(), []);
});

test("toggleChartFullscreen falls back when requestFullscreen rejects", async () => {
  const body = { classList: makeClassList() };
  const doc = { fullscreenElement: null, body };
  const container = {
    classList: makeClassList(),
    async requestFullscreen() {
      throw new Error("fullscreen denied");
    },
  };

  await toggleChartFullscreen(container, { document: doc });
  assert.deepEqual(container.classList.toArray(), ["is-expanded"]);
  assert.deepEqual(body.classList.toArray(), ["chart-modal-open"]);
});
