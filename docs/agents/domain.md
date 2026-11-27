# Domain Docs

How the engineering skills should consume this repo's domain documentation when
exploring the codebase. This repo is **single-context**: one `GLOSSARY.md` and
one `docs/adr/`, both at the root.

## Before exploring, read these

- **`GLOSSARY.md`** at the repo root: the project's vocabulary.
- **`docs/adr/`**: the decisions that touch the area you are about to work in.

If either does not exist yet, **proceed silently**. Don't flag their absence and
don't suggest creating them upfront. The `domain-modeling` skill creates them
lazily, when a term or a decision actually gets resolved.

## File structure

```
/
├── GLOSSARY.md
├── docs/adr/
│   ├── 0001-....md
│   └── 0002-....md
├── cmd/protonmailcli/
└── internal/
    ├── app/
    ├── bridge/
    ├── config/
    ├── model/
    ├── output/
    └── store/
```

There is no `GLOSSARY-MAP.md` and no per-package `docs/adr/`. If this repo ever
splits into several packages with their own vocabularies, switch to the
multi-context layout and record that change here.

## Use the glossary's vocabulary

When your output names a domain concept — an issue title, a refactor proposal, a
hypothesis, a test name — use the term as `GLOSSARY.md` defines it. Don't drift
to synonyms the glossary explicitly avoids.

This repo already has a settled vocabulary in its commands and its JSON
envelope: a *draft* is a message in the Drafts mailbox, a *mailbox* has a
canonical `id` and a `kind` of `system` or `custom`, a *tag* is an IMAP keyword,
a *filter* is the local engine's rule, and `createPath` and `sendPath` name the
route a mutation actually took. Prefer these over invented alternatives, and add
a term to `GLOSSARY.md` when you find yourself explaining one twice.

If the concept you need isn't in the glossary yet, that's a signal: either you
are inventing language the project doesn't use (reconsider), or there's a real
gap (note it for `domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than
silently overriding:

> _Contradicts ADR-0002 (land on master without pull requests), but worth
> reopening because…_
