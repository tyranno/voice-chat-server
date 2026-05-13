# 백업 매니페스트 (Backup Manifest)

> 작성: 2026-05-13
> 백업 위치: `C:\Users\lab\Downloads\gcp-backup\`
> 총 크기: ~257MB (configs ~60MB + nvm 171MB + voicechat data 14MB)

## 📦 백업 파일 일람

### `gcp-backup-2026-05-13.tar.gz` (42MB, 2658 파일)
**소스**: GCP 서버 `34.64.164.13` (직접 SSH 스트리밍, GCP 디스크 안 씀)
**소유**: 비밀정보 포함 — git 커밋 X, 로컬 PC에만

내용:
- `/home/tyranno/.openclaw/` 전체 (config, workspace, agents, memory, flows, tasks, devices, telegram, qqbot, completions, plugins, openclaw.json + .bak 7개) — **plugin-runtime-deps 4.2GB 제외** (npm 재설치 가능)
- `/home/tyranno/.claude/` + `~/.claude.json` (Claude Code 세션/설정)
- `/home/tyranno/lottery/` (로또/연금 스크립트)
- `/home/tyranno/scripts/` (morning_briefing.mjs, watchlist_scan.mjs)
- `/home/tyranno/.bashrc`
- `/opt/voicechat/.env` ⚠️ 비밀
- `/opt/voicechat/firebase-sa.json` ⚠️ 비밀
- `/opt/voicechat/oc_proxy.py`
- `/opt/voicechat/google_stt_server.py`
- `/opt/voicechat/public/`
- `/etc/systemd/system/{voicechat,openclaw-gateway,oc-proxy,google-stt}.service`
- `/etc/nginx/sites-available/` 전체 (voicechat, openclaw, headscale)
- `/etc/letsencrypt/` 전체 ⚠️ TLS 비밀키 포함

### `crontab-tyranno.txt` (606B)
**소스**: GCP tyranno 사용자 crontab
**내용**: 4개 작업 (로또/연금/아침브리핑/관심종목)
**안전**: git 커밋 OK (스케줄만, 시크릿 없음)
**프로젝트 복사본**: `configs/crontab-tyranno.txt`

### `nanopi-extra/cloudflared-config.tar.gz` (786B)
**소스**: NanoPi 현재 NanoPC-T4 cloudflared 설정
**소유**: ⚠️ Tunnel 자격증명 + 계정 인증서 — git 커밋 X
**내용**:
- `/home/tyranno/.cloudflared/cert.pem` (Cloudflare 계정 인증서)
- `/home/tyranno/.cloudflared/6d00f850-6d23-4469-9fbf-c881e8d4c899.json` (Tunnel 자격증명)
- `/home/tyranno/.cloudflared/config.yml`
- `/etc/cloudflared/config.yml`
**Tunnel UUID**: `6d00f850-6d23-4469-9fbf-c881e8d4c899`

### `nanopi-extra/nanopi-voicechat.service` (393B)
NanoPi 현재 voicechat systemd unit (PrivateTmp=true 적용된 버전)
**프로젝트 복사본**: `configs/voicechat.service`

### `nanopi-extra/nanopi-cloudflared.service` (301B)
NanoPi 현재 cloudflared systemd unit
**프로젝트 복사본**: `configs/cloudflared.service`

### `nanopi-extra/nanopi-env.txt` (478B)
NanoPi의 현재 `/opt/voicechat/.env` 내용 ⚠️ 비밀 — git 커밋 X
**템플릿**: `configs/env.template` (키 이름만)

### `nanopi-extra/nanopi-voicechat-data.tar.gz` (14MB)
**소스**: NanoPi `/opt/voicechat/data/` 전체
**내용**:
- `apk/` (앱 OTA APK + meta.json + version.json)
- `conversations/` (사용자 대화 기록 19개)
- `files/` (업로드된 파일들)
- `cache/`
- `fcm_tokens.json`
- `/opt/voicechat/firebase-sa.json` ⚠️ 비밀

### `nanopi-extra/nanopi-openclaw-latest.tar.gz` (25MB)
**소스**: NanoPi `~/.openclaw/` 최신 상태 (plugin-runtime-deps 제외)
**용도**: GCP 백업 후 NanoPi에서 추가 변경된 openclaw 데이터 (있으면)

### `nanopi-extra/nanopi-nvm.tar.gz` (171MB)
**소스**: NanoPi `~/.nvm/` (Node 16 + 의존성)
**선택**: 24.04 재플래시 후엔 apt로 Node 22 깔면 되니 이 백업 안 써도 됨
**용도**: Ubuntu 20.04 재플래시 같은 경우 시간 절약 (Node 16 즉시 사용 가능)

---

## 🗂️ 프로젝트에 커밋된 파일 (시크릿 없음)

```
voice-chat-server/migration/nanopc-t4-reflash/
├── README.md                    # 전체 재플래시 가이드
├── CLOUDFLARE.md                # Cloudflare 처리 가이드
├── BACKUP-MANIFEST.md           # 이 파일
├── .gitignore                   # 시크릿 백업 제외
├── scripts/
│   └── post-install.sh          # 재플래시 후 자동 복원 스크립트
└── configs/
    ├── voicechat.service        # systemd unit (NanoPi 버전, PrivateTmp 포함)
    ├── cloudflared.service      # systemd unit
    ├── openclaw-gateway.service # GCP에서 가져옴
    ├── oc-proxy.service         # GCP에서 가져옴
    ├── google-stt.service       # GCP에서 가져옴
    ├── env.template             # .env 키 이름 (값 X)
    ├── crontab-tyranno.txt      # cron 스케줄
    └── nginx-sites/
        ├── voicechat            # GCP nginx (참고용, Tunnel 쓰면 불필요)
        ├── openclaw             # GCP nginx
        └── headscale            # GCP nginx
```

---

## 🔐 시크릿 분리 정책

### Git 커밋 OK (안전)
- README/docs (이 파일들)
- systemd unit 파일 (EnvironmentFile= 참조만, 값 X)
- env.template (키 이름만)
- crontab (스케줄)
- nginx config (호스트/포트만, 인증서 경로만)
- post-install.sh (스크립트 로직)

### Git 커밋 절대 X (로컬 + 클라우드 백업만)
- `.env` (BRIDGE_TOKEN, GOOGLE_TTS_API_KEY 등)
- `firebase-sa.json` (Firebase service account)
- `*.pem` (TLS 키, cloudflared cert)
- `<UUID>.json` (cloudflared tunnel 자격증명)
- `openclaw.json` (API 토큰 가능성)
- `.claude.json` (Claude Code 세션 토큰)
- conversations/ (사용자 데이터)

---

## 🔄 추가 안전망 (강력 추천)

PC 외 한 곳 더 백업:
- **Google Drive**: `gcp-backup` 폴더 통째로 업로드
- **OneDrive / iCloud**: 같음
- **USB 메모리**: 256MB짜리도 충분 (전체 ~257MB)
- **외장 HDD/SSD**: 영구 보관

PC 디스크 죽으면 모든 게 날아가니까 **꼭 한 곳 더** 저장.

---

## 📝 백업 검증 명령어

```powershell
# PC에서 백업 무결성 확인
cd C:\Users\lab\Downloads\gcp-backup
Get-ChildItem -Recurse | Format-Table Name, Length, LastWriteTime
# 또는 git bash:
ls -la **/*.tar.gz 2>/dev/null
```

```bash
# 각 tarball 안의 파일 수 확인
tar tzf gcp-backup-2026-05-13.tar.gz | wc -l       # 예상: 2658
tar tzf nanopi-extra/nanopi-voicechat-data.tar.gz | wc -l   # 예상: 100
tar tzf nanopi-extra/nanopi-openclaw-latest.tar.gz | wc -l  # ~ 수백 개
tar tzf nanopi-extra/cloudflared-config.tar.gz | wc -l       # 5개
```

수치가 맞으면 백업 OK. 재플래시 진행 가능.
