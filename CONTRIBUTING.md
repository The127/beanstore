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
line of the form `type: description`, no scopes. Allowed types: `feat`,
`fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`, `revert`.

## AI-assisted contributions

Welcome, under three rules. Disclose AI-generated code with a co-author
trailer (e.g. `Co-Authored-By: Claude <noreply@anthropic.com>`). You remain
fully responsible for what you submit: correctness, license compatibility,
and your DCO sign-off. And you must have reviewed and understood the code
yourself before pushing it.

## Licensing of contributions

The server (root module) is AGPL-3.0, the `client/` module is MIT. A
contribution is licensed under the license of the module it touches.
