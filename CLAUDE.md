# Code comments

Write comments like a human dev jotting a quick note, not like documentation.

- Single line only. Don't wrap a comment across multiple lines unless the thing is genuinely too complex to say in one sentence.
- Keep it short and plain. No formal tone, no "Note:" prefixes, no " - " hyphen clauses stacked together, no restating what the code already makes obvious.
- Apply this whenever you write or edit comments in this repo, not just when asked to clean up comments specifically.

# Git

- After making changes, `git add` them and stop there. Don't `git commit` - the user commits themselves.

# Heavy commands

Anything new that's disk or CPU heavy (du over volumes, scans, dumps, archive work) runs through `lowPrio(idle, name, args...)` in `internal/handlers/lowprio.go`: `idle` true adds `ionice -c3` for whole-tree scans and background work, false gives only `nice -n19` for quick things the admin is waiting on. Don't hand-roll nice/ionice or use `ionice -c2 -n7`, mq-deadline ignores best-effort levels. Heavy work that's really an opencli command belongs in opencli, which has its own `sh.LowPrio`.
