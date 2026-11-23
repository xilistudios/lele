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
Two tools cover the rest of the catalog:

- `list_skills` — browses the catalog paginated (20 per page, `page` argument),
  ordered by usage frequency (most used first), excluding the skills already
  in the context (the top-5 summary and the loaded ones).
- `search_skill` — keyword search over skill names, descriptions and SKILL.md
  bodies, ranked by relevance (name matches weigh most). Returns metadata only.
- `load_skill` — loads the full SKILL.md instructions of one or several skills
  into the context (`{"skill": "name"}` or `{"skills": ["a", "b"]}`). Loaded
  skills are pinned into the system prompt for subsequent turns and count
  towards the agent's usage ranking.

These tools respect the agent's `skills` filter and the workspace
enabled/disabled configuration.

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
