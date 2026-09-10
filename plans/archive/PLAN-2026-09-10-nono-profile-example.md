# A default `nono` profile as an example

> TODO: "Add a default `nono` profile as an example"

## Problem

The model assumes a sandbox that sets `NONO_CAP_FILE`, grants **read** on
`~/.claude-bash-rules`, **write** only on `~/.claude-bash-rules/logs`, and keeps
`engine/`+`global/rules` non-writable. The only profile in the repo
(`.sandbox/profile.json`) is for *developing bash-gate itself* — machine-specific,
not a deployment template. Want a generic, documented example that encodes the
required shape (the thing `selfcheck` verifies).

## Constraints

- nono profiles are plain JSON, no reliable inline comments → explain in the
  README, not in the file.
- Machine-neutral: placeholders for workdir + trust dirs, no personal paths.

## Options

- **A. Ship `conf/nono-profile.example.json` + README section (pick).** Generic
  profile beside the other `conf/` examples: `read` = `~/.claude-bash-rules`,
  `allow` (readwrite) = `~/.claude-bash-rules/logs` + workdir placeholder,
  default-deny network. Keep `.sandbox/profile.template.json` as the dev profile,
  and say so.
- **B. Generate via `bash-gate print-profile`.** Interpolates `$HOME` but couples
  the binary to nono's schema and loses comments. Later, if needed.
- **C. Prose only.** Rejected — the TODO wants a copyable file.

## Pick: A

## Changes

- New `conf/nono-profile.example.json`: read `~/.claude-bash-rules`; allow
  `~/.claude-bash-rules/logs` + `<AGENT_WORKDIR>`; `workdir.access readwrite`;
  minimal `allow_vars`; empty network allowlist.
- `README.md`: "Example sandbox profile" — field-by-field, read-not-write
  rationale, wrap the agent, then run `bash-gate selfcheck`.
- Cross-link `ARCHITECTURE.md` Install/Self-protection; note `.sandbox/` is the
  dev profile, `conf/` is the deployment example.

## Open

- Does the target nono version tolerate a `_comment` field? If not, all
  explanation lives in the README.
- Standalone vs `extends` a base `claude` profile — lean standalone for
  portability.
