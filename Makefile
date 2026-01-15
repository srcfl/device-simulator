.PHONY: up down restart build logs

# Auto-detect host IP for Mac (en0 = WiFi, en1 = Ethernet)
HOST_IP ?= $(shell ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "")

up:
	HOST_IP=$(HOST_IP) docker compose up -d

build:
	HOST_IP=$(HOST_IP) docker compose up --build -d

down:
	docker compose down

restart:
	docker compose restart

logs:
	docker compose logs -f
