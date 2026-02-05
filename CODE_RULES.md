# Codex Rules (Project)

- When any HTTP API endpoint is added, removed, or modified, update all three in the same change:
  - `cmd/simulator/static/openapi.json`
  - `internal/mcp/server.go` (MCP tool mapping)
  - `/docs` content in `cmd/simulator/templates/docs.html`
