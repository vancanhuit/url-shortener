## Summary

<!-- What does this PR do and why? One paragraph is enough for small changes. -->

## Type of change

<!-- Tick whichever apply. The PR title must use the matching conventional-commit type. -->

- [ ] `feat` – new user-visible feature
- [ ] `fix` – bug fix
- [ ] `perf` – performance improvement
- [ ] `refactor` – code restructuring, no behaviour change
- [ ] `docs` – documentation only
- [ ] `test` – adding or updating tests
- [ ] `build` / `ci` / `chore` – tooling, dependencies, CI

## Checklist

- [ ] `just fmt` passes (no formatting diff)
- [ ] `just lint` passes (0 issues)
- [ ] `just test` passes (`go test -race ./...`)
- [ ] `just test-integration` passes (if touching DB / Redis / server wiring)
- [ ] New behaviour is covered by tests
- [ ] PR title follows [Conventional Commits](https://www.conventionalcommits.org/) (`type(scope): subject`)

## Related issues / PRs

<!-- Closes #NNN or N/A -->
