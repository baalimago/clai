"""Generate the Cursor-shaped SQLite fixture for the cursor source tests.

Run from this directory:

    python3 gen.py

The values are the shapes Cursor writes to cursorDiskKV: one composerData row
per chat, one bubbleId row per message, and unrelated agentKv rows in between.
The golden file is computed with Python's sqlite3 engine so the hand-written
page reader is checked against the real engine, not only against itself.
"""

import hashlib
import json
import os
import sqlite3

here = os.path.dirname(os.path.abspath(__file__))
db_path = os.path.join(here, "state.vscdb")
for suffix in ("", "-wal", "-shm"):
    if os.path.exists(db_path + suffix):
        os.remove(db_path + suffix)

con = sqlite3.connect(db_path)
con.execute("create table ItemTable(key TEXT UNIQUE, value BLOB)")
con.execute("create table cursorDiskKV(key TEXT UNIQUE, value BLOB)")


def put(key, obj):
    value = json.dumps(obj, ensure_ascii=False, separators=(",", ":")).encode()
    con.execute("insert into cursorDiskKV(key, value) values (?,?)", (key, value))


device = "device-0001"

put(
    "composerData:composer-1",
    {
        "composerId": "composer-1",
        "name": "Fix the parser",
        "text": "Fix the parser please",
        "createdAt": 1750000000000,
        "modelConfig": {"modelName": "claude-4-sonnet"},
        "gitWorktree": {
            "worktreePath": "/home/dev/project",
            "branchName": "main",
        },
        "fullConversationHeadersOnly": [
            {"bubbleId": "b1"},
            {"bubbleId": "b2"},
            {"bubbleId": "b3"},
            {"bubbleId": "b4"},
        ],
    },
)
put(
    "bubbleId:%s:b1" % device,
    {
        "type": 1,
        "bubbleId": "b1",
        "text": "Fix the parser please",
        "createdAt": "2025-06-15T14:26:40.000Z",
        "workspaceProjectDir": "/home/dev/project",
    },
)
put(
    "bubbleId:%s:b2" % device,
    {
        "type": 2,
        "bubbleId": "b2",
        "text": "",
        "createdAt": "2025-06-15T14:26:41.000Z",
        "workspaceProjectDir": "/home/dev/project",
        "richText": {
            "root": {
                "type": "root",
                "children": [
                    {
                        "type": "paragraph",
                        "children": [
                            {"type": "text", "text": "I will "},
                            {"type": "text", "text": "fix it."},
                        ],
                    }
                ],
            }
        },
        "thinking": {"text": "Need to inspect the parser."},
        "toolFormerData": {
            "name": "read_file",
            "rawArgs": '{"path":"parser.go"}',
        },
    },
)
put(
    "bubbleId:%s:b3" % device,
    {
        "type": 1,
        "bubbleId": "b3",
        "text": "Thanks",
        "createdAt": "2025-06-15T14:26:42.000Z",
    },
)
put(
    "bubbleId:%s:b4" % device,
    {
        "type": 2,
        "bubbleId": "b4",
        "text": "Done",
        "createdAt": "2025-06-15T14:26:43.000Z",
    },
)

put(
    "composerData:composer-2",
    {
        "composerId": "composer-2",
        "name": "Flaky test",
        "text": "Investigate the flaky test",
        "createdAt": 1750100000000,
        "fullConversationHeadersOnly": [
            {"bubbleId": "b5"},
            {"bubbleId": "b6"},
        ],
    },
)
put(
    "bubbleId:%s:b5" % device,
    {
        "type": 1,
        "bubbleId": "b5",
        "text": "",
        "richText": {
            "root": {
                "type": "root",
                "children": [
                    {
                        "type": "paragraph",
                        "children": [
                            {"type": "text", "text": "Investigate the flaky test"}
                        ],
                    }
                ],
            }
        },
        "createdAt": "2025-06-16T18:13:20.000Z",
        "workspaceProjectDir": "/home/dev/flaky",
    },
)
put(
    "bubbleId:%s:b6" % device,
    {
        "type": 2,
        "bubbleId": "b6",
        "text": "",
        "allThinkingBlocks": [{"text": "Check the logs."}],
        "createdAt": "2025-06-16T18:13:21.000Z",
    },
)
put(
    "composerData:composer-empty-headers",
    {
        "composerId": "composer-empty-headers",
        "name": "Nothing to read",
        "createdAt": 1750200000000,
        "fullConversationHeadersOnly": [],
    },
)
put(
    "composerData:composer-missing-bubble",
    {
        "composerId": "composer-missing-bubble",
        "name": "Missing message",
        "createdAt": 1750300000000,
        "fullConversationHeadersOnly": [{"bubbleId": "gone"}],
    },
)
put(
    "composerData:composer-no-preview",
    {
        "composerId": "composer-no-preview",
        "createdAt": 1750400000000,
        "fullConversationHeadersOnly": [{"bubbleId": "b7"}],
    },
)
put(
    "bubbleId:%s:b7" % device,
    {
        "type": 1,
        "bubbleId": "b7",
        "text": "Fallback preview",
        "createdAt": "2025-06-20T09:00:00.000Z",
    },
)

# Filler rows make the table span interior pages and force one overflow chain.
for i in range(240):
    put("agentKv:blob:%04d" % i, {"index": i, "payload": "x" * 64})
put("agentKv:blob:large", {"payload": "y" * 80000})

con.execute(
    "insert into ItemTable(key, value) values (?,?)",
    ("workbench.colorTheme", b'"Default Dark+"'),
)
con.execute(
    "insert into ItemTable(key, value) values (?,?)",
    ("cursor.auth", json.dumps({"signedIn": True}).encode()),
)
con.commit()


def table_hash(connection, table):
    digest = hashlib.sha256()
    rows = 0
    for key, value in connection.execute(
        "select * from %s order by rowid" % table
    ):
        digest.update(key.encode("utf-8"))
        digest.update(b"\x00")
        if value is None:
            digest.update(b"\xff")
        elif isinstance(value, str):
            digest.update(value.encode("utf-8"))
        else:
            digest.update(bytes(value))
        digest.update(b"\x00")
        rows += 1
    return {"rows": rows, "sha256": digest.hexdigest()}


golden = {
    "ItemTable": table_hash(con, "ItemTable"),
    "cursorDiskKV": table_hash(con, "cursorDiskKV"),
}
con.close()

with open(os.path.join(here, "golden.json"), "w", encoding="utf-8") as f:
    json.dump(golden, f, indent=1, sort_keys=True)
    f.write("\n")


def pb_blob_id(blob_id):
    raw = bytes.fromhex(blob_id)
    assert len(raw) == 32
    return b"\x0a" + bytes([len(raw)]) + raw


def write_cli_chat(base, workspace, chat, meta):
    chat_dir = os.path.join(here, base, workspace, chat)
    os.makedirs(chat_dir, exist_ok=True)
    for suffix in ("", "-wal", "-shm"):
        p = os.path.join(chat_dir, "store.db" + suffix)
        if os.path.exists(p):
            os.remove(p)
    if meta is not None:
        with open(os.path.join(chat_dir, "meta.json"), "w", encoding="utf-8") as f:
            json.dump(meta, f, separators=(",", ":"))
    con = sqlite3.connect(os.path.join(chat_dir, "store.db"))
    con.execute("create table blobs (id TEXT PRIMARY KEY, data BLOB)")
    con.execute("create table meta (key TEXT PRIMARY KEY, value TEXT)")
    return con


def insert_blob(con, obj):
    raw = json.dumps(obj, ensure_ascii=False, separators=(",", ":")).encode()
    blob_id = hashlib.sha256(raw).hexdigest()
    con.execute("insert into blobs (id, data) values (?,?)", (blob_id, raw))
    return blob_id


def insert_root(con, blob_ids, store_meta):
    raw = b"".join(pb_blob_id(b) for b in blob_ids)
    root_id = hashlib.sha256(raw).hexdigest()
    con.execute("insert into blobs (id, data) values (?,?)", (root_id, raw))
    store_meta = dict(store_meta)
    store_meta["latestRootBlobId"] = root_id
    encoded = json.dumps(store_meta, separators=(",", ":")).encode().hex()
    con.execute("insert into meta (key, value) values ('0', ?)", (encoded,))
    return root_id


cli_dir = os.path.join(here, "cli")
if os.path.isdir(cli_dir):
    import shutil

    shutil.rmtree(cli_dir)

con = write_cli_chat(
    "cli",
    "workspace-a",
    "chat-cli-1",
    {
        "schemaVersion": 1,
        "createdAtMs": 1755000000000,
        "hasConversation": True,
        "title": "CLI review",
        "updatedAtMs": 1755000100000,
        "cwd": "/home/dev/cli",
    },
)
ids = [
    insert_blob(con, {"role": "system", "content": "You are an agent."}),
    insert_blob(
        con,
        {
            "role": "user",
            "content": "<user_info>\nWorkspace Path: /home/dev/cli\n</user_info>",
        },
    ),
    insert_blob(
        con,
        {
            "role": "user",
            "content": "<timestamp>now</timestamp>\n<user_query>\nReview the CLI reader\n</user_query>",
        },
    ),
    insert_blob(
        con,
        {
            "role": "assistant",
            "content": [
                {"type": "reasoning", "text": "Need to read."},
                {"type": "text", "text": "Reading files."},
                {"type": "tool-call", "toolName": "Read", "args": {"path": "cli.go"}},
            ],
        },
    ),
    insert_blob(
        con,
        {
            "role": "tool",
            "content": [{"type": "tool-result", "toolName": "Read", "result": "..."}],
        },
    ),
    insert_blob(
        con,
        {"role": "assistant", "content": [{"type": "text", "text": "Done."}]},
    ),
]
insert_root(
    con,
    ids,
    {"agentId": "chat-cli-1", "name": "CLI review", "createdAt": 1755000000000},
)
con.commit()
cli_golden = {
    "blobs": table_hash(con, "blobs"),
    "meta": table_hash(con, "meta"),
}
con.close()

con = write_cli_chat(
    "cli",
    "workspace-b",
    "chat-empty",
    {"schemaVersion": 1, "createdAtMs": 1755100000000, "hasConversation": False, "title": "Empty"},
)
con.commit()
con.close()

con = write_cli_chat("cli", "workspace-a", "chat-subagent", None)
ids = [
    insert_blob(con, {"role": "user", "content": "<user_query>explore</user_query>"}),
    insert_blob(con, {"role": "assistant", "content": [{"type": "text", "text": "done"}]}),
]
insert_root(
    con,
    ids,
    {
        "agentId": "chat-subagent",
        "name": "New Agent",
        "createdAt": 1755200000000,
        "subagentInfo": {"parentAgentId": "chat-cli-1"},
    },
)
con.commit()
con.close()

broken_dir = os.path.join(here, "cli-broken")
if os.path.isdir(broken_dir):
    import shutil

    shutil.rmtree(broken_dir)
con = write_cli_chat("cli-broken", "workspace-c", "chat-broken-root", {"title": "Broken"})
encoded = json.dumps(
    {"agentId": "chat-broken-root", "latestRootBlobId": "00" * 32, "createdAt": 1755300000000},
    separators=(",", ":"),
).encode().hex()
con.execute("insert into meta (key, value) values ('0', ?)", (encoded,))
con.commit()
con.close()

with open(os.path.join(here, "cli-golden.json"), "w", encoding="utf-8") as f:
    json.dump(cli_golden, f, indent=1, sort_keys=True)
    f.write("\n")

print("fixture written: %s" % db_path)
