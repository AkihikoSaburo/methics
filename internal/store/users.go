package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID          string
	Username    string
	DisplayName string
	Role        string
	ClassIDs    []string
}

type userDoc struct {
	ID           string    `bson:"_id"`
	Username     string    `bson:"username"`
	DisplayName  string    `bson:"display_name"`
	Role         string    `bson:"role"`
	PasswordHash []byte    `bson:"password_hash"`
	Active       bool      `bson:"active"`
	ClassIDs     []string  `bson:"class_ids"`
	CreatedAt    time.Time `bson:"created_at"`
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

	doc := userDoc{
		ID:           id,
		Username:     username,
		DisplayName:  displayName,
		Role:         role,
		PasswordHash: hash,
		Active:       true,
		ClassIDs:     classIDs,
		CreatedAt:    time.Now().UTC(),
	}

	_, err = db.Collection("users").InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrUserExists
		}
		return err
	}
	return nil
}

func (db *Database) EnsureUser(ctx context.Context, username, password, displayName, role string, classIDs []string) error {
	username = strings.TrimSpace(username)
	filter := bson.M{"username": bson.M{"$regex": "^" + regexp.QuoteMeta(username) + "$", "$options": "i"}}
	count, err := db.Collection("users").CountDocuments(ctx, filter)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.CreateUser(ctx, username, password, displayName, role, classIDs)
}

func (db *Database) AuthenticateUser(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	filter := bson.M{"username": bson.M{"$regex": "^" + regexp.QuoteMeta(username) + "$", "$options": "i"}}
	var doc userDoc
	err := db.Collection("users").FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if !doc.Active || bcrypt.CompareHashAndPassword(doc.PasswordHash, []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return User{
		ID:          doc.ID,
		Username:    doc.Username,
		DisplayName: doc.DisplayName,
		Role:        doc.Role,
		ClassIDs:    doc.ClassIDs,
	}, nil
}

func (db *Database) UserByID(ctx context.Context, id string) (User, error) {
	var doc userDoc
	err := db.Collection("users").FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
	if err != nil {
		return User{}, err
	}
	if !doc.Active {
		return User{}, mongo.ErrNoDocuments
	}
	return User{
		ID:          doc.ID,
		Username:    doc.Username,
		DisplayName: doc.DisplayName,
		Role:        doc.Role,
		ClassIDs:    doc.ClassIDs,
	}, nil
}

func (db *Database) UserClassIDs(ctx context.Context, userID string) ([]string, error) {
	var doc userDoc
	err := db.Collection("users").FindOne(ctx, bson.M{"_id": userID}).Decode(&doc)
	if err != nil {
		return nil, err
	}
	return doc.ClassIDs, nil
}

func (db *Database) CountUsersByRole(ctx context.Context, role string) (int, error) {
	count, err := db.Collection("users").CountDocuments(ctx, bson.M{
		"role":   role,
		"active": true,
	})
	return int(count), err
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
