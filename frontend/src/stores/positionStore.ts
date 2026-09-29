// ============================================================
// Position Store - Zustand 상태 관리
// ============================================================

import { create } from 'zustand'
import type { HedgedPositionPair, HedgedPositionLeg, FailedExchange } from '../types'
import {
  GetHedgedPositions,
  RefreshHedgedPositions,
  GetLiqAlertStatus,
  SetLiqAlertEnabled,
} from '../../wailsjs/go/main/App'
import type { services } from '../../wailsjs/go/models'

interface PositionState {
  pairs: HedgedPositionPair[]
  unmatched: HedgedPositionLeg[]
  updatedAt: string
  failedExchanges: FailedExchange[]
  loading: boolean
  error: string | null

  // 필터 상태
  hideSmallPairs: boolean
  hideSmallUnmatched: boolean
  unmatchedTypeFilter: 'all' | 'spot' | 'futures'
  unmatchedExchangeFilter: string

  // 청산 근접/상승 알림 상태 (null = 조회 전 또는 조회 실패 → liqAlertError)
  liqAlert: services.LiqAlertStatus | null
  liqAlertError: string | null

  // 액션
  fetchPositions: () => Promise<void>
  fetchLiqAlert: () => Promise<void>
  setLiqAlert: (exchange: string, symbol: string, enabled: boolean) => Promise<void>  // 실패 시 throw
  refreshPositions: () => Promise<void>
  setHideSmallPairs: (v: boolean) => void
  setHideSmallUnmatched: (v: boolean) => void
  setUnmatchedTypeFilter: (v: 'all' | 'spot' | 'futures') => void
  setUnmatchedExchangeFilter: (v: string) => void
}

interface RawResponse {
  pairs?: HedgedPositionPair[]
  unmatched?: HedgedPositionLeg[]
  updatedAt?: string
  failedExchanges?: FailedExchange[]
}

export const usePositionStore = create<PositionState>((set, get) => ({
  pairs: [],
  unmatched: [],
  updatedAt: '',
  failedExchanges: [],
  loading: false,
  error: null,

  hideSmallPairs: true,
  hideSmallUnmatched: true,
  unmatchedTypeFilter: 'all',
  unmatchedExchangeFilter: 'all',

  liqAlert: null,
  liqAlertError: null,

  fetchPositions: async () => {
    set({ loading: true, error: null })
    try {
      const data = await GetHedgedPositions() as RawResponse | null
      if (data) {
        set({
          pairs: data.pairs ?? [],
          unmatched: data.unmatched ?? [],
          updatedAt: data.updatedAt ?? '',
          failedExchanges: data.failedExchanges ?? [],
          loading: false,
        })
      } else {
        set({ pairs: [], unmatched: [], loading: false })
      }
    } catch (err) {
      set({ error: String(err), loading: false })
    }
    await get().fetchLiqAlert() // 기존 폴링에 편승 (별도 루프 없음)
  },

  fetchLiqAlert: async () => {
    try {
      set({ liqAlert: await GetLiqAlertStatus(), liqAlertError: null })
    } catch (err) {
      set({ liqAlert: null, liqAlertError: String(err) })
    }
  },

  setLiqAlert: async (exchange: string, symbol: string, enabled: boolean) => {
    await SetLiqAlertEnabled(exchange, symbol, enabled) // 오류는 호출자(UI)가 표시
    await get().fetchLiqAlert()
  },

  refreshPositions: async () => {
    set({ loading: true, error: null })
    try {
      await RefreshHedgedPositions()
      await get().fetchPositions()
    } catch (err) {
      set({ error: String(err), loading: false })
    }
  },

  setHideSmallPairs: (v) => set({ hideSmallPairs: v }),
  setHideSmallUnmatched: (v) => set({ hideSmallUnmatched: v }),
  setUnmatchedTypeFilter: (v) => set({ unmatchedTypeFilter: v }),
  setUnmatchedExchangeFilter: (v) => set({ unmatchedExchangeFilter: v }),
}))
