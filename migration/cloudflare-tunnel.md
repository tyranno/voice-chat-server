# Cloudflare Tunnel — NanoPi self-hosting

목적: 집의 NanoPi를 인터넷에서 `voicechat.tyranno.xyz`로 접근 가능하게.
포트 개방·DDNS·집 IP 노출 없음. TLS는 Cloudflare가 처리.

## 사전 준비

1. Cloudflare 계정 (https://dash.cloudflare.com) — 무료
2. `tyranno.xyz` 도메인을 Cloudflare에 추가 (가입 후 안내 따라)
   - 기존 DNS 레코드는 Cloudflare로 자동 import됨
   - registrar에서 NameServer를 Cloudflare가 알려준 2개로 변경 (보통 propagation 1~24h)
3. NanoPi에 cloudflared 설치 완료 상태 (nanopi-setup.sh가 처리)

## Tunnel 생성

```bash
# NanoPi에서:
cloudflared tunnel login
# → 브라우저 URL 나옴. PC 브라우저에서 열어서 tyranno.xyz 인증.
# → ~/.cloudflared/cert.pem 생성됨

cloudflared tunnel create voicechat
# → Tunnel UUID 발급됨. ~/.cloudflared/<UUID>.json 생성

# 라우팅: voicechat.tyranno.xyz → 로컬 8090
cloudflared tunnel route dns voicechat voicechat.tyranno.xyz
# → Cloudflare DNS에 CNAME 자동 등록됨
```

## Tunnel 설정 파일

```bash
mkdir -p ~/.cloudflared
cat > ~/.cloudflared/config.yml <<'EOF'
tunnel: voicechat
credentials-file: /home/<USER>/.cloudflared/<UUID>.json

ingress:
  - hostname: voicechat.tyranno.xyz
    service: http://localhost:8090
    originRequest:
      noTLSVerify: true
      connectTimeout: 30s
      tlsTimeout: 30s
      keepAliveTimeout: 90s
      httpHostHeader: voicechat.tyranno.xyz
  - service: http_status:404
EOF
```
`<USER>`, `<UUID>` 본인 환경 값으로 치환.

## 서비스 등록 (systemd)

```bash
sudo cloudflared service install
sudo systemctl enable cloudflared
sudo systemctl start cloudflared
sudo systemctl status cloudflared
```

## 확인

```bash
# Tunnel 상태
cloudflared tunnel info voicechat

# 외부에서 접근 테스트
curl -v https://voicechat.tyranno.xyz/api/youtube/search?q=test
```

## DNS 전환 (GCP → NanoPi)

Cloudflare 대시보드 → DNS → `voicechat.tyranno.xyz` 레코드:
- 기존: A record → GCP IP (34.64.164.13)
- 변경: CNAME → `<UUID>.cfargotunnel.com` (tunnel route 명령이 자동으로 만들어줌)

전환 즉시 폰 앱이 NanoPi로 연결됨.

## GCP 중지 절차

1. NanoPi 동작 확인 (음악 검색/재생/저장 모두)
2. GCP 인스턴스: stop (immediate billing 차단)
3. 며칠 후 안정성 확인되면 GCP 인스턴스 삭제 + GCP 무료 trial 종료
