// Package gate provides Gate.io exchange adapter.
// Gate.io 인터페이스 어댑터 - OverseasExchange 구현
package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// Adapter Gate.io 어댑터 (OverseasExchange 인터페이스 구현)
type Adapter struct {
	client *Client

	// TradFi 선물 심볼 캐시 — GetAllPositions 가 Position.TradFi 설정 (주식 선물을 같은 티커 코인과 짝짓지 않게)
	tradFi exchange.TradFiSet
}

// NewAdapter 새 어댑터 생성
func NewAdapter(apiKey, apiSecret string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, apiSecret),
	}
}

// Connect 연결
func (a *Adapter) Connect(ctx context.Context) error {
	return a.client.Connect(ctx)
}

// IsConnected 연결 상태
func (a *Adapter) IsConnected() bool {
	return a.client.IsConnected()
}

// ========== 현물 ==========

// GetSpotTicker 현물 티커 조회
func (a *Adapter) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetSpotTicker(ctx, symbol+"_USDT")
}

// GetSpotOrderbook 현물 오더북 조회
func (a *Adapter) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetSpotOrderbook(ctx, symbol+"_USDT")
}

// ========== 선물 ==========

// GetFuturesTicker 선물 티커 조회
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetFuturesTicker(ctx, exchange.ResolveFuturesSymbol("gate", symbol)+"_USDT")
}

// GetFuturesOrderbook 선물 오더북 조회
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetFuturesOrderbook(ctx, exchange.ResolveFuturesSymbol("gate", symbol)+"_USDT")
}

// ========== 잔고 ==========

// GetSpotBalance 현물 잔고 조회
func (a *Adapter) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	return a.client.GetSpotBalance(ctx, currency)
}

// GetFuturesBalance 선물 잔고 (USDT) 조회
func (a *Adapter) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	return a.client.GetFuturesBalance(ctx)
}

// GetAllBalances 전체 잔고 조회
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	return a.client.GetAllBalances(ctx)
}

// ========== 포지션 ==========

// GetAllPositions 전체 선물 포지션 조회 (TradFi 선물 표시 포함)
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	positions, err := a.client.GetAllPositions(ctx)
	if err != nil {
		return nil, err
	}
	a.tradFi.Mark("gate", positions, a.loadTradFi)
	return positions, nil
}

// futures contracts 페이지 크기(docs 최대 100) / 페이지 상한 — usdt 는 현재 1000여 개(11쪽). 넘으면 offset 이 안 먹는 것으로 보고 중단
const (
	contractsPageSize = 100
	contractsMaxPages = 50
)

// loadTradFi USDT 선물 contracts 에서 contract_type 이 있는(주식·금속·지수·외환·원자재) name 수집 — 코인은 빈 문자열
func (a *Adapter) loadTradFi() (map[string]bool, error) {
	set := map[string]bool{}
	for page := 0; ; page++ {
		// offset 이 끝없이 이어지면 포지션 조회가 멈춘다 — 실패로 처리해 이전 목록 유지
		if page == contractsMaxPages {
			return nil, fmt.Errorf("futures contracts %d쪽 초과", contractsMaxPages)
		}
		data, err := a.client.publicRequest("/api/v4/futures/usdt/contracts?limit=" + strconv.Itoa(contractsPageSize) + "&offset=" + strconv.Itoa(page*contractsPageSize))
		if err != nil {
			return nil, err
		}
		var contracts []struct {
			Name         string `json:"name"`
			ContractType string `json:"contract_type"`
		}
		// publicRequest 는 HTTP 상태를 보지 않는다 — 에러 본문({"label":...})은 배열이 아니라 여기서 실패
		if err := json.Unmarshal(data, &contracts); err != nil {
			return nil, err
		}
		for _, c := range contracts {
			if c.ContractType != "" {
				set[c.Name] = true
			}
		}
		if len(contracts) < contractsPageSize {
			return set, nil
		}
	}
}

// GetPosition 포지션 조회
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	return a.client.GetPosition(ctx, exchange.ResolveFuturesSymbol("gate", symbol)+"_USDT")
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }

// GetAdditionalBalances 추가 지갑 잔고 조회 (margin, earn, collateral)
func (a *Adapter) GetAdditionalBalances(ctx context.Context) (map[string]float64, error) {
	result := make(map[string]float64)

	// Cross Margin
	marginUSD, err := a.client.GetCrossMarginBalance()
	if err != nil {
		logger.Warnf("[Gate] Margin 잔고 조회 실패: %v", err)
	} else if marginUSD > 0.01 {
		result["margin"] = marginUSD
	}

	// Earn (Uni Lending)
	earnUSD, err := a.client.GetEarnBalance()
	if err != nil {
		logger.Warnf("[Gate] Earn 잔고 조회 실패: %v", err)
	} else if earnUSD > 0.01 {
		result["earn"] = earnUSD
	}

	// Collateral (담보 - 대출)
	collateralUSD, err := a.client.GetCollateralBalance()
	if err != nil {
		logger.Warnf("[Gate] Collateral 잔고 조회 실패: %v", err)
	} else if collateralUSD > 0.01 || collateralUSD < -0.01 {
		result["collateral"] = collateralUSD
	}

	return result, nil
}
