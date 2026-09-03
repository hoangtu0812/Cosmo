'use client';

import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';

/**
 * A chart the model asked for, drawn from the spec its tool returned.
 *
 * Hand-drawn SVG rather than a charting library: six shapes over one axis is
 * a few hundred lines, where a library is a dependency, a bundle, a theme to
 * fight and an upgrade to own. Colours come from the theme's own tokens, so a
 * chart follows light and dark without being told.
 *
 * The spec is validated on the server, which is what lets this draw without
 * defending itself at every step: the series are the same length, a pie holds
 * no negatives, and the labels match the values.
 */

export type ChartSpec = {
  type: 'bar' | 'hbar' | 'line' | 'area' | 'pie' | 'donut';
  title?: string;
  labels: string[];
  series: {name?: string; values: number[]}[];
  is_stacked?: boolean;
};

/** Reads a tool result, and says nothing when it is not a chart. */
export function chartFromResult(detail: string | undefined): ChartSpec | null {
  if (!detail) return null;
  try {
    const parsed = JSON.parse(detail) as {chart?: ChartSpec};
    const chart = parsed.chart;
    if (!chart || !Array.isArray(chart.labels) || !Array.isArray(chart.series)) return null;
    return chart;
  } catch {
    return null;
  }
}

// Six is enough to tell series apart and few enough that none of them is a
// colour nobody would choose. They repeat beyond that, which is the honest
// outcome of asking for a seventh.
const SERIES_COLORS = [
  'var(--color-accent)',
  'var(--color-success)',
  'var(--color-warning)',
  'var(--color-info, var(--color-accent))',
  'var(--color-error)',
  'var(--color-text-secondary)',
];

const WIDTH = 640;
const HEIGHT = 280;
const PAD = {top: 16, right: 16, bottom: 34, left: 48};

function colorAt(index: number) {
  return SERIES_COLORS[index % SERIES_COLORS.length];
}

/** Trims a number to something readable on an axis. */
function tick(value: number): string {
  const size = Math.abs(value);
  if (size >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (size >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (size >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  if (Number.isInteger(value)) return String(value);
  return value.toFixed(2);
}

export function ChartView({chart}: {chart: ChartSpec}) {
  const isRound = chart.type === 'pie' || chart.type === 'donut';
  return (
    <VStack gap={2} width="100%">
      {chart.title ? <Text type="label">{chart.title}</Text> : null}
      {isRound ? <RoundChart chart={chart} /> : <AxisChart chart={chart} />}
      {chart.series.length > 1 || chart.series[0]?.name ? (
        <HStack gap={3} vAlign="center" wrap="wrap">
          {(isRound ? chart.labels : chart.series.map((item) => item.name ?? '')).map((name, index) => (
            <HStack gap={1} key={`${name}-${index}`} vAlign="center">
              <svg aria-hidden height={10} width={10}>
                <rect fill={colorAt(index)} height={10} rx={2} width={10} />
              </svg>
              <Text color="secondary" type="supporting">{name || `#${index + 1}`}</Text>
            </HStack>
          ))}
        </HStack>
      ) : null}
    </VStack>
  );
}

/** bar, hbar, line and area: everything measured against one numeric axis. */
function AxisChart({chart}: {chart: ChartSpec}) {
  const {labels, series} = chart;
  const stacked = Boolean(chart.is_stacked);
  const horizontal = chart.type === 'hbar';

  // A stacked chart is measured by its totals; every other kind by its tallest
  // single value. Zero is always on the axis, so a bar starts where the eye
  // expects it to.
  const totals = labels.map((_, index) => series.reduce((sum, item) => sum + (item.values[index] ?? 0), 0));
  const flat = series.flatMap((item) => item.values);
  const high = Math.max(0, ...(stacked ? totals : flat));
  const low = Math.min(0, ...(stacked ? totals : flat));
  const span = high - low || 1;

  const plotWidth = WIDTH - PAD.left - PAD.right;
  const plotHeight = HEIGHT - PAD.top - PAD.bottom;
  const zero = horizontal
    ? PAD.left + ((0 - low) / span) * plotWidth
    : PAD.top + ((high - 0) / span) * plotHeight;

  const step = (horizontal ? plotHeight : plotWidth) / labels.length;
  const gap = Math.min(10, step * 0.2);
  const bandWidth = step - gap;
  const groupWidth = stacked ? bandWidth : bandWidth / series.length;

  return (
    <svg
      aria-label={chart.title || 'chart'}
      className="w-full"
      role="img"
      viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
    >
      {/* Four gridlines and their values: enough to read a height off, few
          enough not to become the picture. */}
      {[0, 0.25, 0.5, 0.75, 1].map((fraction) => {
        const value = high - fraction * span;
        const y = PAD.top + fraction * plotHeight;
        const x = PAD.left + fraction * plotWidth;
        return horizontal ? (
          <g key={fraction}>
            <line stroke="var(--color-border)" strokeWidth={1} x1={x} x2={x} y1={PAD.top} y2={PAD.top + plotHeight} />
            <text fill="var(--color-text-secondary)" fontSize={10} textAnchor="middle" x={x} y={HEIGHT - 12}>
              {tick(low + fraction * span)}
            </text>
          </g>
        ) : (
          <g key={fraction}>
            <line stroke="var(--color-border)" strokeWidth={1} x1={PAD.left} x2={WIDTH - PAD.right} y1={y} y2={y} />
            <text dominantBaseline="middle" fill="var(--color-text-secondary)" fontSize={10} textAnchor="end" x={PAD.left - 6} y={y}>
              {tick(value)}
            </text>
          </g>
        );
      })}

      {chart.type === 'bar' || chart.type === 'hbar'
        ? series.map((item, seriesIndex) => (
          <g key={seriesIndex}>
            {item.values.map((value, index) => {
              const below = stacked
                ? series.slice(0, seriesIndex).reduce((sum, other) => sum + (other.values[index] ?? 0), 0)
                : 0;
              const start = PAD.left + index * step + gap / 2 + (stacked ? 0 : seriesIndex * groupWidth);
              if (horizontal) {
                const x = PAD.left + ((below + Math.min(0, value) - low) / span) * plotWidth;
                const length = (Math.abs(value) / span) * plotWidth;
                const y = PAD.top + index * step + gap / 2 + (stacked ? 0 : seriesIndex * groupWidth);
                return <rect fill={colorAt(seriesIndex)} height={Math.max(1, groupWidth)} key={index} rx={2} width={Math.max(1, length)} x={x} y={y} />;
              }
              const top = PAD.top + ((high - below - Math.max(value, 0)) / span) * plotHeight;
              const length = (Math.abs(value) / span) * plotHeight;
              return <rect fill={colorAt(seriesIndex)} height={Math.max(1, length)} key={index} rx={2} width={Math.max(1, groupWidth)} x={start} y={top} />;
            })}
          </g>
        ))
        : series.map((item, seriesIndex) => {
          const points = item.values.map((value, index) => {
            const x = PAD.left + (index + 0.5) * (plotWidth / labels.length);
            const below = stacked
              ? series.slice(0, seriesIndex).reduce((sum, other) => sum + (other.values[index] ?? 0), 0)
              : 0;
            const y = PAD.top + ((high - below - value) / span) * plotHeight;
            return `${x},${y}`;
          });
          const first = points[0]?.split(',')[0] ?? String(PAD.left);
          const last = points[points.length - 1]?.split(',')[0] ?? String(WIDTH - PAD.right);
          return (
            <g key={seriesIndex}>
              {chart.type === 'area' ? (
                <polygon
                  fill={colorAt(seriesIndex)}
                  fillOpacity={0.18}
                  points={`${first},${zero} ${points.join(' ')} ${last},${zero}`}
                />
              ) : null}
              <polyline
                fill="none"
                points={points.join(' ')}
                stroke={colorAt(seriesIndex)}
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
              />
              {item.values.map((value, index) => {
                const [x, y] = (points[index] ?? '0,0').split(',');
                return <circle cx={x} cy={y} fill={colorAt(seriesIndex)} key={index} r={2.5} />;
              })}
            </g>
          );
        })}

      {/* The labels, thinned so they never overlap: on a crowded axis every
          other one, or every third, is still an axis - overlapping text is
          not. */}
      {labels.map((label, index) => {
        const density = Math.ceil(labels.length / (horizontal ? 12 : 10));
        if (index % density !== 0) return null;
        return horizontal ? (
          <text
            dominantBaseline="middle"
            fill="var(--color-text-secondary)"
            fontSize={10}
            key={index}
            textAnchor="end"
            x={PAD.left - 6}
            y={PAD.top + (index + 0.5) * step}
          >
            {label.length > 12 ? `${label.slice(0, 11)}…` : label}
          </text>
        ) : (
          <text
            fill="var(--color-text-secondary)"
            fontSize={10}
            key={index}
            textAnchor="middle"
            x={PAD.left + (index + 0.5) * (plotWidth / labels.length)}
            y={HEIGHT - 12}
          >
            {label.length > 10 ? `${label.slice(0, 9)}…` : label}
          </text>
        );
      })}
    </svg>
  );
}

/** pie and donut: one series as shares of its own total. */
function RoundChart({chart}: {chart: ChartSpec}) {
  const values = chart.series[0]?.values ?? [];
  const total = values.reduce((sum, value) => sum + value, 0);
  const size = 220;
  const radius = size / 2 - 8;
  const inner = chart.type === 'donut' ? radius * 0.58 : 0;
  const centre = size / 2;

  // A single slice is a circle, and an arc that goes all the way round draws
  // nothing at all - so it is drawn as one.
  if (total <= 0) {
    return <Text color="secondary" type="supporting">—</Text>;
  }

  // Each slice starts where every slice before it ended. Written as a sum
  // rather than a running total, because a variable reassigned while the list
  // is being built is a variable React will not let escape a render.
  const drawn = values.filter((value) => value > 0).length;
  const slices = values.map((value, index) => {
    const before = values.slice(0, index).reduce((sum, item) => sum + item, 0);
    const from = -Math.PI / 2 + (before / total) * Math.PI * 2;
    return {index, value, from, to: from + (value / total) * Math.PI * 2, isWhole: drawn === 1};
  });

  return (
    <svg aria-label={chart.title || 'chart'} height={size} role="img" viewBox={`0 0 ${size} ${size}`} width={size}>
      {slices.map((slice) => {
        if (slice.value <= 0) return null;
        if (slice.isWhole) {
          return (
            <g key={slice.index}>
              <circle cx={centre} cy={centre} fill={colorAt(slice.index)} r={radius} />
              {inner > 0 ? <circle cx={centre} cy={centre} fill="var(--color-surface)" r={inner} /> : null}
            </g>
          );
        }
        const large = slice.to - slice.from > Math.PI ? 1 : 0;
        const x1 = centre + radius * Math.cos(slice.from);
        const y1 = centre + radius * Math.sin(slice.from);
        const x2 = centre + radius * Math.cos(slice.to);
        const y2 = centre + radius * Math.sin(slice.to);
        const inner1 = centre + inner * Math.cos(slice.to);
        const innerY1 = centre + inner * Math.sin(slice.to);
        const inner2 = centre + inner * Math.cos(slice.from);
        const innerY2 = centre + inner * Math.sin(slice.from);
        const path = inner > 0
          ? `M ${x1} ${y1} A ${radius} ${radius} 0 ${large} 1 ${x2} ${y2} L ${inner1} ${innerY1} A ${inner} ${inner} 0 ${large} 0 ${inner2} ${innerY2} Z`
          : `M ${centre} ${centre} L ${x1} ${y1} A ${radius} ${radius} 0 ${large} 1 ${x2} ${y2} Z`;
        return <path d={path} fill={colorAt(slice.index)} key={slice.index} />;
      })}
    </svg>
  );
}
