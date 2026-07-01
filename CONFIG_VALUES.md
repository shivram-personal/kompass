# Kompass — configuration values

All settings are read from `KOMPASS_`-prefixed environment variables (see
`kompass_core/config.py`). This file documents the operator-facing knobs; defaults
are chosen so the same image runs across local dev, the test container, and GKE.

## AI apply-actions & budgets (Phase 6)

| Env var | Default | Meaning |
|---|---|---|
| `KOMPASS_DEFAULT_DAILY_TOKEN_BUDGET` | `100000` | Per-user daily token budget (tokens/user/day) when the user has no override. A per-user override lives on `users.daily_token_budget`. `0` or negative = unlimited by default. **Enforced on the LLM call (chat/troubleshoot), not on apply** — apply spends no provider tokens (SPEC §4.3/§4.5). |
| `KOMPASS_PROPOSAL_TTL_SECONDS` | `900` | A proposal is single-use and expires this many seconds after creation (15 min). Expired proposals cannot be applied. |
| `KOMPASS_WORKERS` | `1` | Worker-process count. **Load-bearing: must be 1.** The apply-action `select→apply` serialization uses one in-process lock; more than one worker would silently break wrong-cluster protection, so core **refuses to boot** if this (or `WEB_CONCURRENCY`) is > 1 (SPEC §4.3). |

## Notes

- The per-user budget default is also surfaced in the admin UI as an editable
  value (SPEC §4.5); the env var is the boot default.
- Budgets meter provider token spend; the window is a calendar day in UTC and
  resets at 00:00 UTC.
