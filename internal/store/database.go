package store

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Database membungkus koneksi MongoDB dan database aplikasi (MONGODB_DATABASE).
type Database struct {
	Client *mongo.Client
	DB     *mongo.Database
}

// Open membuka koneksi ke MongoDB (default mongodb://10.100.1.11:27017, database
// "attendance"), memastikan koneksi hidup dalam 8 detik, lalu menjalankan
// migrate (index) dan seed. Keduanya idempoten dan berjalan di SETIAP start.
func Open(uri, dbName string) (*Database, error) {
	if uri == "" {
		uri = "mongodb://10.100.1.11:27017"
	}
	if dbName == "" {
		dbName = "attendance"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	clientOptions := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("connect to mongodb (%s): %w", uri, err)
	}

	if err = client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping mongodb (%s): %w", uri, err)
	}

	db := client.Database(dbName)
	store := &Database{
		Client: client,
		DB:     db,
	}

	if err = store.migrate(context.Background()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("migrate mongodb indexes: %w", err)
	}

	if err = store.seed(context.Background()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("seed mongodb: %w", err)
	}

	return store, nil
}

func (db *Database) Collection(name string) *mongo.Collection {
	return db.DB.Collection(name)
}

func (db *Database) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.Client.Disconnect(ctx)
}

func (db *Database) migrate(ctx context.Context) error {
	// Index untuk users: username unique
	_, err := db.Collection("users").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("index users.username: %w", err)
	}

	// Index untuk students: nim unique
	_, err = db.Collection("students").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "nim", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("index students.nim: %w", err)
	}

	// Index untuk students: class_id
	_, err = db.Collection("students").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "class_id", Value: 1}},
	})
	if err != nil {
		return fmt.Errorf("index students.class_id: %w", err)
	}

	// Index untuk class_students: compound unique
	_, err = db.Collection("class_students").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "class_id", Value: 1}, {Key: "student_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("index class_students: %w", err)
	}

	// Index untuk face_templates: student_id
	_, err = db.Collection("face_templates").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "student_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("index face_templates: %w", err)
	}

	// Index untuk scan_events
	_, err = db.Collection("scan_events").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "scanned_at", Value: -1}}},
		{Keys: bson.D{{Key: "class_id", Value: 1}, {Key: "scanned_at", Value: -1}}},
		{Keys: bson.D{{Key: "student_id", Value: 1}, {Key: "scanned_at", Value: -1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		// image_key: pengecekan hak akses foto (GET /api/v1/images/...).
		{Keys: bson.D{{Key: "image_key", Value: 1}}, Options: options.Index().SetSparse(true)},
	})
	if err != nil {
		return fmt.Errorf("index scan_events: %w", err)
	}

	_, err = db.Collection("face_templates").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "image_key", Value: 1}},
		Options: options.Index().SetSparse(true),
	})
	if err != nil {
		return fmt.Errorf("index face_templates image_key: %w", err)
	}

	return nil
}

// seed meng-upsert data dasar (3 kelas dan 2 perangkat) dengan $setOnInsert:
// dokumen yang sudah ada tidak ditimpa, dokumen yang hilang dibuat lagi.
// Mahasiswa TIDAK di-seed; semuanya didaftarkan manual lewat dashboard atau
// POST /api/v1/students, jadi mahasiswa yang dihapus tidak muncul kembali.
func (db *Database) seed(ctx context.Context) error {
	classes := []struct{ ID, Name string }{
		{"IK24-A", "Ilmu Komputer A"},
		{"IK24-B", "Ilmu Komputer B"},
		{"IK24-C", "Ilmu Komputer C"},
	}
	for _, class := range classes {
		_, err := db.Collection("classes").UpdateOne(ctx,
			bson.M{"_id": class.ID},
			bson.M{"$setOnInsert": bson.M{"_id": class.ID, "name": class.Name}},
			options.Update().SetUpsert(true),
		)
		if err != nil {
			return fmt.Errorf("seed class %s: %w", class.ID, err)
		}
	}

	devices := []struct{ ID, Name string }{
		{"ESP32-GED-A1", "ESP32 Gedung A"},
		{"ESP32-GED-B1", "ESP32 Gedung B"},
	}
	for _, device := range devices {
		_, err := db.Collection("devices").UpdateOne(ctx,
			bson.M{"_id": device.ID},
			bson.M{"$setOnInsert": bson.M{"_id": device.ID, "name": device.Name}},
			options.Update().SetUpsert(true),
		)
		if err != nil {
			return fmt.Errorf("seed device %s: %w", device.ID, err)
		}
	}

	return nil
}
