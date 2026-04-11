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
