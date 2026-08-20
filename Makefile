.PHONY: up down restart build logs clean test env \
	build-macos build-linux build-linux-server build-windows \
	run-macos package help fmu fmu-clean

# Auto-detect host IP for Mac (en0 = WiFi, en1 = Ethernet)
HOST_IP ?= $(shell ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "")

# Docker development commands
up: env
	docker compose up -d

build: env
	docker compose up --build -d

# Write .env with detected host IP so docker compose picks it up
env:
	@echo "HOST_IP=$(HOST_IP)" > .env

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

# Compile modelica/SiteEnergy.Site4 to an FMI 2.0 co-simulation FMU.
# Requires OpenModelica (`omc`). The simulator loads it via siteplant.Open.
fmu:
	@command -v omc >/dev/null || (echo "omc not found. Install OpenModelica: https://openmodelica.org" && exit 1)
	cd modelica && omc export.mos

fmu-clean:
	cd modelica && rm -f Site4.fmu Site4_*.c Site4_*.h Site4_*.o Site4_*.json \
		Site4_*.libs Site4_*.makefile Site4_*.log Site4.inst.mo index.html \
		Site4_FMU.* 2>/dev/null || true

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
	@# Generate app icon
	@mkdir -p /tmp/appicon.iconset
	@sips -z 16 16 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_16x16.png >/dev/null
	@sips -z 32 32 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_16x16@2x.png >/dev/null
	@sips -z 32 32 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_32x32.png >/dev/null
	@sips -z 64 64 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_32x32@2x.png >/dev/null
	@sips -z 128 128 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_128x128.png >/dev/null
	@sips -z 256 256 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_128x128@2x.png >/dev/null
	@sips -z 256 256 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_256x256.png >/dev/null
	@sips -z 512 512 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_256x256@2x.png >/dev/null
	@sips -z 512 512 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_512x512.png >/dev/null
	@sips -z 1024 1024 cmd/simulator/static/app-icon.png --out /tmp/appicon.iconset/icon_512x512@2x.png >/dev/null
	@iconutil -c icns /tmp/appicon.iconset -o "build/macos/Device Simulator.app/Contents/Resources/appicon.icns"
	@rm -rf /tmp/appicon.iconset
	@printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0"><dict>' \
		'<key>CFBundleExecutable</key><string>device-simulator-wrapper</string>' \
		'<key>CFBundleIconFile</key><string>appicon</string>' \
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
	@echo "  make fmu          - Compile SiteEnergy.Site4 to FMI 2.0 CS (needs omc)"
	@echo "  make package      - Create release packages"
	@echo "  make clean        - Remove build artifacts"
