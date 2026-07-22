# nosnitch

**Is your coding agent snitching your code to model training?**

`nosnitch` verifies — from your own machine — whether the AI coding tools you use
are configured to send your prompts/code into model training. It doesn't just
*tell you how to opt out*; it reads the **actual current state** of your accounts
and reports ON/OFF.

```
$ nosnitch check
nosnitch — coding-agent training-share check

[codex-cli] ~/.codex/auth.json
  account            : you@example.com  (ChatGPT pro)
  API data-sharing   : ON  (incentives program = API-key traffic collected for training)
     → turn off: platform.openai.com/settings/organization/data-controls/sharing

[chatgpt-web] Chrome session
  account                  : you@company.com
  Improve the model (all)  : ON
  Codex content training   : OFF

verdict: ⚠️  a training-share setting is ON — review above
```

Exit code: `0` clean · `1` a training-share setting is ON · `2` indeterminate —
so it drops into CI or a pre-commit hook.

## What it checks

| Source | Reads | Reports |
|--------|-------|---------|
| **codex-cli** | `~/.codex/auth.json` (local JWT, no network) | account, plan, and whether the org is opted into OpenAI's **API data-sharing incentives program** |
| **chatgpt-web** | your browser's logged-in ChatGPT session | `training_allowed` ("Improve the model for everyone") and `codex_training_allowed` for that account |

## How it works

- **codex-cli** decodes the id_token in `~/.codex/auth.json` locally — nothing leaves your machine.
- **chatgpt-web** borrows the session you're already logged into:
  1. decrypts Chrome's `chatgpt.com` cookies (macOS Keychain → AES-128-CBC),
  2. impersonates a real browser's TLS fingerprint so Cloudflare lets the request through,
  3. exchanges the session cookie for an `accessToken` and reads `/backend-api/settings/user`.

Everything runs locally against **your own** accounts. No token or cookie ever leaves the machine.

## Install

```bash
brew install circlesac/tap/nosnitch
# or
curl -fsSL https://github.com/circlesac/nosnitch-cli/releases/latest/download/install.sh | sh
```

## Usage

```bash
nosnitch check          # human-readable report
nosnitch check --json   # machine-readable
```

## Status / roadmap

v1 targets **macOS + Chrome**. Planned: Edge/Brave/Arc and Linux/Windows cookie
stores; more agents (Claude Code, opencode, Cursor); optionally *changing* a
setting, not just reading it.

## Security note

`nosnitch` reads sensitive local credentials (Codex tokens, browser cookies) to
inspect **your** settings. It performs read-only requests to your own accounts
and never transmits secrets anywhere.
