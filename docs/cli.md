# RemoraSFTP CLI reference

All commands operate the same local engine used by the browser UI; there is
no separate CLI implementation. Commands that perform remote work load your
saved profiles (created via the UI or any earlier CLI use) and the same
credential vault and host-key trust store.

## Global flags

- `--data-dir=<path>` - use an alternative application data directory
  (equivalent to the `SFTPBOX_DATA_DIR` environment variable).

## start

```
remorasftp start [--addr 127.0.0.1] [--port 0] [--no-browser] [--headless]
```

Starts the engine and serves the embedded UI. A random loopback port is used
unless `--port` is provided. The terminal prints the one-time bootstrap URL,
the credential backend in use, and the data directory. Press Ctrl+C to stop
cleanly.

- `--no-browser` - serve but don't launch the default browser.
- `--headless` - intended for automation; never launches a browser.

Launching the executable with no arguments is equivalent to `start` with the
browser opening automatically.

## status

```
remorasftp status
```

Reports whether an engine instance is currently running (using the local
`instance.json` file) and where.

## version

```
remorasftp version
```

Prints semantic version, commit, build time, Go version, and platform.

## profiles

```
remorasftp profiles
```

Lists saved connection profiles (name, protocol, host, username). Secrets are
not displayed.

## connect

```
remorasftp connect <profile-name-or-id>
```

Establishes a connection, verifies host identity, and prints protocol,
encryption state, and start directory. A first connection to an unknown host
reports the fingerprint and asks you to confirm it via the browser UI; the
CLI never auto-accepts.

## ls

```
remorasftp ls <profile> [path]
```

Lists a remote directory with permissions, size, and modification time.

## put / get

```
remorasftp put <profile> <local-path> <remote-path>
remorasftp get <profile> <remote-path> <local-path>
```

Stream uploads/downloads through the same transfer manager as the UI, with a
live progress indicator. Downloads use an atomic `.remorasftp-part` file that is
renamed only on success.

## mkdir / rm / mv

```
remorasftp mkdir <profile> <remote-path>
remorasftp rm [-r] <profile> <remote-path>
remorasftp mv <profile> <from> <to>
```

`-r` removes directories recursively.

## Using the CLI with a running GUI

One-shot commands build the engine **in-process without opening the HTTP
listener** (they call the same Go services directly rather than going over
the network). They share the same on-disk profiles, credentials, and trust
store as the GUI, so a profile created in the browser is usable from the CLI
immediately. The `start` command is the only one that binds `127.0.0.1` and
serves the web UI.
