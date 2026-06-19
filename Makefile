.DEFAULT_GOAL := up

webui:
	docker run -d \
	--name open-webui \
	--restart always \
	-p 3000:8080 \
	--add-host=host.docker.internal:host-gateway \
	-v open-webui:/app/backend/data \
	ghcr.io/open-webui/open-webui:main
.PHONY: webui

tts:
	docker run -d \
  	--name openedai-speech \
  	--restart always \
  	-p 8000:8000 \
  	-v ~/voice_asistent/openedai-speech/voices:/app/voices \
  	-v ~/voice_asistent/openedai-speech/config/voice_to_speaker.yaml:/app/config/voice_to_speaker.yaml \
  	ghcr.io/matatonic/openedai-speech-min:latest
.PHONY: tts

up:
	docker compose -f docker/docker-compose.yaml up --build --pull missing
.PHONY: up
