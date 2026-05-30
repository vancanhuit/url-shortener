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

- [ ] `mise run fmt` passes (no formatting diff)
- [ ] `mise run lint` passes (0 issues)
- [ ] `mise run test` passes (`go test -race ./...`)
- [ ] `mise run test-integration` passes (if touching DB / Redis / server wiring)
- [ ] New behaviour is covered by tests
- [ ] PR title follows [Conventional Commits](https://www.conventionalcommits.org/) (`type(scope): subject`)

## Related issues / PRs

<!-- Closes #NNN or N/A -->
