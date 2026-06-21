import { useEffect, useMemo, useRef, useState } from "react";
import * as echarts from "echarts";

type RangeValue = "day" | "week" | "month" | "year";

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
};

type TimeseriesPoint = {
  time: string;
  requests: number;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
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
  const [selectedChain, setSelectedChain] = useState<string>("");
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>("");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        if (!data) {
          setLoading(true);
        }
        const bucket = range === "day" ? "hour" : range === "year" ? "month" : "day";
        const chainQuery = selectedChain ? `&chain_root_response_id=${encodeURIComponent(selectedChain)}` : "";
        const [summary, timeseries, models, chains, events] = await Promise.all([
          requestJSON<SummaryResponse>(`/api/summary?range=${range}`),
          requestJSON<TimeseriesResponse>(`/api/timeseries?range=${range}&bucket=${bucket}`),
          requestJSON<ModelsResponse>(`/api/models?range=${range}`),
          requestJSON<ChainsResponse>(`/api/chains?range=${range}&limit=12`),
          requestJSON<EventsResponse>(`/api/events?range=${range}&limit=25${chainQuery}`),
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
        setError(err instanceof Error ? err.message : "Failed to load dashboard data.");
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
  }, [range, selectedChain]);

  const kpis = useMemo(() => {
    if (!data) {
      return [];
    }
    return [
      { label: "Requests", value: formatInt(data.summary.requests) },
      { label: "Total Tokens", value: formatInt(data.summary.total_tokens) },
      { label: "Input", value: formatInt(data.summary.input_tokens) },
      { label: "Output", value: formatInt(data.summary.output_tokens) },
      { label: "Reasoning", value: formatInt(data.summary.reasoning_tokens) },
      { label: "Cached", value: formatInt(data.summary.cached_tokens) },
      { label: "Cache Ratio", value: formatPercent(data.summary.cache_ratio) },
      { label: "Reasoning Ratio", value: formatPercent(data.summary.reasoning_ratio) },
    ];
  }, [data]);

  return (
    <main className="page">
      <header className="hero">
        <div>
          <p className="eyebrow">OAI Response Meter</p>
          <h1>Local Usage Dashboard</h1>
          <p className="subtitle">
            Embedded analytics for response metadata captured via mitmproxy. Prompts and content never appear here.
          </p>
        </div>
        <div className="heroMeta">
          <p>Last updated</p>
          <strong>{updatedAt ? updatedAt.toLocaleTimeString() : "Waiting for first poll"}</strong>
          <div className="segmented">
            {ranges.map((value) => (
              <button
                key={value}
                className={value === range ? "active" : ""}
                onClick={() => setRange(value)}
                type="button"
              >
                {value}
              </button>
            ))}
          </div>
        </div>
      </header>

      {error ? <section className="panel error">{error}</section> : null}
      {loading && !data ? <section className="panel muted">Loading dashboard…</section> : null}

      {data ? (
        <>
          <section className="kpiGrid">
            {kpis.map((item) => (
              <article className="kpiCard" key={item.label}>
                <p>{item.label}</p>
                <strong>{item.value}</strong>
              </article>
            ))}
          </section>

          <section className="chartGrid">
            <ChartPanel
              title="Token Trend"
              subtitle={`${data.timeseries.bucket} buckets over the selected ${range}`}
              option={{
                animation: false,
                tooltip: { trigger: "axis" },
                legend: { textStyle: { color: "#52616f" } },
                grid: { top: 34, right: 18, bottom: 28, left: 42 },
                xAxis: {
                  type: "category",
                  data: data.timeseries.points.map((point) => point.time),
                  axisLabel: { color: "#6a7885", hideOverlap: true },
                },
                yAxis: {
                  type: "value",
                  axisLabel: { color: "#6a7885" },
                  splitLine: { lineStyle: { color: "rgba(102, 115, 128, 0.15)" } },
                },
                series: [
                  {
                    name: "Total",
                    type: "line",
                    smooth: true,
                    areaStyle: { color: "rgba(30, 77, 118, 0.18)" },
                    lineStyle: { color: "#1e4d76", width: 3 },
                    itemStyle: { color: "#1e4d76" },
                    data: data.timeseries.points.map((point) => point.total_tokens),
                  },
                  {
                    name: "Input",
                    type: "line",
                    smooth: true,
                    lineStyle: { color: "#8b3151", width: 2 },
                    itemStyle: { color: "#8b3151" },
                    data: data.timeseries.points.map((point) => point.input_tokens),
                  },
                  {
                    name: "Output",
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
              title="Model Breakdown"
              subtitle="Top models by total tokens"
              option={{
                animation: false,
                tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
                grid: { top: 16, right: 16, bottom: 24, left: 112 },
                xAxis: {
                  type: "value",
                  axisLabel: { color: "#6a7885" },
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
                    data: data.models.items.slice(0, 8).map((item) => item.total_tokens),
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
              <p className="panelEyebrow">Token Composition</p>
              <h2>Input, output, cached, and reasoning mix</h2>
            </div>
            <div className="compositionBars">
              <CompositionBar label="Input" value={data.summary.input_tokens} total={data.summary.total_tokens} tone="blue" />
              <CompositionBar label="Output" value={data.summary.output_tokens} total={data.summary.total_tokens} tone="claret" />
              <CompositionBar label="Cached" value={data.summary.cached_tokens} total={data.summary.total_tokens} tone="green" />
              <CompositionBar label="Reasoning" value={data.summary.reasoning_tokens} total={data.summary.total_tokens} tone="ink" />
            </div>
          </section>

          <section className="tableGrid">
            <div className="panel">
              <div className="panelHeader">
                <div>
                  <p className="panelEyebrow">Conversation Chains</p>
                  <h2>Chain-level rollups</h2>
                </div>
                {selectedChain ? (
                  <button className="ghostButton" onClick={() => setSelectedChain("")} type="button">
                    Clear event filter
                  </button>
                ) : null}
              </div>
              <div className="tableWrap">
                <table>
                  <thead>
                    <tr>
                      <th>Chain</th>
                      <th>Responses</th>
                      <th>Models</th>
                      <th>Tokens</th>
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
                          <span>{formatTime(item.started_at)} to {formatTime(item.ended_at)}</span>
                        </td>
                        <td>{formatInt(item.response_count)}</td>
                        <td>{item.models.join(", ") || "Unknown"}</td>
                        <td>{formatInt(item.total_tokens)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {data.chains.items.length === 0 ? <p className="emptyLine">No chains in the selected range.</p> : null}
              </div>
            </div>

            <div className="panel">
              <div className="panelHeader">
                <div>
                  <p className="panelEyebrow">Events</p>
                  <h2>Newest raw usage records</h2>
                </div>
                <p className="microcopy">{selectedChain ? `Filtered to ${truncate(selectedChain)}` : "All chains"}</p>
              </div>
              <div className="tableWrap">
                <table>
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Model</th>
                      <th>Route</th>
                      <th>Total</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.events.items.map((item) => (
                      <tr key={item.response_id}>
                        <td>
                          <strong>{formatTime(item.ts)}</strong>
                          <span>{item.transport}</span>
                        </td>
                        <td>{item.model || "Unknown"}</td>
                        <td>
                          <strong>{item.host}</strong>
                          <span>{item.path}</span>
                        </td>
                        <td>{formatInt(item.total_tokens)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {data.events.items.length === 0 ? <p className="emptyLine">No events for this filter.</p> : null}
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

function CompositionBar(props: { label: string; value: number; total: number; tone: string }) {
  const ratio = props.total > 0 ? props.value / props.total : 0;
  return (
    <div className="compositionRow">
      <div className="compositionLabel">
        <span>{props.label}</span>
        <strong>{formatInt(props.value)}</strong>
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

function formatInt(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function formatTime(value: string) {
  if (!value) {
    return "n/a";
  }
  return new Date(value).toLocaleString();
}

function truncate(value: string) {
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;
}
