// Package main - Wails 앱 바인딩 (Position Manager)
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/londonpotato1/position-monitor/internal/config"
	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/internal/exchange/binance"
	"github.com/londonpotato1/position-monitor/internal/exchange/bitget"
	"github.com/londonpotato1/position-monitor/internal/exchange/bithumb"
	"github.com/londonpotato1/position-monitor/internal/exchange/bybit"
	"github.com/londonpotato1/position-monitor/internal/exchange/gate"
	"github.com/londonpotato1/position-monitor/internal/exchange/okx"
	"github.com/londonpotato1/position-monitor/internal/exchange/upbit"
	"github.com/londonpotato1/position-monitor/internal/gap"
	"github.com/londonpotato1/position-monitor/internal/services"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// App struct
type App struct {
	ctx          context.Context
	cfg          *config.Config
	cfgMu        sync.Mutex // #S2 fix: cfg 동시 수정/저장 race 방지
	logger       *logger.Logger
	exchangeMgr  *services.ExchangeManager
	posCache     *services.PositionCache
	portfolioSvc *services.PortfolioService
	snapshotSvc  *services.SnapshotService
	btcPriceSvc  *services.BTCPriceService
	wsManager    *gap.Manager
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// resolveDataPath data/ 상대경로를 실행파일 기준으로 절대경로로 변환
// macOS .app 번들 실행 시 CWD=/ 이므로 상대경로가 동작하지 않음
// 탐색 순서: 이미 존재하는 파일 우선 → 실행파일 기준 경로로 생성
func resolveDataPath(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		// 후보 목록: 실행파일 위치에서 상위로 올라가며 기존 파일 탐색
		candidates := []string{
			// wails dev / go run: 현재 디렉터리
			rel,
			// 실행파일과 같은 디렉터리
			filepath.Join(exeDir, rel),
			// .app/Contents/MacOS/ → build/bin/
			filepath.Join(exeDir, "..", "..", "..", rel),
			// .app/Contents/MacOS/ → 프로젝트 루트 (data/ 위치)
			filepath.Join(exeDir, "..", "..", "..", "..", "..", rel),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				if abs, err := filepath.Abs(c); err == nil {
					return abs
				}
			}
		}
		// 기존 파일 없음 → 프로젝트 루트 기준으로 생성
		// .app/Contents/MacOS/ 기준 5단계 상위 = 프로젝트 루트
		projectRoot := filepath.Join(exeDir, "..", "..", "..", "..", "..")
		if abs, err := filepath.Abs(filepath.Join(projectRoot, rel)); err == nil {
			return abs
		}
		// fallback: 실행파일 기준
		if abs, err := filepath.Abs(filepath.Join(exeDir, rel)); err == nil {
			return abs
		}
	}
	return rel
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// config 로드
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		cfg = config.DefaultConfig()
	}
	a.cfg = cfg

	// logger 초기화
	if err := logger.Init(cfg.Logging.Level, cfg.Logging.File); err != nil {
		fmt.Fprintf(os.Stderr, "logger init failed: %v\n", err)
	}
	a.logger = logger.NewLogger("app")

	a.logger.Info("Position Manager started")
	a.logger.Info("enabled exchanges", "count", len(cfg.GetEnabledExchanges()))

	// ExchangeManager 초기화
	zlog := logger.ZeroLogger()
	a.exchangeMgr = services.NewExchangeManager(zlog)
	if err := a.exchangeMgr.Initialize(ctx, cfg); err != nil {
		a.logger.Error("exchange manager init failed", "error", err)
	}

	// 잔고 갱신 루프 시작
	a.exchangeMgr.StartBalanceRefreshLoop(ctx, 1*time.Minute)

	// PositionCache 초기화 (헷지 포지션 쌍 조회)
	a.posCache = services.NewPositionCache(a.exchangeMgr, 5*time.Second, zlog)
	go a.posCache.StartAutoRefresh(a.ctx)

	// Binance User Data Stream — ACCOUNT_UPDATE 실시간 수신 (선물 포지션/잔고 변경 즉시 캐시 갱신)
	if bnEx, ok := a.exchangeMgr.GetOverseasExchange("binance"); ok {
		if bnAdapter, ok := bnEx.(*binance.Adapter); ok {
			bnAdapter.StartUserStream(ctx, func() {
				a.posCache.TriggerRefresh(ctx)
			})
		}
	}

	// Bithumb private WS 잔고 스트림 (myAsset) — 잔고 변경 즉시 캐시 갱신
	if bhEx, ok := a.exchangeMgr.GetDomesticExchange("bithumb"); ok {
		if bhAdapter, ok := bhEx.(*bithumb.Adapter); ok {
			bhAdapter.StartAssetStream(ctx, func(_ map[string]*exchange.Balance) {
				a.posCache.TriggerRefresh(ctx)
			})
		}
	}

	// Upbit private WS 잔고 스트림 (myAsset) — 실시간 잔고 변경 즉시 캐시 갱신
	if upEx, ok := a.exchangeMgr.GetDomesticExchange("upbit"); ok {
		if upAdapter, ok := upEx.(*upbit.Adapter); ok {
			upAdapter.StartAssetStream(ctx, func(_ map[string]*exchange.Balance) {
				a.posCache.TriggerRefresh(ctx)
			})
		}
	}

	// Bybit V5 Private WS — wallet + position 이벤트 실시간 수신 → 캐시 갱신
	if byEx, ok := a.exchangeMgr.GetOverseasExchange("bybit"); ok {
		if byAdapter, ok := byEx.(*bybit.Adapter); ok {
			byAdapter.StartPrivateWS(ctx, func() {
				a.posCache.TriggerRefresh(ctx)
			})
		}
	}

	// OKX V5 Private WS — account + positions(SWAP) 이벤트 수신 시 즉시 캐시 갱신
	if okEx, ok := a.exchangeMgr.GetOverseasExchange("okx"); ok {
		if okAdapter, ok := okEx.(*okx.Adapter); ok {
			okAdapter.StartPrivateWS(ctx, func() {
				a.posCache.TriggerRefresh(ctx)
			})
		}
	}

	// PortfolioService 초기화 (포트폴리오 대시보드)
	portfolioSvc, err := services.NewPortfolioService(a.exchangeMgr, a.posCache, resolveDataPath("data/history.db"), zlog)
	if err != nil {
		a.logger.Error("portfolio service init failed", "error", err)
	} else {
		a.portfolioSvc = portfolioSvc
		a.portfolioSvc.StartDailySnapshot(ctx)
	}

	// SnapshotService 초기화 (5분 간격 잔고 스냅샷)
	if a.portfolioSvc != nil {
		snapshotSvc, err := services.NewSnapshotService(a.portfolioSvc, resolveDataPath("data/snapshots.db"), zlog)
		if err != nil {
			a.logger.Error("snapshot service init failed", "error", err)
		} else {
			a.snapshotSvc = snapshotSvc
			go a.snapshotSvc.Start5MinLoop(ctx)
		}
	}

	// BTCPriceService 초기화
	a.btcPriceSvc = services.NewBTCPriceService(zlog)

	// WS 인프라 초기화 + 거래소 클라이언트 등록
	priceCache := gap.NewPriceCache()
	orderbookCache := gap.NewOrderbookCache()
	a.wsManager = gap.NewManager(priceCache, orderbookCache, zlog)

	// 각 거래소 Public WS 클라이언트 생성 및 enabled 거래소만 등록
	if _, ok := a.exchangeMgr.GetDomesticExchange("upbit"); ok {
		a.wsManager.RegisterClient(upbit.NewPublicWSClient(zlog))
	}
	if _, ok := a.exchangeMgr.GetDomesticExchange("bithumb"); ok {
		a.wsManager.RegisterClient(bithumb.NewPublicWSClient(zlog))
	}
	if _, ok := a.exchangeMgr.GetOverseasExchange("binance"); ok {
		a.wsManager.RegisterClient(binance.NewPublicWSClient())
	}
	if _, ok := a.exchangeMgr.GetOverseasExchange("bybit"); ok {
		a.wsManager.RegisterClient(bybit.NewPublicWSClient())
	}
	if _, ok := a.exchangeMgr.GetOverseasExchange("okx"); ok {
		a.wsManager.RegisterClient(okx.NewPublicWSClient())
	}
	if _, ok := a.exchangeMgr.GetOverseasExchange("gate"); ok {
		a.wsManager.RegisterClient(gate.NewPublicWSClient())
	}
	if _, ok := a.exchangeMgr.GetOverseasExchange("bitget"); ok {
		a.wsManager.RegisterClient(bitget.NewPublicWSClient())
	}

	// WS Manager 시작
	if err := a.wsManager.Start(ctx); err != nil {
		a.logger.Error("WS manager start failed", "error", err)
	}

	// position_cache 와 WS Manager 연결
	a.posCache.SetWSManager(a.wsManager)
}

// shutdown is called when the app is closing
func (a *App) shutdown(ctx context.Context) {
	if a.logger != nil {
		a.logger.Info("Position Manager shutting down")
	}
	if a.wsManager != nil {
		_ = a.wsManager.Stop()
	}
	if a.snapshotSvc != nil {
		a.snapshotSvc.Close()
	}
	if a.portfolioSvc != nil {
		_ = a.portfolioSvc.Close()
	}
}

// GetAPIStatus 거래소 API 설정 상태 반환 (프론트엔드용)
func (a *App) GetAPIStatus() map[string]bool {
	if a.cfg == nil {
		return nil
	}
	return a.cfg.GetAPIStatus()
}

// GetHedgedPositions 헷지 포지션 쌍 조회 (프론트엔드용)
func (a *App) GetHedgedPositions() (*services.HedgedPositionsResponse, error) {
	if a.posCache == nil {
		return nil, fmt.Errorf("position cache not initialized")
	}
	resp := a.posCache.Get(context.Background())
	return &resp, nil
}

// RefreshHedgedPositions 헷지 포지션 강제 새로고침 (프론트엔드용)
func (a *App) RefreshHedgedPositions() error {
	if a.posCache == nil {
		return fmt.Errorf("position cache not initialized")
	}
	a.posCache.Refresh(context.Background())
	return nil
}

// ========== 포트폴리오 대시보드 Wails 바인딩 ==========

// ExchangeStatusItem 거래소 연결 상태
type ExchangeStatusItem struct {
	Name       string `json:"name"`
	Connected  bool   `json:"connected"`
	Configured bool   `json:"configured"`
}

// GetExchangeStatuses 거래소 연결 상태 조회
func (a *App) GetExchangeStatuses() []ExchangeStatusItem {
	exchanges := []string{"binance", "bybit", "okx", "gate", "bitget", "kucoin", "mexc", "htx", "hyperliquid", "lighter", "upbit", "bithumb"}
	var result []ExchangeStatusItem
	for _, name := range exchanges {
		configured := false
		connected := false
		if a.cfg != nil {
			configured = a.cfg.IsAPIConfigured(name)
		}
		if a.exchangeMgr != nil {
			connected = a.exchangeMgr.IsConnected(name)
		}
		if !configured && !connected {
			continue
		}
		result = append(result, ExchangeStatusItem{
			Name:       name,
			Connected:  connected,
			Configured: configured,
		})
	}
	return result
}

// GetPortfolioSummary 포트폴리오 요약 조회
func (a *App) GetPortfolioSummary() (*services.PortfolioSummary, error) {
	if a.portfolioSvc == nil {
		return nil, fmt.Errorf("portfolio service not initialized")
	}
	return a.portfolioSvc.GetSummary(context.Background())
}

// GetPortfolioSnapshots 일별 스냅샷 조회 (balance_snapshots에서 일별 집계)
func (a *App) GetPortfolioSnapshots(days int) ([]services.DailySnapshot, error) {
	if a.snapshotSvc == nil {
		return nil, fmt.Errorf("snapshot service not initialized")
	}
	return a.snapshotSvc.GetDailySnapshots(days)
}

// ========== 잔고 스냅샷 Wails 바인딩 ==========

// GetBalanceHistory 잔고 히스토리 조회 (스무딩 적용)
func (a *App) GetBalanceHistory(period string) ([]services.BalanceHistoryPoint, error) {
	if a.snapshotSvc == nil {
		return nil, fmt.Errorf("snapshot service not initialized")
	}
	return a.snapshotSvc.GetHistory(period)
}

// GetChange24h 24시간 변동 정보 조회
func (a *App) GetChange24h() (*services.Change24h, error) {
	if a.snapshotSvc == nil {
		return nil, fmt.Errorf("snapshot service not initialized")
	}
	return a.snapshotSvc.GetChange24h(context.Background())
}

// GetCumulativeReturn 누적 수익률 조회
func (a *App) GetCumulativeReturn() (*services.CumulativeReturn, error) {
	if a.snapshotSvc == nil {
		return nil, fmt.Errorf("snapshot service not initialized")
	}
	return a.snapshotSvc.GetCumulativeReturn()
}

// TakeSnapshotNow 수동 스냅샷 저장
func (a *App) TakeSnapshotNow() error {
	if a.snapshotSvc == nil {
		return fmt.Errorf("snapshot service not initialized")
	}
	return a.snapshotSvc.TakeManualSnapshot(context.Background())
}

// ========== BTC 가격 + 자산 분배 Wails 바인딩 ==========

// GetBTCKlines BTC kline 데이터 조회
// interval: "15m", "1h", "4h", "1d", "1w" / limit: 0이면 interval별 기본값
func (a *App) GetBTCKlines(interval string, limit int) ([]services.KlinePoint, error) {
	if a.btcPriceSvc == nil {
		return nil, fmt.Errorf("btc price service not initialized")
	}
	return a.btcPriceSvc.GetKlines(interval, limit)
}

// GetBTCPrice BTC 현재가 조회
func (a *App) GetBTCPrice() (float64, error) {
	if a.btcPriceSvc == nil {
		return 0, fmt.Errorf("btc price service not initialized")
	}
	return a.btcPriceSvc.GetCurrentPrice()
}

// GetAssetDistribution 라이브 포트폴리오 기반 거래소별/자산별 분배 조회
func (a *App) GetAssetDistribution() ([]services.AssetDistributionItem, error) {
	if a.portfolioSvc == nil {
		return nil, fmt.Errorf("portfolio service not initialized")
	}
	summary, err := a.portfolioSvc.GetSummary(context.Background())
	if err != nil {
		return nil, err
	}
	var items []services.AssetDistributionItem
	totalUSD := summary.TotalAssetsUSD
	if totalUSD <= 0 {
		return items, nil
	}
	for _, ex := range summary.Exchanges {
		if ex.TotalUSD < 0.01 {
			continue
		}
		items = append(items, services.AssetDistributionItem{
			Asset:    ex.Exchange,
			Amount:   ex.TotalUSD,
			USDValue: ex.TotalUSD,
			Pct:      ex.TotalUSD / totalUSD * 100,
		})
	}
	return items, nil
}
