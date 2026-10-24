# Source from your shell rc: source ~/clai-shell.sh
alias ai='clai q'                                 # ask once
alias ask='clai -dre q'                           # continue this directory's chat
alias fix='clai -l -t "ls,cat,pwd,go,apply_patch" -check "go test ./..." q'
