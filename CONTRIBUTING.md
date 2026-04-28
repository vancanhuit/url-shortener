# Contributing

Thanks for taking the time to contribute!

## Development workflow

1. Install prerequisites: Go 1.26+, Node.js 20+, [Just](https://github.com/casey/just), `golangci-lint` v2.
2. Bootstrap dev tooling once per clone:
   ```sh
   just init
   ```
   This installs husky's git hooks and the commitlint CLI as a local dev dependency.
3. Make changes on a feature branch, then run:
   ```sh
   just fmt
   just lint
   just test
   just build
   ```
4. Open a pull request against `main`.

## Commit messages

We follow the [Conventional Commits 1.0](https://www.conventionalcommits.org/)
specification. The local `commit-msg` hook (installed by `just init`) and the
`commitlint` GitHub Action enforce this on every commit / PR.

```
<type>(<optional scope>): <subject>

[optional body]

[optional footer(s)]
```

Allowed `<type>` values:

- `feat`     -- a new user-visible feature
- `fix`      -- a bug fix
- `perf`     -- a performance improvement
- `refactor` -- code change that neither fixes a bug nor adds a feature
- `docs`     -- documentation only
- `test`     -- adding or updating tests
- `build`    -- build system, dependencies
- `ci`       -- CI / CD configuration
- `chore`    -- maintenance tasks
- `style`    -- formatting / whitespace
- `revert`   -- reverts a previous commit

Subject line must be lower-case, no trailing period, max 100 characters.

Breaking changes are indicated with `!` after the type / scope, e.g.
`feat!: remove deprecated /v0 endpoints`, and described in a `BREAKING CHANGE:`
footer.

## Branching & merge policy

- Default branch: `main`. All work is merged to `main` via pull request.
- PRs are **squash-merged**, so the merge commit on `main` is itself a valid
  conventional commit. The PR title is enforced to follow the same format.
- Tag releases follow semantic versioning: `vMAJOR.MINOR.PATCH`, optionally
  `-rc.N` for pre-releases. CI rejects tags that do not match
  `^v\d+\.\d+\.\d+(-[a-z0-9.]+)?$`.

## Code quality

We optimise for boring, testable code: write the simplest implementation that
passes its tests, then refactor when a second use case or measured bottleneck
appears. Refactor passes happen at phase review checkpoints, not mid-phase, so
PR diffs stay focused.
