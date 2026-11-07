# guardenv

Commit your `.env` secrets to git safely: they are stored encrypted (AES-256-GCM) as `guarded.env` files, and the plaintext `.env` stays git-ignored.

## Install

    go install github.com/yantratmika/guardenv@latest

Make sure `guardenv` is on your `PATH` (the git hooks call it).

## Usage

    guardenv init      # in a repo: finds .env files, updates .gitignore, creates the secret, writes .guardenv/config, installs hooks
    guardenv encrypt   # .env -> guarded.env   (runs automatically on pre-commit, and stages the result)
    guardenv decrypt   # guarded.env -> .env   (runs automatically after checkout / pull / merge)

## Git hooks

`guardenv init` installs the hooks for you. To (re)install them, for example after a fresh clone, run:

    guardenv install-hooks

This adds the following to `.git/hooks/` (or your configured hooks path):

| Hook            | Runs                           | Purpose                                         |
|-----------------|--------------------------------|-------------------------------------------------|
| `pre-commit`    | `guardenv encrypt --stage`     | encrypt `.env` and add `guarded.env` to the commit |
| `post-checkout` | `guardenv decrypt`             | restore `.env` after clone / branch switch      |
| `post-merge`    | `guardenv decrypt`             | restore `.env` after `git pull`                 |

Notes:
- Hooks are local to each clone and are not committed, so every teammate must run `guardenv install-hooks` once per clone.
- If a hook file already exists, guardenv appends its lines (marked `# guardenv-managed`) and leaves your existing content alone. Running the command again does not duplicate them.
- `guardenv` must be on your `PATH`. If it isn't, a failing `pre-commit` blocks the commit, which stops plaintext secrets from slipping through unencrypted. The `post-*` hooks fail silently.
- To remove guardenv, delete the `# guardenv-managed` lines from the hook files.
- To set the hooks up manually, add the commands from the table to the matching files in `.git/hooks/` and make them executable (`#!/bin/sh` at the top).

## Files

- `.guardenv/config` (committed) names the secret and lists the guarded files.
- The secret lives in `~/.guardenv/<name>` (never committed). Share it with teammates over a secure channel; after a fresh clone they save it there and run `guardenv install-hooks && guardenv decrypt`.
- `.env` -> `guarded.env`, `.env.local` -> `guarded.env.local`, in the same directory.
- `decrypt` won't overwrite a locally modified `.env` unless you pass `--force`.
