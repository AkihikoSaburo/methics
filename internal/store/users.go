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
	"go.mongodb.org/mongo-driver/mongo/options"
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

// MinPasswordLength adalah panjang minimal password akun staf. Default 12;
// cmd/server menurunkannya hanya bila APP_ENV bukan production.
var MinPasswordLength = 12

func (db *Database) CreateUser(ctx context.Context, username, password, displayName, role string, classIDs []string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < MinPasswordLength || strings.TrimSpace(displayName) == "" {
		return fmt.Errorf("username/display name required and password must be at least %d characters", MinPasswordLength)
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

// ResetUser menyetel ulang password, role, kelas, dan status aktif akun
// dengan username tersebut (tanpa membedakan huruf besar/kecil), atau
// membuatnya bila belum ada. Dipakai bootstrap mode pengembangan
// (BOOTSTRAP_RESET_PASSWORDS=true). created=true bila akun baru dibuat.
func (db *Database) ResetUser(ctx context.Context, username, password, role string, classIDs []string) (bool, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < MinPasswordLength {
		return false, fmt.Errorf("username required and password must be at least %d characters", MinPasswordLength)
	}
	if role != "administrator" && role != "dosen" {
		return false, errors.New("unsupported staff role")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("hash password: %w", err)
	}
	filter := bson.M{"username": bson.M{"$regex": "^" + regexp.QuoteMeta(username) + "$", "$options": "i"}}
	if classIDs == nil {
		classIDs = []string{}
	}
	result, err := db.Collection("users").UpdateOne(ctx, filter, bson.M{"$set": bson.M{
		"password_hash": hash,
		"role":          role,
		"active":        true,
		"class_ids":     classIDs,
	}})
	if err != nil {
		return false, err
	}
	if result.MatchedCount > 0 {
		return false, nil
	}
	return true, db.CreateUser(ctx, username, password, username, role, classIDs)
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

func (db *Database) CountUsersByRole(ctx context.Context, role string) (int, error) {
	count, err := db.Collection("users").CountDocuments(ctx, bson.M{
		"role":   role,
		"active": true,
	})
	return int(count), err
}

// StaffSummary adalah ringkasan akun staf untuk halaman pengelolaan akun
// (tanpa hash password).
type StaffSummary struct {
	ID          string
	Username    string
	DisplayName string
	Role        string
	Active      bool
	ClassIDs    []string
	CreatedAt   time.Time
}

// ListStaff mengembalikan semua akun staf, administrator lebih dulu.
func (db *Database) ListStaff(ctx context.Context) ([]StaffSummary, error) {
	cursor, err := db.Collection("users").Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "role", Value: 1}, {Key: "username", Value: 1}}).
			SetProjection(bson.M{"password_hash": 0}))
	if err != nil {
		return nil, err
	}
	var docs []userDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	result := make([]StaffSummary, 0, len(docs))
	for _, doc := range docs {
		result = append(result, StaffSummary{
			ID: doc.ID, Username: doc.Username, DisplayName: doc.DisplayName, Role: doc.Role,
			Active: doc.Active, ClassIDs: doc.ClassIDs, CreatedAt: doc.CreatedAt,
		})
	}
	return result, nil
}

// UsernameTaken memeriksa username tanpa membedakan huruf besar/kecil
// (login juga tidak membedakannya).
func (db *Database) UsernameTaken(ctx context.Context, username string) (bool, error) {
	filter := bson.M{"username": bson.M{"$regex": "^" + regexp.QuoteMeta(strings.TrimSpace(username)) + "$", "$options": "i"}}
	count, err := db.Collection("users").CountDocuments(ctx, filter)
	return count > 0, err
}

// UserRole mengembalikan role akun dengan ID tersebut.
func (db *Database) UserRole(ctx context.Context, id string) (string, error) {
	var doc struct {
		Role string `bson:"role"`
	}
	err := db.Collection("users").FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
	return doc.Role, err
}

// DeleteUser menghapus akun berdasarkan ID. Sesi akun itu otomatis tidak
// berlaku lagi karena RequireSession memuat ulang user dari database.
func (db *Database) DeleteUser(ctx context.Context, id string) (bool, error) {
	result, err := db.Collection("users").DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return false, err
	}
	return result.DeletedCount > 0, nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
