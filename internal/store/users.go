package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID          string
	Username    string
	DisplayName string
	Role        string
	ClassIDs    []string
}

var ErrInvalidCredentials = errors.New("invalid username or password")
var ErrUserExists = errors.New("username already exists")
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalization-password"), bcrypt.DefaultCost)

func (db *Database) CreateUser(ctx context.Context, username, password, displayName, role string, classIDs []string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 12 || strings.TrimSpace(displayName) == "" {
		return errors.New("username/display name required and password must be at least 12 characters")
	}
	if role != "administrator" && role != "dosen" {
		return errors.New("unsupported staff role")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	id, err := newID()
	if err != nil {
		return fmt.Errorf("generate user id: %w", err)
	}
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id, username, display_name, role, password_hash) VALUES(?, ?, ?, ?, ?)`, id, username, displayName, role, hash); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrUserExists
		}
		return err
	}
	for _, classID := range classIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_classes(user_id, class_id) VALUES(?, ?)`, id, classID); err != nil {
			return fmt.Errorf("assign class %s: %w", classID, err)
		}
	}
	return tx.Commit()
}

func (db *Database) EnsureUser(ctx context.Context, username, password, displayName, role string, classIDs []string) error {
	var exists int
	err := db.SQL.QueryRowContext(ctx, `SELECT 1 FROM users WHERE username = ?`, strings.TrimSpace(username)).Scan(&exists)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return db.CreateUser(ctx, username, password, displayName, role, classIDs)
}

func (db *Database) AuthenticateUser(ctx context.Context, username, password string) (User, error) {
	var user User
	var hash []byte
	var active bool
	err := db.SQL.QueryRowContext(ctx, `SELECT id, username, display_name, role, password_hash, active FROM users WHERE username = ?`, strings.TrimSpace(username)).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Role, &hash, &active,
	)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if !active || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	user.ClassIDs, err = db.UserClassIDs(ctx, user.ID)
	return user, err
}

func (db *Database) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	var active bool
	err := db.SQL.QueryRowContext(ctx, `SELECT id, username, display_name, role, active FROM users WHERE id = ?`, id).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Role, &active,
	)
	if err != nil {
		return User{}, err
	}
	if !active {
		return User{}, sql.ErrNoRows
	}
	user.ClassIDs, err = db.UserClassIDs(ctx, user.ID)
	return user, err
}

func (db *Database) UserClassIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT class_id FROM user_classes WHERE user_id = ? ORDER BY class_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	classes := make([]string, 0)
	for rows.Next() {
		var classID string
		if err := rows.Scan(&classID); err != nil {
			return nil, err
		}
		classes = append(classes, classID)
	}
	return classes, rows.Err()
}

func (db *Database) CountUsersByRole(ctx context.Context, role string) (int, error) {
	var count int
	err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ? AND active = 1`, role).Scan(&count)
	return count, err
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
