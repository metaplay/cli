# Metaplay develop — shared notes

Cross-area conventions for the three review topics (`review-actions`, `review-configs`, `review-models`). Load this alongside whichever topical sub-page applies to your work.

## Two modes

The rules are the same whether you're writing fresh code or reviewing existing code.

**Authoring** — when writing or editing a file in one of the three areas (or running `/metaplay:implement`), load the matching sub-page *before* you generate code. Apply the design patterns during planning and the rule checklist as you write. Catching violations at the point of writing is cheaper than fixing them after the fact.

**Reviewing** — when the user asks to review, audit, or check action/config/model code, run the full workflow:

1. **Scope.** Determine what to review from the user's request — file paths, class names, commit ref, `changes in this PR`, or area-wide (if unspecified). If they named a focus ("security", "determinism", "validation", "immutability", "fast-forward", "performance"), limit to those rule categories in the loaded sub-page.
2. **Discover.** Grep for the entry points listed in the sub-page. Traverse to sub-models, member types (reward structs, cost definitions), and partial class declarations. Verify completeness by counting the relevant attributes (`[ModelAction(`, `[GameConfigEntry]`, `[MetaMember]`) and comparing against discovered classes — investigate mismatches.
3. **Analyze.** For reviews spanning many files, cluster into small groups (a model + its sub-models, an actions file + its listeners) and launch one subagent per cluster in parallel. Give each subagent the file paths and the checklist from the relevant sub-page.
4. **Consolidate.** Deduplicate findings, group by severity, report.

## Severity convention (review mode)

- **Issues (must fix):** bugs, desyncs, cheating or security holes.
- **Warnings (should fix):** performance problems, patterns that break at scale, fragile designs.
- **Suggestions (consider):** style, naming, minor refinements.

For each finding include: the class name, `file:line`, the violated rule code (e.g. `D2`, `CD2`), and a one-line explanation of what's wrong and what to change. End with a summary — files reviewed, findings per severity, key concerns.

If no issues are found, report a clean result.

## When NOT to use this skill

- Non-Metaplay C# code — use standard practice.
- Build failures, server crashes, or deployment problems — use `metaplay-devops`.
- SDK API or "how do I..." questions — use `metaplay-docs`.
