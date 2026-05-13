# Cloudflare 설정 보존/복원 가이드

## 현재 구조

```
폰 → voicechat.tyranno.xyz → Cloudflare DNS (CNAME)
                              ↓
                              <tunnel-uuid>.cfargotunnel.com
                              ↓
                              Cloudflare Edge
                              ↓
                              (outbound 연결 유지)
                              ↓
                              NanoPC-T4의 cloudflared
                              ↓
                              localhost:8090 (voicechat-server)
```

SSH 접근:
```
PC → ssh.tyranno.xyz → 동일 흐름 → NanoPC-T4의 SSH:22
```

## 핵심 사실: **재플래시 후에도 변경 불필요한 것들**

| 항목 | 위치 | 상태 |
|---|---|---|
| Cloudflare 계정 | 너 로그인 | ✅ 유지 |
| 도메인 `tyranno.xyz` Cloudflare DNS 관리 | Cloudflare 대시보드 | ✅ 유지 |
| Nameserver (hosting.co.kr → Cloudflare) | hosting.co.kr 패널 | ✅ 유지 |
| **Tunnel** (이름: `voicechat`) | Cloudflare 계정 + UUID | ✅ 유지 (UUID 우리가 백업) |
| DNS 레코드 `voicechat.tyranno.xyz` → CNAME → `<UUID>.cfargotunnel.com` | Cloudflare DNS | ✅ 유지 |
| DNS 레코드 `ssh.tyranno.xyz` → CNAME → `<UUID>.cfargotunnel.com` | Cloudflare DNS | ✅ 유지 |
| Cloudflare Access 정책 (SSH 보호) | Cloudflare Zero Trust | ✅ 유지 (있으면) |

**결론**: Cloudflare 대시보드는 **재플래시 동안 절대 건드릴 필요 없음**.

## 사라지는 것 (재플래시 시)

NanoPC-T4의 eMMC에 있던:
- `/usr/local/bin/cloudflared` (바이너리)
- `/home/tyranno/.cloudflared/cert.pem` (Cloudflare 계정 인증서)
- `/home/tyranno/.cloudflared/<UUID>.json` (Tunnel 자격증명 — 우리 UUID `6d00f850-6d23-4469-9fbf-c881e8d4c899`)
- `/home/tyranno/.cloudflared/config.yml` (ingress 라우팅)
- `/etc/cloudflared/config.yml` (systemd 시스템 서비스용)
- systemd unit (`cloudflared.service`)

이것들 다 **`cloudflared-config.tar.gz` (786B)에 백업됨**.

## 복원 방법 (재플래시 후)

### 자동 (post-install.sh가 처리)
post-install 스크립트가 다음을 수행:
1. `cloudflared` 바이너리 재다운로드
2. 백업의 `cloudflared-config.tar.gz` 풀기 → `~/.cloudflared/`, `/etc/cloudflared/` 복원
3. `sudo cloudflared service install` → systemd 등록
4. `sudo systemctl enable --now cloudflared`

### 수동 (스크립트 없이)
```bash
# 1. cloudflared 바이너리
sudo curl -L -o /usr/local/bin/cloudflared \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64
sudo chmod +x /usr/local/bin/cloudflared

# 2. 백업 풀기
cd / && sudo tar xzf ~/gcp-backup/nanopi-extra/cloudflared-config.tar.gz
sudo chown -R tyranno:tyranno /home/tyranno/.cloudflared
sudo chmod 700 /home/tyranno/.cloudflared
sudo chmod 600 /home/tyranno/.cloudflared/*.json /home/tyranno/.cloudflared/cert.pem

# 3. systemd 등록
sudo cloudflared service install
sudo systemctl enable --now cloudflared

# 4. 검증
sudo systemctl status cloudflared
sudo journalctl -u cloudflared -f
# 또는 외부에서:
curl https://voicechat.tyranno.xyz/health
```

## 검증

복원 후 다음 모두 동작해야 함:
- `curl https://voicechat.tyranno.xyz/health` → `{"status":"ok"}`
- `ssh nanopi` (PC에서, 같은 SSH config 그대로) → SSH 키 인증으로 접속

## Cloudflare 대시보드 확인 사항 (선택)

만약 트러블슈팅 필요하면 Cloudflare 대시보드에서:

1. **Zero Trust → Networks → Tunnels**
   - "voicechat" 터널 보임?
   - UUID `6d00f850-6d23-4469-9fbf-c881e8d4c899` 매치?
   - 상태 "HEALTHY"?

2. **DNS → tyranno.xyz**
   - `voicechat` CNAME → `<UUID>.cfargotunnel.com` (Proxy status: DNS only 또는 Proxied 둘 다 동작)
   - `ssh` CNAME → 동일

3. **Zero Trust → Access → Applications** (있으면)
   - SSH 보호 정책 확인

## 응급 시나리오: Tunnel 분실

만약 UUID `6d00f850-6d23-4469-9fbf-c881e8d4c899` 자격증명이 망가졌다면:

```bash
# NanoPi에서 새 터널 만들기 (구버전과 같은 이름으로)
cloudflared tunnel login   # 브라우저 인증
cloudflared tunnel delete voicechat   # 기존 (실패해도 OK, 다른 UUID라)
cloudflared tunnel create voicechat   # 새로 생성 — NEW UUID 발급됨
cloudflared tunnel route dns voicechat voicechat.tyranno.xyz   # DNS 새 UUID로 자동 변경
cloudflared tunnel route dns voicechat ssh.tyranno.xyz
```

→ 새 UUID로 변경되지만 도메인은 그대로 동작.

## 도메인 변경 절대 불필요

- registrar (hosting.co.kr) 안 건드림
- DNS provider (Cloudflare) 그대로
- Nameserver 안 건드림
- TLS 인증서 자동 (Cloudflare가 처리, Let's Encrypt 백업은 NanoPi 직접 TLS용이지만 우리는 Tunnel 쓰므로 불필요)
