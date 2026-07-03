import { useEffect, useMemo, useRef, useState } from "react";
import * as echarts from "echarts";
import { detectLocale, locales, messages, type Locale } from "./i18n";

type TabValue = "overview" | "history" | "limits";
type RangeValue = "day" | "week" | "month" | "year";
type DisplayMode = "tokens" | "cost";
type EventSortValue = "ts_desc" | "ts_asc" | "total_desc" | "input_desc" | "output_desc" | "cached_desc" | "reasoning_desc";

type Cost = {
  pricing_enabled: boolean;
  pricing_status: "disabled" | "priced" | "partial" | "unpriced";
  currency: string;
  estimated_cost: number;
  priced_tokens: number;
  unpriced_tokens: number;
};

type SummaryResponse = {
  range: string;
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
  range: string;
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

type EventItem = {
  ts: string;
  transport: string;
  host: string;
  path: string;
  response_id: string;
  previous_response_id: string;
  chain_root_response_id: string;
  prompt_cache_key: string;
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

type HeatmapDay = {
  date: string;
  requests: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  cost: Cost;
  in_range: boolean;
};

type HeatmapResponse = {
  range: string;
  days: HeatmapDay[];
};

type RateLimitPoint = {
  time: string;
  primary_used_percent: number;
  secondary_used_percent: number;
  events: number;
};

type RateLimitItem = {
  ts: string;
  transport: string;
  host: string;
  path: string;
  plan_type: string;
  allowed: boolean;
  limit_reached: boolean;
  primary_used_percent: number;
  primary_window_minutes: number;
  primary_reset_after_seconds: number;
  primary_reset_at: string;
  secondary_used_percent: number;
  secondary_window_minutes: number;
  secondary_reset_after_seconds: number;
  secondary_reset_at: string;
  raw_json: string;
};

type RateLimitsResponse = {
  range: string;
  bucket: "hour" | "day" | "month";
  items: RateLimitItem[];
  points: RateLimitPoint[];
  limit: number;
  offset: number;
};

type OverviewData = {
  summary: SummaryResponse;
  timeseries: TimeseriesResponse;
  models: ModelsResponse;
  heatmap: HeatmapResponse;
};

type HistoryData = {
  models: ModelsResponse;
  events: EventsResponse;
};

const ranges: RangeValue[] = ["day", "week", "month", "year"];
const tabs: TabValue[] = ["overview", "history", "limits"];
const eventSorts: EventSortValue[] = ["ts_desc", "ts_asc", "total_desc", "input_desc", "output_desc", "cached_desc", "reasoning_desc"];
const historyPageSize = 25;
const limitsPageSize = 25;

export function App() {
  const [activeTab, setActiveTab] = useState<TabValue>("overview");
  const [range, setRange] = useState<RangeValue>("week");
  const [displayMode, setDisplayMode] = useState<DisplayMode>("tokens");
  const [locale, setLocale] = useState<Locale>(() => detectLocale());
  const [languageMenuOpen, setLanguageMenuOpen] = useState(false);
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [overviewData, setOverviewData] = useState<OverviewData | null>(null);
  const [historyData, setHistoryData] = useState<HistoryData | null>(null);
  const [limitsData, setLimitsData] = useState<RateLimitsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const [historyModel, setHistoryModel] = useState("");
  const [historyTransport, setHistoryTransport] = useState("");
  const [historyPromptCacheKey, setHistoryPromptCacheKey] = useState("");
  const [historySort, setHistorySort] = useState<EventSortValue>("ts_desc");
  const [historyOffset, setHistoryOffset] = useState(0);
  const [limitsOffset, setLimitsOffset] = useState(0);
  const t = messages[locale];
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const dateError = useMemo(() => validateDateRange(fromDate, toDate, t), [fromDate, toDate, t]);
  const baseQuery = useMemo(() => {
    const query = new URLSearchParams({ range, tz: timeZone });
    if (fromDate && toDate && !dateError) {
      query.set("from", fromDate);
      query.set("to", toDate);
    }
    return query.toString();
  }, [dateError, fromDate, range, timeZone, toDate]);

  useEffect(() => {
    window.localStorage.setItem("oai-meter-locale", locale);
    document.documentElement.lang = locale;
  }, [locale]);

  useEffect(() => {
    setHistoryOffset(0);
  }, [range, fromDate, toDate, historyModel, historyTransport, historyPromptCacheKey, historySort]);

  useEffect(() => {
    setLimitsOffset(0);
  }, [range, fromDate, toDate]);

  useEffect(() => {
    if (dateError) {
      setError(dateError);
      setLoading(false);
      return;
    }
    let cancelled = false;

    async function load() {
      try {
        setLoading((current) => (hasTabData(activeTab, overviewData, historyData, limitsData) ? current : true));
        if (activeTab === "overview") {
          const bucket = range === "day" ? "hour" : range === "year" ? "month" : "day";
          const [summary, timeseries, models, heatmap] = await Promise.all([
            requestJSON<SummaryResponse>(`/api/summary?${baseQuery}`),
            requestJSON<TimeseriesResponse>(`/api/timeseries?bucket=${bucket}&${baseQuery}`),
            requestJSON<ModelsResponse>(`/api/models?${baseQuery}`),
            requestJSON<HeatmapResponse>(`/api/heatmap?${baseQuery}`),
          ]);
          if (cancelled) {
            return;
          }
          setOverviewData({ summary, timeseries, models, heatmap });
        } else if (activeTab === "history") {
          const historyQuery = new URLSearchParams(baseQuery);
          historyQuery.set("limit", String(historyPageSize));
          historyQuery.set("offset", String(historyOffset));
          historyQuery.set("sort", historySort);
          if (historyModel) {
            historyQuery.set("model", historyModel);
          }
          if (historyTransport) {
            historyQuery.set("transport", historyTransport);
          }
          if (historyPromptCacheKey.trim()) {
            historyQuery.set("prompt_cache_key", historyPromptCacheKey.trim());
          }
          const [models, events] = await Promise.all([
            requestJSON<ModelsResponse>(`/api/models?${baseQuery}`),
            requestJSON<EventsResponse>(`/api/events?${historyQuery.toString()}`),
          ]);
          if (cancelled) {
            return;
          }
          setHistoryData({ models, events });
        } else {
          const limitsQuery = new URLSearchParams(baseQuery);
          limitsQuery.set("limit", String(limitsPageSize));
          limitsQuery.set("offset", String(limitsOffset));
          const rateLimits = await requestJSON<RateLimitsResponse>(`/api/rate-limits?${limitsQuery.toString()}`);
          if (cancelled) {
            return;
          }
          setLimitsData(rateLimits);
        }
        setUpdatedAt(new Date());
        setError("");
      } catch (loadError) {
        if (cancelled) {
          return;
        }
        setError(loadError instanceof Error ? loadError.message : t.failedToLoad);
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
  }, [
    activeTab,
    baseQuery,
    dateError,
    historyModel,
    historyOffset,
    historyPromptCacheKey,
    historySort,
    historyTransport,
    limitsOffset,
    range,
    t.failedToLoad,
  ]);

  const historyModels = historyData?.models.items ?? overviewData?.models.items ?? [];

  return (
    <main className="shell">
      <aside className="sidebar">
        <div className="sidebarBrand">
          <p>{t.brand}</p>
          <h1>{t.title}</h1>
          <span>{t.subtitle}</span>
        </div>
        <nav className="sidebarTabs" aria-label={t.tabs.nav}>
          {tabs.map((tab) => (
            <button className={tab === activeTab ? "active" : ""} key={tab} onClick={() => setActiveTab(tab)} type="button">
              <strong>{t.tabs[tab]}</strong>
              <span>{t.tabsDescription[tab]}</span>
            </button>
          ))}
        </nav>
        <div className="sidebarMeta">
          <p>{t.lastUpdated}</p>
          <strong>{updatedAt ? updatedAt.toLocaleTimeString(locale) : t.waitingForFirstPoll}</strong>
        </div>
      </aside>

      <section className="content">
        <header className="topbar">
          <div>
            <p className="eyebrow">{t.tabs[activeTab]}</p>
            <h2>{t.headings[activeTab]}</h2>
          </div>
          <div className="topbarActions">
            <div className="languagePicker">
              <button
                aria-expanded={languageMenuOpen}
                aria-haspopup="menu"
                className="secondaryButton"
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
        </header>

        <section className="panel controlsPanel">
          <div className="segmented">
            {ranges.map((value) => (
              <button className={value === range ? "active" : ""} key={value} onClick={() => setRange(value)} type="button">
                {t.range[value]}
              </button>
            ))}
          </div>
          <div className="segmented compact">
            {(["tokens", "cost"] as DisplayMode[]).map((value) => (
              <button className={value === displayMode ? "active" : ""} key={value} onClick={() => setDisplayMode(value)} type="button">
                {t.mode[value]}
              </button>
            ))}
          </div>
          <div className="dateControls">
            <label>
              <span>{t.controls.from}</span>
              <input onChange={(event) => setFromDate(event.target.value)} type="date" value={fromDate} />
            </label>
            <label>
              <span>{t.controls.to}</span>
              <input onChange={(event) => setToDate(event.target.value)} type="date" value={toDate} />
            </label>
            <button
              className="secondaryButton"
              disabled={!fromDate && !toDate}
              onClick={() => {
                setFromDate("");
                setToDate("");
              }}
              type="button"
            >
              {t.controls.clearDates}
            </button>
          </div>
          <p className="microcopy">{dateError || t.controls.customRangeHint}</p>
        </section>

        {error ? <section className="panel error">{error}</section> : null}
        {loading && !hasTabData(activeTab, overviewData, historyData, limitsData) ? <section className="panel muted">{t.loadingDashboard}</section> : null}

        {activeTab === "overview" && overviewData ? <OverviewPanel data={overviewData} displayMode={displayMode} locale={locale} t={t} /> : null}
        {activeTab === "history" ? (
          <HistoryPanel
            data={historyData}
            displayMode={displayMode}
            eventSort={historySort}
            historyModel={historyModel}
            historyOffset={historyOffset}
            historyPromptCacheKey={historyPromptCacheKey}
            historyTransport={historyTransport}
            locale={locale}
            modelOptions={historyModels}
            onModelChange={setHistoryModel}
            onNextPage={() => setHistoryOffset((current) => current + historyPageSize)}
            onOffsetChange={setHistoryOffset}
            onPromptCacheKeyChange={setHistoryPromptCacheKey}
            onSortChange={setHistorySort}
            onTransportChange={setHistoryTransport}
            t={t}
          />
        ) : null}
        {activeTab === "limits" && limitsData ? <LimitsPanel data={limitsData} limitsOffset={limitsOffset} locale={locale} onOffsetChange={setLimitsOffset} t={t} /> : null}
      </section>
    </main>
  );
}

function OverviewPanel(props: { data: OverviewData; displayMode: DisplayMode; locale: Locale; t: (typeof messages)[Locale] }) {
  const { data, displayMode, locale, t } = props;
  const kpis = displayMode === "cost"
    ? [
        { label: t.kpi.estimatedCost, value: formatCost(data.summary.cost, locale, t), detail: describeCost(data.summary.cost, locale, t) },
        { label: t.kpi.requests, value: formatInt(data.summary.requests, locale), detail: t.kpi.recordsInRange },
        { label: t.kpi.pricedTokens, value: formatInt(data.summary.cost.priced_tokens, locale), detail: t.kpi.coveredByPrices },
        { label: t.kpi.unpricedTokens, value: formatInt(data.summary.cost.unpriced_tokens, locale), detail: t.kpi.missingModelRates },
        { label: t.kpi.cacheRatio, value: formatPercent(data.summary.cache_ratio), detail: t.kpi.cachedInputShare },
        { label: t.kpi.reasoningRatio, value: formatPercent(data.summary.reasoning_ratio), detail: t.kpi.reasoningTokenShare },
      ]
    : [
        { label: t.kpi.requests, value: formatInt(data.summary.requests, locale), detail: t.kpi.recordsInRange },
        { label: t.kpi.totalTokens, value: formatInt(data.summary.total_tokens, locale), detail: describeCost(data.summary.cost, locale, t) },
        { label: t.kpi.input, value: formatInt(data.summary.input_tokens, locale), detail: t.kpi.promptSideUsage },
        { label: t.kpi.output, value: formatInt(data.summary.output_tokens, locale), detail: t.kpi.completionSideUsage },
        { label: t.kpi.cached, value: formatInt(data.summary.cached_tokens, locale), detail: t.kpi.cachedInput },
        { label: t.kpi.reasoning, value: formatInt(data.summary.reasoning_tokens, locale), detail: t.kpi.reportedReasoning },
      ];

  return (
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

      <section className="overviewGrid">
        <ChartPanel
          title={displayMode === "cost" ? t.charts.costTrend : t.charts.tokenTrend}
          subtitle={t.charts.bucketsOverRange(t.range[data.timeseries.bucket], t.rangeLabel(data.summary.range))}
          option={{
            animation: false,
            tooltip: {
              trigger: "axis",
              formatter: (params: unknown) => formatChartTooltip(params, displayMode, data.summary.cost.currency, locale),
            },
            grid: { top: 34, right: 16, bottom: 32, left: 44 },
            xAxis: {
              type: "category",
              data: data.timeseries.points.map((point) => formatBucketTime(point.time, data.timeseries.bucket, locale)),
              axisLabel: { color: "#57606a", hideOverlap: true },
            },
            yAxis: {
              type: "value",
              min: 0,
              axisLabel: {
                color: "#57606a",
                formatter: (value: number) =>
                  displayMode === "cost" ? formatCompactCost(value, data.summary.cost.currency, locale) : formatCompactNumber(value, locale),
              },
              splitLine: { lineStyle: { color: "#d8dee4" } },
            },
            series:
              displayMode === "cost"
                ? [
                    {
                      name: t.charts.estimatedCost,
                      type: "line",
                      smooth: false,
                      showSymbol: false,
                      lineStyle: { color: "#1f6feb", width: 2 },
                      areaStyle: { color: "rgba(31, 111, 235, 0.10)" },
                      data: data.timeseries.points.map((point) => point.cost.estimated_cost),
                    },
                  ]
                : [
                    {
                      name: t.charts.total,
                      type: "line",
                      smooth: false,
                      showSymbol: false,
                      lineStyle: { color: "#1f6feb", width: 2 },
                      data: data.timeseries.points.map((point) => point.total_tokens),
                    },
                    {
                      name: t.charts.input,
                      type: "line",
                      smooth: false,
                      showSymbol: false,
                      lineStyle: { color: "#2da44e", width: 2 },
                      data: data.timeseries.points.map((point) => point.input_tokens),
                    },
                    {
                      name: t.charts.output,
                      type: "line",
                      smooth: false,
                      showSymbol: false,
                      lineStyle: { color: "#0969da", width: 2 },
                      data: data.timeseries.points.map((point) => point.output_tokens),
                    },
                  ],
          }}
        />

        <section className="panel sidePanel">
          <div className="panelHeader">
            <div>
              <p className="panelEyebrow">{t.models.eyebrow}</p>
              <h3>{t.models.title}</h3>
            </div>
          </div>
          <div className="miniList">
            {data.models.items.slice(0, 8).map((item) => (
              <div className="miniListRow" key={item.model}>
                <div>
                  <strong>{item.model}</strong>
                  <span>{formatInt(item.requests, locale)} {t.models.requests}</span>
                </div>
                <div className="miniListMetric">
                  <strong>{displayMode === "cost" ? formatCost(item.cost, locale, t) : formatInt(item.total_tokens, locale)}</strong>
                  <span>{t.tables.inputOutput(formatInt(item.input_tokens, locale), formatInt(item.output_tokens, locale))}</span>
                </div>
              </div>
            ))}
            {data.models.items.length === 0 ? <p className="emptyLine">{t.tables.noEvents}</p> : null}
          </div>
        </section>
      </section>

      <section className="panel heatmapPanel">
        <div className="panelHeader">
          <div>
            <p className="panelEyebrow">{t.heatmap.eyebrow}</p>
            <h3>{t.heatmap.title}</h3>
          </div>
          <p className="microcopy">{t.heatmap.subtitle}</p>
        </div>
        <HeatmapGrid days={data.heatmap.days} displayMode={displayMode} locale={locale} t={t} />
      </section>

      <section className="panel">
        <div className="panelHeader">
          <div>
            <p className="panelEyebrow">{t.composition.eyebrow}</p>
            <h3>{t.composition.title}</h3>
          </div>
        </div>
        <div className="compositionBars">
          <CompositionBar label={t.kpi.input} locale={locale} tone="blue" total={data.summary.total_tokens} value={data.summary.input_tokens} />
          <CompositionBar label={t.kpi.output} locale={locale} tone="cyan" total={data.summary.total_tokens} value={data.summary.output_tokens} />
          <CompositionBar label={t.kpi.cached} locale={locale} tone="green" total={data.summary.total_tokens} value={data.summary.cached_tokens} />
          <CompositionBar label={t.kpi.reasoning} locale={locale} tone="slate" total={data.summary.total_tokens} value={data.summary.reasoning_tokens} />
        </div>
      </section>
    </>
  );
}

function HistoryPanel(props: {
  data: HistoryData | null;
  displayMode: DisplayMode;
  eventSort: EventSortValue;
  historyModel: string;
  historyOffset: number;
  historyPromptCacheKey: string;
  historyTransport: string;
  locale: Locale;
  modelOptions: ModelItem[];
  onModelChange: (value: string) => void;
  onNextPage: () => void;
  onOffsetChange: (value: number) => void;
  onPromptCacheKeyChange: (value: string) => void;
  onSortChange: (value: EventSortValue) => void;
  onTransportChange: (value: string) => void;
  t: (typeof messages)[Locale];
}) {
  const { data, displayMode, eventSort, historyModel, historyOffset, historyPromptCacheKey, historyTransport, locale, modelOptions, onModelChange, onNextPage, onOffsetChange, onPromptCacheKeyChange, onSortChange, onTransportChange, t } = props;

  return (
    <>
      <section className="panel filterPanel">
        <div className="filters">
          <label>
            <span>{t.filters.model}</span>
            <select onChange={(event) => onModelChange(event.target.value)} value={historyModel}>
              <option value="">{t.filters.allModels}</option>
              {modelOptions.map((item) => (
                <option key={item.model} value={item.model}>
                  {item.model}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>{t.filters.transport}</span>
            <select onChange={(event) => onTransportChange(event.target.value)} value={historyTransport}>
              <option value="">{t.filters.allTransports}</option>
              <option value="https-json">https-json</option>
              <option value="websocket">websocket</option>
            </select>
          </label>
          <label>
            <span>{t.filters.promptCacheKey}</span>
            <input onChange={(event) => onPromptCacheKeyChange(event.target.value)} placeholder={t.filters.promptCachePlaceholder} type="text" value={historyPromptCacheKey} />
          </label>
          <label>
            <span>{t.filters.sort}</span>
            <select onChange={(event) => onSortChange(event.target.value as EventSortValue)} value={eventSort}>
              {eventSorts.map((sort) => (
                <option key={sort} value={sort}>
                  {t.sorts[sort]}
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>

      <section className="panel">
        <div className="panelHeader">
          <div>
            <p className="panelEyebrow">{t.tables.events}</p>
            <h3>{t.tables.historyTitle}</h3>
          </div>
          <p className="microcopy">{t.pagination.page(Math.floor(historyOffset / historyPageSize) + 1)}</p>
        </div>
        <div className="tableWrap">
          <table>
            <thead>
              <tr>
                <th>{t.tables.time}</th>
                <th>{t.tables.model}</th>
                <th>{t.filters.transport}</th>
                <th>{t.filters.promptCacheKey}</th>
                <th>{t.tables.route}</th>
                <th>{t.tables.chain}</th>
                <th>{displayMode === "cost" ? t.tables.cost : t.tables.total}</th>
              </tr>
            </thead>
            <tbody>
              {data?.events.items.map((item) => (
                <tr key={item.response_id}>
                  <td>
                    <strong>{formatTime(item.ts, locale, t)}</strong>
                    <span>{truncate(item.response_id)}</span>
                  </td>
                  <td>{item.model || t.tables.unknown}</td>
                  <td>{item.transport}</td>
                  <td>{item.prompt_cache_key || t.tables.none}</td>
                  <td>
                    <strong>{item.host}</strong>
                    <span>{item.path}</span>
                  </td>
                  <td>{truncate(item.chain_root_response_id)}</td>
                  <td>
                    <strong>{displayMode === "cost" ? formatCost(item.cost, locale, t) : formatInt(item.total_tokens, locale)}</strong>
                    <span>{t.tables.inputOutput(formatInt(item.input_tokens, locale), formatInt(item.output_tokens, locale))}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {data && data.events.items.length === 0 ? <p className="emptyLine">{t.tables.noEvents}</p> : null}
        </div>
        <div className="pagination">
          <button className="secondaryButton" disabled={historyOffset === 0} onClick={() => onOffsetChange(Math.max(0, historyOffset - historyPageSize))} type="button">
            {t.pagination.previous}
          </button>
          <button className="secondaryButton" disabled={!data || data.events.items.length < historyPageSize} onClick={onNextPage} type="button">
            {t.pagination.next}
          </button>
        </div>
      </section>
    </>
  );
}

function LimitsPanel(props: {
  data: RateLimitsResponse;
  limitsOffset: number;
  locale: Locale;
  onOffsetChange: (value: number) => void;
  t: (typeof messages)[Locale];
}) {
  const { data, limitsOffset, locale, onOffsetChange, t } = props;

  return (
    <>
      <ChartPanel
        title={t.limits.chartEyebrow}
        subtitle={t.limits.chartTitle}
        option={{
          animation: false,
          tooltip: {
            trigger: "axis",
          },
          legend: { top: 0, textStyle: { color: "#57606a" } },
          grid: { top: 40, right: 16, bottom: 32, left: 44 },
          xAxis: {
            type: "category",
            data: data.points.map((point) => formatBucketTime(point.time, data.bucket, locale)),
            axisLabel: { color: "#57606a", hideOverlap: true },
          },
          yAxis: {
            type: "value",
            min: 0,
            max: 100,
            axisLabel: {
              color: "#57606a",
              formatter: (value: number) => `${value}%`,
            },
            splitLine: { lineStyle: { color: "#d8dee4" } },
          },
          series: [
            {
              name: t.limits.primary,
              type: "line",
              smooth: false,
              showSymbol: false,
              lineStyle: { color: "#1f6feb", width: 2 },
              data: data.points.map((point) => point.primary_used_percent),
            },
            {
              name: t.limits.secondary,
              type: "line",
              smooth: false,
              showSymbol: false,
              lineStyle: { color: "#2da44e", width: 2 },
              data: data.points.map((point) => point.secondary_used_percent),
            },
          ],
        }}
      />

      <section className="panel">
        <div className="panelHeader">
          <div>
            <p className="panelEyebrow">{t.limits.tableEyebrow}</p>
            <h3>{t.limits.tableTitle}</h3>
          </div>
          <p className="microcopy">{t.pagination.page(Math.floor(limitsOffset / limitsPageSize) + 1)}</p>
        </div>
        <div className="tableWrap">
          <table>
            <thead>
              <tr>
                <th>{t.tables.time}</th>
                <th>{t.limits.plan}</th>
                <th>{t.filters.transport}</th>
                <th>{t.limits.allowed}</th>
                <th>{t.limits.primary}</th>
                <th>{t.limits.secondary}</th>
                <th>{t.limits.rawJson}</th>
              </tr>
            </thead>
            <tbody>
              {data.items.map((item, index) => (
                <tr key={`${item.ts}-${index}`}>
                  <td>
                    <strong>{formatTime(item.ts, locale, t)}</strong>
                    <span>{item.host}{item.path}</span>
                  </td>
                  <td>{item.plan_type || t.tables.unknown}</td>
                  <td>{item.transport}</td>
                  <td>
                    <strong>{item.allowed ? t.limits.allowedYes : t.limits.allowedNo}</strong>
                    <span>{item.limit_reached ? t.limits.limitReached : t.limits.notReached}</span>
                  </td>
                  <td>
                    <strong>{formatPercentFromWhole(item.primary_used_percent)}</strong>
                    <span>{t.limits.resetInfo(item.primary_window_minutes, item.primary_reset_after_seconds, formatTime(item.primary_reset_at, locale, t))}</span>
                  </td>
                  <td>
                    <strong>{formatPercentFromWhole(item.secondary_used_percent)}</strong>
                    <span>{t.limits.resetInfo(item.secondary_window_minutes, item.secondary_reset_after_seconds, formatTime(item.secondary_reset_at, locale, t))}</span>
                  </td>
                  <td>
                    <details>
                      <summary>{t.limits.expandJson}</summary>
                      <pre>{item.raw_json}</pre>
                    </details>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {data.items.length === 0 ? <p className="emptyLine">{t.limits.noEvents}</p> : null}
        </div>
        <div className="pagination">
          <button className="secondaryButton" disabled={limitsOffset === 0} onClick={() => onOffsetChange(Math.max(0, limitsOffset - limitsPageSize))} type="button">
            {t.pagination.previous}
          </button>
          <button className="secondaryButton" disabled={data.items.length < limitsPageSize} onClick={() => onOffsetChange(limitsOffset + limitsPageSize)} type="button">
            {t.pagination.next}
          </button>
        </div>
      </section>
    </>
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
          <h3>{props.subtitle}</h3>
        </div>
      </div>
      <div className="chartCanvas" ref={ref} />
    </section>
  );
}

function HeatmapGrid(props: { days: HeatmapDay[]; displayMode: DisplayMode; locale: Locale; t: (typeof messages)[Locale] }) {
  const weeks = chunkWeeks(props.days);
  const values = props.days.map((day) => getHeatValue(day, props.displayMode)).filter((value) => value > 0);
  const maxValue = values.length > 0 ? Math.max(...values) : 0;
  const monthLabels = weeks.map((week) => week.find((day) => day && day.date.endsWith("-01"))?.date ?? "");

  return (
    <div className="heatmapWrap">
      <div className="heatmapMonths">
        {monthLabels.map((label, index) => (
          <span key={`${label}-${index}`}>{label ? formatMonthLabel(label, props.locale) : ""}</span>
        ))}
      </div>
      <div className="heatmapBody">
        <div className="heatmapWeekdays">
          {["", props.t.heatmap.mon, "", props.t.heatmap.wed, "", props.t.heatmap.fri, ""].map((label, index) => (
            <span key={`${label}-${index}`}>{label}</span>
          ))}
        </div>
        <div className="heatmapGrid">
          {weeks.map((week, weekIndex) => (
            <div className="heatmapColumn" key={`week-${weekIndex}`}>
              {week.map((day, dayIndex) =>
                day ? (
                  <div
                    className={`heatmapCell ${day.in_range ? "inRange" : ""}`}
                    key={day.date}
                    style={{ backgroundColor: heatColor(getHeatValue(day, props.displayMode), maxValue) }}
                    title={props.t.heatmap.tooltip(
                      day.date,
                      formatInt(day.requests, props.locale),
                      formatInt(day.total_tokens, props.locale),
                      formatCost(day.cost, props.locale, props.t),
                    )}
                  />
                ) : (
                  <div className="heatmapCell empty" key={`empty-${weekIndex}-${dayIndex}`} />
                ),
              )}
            </div>
          ))}
        </div>
      </div>
      <div className="heatmapLegend">
        <span>{props.t.heatmap.less}</span>
        {[0, 0.25, 0.5, 0.75, 1].map((value) => (
          <span className="heatmapLegendCell" key={value} style={{ backgroundColor: heatColor(value, 1) }} />
        ))}
        <span>{props.t.heatmap.more}</span>
      </div>
    </div>
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
        <div className={`compositionFill ${props.tone}`} style={{ width: `${Math.max(ratio * 100, props.value > 0 ? 4 : 0)}%` }} />
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

function hasTabData(activeTab: TabValue, overviewData: OverviewData | null, historyData: HistoryData | null, limitsData: RateLimitsResponse | null) {
  if (activeTab === "overview") {
    return overviewData !== null;
  }
  if (activeTab === "history") {
    return historyData !== null;
  }
  return limitsData !== null;
}

function validateDateRange(fromDate: string, toDate: string, t: (typeof messages)[Locale]) {
  if ((fromDate && !toDate) || (!fromDate && toDate)) {
    return t.controls.datePairRequired;
  }
  if (fromDate && toDate && fromDate > toDate) {
    return t.controls.dateOrderInvalid;
  }
  return "";
}

function chunkWeeks(days: HeatmapDay[]) {
  const weeks: Array<Array<HeatmapDay | null>> = [];
  const alignedDays: Array<HeatmapDay | null> = [...days];
  const firstDay = days[0] ? new Date(`${days[0].date}T00:00:00`) : null;
  const leadingBlanks = firstDay && !Number.isNaN(firstDay.getTime()) ? firstDay.getDay() : 0;
  for (let index = 0; index < leadingBlanks; index++) {
    alignedDays.unshift(null);
  }
  for (let index = 0; index < alignedDays.length; index += 7) {
    const week = alignedDays.slice(index, index + 7);
    while (week.length < 7) {
      week.push(null);
    }
    weeks.push(week);
  }
  return weeks;
}

function getHeatValue(day: HeatmapDay, displayMode: DisplayMode) {
  return displayMode === "cost" ? day.cost.estimated_cost : day.total_tokens;
}

function heatColor(value: number, maxValue: number) {
  if (maxValue <= 0 || value <= 0) {
    return "#ebedf0";
  }
  const ratio = Math.min(1, value / maxValue);
  if (ratio < 0.25) {
    return "#9be9a8";
  }
  if (ratio < 0.5) {
    return "#40c463";
  }
  if (ratio < 0.75) {
    return "#30a14e";
  }
  return "#216e39";
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

function formatPercentFromWhole(value: number) {
  return `${value}%`;
}

function formatTime(value: string, locale: Locale, t: (typeof messages)[Locale]) {
  if (!value) {
    return t.time.none;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString(locale);
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
      month: "short",
    }).format(date);
  }
  return new Intl.DateTimeFormat(locale, {
    month: "2-digit",
    day: "2-digit",
  }).format(date);
}

function formatMonthLabel(value: string, locale: Locale) {
  const date = new Date(`${value}T00:00:00`);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat(locale, { month: "short" }).format(date);
}

function truncate(value: string) {
  return value.length > 18 ? `${value.slice(0, 8)}...${value.slice(-6)}` : value;
}
