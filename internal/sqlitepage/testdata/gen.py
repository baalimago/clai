"""Generate the SQLite fixtures for the sqlitepage tests.

Run from this directory:

    python3 gen.py

The fixtures are checked in. Regenerate only when the reader's contract
changes, and keep the tests in step with the data.
"""

import os
import sqlite3

here = os.path.dirname(os.path.abspath(__file__))


def fresh(name):
    p = os.path.join(here, name)
    for suffix in ("", "-wal", "-shm"):
        if os.path.exists(p + suffix):
            os.remove(p + suffix)
    return p


# basic.db: mixed types, unicode, overflow payloads, interior pages.
p = fresh("basic.db")
c = sqlite3.connect(p)
c.execute("create table kv(key text unique, value blob)")
c.execute("create table other(a integer primary key, b text)")
c.execute("create table types(v)")
rows = {
    "small": b"hello",
    "unicode": "näppäimistö 日本語".encode(),
    "null": None,
    "int": -1234567890123,
    "zero": 0,
    "one": 1,
    "real": 3.5,
    "bigblob": bytes(range(256)) * 1200,  # 307200 bytes, forces overflow
    "bigtext": ("x" * 100000).encode(),
}
for k, v in rows.items():
    c.execute("insert into kv(key,value) values(?,?)", (k, v))
for v in (None, 0, 1, -1, 2**40, 3.25, b"bytes", "text"):
    c.execute("insert into types(v) values(?)", (v,))
for i in range(2000):
    c.execute("insert into other(a,b) values(?,?)", (i, "row-%d" % i))
c.commit()
c.close()

# utf16.db: the database stores text as UTF-16LE.
p = fresh("utf16.db")
c = sqlite3.connect(p)
c.execute("pragma encoding='UTF-16le'")
c.execute("create table t(k text)")
c.execute("insert into t values('日本語 text')")
c.commit()
c.close()

# wal.db: a non-empty -wal sidecar must make Open refuse the stale file.
p = fresh("wal.db")
c = sqlite3.connect(p)
c.execute("create table t(k text)")
c.execute("insert into t values('x')")
c.commit()
c.close()
with open(p + "-wal", "wb") as f:
    f.write(b"ANONWAL")

print("fixtures written")
