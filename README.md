<p align="center">
<pre align="center">
██████╗ ███████╗██████╗ ████████╗██╗  ██╗
██╔══██╗██╔════╝██╔══██╗╚══██╔══╝██║  ██║
██████╔╝█████╗  ██████╔╝   ██║   ███████║
██╔══██╗██╔══╝  ██╔══██╗   ██║   ██╔══██║
██████╔╝███████╗██║  ██║   ██║   ██║  ██║
╚═════╝ ╚══════╝╚═╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝
      ~  ~~   ~   ssh, moored in ~/.ssh/config   ~   ~~  ~
</pre>
</p>

<p align="center">
<a href="https://go.dev"><img alt="Go 1.27+" src="https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go&logoColor=white"></a>
<a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-8aadf4"></a>
<img alt="macOS · Linux" src="https://img.shields.io/badge/macOS%20%C2%B7%20Linux-terminal-2e3440">
<a href="https://sl0p.foo"><img alt="slosh" src="https://img.shields.io/badge/pairs%20with-slosh%20%C2%B7%20Ghostty-ff5fd7"></a>
</p>

<p align="center"><b>The SSH client you wanted on the desktop, living in your terminal, with no config of its own.</b></p>

<p align="center"><img src="demo/demo.gif" alt="berth: browsing hosts, editing a stanza, connecting through the connection screen" width="900"></p>

Berth is a host list over `~/.ssh/config`: pick a host, see what `ssh` will
*actually* do for it, connect through a clean connection screen, and keep
every session as a pane of your multiplexer. No database, no import, no
second file to keep in sync. Your `ssh_config` is the only source of truth,
and berth edits it in place without touching a byte it does not understand.

```
╭─ berth ──────────────────────────────╮ ╭─ web-1 ─────────────────────────────────────────╮
│ / filter                             │ │ web-1                                           │
│                                      │ │ deploy@203.0.113.10                             │
│ ▾ #prod  2                           │ │ #prod                                           │
│   ● web-1   deploy@203.0.113.10      │ │                                                 │
│   ● db-1    deploy@203.0.113.11 ⧉1   │ │ auth     1Password agent · id_ed25519_sk as f…  │
│ ▾ #homelab  2                        │ │ term     xterm-256color  override · i installs… │
│   ● nas     admin@192.168.1.20       │ │ reach    ● up  18ms to 203.0.113.10:22          │
│   ○ pi      pi@192.168.1.21          │ │ sessions none open · t s v f open one           │
│ ▾ untagged  1                        │ │                                                 │
│   ● bastion ops@bastion.example.org  │ │ ~/.ssh/config:14                                │
│ ▾ defaults  1                        │ │ Host web-1                                      │
│   · *       defaults                 │ │     HostName 203.0.113.10                       │
╰──────────────────────────────────────╯ ╰─────────────────────────────────────────────────╯
 enter connect  t tab  s split  v vsplit  f float  o jump  a add  e edit  d del  # groups  …
```

## Why

- **One file.** Desktop SSH clients keep their own host database next to the
  `ssh_config` you already maintain. Berth reads yours, and writes back only
  the stanza you edited, with its indentation, comments and everything else
  left byte for byte.
- **Keys and agents, never passwords.** 1Password, a YubiKey, an agent, a key
  file: berth names which one will answer, and the connection screen tells
  you when to touch or approve.
- **Built for a multiplexer.** Inside [slosh](https://sl0p.foo) a host opens
  as a tab, a split or a float; berth counts the open sessions per host,
  jumps to them, and wears slosh's theme, following it live.

## What it does

- **Reads your real config.** Every `Host` stanza, `Include`d files, tags
  from a `# tags:` comment, and the *effective* values from `ssh -G`: user,
  port, jump host, forwards, and which key or agent will answer.
- **Shows the auth story in plain words**: `1Password agent`, `YubiKey ·
  id_ed25519_sk`, `key file · deploy_key`, or what your `Host *` inherits.
- **Probes reachability** with a TCP dial, no key and no touch involved.
  Hosts behind a `ProxyJump` or `ProxyCommand` are not dialled at all.
- **Connects on its own screen.** The host, then four steps with what is
  really going on: `touch your YubiKey`, `approve in 1Password`, a host key
  fingerprint to accept, a masked PIN field. Warnings such as a missing
  post-quantum key exchange are one line, not three. Then the terminal is
  handed over untouched, on a blank screen.
- **Lives in your multiplexer.** Inside [slosh](https://sl0p.foo) a host opens
  as a new tab, a split or a float; berth counts open sessions per host and
  jumps to them. It wears slosh's current theme, and follows it live.
- **Edits `~/.ssh/config` surgically.** Add, edit and delete stanzas from a
  form; only the lines you changed are rewritten, with the stanza's own
  indentation. Comments, blank lines, `Match` blocks and directives berth
  does not know about survive byte for byte.
- **Helps with Ghostty.** One key installs `xterm-ghostty` terminfo on a host
  and offers to drop the `SetEnv TERM=xterm-256color` workaround.

## Install

```sh
brew install axel-eck/tap/berth          # macOS and Linux, via Homebrew
go install github.com/axel-eck/berth@latest
```

Prebuilt binaries for macOS and Linux (amd64, arm64) are on the
[releases page](https://github.com/axel-eck/berth/releases), with checksums.

Needs Go 1.27+ to build, OpenSSH 8.2+ at runtime, and a Unix-like system
(macOS, Linux). slosh and Ghostty are optional: without them berth is still a
complete client, it just loses the pane spawning and theme sync.

> **macOS and security keys.** Apple's `/usr/bin/ssh` is built without FIDO
> support, so it cannot use `*_sk` (YubiKey) keys. Install OpenSSH from
> Homebrew and keep it first on your `PATH`; berth uses the `ssh` your shell
> would, and carries it into every pane it spawns.

## Use

```sh
berth                    # the host list
berth connect <alias>    # one session, then exit (what spawned panes run)
berth ls                 # aliases, one per line, for scripts and fzf
berth -config FILE       # another ssh_config, used for listing and connecting (-F)
```

`BERTH_SPLASH=0` skips the greeting animation.

### Keys

| key | does |
|---|---|
| `enter` | connect here; the list comes back when the session ends |
| `t` · `s` · `v` · `f` | open in a new slosh tab · split below · split beside · float |
| `o` | jump to the host's open session |
| `a` · `e` · `d` | add · edit · delete a stanza |
| `E` | open the config in `$VISUAL` (or `$EDITOR`) at the selected host |
| `M` · `X` · `Y` | open a control master socket · close it · copy `ssh -S <socket> <alias>` |
| `i` | install `xterm-ghostty` terminfo on the host |
| `y` | copy `ssh <alias>` to the clipboard (OSC 52, works through a multiplexer) |
| `/` | filter by alias, target, `#tag` or comment |
| `#` | group hosts under their first tag; `space` on a header folds it |
| `r` | reload the file and re-probe |
| `?` | help |

`● up  ○ unreachable  ↗ behind a jump host  ⧉ open sessions  ⌁ control socket  ⎘ from an included file`

### The connection screen

```
╭─ db-1 ──────────────────────────────────────────────╮
│                                                     │
│   deploy@203.0.113.11                               │
│   #prod · YubiKey · id_ed25519_sk                   │
│                                                     │
│   ✓ connected                                       │
│   ✓ host key known  SHA256:nWMyJVx9zEGElfFCrw0Hok…  │
│   ⠹ touch your YubiKey  ED25519-SK SHA256:FBzDGXx…  │
│   · shell                                           │
│                                                     │
│   PIN · ED25519-SK id_ed25519_sk  ••••••            │
│                                                     │
│   server OpenSSH_9.9                                │
│   ! no post-quantum key exchange: the server sho…   │
│                                                     │
│   4s · esc cancels                                  │
╰─────────────────────────────────────────────────────╯
```

A failed connection stays on screen with the reason; `enter` retries. In a
pane berth spawned, the pane closes when the session ends (or a few seconds
after a failure), so dead panes do not pile up.

When the host will sign with a FIDO key (an `id_*_sk` file on disk among
its identities, even as the fallback behind an agent) and none is plugged
in, the card says `plug in your YubiKey` and starts ssh the moment one
appears, instead of letting ssh fail with `signing failed` after the touch
step. `enter` tries without it, for when the agent has a key the server
takes. The check uses `fido2-token -L` (from libfido2, which FIDO-capable
OpenSSH builds depend on) or `ykman list`, whichever is on `PATH`; with
neither, ssh is started straight away.

The key also ends what it opened. On a production host (see
[environments](#environments-never-mistake-prod-for-staging)) pulling the
key kills the session within a second, and while control sockets are open
berth keeps watching: the key going away closes every master. Both only
arm when the key was there to begin with, so a session or socket opened
without it (`enter` on the waiting card) is left alone.

### Editing

The form has alias, hostname, user, port, jump host, an auth preset, key
file, a TERM override and tags. Presets are plain directives:

| preset | writes |
|---|---|
| inherit | nothing: your `Host *` defaults apply |
| 1Password | `IdentityAgent` pointing at the 1Password socket |
| YubiKey | `IdentityFile ~/.ssh/<key>_sk`, `IdentitiesOnly yes`, `IdentityAgent none` |
| key file | `IdentityFile <path>`, `IdentitiesOnly yes`, `IdentityAgent none` |

There is no password preset, and berth never stores a secret: a PIN or
passphrase typed on the connection screen goes straight to ssh's tty and is
cleared from the field. A secret field only appears when ssh's own log says
ssh is asking; a remote banner that imitates a prompt is shown as text, with
the lines before it, never as a field.

Saves are atomic: a sibling temp file is written and fsynced, checked with
`ssh -G -F` so a line ssh would refuse never lands, then renamed over the
real file (the symlink target, if your config is a symlink into a dotfiles
repo). A save is refused if the file changed on disk since it was loaded.
If [chezmoi](https://www.chezmoi.io) manages the file, berth runs `chezmoi
re-add` after each save so your dotfiles source does not drift.

Hosts from `Include`d files are listed and connectable but read-only.
Stanzas made only of wildcards (`Host *`) show as *defaults* and open in
`$EDITOR`.

### Control sockets

`M` opens a control master for the selected host: `ssh -M -S <socket> -f -N`
through the same connection screen, so the YubiKey touch or PIN is asked
once, and ssh stays in the background. The socket defaults to
`/tmp/ssh-<alias>` and can be changed before opening. A host with a live
socket shows `⌁`; `Y` copies `ssh -S <socket> <alias>` for a tool or an
agent to ride the connection without authenticating again, and `X` closes
the master (`-O exit`). Berth's default socket path is probed at start, so a
master left by an earlier run is picked up.

### Environments: never mistake prod for staging

A host whose tags include `prod`, `staging` or `dev` (or their usual
spellings: production, stage, preprod, uat, test, ...) gets an environment,
and a `# env: prod` comment in the stanza sets one explicitly. Production is
red, staging amber, dev green, in every theme, and the cue stays on for the
whole session, not just while connecting:

- in the list and the detail pane, a `PROD` badge beside the alias;
- on the connection screen, a red frame titled `PROD · db-1`;
- inside slosh, the pane is renamed `PROD · db-1` and its status line says
  `⚠ PROD · db-1` until ssh exits, visible from every other tab. With
  `in_band_shaders true` in your slosh config, the pane's frame lines are
  tinted red as well. And when the pane has room (100×26 or more), a
  rotating 3D warning sign floats in its bottom-right corner for the whole
  session, in a small pane of its own so the shell is never touched;
- outside slosh, the terminal background is tinted (OSC 11) for the
  session and restored afterwards.

### Tags and groups

A `# tags: prod, eu` comment inside a stanza tags it. With any tag present
the list groups hosts under their first tag, then `untagged`, then
`defaults`. `#` switches to a flat list; `/#prod` filters by tag.

## How it works

Berth runs `ssh -v -E <tempfile> <alias>` inside a pty it owns. ssh's log
(debug steps, warnings, `Permission denied`) goes to the temp file, which
berth tails for progress; only genuine questions (host key, PIN, passphrase)
and remote output reach the pty. When ssh logs `Entering interactive
session` (or reuses a `ControlMaster` connection) berth puts the terminal in
raw mode, replays whatever the remote already printed, and relays bytes both
ways, resizing the pty on `SIGWINCH`, until ssh exits. The temp log is
removed when the session ends.

Inside slosh, sessions are spawned through the control socket as panes that
were *given* `berth connect <alias>` as their command, carrying the purpose
`berth:<alias>` so they can be found again. The palette comes from `slosh
--dump-theme` plus the theme the session wears right now.

## What berth touches

- `~/.ssh/config` (or `-config`): read always, written only when you save a
  form or delete a stanza, atomically with mode 0600.
- A temp file for ssh's log during a connection, mode 0600, unlinked as soon
  as ssh has it open.
- TCP connections to each host's port for the reachability dot. Nothing is
  sent on them, and hosts behind a jump host or `ProxyCommand` are skipped.
- The slosh control socket, when running inside slosh.
- `fido2-token -L` or `ykman list`, before connecting to a host that has a
  FIDO key, and once a second during a production session or while control
  sockets are open, to see whether the key is still plugged in.
- `chezmoi re-add` on the config, only if chezmoi already manages it.

## License

MIT. See [LICENSE](LICENSE).
