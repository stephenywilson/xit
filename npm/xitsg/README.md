# XiT / 吸T神功

> AI CLI command-output compression, raw_log evidence, and Kimi integration.

## Install

```
npm i -g xitsg
```

The installed command is `xit`.

## Usage

```
xit --version
xit auto go test -v ./...
xit auto npm test
xit doctor
```

## Kimi Code Setup

```
xit init kimi --method official_hook --scope user --yes
xit kimi rules install --scope user --yes
```

## Optional Kimi Toolbar Patch

```
xit kimi status-patch install --yes --accept-risk
```

> **Note:** The status patch is opt-in because it modifies your local Kimi package. It is not applied automatically.

## Safety

- No telemetry
- raw logs stay local at `.xit/runs/` and `.xit/history.jsonl`
- `xit auto` never uploads output to any remote service
- Kimi status patch is opt-in, clearly labeled `--accept-risk`
- No network downloads at install time

## Supported Platforms

| Platform     | Architecture |
|--------------|-------------|
| macOS        | arm64 (Apple Silicon) |
| macOS        | x64 (Intel) |
| Linux        | x64 |
| Linux        | arm64 |
| Windows      | x64 |

## How It Works

`xitsg` is a thin Node.js wrapper that selects the correct pre-compiled Go binary for your platform and forwards all arguments and stdio. No compilation required at install time.

## License

MIT
