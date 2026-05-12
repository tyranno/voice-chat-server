# VoiceChat Server — NanoPi 자가호스팅 마이그레이션

## 전체 흐름

1. NanoPi M4에 Linux (Armbian/Ubuntu) 설치 (이미 있으면 skip)
2. SSH 접속 가능한 상태 확인
3. 마이그레이션 패키지 NanoPi에 복사
4. setup 스크립트 실행 (의존성 자동 설치 + 바이너리 배포 + systemd 등록)
5. .env / data 디렉토리 GCP에서 옮김
6. Cloudflare Tunnel 설정 (포트 개방 불필요)
7. DNS 전환
8. GCP 중지

## 1단계: NanoPi 준비

OS 없으면 Armbian 추천 (Ubuntu 기반, ARM64 SBC에 최적화):
- https://www.armbian.com/nanopi-m4/
- microSD 8GB+ 권장
- 이미지 굽기: balenaEtcher 또는 Rufus

부팅 후 첫 ssh:
```
ssh <user>@<NanoPi IP>
sudo apt update && sudo apt upgrade -y
```

## 2단계: 마이그레이션 패키지 NanoPi에 복사

PC에서 (Windows / PowerShell):
```
scp -r migration/ <user>@<NanoPi IP>:~/voicechat-migration/
scp ../voicechat-server-linux-arm64 <user>@<NanoPi IP>:~/voicechat-migration/
```

## 3단계: setup 실행

NanoPi에서:
```
cd ~/voicechat-migration
chmod +x nanopi-setup.sh
bash nanopi-setup.sh
```

자동 설치되는 것:
- python3, ffmpeg, ufw, jq
- yt-dlp (최신 ARM64 release)
- cloudflared
- 서비스 user `voicechat`
- `/opt/voicechat/` 구조
- systemd unit (자동 재시작)

## 4단계: .env / data 복사

GCP 서버에서 NanoPi로 데이터 이전. 본인이 직접 (보안상 자동 안 함).

GCP에서 받기:
```
# GCP에 SSH
ssh -i ~/.ssh/voicechat-key tyranno@voicechat.tyranno.xyz
sudo tar czf /tmp/voicechat-data.tar.gz \
    /opt/voicechat/.env \
    /opt/voicechat/data \
    /opt/voicechat/firebase-sa.json
sudo chown $(whoami) /tmp/voicechat-data.tar.gz

# 로컬로:
scp -i ~/.ssh/voicechat-key tyranno@voicechat.tyranno.xyz:/tmp/voicechat-data.tar.gz ./
```

NanoPi로 전송:
```
scp voicechat-data.tar.gz <user>@<NanoPi IP>:~/
ssh <user>@<NanoPi IP>
sudo tar xzf ~/voicechat-data.tar.gz -C /
sudo chown -R voicechat:voicechat /opt/voicechat/data /opt/voicechat/.env /opt/voicechat/firebase-sa.json
sudo chmod 600 /opt/voicechat/.env
```

## 5단계: Cloudflare Tunnel 설정

`cloudflare-tunnel.md` 참고. 핵심:

```
cloudflared tunnel login          # 브라우저 인증
cloudflared tunnel create voicechat
cloudflared tunnel route dns voicechat voicechat.tyranno.xyz
```

config.yml 작성 후:
```
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

## 6단계: 서비스 시작 + 검증

```
sudo systemctl start voicechat
sudo systemctl status voicechat
journalctl -u voicechat -f
# 다른 터미널:
curl http://localhost:8090/api/youtube/search?q=test    # 내부
curl https://voicechat.tyranno.xyz/api/youtube/search?q=test  # 외부 (Cloudflare 통해)
```

## 7단계: GCP 중지

NanoPi에서 모든 기능 검증 OK 후:
```
# GCP 콘솔에서 voicechat 인스턴스 stop
# 며칠 모니터링 후 삭제
```

도메인 DNS는 이미 Cloudflare Tunnel 가리키므로 폰 앱 무수정.

## 트러블슈팅

**APK 업데이트 안 됨**: APK 파일을 `/opt/voicechat/data/apk/`에 복사했는지 확인. `app-debug.apk`, `meta.json`, `version.json` 모두 필요.

**Cloudflare Tunnel 연결 안 됨**: `cloudflared tunnel info voicechat`로 상태 확인. 방화벽 OUTBOUND 443 차단되어 있지 않은지.

**음악 재생 안 됨**: yt-dlp 버전 최신인지 (`yt-dlp -U`로 업데이트). `journalctl -u voicechat -n 100`로 에러 확인.

**메모리 부족 (2GB 모델)**: 동시 yt-dlp 호출 많을 때. 보통 1명 사용에선 문제 없음. swap 추가 가능.
