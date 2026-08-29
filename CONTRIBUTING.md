# Contributing to beanstore

## Developer Certificate of Origin

Contributions must be signed off. By adding a `Signed-off-by` line to your
commit you certify the [Developer Certificate of Origin 1.1](https://developercertificate.org/):
that you wrote the contribution or otherwise have the right to submit it
under the project's license.

Sign off with git's built-in flag:

```
git commit -s
```

which appends a line of the form

```
Signed-off-by: Your Name <your@email.example>
```

Commits without a sign-off are rejected by the commit-msg hook (and will be
rejected in review).

## Commit messages

Plain [Conventional Commits](https://www.conventionalcommits.org/): a first
line of the form `type: description` — no scopes. Allowed types: `feat`,
`fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`, `revert`.

## Licensing of contributions

The server (root module) is AGPL-3.0; the `client/` module is MIT. A
contribution is licensed under the license of the module it touches.
