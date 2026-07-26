# nosnitch

**Are your AI accounts training on or publicly sharing your work?**

`nosnitch` verifies the current privacy state of the AI accounts available on
your machine. It groups CLI and browser sessions by account instead of treating
each product surface as a separate identity.

```text
$ nosnitch check
nosnitch · AI account privacy check

  [Claude Account]
    Account           you@company.com
    Plan              Max
    Discovered via    Claude Code, Chrome
    Model improvement OFF
    Shared chats      0

  [OpenAI Account]
    Account           you@company.com
    Plan              ChatGPT Pro
    Discovered via    Codex CLI, Chrome
    API data sharing  OFF
    Model training    OFF
    Codex training    OFF

  ✓ no training or public-sharing exposure found
```

Exit code: `0` clean · `1` training or public-sharing exposure found ·
`2` indeterminate. This makes the command suitable for CI and local checks.

## What it checks

| Account | Source | Reports |
|---|---|---|
| **OpenAI Account** | Codex CLI | Account, plan, and API data-sharing incentives enrollment |
| **OpenAI Account** | ChatGPT browser session | ChatGPT and Codex model-training settings |
| **Claude Account** | Claude Code | Account, plan, and the account-wide “Help improve Claude” setting |
| **Claude Account** | Claude Desktop or browser session | Publicly shared Claude conversations |

The Claude model-improvement preference applies to consumer Claude chats and
Claude Code coding sessions. Commercial Claude plans and API usage follow their
organization's commercial data policy.

## How it works

- **Codex CLI**: decodes the ID token in `~/.codex/auth.json` locally.
- **ChatGPT**: borrows the logged-in browser session to read
  `/backend-api/settings/user`.
- **Claude Code**: reads account metadata from `~/.claude.json`, then uses the
  OAuth token stored in macOS Keychain for a read-only request to
  `/api/oauth/account/settings`.
- **Claude**: borrows the logged-in Claude Desktop or browser session and reads the same
  `/api/organizations/{id}/shares` endpoint used by claude.ai.

Browser cookies are decrypted locally using macOS Keychain. Tokens and cookies
are sent only to the corresponding first-party service and are never printed or
sent elsewhere.

## Install

```bash
brew install circlesac/tap/nosnitch
# or
curl -fsSL https://github.com/circlesac/nosnitch-cli/releases/latest/download/install.sh | sh
```

## Usage

```bash
nosnitch check          # human-readable report
nosnitch check --json   # machine-readable report
nosnitch off            # turn off training and remove public shares
nosnitch off --yes      # skip confirmation when public shares are found
nosnitch claude unshare        # review and remove public Claude chat links
nosnitch claude unshare --yes  # remove all detected links without prompting
```

`nosnitch off` disables supported OpenAI training settings, disables Claude's
account-wide model-improvement setting, and removes detected public Claude chat
links. It asks for confirmation when public links exist unless `--yes` is
provided. `nosnitch claude unshare` is available when only the public links
should be removed.

## Platform support

The current release targets macOS. Chromium browser support includes Chrome,
Edge, and Brave default profiles; Safari requires Full Disk Access. Planned:
additional browser profiles, Arc, and Linux/Windows cookie stores.

## Security note

`nosnitch` reads sensitive local credentials to inspect your settings. Requests
are read-only except when you explicitly run `nosnitch off`.
