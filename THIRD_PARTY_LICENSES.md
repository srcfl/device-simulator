# Third-Party Licenses

Device Simulator is distributed under the MIT License (see [LICENSE](LICENSE)).
It bundles and depends on third-party software covered by their own licenses,
acknowledged below.

## Bundled front-end assets

These files are committed into this repository and shipped inside the binary.

| Component | Location | License | Source |
|-----------|----------|---------|--------|
| Lucide icons | `cmd/simulator/static/icons/` | ISC | https://github.com/lucide-icons/lucide |
| Swagger UI | `cmd/simulator/static/vendor/swagger-ui/` | Apache-2.0 | https://github.com/swagger-api/swagger-ui |
| Alpine.js | `cmd/simulator/static/js/alpine.min.js` | MIT | https://github.com/alpinejs/alpine |
| JetBrains Mono | `cmd/simulator/static/fonts/jetbrains-mono-*.woff2` | SIL Open Font License 1.1 | https://github.com/JetBrains/JetBrainsMono |
| Manrope | `cmd/simulator/static/fonts/manrope-*.woff2` | SIL Open Font License 1.1 | https://github.com/sharanda/manrope |

The full Manrope license text is included at
`cmd/simulator/static/fonts/Manrope-OFL.txt`.

## Go module dependencies

Direct dependencies declared in `go.mod`. Full transitive license information is
available via `go mod download` / https://pkg.go.dev.

| Module | License |
|--------|---------|
| github.com/wailsapp/wails/v2 | MIT |
| github.com/mochi-mqtt/server/v2 | MIT |
| github.com/gorilla/websocket | BSD-2-Clause |
| github.com/google/go-github/v50 | BSD-3-Clause |
| github.com/blang/semver | MIT |
