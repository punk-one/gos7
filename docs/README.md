# Documentation

The root [README](../README.md) is the canonical public project overview and API
compatibility statement. The [implementation specification](spec.md) is the
detailed, version-controlled source of truth for maintainers and LLMs working on
the current codebase. Runnable examples and real-PLC qualification are maintained
with their code so that commands cannot drift from implementation.

## Guides

- [Current implementation and release specification](spec.md)
- [Runnable examples](../examples/README.md)
- [Real PLC qualification](../test/qualification/README.md)
- [Changelog](../CHANGELOG.md)

## Translations

| Locale | Translation | Synchronized baseline |
| --- | --- | --- |
| `zh-CN` | [简体中文](i18n/zh-CN/README.md) | `v0.1.0`, 2026-08-21 |

Translation directories use BCP 47 locale tags, for example `zh-CN`, `ja-JP`,
and `pt-BR`. Add future languages as `docs/i18n/<locale>/README.md`; do not add
localized README files to the repository root.

The English root README is the semantic source of truth. A translation must:

1. identify its locale and synchronized release/commit near the top;
2. preserve safety, platform, write-outcome, and release-status statements;
3. use repository-relative links that are valid from its locale directory;
4. update this index when added or synchronized.

Translations may improve wording for their audience, but they must not claim
features, controller qualification, or compatibility absent from the English
source.
