<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/License-MIT-22c55e?style=for-the-badge" alt="License">
  <img src="https://img.shields.io/github/stars/kittors/CliRelay?style=for-the-badge&color=f59e0b" alt="Stars">
  <img src="https://img.shields.io/github/forks/kittors/CliRelay?style=for-the-badge&color=8b5cf6" alt="Forks">
</p>

<h1 align="center">🔀 CliRelay</h1>

<p align="center">
  <strong>A unified proxy server for AI CLI tools — use your <em>existing</em> subscriptions with any OpenAI / Gemini / Claude / Codex compatible client.</strong>
</p>

<p align="center">
  English | <a href="README_CN.md">中文</a>
</p>

<p align="center">
  <a href="https://help.router-for.me/">📖 Docs</a> ·
  <a href="https://github.com/kittors/codeProxy">🖥️ Management Panel</a> ·
  <a href="https://github.com/kittors/CliRelay/issues">🐛 Report Bug</a> ·
  <a href="https://github.com/kittors/CliRelay/pulls">✨ Request Feature</a>
</p>

---

## ⚡ What is CliRelay?

> **✨ Heavily enhanced fork of the [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) project** — rebuilt with a production-grade management layer, web control panel hosting, and a terminal TUI for day-2 operations.

CliRelay lets you **proxy requests** from AI coding tools and compatible API clients (Claude Code, Gemini CLI, OpenAI Codex, Amp CLI, OpenAI-compatible clients, etc.) through a single unified endpoint. Authenticate with OAuth, API keys, cookies, or a mix of them, and CliRelay handles routing, failover, usage logging, `/manage` web hosting, and terminal management workflows automatically.

```
┌───────────────────────┐         ┌──────────────┐         ┌────────────────────┐
│   AI Coding Tools     │         │              │         │  Upstream Providers │
│                       │         │              │ ──────▶ │  Google Gemini      │
│  Claude Code          │ ──────▶ │   CliRelay   │ ──────▶ │  OpenAI / Codex    │
│  Gemini CLI           │         │   :8317      │ ──────▶ │  Anthropic Claude  │
│  OpenAI Codex         │         │              │ ──────▶ │  Qwen / iFlow      │
│  Amp CLI / IDE        │         │              │ ──────▶ │  Antigravity       │
│  Any OAI-compatible   │         └──────────────┘         │  Vertex / OpenAI   │
└───────────────────────┘                                  │  iFlow / Qwen /    │
                                                           │  Kimi / Claude     │
                                                           └────────────────────┘
```

## ✨ Key Features

### 🔌 Multi-Provider Proxy Engine

| Feature | Description |
|:--------|:------------|
| 🌐 **Unified Endpoint** | One `http://localhost:8317` fronts Gemini, Claude, Codex, Qwen, iFlow, Antigravity, Vertex-compatible endpoints, OpenAI-compatible upstreams, and Amp integration |
| ⚖️ **Smart Load Balancing** | Round-robin or fill-first scheduling across multiple API keys for the same provider |
| 🔄 **Auto Failover** | Automatically switches to backup channels when quotas are exhausted or errors occur |
| 🧠 **Multimodal Support** | Full support for text + image inputs, function calling (tools), and streaming SSE responses |
| 🔗 **OpenAI-Compatible** | Works with any upstream that speaks the OpenAI Chat Completions protocol |

### 📊 Request Logging & Monitoring (SQLite)

| Feature | Description |
|:--------|:------------|
| 📝 **Full Request Capture** | Every API request is logged to SQLite with timestamp, model, tokens (in/out/reasoning/cache), latency, status, and source channel |
| 💬 **Message Body Storage** | Full request/response message content captured in compressed SQLite storage, with separate retention for content vs. metadata |
| 🔍 **Advanced Querying** | Filter logs by API Key, model, status, time range with efficient pagination (LIMIT/OFFSET) |
| 📈 **Analytics Aggregation** | Pre-computed dashboards: daily trends, model distribution, hourly heatmaps, per-key statistics |
| 🏥 **Health Score Engine** | Real-time 0–100 health score considering success rate, latency, active channels, and error patterns |
| 📡 **WebSocket Monitoring** | Live system stats streamed via WebSocket: CPU, memory, goroutines, network I/O, DB size |
| 🗄️ **No-CGO SQLite** | Uses `modernc.org/sqlite` — pure Go, no CGO dependency, easy cross-compilation |

### 🔐 API Key & Access Management

| Feature | Description |
|:--------|:------------|
| 🔑 **API Key CRUD** | Create, edit, delete API keys via Management API — each with custom name, notes, and independent enable/disable toggle |
| 📊 **Per-Key Quotas** | Set max token / request quotas per key with automatic enforcement |
| ⏱️ **Rate Limiting** | Per-key rate limiting (requests per minute/hour) |
| 🔒 **Key Masking** | API keys are always displayed masked (`sk-***xxx`) in UI and logs |
| 🌍 **Public Lookup Page** | End users can query their own usage stats and request logs via a public self-service page (no login required) |

### 🔗 Provider Channel Management

| Feature | Description |
|:--------|:------------|
| 📋 **Multi-Tab Config** | Manage channels organized by provider type: Gemini, Claude, Codex, Vertex, OpenAI Compatible, Ampcode |
| 🏷️ **Channel Naming** | Each channel can have a custom name, notes, proxy URL, custom headers, and model alias mappings |
| ⏱️ **Latency Tracking** | Average latency (`latency_ms`) tracked per channel with visual indicators |
| 🔄 **Enable/Disable** | Individually toggle channels on/off without deletion |
| 🚫 **Model Exclusions** | Exclude specific models from a channel (e.g., block expensive models on backup keys) |
| 📊 **Channel Stats** | Per-channel success/fail counts and model availability displayed on each channel card |

### 🛡️ Security & Authentication

| Feature | Description |
|:--------|:------------|
| 🔐 **OAuth Support** | Native OAuth flows for Gemini, Claude, Codex, Qwen, iFlow, Antigravity, and Kimi, plus device/browser/cookie variants where supported |
| 🔒 **TLS Handling** | Configurable TLS settings for upstream communication |
| 🏠 **Panel Isolation** | Management panel access controlled independently with admin password |
| 🛡️ **Request Cloaking** | Upstream requests are stripped of client-identifying headers for privacy |

### 🗄️ Data Persistence

| Feature | Description |
|:--------|:------------|
| 💾 **SQLite Storage** | All usage data, request logs, and message bodies stored in local SQLite database |
| 🔄 **Redis Backup** | Optional Redis integration for periodic snapshotting and cross-restart metric preservation |
| 🗃️ **Pluggable Auth/Config Backends** | Local files by default, with optional PostgreSQL, Git, or S3-compatible object storage backends for config/auth persistence |
| 📦 **Config Snapshots** | Import/export entire system configuration as JSON for backup and migration |

## 📸 Management Panel Preview

CliRelay can expose a built-in web control panel at `/manage`. The server can host bundled SPA assets or fall back to synced management assets from the configured panel repository.

The latest panel walkthrough below covers all 13 core screens now shipped in the management workflow.

| Screen | What operators can do there |
| :----- | :-------------------------- |
| Dashboard Overview | Review KPIs, health score, system status, resource usage, throughput, and channel latency |
| Monitor Center | Analyze model distribution, daily trends, API Key usage share, and time-range summaries |
| Request Logs | Filter and inspect requests by time range, key, model, channel, and status |
| Request Details | Read formatted prompt/response bodies with Markdown rendering and collapsible blocks |
| AI Providers | Manage provider tabs, channel health, model coverage, and CRUD operations |
| Auth Files | Inspect stored auth files, rename channels, configure prefixes/proxies, and download credentials |
| OAuth Login | Start provider authorization and submit remote callback URLs |
| API Keys | Control key quotas, RPM/TPM, allowed models, channel bindings, and shortcuts |
| Model Pricing | Maintain quota-cost pricing for input, output, and cache dimensions |
| Quota | Review remaining refresh time and quota progress across provider families |
| Config | Edit source YAML, search config, and switch runtime/config views |
| System | Inspect base URLs, versions, lookup links, and discovered model inventory |
| Logs | Stream runtime logs with search, download, clear, and visibility toggles |

### 1. Dashboard Overview

<p align="center">
  <img src="docs/images/dashboard-overview.png" width="100%" />
</p>
<p align="center"><em>Dashboard — KPI cards, health score, live system monitor, throughput, storage, and channel latency ranking.</em></p>

### 2. Monitor Center

<p align="center">
  <img src="docs/images/monitor-center-zh.png" width="100%" />
</p>
<p align="center"><em>Monitor Center (Chinese locale) — request summary, model distribution, daily token/request trends, and API Key usage share.</em></p>

### 3. Request Logs

<p align="center">
  <img src="docs/images/request-logs-table.png" width="100%" />
</p>
<p align="center"><em>Request Logs — time-range switcher, multi-filter toolbar, high-density table, and success metrics at a glance.</em></p>

### 4. Request Details Viewer

<p align="center">
  <img src="docs/images/request-details-modal.png" width="100%" />
</p>
<p align="center"><em>Request Details — input/output tabs, Markdown rendering, collapsible sections, and copy/export helpers.</em></p>

### 5. AI Providers

<p align="center">
  <img src="docs/images/providers-codex.png" width="100%" />
</p>
<p align="center"><em>AI Providers — provider tabs, per-channel success/failure stats, model badges, latency bars, and CRUD actions.</em></p>

### 6. Auth Files

<p align="center">
  <img src="docs/images/auth-files-grid.png" width="100%" />
</p>
<p align="center"><em>Auth Files — card-based inventory for saved credentials with model inspection, rename, proxy-prefix, download, and delete actions.</em></p>

### 7. OAuth Login Workbench

<p align="center">
  <img src="docs/images/oauth-login-workbench.png" width="100%" />
</p>
<p align="center"><em>OAuth Login — provider-specific authorization launcher plus remote callback URL submission workflow.</em></p>

### 8. API Keys Management

<p align="center">
  <img src="docs/images/api-keys-management.png" width="100%" />
</p>
<p align="center"><em>API Keys — quotas, RPM/TPM limits, model permissions, channel bindings, and quick analytics/edit actions.</em></p>

### 9. Model Pricing

<p align="center">
  <img src="docs/images/model-pricing.png" width="100%" />
</p>
<p align="center"><em>Models — built-in pricing catalog for input/output/cache cost calculation and quota accounting.</em></p>

### 10. Quota Management

<p align="center">
  <img src="docs/images/quota-management.png" width="100%" />
</p>
<p align="center"><em>Quota — remaining refresh time and progress bars for Codex, Gemini CLI, Kiro, and other provider-specific quotas.</em></p>

### 11. Config Editor

<p align="center">
  <img src="docs/images/config-source-editor.png" width="100%" />
</p>
<p align="center"><em>Config — source editor mode with YAML search, keyboard-friendly navigation, and runtime config switching.</em></p>

### 12. System Info

<p align="center">
  <img src="docs/images/system-info-models.png" width="100%" />
</p>
<p align="center"><em>System — API base, management endpoint, version/build metadata, API Key lookup entry, and vendor-colored model tags.</em></p>

### 13. Live Logs

<p align="center">
  <img src="docs/images/live-logs.png" width="100%" />
</p>
<p align="center"><em>Logs — live stream viewer with keyword search, hide-management toggle, download, clear, and jump-to-latest controls.</em></p>

> 🔗 The runtime panel source is configurable via `remote-management.panel-github-repository`. The default repository is [kittors/codeProxy](https://github.com/kittors/codeProxy).

## 🏗️ Supported Providers

| Provider / Channel | Auth | Notes |
|:-------------------|:-----|:------|
| Google Gemini | OAuth + API Key | Gemini CLI / AI Studio style flows |
| Anthropic Claude | OAuth + API Key | Claude Code and Claude-compatible clients |
| OpenAI Codex | OAuth + API Key | Includes Responses and WebSocket bridging |
| Qwen | OAuth | Qwen Code style login flow |
| iFlow / GLM | OAuth + Cookie | Supports iFlow routing and related model families |
| Kimi | OAuth | Browser-based login flow |
| Antigravity | OAuth | Dedicated OAuth channel with model backfill support |
| Vertex-compatible endpoints | API Key | Custom base URL, headers, aliases, exclusions |
| OpenAI-compatible upstreams | API Key | OpenRouter, Grok-compatible endpoints, and custom providers |
| Amp integration | Upstream API key + mappings | Direct Amp upstream fallback or mapped local routing |

## 🚀 Quick Start

### 1️⃣ Download & Configure

```bash
# Download the latest release for your platform from GitHub Releases
# Then copy the example config
cp config.example.yaml config.yaml

# Optional: build locally from source
go build -o cli-proxy-api ./cmd/server
```

Edit `config.yaml` to add your API keys or OAuth credentials. By default, client API routes
(`/v1`, `/v1beta`) require an API key. To run without client keys, set `allow-unauthenticated: true`
(not recommended for production).

### 2️⃣ Run

```bash
./cli-proxy-api -config ./config.yaml
# Server: http://localhost:8317
# Web panel (if enabled): http://localhost:8317/manage
```

The release artifact is currently named `cli-proxy-api`. The `clirelay` command shown later is a helper wrapper installed by `install.sh`, not the raw server binary name.

### Useful CLI Modes

```bash
# OAuth / credential flows
./cli-proxy-api -login
./cli-proxy-api -codex-login
./cli-proxy-api -codex-device-login
./cli-proxy-api -claude-login
./cli-proxy-api -qwen-login
./cli-proxy-api -iflow-login
./cli-proxy-api -iflow-cookie
./cli-proxy-api -antigravity-login
./cli-proxy-api -kimi-login

# Admin interfaces
./cli-proxy-api -tui
./cli-proxy-api -tui -standalone

# Other utilities
./cli-proxy-api -vertex-import ./service-account.json
./cli-proxy-api -oauth-callback-port 18080 -no-browser
```

### 🐳 Docker (Recommended)

**One-Click Deploy**:

- Linux `amd64` / `arm64`: supported for automatic Docker installation
- macOS `arm64` / `amd64`: supported when Docker Desktop / OrbStack / Colima is already installed and running

```bash
curl -fsSL https://raw.githubusercontent.com/kittors/CliRelay/main/install.sh | bash
```

The script will:

- automatically install Docker when needed
- detect the host architecture and pin the correct Docker platform for `amd64` / `arm64`
- let you choose **English** or **Chinese** during installation
- persist that language into the container so the built-in TUI starts in the selected language by default
- install a local `clirelay` helper command for day-2 operations

On macOS, the installer will **not** try to install Docker for you. It will reuse your existing Docker runtime and stop with a clear message if Docker Desktop / OrbStack / Colima is not running.

After installation, use:

```bash
clirelay status
clirelay update
clirelay restart
clirelay logs
clirelay tui
```

`clirelay update` keeps the existing configuration and only refreshes the image + containers, so ongoing upgrades stay simple.

> 💡 If `curl` is not installed, install it first:
> ```bash
> # Debian / Ubuntu
> apt-get update && apt-get install -y curl
>
> # CentOS / RHEL / Fedora
> yum install -y curl
> ```

Or deploy manually with Docker Compose:

```bash
docker compose up -d
```

The included `docker-compose.yml` uses the published `ghcr.io/kittors/clirelay:latest` image by default, so a fresh clone follows the normal production deployment path and does not compile Go on the target machine.

The bundled `build:` section is kept as a local fallback for source-level verification or emergency rebuilds. If you explicitly want to force a local build from the checked out source instead of pulling GHCR:

```bash
CLI_PROXY_IMAGE=clirelay-local:dev CLI_PROXY_PULL_POLICY=never docker compose up -d
```

For manual Docker deployments, you can also set `CLIRELAY_LOCALE=en` or `CLIRELAY_LOCALE=zh` in your Compose environment to control the default TUI language.

### 🗄️ Enabling Data Persistence

By default, API usage logs are stored in SQLite for persistence. For additional backup:
1. Ensure you have a Redis server running.
2. Edit `config.yaml` and set `redis.enable: true` with your Redis address.
CliRelay will automatically snapshot and restore traffic metrics on every startup!

For large installations, tune `request-log-storage` in `config.yaml` to control how full request/response bodies are retained. By default, full bodies are compressed, kept for 30 days, and capped at ~1GB (1024MB); lightweight request metadata remains queryable for longer-term statistics. Set `content-retention-days: 0` to keep full content indefinitely, set `store-content: false` to stop new body storage without deleting existing historical content, and adjust `max-total-size-mb` to cap body storage so the oldest full bodies are pruned before the retention window is reached.

If you need non-local config/auth persistence, the server also supports PostgreSQL, Git-backed, and S3-compatible object-store backends through environment-based bootstrap settings.

### 3️⃣ Point Your Tools

Set your AI tool's API base to `http://localhost:8317` and start coding!

**Example: OpenAI Codex (`~/.codex/config.toml`)**
```toml
[model_providers.tabcode]
name = "openai"
base_url = "http://localhost:8317/v1"
requires_openai_auth = true
```

> 📖 **Full setup guides →** [help.router-for.me](https://help.router-for.me/)

## 🖥️ Management Panel

When the control panel is enabled, open:

```bash
http://localhost:8317/manage
```

- `remote-management.disable-control-panel` now defaults to `false` in the example config and installer-generated config, so the control panel is reachable after a standard deployment.
- When enabled, the current panel route is `/manage/login`. The old `management.html#/login` route is legacy-only.
- Official Docker installs and the published image expose the panel at `/manage`.
- The server can serve a bundled SPA directory or auto-fetch panel assets when needed.
- This repository contains the hosting/update path for `/manage`; the standalone web panel source is maintained separately from the Go server code.
- Terminal-first management is also available through `clirelay tui` or `./cli-proxy-api -tui`.
- If you want to customize the panel asset source, set `remote-management.panel-github-repository`.

## 📐 Architecture

```text
CliRelay/
├── cmd/server/               # Binary entry point and CLI mode dispatch
├── internal/api/             # HTTP server, management routes, middleware
├── internal/auth/            # Provider OAuth / cookie / browser auth flows
├── internal/config/          # Config parsing, defaults, migrations
├── internal/store/           # Local, Git, PostgreSQL, object-store auth/config persistence
├── internal/tui/             # Terminal management UI
├── internal/usage/           # SQLite usage DB, retention, analytics
├── internal/managementasset/ # /manage panel hosting and asset sync
├── sdk/                      # Reusable Go SDK, handlers, executors
├── auths/                    # Local credential storage
├── examples/                 # SDK / custom provider examples
├── docs/                     # Local docs and panel screenshots
└── docker-compose.yml        # Container deployment entry
```

## 📚 Documentation

| Doc | Description |
|:----|:------------|
| [Getting Started](https://help.router-for.me/) | Full installation and setup guide |
| [Management API](https://help.router-for.me/management/api) | REST API reference for management endpoints |
| [Amp CLI Guide](https://help.router-for.me/agent-client/amp-cli.html) | Integrate with Amp CLI & IDE extensions |
| [SDK Usage](docs/sdk-usage.md) | Embed the proxy in Go applications |
| [SDK Advanced](docs/sdk-advanced.md) | Executors & translators deep-dive |
| [SDK Access](docs/sdk-access.md) | Authentication in SDK context |
| [SDK Watcher](docs/sdk-watcher.md) | Credential loading & hot-reload |

## 🤝 Contributing

Contributions are welcome! Here's how to get started:

```bash
# 1. Clone the repository
git clone https://github.com/kittors/CliRelay.git

# 2. Create a feature branch
git checkout -b feature/amazing-feature

# 3. Make your changes & commit
git commit -m "feat: add amazing feature"

# 4. Push to your branch & open a PR
git push origin feature/amazing-feature
```

## 📜 License

This project is licensed under the **MIT License** — see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgements & Special Thanks

This project is a deeply enhanced fork built upon the excellent core logic of the open-source **[router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)** project.
We want to express our deepest gratitude to the original **CLIProxyAPI** project and all its contributors!

It is thanks to the solid, innovative proxy distribution foundation built by the upstream that we were able to stand on the shoulders of giants. This allowed us to develop unique advanced management features (like API Key tracking & control, full request logging with SQLite, and real-time system monitoring) and rebuild an entirely new frontend dashboard from scratch.

A huge salute to the spirit of open source! ❤️
