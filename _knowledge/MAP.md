# OpenCode — working map

Claude Code speaks the Anthropic API; the `ogc` proxy translates to
OpenAI-shaped backends and is opted into per launch.

Home: `/home/ff235/dev/opencode` — code, `GUIDE.md`, `PATCHES.md` and these
notes in one place. Open a session here to work on any of it. Being under
`~/dev` it gets its own ccage cage, and `sync-knowledge-vault.sh` links
`_knowledge/` into `/home/ff235/knowledge-vault/opencode`.

Paths first, prose second. Every path absolute. If a fact here cannot be
checked by running the command next to it, it does not belong here.

---

## OpenCode (Claude Code routed to open-weight models via the `ogc` proxy)

Claude Code speaks the Anthropic API; `ogc` translates to OpenAI-shaped
backends and is opted into per launch. Four regions, three outside `~/dev`.

| Part | Path |
|---|---|
| Proxy source (Go) | `/home/ff235/dev/opencode` |
| Built binary | `/home/ff235/go/bin/ogc` |
| systemd user unit | `/home/ff235/.config/systemd/user/ogc.service` |
| Model map / routing | `/home/ff235/.config/ogc/config.json` |
| API key | `/home/ff235/.config/ogc/.env` (`OGC_API_KEY`) — never print it |
| Launcher, picker, ctx limits | `/home/ff235/.bashrc.d/claude-overrides.sh` |
| Aliases | `/home/ff235/.bashrc.d/yolo-aliases.sh` (lines 7-10) |
| Statusline context display | `/home/ff235/.claude/statusline-command.sh` |
| Architecture + setup | `/home/ff235/dev/opencode/GUIDE.md` |
| The six local patches | `/home/ff235/dev/opencode/PATCHES.md` |

Functions in `claude-overrides.sh`: `_ccage_pre_exec_hook` (the routing
opt-in), `_opencode_select_model`, `_opencode_select_effort`,
`_opencode_model_context_limit`, `opencode-claude`.

Run / inspect:

    systemctl --user restart ogc      # after rebuilding the binary
    systemctl --user is-active ogc
    journalctl --user -u ogc -f       # every request + every routing error

**Adding or renaming a model means editing three places. Two fail silently.**

1. `/home/ff235/.config/ogc/config.json` — miss it and every request 500s with
   `no model mapping found`.
2. `_opencode_select_model` — the `models` and `descriptions` arrays are
   **positional**; edit one only and the menu mislabels everything below.
3. `_opencode_model_context_limit` — miss it and the model silently falls back
   to 200k, so a 1M-context model loses most of its window.

Drift check (expects three equal counts, currently 26/26/26):

    python3 - <<'PY'
    import json,re
    cfg=set(json.load(open('/home/ff235/.config/ogc/config.json'))['models'])
    src=open('/home/ff235/.bashrc.d/claude-overrides.sh').read()
    picker=set(re.findall(r'"([a-z0-9.\-]+)"',
        src[src.index('_opencode_select_model'):src.index('local descriptions')]))
    ctx=src[src.index('_opencode_model_context_limit'):src.index('# Usage:')]
    names=set(re.findall(r'[a-z0-9.\-]+', ctx.replace('|','\n')))
    print("config:",len(cfg),"picker:",len(picker))
    print("in config, not offered:", sorted(cfg-picker) or "none")
    print("offered, would 500:",    sorted(picker-cfg) or "none")
    print("no ctx limit (->200k):", [m for m in sorted(cfg) if m not in names] or "none")
    PY

**Not in the ccage repo.** ccage carries only a generic commented example in
`share/claude-overrides.sh.example` and a gateway section in
`docs/FEATURES.md`. No OpenCode behaviour lives there.

**Risk (2026-08-19):** this repo is a clone of `github.com/xynogen/ogc` —
upstream, not ours — and `origin` still points there, so there is nowhere to
push. All local work is uncommitted: 8 modified Go files, plus untracked
`GUIDE.md`, `PATCHES.md`, `internal/transformer/request_test.go` and
`internal/transformer/stream_test.go`. A `git checkout`/`stash` here destroys
the lot. Fork, repoint `origin`, keep `xynogen/ogc` as `upstream`, and commit.

---

## Related

- ccage (per-project isolation): `/home/ff235/dev/ccage`. Carries only a
  generic commented example in `share/claude-overrides.sh.example` and a
  gateway section in `docs/FEATURES.md` — no OpenCode behaviour.
- Vault convention: `/home/ff235/dev/knowledge-vault-setup`.
