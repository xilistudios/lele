# Skills Authoring

Skills are reusable instruction bundles that Lele can load from builtin locations, the global Lele directory, or the active workspace.

## Where Skills Can Live

Lele loads skills from locations such as:

- workspace skills directory
- global skills directory under `~/.lele`
- builtin bundled skills

Workspace-local skills normally live in:

```text
~/.lele/workspace/skills/
```

## Managing Skills

Useful commands:

```bash
lele skills list
lele skills search
lele skills show <name>
lele skills install <repo>
lele skills remove <name>
lele skills install-builtin
lele skills list-builtin
```

## How Skills Reach the Agent Context

To keep the system prompt small, the agent only sees the **5 most used skills**
for its agent ID (usage is tracked per agent in `~/.lele/skills_usage.json`).
Until enough usage accumulates the window is filled in alphabetical order, so a
fresh install shows the first five skills by name rather than the busiest ones.
Three tools cover the rest of the catalog:

- `list_skills` — browses the catalog paginated (20 per page, `page` argument),
  ordered by usage frequency (most used first), excluding the skills already
  in this session's context (the top-5 summary and the loaded ones).
- `search_skill` — keyword search over skill names, descriptions and SKILL.md
  bodies, ranked by relevance (name matches weigh most). Returns metadata only.
- `load_skill` — loads the full SKILL.md instructions of one or several skills
  into the context (`{"skill": "name"}` or `{"skills": ["a", "b"]}`). Loaded
  skills are pinned into the system prompt for subsequent turns and count
  towards the agent's usage ranking.

These tools respect the agent's `skills` filter and the workspace
enabled/disabled configuration.

### Session Pins

A loaded skill is **pinned to the session that loaded it**: one chat's load
never shows up in another chat's or another user's context, and `/new` (or any
session reset) drops the retired session's pins while leaving every other
session alone. A session may pin at most **8 skills / 32 KiB of SKILL.md text**;
a load that would exceed the budget is refused with a message naming the skills
and the unload call to make room. Loading a skill that is already pinned is a
no-op: no second copy of the body and no extra usage count.

To remove a pin, call `load_skill` with `unload` set:

```text
load_skill
{"skill": "dagu", "unload": true}

load_skill
{"skills": ["dagu", "lele"], "unload": true}
```

Unloading never changes the usage counters — those record how often a skill was
loaded, not how long it stays pinned. Subagents receive the full instructions in
their own result when they load a skill, but a subagent never pins into the
session that spawned it.

## Authoring Approach

Keep each skill focused on one reusable capability or workflow.

Good examples:

- code search workflow
- deployment checklist
- environment-specific ops workflow
- documentation authoring workflow

## Suggested Structure

Typical skill directory:

```text
skills/<skill-name>/
└── SKILL.md
```

## Writing Good Skill Content

Good skills usually include:

- purpose
- when to use it
- expected inputs
- workflow steps
- examples
- caveats

## Recommendations

- keep the scope narrow
- avoid duplicating global project rules
- describe the workflow in executable steps
- prefer stable filenames and stable skill names

## Related Docs

- `docs/SKILL_SUBAGENTS.md`
- `docs/session-and-workspace.md`
