-- T01: core registry tables.
--
-- SQL migrations run verbatim against the configured DSN, so this DDL is
-- SQLite-flavored (the local default, see .env.example). Timestamps are
-- always set by Go code, not DB defaults.

CREATE TABLE repositories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL UNIQUE,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE blobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    digest VARCHAR(255) NOT NULL UNIQUE,
    size BIGINT NOT NULL,
    path VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE repo_blobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES repositories(id),
    blob_id INTEGER NOT NULL REFERENCES blobs(id),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (repo_id, blob_id)
);

CREATE TABLE manifests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES repositories(id),
    digest VARCHAR(255) NOT NULL,
    media_type VARCHAR(255) NOT NULL,
    artifact_type VARCHAR(255),
    subject_digest VARCHAR(255),
    size BIGINT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (repo_id, digest)
);

CREATE TABLE tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES repositories(id),
    name VARCHAR(255) NOT NULL,
    manifest_digest VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (repo_id, name)
);
