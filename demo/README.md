# Demo recording

`demo.gif` is rendered with [vhs](https://github.com/charmbracelet/vhs) from
`demo.tape`, against the fictional `ssh_config` here. The only host that
really answers is `web-1`, a disposable sshd in Docker:

```sh
ssh-keygen -q -t ed25519 -N "" -C demo -f keys/demo_ed25519
docker run -d --name berth-demo-ssh --hostname web-1 -p 127.0.0.1:2222:2222 \
  -e PUID=1000 -e PGID=1000 -e USER_NAME=deploy \
  -e PUBLIC_KEY="$(cat keys/demo_ed25519.pub)" linuxserver/openssh-server:latest
rm -f known_hosts          # so the recording shows the host-key question
vhs demo.tape
docker rm -f berth-demo-ssh
```

`keys/` and `known_hosts` are ignored by git.
