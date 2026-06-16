# Contributing to Device Simulator

Thanks for your interest in contributing! This is a multi-protocol device
simulator for testing solar inverters, batteries, EV chargers, and energy
systems. Contributions of bug fixes, new device profiles, and improvements are
welcome.

## Getting started

```bash
# Run the server (Docker)
docker compose up --build      # dashboard at http://localhost

# Run tests
go test ./...

# Build and run the desktop app (macOS)
make build-macos && make run-macos
```

See the [README](README.md) and [CLAUDE.md](CLAUDE.md) for architecture and
build details.

## Development workflow

1. **Open an issue first** for anything beyond a trivial fix, so we can agree on
   the approach.
2. **Branch** from `main` using `{issue-number}-short-description`
   (e.g. `45-add-mqtt-handler`).
3. **Keep PRs focused** — one logical change per pull request.
4. **Add tests** for new code. Tests must pass before a PR is merged.
5. **Link the issue** in your PR description.

## Adding a device profile

New device profiles are very welcome. Add a JSON file under `profiles/` or an
entry to `EmbeddedProfiles` in `internal/profiles/loader.go`. See the README
section "Adding New Device Profiles" for the format.

## API changes

Whenever an HTTP API endpoint is added, removed, or modified, update all of:

- `cmd/simulator/static/openapi.json` (OpenAPI spec)
- `internal/mcp/server.go` (MCP tool mapping)
- `cmd/simulator/templates/docs.html` (in-app `/docs`)

## Commit messages & releases

Releases are triggered automatically by commit-message prefixes on `main`:

| Prefix   | Version bump        |
|----------|---------------------|
| `patch:` | 0.1.X → 0.1.(X+1)   |
| `minor:` | 0.X.0 → 0.(X+1).0   |
| `major:` | X.0.0 → (X+1).0.0   |

Commits without a prefix do not create a release. The commit body becomes the
release notes.

## Code style

- Run `go vet ./...` and `gofmt` before committing.
- Match the style and conventions of the surrounding code.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).
