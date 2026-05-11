// ============================================================
// Position Manager - 타입 정의
// Go 백엔드 Wails 바인딩과 1:1 대응
// ============================================================

export interface HedgedPositionLeg {
  exchange: string
  marketType: 'spot' | 'futures'
  symbol: string
  side: 'holding' | 'long' | 'short'
  size: number
  entryPrice: number
  markPrice: number
  pnl: number
  leverage: number
}

export interface HedgedPositionPair {
  pairId: string
  coin: string
  spotLeg?: HedgedPositionLeg
  futuresLeg?: HedgedPositionLeg
  longLeg?: HedgedPositionLeg
  shortLeg?: HedgedPositionLeg
  matchedSize: number
  direction: string
  pairType: 'spot_futures' | 'futures_futures'
  futuresPnl: number
}

export interface FailedExchange {
  exchange: string
  scope: string   // "spot" | "futures"
  error: string
}

export interface HedgedPositionsResponse {
  pairs: HedgedPositionPair[]
  unmatched: HedgedPositionLeg[]
  updatedAt: string
  failedExchanges?: FailedExchange[]
}

// ============================================================
// Portfolio / Dashboard 타입
// ============================================================

export interface ExchangeSummary {
  exchange: string
  spotValueUSD: number
  futuresMargin: number
  additionalUSD: number
  unrealizedPnl: number
  totalUSD: number
}

export interface PortfolioSummary {
  totalAssetsUSD: number
  totalSpotUSD: number
  totalFuturesUSD: number
  totalAdditionalUSD: number
  totalUnrealizedPnl: number
  matchRate: number
  hedgedPairCount: number
  unmatchedCount: number
  exchanges: ExchangeSummary[]
  updatedAt: string
}

export interface DailySnapshot {
  date: string
  totalAssetsUSD: number
  totalPnl: number
}

// 스냅샷 히스토리
export interface BalanceHistoryPoint {
  timestamp: string
  totalUSD: number
  totalKRW: number
  btcPrice: number
}

// 24h 변동
export interface Change24h {
  changeUSD: number
  changePct: number
  prevTotal: number
  currTotal: number
}

// 누적 수익률
export interface CumulativeReturn {
  returnUSD: number
  returnPct: number
  firstSnapshotDate: string
  firstSnapshotUSD: number
}

// ============================================================
// Chart 타입
// ============================================================

export interface KlinePoint {
  timestamp: number  // Unix ms
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface AssetDistributionItem {
  asset: string
  amount: number
  usdValue: number
  pct: number
}

