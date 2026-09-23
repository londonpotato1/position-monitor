import type { HedgedPositionLeg, HedgedPositionPair } from '../types'

const USDT_BASE_QUANTITY_EXCHANGES = new Set(['binance', 'bybit', 'bitget'])

function supportsUSDT(leg?: HedgedPositionLeg): leg is HedgedPositionLeg {
  return !!leg && USDT_BASE_QUANTITY_EXCHANGES.has(leg.exchange.toLowerCase()) &&
    /^[A-Z0-9]+USDT$/.test(leg.symbol.toUpperCase())
}

export function positionNotional(leg?: HedgedPositionLeg): number | null {
  if (!supportsUSDT(leg) || !Number.isFinite(leg.size) || !Number.isFinite(leg.markPrice) ||
      leg.size === 0 || leg.markPrice <= 0) return null
  const value = Math.abs(leg.size) * leg.markPrice
  return Number.isFinite(value) && value > 0 ? value : null
}

export function totalPositionNotional(pairs: HedgedPositionPair[]): number | null {
  let total = 0
  for (const pair of pairs) {
    const value = positionNotional(pair.futuresLeg ?? pair.shortLeg)
    if (value === null) return null
    total += value
  }
  return Number.isFinite(total) ? total : null
}

export function formatNotional(value: number | null): string {
  return value === null ? '—' : value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function formatPositionPrice(leg: HedgedPositionLeg | undefined, field: 'markPrice' | 'liquidationPrice'): string {
  if (!supportsUSDT(leg)) return '—'
  const raw = leg[field]
  const factor = leg.priceScaleFactor ?? 1
  if (raw == null || !Number.isFinite(raw) || raw <= 0 || !Number.isFinite(factor) || factor <= 0) return '—'
  const price = raw / factor
  return Number.isFinite(price) && price > 0
    ? price.toLocaleString(undefined, { maximumSignificantDigits: 8 })
    : '—'
}
