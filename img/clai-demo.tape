# Records the end-to-end showcase embedded from img/clai-demo.gif.
# Run from the repository root: bash img/clai-demo/record.sh
# In a container that cannot run Chrome's sandbox, prefix the script with
# VHS_NO_SANDBOX=true.
# Six model calls: one loop run that must pass `go test`, one image generation
# plus the -re follow-up that reads it, two -rf calls that jev answers (one
# inline and one that pipes data in and the answer out to jq), and one
# continuation with -cm.
#
# The tape writes an MP4: VHS renders a GIF through palettegen, which keeps one
# colour histogram per captured frame, and two earlier runs of this tape died
# when ffmpeg reached 8-10 GB. record.sh encodes the GIF with a sampled palette
# instead.
Output img/clai-demo.mp4

Set Shell "bash"
Set FontSize 20
Set Width 1600
Set Height 1000
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Framerate 10
Set TypingSpeed 40ms
Set CursorBlink false

Hide
Type "bash img/clai-demo/setup.sh"
Enter
Type "export HOME=$HOME/.cache/clai-demo/home"
Enter
Type "export CLAI_CONFIG_DIR=$HOME/.config/.clai CLAI_CACHE_DIR=$HOME/.cache/clai"
Enter
Type "cd $HOME/work"
Enter
Type "export PS1='~/work $ '"
Enter
Type "clear"
Enter
Sleep 1s
Show

Hide
Type "bash /tmp/clai-note.sh '1 · three aliases are the whole shell integration'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "source ~/clai-shell.sh"
Enter
Sleep 2s

Type "alias ai ask fix"
Enter
Sleep 4s

Hide
Type "bash /tmp/clai-note.sh '2 · the goal: this test fails'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "go test ./..."
Enter
Sleep 4s

Hide
Type "bash /tmp/clai-note.sh '3 · -l iterates until -check exits 0'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "fix 'make the failing Add test pass without editing it'; echo loop=$?"
Enter
Wait+Screen@3m /loop=0/
Sleep 5s

# --- image: generate, then look at what was made ---
Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '4 · generate_image draws a picture'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "clai -t generate_image q 'Draw a minimal orange triangle on a dark navy background.'; echo gen=$?"
Enter
Wait+Screen@2m /gen=0/
Sleep 8s

Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '5 · -re asks again; the model sees the image it drew'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "clai -re q 'In one line: what shape and colour did you draw?'; echo view=$?"
Enter
Wait+Screen@2m /view=0/
Sleep 8s

# --- jev: fixed questions, JSON in and out ---
# Clear first: VHS keeps no scrollback, so a screen already full of earlier acts
# frames the new lines below the visible area and Wait+Screen never sees them.
Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '6 · -rf turns jev questions into JSON'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "cat classify.json"
Enter
Sleep 5s

Type "clai -cm jev-latest -rf classify.json q 'What kind of file is go.mod?'"
Enter
Wait+Screen@1m /jev-1/
Sleep 5s

Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '7 · pipe data in; pipe the answer out to jq'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "cat demo_test.go | clai -cm jev-latest -rf classify.json q '{}' | jq -r .answers.kind.choice; echo kind=$?"
Enter
Wait+Screen@1m /kind=0/
Sleep 6s

# --- chat: list, bind, continue, replay ---
Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '8 · every run is stored; [d] keeps this directory'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "clai c l; echo list=$?"
Enter
Wait+Screen@30s /convs: off/
Sleep 4s

Type "d"
Enter
Wait+Screen@15s /convs: on/
Sleep 4s

# Row 3 is the loop run. The list is newest first, and the two image calls
# share one group row, so this recording's own runs take rows 0 to 2.
Type "3"
Enter
Wait+Screen@15s /Chat info/
Sleep 3s

Enter
Sleep 10s

# VHS reads only the first `rows` lines of the buffer (no scrollback), so a
# long transcript hides every later line from Wait+Screen. Clear before the
# next wait: /ask=0/ then sits near the top of a fresh screen.
Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '9 · -dre continues this chat; -cm swaps the model'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "ask -cm deepseek-v4-flash 'In one line: what changed and why?'; echo ask=$?"
Enter
Wait+Screen@2m /ask=0/
Sleep 6s

Hide
Type "clear"
Enter
Sleep 500ms
Type "bash /tmp/clai-note.sh '10 · same chat, the Model column changed'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "clai c l; echo done=$?"
Enter
Wait+Screen@30s /convs: off/
Sleep 3s

Type "d"
Enter
Wait+Screen@15s /convs: on/
Sleep 6s

Type "q"
Enter
Wait+Screen@15s /done=0/
Sleep 2s

Hide
Type "bash /tmp/clai-note.sh '11 · replay prints the last reply, no model call'"
Enter
Sleep 300ms
Show
Sleep 1s

Type "clai replay; echo replay=$?"
Enter
Wait+Screen@15s /replay=0/
Sleep 6s
