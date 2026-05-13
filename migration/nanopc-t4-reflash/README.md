# NanoPC-T4 OS 재플래시 가이드

> 작성: 2026-05-13
> 대상: NanoPi M4 시리즈 (NanoPC-T4, NanoPi M4, NanoPi NEO4 — RK3399 보드 공통)
> 현재 OS: Ubuntu 18.04 (FriendlyArm 커스텀, 커널 4.4.179)
> 목표 OS: **Ubuntu 24.04 Noble core** (커널 6.6.y, glibc 2.39) 또는 Debian 13 Trixie core

---

## 📋 왜 재플래시?

| 문제 | 현재 (Ubuntu 18.04) | 재플래시 후 (Ubuntu 24.04) |
|---|---|---|
| openclaw 실행 (Node ≥22.14) | ❌ glibc 2.27 한계 → Docker 격리만 가능 | ✅ Node 22 네이티브 (`apt install nodejs`) |
| 음성 STT (VOSK) | ❌ Python 3.6, 신버전 vosk 호환 안 됨 | ✅ Python 3.12, VOSK 모든 버전 |
| SD 슬롯 mmc 드라이버 | ❌ 4.4 커널 (-110 timeout) | ❓ 6.6 커널 (가능성 있음) |
| 보안 패치 | ❌ EOL 2023년 | ✅ 2029년까지 |
| 디스크 여유 | 1.6GB (Docker 깐 후) | 새로 시작 (~6GB 여유) |

---

## ⚠️ 위험 요소

- **eMMC 재플래시 = 모든 데이터 삭제**. 우리 백업이 유일한 안전망.
- 잘못 플래시하면 부팅 안 됨 → 집에 가서 다시 시도 필요 (원격 복구 불가)
- 작업 시간 2~4시간 (집중)
- SD 슬롯 죽었으니 **USB 메모리만 사용 가능**

---

## 🗂️ 백업 위치 (PC)

`C:\Users\lab\Downloads\gcp-backup\`

```
gcp-backup/
├── gcp-backup-2026-05-13.tar.gz       (42MB) — GCP 전체 핵심 데이터
├── crontab-tyranno.txt                (606B) — cron 4개 작업
└── nanopi-extra/
    ├── cloudflared-config.tar.gz       (786B) — Tunnel UUID + 인증서 + config.yml ⭐
    ├── nanopi-cloudflared.service      (301B) — systemd unit
    ├── nanopi-voicechat.service        (393B) — systemd unit
    ├── nanopi-env.txt                  (478B) — 현재 .env 내용
    ├── nanopi-voicechat-data.tar.gz    (14MB) — /opt/voicechat/data (apk, conversations, files)
    ├── nanopi-openclaw-latest.tar.gz   (25MB) — NanoPi의 최신 .openclaw 상태
    └── nanopi-nvm.tar.gz               (171MB) — Node 16 nvm install (선택, 24.04엔 apt로 깔면 됨)
```

**⚠️ 비밀정보 포함**:
- `.env` (BRIDGE_TOKEN, GOOGLE_TTS_API_KEY 등)
- `firebase-sa.json` (Firebase service account)
- TLS 인증서 (privkey.pem)
- cloudflared cert.pem + tunnel UUID json
- openclaw.json (API tokens 가능)

이 파일들은 **로컬 PC에만 보관, git에 커밋 X**. 추가 안전망: 클라우드 (Google Drive, OneDrive) 또는 USB에 한 부 더 저장.

---

## 📦 Cloudflare 처리 — 중요!

### 보존되는 것 (재플래시 후에도 그대로)
- ✅ **Cloudflare 계정**: 너가 로그인하는 거 (변동 없음)
- ✅ **도메인 `tyranno.xyz`**: Cloudflare DNS에 등록됨, 그대로 유지
- ✅ **DNS 레코드**: `voicechat.tyranno.xyz` CNAME → `<tunnel UUID>.cfargotunnel.com` (변동 없음)
- ✅ **Tunnel UUID**: `6d00f850-6d23-4469-9fbf-c881e8d4c899` — 우리가 `cloudflared-config.tar.gz`에 백업함

### NanoPi에서 사라지는 것 (재플래시 시)
- ❌ cloudflared 바이너리 (`/usr/local/bin/cloudflared`)
- ❌ cloudflared 설정 (`/home/tyranno/.cloudflared/`)
- ❌ cloudflared systemd 서비스

### 복원 방법 (재플래시 후)
1. 새 OS 부팅 후 → cloudflared 재설치
2. **백업한 `~/.cloudflared/` 복원** (같은 UUID = 같은 터널)
3. systemd 등록
4. `cloudflared tunnel run voicechat`
5. → Cloudflare가 자동으로 인식 (UUID 같음), DNS 변경 불필요, 즉시 서비스 복구

**Cloudflare 대시보드는 절대 건드릴 필요 없음** — Tunnel UUID 살아있으면 다 그대로 됨.

---

## 🚀 작업 순서 (단계별)

### Phase 0: 사전 준비 (PC에서, 안전한 환경)

- [ ] **백업 검증**: `C:\Users\lab\Downloads\gcp-backup\` 안의 모든 파일 존재 확인
- [ ] **백업 추가 사본**: 클라우드 또는 다른 USB에 한 번 더 저장 (안전)
- [ ] **USB 메모리 준비**: 8GB+ (이미지 굽기용)
- [ ] **다운로드 도구**: balenaEtcher 또는 Rufus
- [ ] **OS 이미지 다운로드**:
  - `rk3399-usb-ubuntu-noble-core-arm64-*.zip` (Ubuntu 24.04, 591.7MB)
  - 또는 `rk3399-usb-debian-trixie-core-arm64-*.zip` (Debian 13, 676.6MB)
  - 위치: [구글 드라이브 03_USB upgrade images](https://drive.google.com/drive/folders/1iOO3ZQ8qWCsEoMw6V82BZ4QEZCp1DGuC)

### Phase 1: GCP STOP (전 단계)

- [ ] **GCP 콘솔에서 VM 인스턴스 STOP** (오늘이 무료 트라이얼 종료일이면 즉시 청구 차단)
- [ ] (선택) 스냅샷 한 번 찍어두기
- [ ] DNS는 이미 Cloudflare Tunnel 통하니까 GCP 꺼져도 서비스 영향 없음

### Phase 2: USB 이미지 굽기 (PC)

- [ ] 다운받은 `.zip` 압축 풀기 → `.img` 파일
- [ ] balenaEtcher 실행
- [ ] `.img` 선택 → USB 메모리 선택 → Flash
- [ ] 완료까지 ~5분

### Phase 3: NanoPC-T4 재플래시 (집에서, 물리적 작업)

- [ ] NanoPC-T4 전원 끄기
- [ ] 모든 USB 장치 제거 (혹시 다른 USB)
- [ ] 이미지 USB만 꽂기
- [ ] 전원 켜기 → 자동으로 USB 부팅 (FriendlyELEC eflasher 화면 뜸)
- [ ] eflasher GUI에서 "Install to eMMC" 또는 "OK" 누르기
- [ ] 설치 진행 (~5~15분)
- [ ] 완료 알림 후 → 전원 끄기 → USB 빼기 → 전원 켜기
- [ ] 이제 eMMC의 새 OS로 부팅
- [ ] 기본 로그인 (FriendlyELEC 이미지: `pi` / `pi` 또는 `root` / `fa` — 이미지마다 다름)

### Phase 4: 기본 네트워크/SSH 설정 (NanoPC-T4 콘솔, HDMI/키보드 필요할 수도)

- [ ] 네트워크 확인 (DHCP로 IP 자동 할당 — 192.168.123.x)
- [ ] SSH 활성화 (`systemctl enable --now ssh`)
- [ ] 새 사용자 `tyranno` 생성 + 비밀번호 `1234`
  ```bash
  sudo adduser tyranno
  sudo usermod -aG sudo tyranno
  ```
- [ ] sudo 비번 없이 (선택): `echo 'tyranno ALL=(ALL) NOPASSWD:ALL' | sudo tee /etc/sudoers.d/tyranno`

### Phase 5: SSH 키 등록 (PC에서)

- [ ] PC에서: `ssh-copy-id tyranno@192.168.123.<IP>`
- [ ] 한 번 비번 `1234` 입력
- [ ] 이후 비번 없이 접속 가능
- [ ] (선택) password 인증 비활성화: 서버에서 `PasswordAuthentication no`

### Phase 6: 핵심 도구 설치 (NanoPC-T4 SSH로)

자동화 스크립트 사용: `scripts/post-install.sh` 참고

수동 단계:
- [ ] apt 갱신: `sudo apt update && sudo apt upgrade -y`
- [ ] 필수 도구: `sudo apt install -y git ffmpeg python3-pip jq curl wget unzip`
- [ ] Node.js 22 (24.04 기본 저장소에 있음): `sudo apt install -y nodejs npm` (버전 확인 `node --version` → ≥18, 더 신버전 원하면 NodeSource 사용)
- [ ] yt-dlp 정적 바이너리: `sudo curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux_aarch64 -o /usr/local/bin/yt-dlp && sudo chmod +x /usr/local/bin/yt-dlp`
- [ ] deno (yt-dlp JS 런타임): `sudo curl -L https://github.com/denoland/deno/releases/latest/download/deno-aarch64-unknown-linux-gnu.zip -o /tmp/deno.zip && sudo unzip -o /tmp/deno.zip -d /usr/local/bin/`
- [ ] cloudflared: `sudo curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64 -o /usr/local/bin/cloudflared && sudo chmod +x /usr/local/bin/cloudflared`

### Phase 7: 백업 복원 (PC → NanoPC-T4)

- [ ] PC에서 NanoPi로 백업 전송:
  ```bash
  scp -r C:/Users/lab/Downloads/gcp-backup/ tyranno@192.168.123.<IP>:~/
  ```
- [ ] NanoPi에서 복원:
  ```bash
  cd ~
  # GCP 백업 (configs, scripts, etc)
  sudo tar xzf gcp-backup/gcp-backup-2026-05-13.tar.gz -C /
  # NanoPi 추가 데이터
  sudo tar xzf gcp-backup/nanopi-extra/cloudflared-config.tar.gz -C /
  sudo tar xzf gcp-backup/nanopi-extra/nanopi-voicechat-data.tar.gz -C /
  sudo tar xzf gcp-backup/nanopi-extra/nanopi-openclaw-latest.tar.gz -C /
  # 권한 설정
  sudo chown -R tyranno:tyranno /home/tyranno
  sudo chmod 600 /opt/voicechat/.env /opt/voicechat/firebase-sa.json
  # crontab 복원
  crontab gcp-backup/crontab-tyranno.txt
  ```

### Phase 8: voicechat-server 배포

- [ ] PC에서: `scp voice-chat-server/voicechat-server-linux-arm64 tyranno@<IP>:/tmp/`
- [ ] NanoPi에서: `sudo mv /tmp/voicechat-server-linux-arm64 /opt/voicechat/voicechat-server && sudo chmod +x /opt/voicechat/voicechat-server`
- [ ] systemd unit 활성화 (`migration/nanopc-t4-reflash/configs/voicechat.service` 참고):
  ```bash
  sudo cp ~/gcp-backup/etc/systemd/system/voicechat.service /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable --now voicechat
  ```

### Phase 9: cloudflared 등록 + Tunnel 복원

- [ ] cloudflared 설정 복원 (백업 이미 풀음 — `/home/tyranno/.cloudflared/` + `/etc/cloudflared/`)
- [ ] systemd 등록: `sudo cloudflared service install` (이미 systemd unit이 있을 수 있음 — 백업에서 복원됨)
- [ ] 시작: `sudo systemctl enable --now cloudflared`
- [ ] 검증: `curl https://voicechat.tyranno.xyz/health` (외부에서 PC로) → `{"status":"ok"}` 나와야 함

### Phase 10: openclaw 설치 (네이티브!)

24.04에서는 Node 22 네이티브 동작:
- [ ] `sudo npm install -g openclaw`
- [ ] openclaw 실행 (백업된 ~/.openclaw 자동 사용)
- [ ] systemd 등록 (백업의 `openclaw-gateway.service` 참고)

### Phase 11: VOSK STT (선택)

- [ ] `pip3 install vosk`
- [ ] 한국어 모델: `wget https://alphacephei.com/vosk/models/vosk-model-small-ko-0.22.zip && unzip vosk-model-small-ko-0.22.zip -d ~/vosk-model-ko`
- [ ] `google_stt_server.py` 또는 vosk 서버 실행
- [ ] `.env`의 `VOSK_URL` 갱신

### Phase 12: 전체 검증

- [ ] **NanoPi 서비스**: `systemctl is-active voicechat cloudflared openclaw-gateway` (모두 active)
- [ ] **외부 HTTP**: `curl https://voicechat.tyranno.xyz/health` → OK
- [ ] **외부 SSH**: `ssh nanopi` (다른 PC에서) — Cloudflare Tunnel 통해 접속
- [ ] **음악 검색**: `curl 'https://voicechat.tyranno.xyz/api/youtube/search?q=test'` → JSON 결과
- [ ] **음악 재생**: `curl -I https://voicechat.tyranno.xyz/api/youtube/proxy?videoId=dQw4w9WgXcQ` → 200/206 + Content-Type audio/mp4
- [ ] **폰 앱**: 검색/재생/다운로드/채팅/STT/TTS 다 동작
- [ ] **Ralph (openclaw)**: 음성 명령으로 자율 작업 → 응답
- [ ] **cron 작업**: 다음 실행 시 정상 (로또/연금/아침브리핑/관심종목)
- [ ] **FCM 푸시**: 폰에 알림 도달

---

## 🔧 트러블슈팅

### 부팅 실패 (eflasher 화면 안 뜸)
- USB 다른 포트에 꽂아보기 (USB3 파란색 권장)
- 다른 USB 메모리로 시도 (일부 USB는 호환성 문제)
- 이미지 다시 굽기 (체크섬 확인)
- HDMI 모니터로 부팅 메시지 확인

### Cloudflare Tunnel 안 됨
- `/home/tyranno/.cloudflared/<UUID>.json` 존재 확인
- `cloudflared tunnel info voicechat` → 상태 확인
- 만약 UUID 인식 안 되면 → `cloudflared tunnel route dns voicechat voicechat.tyranno.xyz` 다시 실행
- 최후의 수단: 새 터널 생성 + DNS 수동 변경

### openclaw 실행 실패
- Node 버전 확인 (≥22.14)
- `~/.openclaw/openclaw.json` 권한 (600, owner tyranno)
- 로그: `journalctl -u openclaw-gateway -f`

### 음악 다운로드 webm으로 나옴
- deno 설치 확인 (`/usr/local/bin/deno`)
- voicechat-server 코드의 `--js-runtimes` 인자 확인 (있으면 잘못된 경로일 수 있음)

---

## 📚 참고 문서

- [FriendlyELEC sd-fuse_rk3399 (kernel-6.6.y)](https://github.com/friendlyarm/sd-fuse_rk3399/tree/kernel-6.6.y)
- [FriendlyELEC NanoPC-T4 Wiki](https://wiki.friendlyelec.com/wiki/index.php/NanoPC-T4)
- [Google Drive USB upgrade images](https://drive.google.com/drive/folders/1iOO3ZQ8qWCsEoMw6V82BZ4QEZCp1DGuC)
- [Armbian for NanoPi M4 (RK3399 대안)](https://www.armbian.com/nanopi-m4/)

---

## 🆘 응급 복구

만약 재플래시 후 뭔가 망가지고 NanoPi 접근 불가:
1. 다시 USB 이미지 굽기
2. Phase 3부터 다시
3. 백업은 PC에 그대로 있으니 데이터 손실 없음
4. 단지 시간 1~2시간 더 소요

만약 NanoPi 자체가 죽으면 (재플래시 중 전원 끊김 등):
1. 다른 SBC 구매 (NanoPi M6, Pi 5 등) 또는 NanoPi M4V2 신품
2. 백업으로 데이터 모두 복원 가능
3. 도메인/Cloudflare 그대로 → 새 보드에 cloudflared 설정만 복원
