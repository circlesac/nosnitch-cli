# nosnitch

**Are your AI accounts training on or publicly sharing your work?**

`nosnitch` verifies the current privacy state of the AI accounts available on
your machine. It groups CLI and browser sessions by account instead of treating
each product surface as a separate identity.

## Why this exists

Privacy guidance often stops at “change these settings,” which does not verify
that every signed-in account is configured as intended. `nosnitch` reads the
current settings across the CLI and browser sessions already on your machine so
you can check the actual state in one place.

The Claude public-sharing check was added after
[a community report showed that publicly shared Claude conversations could appear in Google Search](https://www.reddit.com/r/ClaudeAI/comments/1v6fiyj/you_can_view_a_lot_of_shared_conversations_via/).
`nosnitch` shows the title and URL of each public share it finds and can remove
those links with `nosnitch claude unshare`.

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
`2` incomplete (one or more account checks could not be completed). This makes
the command suitable for CI and local checks.

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
  OAuth token stored in macOS Keychain or `~/.claude/.credentials.json` on
  Linux for a read-only request to `/api/oauth/account/settings`.
- **Claude**: borrows the logged-in Claude Desktop or browser session and reads the same
  `/api/organizations/{id}/shares` endpoint used by claude.ai.

Browser cookies are decrypted locally using macOS Keychain or Linux Secret
Service. Tokens and cookies are sent only to the corresponding first-party
service and are never printed or sent elsewhere.

## Install

```bash
# Debian and Ubuntu
sudo install -d -m 0755 /etc/apt/keyrings
tmp_key="$(mktemp)"
curl -fsSL -o "$tmp_key" \
  https://github.com/circlesac/homebrew-tap/releases/latest/download/circlesac-archive-keyring.asc
fingerprint="$(gpg --batch --show-keys --with-colons "$tmp_key" 2>/dev/null \
  | awk -F: '$1 == "fpr" {print $10; exit}')"
test "$fingerprint" = "EDEB035445B676C3D9C4CFA2263CBDF3A243818E"
sudo install -m 0644 "$tmp_key" /etc/apt/keyrings/circlesac-archive-keyring.asc
rm -f "$tmp_key"
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/circlesac-archive-keyring.asc] https://github.com/circlesac/homebrew-tap/releases/latest/download ./" \
  | sudo tee /etc/apt/sources.list.d/circlesac.list >/dev/null
sudo apt-get update
sudo apt-get install nosnitch

# macOS or Linuxbrew
brew install circlesac/tap/nosnitch

# Standalone
curl -fsSL https://github.com/circlesac/nosnitch-cli/releases/latest/download/install.sh | sh
```

## Usage

```bash
nosnitch check                  # human-readable account report
nosnitch check --json           # machine-readable account report
nosnitch off                    # clear all detected privacy exposure
nosnitch off --yes              # skip confirmation
nosnitch openai training        # turn off OpenAI training settings only
nosnitch openai training --yes  # turn off without prompting
nosnitch claude training        # turn off Claude model improvement only
nosnitch claude training --yes  # turn off without prompting
nosnitch claude unshare         # remove only public Claude links
nosnitch claude unshare --yes   # remove links without prompting
nosnitch openai --help          # OpenAI-specific help
nosnitch claude --help          # Claude-specific help
```

`nosnitch off` disables supported OpenAI training settings, disables Claude's
account-wide model-improvement setting, and removes detected public Claude chat
links. Every command that changes account state asks for confirmation unless
`--yes` is provided. The provider-specific `training` commands change only
that provider's training settings. `nosnitch claude unshare` changes only
Claude's public links.

## Platform support

`nosnitch` supports macOS and Linux on amd64 and arm64. On macOS, browser
support includes Chrome, Edge, Brave, Claude Desktop, and Safari; Safari
requires Full Disk Access. On Linux, browser support includes Chrome, Chromium,
Edge, and Brave default profiles. Linux `v11` Chromium cookies require an
unlocked Secret Service keyring and `secret-tool` (`libsecret-tools` on
Ubuntu); Homebrew installs this dependency automatically.

## Security note

`nosnitch` reads sensitive local credentials to inspect your settings. Requests
are read-only unless you explicitly run `off`, a provider-specific `training`
command, or `claude unshare`.
