# Domain Docs

This repository uses a single-context domain documentation layout.

## Before exploring, read these

- **`CONTEXT.md`** at the repository root.
- **`docs/adr/`** for architectural decisions related to the area being
  explored.

If these files do not exist, proceed silently. Do not flag their absence or
suggest creating them upfront. The producer skill (`/grill-with-docs`) creates
them lazily when domain terms or decisions are resolved.

## File structure

```text
/
├── CONTEXT.md
├── docs/
│   └── adr/
├── cmd/
├── internal/
├── pkg/
└── ent/
```

## Use the glossary's vocabulary

When output names a domain concept in an issue title, refactor proposal,
hypothesis, or test name, use the term defined in `CONTEXT.md`. Do not replace
a defined term with a synonym that the glossary explicitly avoids.

If a required concept is absent from the glossary, reconsider whether the term
belongs to this project or record the gap for `/grill-with-docs`.

## Flag ADR conflicts

If proposed work contradicts an existing ADR, identify the conflicting ADR and
explain why the decision may need to be revisited instead of silently
overriding it.
