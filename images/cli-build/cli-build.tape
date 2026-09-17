# VHS script for cli-build.gif. See record.sh.

# Paths starting with '/' must be quoted.
Output "/out/cli-build.gif"

Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 14
Set LineHeight 1.25
# Wide enough for the "Available environments" line.
Set Width 1240
Set Height 760
Set Padding 20
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set WindowBarSize 40
Set TypingSpeed 50ms
Set Framerate 24
# VHS regexes can't contain an escaped '/'.
Set WaitPattern /Idler [$] *$/
Set WaitTimeout 300s

Env COLORTERM truecolor

# No prompt color: it renders gray after the docker build output.
Hide
Type `export PS1="~/Idler $ "`
Enter
Type "clear"
Enter
Show

Sleep 300ms
Type "metaplay"
Sleep 700ms
Enter
Wait
Sleep 2s

Ctrl+L
Sleep 300ms
Type "metaplay build image"
Sleep 700ms
Enter
Wait
Sleep 4s
