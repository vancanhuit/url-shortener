// Conventional Commits config.
// See: https://www.conventionalcommits.org/
module.exports = {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "type-enum": [
      2,
      "always",
      [
        "build",
        "chore",
        "ci",
        "docs",
        "feat",
        "fix",
        "perf",
        "refactor",
        "revert",
        "style",
        "test",
      ],
    ],
    "subject-case": [
      2,
      "never",
      ["start-case", "pascal-case", "upper-case"],
    ],
    "header-max-length": [2, "always", 100],
  },
};
