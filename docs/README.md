# MediaBrain docs

What has been built and why. Kept short on purpose: the code and the GraphQL schema are the
detail, these pages are the reasoning.

| Page | Contents |
|---|---|
| [architecture.md](architecture.md) | Layers, transactions, errors, dataloaders, testing approach |
| [data-model.md](data-model.md) | The entities in use and why they are shaped this way |
| [api.md](api.md) | The GraphQL surface per area, with examples |
| [security.md](security.md) | Sign-in, permission rules, administrators, hardening, known limits |
| [log.md](log.md) | Dated entries: what changed and why |

## Keeping this current

Update these pages in the same change as the code (see `AGENTS.md`):

- behaviour or a rule changed: fix the page that describes it,
- something was added or decided: add a dated entry to `log.md` (newest first, a few lines:
  what, why, and what was deliberately left out).
