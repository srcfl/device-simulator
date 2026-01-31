.PHONY: up down restart build logs clean test \
	build-macos build-linux build-linux-server build-windows \
	run-macos package help

# Auto-detect host IP for Mac (en0 = WiFi, en1 = Ethernet)
HOST_IP ?= $(shell ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "")

# Docker development commands
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

# Desktop app builds
clean:
	rm -rf build/macos build/linux build/windows

test:
	go test ./...

# Build macOS desktop app (native, requires macOS)
build-macos:
	@echo "Building macOS desktop app..."
	@mkdir -p "build/macos/Device Simulator.app/Contents/MacOS"
	@mkdir -p "build/macos/Device Simulator.app/Contents/Resources"
	CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
		go build -tags "desktop,production" -ldflags "-s -w" \
		-o "build/macos/Device Simulator.app/Contents/MacOS/device-simulator" \
		./cmd/simulator/
	@printf '#!/bin/bash\nDIR="$$(cd "$$(dirname "$$0")" && pwd)"\nexec "$$DIR/device-simulator" --desktop "$$@"\n' \
		> "build/macos/Device Simulator.app/Contents/MacOS/device-simulator-wrapper"
	@chmod +x "build/macos/Device Simulator.app/Contents/MacOS/device-simulator-wrapper"
	@printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0"><dict>' \
		'<key>CFBundleExecutable</key><string>device-simulator-wrapper</string>' \
		'<key>CFBundleIdentifier</key><string>io.srcful.device-simulator</string>' \
		'<key>CFBundleName</key><string>Device Simulator</string>' \
		'<key>CFBundlePackageType</key><string>APPL</string>' \
		'<key>CFBundleShortVersionString</key><string>1.0.0</string>' \
		'<key>LSMinimumSystemVersion</key><string>10.15</string>' \
		'<key>NSHighResolutionCapable</key><true/>' \
		'</dict></plist>' > "build/macos/Device Simulator.app/Contents/Info.plist"
	@echo "Built: build/macos/Device Simulator.app"

# Build Linux desktop app (via Docker)
build-linux:
	@echo "Building Linux desktop app via Docker..."
	@mkdir -p build/linux
	docker build -f Dockerfile.desktop-linux --output build/linux .
	@echo "Built: build/linux/device-simulator"

# Build Linux server-only (no GUI, cross-compile from any OS)
build-linux-server:
	@echo "Building Linux server binary..."
	@mkdir -p build/linux
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" \
		-o build/linux/device-simulator-server ./cmd/simulator/
	@echo "Built: build/linux/device-simulator-server"

# Build Windows server-only (no GUI, cross-compile from any OS)
build-windows-server:
	@echo "Building Windows server binary..."
	@mkdir -p build/windows
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" \
		-o build/windows/device-simulator-server.exe ./cmd/simulator/
	@echo "Built: build/windows/device-simulator-server.exe"

# Run the macOS app
run-macos: build-macos
	open "build/macos/Device Simulator.app"

# Package all for release
package: build-macos build-linux
	@echo "Packaging releases..."
	cd build/macos && zip -r ../Device-Simulator-macos.zip "Device Simulator.app"
	cd build && tar -czvf Device-Simulator-linux-amd64.tar.gz linux/
	@echo "Packages created in build/"

help:
	@echo "Device Simulator - Build Targets"
	@echo ""
	@echo "Docker Development:"
	@echo "  make up           - Start Docker containers"
	@echo "  make build        - Build and start Docker containers"
	@echo "  make down         - Stop Docker containers"
	@echo "  make logs         - Follow Docker logs"
	@echo ""
	@echo "Desktop Builds:"
	@echo "  make build-macos  - Build macOS desktop app"
	@echo "  make build-linux  - Build Linux desktop app (via Docker)"
	@echo "  make run-macos    - Build and run macOS app"
	@echo ""
	@echo "Server-only Builds (no GUI, cross-compile):"
	@echo "  make build-linux-server   - Build Linux server binary"
	@echo "  make build-windows-server - Build Windows server binary"
	@echo ""
	@echo "Other:"
	@echo "  make test         - Run Go tests"
	@echo "  make package      - Create release packages"
	@echo "  make clean        - Remove build artifacts"
