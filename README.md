# Position Monitor

> 🇰🇷 한국어 | [English](README_EN.md)

> 멀티 거래소 헷지 포지션 모니터 + 잔액 대시보드.
> **읽기 전용.** 주문/매매/출금 기능 없음.

12개 암호화폐 거래소에 **읽기 전용 API 키** 로 연결해서 다음을 보여주는 데스크톱 앱 (Wails + React):

1. **헷지 페어** — 같은 코인의 현물↔선물 자동 매칭 (예: Upbit BTC 현물 ↔ Binance BTC 숏)
2. **미매칭 포지션** — 헷지가 깨져서 한쪽만 남은 포지션 + PnL
3. **잔액 대시보드** — 총 자산 (USD/KRW), 24시간 변화, 누적 수익률, BTC 차트, 자산 분포
4. **설정** — 거래소 활성화/비활성화 표시
5. **텔레그램 청산 알림 (선택, 기본 꺼짐)** — 아래 [텔레그램 청산 알림](#텔레그램-청산-알림-선택) 참조

헷지 페어 표는 Binance, Bybit, OKX, Gate, Bitget, Hyperliquid 선물의 청산가를 표시. 표의 mark/청산가는 0.1 이상 소수 2자리, 0.1 미만 소수 5자리 (초저가는 유효숫자 3개), 포지션 표의 수량은 소수 2자리 (소량은 유효숫자 3개).

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

프로젝트 루트의 `.env` 는 평문 `key=value` 파일. **변수명은 반드시 `_API_KEY` / `_API_SECRET` 형태로** 적어야 합니다 (예: `BINANCE_API_KEY=...`).

```bash
BINANCE_API_KEY=your_read_only_key
BINANCE_API_SECRET=your_read_only_secret

BYBIT_API_KEY=...
BYBIT_API_SECRET=...
# ... 모든 거래소 동일 패턴
```

DEX 는 별도 형식:
- `HYPERLIQUID_WALLET_ADDRESS` + `HYPERLIQUID_PRIVATE_KEY`
- `LIGHTER_ACCOUNT_INDEX` + `LIGHTER_PRIVATE_KEY` + `LIGHTER_API_KEY_INDEX`

**하위 호환 자동 별칭**: 짧은 형태 (`BINANCE_SECRET` 등) 으로 적어도 자동으로 `_API_SECRET` 으로 매핑됨 (사내 봇 사용자 대상). 단 `_KEY` 측은 매핑 없으므로 **반드시 `_API_KEY` 명시 권장**.

전체 변수명 목록은 [`.env.example`](.env.example) 참조.

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

## 텔레그램 청산 알림 (선택)

헷지 페어 표의 선물 숏 행마다 알림 ON/OFF 토글이 있고, ON 한 포지션만 본인 텔레그램 봇으로 알림을 보냅니다. 알림 전송만 하며 매매는 하지 않습니다. 기본 꺼짐.

- 지원 거래소: Binance, Bybit, OKX, Gate, Bitget, Hyperliquid
- 청산가까지 거리: 100 / 80 / 70 / 60 / 50% 는 진입 시 1회, 해당 구간에 머무는 동안 40% 는 4시간마다, 30% 1시간, 15% 15분, 5% 2분마다 반복. 추정 청산가 도달/초과 시 별도 알림
- 선물 진입가 대비 가격 상승 +20 / 30 / 40%
- 긴급 알림 (15% 이하 단계, 도달/초과, 상승 알림) 은 같은 메시지를 1초 간격 10회 전송
- 상태 메시지: 모니터링 시작마다 ON 목록과 함께 "모니터링 시작 (ON N개)" 1회 (ON 이 1개 이상일 때). 포지션 데이터가 30초 넘게 갱신 안 되거나 거래소 연결/조회 실패로 확인 불가한 상태가 60초 넘게 이어지면 "감시 불가" 1회, 이후 다시 확인되면 "감시 복구" 또는 그 사이 포지션이 사라졌으면 "감시 종료" 1회. 단계는 메모리에만 있어 앱 재시작 후 현재 단계를 1회 다시 알림
- 토글 상태는 `data/liq_alerts.db` 에 저장

켜는 법:

1. `config.yaml` 에 `telegram.enabled: true`
2. `.env` 에 `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID` 입력
3. 앱 재시작 (두 설정 모두 시작 시에만 읽음) 후 헷지 페어 표에서 원하는 행의 알림 ON

ON 된 행이 있는데 전송할 수 없으면 (스위치 OFF / 키 누락) 헷지 페어 표에 사유 배너가 표시됩니다.

## 이 앱이 절대 안 하는 것

- 주문 생성
- 주문 취소
- 포지션 청산
- 레버리지 설정
- 출금 / 이체
- Google Sheets / Excel 동기화

위 기능을 위한 인터페이스 + 어댑터 코드는 모두 제거됨. 포크해서 매매 추가하려면 직접 구현 필요. 앱이 외부로 보내는 메시지는 선택 기능인 텔레그램 청산 알림 (본인 봇) 뿐입니다.

## 라이센스

MIT — [`LICENSE`](LICENSE) 참조.

## 면책 조항

이 소프트웨어는 **정보 제공 목적** 으로만 제공됩니다. API 키 보안, 화면에 표시되는 데이터의 정확성, 그 데이터에 기반한 모든 의사결정의 책임은 사용자에게 있습니다. 저자는 거래 손실, 데이터 손실, 기타 어떤 책임도 지지 않습니다.
