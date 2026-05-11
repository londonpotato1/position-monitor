// ============================================================
// Position Store - Zustand 상태 관리
// ============================================================

import { create } from 'zustand'
import type { HedgedPositionPair, HedgedPositionLeg, FailedExchange } from '../types'
import {
  GetHedgedPositions,
  RefreshHedgedPositions,
} from '../../wailsjs/go/main/App'

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

  // 액션
  fetchPositions: () => Promise<void>
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
