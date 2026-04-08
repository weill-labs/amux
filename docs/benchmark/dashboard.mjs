const COLORS = ["#89b4fa", "#a6e3a1", "#f9e2af", "#f38ba8", "#cba6f7", "#94e2d5", "#fab387", "#74c7ec"];
const NO_DATA_MESSAGE = '<p class="error">No benchmark history published yet.</p>';

function formatDateLabel(timestamp, { locale, timeZone } = {}) {
  if (!Number.isFinite(timestamp)) {
    return "?";
  }
  const options = timeZone ? { timeZone } : undefined;
  return new Date(timestamp * 1000).toLocaleDateString(locale, options);
}

function shortBenchmarkName(name) {
  return name.split("/").slice(1).join("/") || name;
}

function suiteNameForBenchmark(name) {
  return name.split("/")[0].replace(/^Benchmark/, "");
}

function collapseExpandedFallback(doc, except = null) {
  for (const element of doc.querySelectorAll?.(".chart-container.is-expanded") || []) {
    if (element !== except) {
      element.classList.remove("is-expanded");
    }
  }

  if (!except || !except.classList?.contains("is-expanded")) {
    doc.body?.classList?.remove("chart-modal-open");
  }
}

export function buildSuiteCharts(data, options = {}) {
  const groupedSuites = {};

  for (const run of data) {
    const timestamp = Number(run.Date);
    for (const suite of run.Suites || []) {
      for (const benchmark of suite.Benchmarks || []) {
        const name = benchmark.Name;
        const suiteName = suiteNameForBenchmark(name);

        groupedSuites[suiteName] ||= {};
        groupedSuites[suiteName][name] ||= [];
        groupedSuites[suiteName][name].push({
          timestamp,
          nsOp: Number(benchmark.NsPerOp) || 0,
        });
      }
    }
  }

  const suiteCharts = {};
  for (const [suiteName, benchmarks] of Object.entries(groupedSuites)) {
    const timestamps = Array.from(new Set(
      Object.values(benchmarks)
        .flatMap((points) => points.map((point) => point.timestamp))
        .filter(Number.isFinite),
    )).sort((left, right) => left - right);

    const labels = timestamps.map((timestamp) => formatDateLabel(timestamp, options));
    const datasets = Object.entries(benchmarks).map(([name, points]) => {
      const valuesByTimestamp = new Map(
        points
          .filter((point) => Number.isFinite(point.timestamp))
          .map((point) => [point.timestamp, point.nsOp / 1e6]),
      );

      return {
        label: shortBenchmarkName(name),
        data: timestamps.map((timestamp) => valuesByTimestamp.get(timestamp) ?? null),
      };
    });

    suiteCharts[suiteName] = { timestamps, labels, datasets };
  }

  return suiteCharts;
}

export async function toggleChartFullscreen(container, env = {}) {
  const doc = env.document ?? globalThis.document;
  if (!doc) {
    return;
  }

  if (doc.fullscreenElement === container && typeof doc.exitFullscreen === "function") {
    await doc.exitFullscreen();
    return;
  }

  if (typeof container.requestFullscreen === "function") {
    collapseExpandedFallback(doc);
    try {
      await container.requestFullscreen();
      return;
    } catch (_err) {
      // Fall back to the in-page expanded view when the Fullscreen API is denied.
    }
  }

  const isExpanded = container.classList?.contains("is-expanded");
  collapseExpandedFallback(doc, isExpanded ? null : container);
  if (isExpanded) {
    container.classList?.remove("is-expanded");
    doc.body?.classList?.remove("chart-modal-open");
    return;
  }

  container.classList?.add("is-expanded");
  doc.body?.classList?.add("chart-modal-open");
}

function renderMeta(run) {
  const meta = document.getElementById("meta");
  if (!run || !run.head_sha) {
    meta.textContent = "No benchmark metadata yet.";
    return;
  }

  const status = run.benchmark_success ? "successful benchmark run" : "benchmark run failed";
  meta.textContent = `Latest run ${run.head_sha.slice(0, 7)} on ${run.runner_os}, ${status}.`;
}

async function fetchJSON(path) {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error(`failed to fetch ${path}`);
  }
  return res.json();
}

function syncFullscreenButton(container, button, doc) {
  const isExpanded = doc.fullscreenElement === container || container.classList.contains("is-expanded");
  button.textContent = isExpanded ? "Collapse" : "Expand";
  button.setAttribute("aria-pressed", isExpanded ? "true" : "false");
}

function createChartCard(suiteName) {
  const card = document.createElement("section");
  card.className = "chart-container";

  const header = document.createElement("div");
  header.className = "chart-header";

  const title = document.createElement("h2");
  title.textContent = suiteName;

  const button = document.createElement("button");
  button.type = "button";
  button.className = "fullscreen-toggle";
  button.title = `${suiteName} chart fullscreen`;
  button.setAttribute("aria-label", `Toggle fullscreen for the ${suiteName} benchmark chart`);
  button.setAttribute("aria-pressed", "false");
  button.textContent = "Expand";

  header.append(title, button);

  const canvasWrap = document.createElement("div");
  canvasWrap.className = "chart-canvas-wrap";

  const canvas = document.createElement("canvas");
  canvasWrap.appendChild(canvas);

  card.append(header, canvasWrap);
  return { button, canvas, card };
}

function renderChart(container, suiteName, chartData, colorOffset, ChartCtor) {
  const { button, canvas, card } = createChartCard(suiteName);
  container.appendChild(card);

  const datasets = chartData.datasets.map((dataset, index) => {
    const color = COLORS[(colorOffset + index) % COLORS.length];
    return {
      ...dataset,
      borderColor: color,
      backgroundColor: `${color}33`,
      tension: 0.3,
      pointRadius: 3,
      spanGaps: false,
    };
  });

  const chart = new ChartCtor(canvas, {
    type: "line",
    data: { labels: chartData.labels, datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: "index", intersect: false },
      plugins: {
        legend: { labels: { color: "#cdd6f4" } },
      },
      scales: {
        x: {
          ticks: { color: "#6c7086", maxRotation: 45, minRotation: 45 },
          grid: { color: "#45475a" },
        },
        y: {
          title: { display: true, text: "ms/op", color: "#6c7086" },
          ticks: { color: "#6c7086" },
          grid: { color: "#45475a" },
        },
      },
    },
  });

  const resizeChart = () => {
    syncFullscreenButton(card, button, document);
    globalThis.requestAnimationFrame?.(() => chart.resize()) ?? chart.resize();
  };

  button.addEventListener("click", async () => {
    await toggleChartFullscreen(card);
    resizeChart();
  });
  document.addEventListener("fullscreenchange", resizeChart);
}

function showNoData(container) {
  container.innerHTML = NO_DATA_MESSAGE;
}

export async function main({ Chart: ChartCtor } = {}) {
  const container = document.getElementById("charts");
  try {
    renderMeta(await fetchJSON("latest-run.json"));
  } catch (_err) {
    renderMeta(null);
  }

  let data;
  try {
    data = await fetchJSON("benchmarks.json");
  } catch (_err) {
    showNoData(container);
    return;
  }

  if (!Array.isArray(data) || data.length === 0 || !ChartCtor) {
    showNoData(container);
    return;
  }

  const suites = buildSuiteCharts(data);
  let colorOffset = 0;
  for (const [suiteName, chartData] of Object.entries(suites)) {
    renderChart(container, suiteName, chartData, colorOffset, ChartCtor);
    colorOffset += chartData.datasets.length;
  }
}
