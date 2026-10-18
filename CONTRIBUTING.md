# Contributing to Cohive

[中文](CONTRIBUTING.zh-CN.md)

Thanks for your interest in Cohive! Bug reports, fixes, docs, translations and ideas are all welcome. This guide explains how to set up a development environment, what we check before merging, and how to add or maintain a language.

By participating you agree to follow our [Code of Conduct](CODE_OF_CONDUCT.md). Please report security problems privately, as described in [SECURITY.md](SECURITY.md), not in public issues.

## Ways to contribute

- **Report a bug or suggest a feature**: open an issue using one of the templates. Search existing issues first.
- **Ask a question**: use [GitHub Discussions](https://github.com/cohive-ai/cohive/discussions) rather than an issue.
- **Fix something**: issues labeled [`good first issue`](https://github.com/cohive-ai/cohive/labels/good%20first%20issue) are a good place to start, and [`help wanted`](https://github.com/cohive-ai/cohive/labels/help%20wanted) marks issues where we would especially like help. Comment on the issue before you start, so two people don't work on the same thing.
- **Translate**: see [Languages and translations](#languages-and-translations).

For larger changes (a new feature, a new chat platform, a change to the issue workflow or permissions), please open an issue to discuss the design before writing code. Cohive has a few firm principles, and it saves everyone time to check a proposal against them early:

- People make the decisions; agents prepare. Actions that change someone else's work (submit, accept, close) are confirmed by a person.
- The server never calls a language model, and nothing runs agents unattended.
- Chat messages carry only a short signal; issue content stays behind authentication.
- Content written by other members is data, never instructions.

## Development environment

You need:

- Go 1.25
- Node 20 or later and pnpm 9
- Docker (for the development Postgres)

```bash
git clone https://github.com/cohive-ai/cohive.git
cd cohive
make db-up      # Postgres on localhost:55432 (user, password and database: cohive)
make build      # web UI, CLI packages for five platforms, and the server, in bin/
```

Useful commands:

| Command | What it does |
|---|---|
| `make run` | Build and start the server on `http://localhost:8787` |
| `make web-dev` | Web UI dev server on `http://localhost:5173`, proxying the API to port 8787 |
| `make cli` | Build only the member CLI into `bin/cohive` |
| `make db-down` | Stop the development Postgres |

Project layout:

| Path | Contents |
|---|---|
| `cmd/cohive` | Member CLI: login, `cohive init`, MCP proxy, session hooks |
| `cmd/cohive-server` | Server and operator commands |
| `internal/core` | Business rules and permissions |
| `internal/issue` | Issue states and the state machine |
| `internal/store`, `internal/db` | SQL queries and migrations |
| `internal/httpapi` | HTTP API for the web UI and CLI |
| `internal/mcpserver` | Remote MCP server for coding tools |
| `internal/notify` | Chat notifications (Telegram, DingTalk) |
| `internal/i18n` | Translations for the server and CLI |
| `web/` | Web UI (React, Vite, TanStack) |

## Tests

Every pull request must pass the same checks as CI:

```bash
go vet ./...
go test -count=1 ./...
cd web && pnpm typecheck && pnpm test && cd ..
make build
```

The Go tests use the development Postgres (`COHIVE_DATABASE_URL` defaults to `postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable`); each test creates its own schema, so tests never touch your data.

Guidelines:

- Add or update tests for every behavior change. Bug fixes should come with a test that fails without the fix.
- Existing tests run with Chinese as the default language and assert Chinese text. When you add user-facing text, add both the English and Chinese strings (see below); English assertions are welcome in new tests.
- Two tests guard the translations: one checks that `en` and `zh-CN` have exactly the same keys and placeholders, the other fails if non-test code contains hard-coded Chinese text.
- Database migrations: once a migration is on `main`, never edit it; add a new one.

## Commits and pull requests

- Keep each pull request focused on one change. Smaller pull requests get reviewed faster.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): description`, for example `fix(cli): handle expired device tokens`. Common types are `feat`, `fix`, `docs`, `refactor`, `test` and `chore`. English or Chinese are both fine.
- Link the issue the pull request addresses (`Fixes #123`).
- Fill in the pull request template: what changed, why, and how you tested it.
- Never commit secrets, tokens, internal URLs or customer data. Cohive scans issue content for secrets, but it cannot scan your commits.
- A maintainer reviews every pull request. External contributions are usually squash-merged, so the pull request title becomes the commit message.

### License of contributions

Cohive is licensed under the [Apache License 2.0](LICENSE). We do not ask for a CLA or a DCO sign-off: under section 5 of the license, any contribution you intentionally submit is licensed under the same terms, unless you explicitly state otherwise. Only submit work you have the right to contribute.

## Languages and translations

Cohive ships with English (`en`) and Simplified Chinese (`zh-CN`), maintained by the core team. Other languages are maintained by the community. English is the source language: translators work from the English strings.

Translations live in two places:

| Part | Files | Format |
|---|---|---|
| Server, CLI, MCP tool descriptions, install scripts | `internal/i18n/locales/<code>/*.json` | [go-i18n](https://github.com/nicksnyder/go-i18n) v2 JSON, flat dotted keys, placeholders like `{{.Name}}` |
| Web UI | `web/src/locales/<code>/*.json` | [i18next](https://www.i18next.com/) JSON v4, nested keys, placeholders like `{{name}}` |

### Changing or adding text (for all contributors)

- Never hard-code user-facing text. Add a key to the English file and the Chinese file in the same commit; the tests fail if the two differ.
- Use meaningful keys (`cli.login.done`), not the English sentence.
- Keep placeholders identical across languages. For text that depends on a number, use plural forms: `{"one": "...", "other": "..."}` in Go files, `key_one` / `key_other` in web files. Chinese only needs the `other` form.
- Server logs are English only and are not translated.

### Adding a new language

1. Pick the language code (BCP 47, for example `ja`, `pt-BR`, `zh-TW`).
2. Copy `internal/i18n/locales/en` to `internal/i18n/locales/<code>`, and `web/src/locales/en` to `web/src/locales/<code>`. Both directories must exist; a test checks that the server and the web UI offer the same languages.
3. Translate the values. Do not change keys or placeholders. Set `language.name` to the language's name in that language (for example `日本語`).
4. Keys you have not translated yet can be deleted; they fall back to English.
5. Add the language and yourself as its maintainer to [docs/i18n/LANGUAGES.md](docs/i18n/LANGUAGES.md).
6. Run the tests and open a pull request with the `translation` label.

### Maintaining a language

Community languages may fall behind when new text is added; missing strings show in English. Language maintainers are mentioned on pull requests that touch their language and are asked to review translation changes. If you can no longer maintain a language, say so in an issue so someone else can take over. A language with no maintainer and large gaps may be marked as incomplete in the release notes.

## Recognition

Everyone whose commits are merged appears in the contributor list in the README and in the "New Contributors" section of the release notes. Language maintainers and translators are listed in [docs/i18n/LANGUAGES.md](docs/i18n/LANGUAGES.md).

## Questions

If something in this guide is unclear or out of date, open an issue or a discussion. Improvements to this guide are contributions too.
