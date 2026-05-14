# VoiceChat 전체 아키텍처

> 최종 갱신: 2026-05-14
> GCP → NanoPC-T4 자가호스팅 마이그레이션 완료. 모든 컴포넌트가 NanoPi 한 장비에 통합.

## 시스템 구성도

```
┌─────────────────────────────────────────────────────────────────┐
│                        사용자 Samsung S25                        │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │              VoiceChat App (Capacitor + SvelteKit)         │  │
│  │                                                           │  │
│  │  [마이크] → AudioRecord → OkHttp WebSocket ──────────────────┐
│  │  [스피커] ← Android TextToSpeech (on-device TTS) ←── AI 응답│ │
│  │  [UI]    → HTTP POST /api/chat (SSE) ──────────────────┐   │ │
│  └───────────────────────────────────────────────────────┘ │   │ │
└────────────────────────────────────────────────────────────┘   │ │
                                                                  │ │
                          인터넷 (HTTPS)                          │ │
                                                                  │ │
┌─────────────────────────────────────────────────────────────────┴─┴──┐
│                  Cloudflare Edge (Tunnel)                              │
│                                                                        │
│  voicechat.tyranno.xyz ──► Tunnel UUID ──► NanoPi cloudflared        │
│  openclaw.tyranno.xyz                                                  │
│  ssh.tyranno.xyz                                                       │
└────────────────────────────────────────┬───────────────────────────────┘
                                          │
                       outbound 영구 연결 (NanoPi → CF 엣지)
                                          │
┌─────────────────────────────────────────┴───────────────────────────────┐
│         NanoPC-T4 (집 LAN 192.168.123.200, Ubuntu 24.04, ARM64)        │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  cloudflared (systemd: cloudflared.service)                     │    │
│  │  Tunnel UUID: 6d00f850-6d23-4469-9fbf-c881e8d4c899              │    │
│  │  config: /etc/cloudflared/config.yml                            │    │
│  │  ingress:                                                        │    │
│  │    voicechat.tyranno.xyz → http://localhost:8090                │    │
│  │    openclaw.tyranno.xyz  → http://localhost:18789               │    │
│  │    ssh.tyranno.xyz       → ssh://localhost:22                   │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│         │                                  │                            │
│  ┌──────▼──────────────────────────┐   ┌──▼──────────────────────────┐  │
│  │  voicechat-server (Go, ARM64)   │   │  openclaw-gateway (Node 22)  │  │
│  │  /opt/voicechat/voicechat-server│   │  /home/tyranno/.openclaw/    │  │
│  │  systemd: voicechat.service     │   │  systemd: openclaw-gateway   │  │
│  │  user: voicechat                │   │  user: tyranno               │  │
│  │                                 │   │                              │  │
│  │  HTTP :8090                     │   │  WS :18789 (loopback)        │  │
│  │   ├─ GET  /health               │   │   ├─ Anthropic Claude        │  │
│  │   ├─ GET  /api/instances        │   │   ├─ Telegram / WhatsApp 봇  │  │
│  │   ├─ POST /api/chat (SSE)       │   │   ├─ Tool 실행 (exec/web/...) │  │
│  │   ├─ POST /api/tts              │   │   ├─ Cron / Heartbeat        │  │
│  │   ├─ GET  /api/youtube/search   │   │   └─ Plugin runtime          │  │
│  │   ├─ GET  /api/youtube/proxy    │   └──────────────────────────────┘  │
│  │   ├─ GET  /api/youtube/stream   │              ▲                      │
│  │   ├─ POST /api/files/upload     │              │                      │
│  │   ├─ GET  /api/files/:id/:name  │              │ (oc-proxy 18789→18790)│
│  │   ├─ GET  /api/apk/latest       │   ┌──────────┴──────────────────┐  │
│  │   └─ GET  /api/apk/download     │   │  oc-proxy (Python)          │  │
│  │                                 │   │  systemd: oc-proxy.service  │  │
│  │  TCP :9090 (BRIDGE_TLS_ENABLED) │   │  /opt/voicechat/oc_proxy.py │  │
│  │   ├─ ClawBridge instances       │   └─────────────────────────────┘  │
│  │   ├─ chat_request → Bridge      │                                     │
│  │   └─ chat_response ← Bridge     │                                     │
│  └─────────────────────────────────┘                                     │
│         ▲                                                                │
│         │ (yt-dlp 호출 - subprocess)                                     │
│  ┌──────┴──────────────────────────┐                                     │
│  │  yt-dlp (정적 ARM64 바이너리)    │                                     │
│  │  /usr/local/bin/yt-dlp          │                                     │
│  │  + deno (/usr/local/bin/deno) JS 런타임                              │
│  └─────────────────────────────────┘                                     │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  추가 도구                                                       │    │
│  │  - sshd (port 22, fail2ban 보호)                                │    │
│  │  - cron (lottery, morning_briefing, watchlist_scan)             │    │
│  │  - ffmpeg, Python 3.12                                          │    │
│  └─────────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────┘
```

## 데이터 흐름

### 1. 음성인식 (STT) 흐름

현재는 클라이언트 측 Web Speech API 폴백 사용 (서버 측 VOSK 설치 예정):

```
S25 마이크 → Web Speech API (브라우저 또는 SvelteKit WebView)
  → 인식 결과 텍스트 → 앱 UI
```

서버 측 STT 옵션 (필요 시 활성화):
- VOSK (오프라인, `pip install vosk`, 한국어 모델 ~50MB)
- Google Cloud STT (`/opt/voicechat/google_stt_server.py`, API 키 필요)

### 2. AI 대화 (Chat) 흐름

```
앱 → POST https://voicechat.tyranno.xyz/api/chat
  { instanceId: "...", messages: [...] }
  → Cloudflare Tunnel → NanoPi:cloudflared → http://localhost:8090
  → voicechat-server relay.go
  → TCP :9090 → ClawBridge (loopback)
  → openclaw-gateway :18789
  → Anthropic Claude API
  ← SSE delta 스트리밍
  ← 응답 → 앱
```

### 3. 음악 재생 흐름

```
앱 → GET /api/youtube/proxy?videoId=X (Cloudflare Tunnel)
  → voicechat-server youtube.go
  → yt-dlp subprocess (deno JS 런타임 사용 → m4a 추출)
  → googlevideo.com (NanoPi 주거용 IP → 봇 차단 없음)
  ← audioUrl 스트리밍 ← Range 응답
  → ExoPlayer (앱 Android) → 재생
```

### 4. TTS (음성합성) 흐름

```
방법 A: On-device (기본)
  AI 응답 텍스트 → Android TextToSpeech → 스피커

방법 B: Cloud TTS (서버 API)
  POST /api/tts → Google Cloud TTS → MP3 → 앱 재생
```

## 컴포넌트 상세

### voice-chat-server (NanoPi, Go ARM64)

| 항목 | 값 |
|---|---|
| 바이너리 | `/opt/voicechat/voicechat-server` (ARM64) |
| 소스 | `C:\Project\88.MyProject\voice-chat-server` |
| 빌드 | `build-linux.bat` (GOOS=linux GOARCH=arm64) |
| 서비스 | `sudo systemctl status voicechat` |
| HTTP 포트 | 8090 (loopback) |
| Bridge 포트 | 9090 (TLS TCP) |
| 데이터 | `/opt/voicechat/data/` + `/data/` (SD 마운트) |
| 실행 사용자 | `voicechat` |

### cloudflared (NanoPi)

| 항목 | 값 |
|---|---|
| 바이너리 | `/usr/local/bin/cloudflared` (ARM64) |
| 서비스 | `sudo systemctl status cloudflared` |
| 시스템 config | `/etc/cloudflared/config.yml` |
| Tunnel UUID | `6d00f850-6d23-4469-9fbf-c881e8d4c899` |
| 자격증명 | `/home/tyranno/.cloudflared/<UUID>.json` |

### openclaw-gateway (NanoPi, Node 22)

| 항목 | 값 |
|---|---|
| 설치 | `npm install -g openclaw` (Node ≥22.14 필요) |
| 설정 | `/home/tyranno/.openclaw/openclaw.json` |
| 서비스 | `sudo systemctl status openclaw-gateway` |
| 포트 | 18789 (loopback) |
| 외부 접근 | `https://openclaw.tyranno.xyz` (Cloudflare Tunnel) |
| 실행 사용자 | `tyranno` |

### oc-proxy (NanoPi, Python)

| 항목 | 값 |
|---|---|
| 소스 | `/opt/voicechat/oc_proxy.py` |
| 서비스 | `sudo systemctl status oc-proxy` |
| 포트 | 18790 |
| 역할 | 18789 → 18790 프록시 (CORS / origin 헤더 처리) |

### VoiceChat App (Android)

| 항목 | 값 |
|---|---|
| 소스 | `C:\Project\88.MyProject\voice-chat` |
| 프레임워크 | Capacitor 6 + SvelteKit 5 + Tailwind 4 |
| 테스트 기기 | Samsung Galaxy S25 |
| STT | Web Speech API (현재) / NativeSttPlugin (옵션) |
| TTS | Android TextToSpeech (on-device) |
| API 서버 | `https://voicechat.tyranno.xyz` |

## 서버 접속 정보

### SSH (회사 PC / 외부)

```bash
ssh nanopi
# ~/.ssh/config 에 정의됨:
#   HostName ssh.tyranno.xyz
#   User tyranno
#   ProxyCommand cloudflared access ssh --hostname ssh.tyranno.xyz
# 키 인증 (id_rsa) — 비번 불필요
```

### SSH (집 LAN)

```bash
ssh nanopi-lan
# HostName 192.168.123.200, User tyranno
```

### 서비스 관리 (NanoPi에서)

```bash
sudo systemctl status voicechat cloudflared openclaw-gateway oc-proxy
sudo systemctl restart voicechat
sudo journalctl -u voicechat -f
sudo journalctl -u cloudflared -f

# fail2ban 차단 확인
sudo fail2ban-client status sshd
```

## 배포

### 서버 배포 (voice-chat-server)

```bash
# 로컬 (회사/집 PC)에서:
cd C:\Project\88.MyProject\voice-chat-server
deploy.bat                 # ARM64 빌드 → nanopi에 SCP → 재시작
# 또는 LAN 직접:
deploy.bat nanopi-lan
```

### APK 배포

```bash
# server 측 스크립트
upload-apk.bat 0.10.27

# 또는 app 측 PowerShell (빌드부터)
cd C:\Project\88.MyProject\voice-chat
.\deploy-apk.ps1 -Version "0.10.27"
```

### 앱 빌드 (수동)

```bash
cd C:\Project\88.MyProject\voice-chat
npm run build          # SvelteKit 빌드
npx cap sync android   # Capacitor 동기화
cd android
.\gradlew assembleDebug
# 출력: android/app/build/outputs/apk/debug/app-debug.apk
```

## 보안

- **SSH**: 키 인증만 (비번 보조), fail2ban (3회 실패 24시간 차단, LAN 제외)
- **Cloudflare Tunnel**: outbound 연결만, 포트 개방 불필요, 집 IP 비공개
- **Bridge 인증**: BRIDGE_TOKEN으로 TCP 연결 시 인증
- **TLS**: Cloudflare가 자동 처리 (Tunnel + Edge TLS)
- **Bridge TCP**: `BRIDGE_TLS_ENABLED=true` 시 자체 TLS
- **OpenClaw**: loopback 18789 (외부는 Cloudflare Tunnel + openclaw.tyranno.xyz 경유)
- **API 키**: Google Cloud TTS, Anthropic 등 `/opt/voicechat/.env`에 저장 (600 권한)

## 백업 / 복구

GCP 백업: `C:\Users\lab\Downloads\gcp-backup\` (42MB tarball + NanoPi 추가 백업)

재플래시 / 재해 복구 절차: [`migration/nanopc-t4-reflash/README.md`](migration/nanopc-t4-reflash/README.md)

## 관련 저장소

- `voice-chat` — 앱 (Capacitor + SvelteKit, Android)
- `voice-chat-server` — 이 저장소 (Go server)
- `clawdbot-service` — 별도 (이전 GCP 시절 윈도우 PC 측 Bridge 클라이언트, **현재 미사용**)
- OpenClaw — 외부 npm 패키지
