import type { Severity } from './types'

export const SEVERITIES: Severity[] = ['critical', 'high', 'medium', 'low']

/**
 * Severity is an ORDERED scale, so it is encoded with a single-hue ordinal
 * ramp rather than four categorical hues. The obvious red/orange/yellow/green
 * set fails CVD separation (worst pair ΔE 4.1 deutan, target >= 8) and the
 * normal-vision floor (13.6, hard floor 15) - it is unreadable to a
 * deuteranope. This ramp passes every ordinal check against the #1a1a19
 * surface: monotone lightness, adjacent ΔL >= 0.06, light end 2.15:1.
 */
export const SEVERITY_COLOR: Record<Severity, string> = {
  critical: '#b7d3f6',
  high: '#6da7ec',
  medium: '#2a78d6',
  low: '#184f95',
}

/**
 * Status hues are reserved for a LONE status mark that also carries a text
 * label - the health tile, the branch health pill, a delta arrow. Never four
 * of them in one chart.
 */
export const STATUS = {
  good: '#0ca30c',
  warning: '#fab219',
  serious: '#ec835a',
  critical: '#d03b3b',
} as const

export const CHART = {
  surface: '#1a1a19',
  series1: '#3987e5',   // categorical slot 1: single-series bars
  grid: '#2c2c2a',
  axis: '#383835',
  muted: '#898781',
  text: '#ffffff',
} as const

export function healthStatus(score: number): { color: string; label: string } {
  if (score >= 80) return { color: STATUS.good, label: 'healthy' }
  if (score >= 50) return { color: STATUS.warning, label: 'needs attention' }
  return { color: STATUS.critical, label: 'at risk' }
}

export function relativeTime(iso: string): string {
  const mins = Math.round((Date.now() - new Date(iso).getTime()) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  if (mins < 60 * 24) return `${Math.round(mins / 60)}h ago`
  return `${Math.round(mins / 1440)}d ago`
}
