# ConnectMac Web Member Management Plan

## Goal

Add a lightweight local data layer for the `cm web` manager so teams can maintain members, ownership, and operator metadata without making the YAML profile files carry every operational detail.

## Recommended First Version

Use a local SQLite database under:

```text
~/.connectmac/connectmac.db
```

SQLite is the best first choice because it is small, local, transactional, easy to back up, and does not require a background service. It also fits Homebrew installs well: `cm` owns the schema and user data stays under `~/.connectmac`, so uninstalling Homebrew removes the app files but keeps user data.

## Data Scope

Keep AWS infrastructure source-of-truth in profile YAML and AWS tags. Use the database for app-side management metadata:

- Members: display name, email, role, enabled status.
- Apple account ownership: Apple email mapped to member IDs.
- Operation audit log: who previewed/opened/released, profile, Apple email, timestamp, result.
- Web preferences: table filters, pinned profiles, default view.
- Optional notes: per Apple account operational notes.

## Proposed Tables

```sql
members (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL UNIQUE,
  role TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

apple_account_members (
  apple_email TEXT NOT NULL,
  member_id TEXT NOT NULL,
  relation TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (apple_email, member_id)
);

operation_events (
  id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  profile TEXT NOT NULL,
  apple_email TEXT,
  member_id TEXT,
  confirmed INTEGER NOT NULL,
  status TEXT NOT NULL,
  message TEXT,
  created_at TEXT NOT NULL
);
```

## CLI Commands

```sh
cm member list
cm member add --name <name> --email <email> --role <admin|operator|viewer>
cm member disable <email>
cm member enable <email>
cm member assign <apple-email> --member <member-email> --relation owner
cm member unassign <apple-email> --member <member-email>
```

## Web UI Additions

- Add a Members tab.
- Add member name next to each Apple account when assigned.
- Add owner filter in the profile table.
- Show recent operation events per Apple account.
- Confirm dialogs should display the selected Apple account and assigned owner.

## Safety Rules

- Do not store PEM contents, AWS secret keys, or VNC passwords.
- Do not use the database as the AWS resource source-of-truth.
- Keep profile YAML valid and usable without the database.
- AWS mutations must still preview before confirm.
- Destroy workflows still never release Elastic IP allocations.

## Alternative If SQLite Is Too Much

Use YAML files under:

```text
~/.connectmac/members/
```

This is simpler and easier to edit manually, but harder to query, audit, and evolve. Use it only if avoiding SQLite is more important than audit/history features.

## Implementation Order

1. Add a storage abstraction and SQLite-backed implementation.
2. Add schema migration on first use.
3. Add `cm member ...` commands.
4. Add Web Members tab.
5. Add operation event logging for Web open/destroy actions.
6. Add tests for migration, CRUD, assignment, and audit records.
