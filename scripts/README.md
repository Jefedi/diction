# `diction-key` — token management CLI

A small host-side helper to manage the Diction gateway's API tokens without
editing `tokens.txt` by hand. The gateway hot-reloads the file, so the CLI just
edits it cleanly and the changes take effect on their own — **create/delete are
live within a few seconds, and revocation is immediate**. No gateway restart.

It is a **host tool**: it edits the token file on disk. Do **not** run it inside
the container — the file is bind-mounted read-only there, and the container must
not modify its own auth file.

## Token file

The gateway reads `token:name` entries, one per line (blank lines and `#`
comments ignored). The CLI targets:

```
/srv/docker/dockerhand/dockhand_data/stacks/ax42/diction/config/tokens.txt
```

Override with the `DICTION_TOKENS_FILE` environment variable (used by the tests,
and handy on any other host):

```bash
DICTION_TOKENS_FILE=/path/to/tokens.txt diction-key list
```

The file is owned by root on the server, so the CLI needs write access — run it
with **`sudo`** on AX42.

## Install

```bash
sudo install -m 755 scripts/diction-key /usr/local/bin/diction-key
```

## Commands

| Command | Description |
| --- | --- |
| `diction-key list` | List tokens: name + **masked** token (first 8 chars `…` last 4). |
| `diction-key create <name>` | Generate a token (`openssl rand -hex 32`) for `<name>`, append it, and print it **once in clear**. Refuses an existing name. |
| `diction-key show <name>` | Print the full token for `<name>`. |
| `diction-key delete <name>` | Remove `<name>`'s line (immediate revocation). Errors if `<name>` is unknown. |
| `diction-key` / `-h` / `--help` | Show help. |

`<name>` may contain letters, digits, `_` and `-` only. A `:`, a space, or any
special character is rejected (it would break the `token:name` format).

### Examples

```bash
# Create a token for a new user (printed once — copy it now)
$ sudo diction-key create alice
Created token for alice — copy it now, it is shown only once:

  9f2c…full 64-hex token…8e4a

The gateway will pick it up automatically within a few seconds.

# List everyone, tokens masked
$ sudo diction-key list
NAME                     TOKEN
jefe                     d3422d31…25e4
alice                    9f2c1a70…8e4a

# Reveal one token in full
$ sudo diction-key show alice
9f2c1a70…full token…8e4a

# Revoke (takes effect within seconds)
$ sudo diction-key delete alice
Revoked token for alice. The gateway drops it within a few seconds.
```

The client then authenticates with that token — `Authorization: Bearer <token>`
on REST, or `wss://host/v1/audio/stream?token=<token>` on the WebSocket.

## Inode safety (why no `mv` / `sed -i`)

The container bind-mounts the **file**, so an edit that swaps the inode
(`mv newfile tokens.txt`, or GNU `sed -i`, which renames a temp file over the
original) would detach the container from the live file and break hot-reload.

The CLI therefore edits **in place, preserving the inode**:

- `create` **appends** (`>>`).
- `delete` filters the content and rewrites it with `cat tmp > tokens.txt`,
  which truncates the existing inode instead of replacing it.

## Tests

`scripts/test-diction-key.sh` is a pure-bash harness (no bats needed). It runs
without root against a temporary file — it never touches the real AX42 file —
and verifies well-formed output, masking, duplicate/invalid-name rejection,
correct deletion, and that **the inode is unchanged after create and delete**
(`stat -c %i`).

```bash
bash scripts/test-diction-key.sh
```

Requires GNU coreutils (`stat -c`) and `openssl` — both present on Debian.
