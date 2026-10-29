# Basis Data (MongoDB)

> Status: **berlaku** — sesuai `internal/store/database.go`, `internal/store/users.go`,
> dan query di `internal/api/server.go`, `face.go`, `images.go`, `admin.go`.
> Terakhir diselaraskan: 26 Oktober 2026.
> Versi lama proyek memakai SQLite; semua rujukan ke `DATABASE_PATH`,
> `data/attendance.db`, tabel `user_classes`, atau `date(…, 'localtime')`
> sudah **tidak berlaku**.

## 1. Koneksi

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `MONGODB_URI` | `mongodb://10.100.1.11:27017` | URI driver resmi `go.mongodb.org/mongo-driver` v1.17 |
| `MONGODB_DATABASE` | `attendance` | nama database; dibuat otomatis saat pertama ditulisi (`.env` pengembangan saat ini: `methics`) |

`store.Open` melakukan, berurutan:

1. `mongo.Connect` lalu `Ping` dengan batas waktu **8 detik**. Gagal → server
   berhenti dengan `initialize database (<uri>): …`.
2. `migrate` — membuat index (idempoten; aman dijalankan setiap startup).
3. `seed` — upsert kelas dan perangkat memakai `$setOnInsert`, sehingga
   **tidak menimpa** data yang sudah diubah (lihat §5). Mahasiswa tidak
   di-seed.

Nilai `MONGODB_*` dibaca dari `.env` dengan `godotenv.Overload` (menimpa
environment). Baris log `connected to MongoDB at … (database: …)` menunjukkan
database yang benar-benar dipakai.

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
| `_id` | string | `student-<nim>`, mis. `student-241011001` |
| `nim` | string | **9 digit angka** (mis. `241011001`), **unik** |
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

Relasi ini ditulis saat `CreateStudent`, tetapi **belum dibaca**
oleh query mana pun. Ia disiapkan untuk mahasiswa yang mengikuti lebih dari
satu kelas. Bila kelak dipakai, pastikan kedua sumber tetap sinkron.

### `users` (akun staf)

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `_id` | string | 32 karakter hex acak (`newID`) |
| `username` | string | **unik** (index biasa, case-sensitive); pencarian login memakai regex case-insensitive |
| `display_name` | string | nama tampilan |
| `role` | string | `administrator` atau `dosen` |
| `password_hash` | binary | bcrypt (`DefaultCost`); password minimal 12 karakter di production, 4 di development (`store.MinPasswordLength`) |
| `active` | bool | `false` → login & session ditolak |
| `class_ids` | array string | kelas yang ditugaskan (untuk dosen); diisi dari centang kelas di form `/akses` |
| `created_at` | date | UTC |

Penugasan kelas dosen disimpan langsung di `users.class_ids` (pengganti tabel
`user_classes` di versi SQLite). Middleware membacanya ulang di **setiap
request**, jadi perubahan langsung berlaku tanpa logout.

Akun dibuat oleh tiga jalur: bootstrap `.env` (`BOOTSTRAP_ADMIN_*`,
`BOOTSTRAP_LECTURER_*`; `BOOTSTRAP_RESET_PASSWORDS=true` menyetel ulang
keduanya setiap start di development), form "Buat akun dosen" di `/akses`
(`CreateLecturer`: username huruf kecil, role `dosen`), dan `POST /api/v1/staff`.
Menghapus akun dosen di `/akses` menghapus dokumennya; sesi dosen itu
langsung tidak berlaku.

> Karena index unik `username` bersifat case-sensitive sedangkan login
> case-insensitive, `Admin` dan `admin` bisa tersimpan sebagai dua dokumen
> lewat `CreateUser`. `EnsureUser`, `ResetUser`, dan `CreateLecturer` (form
> `/akses`) sudah memeriksa username tanpa membedakan huruf besar/kecil.

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
> `Perangkat <id>`. Lihat `docs/devices.md` §7.

### `face_templates`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `student_id` | string | → `students._id`, **unik** (satu dokumen per mahasiswa) |
| `encodings` | array of array double | 1–6 embedding ArcFace 512 dimensi (ternormalisasi L2), satu per foto pose |
| `template_count` | int | jumlah vektor di `encodings` |
| `dimension` | int | panjang tiap vektor |
| `detection_confidences` | array double | confidence deteksi wajah per foto |
| `created_at` | date | enrollment pertama (UTC) |
| `updated_at` | date | enrollment terakhir (UTC) |
| `image_keys` | array string, opsional | kunci semua foto enrollment di bucket S3 (Garage); hanya ada bila penyimpanan foto aktif |
| `image_key` | string, opsional | kunci foto pertama (dipakai tombol **Lihat foto**) |
| `encoding`, `detection_confidence` | lama | format sebelum 28 Okt 2026 (satu foto); tetap dibaca sebagai satu pose dan dihapus saat enrollment ulang |

Ditulis oleh **Go** (`enrollEmbeddings` di `internal/api/face.go`) dari
embedding yang dikembalikan face service `POST /register`; face service
sendiri tidak mengakses MongoDB. Saat scan, Go memuat semua dokumen koleksi
ini dan menghitung cosine similarity terhadap setiap pose (skor mahasiswa =
pose terbaik). Lihat `docs/face-service.md`.

### `scan_events`

| Field | Tipe | Keterangan |
| --- | --- | --- |
| `student_id` | string | mahasiswa yang cocok (suara frame terbanyak); `""` bila tidak dikenali |
| `class_id` | string | kelas tetap perangkat, atau — bila perangkat mengirim `class_id` kosong — kelas mahasiswa yang cocok; `""` untuk wajah tidak dikenal pada perangkat kelas otomatis |
| `device_id` | string | perangkat pengirim |
| `scanned_at` | string | `captured_at` perangkat, diubah ke **RFC3339 UTC** |
| `face_verified` | bool | ada mahasiswa yang cocok (≥ 1 frame dengan skor ≥ `FACE_SIM_THRESHOLD`) |
| `liveness_passed` | bool | klaim perangkat (bukan deteksi biologis) |
| `confidence` | number | cosine similarity terbaik terhadap `face_templates` (0 bila tidak ada wajah) |
| `frames_total` | int | jumlah frame yang dikirim perangkat (1–6) |
| `frames_with_face` | int | frame yang diproses dan berisi wajah (Go berhenti lebih awal begitu cukup frame cocok) |
| `frames_matched` | int | frame yang cocok dengan `student_id`; ≥ `SCAN_MIN_MATCH_FRAMES` → `Hadir` |
| `status` | string | `Hadir` / `Perlu ditinjau` / `Ditolak` |
| `review_reason` | string | alasan untuk status selain `Hadir` |
| `image_key` | string, opsional | kunci satu frame bukti (skor terbaik) di bucket S3; dihapus (`$unset`) saat foto melewati masa retensi |

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
| `scan_events` | `{image_key: 1}` | sparse |
| `face_templates` | `{image_key: 1}` | sparse |
| `face_templates` | `{image_keys: 1}` | sparse, multikey |

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
| Mahasiswa | **tidak ada** — didaftarkan manual (NIM 9 digit, mis. `241011001`) |
| Perangkat | `ESP32-GED-A1` ESP32 Gedung A, `ESP32-GED-B1` ESP32 Gedung B |
| Akun staf | **tidak ada** — dibuat dari `BOOTSTRAP_*` (lihat `README.md`) |
| Template wajah / scan | **tidak ada** |

Seed berjalan di setiap startup tetapi hanya menyisipkan kelas dan perangkat
yang belum ada. Versi lama juga men-seed mahasiswa `IK24001`–`IK24005`; bila
masih ada di database, hapus sekali lewat halaman Mahasiswa (tidak akan
dibuat lagi), atau lewat `mongosh`:

```javascript
db.students.find({ nim: /^IK24/ })          // cek dulu
// lalu hapus lewat tombol hapus di dashboard agar template, foto, dan scan ikut terhapus
```

## 5a. Penghapusan mahasiswa

`DELETE /partials/mahasiswa/{nim}` (tombol hapus di halaman Mahasiswa) dan
`DELETE /api/v1/students/{nim}` menjalankan `DeleteStudent`
(`internal/api/admin.go`):

1. Mengumpulkan `image_key`/`image_keys` dari `face_templates` dan `image_key` dari `scan_events` mahasiswa
   itu, lalu menghapus fotonya dari Garage (gagal hanya dicatat di log).
2. Menghapus dokumen `scan_events`, `face_templates`, `class_students`, dan
   `students` milik mahasiswa itu (berurutan; tidak dalam transaksi).

Riwayat absensi mahasiswa tersebut ikut hilang dan tidak bisa dikembalikan
kecuali dari backup (§7).

## 6. Operasi umum (mongosh)

```javascript
use methics   // sesuaikan dengan MONGODB_DATABASE

// Melihat akun staf (tanpa hash password)
db.users.find({}, { username: 1, role: 1, class_ids: 1, active: 1 })

// Menugaskan kelas ke dosen
db.users.updateOne({ username: "rina" }, { $set: { class_ids: ["IK24-A", "IK24-C"] } })

// Menonaktifkan akun staf (sesi aktifnya ikut ditolak pada request berikutnya)
db.users.updateOne({ username: "rina" }, { $set: { active: false } })

// Menambah kelas baru (ingat: opsi filter kelas di view masih hardcoded)
db.classes.insertOne({ _id: "IK24-D", name: "Ilmu Komputer D" })

// Mendaftarkan perangkat baru secara eksplisit
db.devices.insertOne({ _id: "ESP32-GED-C1", name: "ESP32 Gedung C" })

// Melihat jumlah pose dan kunci foto tiap template (tanpa vektor 512-d)
db.face_templates.find({}, { student_id: 1, template_count: 1, dimension: 1, image_keys: 1 })

// Template format lama (satu foto) yang sebaiknya di-enroll ulang
db.face_templates.find({ encoding: { $exists: true } }, { student_id: 1 })

// Scan hari ini yang perlu ditinjau
db.scan_events.find({ status: "Perlu ditinjau" }).sort({ scanned_at: -1 }).limit(20)
```

Membuat/menghapus akun dosen dan menghapus mahasiswa (beserta data
biometrik) sudah tersedia di UI. Mengubah kelas dosen, menonaktifkan akun,
menambah kelas, dan mendaftarkan perangkat belum ada di UI (lihat `HANDOFF.md`
§Langkah Lanjutan). Jangan menghapus `face_templates` langsung bila fotonya
ada di Garage; gunakan hapus mahasiswa agar foto ikut terhapus.

## 7. Backup & pemulihan

```bash
# Backup (arsip terkompresi)
mongodump --uri="$MONGODB_URI" --db="$MONGODB_DATABASE" --archive=methics-$(date +%F).gz --gzip

# Pemulihan
mongorestore --uri="$MONGODB_URI" --archive=methics-2026-10-26.gz --gzip --drop
```

Backup MongoDB tidak mencakup foto di Garage; cadangkan bucket terpisah bila
foto perlu dipulihkan. `face_templates` berisi data biometrik: simpan arsip backup terenkripsi,
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
