import type { HedgedPositionLeg, HedgedPositionPair } from '../types'

const USDT_BASE_QUANTITY_EXCHANGES = new Set(['binance', 'bybit', 'bitget'])
const USDT_SYMBOL = /^[A-Z0-9]+USDT$/
// 가격(mark/청산가) 표시 가능한 선형 무기한 심볼 형식. OKX/Gate size 는 계약 수라 notional 은 제외.
const LINEAR_PERP_SYMBOLS = new Map<string, RegExp>([
  ['binance', USDT_SYMBOL], ['bybit', USDT_SYMBOL], ['bitget', USDT_SYMBOL],
  ['okx', /^[A-Z0-9]+-USDT-SWAP$/], ['gate', /^[A-Z0-9]+_USDT$/], ['hyperliquid', /^[A-Z0-9]+$/],
])

function supportsUSDT(leg?: HedgedPositionLeg): leg is HedgedPositionLeg {
  return !!leg && USDT_BASE_QUANTITY_EXCHANGES.has(leg.exchange.toLowerCase()) &&
    USDT_SYMBOL.test(leg.symbol.toUpperCase())
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
  if (!leg || !LINEAR_PERP_SYMBOLS.get(leg.exchange.toLowerCase())?.test(leg.symbol.toUpperCase())) return '—'
  const raw = leg[field]
  const factor = leg.priceScaleFactor ?? 1
  if (raw == null || !Number.isFinite(raw) || raw <= 0 || !Number.isFinite(factor) || factor <= 0) return '—'
  const price = raw / factor
  if (!Number.isFinite(price) || price <= 0) return '—'
  // 0.1 이상 소수 2자리, 미만 5자리. 5자리로 0.00000 이 되는 초저가만 유효숫자 3개.
  if (price >= 0.1) return price.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  if (price >= 0.000005) return price.toLocaleString(undefined, { minimumFractionDigits: 5, maximumFractionDigits: 5 })
  return price.toLocaleString(undefined, { maximumSignificantDigits: 3 })
}

// 수량 표시: 소수 최대 2자리. 2자리로 0 이 되는 소량(BTC 0.004 등)만 유효숫자 3개.
export function formatQty(qty: number): string {
  return qty === 0 || Math.abs(qty) >= 0.005
    ? qty.toLocaleString(undefined, { maximumFractionDigits: 2 })
    : qty.toLocaleString(undefined, { maximumSignificantDigits: 3 })
}
