package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Database struct {
	SQL *sql.DB
}

func Open(path string) (*Database, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	store := &Database{SQL: db}
	if err = store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = store.seed(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (db *Database) Close() error {
	return db.SQL.Close()
}

func (db *Database) migrate(ctx context.Context) error {
	_, err := db.SQL.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS classes (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS students (
	id TEXT PRIMARY KEY,
	nim TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	email TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	display_name TEXT NOT NULL,
	role TEXT NOT NULL CHECK(role IN ('administrator', 'dosen')),
	password_hash BLOB NOT NULL,
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS user_classes (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	class_id TEXT NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
	PRIMARY KEY (user_id, class_id)
);
CREATE TABLE IF NOT EXISTS class_students (
	class_id TEXT NOT NULL REFERENCES classes(id),
	student_id TEXT NOT NULL REFERENCES students(id),
	PRIMARY KEY (class_id, student_id)
);
CREATE TABLE IF NOT EXISTS devices (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	last_seen TEXT
);
CREATE TABLE IF NOT EXISTS face_templates (
	student_id TEXT PRIMARY KEY REFERENCES students(id) ON DELETE CASCADE,
	encoding TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS scan_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	student_id TEXT REFERENCES students(id),
	class_id TEXT NOT NULL REFERENCES classes(id),
	device_id TEXT NOT NULL REFERENCES devices(id),
	scanned_at TEXT NOT NULL,
	face_verified INTEGER NOT NULL DEFAULT 0,
	liveness_passed INTEGER NOT NULL DEFAULT 0,
	confidence REAL NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	review_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS scan_events_by_date ON scan_events(scanned_at);
CREATE INDEX IF NOT EXISTS scan_events_by_student_date ON scan_events(student_id, scanned_at);
`)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

func (db *Database) seed(ctx context.Context) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	classes := []struct{ id, name string }{
		{"IK24-A", "Ilmu Komputer A"},
		{"IK24-B", "Ilmu Komputer B"},
		{"IK24-C", "Ilmu Komputer C"},
	}
	for _, class := range classes {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO classes(id, name) VALUES(?, ?)`, class.id, class.name); err != nil {
			return fmt.Errorf("seed class %s: %w", class.id, err)
		}
	}

	students := []struct{ id, nim, name, class string }{
		{"student-ik24001", "IK24001", "Alya Putri", "IK24-A"},
		{"student-ik24002", "IK24002", "Bima Saputra", "IK24-A"},
		{"student-ik24003", "IK24003", "Citra Nabila", "IK24-B"},
		{"student-ik24004", "IK24004", "Dafa Ramadhan", "IK24-B"},
		{"student-ik24005", "IK24005", "Eka Anindya", "IK24-C"},
	}
	for _, student := range students {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO students(id, nim, name) VALUES(?, ?, ?)`, student.id, student.nim, student.name); err != nil {
			return fmt.Errorf("seed student %s: %w", student.nim, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO class_students(class_id, student_id) VALUES(?, ?)`, student.class, student.id); err != nil {
			return fmt.Errorf("enroll student %s: %w", student.nim, err)
		}
	}

	devices := []struct{ id, name string }{
		{"ESP32-GED-A1", "ESP32 Gedung A"},
		{"ESP32-GED-B1", "ESP32 Gedung B"},
	}
	for _, device := range devices {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO devices(id, name) VALUES(?, ?)`, device.id, device.name); err != nil {
			return fmt.Errorf("seed device %s: %w", device.id, err)
		}
	}
	return tx.Commit()
}
