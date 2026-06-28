CREATE TABLE charge_sessions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at        TIMESTAMP NOT NULL,
    ended_at          TIMESTAMP,
    start_reason      TEXT NOT NULL DEFAULT '',
    stop_reason       TEXT,
    start_vehicle_soc INTEGER,
    end_vehicle_soc   INTEGER,
    energy_wh         REAL NOT NULL DEFAULT 0,
    avg_charge_w      REAL NOT NULL DEFAULT 0,
    peak_charge_w     REAL NOT NULL DEFAULT 0
);

CREATE INDEX idx_charge_sessions_started_at ON charge_sessions (started_at);

CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TIMESTAMP NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    from_state  TEXT NOT NULL DEFAULT '',
    to_state    TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL DEFAULT '',
    surplus_w   REAL NOT NULL DEFAULT 0,
    grid_w      REAL NOT NULL DEFAULT 0,
    pv_w        REAL NOT NULL DEFAULT 0,
    battery_soc INTEGER NOT NULL DEFAULT 0,
    battery_w   REAL NOT NULL DEFAULT 0,
    vehicle_soc INTEGER NOT NULL DEFAULT 0,
    charge_w    REAL NOT NULL DEFAULT 0,
    detail      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_events_ts ON events (ts);

CREATE TABLE samples (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TIMESTAMP NOT NULL,
    grid_w      REAL NOT NULL DEFAULT 0,
    pv_w        REAL NOT NULL DEFAULT 0,
    home_w      REAL NOT NULL DEFAULT 0,
    battery_soc INTEGER NOT NULL DEFAULT 0,
    battery_w   REAL NOT NULL DEFAULT 0,
    charge_w    REAL NOT NULL DEFAULT 0,
    vehicle_soc INTEGER NOT NULL DEFAULT 0,
    charging    INTEGER NOT NULL DEFAULT 0,
    mode        TEXT NOT NULL DEFAULT '',
    surplus_w   REAL NOT NULL DEFAULT 0,
    state       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_samples_ts ON samples (ts);
