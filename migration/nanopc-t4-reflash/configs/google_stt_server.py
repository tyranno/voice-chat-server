#!/usr/bin/env python3
"""
Google Cloud Speech-to-Text WebSocket Server v2
Fixed: audio_buffer now only starts from speech onset (drops leading silence)
Fixed: increased silence threshold to 1.2s to avoid cutting speech too early
Fixed: added debug logging for audio levels and buffer sizes
"""

import asyncio
import json
import websockets
import logging
import base64
import requests
import numpy as np
from typing import Optional

# Configuration
import os as _os
GOOGLE_API_KEY = _os.environ.get("GOOGLE_STT_API_KEY")
if not GOOGLE_API_KEY:
    raise SystemExit("GOOGLE_STT_API_KEY env var required")
GOOGLE_STT_URL = "https://speech.googleapis.com/v1/speech:recognize"
PORT = 2700
HOST = "127.0.0.1"

# Audio configuration
SAMPLE_RATE = 16000
SAMPLE_WIDTH = 2  # 16-bit = 2 bytes

# VAD configuration
RMS_THRESHOLD = 400           # Higher = less noise false positives
SILENCE_DURATION_S = 2.0      # Wait 2.0s of silence before processing
SILENCE_SAMPLES = int(SAMPLE_RATE * SILENCE_DURATION_S)
MIN_SPEECH_SAMPLES = int(SAMPLE_RATE * 0.3)  # Need at least 300ms of speech
MAX_AUDIO_SAMPLES = int(SAMPLE_RATE * 15)     # 15s max
PRE_SPEECH_BUFFER_SIZE = int(SAMPLE_RATE * 0.3) * SAMPLE_WIDTH  # Keep 300ms before speech onset

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)


class GoogleSTTServer:
    def __init__(self):
        self.clients = set()

    def calculate_rms(self, audio_data: bytes) -> float:
        if len(audio_data) < 2:
            return 0.0
        samples = np.frombuffer(audio_data, dtype=np.int16)
        return float(np.sqrt(np.mean(samples.astype(np.float32) ** 2)))

    async def call_google_stt(self, audio_data: bytes) -> Optional[str]:
        try:
            audio_base64 = base64.b64encode(audio_data).decode('utf-8')
            duration_s = len(audio_data) / (SAMPLE_RATE * SAMPLE_WIDTH)
            rms = self.calculate_rms(audio_data)
            logger.info(f"Sending to Google STT: {len(audio_data)} bytes ({duration_s:.1f}s), RMS={rms:.0f}")

            payload = {
                "config": {
                    "encoding": "LINEAR16",
                    "sampleRateHertz": SAMPLE_RATE,
                    "languageCode": "ko-KR",
                    "enableAutomaticPunctuation": True,
                    "model": "default"
                },
                "audio": {"content": audio_base64}
            }

            url = f"{GOOGLE_STT_URL}?key={GOOGLE_API_KEY}"
            response = requests.post(url, json=payload, timeout=30)

            if response.status_code == 200:
                result = response.json()
                if 'results' in result and result['results']:
                    alt = result['results'][0].get('alternatives', [])
                    if alt:
                        transcript = alt[0].get('transcript', '').strip()
                        confidence = alt[0].get('confidence', 0.0)
                        logger.info(f"Result: '{transcript}' (conf={confidence:.2f})")
                        return transcript
                logger.info("No speech recognized in audio")
                return ""
            else:
                logger.error(f"API error {response.status_code}: {response.text[:200]}")
                return None
        except Exception as e:
            logger.error(f"STT API error: {e}")
            return None

    async def handle_client(self, websocket):
        logger.info(f"Client connected: {websocket.remote_address}")

        # Ring buffer for pre-speech audio (keeps last 300ms)
        pre_buffer = bytearray()
        # Speech audio buffer (only contains audio from speech onset)
        speech_buffer = bytearray()
        silence_sample_count = 0
        speech_sample_count = 0
        speech_started = False
        paused = False

        try:
            async for message in websocket:
                if isinstance(message, str):
                    try:
                        data = json.loads(message)
                        if data.get("eof"):
                            if speech_started and len(speech_buffer) > MIN_SPEECH_SAMPLES * SAMPLE_WIDTH:
                                logger.info("EOF: processing remaining audio")
                                await websocket.send(json.dumps({"type": "partial", "text": "인식 중..."}))
                                text = await self.call_google_stt(bytes(speech_buffer))
                                await websocket.send(json.dumps({"type": "final", "text": text or ""}))
                                speech_buffer.clear()
                                speech_started = False
                        elif data.get("pause"):
                            paused = True
                            logger.info("Paused (TTS playing)")
                            # Clear buffers to avoid processing stale audio
                            speech_buffer.clear()
                            pre_buffer.clear()
                            speech_started = False
                            silence_sample_count = 0
                            speech_sample_count = 0
                        elif data.get("resume"):
                            paused = False
                            logger.info("Resumed")
                    except json.JSONDecodeError:
                        pass
                    continue

                if not isinstance(message, bytes) or paused:
                    continue

                num_samples = len(message) // SAMPLE_WIDTH
                rms = self.calculate_rms(message)
                is_speech = rms > RMS_THRESHOLD

                if not speech_started:
                    # Not in speech — maintain rolling pre-buffer
                    pre_buffer.extend(message)
                    if len(pre_buffer) > PRE_SPEECH_BUFFER_SIZE:
                        pre_buffer = pre_buffer[-PRE_SPEECH_BUFFER_SIZE:]

                    if is_speech:
                        # Speech onset! Start recording from pre-buffer
                        speech_started = True
                        speech_buffer = bytearray(pre_buffer)  # include pre-speech context
                        speech_buffer.extend(message)
                        speech_sample_count = len(speech_buffer) // SAMPLE_WIDTH
                        silence_sample_count = 0
                        logger.info(f"Speech started (RMS={rms:.0f})")
                else:
                    # In speech — accumulate
                    speech_buffer.extend(message)
                    speech_sample_count += num_samples

                    if is_speech:
                        silence_sample_count = 0
                    else:
                        silence_sample_count += num_samples

                    # Check if we should process
                    should_process = False
                    if silence_sample_count >= SILENCE_SAMPLES:
                        logger.info(f"Silence ({silence_sample_count/SAMPLE_RATE:.1f}s), processing {speech_sample_count/SAMPLE_RATE:.1f}s audio")
                        should_process = True
                    elif speech_sample_count >= MAX_AUDIO_SAMPLES:
                        logger.info(f"Max duration, processing {speech_sample_count/SAMPLE_RATE:.1f}s audio")
                        should_process = True

                    if should_process:
                        if speech_sample_count > MIN_SPEECH_SAMPLES:
                            await websocket.send(json.dumps({"type": "partial", "text": "인식 중..."}))
                            text = await self.call_google_stt(bytes(speech_buffer))
                            await websocket.send(json.dumps({"type": "final", "text": text or ""}))
                        else:
                            logger.info(f"Audio too short ({speech_sample_count/SAMPLE_RATE:.2f}s), skipping")

                        speech_buffer.clear()
                        pre_buffer.clear()
                        speech_started = False
                        silence_sample_count = 0
                        speech_sample_count = 0

        except websockets.exceptions.ConnectionClosed:
            logger.info("Client disconnected")
        except Exception as e:
            logger.error(f"Error: {e}")

    async def start_server(self):
        logger.info(f"Starting Google STT v2 on {HOST}:{PORT}")
        logger.info(f"Config: RMS_THRESHOLD={RMS_THRESHOLD}, SILENCE={SILENCE_DURATION_S}s, MIN_SPEECH=0.3s")

        server = await websockets.serve(
            self.handle_client, HOST, PORT,
            max_size=10_000_000, ping_interval=20, ping_timeout=10
        )
        logger.info(f"Server ready on ws://{HOST}:{PORT}")
        await server.wait_closed()


if __name__ == "__main__":
    try:
        asyncio.run(GoogleSTTServer().start_server())
    except KeyboardInterrupt:
        logger.info("Shutting down")
