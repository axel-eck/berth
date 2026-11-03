# berth demo. Rendered with vhs (https://github.com/charmbracelet/vhs):
#   vhs demo.tape
# Needs the disposable sshd container on 127.0.0.1:2222 and a key in keys/ (see README.md).
Output demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1180
Set Height 660
Set Padding 14
Set Theme "Catppuccin Macchiato"
Set TypingSpeed 55ms
Set Framerate 30

Env PS1 "❯ "
Env TERM "xterm-256color"
Env SLOSH "0"
Env SLOSH_SESSION ""


Type "berth -config ssh_config"
Sleep 600ms
Enter
Sleep 2800ms

Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 700ms

Type "/"
Sleep 300ms
Type "pi"
Sleep 1000ms
Escape
Sleep 600ms

Type "#"
Sleep 1100ms
Type "#"
Sleep 700ms

Type "e"
Sleep 1800ms
Escape
Sleep 500ms

Type "?"
Sleep 1800ms
Escape
Sleep 500ms

Type "/"
Sleep 300ms
Type "web"
Sleep 600ms
Enter
Sleep 700ms
Enter
Sleep 1800ms
Type "y"
Sleep 2600ms

Type "uname -sr"
Enter
Sleep 1500ms
Type "exit"
Enter
Sleep 2200ms

