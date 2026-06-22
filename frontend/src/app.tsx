import { useEffect, useMemo, useRef, useState } from "react";
import * as echarts from "echarts";
import { detectLocale, locales, messages, type Locale } from "./i18n";

type RangeValue = "day" | "week" | "month" | "year";
type DisplayMode = "tokens" | "cost";

type Cost = {
  pricing_enabled: boolean;
  pricing_status: "disabled" | "priced" | "partial" | "unpriced";
  currency: string;
  estimated_cost: number;
  priced_tokens: number;
  unpriced_tokens: number;
};

type SummaryResponse = {
  range: RangeValue;
  requests: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  cache_ratio: number;
  reasoning_ratio: number;
  latest_event_time: string;
  cost: Cost;
};

type TimeseriesPoint = {
  time: string;
  requests: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  cost: Cost;
};

type TimeseriesResponse = {
  range: RangeValue;
  bucket: "hour" | "day" | "month";
  points: TimeseriesPoint[];
};

type ModelItem = {
  model: string;
  requests: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  cost: Cost;
};

type ModelsResponse = {
  items: ModelItem[];
};

type ChainItem = {
  chain_root_response_id: string;
  response_count: number;
  started_at: string;
  ended_at: string;
  models: string[];
  transports: string[];
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  cost: Cost;
};

type ChainsResponse = {
  items: ChainItem[];
};

type EventItem = {
  ts: string;
  transport: string;
  host: string;
  path: string;
  response_id: string;
  previous_response_id: string;
  chain_root_response_id: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  cost: Cost;
};

type EventsResponse = {
  items: EventItem[];
  limit: number;
  offset: number;
};

type DashboardData = {
  summary: SummaryResponse;
  timeseries: TimeseriesResponse;
  models: ModelsResponse;
  chains: ChainsResponse;
  events: EventsResponse;
};

const ranges: RangeValue[] = ["day", "week", "month", "year"];

export function App() {
  const [range, setRange] = useState<RangeValue>("week");
  const [displayMode, setDisplayMode] = useState<DisplayMode>("tokens");
  const [locale, setLocale] = useState<Locale>(() => detectLocale());
  const [languageMenuOpen, setLanguageMenuOpen] = useState(false);
  const [selectedChain, setSelectedChain] = useState<string>("");
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>("");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const t = messages[locale];
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  useEffect(() => {
    window.localStorage.setItem("oai-meter-locale", locale);
    document.documentElement.lang = locale;
  }, [locale]);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        if (!data) {
          setLoading(true);
        }
        const bucket = range === "day" ? "hour" : range === "year" ? "month" : "day";
        const tzQuery = `tz=${encodeURIComponent(timeZone)}`;
        const chainQuery = selectedChain ? `&chain_root_response_id=${encodeURIComponent(selectedChain)}` : "";
        const [summary, timeseries, models, chains, events] = await Promise.all([
          requestJSON<SummaryResponse>(`/api/summary?range=${range}&${tzQuery}`),
          requestJSON<TimeseriesResponse>(`/api/timeseries?range=${range}&bucket=${bucket}&${tzQuery}`),
          requestJSON<ModelsResponse>(`/api/models?range=${range}&${tzQuery}`),
          requestJSON<ChainsResponse>(`/api/chains?range=${range}&limit=12&${tzQuery}`),
          requestJSON<EventsResponse>(`/api/events?range=${range}&limit=25&${tzQuery}${chainQuery}`),
        ]);
        if (cancelled) {
          return;
        }
        setData({ summary, timeseries, models, chains, events });
        setUpdatedAt(new Date());
        setError("");
      } catch (err) {
        if (cancelled) {
          return;
        }
        setError(err instanceof Error ? err.message : t.failedToLoad);
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }

    void load();
    const timer = window.setInterval(load, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [range, selectedChain, t.failedToLoad, timeZone]);

  const kpis = useMemo(() => {
    if (!data) {
      return [];
    }
    if (displayMode === "cost") {
      return [
        { label: t.kpi.estimatedCost, value: formatCost(data.summary.cost, locale, t), detail: describeCost(data.summary.cost, locale, t) },
        { label: t.kpi.pricedTokens, value: formatInt(data.summary.cost.priced_tokens, locale), detail: t.kpi.coveredByPrices },
        { label: t.kpi.unpricedTokens, value: formatInt(data.summary.cost.unpriced_tokens, locale), detail: t.kpi.missingModelRates },
        { label: t.kpi.requests, value: formatInt(data.summary.requests, locale), detail: t.kpi.recordsInRange },
        { label: t.kpi.inputTokens, value: formatInt(data.summary.input_tokens, locale), detail: t.kpi.costBasis },
        { label: t.kpi.outputTokens, value: formatInt(data.summary.output_tokens, locale), detail: t.kpi.costBasis },
        { label: t.kpi.cacheRatio, value: formatPercent(data.summary.cache_ratio), detail: t.kpi.cachedInputShare },
        { label: t.kpi.reasoningRatio, value: formatPercent(data.summary.reasoning_ratio), detail: t.kpi.reasoningTokenShare },
      ];
    }
    return [
      { label: t.kpi.requests, value: formatInt(data.summary.requests, locale), detail: t.kpi.recordsInRange },
      { label: t.kpi.totalTokens, value: formatInt(data.summary.total_tokens, locale), detail: describeCost(data.summary.cost, locale, t) },
      { label: t.kpi.input, value: formatInt(data.summary.input_tokens, locale), detail: t.kpi.promptSideUsage },
      { label: t.kpi.output, value: formatInt(data.summary.output_tokens, locale), detail: t.kpi.completionSideUsage },
      { label: t.kpi.reasoning, value: formatInt(data.summary.reasoning_tokens, locale), detail: t.kpi.reportedReasoning },
      { label: t.kpi.cached, value: formatInt(data.summary.cached_tokens, locale), detail: t.kpi.cachedInput },
      { label: t.kpi.cacheRatio, value: formatPercent(data.summary.cache_ratio), detail: t.kpi.cachedOverInput },
      { label: t.kpi.reasoningRatio, value: formatPercent(data.summary.reasoning_ratio), detail: t.kpi.reasoningOverTotal },
    ];
  }, [data, displayMode, locale, t]);

  return (
    <main className="page">
      <header className="hero">
        <div>
          <p className="eyebrow">{t.brand}</p>
          <h1>{t.title}</h1>
          <p className="subtitle">{t.subtitle}</p>
        </div>
        <div className="heroMeta">
          <div className="heroMetaTop">
            <div>
              <p>{t.lastUpdated}</p>
              <strong>{updatedAt ? updatedAt.toLocaleTimeString(locale) : t.waitingForFirstPoll}</strong>
            </div>
            <div className="languagePicker">
              <button
                aria-expanded={languageMenuOpen}
                aria-haspopup="menu"
                className="languageButton"
                onClick={() => setLanguageMenuOpen((open) => !open)}
                type="button"
              >
                {t.language}
              </button>
              {languageMenuOpen ? (
                <div className="languageMenu" role="menu">
                  {locales.map((item) => (
                    <button
                      className={item.value === locale ? "active" : ""}
                      key={item.value}
                      onClick={() => {
                        setLocale(item.value);
                        setLanguageMenuOpen(false);
                      }}
                      role="menuitem"
                      type="button"
                    >
                      {item.label}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          </div>
          <div className="segmented">
            {ranges.map((value) => (
              <button
                key={value}
                className={value === range ? "active" : ""}
                onClick={() => setRange(value)}
                type="button"
              >
                {t.range[value]}
              </button>
            ))}
          </div>
          <div className="segmented compact">
            {(["tokens", "cost"] as DisplayMode[]).map((value) => (
              <button
                key={value}
                className={value === displayMode ? "active" : ""}
                onClick={() => setDisplayMode(value)}
                type="button"
              >
                {t.mode[value]}
              </button>
            ))}
          </div>
        </div>
      </header>

      {error ? <section className="panel error">{error}</section> : null}
      {loading && !data ? <section className="panel muted">{t.loadingDashboard}</section> : null}

      {data ? (
        <>
          <section className="kpiGrid">
            {kpis.map((item) => (
              <article className="kpiCard" key={item.label}>
                <p>{item.label}</p>
                <strong>{item.value}</strong>
                <span>{item.detail}</span>
              </article>
            ))}
          </section>

          <section className="chartGrid">
            <ChartPanel
              title={displayMode === "cost" ? t.charts.costTrend : t.charts.tokenTrend}
              subtitle={t.charts.bucketsOverRange(t.range[data.timeseries.bucket], t.range[range])}
              option={{
                animation: false,
                tooltip: {
                  trigger: "axis",
                  formatter: (params: unknown) => formatChartTooltip(params, displayMode, data.summary.cost.currency, locale),
                },
                legend: { textStyle: { color: "#52616f" } },
                grid: { top: 34, right: 18, bottom: 28, left: 42 },
                xAxis: {
                  type: "category",
                  data: data.timeseries.points.map((point) => formatBucketTime(point.time, data.timeseries.bucket, locale)),
                  axisLabel: { color: "#6a7885", hideOverlap: true },
                },
                yAxis: {
                  type: "value",
                  axisLabel: {
                    color: "#6a7885",
                    formatter: (value: number) =>
                      displayMode === "cost" ? formatCompactCost(value, data.summary.cost.currency, locale) : formatCompactNumber(value, locale),
                  },
                  splitLine: { lineStyle: { color: "rgba(102, 115, 128, 0.15)" } },
                },
                series:
                  displayMode === "cost"
                    ? [
                        {
                          name: t.charts.estimatedCost,
                          type: "line",
                          smooth: true,
                          areaStyle: { color: "rgba(30, 77, 118, 0.18)" },
                          lineStyle: { color: "#1e4d76", width: 3 },
                          itemStyle: { color: "#1e4d76" },
                          data: data.timeseries.points.map((point) => point.cost.estimated_cost),
                        },
                      ]
                    : [
                        {
                          name: t.charts.total,
                          type: "line",
                          smooth: true,
                          areaStyle: { color: "rgba(30, 77, 118, 0.18)" },
                          lineStyle: { color: "#1e4d76", width: 3 },
                          itemStyle: { color: "#1e4d76" },
                          data: data.timeseries.points.map((point) => point.total_tokens),
                        },
                        {
                          name: t.charts.input,
                          type: "line",
                          smooth: true,
                          lineStyle: { color: "#8b3151", width: 2 },
                          itemStyle: { color: "#8b3151" },
                          data: data.timeseries.points.map((point) => point.input_tokens),
                        },
                        {
                          name: t.charts.output,
                          type: "line",
                          smooth: true,
                          lineStyle: { color: "#2d6a57", width: 2 },
                          itemStyle: { color: "#2d6a57" },
                          data: data.timeseries.points.map((point) => point.output_tokens),
                        },
                      ],
              }}
            />
            <ChartPanel
              title={t.charts.modelBreakdown}
              subtitle={displayMode === "cost" ? t.charts.topModelsByCost : t.charts.topModelsByTokens}
              option={{
                animation: false,
                tooltip: {
                  trigger: "axis",
                  axisPointer: { type: "shadow" },
                  formatter: (params: unknown) => formatChartTooltip(params, displayMode, data.summary.cost.currency, locale),
                },
                grid: { top: 16, right: 16, bottom: 24, left: 112 },
                xAxis: {
                  type: "value",
                  axisLabel: {
                    color: "#6a7885",
                    formatter: (value: number) =>
                      displayMode === "cost" ? formatCompactCost(value, data.summary.cost.currency, locale) : formatCompactNumber(value, locale),
                  },
                  splitLine: { lineStyle: { color: "rgba(102, 115, 128, 0.15)" } },
                },
                yAxis: {
                  type: "category",
                  inverse: true,
                  axisLabel: { color: "#52616f" },
                  data: data.models.items.slice(0, 8).map((item) => item.model),
                },
                series: [
                  {
                    type: "bar",
                    data: data.models.items
                      .slice(0, 8)
                      .map((item) => (displayMode === "cost" ? item.cost.estimated_cost : item.total_tokens)),
                    itemStyle: {
                      color: "#34495e",
                      borderRadius: [0, 6, 6, 0],
                    },
                  },
                ],
              }}
            />
          </section>

          <section className="panel composition">
            <div>
              <p className="panelEyebrow">{t.composition.eyebrow}</p>
              <h2>{t.composition.title}</h2>
            </div>
            <div className="compositionBars">
              <CompositionBar label={t.kpi.input} value={data.summary.input_tokens} total={data.summary.total_tokens} tone="blue" locale={locale} />
              <CompositionBar label={t.kpi.output} value={data.summary.output_tokens} total={data.summary.total_tokens} tone="claret" locale={locale} />
              <CompositionBar label={t.kpi.cached} value={data.summary.cached_tokens} total={data.summary.total_tokens} tone="green" locale={locale} />
              <CompositionBar label={t.kpi.reasoning} value={data.summary.reasoning_tokens} total={data.summary.total_tokens} tone="ink" locale={locale} />
            </div>
          </section>

          <section className="tableGrid">
            <div className="panel">
              <div className="panelHeader">
                <div>
                  <p className="panelEyebrow">{t.tables.conversationChains}</p>
                  <h2>{t.tables.chainRollups}</h2>
                </div>
                {selectedChain ? (
                  <button className="ghostButton" onClick={() => setSelectedChain("")} type="button">
                    {t.tables.clearEventFilter}
                  </button>
                ) : null}
              </div>
              <div className="tableWrap">
                <table>
                  <thead>
                    <tr>
                      <th>{t.tables.chain}</th>
                      <th>{t.tables.responses}</th>
                      <th>{t.tables.models}</th>
                      <th>{displayMode === "cost" ? t.tables.cost : t.tables.tokens}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.chains.items.map((item) => (
                      <tr
                        className={item.chain_root_response_id === selectedChain ? "selected" : ""}
                        key={item.chain_root_response_id}
                        onClick={() => setSelectedChain(item.chain_root_response_id)}
                      >
                        <td>
                          <strong>{truncate(item.chain_root_response_id)}</strong>
                          <span>
                            {formatTime(item.started_at, locale, t)} {t.tables.to} {formatTime(item.ended_at, locale, t)}
                          </span>
                        </td>
                        <td>{formatInt(item.response_count, locale)}</td>
                        <td>{item.models.join(", ") || t.tables.unknown}</td>
                        <td>
                          <strong>{displayMode === "cost" ? formatCost(item.cost, locale, t) : formatInt(item.total_tokens, locale)}</strong>
                          <span>
                            {displayMode === "cost"
                              ? describeCost(item.cost, locale, t)
                              : t.tables.inputOutput(formatInt(item.input_tokens, locale), formatInt(item.output_tokens, locale))}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {data.chains.items.length === 0 ? <p className="emptyLine">{t.tables.noChains}</p> : null}
              </div>
            </div>

            <div className="panel">
              <div className="panelHeader">
                <div>
                  <p className="panelEyebrow">{t.tables.events}</p>
                  <h2>{t.tables.newestRecords}</h2>
                </div>
                <p className="microcopy">{selectedChain ? t.tables.filteredTo(truncate(selectedChain)) : t.tables.allChains}</p>
              </div>
              <div className="tableWrap">
                <table>
                  <thead>
                    <tr>
                      <th>{t.tables.time}</th>
                      <th>{t.tables.model}</th>
                      <th>{t.tables.route}</th>
                      <th>{displayMode === "cost" ? t.tables.cost : t.tables.total}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.events.items.map((item) => (
                      <tr key={item.response_id}>
                        <td>
                          <strong>{formatTime(item.ts, locale, t)}</strong>
                          <span>{item.transport}</span>
                        </td>
                        <td>{item.model || t.tables.unknown}</td>
                        <td>
                          <strong>{item.host}</strong>
                          <span>{item.path}</span>
                        </td>
                        <td>
                          <strong>{displayMode === "cost" ? formatCost(item.cost, locale, t) : formatInt(item.total_tokens, locale)}</strong>
                          <span>
                            {displayMode === "cost"
                              ? describeCost(item.cost, locale, t)
                              : t.tables.inputOutput(formatInt(item.input_tokens, locale), formatInt(item.output_tokens, locale))}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {data.events.items.length === 0 ? <p className="emptyLine">{t.tables.noEvents}</p> : null}
              </div>
            </div>
          </section>
        </>
      ) : null}
    </main>
  );
}

function ChartPanel(props: { title: string; subtitle: string; option: echarts.EChartsCoreOption }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    if (!ref.current) {
      return;
    }
    const chart = echarts.init(ref.current, undefined, { renderer: "canvas" });
    chartRef.current = chart;
    const resize = () => chart.resize();
    window.addEventListener("resize", resize);
    return () => {
      window.removeEventListener("resize", resize);
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    chartRef.current?.setOption(props.option, {
      notMerge: true,
      lazyUpdate: true,
    });
  }, [props.option]);

  return (
    <section className="panel chartPanel">
      <div className="panelHeader">
        <div>
          <p className="panelEyebrow">{props.title}</p>
          <h2>{props.subtitle}</h2>
        </div>
      </div>
      <div className="chartCanvas" ref={ref} />
    </section>
  );
}

function CompositionBar(props: { label: string; value: number; total: number; tone: string; locale: Locale }) {
  const ratio = props.total > 0 ? props.value / props.total : 0;
  return (
    <div className="compositionRow">
      <div className="compositionLabel">
        <span>{props.label}</span>
        <strong>{formatInt(props.value, props.locale)}</strong>
      </div>
      <div className="compositionTrack">
        <div className={`compositionFill ${props.tone}`} style={{ width: `${Math.max(ratio * 100, 4)}%` }} />
      </div>
      <span className="compositionRatio">{formatPercent(ratio)}</span>
    </div>
  );
}

async function requestJSON<T>(url: string): Promise<T> {
  const response = await fetch(url);
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `Request failed: ${response.status}`);
  }
  return response.json() as Promise<T>;
}

function formatInt(value: number, locale: Locale) {
  return new Intl.NumberFormat(locale).format(value);
}

function formatCompactNumber(value: number, locale: Locale) {
  return new Intl.NumberFormat(locale, {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);
}

function formatCost(cost: Cost, locale: Locale, t: (typeof messages)[Locale]) {
  if (!cost.pricing_enabled) {
    return t.cost.pricingOff;
  }
  if (cost.pricing_status === "unpriced") {
    return t.cost.unpriced;
  }
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: cost.currency || "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(cost.estimated_cost);
}

function formatCompactCost(value: number, currency: string, locale: Locale) {
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: currency || "USD",
    notation: "compact",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value);
}

function formatChartTooltip(params: unknown, displayMode: DisplayMode, currency: string, locale: Locale) {
  const items = Array.isArray(params) ? params : [params];
  const rows = items
    .map((item) => {
      if (!isTooltipParam(item)) {
        return "";
      }
      const value = Array.isArray(item.value) ? Number(item.value[item.value.length - 1]) : Number(item.value);
      const formattedValue =
        displayMode === "cost" ? formatMoneyValue(value, currency, locale) : formatInt(Number.isFinite(value) ? value : 0, locale);
      return `${item.marker ?? ""}${item.seriesName || item.name}: ${formattedValue}`;
    })
    .filter(Boolean);
  const first = items.find(isTooltipParam);
  const title = first?.axisValueLabel || first?.axisValue || first?.name || "";
  return [title, ...rows].filter(Boolean).join("<br/>");
}

function isTooltipParam(value: unknown): value is {
  axisValue?: string | number;
  axisValueLabel?: string;
  marker?: string;
  name?: string;
  seriesName?: string;
  value?: number | string | Array<number | string>;
} {
  return typeof value === "object" && value !== null;
}

function formatMoneyValue(value: number, currency: string, locale: Locale) {
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: currency || "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(Number.isFinite(value) ? value : 0);
}

function describeCost(cost: Cost, locale: Locale, t: (typeof messages)[Locale]) {
  if (!cost.pricing_enabled) {
    return t.cost.pricesNotLoaded;
  }
  if (cost.pricing_status === "partial") {
    return t.cost.unpricedTokens(formatInt(cost.unpriced_tokens, locale));
  }
  if (cost.pricing_status === "unpriced") {
    return t.cost.modelPriceMissing;
  }
  return t.cost.pricedTokens(formatInt(cost.priced_tokens, locale));
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function formatTime(value: string, locale: Locale, t: (typeof messages)[Locale]) {
  if (!value) {
    return t.time.none;
  }
  return new Date(value).toLocaleString(locale);
}

function formatBucketTime(value: string, bucket: "hour" | "day" | "month", locale: Locale) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  if (bucket === "hour") {
    return new Intl.DateTimeFormat(locale, {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    }).format(date);
  }
  if (bucket === "month") {
    return new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "2-digit",
    }).format(date);
  }
  return new Intl.DateTimeFormat(locale, {
    month: "2-digit",
    day: "2-digit",
  }).format(date);
}

function truncate(value: string) {
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;
}
