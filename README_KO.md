# Position Monitor (한국어)

> 🇰🇷 한국어 | [English](README.md)

> 멀티 거래소 헷지 포지션 모니터 + 잔액 대시보드.
> **읽기 전용.** 주문/매매/출금 기능 없음.

12개 암호화폐 거래소에 **읽기 전용 API 키** 로 연결해서 다음을 보여주는 데스크톱 앱 (Wails + React):

1. **헷지 페어** — 같은 코인의 현물↔선물 자동 매칭 (예: Upbit BTC 현물 ↔ Binance BTC 숏)
2. **미매칭 포지션** — 헷지가 깨져서 한쪽만 남은 포지션 + PnL
3. **잔액 대시보드** — 총 자산 (USD/KRW), 24시간 변화, 누적 수익률, BTC 차트, 자산 분포
4. **설정** — 거래소 활성화/비활성화 표시

매매 코드는 일체 포함 안 됨. 거래소 어댑터는 ticker / orderbook / balance / position 조회 메서드만 노출.

## 지원 거래소 (12개)

- **국내**: Upbit, Bithumb
- **해외 (현물 + 선물)**: Binance, Bybit, OKX, Gate, Bitget, KuCoin, MEXC, HTX
- **DEX (선물 전용)**: Hyperliquid, Lighter

## 기술 스택

- 백엔드: Go 1.25 + [Wails v2.12](https://wails.io)
- 프론트엔드: React 18 + TypeScript 5 + Vite 5 + Tailwind CSS v4 + Zustand
- DB: SQLite (modernc.org/sqlite, WAL 모드) — 로컬 스냅샷 전용
- 빌드: `wails build` → macOS `.app`

## 빠른 시작

### 사전 요구사항

- Go 1.25 이상
- Node 18 이상
- [Wails CLI](https://wails.io/docs/gettingstarted/installation): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

### 설치

```bash
git clone https://github.com/londonpotato1/position-monitor.git
cd position-monitor

# 1. 설정 템플릿 복사
cp config.yaml.example config.yaml
cp .env.example .env

# 2. .env 편집 — 읽기 전용 API 키 입력 (아래 "API 키" 섹션 참조)
# 3. config.yaml 편집 — 사용할 거래소를 enabled: true 로

# 4. 빌드
wails build

# 5. 실행
open build/bin/position-monitor.app
```

개발 모드 (핫 리로드):

```bash
wails dev
```

## API 키

**읽기 전용 키만 사용하세요.** 이 앱은 매매/출금/이체 권한 필요 없음. 거래소에서 키 발급 시 다음만 활성화:

- 계정 / 잔액 조회
- 포지션 조회
- 시세 / 호가 조회

### .env 파일

프로젝트 루트의 `.env` 는 평문 `key=value` 파일. 거래소마다 변수명 변형이 있어서, 주된 이름 하나만 적으면 앱이 자동으로 별칭 적용:

| 사용자가 적는 이름 | 자동으로 같이 set 되는 이름 |
|--------------------|-----------------------------|
| `BINANCE_SECRET` | `BINANCE_API_SECRET` |
| `BYBIT_SECRET` | `BYBIT_API_SECRET` |
| `OKX_SECRET` | `OKX_API_SECRET` |
| `HTX_SECRET` | `HTX_API_SECRET` |
| `UPBIT_SECRET` | `UPBIT_API_SECRET` |
| `BITHUMB_SECRET` | `BITHUMB_API_SECRET` |
| `GATE_API_SECRET` | `GATE_SECRET` |
| `HYPERLIQUID_API_KEY` | `HYPERLIQUID_WALLET_ADDRESS` |
| `HYPERLIQUID_SECRET` | `HYPERLIQUID_PRIVATE_KEY` |
| `LIGHTER_SECRET` | `LIGHTER_PRIVATE_KEY` |

전체 목록은 [`.env.example`](.env.example) 참조.

### 🔒 보안 권장 사항

`.env` 평문 + `.gitignore` 는 업계 표준이지만, 본인 머신에서 다음을 확인하세요:

1. **읽기 전용 키 보장** ← 가장 중요
   - 거래소 API 키 발급 시 "출금" / "거래" 권한 **모두 OFF**
   - "조회" 권한만 ON. 키가 유출돼도 도둑이 할 수 있는 건 잔액 보기뿐

2. **거래소 IP 화이트리스트** 설정
   - 거래소 설정에서 "내 IP 만 허용". 키 유출돼도 다른 IP 차단

3. **macOS FileVault** ON 확인
   - 시스템 설정 → 개인정보 보호 → FileVault. 컴퓨터 분실 시 디스크 보호

4. **키 주기적 회전** (3-6개월)
   - 거래소에서 키 재발급, 옛 키 폐기

5. **고급 사용자**: `.env` 평문이 부담스러우면 외부 시크릿 매니저 사용 권장
   - macOS Keychain (`security` CLI)
   - [1Password CLI](https://developer.1password.com/docs/cli/)
   - [Bitwarden CLI](https://bitwarden.com/help/cli/)
   - 위 도구로 키를 가져와서 환경변수로 export 후 앱 실행

## 동작 원리

- 활성화된 거래소를 `position.refresh_interval` 초마다 폴링 (기본 5초)
- 같은 코인의 현물 잔고 + 선물 포지션 → 헷지 페어로 자동 매칭
- 한쪽만 있는 포지션은 미매칭 탭에 표시
- 잔액 대시보드는 USDT/KRW 환율 (Upbit → Bithumb 폴백) 으로 USD 환산
- 일일 스냅샷은 로컬 `data/` 디렉토리에 SQLite 로 저장

## 이 앱이 절대 안 하는 것

- 주문 생성
- 주문 취소
- 포지션 청산
- 레버리지 설정
- 출금 / 이체
- 알림 발송 (텔레그램 등)
- Google Sheets / Excel 동기화

위 기능을 위한 인터페이스 + 어댑터 코드는 모두 제거됨. 포크해서 매매 추가하려면 직접 구현 필요.

## 라이센스

MIT — [`LICENSE`](LICENSE) 참조.

## 면책 조항

이 소프트웨어는 **정보 제공 목적** 으로만 제공됩니다. API 키 보안, 화면에 표시되는 데이터의 정확성, 그 데이터에 기반한 모든 의사결정의 책임은 사용자에게 있습니다. 저자는 거래 손실, 데이터 손실, 기타 어떤 책임도 지지 않습니다.
