# VoiceChat 전체 아키텍처

## 시스템 구성도

```
┌─────────────────────────────────────────────────────────────────┐
│                        사용자 Samsung S25                        │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │              VoiceChat App (Capacitor + SvelteKit)         │  │
│  │                                                           │  │
│  │  [마이크] → AudioRecord → OkHttp WebSocket ──────────────────────┐
│  │  [스피커] ← Android TextToSpeech (on-device TTS) ←── AI 응답    │
│  │  [UI]    → HTTP POST /api/chat (SSE) ────────────────────────┐  │
│  └───────────────────────────────────────────────────────────┘  │  │
└─────────────────────────────────────────────────────────────────┘  │
                                                                     │
                          인터넷 (HTTPS/TLS)                         │
                                                                     │
┌─────────────────────────────────────────────────────────────────┐  │
│               GCP VM (34.64.164.13)                              │  │
│               voicechat.tyranno.xyz                              │  │
│                                                                  │  │
│  ┌──────────────────────────────────────────┐                   │  │
│  │         voice-chat-server (Go)            │                   │  │
│  │         /opt/voicechat/voicechat-server   │                   │  │
│  │                                           │                   │  │
│  │  HTTPS :443 ◄──── 앱 API 요청 ◄──────────────────────────────┘  │
│  │    ├─ GET  /health                        │                      │
│  │    ├─ GET  /api/instances                 │                      │
│  │    ├─ POST /api/chat (SSE) ──► Relay ─────────► ClawBridge      │
│  │    ├─ POST /api/tts ──► Google Cloud TTS  │                      │
│  │    ├─ WS   /api/stt/stream ──► :2700 ─┐  │                      │
│  │    ├─ POST /api/files/upload           │  │                      │
│  │    ├─ GET  /api/files/:id/:name        │  │                      │
│  │    ├─ GET  /api/apk/latest             │  │                      │
│  │    └─ GET  /api/apk/download           │  │                      │
│  │                                        │  │                      │
│  │  TLS TCP :9090 ◄── ClawBridge 연결 ◄──────────────────────────┘  │
│  │    ├─ register (인증)                  │  │                       │
│  │    ├─ heartbeat (30초)                 │  │                       │
│  │    ├─ chat_request → Bridge → OpenClaw │  │                       │
│  │    ├─ chat_response ← Bridge ← OpenClaw│  │                       │
│  │    └─ file_response ← Bridge           │  │                       │
│  └──────────────────────────────────────────┘                       │
│                                            │                         │
│  ┌──────────────────────────────────────┐  │                         │
│  │  google_stt_server.py (Python)       │◄─┘                         │
│  │  WebSocket :2700 (localhost only)    │                             │
│  │  Google Cloud STT REST API 호출       │                             │
│  │  VAD (RMS 기반) → 발화 감지 → 전송    │                             │
│  └──────────────────────────────────────┘                             │
│                                                                       │
│  TLS: Let's Encrypt (/etc/letsencrypt/live/voicechat.tyranno.xyz/)   │
│  Nginx: :80 → proxy_pass :8080 (HTTP 전용, 현재 미사용)              │
└───────────────────────────────────────────────────────────────────────┘

                          인터넷 (TLS TCP :9090)
                                  │
┌─────────────────────────────────────────────────────────────────┐
│                  윈도우 PC (우리집)                                │
│                                                                  │
│  ┌──────────────────────────────────────────┐                   │
│  │  clawdbot-service.exe (Go)               │                   │
│  │  Windows 서비스: OpenClawGateway          │                   │
│  │                                           │                   │
│  │  ├─ ClawBridge Client ─── TLS ──► :9090  │                   │
│  │  │   (chat_request 수신 → OpenClaw 전달)  │                   │
│  │  │   (chat_response 반환 → GCP 서버)      │                   │
│  │  │                                        │                   │
│  │  ├─ Gateway 관리 (node 프로세스)           │                   │
│  │  │   node entry.js gateway → :18789       │                   │
│  │  │                                        │                   │
│  │  └─ Power Monitor / Config Watcher        │                   │
│  └──────────────────────────────────────────┘                   │
│                          │                                       │
│                          ▼                                       │
│  ┌──────────────────────────────────────────┐                   │
│  │  OpenClaw Gateway (Node.js)              │                   │
│  │  ws://127.0.0.1:18789                    │                   │
│  │                                           │                   │
│  │  ├─ Anthropic Claude (AI 응답 생성)       │                   │
│  │  ├─ Telegram Bot 연결                     │                   │
│  │  ├─ WebChat Dashboard                     │                   │
│  │  ├─ Tool 실행 (exec, web, browser 등)     │                   │
│  │  └─ Cron / Heartbeat                      │                   │
│  └──────────────────────────────────────────┘                   │
└─────────────────────────────────────────────────────────────────┘
```

## 데이터 흐름

### 1. 음성인식 (STT) 흐름
```
S25 마이크 → AudioRecord (16kHz, 16-bit, mono)
  → NativeSttPlugin.java (OkHttp WebSocket)
  → wss://voicechat.tyranno.xyz/api/stt/stream
  → voice-chat-server stt.go (WebSocket proxy)
  → ws://127.0.0.1:2700 (google_stt_server.py)
  → VAD (RMS 기반 발화 감지)
  → Google Cloud STT REST API (speech:recognize)
  → 인식 결과 JSON 반환
  → 앱 UI에 텍스트 표시
```

### 2. AI 대화 (Chat) 흐름
```
앱 → POST https://voicechat.tyranno.xyz/api/chat
  { instanceId: "bridge_xxx", messages: [...] }
  → voice-chat-server relay.go
  → TCP 메시지 → ClawBridge (PC)
  → clawdbot-service bridge.go
  → POST http://localhost:18789/v1/chat/completions (SSE)
  → OpenClaw Gateway → Anthropic Claude
  ← SSE delta 스트리밍
  ← TCP chat_response (delta)
  ← SSE data: {"delta": "..."} → 앱
```

### 3. TTS (음성합성) 흐름
```
방법 A: On-device (현재 기본)
  AI 응답 텍스트 → Android TextToSpeech
  → USAGE_ASSISTANCE_NAVIGATION_GUIDANCE (Samsung DND 우회)
  → 스피커 출력

방법 B: Cloud TTS (서버 API)
  POST https://voicechat.tyranno.xyz/api/tts
  → Google Cloud TTS API (ko-KR-Neural2-A)
  → MP3 바이너리 반환 → 앱에서 재생
```

## 컴포넌트 상세

### voice-chat-server (GCP)
| 항목 | 값 |
|------|-----|
| 경로 | `/opt/voicechat/voicechat-server` |
| 소스 | `E:\Project\My\voice-chat-server` |
| 서비스 | `systemctl status voicechat-server` |
| HTTPS 포트 | 443 (TLS, Let's Encrypt) |
| Bridge 포트 | 9090 (TLS TCP) |
| 데이터 | `/opt/voicechat/data/` |
| TLS 인증서 | `/etc/letsencrypt/live/voicechat.tyranno.xyz/` |

### google_stt_server.py (GCP)
| 항목 | 값 |
|------|-----|
| 경로 | `/opt/voicechat/google_stt_server.py` |
| 서비스 | `systemctl status google-stt` |
| 포트 | 2700 (localhost WebSocket) |
| VAD | RMS threshold=800, silence=0.5s, min speech=0.3s |
| 최대 오디오 | 15초 |
| API | Google Cloud STT REST (`speech:recognize`) |

### clawdbot-service (Windows PC)
| 항목 | 값 |
|------|-----|
| 소스 | `E:\Project\My\clawdbot-service` |
| 서비스 | Windows 서비스 `OpenClawGateway` (자동 시작) |
| 설정 | `~/.openclaw/service-config.txt` |
| 로그 | `~/.openclaw/logs/service.log` |
| 기능 | Gateway 관리, ClawBridge, Power Monitor, Config Watch |

### VoiceChat App (Android)
| 항목 | 값 |
|------|-----|
| 소스 | `E:\Project\My\voice-chat` |
| 프레임워크 | Capacitor + SvelteKit |
| 테스트 기기 | Samsung Galaxy S25 (R3CY10AF61Z) |
| STT | NativeSttPlugin (OkHttp WebSocket) |
| TTS | Android TextToSpeech (on-device) |
| API 서버 | `https://voicechat.tyranno.xyz` |

## 서버 접속 정보

### GCP VM
```bash
ssh -i ~/.ssh/voicechat-key tyranno@34.64.164.13
# 또는
ssh -i ~/.ssh/voicechat-key tyranno@voicechat.tyranno.xyz
```

### 서비스 관리
```bash
# GCP
sudo systemctl restart voicechat-server
sudo systemctl restart google-stt
sudo systemctl status voicechat-server google-stt

# Windows (PowerShell 관리자)
Start-Service OpenClawGateway
Stop-Service OpenClawGateway
Get-Service OpenClawGateway
```

## 배포

### 서버 배포 (voice-chat-server)
```bat
# 로컬에서 빌드
E:\Project\My\voice-chat-server\build-linux.bat

# GCP에 업로드 + 재시작
E:\Project\My\voice-chat-server\deploy.bat
```

### 앱 빌드 (voice-chat)
```bat
cd E:\Project\My\voice-chat
npm run build          # SvelteKit 빌드
npx cap sync android   # Capacitor 동기화
cd android
.\gradlew assembleDebug
# APK: android/app/build/outputs/apk/debug/app-debug.apk
```

## 보안

- **Bridge 인증**: BRIDGE_TOKEN으로 TCP 연결 시 인증
- **TLS**: HTTPS(:443) + Bridge TCP(:9090) 모두 Let's Encrypt 인증서
- **STT 서버**: localhost:2700 바인딩 (외부 접근 불가)
- **OpenClaw**: localhost:18789 바인딩 (외부 접근 불가)
- **API 키**: Google Cloud STT/TTS API 키 사용 (환경변수)
