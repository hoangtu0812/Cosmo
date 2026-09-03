'use client';

import {useState} from 'react';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';

/**
 * A chart the model asked for, drawn from the spec its tool returned.
 *
 * Hand-drawn SVG rather than a charting library: six shapes over one axis is a
 * few hundred lines, where a library is a dependency, a bundle, a theme to
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

/** A value as a reader would write it, rather than as a float. */
function formatValue(value: number): string {
  if (Number.isInteger(value)) return value.toLocaleString('vi-VN');
  return value.toLocaleString('vi-VN', {maximumFractionDigits: 2});
}

/**
 * What the pointer is resting on.
 *
 * A whole column rather than one mark: on a chart with three lines, the useful
 * answer to "what happened in 2025" is all three values, not whichever dot the
 * cursor happened to land on.
 */
type Reading = {
  label: string;
  entries: {name?: string; value: number; colorIndex: number; share?: number}[];
};

/** A series with its place in the colour order kept after filtering. */
type DrawnSeries = {name?: string; values: number[]; colorIndex: number};

export function ChartView({chart, isInteractive = false}: {chart: ChartSpec; isInteractive?: boolean}) {
  const isRound = chart.type === 'pie' || chart.type === 'donut';
  // Both are inert on the inline copy: a chart in the middle of an answer
  // should not change under the cursor while somebody reads past it.
  const [reading, setReading] = useState<Reading | null>(null);
  const [hidden, setHidden] = useState<number[]>([]);
  const shown = isInteractive ? reading : null;

  // A hidden series keeps its colour index, so switching one off does not
  // recolour the rest.
  const drawn: DrawnSeries[] = chart.series
    .map((item, index) => ({...item, colorIndex: index}))
    .filter((item) => !hidden.includes(item.colorIndex));

  const legend = isRound
    ? chart.labels.map((name, index) => ({name, index}))
    : chart.series.map((item, index) => ({name: item.name ?? '', index}));
  const hasLegend = chart.series.length > 1 || Boolean(chart.series[0]?.name) || isRound;
  // Only an axis chart can drop a series: a pie with a slice removed is a
  // different pie, not the same one with less on it.
  const canToggle = isInteractive && !isRound && legend.length > 1;

  return (
    <VStack gap={2} width="100%">
      {chart.title ? <Text type="label">{chart.title}</Text> : null}

      {isRound
        ? <RoundChart chart={chart} isInteractive={isInteractive} onRead={setReading} />
        : <AxisChart chart={chart} isInteractive={isInteractive} onRead={setReading} series={drawn} />}

      {/* What the pointer is on, said under the chart rather than in a box
          that follows the cursor: a tooltip you have to chase is a tooltip.
          The row is held open while interactive, so the chart does not jump as
          the pointer crosses it. */}
      {isInteractive ? (
        <HStack gap={3} vAlign="center" wrap="wrap">
          {shown ? (
            <>
              <Text type="label">{shown.label}</Text>
              {shown.entries.map((entry, index) => (
                <HStack gap={1} key={`${entry.name ?? ''}-${index}`} vAlign="center">
                  <svg aria-hidden height={10} width={10}>
                    <rect fill={colorAt(entry.colorIndex)} height={10} rx={2} width={10} />
                  </svg>
                  {entry.name ? <Text color="secondary" type="supporting">{entry.name}</Text> : null}
                  <Text type="body">{formatValue(entry.value)}</Text>
                  {entry.share !== undefined ? (
                    <Text color="secondary" type="supporting">{`${(entry.share * 100).toFixed(1)}%`}</Text>
                  ) : null}
                </HStack>
              ))}
            </>
          ) : (
            <Text color="secondary" type="supporting">&nbsp;</Text>
          )}
        </HStack>
      ) : null}

      {hasLegend ? (
        <HStack gap={3} vAlign="center" wrap="wrap">
          {legend.map((item) => {
            const isOff = hidden.includes(item.index);
            const label = item.name || `#${item.index + 1}`;
            return (
              <HStack
                className={canToggle ? 'cursor-pointer' : undefined}
                gap={1}
                key={`${label}-${item.index}`}
                onClick={canToggle
                  ? () => setHidden((current) => current.includes(item.index)
                    ? current.filter((index) => index !== item.index)
                    : [...current, item.index])
                  : undefined}
                vAlign="center"
              >
                <svg aria-hidden height={10} width={10}>
                  <rect fill={isOff ? 'var(--color-border)' : colorAt(item.index)} height={10} rx={2} width={10} />
                </svg>
                <Text color="secondary" type="supporting">{label}</Text>
              </HStack>
            );
          })}
        </HStack>
      ) : null}
    </VStack>
  );
}

/** bar, hbar, line and area: everything measured against one numeric axis. */
function AxisChart({chart, series, isInteractive, onRead}: {
  chart: ChartSpec;
  series: DrawnSeries[];
  isInteractive: boolean;
  onRead: (reading: Reading | null) => void;
}) {
  const {labels} = chart;
  const stacked = Boolean(chart.is_stacked);
  const horizontal = chart.type === 'hbar';
  const isBar = chart.type === 'bar' || chart.type === 'hbar';

  // A stacked chart is measured by its totals; every other kind by its tallest
  // single value - and only across the series still shown, so hiding the tall
  // one rescales the rest rather than leaving them in its shadow. Zero is
  // always on the axis, so a bar starts where the eye expects it to.
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

  const step = (horizontal ? plotHeight : plotWidth) / Math.max(1, labels.length);
  const gap = Math.min(10, step * 0.2);
  const bandWidth = step - gap;
  const groupWidth = stacked || series.length === 0 ? bandWidth : bandWidth / series.length;

  // Everything visible at one label, which is what a reader is asking about
  // when they point at a column.
  const readingAt = (index: number): Reading => ({
    label: labels[index] ?? '',
    entries: series.map((item) => ({name: item.name, value: item.values[index] ?? 0, colorIndex: item.colorIndex})),
  });

  return (
    <svg
      aria-label={chart.title || 'chart'}
      className="w-full"
      onMouseLeave={isInteractive ? () => onRead(null) : undefined}
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

      {isBar
        ? series.map((item, position) => (
          <g key={item.colorIndex}>
            {item.values.map((value, index) => {
              const below = stacked
                ? series.slice(0, position).reduce((sum, other) => sum + (other.values[index] ?? 0), 0)
                : 0;
              const offset = stacked ? 0 : position * groupWidth;
              if (horizontal) {
                const x = PAD.left + ((below + Math.min(0, value) - low) / span) * plotWidth;
                const length = (Math.abs(value) / span) * plotWidth;
                const y = PAD.top + index * step + gap / 2 + offset;
                return <rect fill={colorAt(item.colorIndex)} height={Math.max(1, groupWidth)} key={index} rx={2} width={Math.max(1, length)} x={x} y={y} />;
              }
              const top = PAD.top + ((high - below - Math.max(value, 0)) / span) * plotHeight;
              const length = (Math.abs(value) / span) * plotHeight;
              const x = PAD.left + index * step + gap / 2 + offset;
              return <rect fill={colorAt(item.colorIndex)} height={Math.max(1, length)} key={index} rx={2} width={Math.max(1, groupWidth)} x={x} y={top} />;
            })}
          </g>
        ))
        : series.map((item, position) => {
          const points = item.values.map((value, index) => {
            const x = PAD.left + (index + 0.5) * (plotWidth / Math.max(1, labels.length));
            const below = stacked
              ? series.slice(0, position).reduce((sum, other) => sum + (other.values[index] ?? 0), 0)
              : 0;
            const y = PAD.top + ((high - below - value) / span) * plotHeight;
            return `${x},${y}`;
          });
          const first = points[0]?.split(',')[0] ?? String(PAD.left);
          const last = points[points.length - 1]?.split(',')[0] ?? String(WIDTH - PAD.right);
          return (
            <g key={item.colorIndex}>
              {chart.type === 'area' ? (
                <polygon
                  fill={colorAt(item.colorIndex)}
                  fillOpacity={0.18}
                  points={`${first},${zero} ${points.join(' ')} ${last},${zero}`}
                />
              ) : null}
              <polyline
                fill="none"
                points={points.join(' ')}
                stroke={colorAt(item.colorIndex)}
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
              />
              {item.values.map((_, index) => {
                const [x, y] = (points[index] ?? '0,0').split(',');
                return <circle cx={x} cy={y} fill={colorAt(item.colorIndex)} key={index} r={3} />;
              })}
            </g>
          );
        })}

      {/* One invisible band per label, the full height of the plot. Hovering a
          six-pixel dot is a game; hovering a column is not, and the band
          answers for every series at once. Drawn last so it sits above the
          marks and nothing else steals the pointer. */}
      {isInteractive ? labels.map((_, index) => (
        <rect
          fill="transparent"
          height={horizontal ? step : plotHeight}
          key={`band-${index}`}
          onMouseEnter={() => onRead(readingAt(index))}
          width={horizontal ? plotWidth : step}
          x={horizontal ? PAD.left : PAD.left + index * step}
          y={horizontal ? PAD.top + index * step : PAD.top}
        />
      )) : null}

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
            pointerEvents="none"
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
            pointerEvents="none"
            textAnchor="middle"
            x={PAD.left + (index + 0.5) * (plotWidth / Math.max(1, labels.length))}
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
function RoundChart({chart, isInteractive, onRead}: {
  chart: ChartSpec;
  isInteractive: boolean;
  onRead: (reading: Reading | null) => void;
}) {
  const values = chart.series[0]?.values ?? [];
  const total = values.reduce((sum, value) => sum + value, 0);
  const size = 260;
  const radius = size / 2 - 8;
  const inner = chart.type === 'donut' ? radius * 0.58 : 0;
  const centre = size / 2;

  if (total <= 0) {
    return <Text color="secondary" type="supporting">—</Text>;
  }

  // Each slice starts where every slice before it ended, written as a sum
  // rather than a running total: a variable reassigned while the list is being
  // built is a variable React will not let escape a render.
  const drawnCount = values.filter((value) => value > 0).length;
  const slices = values.map((value, index) => {
    const before = values.slice(0, index).reduce((sum, item) => sum + item, 0);
    const from = -Math.PI / 2 + (before / total) * Math.PI * 2;
    return {index, value, from, to: from + (value / total) * Math.PI * 2, isWhole: drawnCount === 1};
  });

  return (
    <svg
      aria-label={chart.title || 'chart'}
      className="max-w-full"
      height={size}
      onMouseLeave={isInteractive ? () => onRead(null) : undefined}
      role="img"
      viewBox={`0 0 ${size} ${size}`}
      width={size}
    >
      {slices.map((slice) => {
        if (slice.value <= 0) return null;
        const read = isInteractive
          ? () => onRead({
            label: chart.labels[slice.index] ?? '',
            entries: [{value: slice.value, colorIndex: slice.index, share: slice.value / total}],
          })
          : undefined;
        if (slice.isWhole) {
          return (
            <g key={slice.index} onMouseEnter={read}>
              <circle cx={centre} cy={centre} fill={colorAt(slice.index)} r={radius} />
              {inner > 0 ? <circle cx={centre} cy={centre} fill="var(--color-surface)" pointerEvents="none" r={inner} /> : null}
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
        return <path d={path} fill={colorAt(slice.index)} key={slice.index} onMouseEnter={read} />;
      })}
    </svg>
  );
}
