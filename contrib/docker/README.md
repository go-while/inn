# INN in a container

`Dockerfile` builds INN from this source tree on Debian and runs `innd` in the
foreground. It's meant for testing and development, for example as a real INN to
run a news client or feeder against. It isn't a hardened production setup.

Build from the top of the tree (Docker or Podman):

```sh
docker build -f contrib/docker/Dockerfile -t inn .
docker run --rm -p 1119:119 -e INN_GROUPS="local.test,local.misc" inn
```

A reader can then connect to port 1119 of the host.

## Configuration

The entry point prepares the site on every start:

| variable | effect |
|---|---|
| `INN_PATHHOST` | `pathhost`, and `domain` after its first dot. Default: the container's host name, with `.invalid` appended when it has no dot |
| `INN_GROUPS` | newsgroups to create, separated by commas or spaces; existing ones are kept |
| `INN_READERS` | hosts that may read, post and use NEWNEWS (`readers.conf`). Default `*`: **anybody who can reach the port** |
| `INN_PEERS` | hosts that may feed articles with IHAVE or streaming (`incoming.conf`). Default: none |

On the first start, the entry point writes `readers.conf`, `incoming.conf` and a
`newsfeeds` that only has the `ME` entry, then leaves a marker
(`etc/.docker-generated`) so it doesn't rewrite them later. To use your own
files, mount them over `/usr/local/news/etc/` together with that marker.

`/usr/local/news/spool`, `/usr/local/news/db` and `/usr/local/news/log` are
volumes. Empty volumes get a minimal `active` file (control, control.cancel,
junk and `INN_GROUPS`) and an empty history database.

## Stopping

`docker stop` sends SIGTERM, and `innd` shuts down cleanly. That includes an
idle `innd`: before the fix in this tree, it only noticed the signal at the next
connection or timeout, so `docker stop` ended up killing it with SIGKILL after
10 seconds.

INN logs its notices to syslog, and the image has no syslog daemon. Mount
`/dev/log` from the host, or add a syslog daemon, to see them.
