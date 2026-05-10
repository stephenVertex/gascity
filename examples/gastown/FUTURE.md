# Gas Town Example - Future Work

Tracks the remaining gaps between the Gastown packs under
`examples/gastown/` and the current `gc` implementation.

This file used to be a broad list of commands referenced by early
prompts/formulas that did not exist yet. Most of those gaps have since
closed. The current status is:

- Core propulsion exists: `gc hook`, `gc sling`, `gc prime`, `gc handoff`,
  `gc runtime drain-ack`, `gc runtime request-restart`.
- Operational inspection exists: `gc status`, `gc session list`, `gc session
  peek`, `gc session logs`, `gc doctor`, `gc events --watch`.
- Coordination exists: `gc mail ...`, `gc convoy ...`, `gc order ...`,
  `gc bd ...` passthrough, formulas, wisps, and controller dispatch.
- Rig dormancy exists as `gc rig suspend/resume`; rig process refresh exists
  as `gc rig restart`; rig health exists as `gc rig status`.

Use this document for the remaining edge work, not as a complete command
inventory.

## Remaining gc gaps

These are the command or feature gaps still visible after auditing the
current implementation, prompts, formulas, and pack scripts.

| Gap | Current status | Action |
|-----|----------------|--------|
| `gc context --usage` | Not implemented. No current Gastown prompt/formula appears to call it directly, but older roadmap text expected a provider-specific context utilization query. | Decide whether this is still wanted. If yes, define provider contract and expose through the session/worker boundary. |
| `gc mail hook <id>` | Not implemented. Current prompts use `gc mail read/peek/archive/reply` and ordinary bead assignment instead. | Probably delete as a planned command unless a prompt starts needing "turn mail into assigned work" as a first-class operation. |
| `gc mail send --human` | Not implemented as a flag. Current supported form is `gc mail send human ...`. | Prefer documenting `human` as the recipient. Add the flag only if scripts need backwards-compatible sugar. |
| `gc rig start/stop/park/dock/unpark/undock/reboot` | Not implemented. The current command set is `add`, `list`, `remove`, `restart`, `resume`, `set-endpoint`, `status`, `suspend`. | Keep prompts on `suspend/resume/restart/status`, or implement aliases only after a concrete UX decision. |

## Prompt and doc cleanup

These are not missing SDK primitives, but they are current text edges in the
Gastown example.

| Location | Issue | Suggested fix |
|----------|-------|---------------|
| `packs/gastown/agents/mayor/prompt.template.md` | The rig lifecycle section still describes `stop/start` and `restart/reboot`, while the CLI only has `suspend/resume/restart/status` for rigs. | Rewrite that section around the implemented verbs. |
| `examples/gastown/SDK-ROADMAP.md` | This file repeats many of the old `FUTURE.md` claims, including stale entries for `gc peek`, `gc doctor`, prompt rendering, pre-start hooks, and mail lifecycle. | Either archive it or rewrite it to match this file. |
| Historical comments/tests mentioning `gc handoff` | Some comments correctly distinguish bare `gc handoff` from `gc handoff --auto`. | No command gap; keep only if useful as regression context. |

## Implemented command surfaces

Current `gc` exposes these relevant top-level command families:

- City lifecycle: `start`, `stop`, `restart`, `status`, `suspend`, `resume`,
  `reload`, `register`, `unregister`, `cities`, `supervisor`.
- Agents/sessions: `agent`, `session`, `runtime`, `hook`, `prime`, `handoff`,
  `nudge`, `wait`.
- Work and coordination: `bd`, `formula`, `sling`, `convoy`, `workflow`
  (deprecated alias for convoy/control operations), `order`, `events`, `event`,
  `graph`, `mail`.
- Infrastructure: `rig`, `beads`, `doctor`, `dolt-cleanup`, `dolt-config`,
  `dolt-state`, `service`, `dashboard`, `shell`, `mcp`, `skill`.

Notable implemented subcommands that were formerly listed as missing:

- `gc hook [agent]` runs the configured `work_query`.
- `gc sling [target] <bead-or-formula-or-text>` routes work, can instantiate
  formula wisps, can attach formula wisps with `--on`, and can auto-create
  convoys.
- `gc session peek <target> --lines N` replaces the old proposed top-level
  `gc peek`.
- `gc session nudge <target> <message...> --delivery immediate|wait-idle|queue`
  replaces the old proposed top-level `gc nudge`.
- `gc doctor [-v|--verbose] [--fix]` exists.
- `gc events --watch [--type ...] [--timeout ...]` exists, along with
  `--follow`, `--seq`, `--after`, `--after-cursor`, and payload filters.
- `gc convoy create/list/status/target/add/close/check/stranded/land/delete`
  exists.
- `gc order list/show/run/check/history/sweep-tracking` exists and the
  controller dispatches orders as wisps or exec scripts.
- `gc mail archive/delete/mark-read/mark-unread/peek/reply/thread/check/count`
  exists; `gc mail send --notify` exists.

## Implemented config features

These features were previously marked as missing but are now implemented:

| Feature | Current implementation |
|---------|------------------------|
| Custom session naming | `[workspace].session_template` supports Go-template placeholders such as `{{.City}}`, `{{.Agent}}`, `{{.Dir}}`, and `{{.Name}}`. |
| Pre-start hooks | `[[agent]].pre_start` is parsed, template-expanded, included in runtime fingerprints, and executed before session creation by tmux sessions. K8s embeds pre-start in the pod entrypoint. Some transports intentionally do not execute host-side pre-start. |
| Prompt template rendering | `prompt_template` files ending in `.template.md` are rendered with Go `text/template`, shared template fragments, injected fragments, frontmatter metadata, and prompt hashes. |
| Nudge delivery modes | `gc session nudge --delivery immediate|wait-idle|queue` and the deferred `gc nudge` queue commands exist. |
| Activity feed subscription | `gc events --watch` and `gc events --follow` exist. |
| Orders | Orders are first-class config under `orders/*.toml`, surfaced through `gc order`, and evaluated by the controller. |
| Wisps | Gastown uses `gc bd mol wisp`, `gc bd mol burn`, and hidden `gc wisp autoclose` infrastructure. |

## Current Gastown pack shape

The shipped example is now pack-driven:

- `examples/gastown/pack.toml` imports `packs/gastown` and uses it as the
  default rig import.
- `packs/gastown` imports `packs/maintenance`.
- City-scoped configured named sessions: mayor, deacon, boot.
- Rig-scoped configured named sessions: witness always-on, refinery on-demand.
- Rig-scoped pool agent: polecat, with `pre_start` worktree setup and
  `max_active_sessions = 5`.
- City-scoped pool utility: dog, supplied by maintenance and patched by
  Gastown.
- Maintenance orders handle mechanical sweeps such as gates, orphan cleanup,
  branch pruning, spawn-storm detection, wisp compaction, and JSONL/reaper
  jobs.

## Resolved replacements

These old Gastown-style command families should stay out of Go unless a new
first-principles need appears.

| Old idea | Current route |
|----------|---------------|
| Role-specific `gc polecat ...`, `gc dog ...`, `gc boot ...`, `gc mayor ...`, `gc deacon ...` | Generic session, rig, runtime, mail, bd, convoy, and order commands. |
| `gc done` | Push branch, update bead metadata/status, route to refinery when needed, `gc runtime drain-ack`. |
| `gc escalate` | `gc mail send <target> -s "ESCALATION: ..."` plus durable bead metadata when needed. |
| `gc mq ...` | Merge-request beads, refinery prompt/formula workflow, git operations, and bead metadata. |
| `gc warrant file ...` | `gc bd create --type=task --label=warrant --metadata ...` routed to dog. |
| `gc compact`, `gc patrol digest` | `bd`/`gc bd` queries plus maintenance orders and scripts. |
| `gc worktree ...` | Agent `pre_start` scripts and raw `git worktree` operations in prompts. |
| `gc feed ...` | `gc events --since ...`, `gc events --watch`, or `gc events --follow`. |
| `gc costs` | Removed; provider-specific and not a Gas City SDK primitive. |

## Remaining statistics

Approximate current gap count:

- SDK command gaps still worth considering: 1-3 (`gc context --usage`,
  maybe `gc mail hook`, maybe `gc mail send --human`).
- Implemented but prompt/doc cleanup needed: rig lifecycle wording, stale
  `SDK-ROADMAP.md`.
- Open primitive-design questions from the old file: none. Convoys, orders,
  events, prompt rendering, pre-start hooks, and nudge delivery all have
  current implementations.
