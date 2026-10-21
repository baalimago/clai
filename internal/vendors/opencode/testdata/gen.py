"""Generate the opencode fixture.

Run from this directory:

    python3 gen.py

The fixtures are checked in. Regenerate only when the reader's contract changes,
and keep the tests in step with the data.

opencode.db           the conversation shapes: titles, prompts out of seq order,
                      settled and unsettled tool parts, skipped sessions
drift.db              a rebuilt schema: a reordered column and a missing column
foreign.db            a valid SQLite file without the opencode tables
opencode-wal.db(+wal) a session that exists only in the write-ahead log

The table definitions copy `sqlite_master` from a real opencode database, since
the reader addresses session_v2 by column position. Inspect a generated fixture
only through a copy: the sqlite3 CLI checkpoints and deletes a -wal sidecar on
close. The WAL fixture is therefore copied while its source connection is still
open.
"""

import json
import os
import shutil
import sqlite3
import tempfile

here = os.path.dirname(os.path.abspath(__file__))


def fresh(name):
    p = os.path.join(here, name)
    for suffix in ("", "-wal", "-shm"):
        if os.path.exists(p + suffix):
            os.remove(p + suffix)
    return p


def create_schema(c):
    c.executescript("""
create table `session_v2` (
  `id` text PRIMARY KEY,
  `project_id` text NOT NULL,
  `workspace_id` text,
  `parent_id` text,
  `fork_session_id` text,
  `fork_boundary` text,
  `slug` text NOT NULL,
  `directory` text NOT NULL,
  `path` text,
  `title` text,
  `version` text NOT NULL,
  `share_url` text,
  `summary_additions` integer,
  `summary_deletions` integer,
  `summary_files` integer,
  `summary_diffs` text,
  `metadata` text,
  `cost` real DEFAULT 0 NOT NULL,
  `tokens_input` integer DEFAULT 0 NOT NULL,
  `tokens_output` integer DEFAULT 0 NOT NULL,
  `tokens_reasoning` integer DEFAULT 0 NOT NULL,
  `tokens_cache_read` integer DEFAULT 0 NOT NULL,
  `tokens_cache_write` integer DEFAULT 0 NOT NULL,
  `revert` text,
  `permission` text,
  `agent` text,
  `model` text,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `time_idle` integer,
  `time_viewed` integer,
  `idle_outcome` text,
  `time_compacting` integer,
  `time_archived` integer,
  `time_suspended` integer,
  `resume_attempts` integer DEFAULT 0 NOT NULL
);
create table `session_message` (
  `id` text PRIMARY KEY,
  `session_id` text NOT NULL,
  `type` text NOT NULL,
  `seq` integer NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `data` text NOT NULL
);
""")


path = fresh("opencode.db")
c = sqlite3.connect(path)
create_schema(c)

BORN = 1792513387087


def session(sid, parent=None, title=None, directory="/work/clai", model=None, born=None):
    c.execute(
        "insert into session_v2 (id, project_id, parent_id, slug, directory, title,"
        " version, model, time_created, time_updated) values (?,?,?,?,?,?,?,?,?,?)",
        (
            sid,
            "proj-1",
            parent,
            sid,
            directory,
            title,
            "0.0.1",
            json.dumps(model) if model else None,
            born or BORN,
            born or BORN,
        ),
    )


seq = [0]


def message(sid, kind, data, at=None):
    seq[0] += 1
    c.execute(
        "insert into session_message (id, session_id, type, seq, time_created,"
        " time_updated, data) values (?,?,?,?,?,?,?)",
        ("msg_%04d" % seq[0], sid, kind, seq[0] if at is None else at, BORN, BORN, json.dumps(data, separators=(",", ":"))),
    )


def tool(status, name, call_id, **state):
    return {
        "type": "tool",
        "id": call_id,
        "name": name,
        "state": {"status": status, **state},
    }


# ses_main: title, model, reasoning, a settled call, a failed call and a tail.
session("ses_main", title="Ship the slurp", model={"id": "m1", "providerID": "prov"})
message("ses_main", "user", {"time": {"created": BORN}, "text": "hello opencode", "files": [], "agents": []})
message(
    "ses_main",
    "assistant",
    {
        "time": {"created": BORN},
        "agent": "build",
        "content": [
            {"type": "text", "text": "looking around"},
            {"type": "reasoning", "text": "the repo is small"},
            tool("completed", "shell", "call_1", input={"command": "ls"}, content=[{"type": "text", "text": "out"}]),
            tool("error", "question", "call_2", input={"q": 1}, error={"type": "aborted", "message": "dismissed"}),
        ],
    },
)
message("ses_main", "assistant", {"time": {"created": BORN}, "content": [{"type": "text", "text": "done"}]})

# ses_untitled: no title and no model, so the preview falls back to the prompt.
session("ses_untitled", directory="", born=BORN - 60000)
message("ses_untitled", "user", {"text": "first prompt wins"})

# ses_child: a subagent session, never a user conversation.
session("ses_child", parent="ses_main", title="child agent", born=BORN - 60000)
message("ses_child", "user", {"text": "child prompt"})

# ses_empty: no messages at all.
session("ses_empty", title="empty", born=BORN - 60000)

# ses_noise: only bookkeeping messages, so nothing to clone.
session("ses_noise", title="noise", born=BORN - 60000)
message("ses_noise", "compaction", {"status": "completed", "reason": "auto", "summary": "s"})
message("ses_noise", "idle", {"outcome": "succeeded"})
message("ses_noise", "assistant", {"content": []})

# ses_broken: a message body this reader cannot decode contributes nothing.
session("ses_broken", title="broken", born=BORN - 60000)
c.execute(
    "insert into session_message (id, session_id, type, seq, time_created,"
    " time_updated, data) values (?,?,?,?,?,?,?)",
    ("msg_broken", "ses_broken", "assistant", 999, BORN, BORN, "not json"),
)
c.execute(
    "insert into session_message (id, session_id, type, seq, time_created,"
    " time_updated, data) values (?,?,?,?,?,?,?)",
    ("msg_broken_user", "ses_broken", "user", 998, BORN, BORN, "not json"),
)

# ses_outoforder: rowid order differs from seq, so the prompt preview must
# follow seq and not the scan order.
session("ses_outoforder", title="order", born=BORN - 90000)
message("ses_outoforder", "user", {"text": "later prompt"}, at=200)
message("ses_outoforder", "user", {"text": "earlier prompt"}, at=100)

# ses_parts: parts this reader must fold, skip, or render as text.
session("ses_parts", title="parts", born=BORN - 90000)
message(
    "ses_parts",
    "assistant",
    {
        "content": [
            {"type": "text", "text": "kept"},
            {"type": "text", "text": "   "},
            {"type": "snapshot", "text": "ignored"},
            tool("running", "bash", "call_run", input={"command": "pwd"}),
            tool("completed", "grep", "", input={"pattern": "x"}, content=[{"type": "text", "text": "out"}]),
            tool("streaming", "read", "call_stream", input={}),
        ]
    },
)

c.commit()
c.close()

# drift.db: a rebuilt schema that reordered a column and dropped another. The
# reader must refuse the positions instead of misreading them.
p = fresh("drift.db")
c = sqlite3.connect(p)
c.executescript("""
create table `session_v2` (
  `id` text PRIMARY KEY,
  `parent_id` text,
  `directory` text,
  `title` text,
  `model` text
);
create table `session_message` (
  `id` text PRIMARY KEY,
  `session_id` text NOT NULL,
  `seq` integer NOT NULL,
  `type` text NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `data` text NOT NULL
);
""")
c.commit()
c.close()

# foreign.db: a valid SQLite file without the opencode tables.
p = fresh("foreign.db")
c = sqlite3.connect(p)
c.execute("create table t(k text)")
c.execute("insert into t values('x')")
c.commit()
c.close()

# opencode-wal.db: a session that exists only in the committed frames of a live
# write-ahead log. Copy the files while the source connection is open, because
# a close checkpoints the log into the main file and deletes the sidecar.
tmp = tempfile.mkdtemp()
src = os.path.join(tmp, "opencode-wal.db")
c = sqlite3.connect(src)
c.execute("pragma journal_mode=wal")
c.execute("pragma wal_autocheckpoint=0")
create_schema(c)
session("ses_checkpointed", title="checkpointed")
message("ses_checkpointed", "user", {"text": "in the main file"})
c.commit()
c.execute("pragma wal_checkpoint(truncate)")
session("ses_wal", title="wal")
message("ses_wal", "user", {"text": "from the log"})
c.commit()
p = fresh("opencode-wal.db")
for suffix in ("", "-wal"):
    shutil.copyfile(src + suffix, p + suffix)
c.close()
shutil.rmtree(tmp)

print("fixtures written")
