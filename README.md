# VoiceChat Server

OpenClaw 음성 채팅 중계 서버 (GCP VM)

## 아키텍처

```
📱 App ──HTTPS/SSE──→ VoiceChat Server ──TCP──→ ClawBridge ──HTTP──→ OpenClaw
```

## 역할
- ClawBridge(OpenClaw) 인스턴스 관리 (TCP 상시연결)
- 앱 API 제공 (HTTPS + SSE 스트리밍)
- 메시지 라우팅 (앱 → 대상 OpenClaw)
- 인증/세션 관리

## 실행
```bash
go build -o voicechat-server .
./voicechat-server
```

## 설정
환경변수:
- `PORT` - HTTP 서버 포트 (기본: 8080)
- `BRIDGE_PORT` - ClawBridge TCP 포트 (기본: 9090)
- `AUTH_TOKEN` - 앱 인증 토큰
- `BRIDGE_TOKEN` - ClawBridge 인증 토큰
