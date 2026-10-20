# Basis Data (MongoDB)

> Status: **berlaku** — sesuai `internal/store/database.go`, `internal/store/users.go`,
> dan query di `internal/api/server.go`.
> Versi lama proyek memakai SQLite; semua rujukan ke `DATABASE_PATH`,
> `data/attendance.db`, tabel `user_classes`, atau `date(…, 'localtime')`
> sudah **tidak berlaku**.

## 1. Koneksi

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `MONGODB_URI` | `mongodb://10.100.1.11:27017` | URI driver resmi `go.mongodb.org/mongo-driver` v1.17 |
| `MONGODB_DATABASE` | `attendance` | nama database |

`store.Open` melakukan, berurutan:

1. `mongo.Connect` lalu `Ping` dengan batas waktu **8 detik**. Gagal → server
   berhenti dengan `initialize database (<uri>): …`.
2. `migrate` — membuat index (idempoten; aman dijalankan setiap startup).
3. `seed` — upsert kelas, mahasiswa, relasi kelas, dan perangkat contoh
   memakai `$setOnInsert`, sehingga **tidak menimpa** data yang sudah diubah.

Koneksi ditutup saat shutdown (`Database.Close`, batas 5 detik).

> Catatan: URI dicetak ke log saat startup (`connected to MongoDB at …`).
> Bila URI memuat password (lihat panduan hardening di `README.md`), password
> itu ikut tercetak di log. Pertimbangkan menyamarkannya sebelum produksi.

## 2. Koleksi

ID dokumen memakai string yang bermakna (bukan `ObjectId`), kecuali
`scan_events`, `class_students`, dan `face_templates` yang memakai `_id`
otomatis.

### `classes`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `_id` | string | kode kelas, mis. `IK24-A` (huruf besar) |
| `name` | string | nama tampilan, mis. `Ilmu Komputer A` |

### `students`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `_id` | string | `student-<nim huruf kecil>`, mis. `student-ik24001` |
| `nim` | string | huruf besar, **unik** |
| `name` | string | nama lengkap |
| `email` | string | saat ini selalu `""` |
| `class_id` | string | kelas utama → `classes._id` |

`students.class_id` adalah **sumber kebenaran** keanggotaan kelas yang dipakai
query (ringkasan kelas, roster, cek anggota kelas saat scan).

### `class_students`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `class_id` | string | → `classes._id` |
| `student_id` | string | → `students._id` |

Relasi ini ditulis saat seed dan saat `CreateStudent`, tetapi **belum dibaca**
oleh query mana pun. Ia disiapkan untuk mahasiswa yang mengikuti lebih dari
satu kelas. Bila kelak dipakai, pastikan kedua sumber tetap sinkron.

### `users` (akun staf)

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `_id` | string | 32 karakter hex acak (`newID`) |
| `username` | string | **unik** (index biasa, case-sensitive); pencarian login memakai regex case-insensitive |
| `display_name` | string | nama tampilan |
| `role` | string | `administrator` atau `dosen` |
| `password_hash` | binary | bcrypt (`DefaultCost`), password minimal 12 karakter |
| `active` | bool | `false` → login & session ditolak |
| `class_ids` | array string | kelas yang ditugaskan (untuk dosen) |
| `created_at` | date | UTC |

Penugasan kelas dosen disimpan langsung di `users.class_ids` (pengganti tabel
`user_classes` di versi SQLite). Middleware membacanya ulang di **setiap
request**, jadi perubahan langsung berlaku tanpa logout.

> Karena index unik `username` bersifat case-sensitive sedangkan login
> case-insensitive, `Admin` dan `admin` bisa tersimpan sebagai dua dokumen
> lewat `CreateUser`. `EnsureUser` (dipakai bootstrap dosen) sudah mencegah
> hal ini. Hindari membuat akun yang hanya berbeda huruf besar/kecil.

### `devices`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `_id` | string | ID perangkat, mis. `ESP32-GED-A1` |
| `name` | string | nama tampilan |
| `last_seen` | string | RFC3339 UTC; diisi oleh scan, ping, atau health |

Status "online" di dashboard = ada perangkat dengan `last_seen` < **2 menit**
yang lalu (diambil satu dokumen dengan `last_seen` terbaru).

> Endpoint `/api/v1/devices/ping` dan `/api/v1/health?device_id=…` melakukan
> **upsert**: ID perangkat yang belum ada akan dibuat otomatis dengan nama
> `Perangkat <id>`. Lihat `docs/devices.md` §5.

### `face_templates`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `student_id` | string | → `students._id`, **unik** (satu template per mahasiswa) |
| `encoding` | — | embedding wajah 128 dimensi |
| `created_at` | — | waktu enrollment |

Go **tidak menulis** koleksi ini; Go hanya menghitung dokumennya untuk status
"wajah terdaftar". Penulisnya adalah face service Python (`/enroll`). Format
pasti `encoding`/`created_at` ditentukan oleh implementasi Python — lihat
`docs/face-service.md`.

### `scan_events`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `student_id` | string | kandidat dari face service; `""` bila tidak dikenali |
| `class_id` | string | kelas yang dikirim perangkat |
| `device_id` | string | perangkat pengirim |
| `scanned_at` | string | `captured_at` perangkat, diubah ke **RFC3339 UTC** |
| `face_verified` | bool | face service menyatakan cocok dan ada `studentId` |
| `liveness_passed` | bool | klaim perangkat (bukan deteksi biologis) |
| `confidence` | number | dari face service (`1 - distance`) |
| `status` | string | `Hadir` / `Perlu ditinjau` / `Ditolak` |
| `review_reason` | string | alasan untuk status selain `Hadir` |

## 3. Index

Dibuat oleh `migrate()`:

| Koleksi | Index | Opsi |
| --- | --- | --- |
| `users` | `{username: 1}` | unique |
| `students` | `{nim: 1}` | unique |
| `students` | `{class_id: 1}` | — |
| `class_students` | `{class_id: 1, student_id: 1}` | unique |
| `face_templates` | `{student_id: 1}` | unique |
| `scan_events` | `{scanned_at: -1}` | — |
| `scan_events` | `{class_id: 1, scanned_at: -1}` | — |
| `scan_events` | `{student_id: 1, scanned_at: -1}` | — |
| `scan_events` | `{status: 1}` | — |

## 4. Waktu dan "hari ini"

- `scan_events.scanned_at` dan `devices.last_seen` disimpan sebagai **string
  RFC3339 UTC** (`2026-10-21T00:15:00Z`), bukan tipe `Date` BSON.
- Filter "hari ini" dihitung di Go oleh `todayRangeUTC()`: tengah malam
  **zona waktu lokal server** sampai +24 jam, dikonversi ke UTC, lalu
  dibandingkan sebagai string (`$gte` / `$lt`). Perbandingan leksikografis ini
  benar selama semua nilai berformat RFC3339 UTC yang seragam (akhiran `Z`).
- Akibatnya zona waktu server menentukan batas hari. Untuk ITBJH (WITA,
  UTC+8) jalankan server dengan zona waktu `Asia/Makassar` (atau set `TZ`).
- Jam di tampilan memakai `time.Local` (`formatTime` → `15:04`).

Jangan menulis `scanned_at` dalam format lain (mis. dengan offset `+08:00`):
data itu akan salah masuk/keluar dari rentang "hari ini".

## 5. Data seed

| Jenis | Data |
| --- | --- |
| Kelas | `IK24-A` Ilmu Komputer A, `IK24-B` Ilmu Komputer B, `IK24-C` Ilmu Komputer C |
| Mahasiswa | Alya Putri `IK24001` (A), Bima Saputra `IK24002` (A), Citra Nabila `IK24003` (B), Dafa Ramadhan `IK24004` (B), Eka Anindya `IK24005` (C) |
| Perangkat | `ESP32-GED-A1` ESP32 Gedung A, `ESP32-GED-B1` ESP32 Gedung B |
| Akun staf | **tidak ada** — dibuat dari `BOOTSTRAP_*` (lihat `README.md`) |
| Template wajah / scan | **tidak ada** |

Seed berjalan di setiap startup tetapi hanya menyisipkan dokumen yang belum
ada. Menghapus mahasiswa seed lalu me-restart server akan memunculkannya lagi.

## 6. Operasi umum (mongosh)

```javascript
use attendance

// Menugaskan kelas ke dosen
db.users.updateOne({ username: "rina" }, { $set: { class_ids: ["IK24-A", "IK24-C"] } })

// Menonaktifkan akun staf (sesi aktifnya ikut ditolak pada request berikutnya)
db.users.updateOne({ username: "rina" }, { $set: { active: false } })

// Menambah kelas baru (ingat: opsi filter kelas di view masih hardcoded)
db.classes.insertOne({ _id: "IK24-D", name: "Ilmu Komputer D" })

// Mendaftarkan perangkat baru secara eksplisit
db.devices.insertOne({ _id: "ESP32-GED-C1", name: "ESP32 Gedung C" })

// Menghapus template biometrik seorang mahasiswa (permintaan penghapusan data)
db.face_templates.deleteOne({ student_id: "student-ik24001" })

// Scan hari ini yang perlu ditinjau
db.scan_events.find({ status: "Perlu ditinjau" }).sort({ scanned_at: -1 }).limit(20)
```

Belum ada UI untuk operasi di atas (lihat `HANDOFF.md` §Langkah Lanjutan).

## 7. Backup & pemulihan

```bash
# Backup (arsip terkompresi)
mongodump --uri="$MONGODB_URI" --db=attendance --archive=attendance-$(date +%F).gz --gzip

# Pemulihan
mongorestore --uri="$MONGODB_URI" --archive=attendance-2026-10-21.gz --gzip --drop
```

`face_templates` berisi data biometrik: simpan arsip backup terenkripsi,
batasi aksesnya, dan tetapkan masa retensi. Panduan hardening server MongoDB
(user aplikasi, `authorization`, `bindIp`, firewall, TLS) ada di `README.md`.

## 8. Mengubah skema

MongoDB tidak memerlukan `ALTER TABLE`, tetapi dokumen lama tidak otomatis
punya field baru:

1. Tambahkan field pada insert/update di `internal/api/server.go` (atau
   `internal/store/users.go`).
2. Pastikan pembaca menoleransi field yang hilang (nilai nol pada struct Go).
3. Bila perlu index, tambahkan di `migrate()`; `CreateOne` idempoten untuk
   definisi yang sama, tetapi **gagal** bila index dengan nama sama sudah ada
   dengan opsi berbeda — hapus index lama dulu.
4. Untuk backfill data lama, tulis skrip `mongosh` terpisah dan catat di
   `HANDOFF.md`. Belum ada mekanisme migrasi berversi.
