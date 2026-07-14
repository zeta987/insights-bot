# Issue tracker: GitHub

Issues and PRDs for this repository live in GitHub Issues for
`zeta987/insights-bot`. Use the `gh` CLI for all operations.

## Repository

- GitHub repository: `zeta987/insights-bot`
- Git remote: `origin`
- CLI: `gh`

When commands run inside this clone, `gh` infers the repository from the Git
remote. Use `--repo zeta987/insights-bot` when running elsewhere.

## Conventions

- **Create an issue**: prepare a Markdown body file, then run
  `gh issue create --title "..." --body-file <path>`.
- **Read an issue**: run `gh issue view <number> --comments` and include labels
  when structured output is required.
- **List issues**: run
  `gh issue list --state open --json number,title,body,labels,comments`
  with appropriate `--label` and `--state` filters.
- **Comment on an issue**: run
  `gh issue comment <number> --body-file <path>`.
- **Apply labels**: run
  `gh issue edit <number> --add-label "<label>"`.
- **Remove labels**: run
  `gh issue edit <number> --remove-label "<label>"`.
- **Close an issue**: run
  `gh issue close <number> --comment "..."`.

## When a skill says "publish to the issue tracker"

Create a GitHub issue in `zeta987/insights-bot`.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.
