"""Generate the Antigravity fixture store.

Run from this directory:

    python3 gen.py

The fixtures are checked in. Regenerate only when the reader's contract
changes, and keep the tests in step with the data.

store/antigravity/conversations/main.db
    a user prompt, a model turn with one settled and one unsettled tool call,
    the settled result, an error step, a task-notification step and a final
    answer.
store/antigravity/conversations/{dup,nouser,fallback,emptyuser,empty}.db
    the identity, preview, fallback, whitespace and empty conversation shapes.
    nouser has no user message, so it stands for a child agent run that the
    summaries index does not know.
store/antigravity/conversations/{titled,nested,childprompt,stray}.db
    the summaries index shapes: a generated title, a child run, a child run
    that still carries a user step, and a conversation the index omits.
store/antigravity/conversations/orphan.db
    a child run the app recorded without a parent link, so only the nesting
    depth identifies it.
store/antigravity/conversations/{foreign,junk,drift,malformed}.db
    the refusals: no steps table, not SQLite, reordered columns and an
    unreadable step payload.
store/antigravity/conversation_summaries.db
    the current index: title, parent and nesting depth per conversation.
store/antigravity-ide/conversations/ide.db
    a third root, whose index has no conversation identity column.
store/antigravity-cli/conversations/{cli,dup}.db
    a second root, including an identity the first root also has.
store/antigravity-cli/conversation_summaries.db
    the index before the parent and nesting columns existed.

The table definitions copy `sqlite_master` from a real Antigravity database,
since the reader addresses steps by column position.
"""

import json
import os
import sqlite3

here = os.path.dirname(os.path.abspath(__file__))

SEC = 1792597430
NANOS = 600138127


def varint(value):
    out = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            out.append(byte | 0x80)
        else:
            out.append(byte)
            return bytes(out)


def field(num, wire, payload):
    key = varint((num << 3) | wire)
    if wire == 0:
        return key + varint(payload)
    if wire == 2:
        return key + varint(len(payload)) + payload
    raise ValueError(wire)


def vint(num, value):
    return field(num, 0, value)


def msg(num, payload):
    return field(num, 2, payload)


def timestamp(seconds=SEC, nanos=NANOS):
    return msg(1, vint(1, seconds) + vint(2, nanos))


def meta(seconds=SEC, nanos=NANOS, call=None):
    out = timestamp(seconds, nanos)
    if call is not None:
        out += msg(4, call)
    return msg(5, out)


def user_step(text, seconds=SEC):
    user = msg(2, text.encode()) + msg(3, text.encode())
    return vint(1, 14) + vint(4, 3) + meta(seconds) + msg(19, user)


def user_step_parts(parts, seconds=SEC):
    user = b"".join(msg(3, part.encode()) for part in parts)
    return vint(1, 14) + vint(4, 3) + meta(seconds) + msg(19, user)


def call(call_id, name, arguments):
    return msg(1, call_id.encode()) + msg(2, name.encode()) + msg(3, arguments.encode())


def assistant_step(thought, calls, text=None, seconds=SEC):
    body = msg(3, thought.encode())
    if text is not None:
        body += msg(1, text.encode())
    for c in calls:
        body += msg(7, c)
    return vint(1, 15) + vint(4, 3) + meta(seconds) + msg(20, body)


def assistant_text(text, thought="", seconds=SEC):
    body = msg(1, text.encode())
    if thought:
        body += msg(3, thought.encode())
    return vint(1, 15) + vint(4, 3) + meta(seconds) + msg(20, body)


def tool_result_step(call_msg, result, seconds=SEC):
    body = msg(2, msg(1, result.encode()))
    return vint(1, 132) + vint(4, 3) + meta(seconds, call=call_msg) + msg(140, body)


def error_step(seconds=SEC):
    details = msg(1, b"Agent execution terminated due to error.") + msg(2, b"NOT_FOUND")
    return vint(1, 17) + vint(4, 3) + meta(seconds) + msg(24, msg(3, details))


def task_notice_step(seconds=SEC):
    body = msg(1, b"[Message] sender=bot priority=MESSAGE_PRIORITY_HIGH")
    return vint(1, 101) + vint(4, 3) + meta(seconds) + msg(114, body)


def workspace_blob(uri):
    workspace = msg(1, uri.encode()) + msg(2, uri.encode())
    return msg(1, workspace)


SCHEMA = """
CREATE TABLE `trajectory_meta` (`trajectory_id` text, `cascade_id` text,
  `trajectory_type` integer, `source` integer, PRIMARY KEY (`trajectory_id`));
CREATE TABLE `steps` (`idx` integer, `step_type` integer NOT NULL DEFAULT 0,
  `status` integer NOT NULL DEFAULT 0, `has_subtrajectory` numeric NOT NULL DEFAULT false,
  `metadata` blob, `error_details` blob, `permissions` blob, `task_details` blob,
  `render_info` blob, `step_payload` blob, `step_format` integer NOT NULL DEFAULT 0,
  PRIMARY KEY (`idx`));
CREATE INDEX `idx_steps_status` ON `steps`(`status`);
CREATE INDEX `idx_steps_step_type` ON `steps`(`step_type`);
CREATE TABLE `gen_metadata` (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0,
  PRIMARY KEY (`idx`));
CREATE TABLE `executor_metadata` (`idx` integer, `data` blob, PRIMARY KEY (`idx`));
CREATE TABLE `parent_references` (`idx` integer, `data` blob, PRIMARY KEY (`idx`));
CREATE TABLE `trajectory_metadata_blob` (`id` text DEFAULT "main", `data` blob,
  PRIMARY KEY (`id`));
CREATE TABLE `battle_mode_infos` (`idx` integer, `data` blob, PRIMARY KEY (`idx`));
"""


def fresh(path):
    for suffix in ("", "-wal", "-shm"):
        if os.path.exists(path + suffix):
            os.remove(path + suffix)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    return path


def new_db(path, schema=SCHEMA):
    path = fresh(path)
    conn = sqlite3.connect(path)
    conn.executescript(schema)
    return conn


SUMMARY_SCHEMA = """
CREATE TABLE `conversation_summaries` (`conversation_id` text,
  `title` text NOT NULL DEFAULT "", `preview` text NOT NULL DEFAULT "",
  `step_count` integer NOT NULL DEFAULT 0, `last_modified_time` datetime NOT NULL,
  `workspace_uris` text NOT NULL, `status` text NOT NULL DEFAULT "",
  `source` text NOT NULL DEFAULT "", `project_id` text NOT NULL DEFAULT "",
  `agent_name` text NOT NULL DEFAULT "",
  `parent_conversation_id` text NOT NULL DEFAULT "",
  `nesting_depth` integer NOT NULL DEFAULT 0, `battle_id` text NOT NULL DEFAULT "",
  `winning_conversation_id` text NOT NULL DEFAULT "",
  `not_fully_idle` numeric NOT NULL DEFAULT false,
  `killed` numeric NOT NULL DEFAULT false, `last_user_input_time` datetime NOT NULL,
  `last_user_input_step_index` integer NOT NULL DEFAULT -1,
  `app_data_dir` text NOT NULL DEFAULT "", `raw_summary` blob,
  `group_id` text NOT NULL DEFAULT "", PRIMARY KEY (`conversation_id`));
"""

OLD_SUMMARY_SCHEMA = """
CREATE TABLE `conversation_summaries` (`conversation_id` text,
  `title` text NOT NULL DEFAULT "", `preview` text NOT NULL DEFAULT "",
  `step_count` integer NOT NULL DEFAULT 0, `last_modified_time` datetime NOT NULL,
  `workspace_uris` text NOT NULL, PRIMARY KEY (`conversation_id`));
"""


def add_summary(conn, conversation_id, title="", cwd="", parent="", depth=0, agent=""):
    conn.execute(
        "insert into conversation_summaries (conversation_id, title, workspace_uris,"
        " parent_conversation_id, nesting_depth, agent_name, last_modified_time,"
        " last_user_input_time) values (?,?,?,?,?,?,?,?)",
        (
            conversation_id,
            title,
            json.dumps([cwd]) if cwd else "[]",
            parent,
            depth,
            agent,
            "2026-10-21 17:54:46+00:00",
            "2026-10-21 17:54:46+00:00",
        ),
    )


def finish(conn, cwd=None):
    if cwd is not None:
        conn.execute(
            "insert into trajectory_metadata_blob (id, data) values ('main', ?)",
            (workspace_blob(cwd),),
        )
    conn.commit()
    conn.close()


def add_steps(conn, steps):
    for idx, (step_type, payload) in enumerate(steps):
        conn.execute(
            "insert into steps (idx, step_type, step_payload) values (?,?,?)",
            (idx, step_type, payload),
        )


store = os.path.join(here, "store")

# main: the stable conversation shape.
conn = new_db(os.path.join(store, "antigravity", "conversations", "main.db"))
c1 = call("call_1", "read_file", '{"Path":"/tmp/a"}')
c2 = call("call_2", "run_command", '{"CommandLine":"true"}')
conn.execute(
    "insert into trajectory_meta (trajectory_id, cascade_id, trajectory_type, source)"
    " values ('traj-main','main',4,1)"
)
add_steps(
    conn,
    [
        (14, user_step("first prompt for antigravity")),
        (15, assistant_step("think one", [c1, c2])),
        (132, tool_result_step(c1, "file body")),
        (17, error_step()),
        (101, task_notice_step()),
        (15, assistant_text("final answer", thought="think two")),
    ],
)
finish(conn, cwd="file:///work/dir")

# dup: the same identity in both roots. The first root must win.
conn = new_db(os.path.join(store, "antigravity", "conversations", "dup.db"))
add_steps(
    conn,
    [
        (14, user_step("duplicate identity")),
        (15, assistant_text("first root answer")),
    ],
)
finish(conn, cwd="file:///work/first")

conn = new_db(os.path.join(store, "antigravity-cli", "conversations", "dup.db"))
add_steps(
    conn,
    [
        (14, user_step("duplicate identity from cli")),
        (15, assistant_text("second root answer")),
    ],
)
finish(conn, cwd="file:///work/second")

# cli: the CLI root's own conversation.
conn = new_db(os.path.join(store, "antigravity-cli", "conversations", "cli.db"))
add_steps(
    conn,
    [
        (14, user_step("cli prompt")),
        (15, assistant_text("cli answer")),
    ],
)
finish(conn, cwd="file:///work/cli")

# nouser: a conversation without a user prompt.
conn = new_db(os.path.join(store, "antigravity", "conversations", "nouser.db"))
add_steps(conn, [(15, assistant_text("assistant only"))])
finish(conn, cwd="file:///work/dir")

# fallback: the display text field is absent, so the parts are joined.
conn = new_db(os.path.join(store, "antigravity", "conversations", "fallback.db"))
add_steps(
    conn,
    [
        (14, user_step_parts(["part one", " part two"])),
        (15, assistant_text("fallback answer")),
    ],
)
finish(conn, cwd="file:///work/dir")

# emptyuser: the only user step is whitespace, so it is not a message.
conn = new_db(os.path.join(store, "antigravity", "conversations", "emptyuser.db"))
add_steps(
    conn,
    [
        (14, user_step("   ")),
        (15, assistant_text("answer without a prompt")),
    ],
)
finish(conn, cwd="file:///work/dir")

# empty: a valid database whose conversation has no messages.
conn = new_db(os.path.join(store, "antigravity", "conversations", "empty.db"))
finish(conn)

# nometa: a conversation without the workspace metadata table.
nometa_schema = SCHEMA.replace(
    'CREATE TABLE `trajectory_metadata_blob` (`id` text DEFAULT "main", `data` blob,\n'
    "  PRIMARY KEY (`id`));\n",
    "",
)
conn = new_db(
    os.path.join(store, "antigravity", "conversations", "nometa.db"),
    schema=nometa_schema,
)
add_steps(
    conn,
    [
        (14, user_step("prompt without metadata")),
        (15, assistant_text("answer without metadata")),
    ],
)
finish(conn)

# foreign: a valid SQLite file without the Antigravity tables.
conn = new_db(
    os.path.join(store, "antigravity", "conversations", "foreign.db"),
    schema="CREATE TABLE unrelated (id integer primary key, value text);",
)
finish(conn)

# drift: the steps table rebuilt with step_payload in the wrong position.
drift_schema = SCHEMA.replace(
    "`step_payload` blob, `step_format` integer NOT NULL DEFAULT 0",
    "`step_format` integer NOT NULL DEFAULT 0, `step_payload` blob",
)
conn = new_db(
    os.path.join(store, "antigravity", "conversations", "drift.db"), schema=drift_schema
)
conn.execute(
    "insert into steps (idx, step_type, step_format, step_payload) values (0,14,0,?)",
    (user_step("drifted"),),
)
finish(conn)

# malformed: a known step type with an unreadable payload.
conn = new_db(os.path.join(store, "antigravity", "conversations", "malformed.db"))
conn.execute(
    "insert into steps (idx, step_type, step_payload) values (0,14,?)",
    (b"\xff\xff",),
)
finish(conn)

# junk: not a SQLite database at all.
path = fresh(os.path.join(store, "antigravity", "conversations", "junk.db"))
with open(path, "wb") as f:
    f.write(b"this is not a sqlite database\n")

# titled: the index carries the generated title and the workspace, since the
# database has no workspace metadata table of its own.
conn = new_db(
    os.path.join(store, "antigravity", "conversations", "titled.db"),
    schema=nometa_schema,
)
add_steps(
    conn,
    [
        (14, user_step("prompt behind the title")),
        (15, assistant_text("titled answer")),
    ],
)
finish(conn)

# nested: a child agent run, the shape the app records for a task it spawned.
conn = new_db(os.path.join(store, "antigravity", "conversations", "nested.db"))
add_steps(
    conn,
    [
        (101, task_notice_step()),
        (15, assistant_text("child run answer")),
    ],
)
finish(conn, cwd="file:///work/dir")

# childprompt: a child agent run that still carries a user step, so only the
# index can identify it.
conn = new_db(os.path.join(store, "antigravity", "conversations", "childprompt.db"))
add_steps(
    conn,
    [
        (14, user_step("prompt handed to the child")),
        (15, assistant_text("child answer")),
    ],
)
finish(conn, cwd="file:///work/dir")

# orphan: a child run without a parent link in the index.
conn = new_db(os.path.join(store, "antigravity", "conversations", "orphan.db"))
add_steps(
    conn,
    [
        (14, user_step("prompt handed to an orphan")),
        (15, assistant_text("orphan answer")),
    ],
)
finish(conn, cwd="file:///work/dir")

# stray: a conversation the index does not mention at all.
conn = new_db(os.path.join(store, "antigravity", "conversations", "stray.db"))
add_steps(
    conn,
    [
        (14, user_step("prompt outside the index")),
        (15, assistant_text("stray answer")),
    ],
)
finish(conn, cwd="file:///work/dir")

# ide: a third root. Its index has no conversation identity column, so
# discovery must fall back to the trajectory.
conn = new_db(os.path.join(store, "antigravity-ide", "conversations", "ide.db"))
add_steps(
    conn,
    [
        (14, user_step("prompt from the ide")),
        (15, assistant_text("ide answer")),
    ],
)
finish(conn, cwd="file:///work/ide")
index = new_db(
    os.path.join(store, "antigravity-ide", "conversation_summaries.db"),
    schema="CREATE TABLE `conversation_summaries` (`title` text, `workspace_uris` text);",
)
index.execute("insert into conversation_summaries values ('Unkeyed Title','[]')")
index.commit()
index.close()

# The desktop index: a generated title for main, the workspace for a
# conversation without metadata, and the parent links of two child runs.
summary_db = new_db(
    os.path.join(store, "antigravity", "conversation_summaries.db"),
    schema=SUMMARY_SCHEMA,
)
add_summary(summary_db, "main", title="Main Conversation Title")
add_summary(summary_db, "titled", title="Generated Title", cwd="file:///work/titled")
add_summary(summary_db, "nometa", cwd="file:///work/from-index")
add_summary(summary_db, "nested", parent="main", depth=1, agent="self")
add_summary(summary_db, "childprompt", parent="main", depth=1, agent="self")
add_summary(summary_db, "orphan", depth=1, agent="self")
summary_db.commit()
summary_db.close()

# The CLI index predates the parent and nesting columns.
old_db = new_db(
    os.path.join(store, "antigravity-cli", "conversation_summaries.db"),
    schema=OLD_SUMMARY_SCHEMA,
)
old_db.execute(
    "insert into conversation_summaries (conversation_id, title, workspace_uris,"
    " last_modified_time) values (?,?,?,?)",
    ("cli", "CLI Conversation Title", "[]", "2026-10-21 17:54:46+00:00"),
)
old_db.commit()
old_db.close()
