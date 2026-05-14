# VoiceChat Server

OpenClaw 음성 채팅 중계 서버 (자가호스팅 NanoPC-T4)

## 아키텍처

```
📱 App ──HTTPS──→ Cloudflare Tunnel ──→ NanoPC-T4 ──TCP──→ ClawBridge ──→ OpenClaw
       (voicechat.tyranno.xyz)         (port 8090)           (port 9090)
```

## 역할

- ClawBridge(OpenClaw) 인스턴스 관리 (TCP 상시연결)
- 앱 API 제공 (HTTPS + SSE 스트리밍, YouTube proxy)
- 메시지 라우팅 (앱 → 대상 OpenClaw)
- 인증/세션 관리
- APK OTA 배포 (`/api/apk/*`)

## 현재 운영 환경 (2026-05-14~)

| 항목 | 값 |
|---|---|
| 장비 | NanoPC-T4 (RK3399 ARM64) |
| OS | Ubuntu 24.04 Noble core (kernel 4.19) |
| 위치 | 집 LAN (192.168.123.200) |
| 외부 접근 | Cloudflare Tunnel (voicechat.tyranno.xyz, openclaw.tyranno.xyz, ssh.tyranno.xyz) |
| 서비스 | voicechat, cloudflared, openclaw-gateway, oc-proxy, fail2ban |

## 빌드 + 배포

### 로컬 빌드

```bash
# ARM64 (NanoPi 운영) — 기본
build-linux.bat
# → voicechat-server-linux-arm64

# amd64 (legacy, GCP/일반 VPS용 — 더 이상 운영 안 함)
build-linux-amd64.bat
```

### NanoPi 자동 배포

SSH 키 인증 미리 설정 필요 (`~/.ssh/config`에 `nanopi` 호스트 정의 + `ssh-copy-id nanopi` 완료):

```bash
# 빌드 + 배포 + 재시작 + 검증 한 번에
deploy.bat

# 특정 호스트 (LAN이나 다른 alias)
deploy.bat nanopi-lan

# Bridge TLS 활성화 deploy
deploy-bridge-tls.bat
```

### APK 배포 (`upload-apk.bat`)

```bash
# 기본 (c:\Project\88.MyProject\voice-chat 의 debug APK)
upload-apk.bat 0.10.27

# 특정 APK 경로
upload-apk.bat 0.10.27 path\to\app-debug.apk

# 특정 호스트
upload-apk.bat 0.10.27 . nanopi-lan
```

또는 voice-chat 측 PowerShell:
```powershell
cd C:\Project\88.MyProject\voice-chat
.\deploy-apk.ps1 -Version "0.10.27"
```

## 설정

환경변수 (`/opt/voicechat/.env`):

| 키 | 설명 |
|---|---|
| `PORT` | HTTP 서버 포트 (기본 8090) |
| `BRIDGE_PORT` | ClawBridge TCP 포트 (기본 9090) |
| `ACCESS_CODE` | 앱 인증 토큰 |
| `BRIDGE_TOKEN` | ClawBridge 인증 토큰 |
| `TLS_ENABLED` | HTTPS 활성화 (Cloudflare Tunnel 쓰면 false) |
| `BRIDGE_TLS_ENABLED` | Bridge TCP TLS (true 권장) |
| `DATA_DIR` | 데이터 디렉토리 (`/opt/voicechat/data`) |
| `VOSK_URL` | VOSK STT WebSocket URL |
| `GOOGLE_TTS_API_KEY` | Google Cloud TTS API Key |
| `FCM_SERVICE_ACCOUNT` | Firebase service account JSON path |
| `LOCAL_OPENCLAW_URL` | openclaw 게이트웨이 URL |
| `LOCAL_OPENCLAW_NAME` | openclaw 인스턴스 이름 |
| `LOCAL_OPENCLAW_TOKEN` | openclaw 토큰 |

## 마이그레이션 히스토리

| 시점 | 내용 |
|---|---|
| 2026-05-12 ~ 13 | GCP VM (`34.64.164.13`) → NanoPC-T4 마이그레이션 |
| 2026-05-13 | Ubuntu 18.04 → 24.04 재플래시 (Node 22 네이티브 위해) |
| 2026-05-14 | SSH 회사 PC 접근 복구 (호스트 키 갱신) |

상세 가이드: [`migration/nanopc-t4-reflash/README.md`](migration/nanopc-t4-reflash/README.md)

## 관련 저장소

- `voice-chat` — 앱 (Capacitor + SvelteKit, Android)
- `voice-chat-server` — 이 저장소 (Go server)
- `clawdbot-service` — ClawBridge 클라이언트 측
- OpenClaw — 외부 패키지 (npm: `openclaw`)
