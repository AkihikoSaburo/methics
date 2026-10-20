# Kontrak API Algorithmics Face Recognation

Acuan: `cmd/server/main.go` (routing), `internal/api/server.go` (handler JSON),
`internal/controller/partials.go` (fragment htmx). Bila dokumen ini berbeda
dengan kode, **kode yang berlaku** — lalu perbarui dokumen ini.

Go adalah satu-satunya pintu masuk untuk browser dan perangkat. Face service
Python bersifat internal di belakang Go (kontraknya di
[docs/face-service.md](docs/face-service.md)).

## 1. Peta endpoint

| Kelompok | Method & path | Auth | Respons |
| --- | --- | --- | --- |
| Halaman | `GET /`, `/mahasiswa`, `/kelas`, `/laporan`, `/logs` | session staf | HTML |
| Halaman | `GET /akses` | session administrator (dosen dialihkan ke `/`) | HTML |
| Auth | `GET /login`, `POST /login`, `POST /logout` | — / CSRF | HTML / redirect |
| Fragment | `GET/POST /partials/*` (§5) | session staf (+ CSRF untuk POST) | HTML |
| API staf | `GET /api/v1/dashboard`, `/classes`, `/reports` | session staf | JSON |
| API staf | `POST /api/v1/students`, `/students/{nim}/face-template` | session administrator + CSRF | JSON |
| Perangkat | `POST /api/v1/scans` | bearer `DEVICE_TOKEN` + HMAC + timestamp | JSON |
| Perangkat | `GET\|POST /api/v1/devices/ping` | bearer `DEVICE_TOKEN` (hanya production) | JSON |
| Publik | `GET /api/v1/health` | — | JSON |
| Aset | `GET /static/*` | — | file |

Halaman server-rendered **tidak** membaca `/api/v1`; halaman memuat datanya
dari `/partials/*`. Endpoint `/api/v1` adalah API JSON untuk integrasi,
pengujian, dan perangkat. Keduanya memakai query & validasi yang sama di
`internal/api/server.go`, jadi datanya konsisten.

## 2. Autentikasi

### Staf (browser)

- Login `POST /login` (form `username`, `password`, `next`, `csrf_token`).
  Berhasil → session token diperbarui, redirect `303` ke `next` (hanya path
  lokal; selain itu ke `/`). Gagal → halaman login dengan pesan.
- Rate limit login: **10 percobaan per IP per 5 menit**; lebih dari itu
  `429` + header `Retry-After: 300`.
- Session cookie `itbjh_session`: HttpOnly, SameSite=Lax, umur 8 jam, idle
  30 menit, `Secure` bila `APP_ENV=production`. Disimpan **in-memory**.
- CSRF (gorilla/csrf, cookie `itbjh_csrf`) berlaku untuk semua method tidak
  aman di jalur staf, termasuk `POST /api/v1/*` staf. Kirim lewat field form
  `csrf_token` atau header `X-CSRF-Token`. Token tersedia di
  `<meta name="csrf-token">` pada halaman. Token salah → `403` (teks biasa).
- Role dan kelas dosen dibaca ulang dari MongoDB di setiap request
  (`users.role`, `users.class_ids`). Role dari query/header klien tidak
  pernah dipakai.

### Perangkat

Lihat §4 dan [docs/devices.md](docs/devices.md). Endpoint perangkat
dipasang di luar session/CSRF.

## 3. Format error JSON

Semua error dari `/api/v1/*` berbentuk:

```json
{ "error": { "code": "invalid_signature", "message": "Signature scan tidak valid atau timestamp kedaluwarsa." } }
```

| HTTP | `code` | Sumber |
| --- | --- | --- |
| 401 | `unauthorized` | session tidak ada/kedaluwarsa (API staf & `/partials/*`) |
| 403 | `forbidden` | role tidak diizinkan (mis. dosen memanggil `POST /api/v1/students`) |
| 400 | `invalid_json` | body JSON rusak / > 1 MiB |
| 400 | `missing_fields` | field wajib kosong |
| 400 | `invalid_class`, `unknown_class` | kelas tidak ada |
| 409 | `student_exists` | NIM sudah terdaftar |
| 404 | `student_not_found` | NIM tidak ditemukan saat enrollment |
| 400 | `invalid_image` | bukan multipart, field `image` hilang, kosong, atau > 8 MiB |
| 401 | `invalid_device` | bearer token perangkat salah (scan) |
| 401 | `invalid_token` | bearer token perangkat salah (ping, production) |
| 401 | `invalid_signature` | HMAC salah atau `X-Device-Timestamp` > ±2 menit |
| 400 | `stale_capture` | `captured_at` bukan RFC3339 atau > ±5 menit |
| 400 | `invalid_liveness` | `liveness_passed` bukan `true`/`false` |
| 403 | `unknown_device` | `device_id` tidak ada di koleksi `devices` |
| 502 | `face_service_error` | face service gagal/timeout (8 detik) |
| 500 | `database_error`, `enroll_error` | kegagalan internal |

## 4. API JSON

### `GET /api/v1/health` — publik

```json
{ "status": "ok", "faceVerification": { "online": false, "provider": "Python service" } }
```

`faceVerification.online` = face service menjawab `GET /health` dengan
`200` dan `modelReady: true`. `provider` bernilai `Python face_recognition`
bila service menjawab `200` dengan JSON valid, dan `Python service` bila
tidak terjangkau. Opsional `?device_id=<id>` memperbarui `devices.last_seen`
(upsert, **tanpa token** — lihat catatan keamanan di `docs/devices.md` §5).

### `GET /api/v1/dashboard` — staf

Administrator menerima semua kelas; dosen hanya `users.class_ids`. Data
"hari ini" mengikuti zona waktu server.

```json
{
  "summary": { "students": 5, "classes": 3, "verifiedScans": 3, "scansToReview": 0, "faceTemplates": 2 },
  "classes": [
    { "id": "IK24-A", "name": "Ilmu Komputer A", "present": 2, "studentCount": 2 }
  ],
  "integrations": {
    "esp32": { "online": true, "lastSeen": "08:11" },
    "faceVerification": { "online": true, "provider": "Python face_recognition" }
  },
  "attendance": [
    {
      "student": { "id": "student-ik24001", "nim": "IK24001", "name": "Alya Putri" },
      "class": { "id": "IK24-A", "name": "Ilmu Komputer A" },
      "scannedAt": "2026-10-21T00:02:00Z",
      "faceVerified": true,
      "faceEnrolled": true,
      "deviceId": "ESP32-GED-A1",
      "status": "Hadir"
    }
  ]
}
```

- `attendance` = seluruh roster dalam cakupan + scan **terakhir hari ini**
  per mahasiswa. Belum ada scan → `scannedAt: null`, `status: "Belum hadir"`.
- `scannedAt` berupa RFC3339 UTC; `esp32.lastSeen` berupa jam lokal `HH:MM`
  (kosong bila belum pernah terlihat). `esp32.online` = `last_seen` < 2 menit.

### `GET /api/v1/classes` — staf

```json
{
  "summary": { "classes": 3, "students": 5, "enrolled": 2, "present": 1 },
  "classes": [
    { "id": "IK24-A", "name": "Ilmu Komputer A", "studentCount": 2, "enrolled": 1, "present": 1, "attendanceRate": 50 }
  ]
}
```

`present` = jumlah mahasiswa unik berstatus `Hadir` hari ini;
`attendanceRate` = `present * 100 / studentCount` (dibulatkan ke bawah).

### `GET /api/v1/reports` — staf

```json
{
  "summary": { "verifiedScans": 1, "reviewScans": 0, "rejectedScans": 0, "students": 5, "present": 1, "attendanceRate": 20 },
  "statusBreakdown": [ { "status": "Hadir", "count": 1 } ],
  "classes": [ { "id": "IK24-A", "name": "Ilmu Komputer A", "studentCount": 2, "enrolled": 1, "present": 1, "attendanceRate": 50 } ],
  "events": [
    {
      "student": { "nim": "IK24001", "name": "Alya Putri" },
      "class": { "id": "IK24-A", "name": "Ilmu Komputer A" },
      "scannedAt": "2026-10-21T00:02:00Z",
      "status": "Hadir",
      "confidence": 0.91,
      "reviewReason": "",
      "deviceId": "ESP32-GED-A1"
    }
  ]
}
```

`summary` dan `statusBreakdown` dihitung untuk **hari ini**; `events` adalah
**50 event terbaru tanpa batas tanggal**. `statusBreakdown` hanya memuat
status yang jumlahnya > 0.

### `POST /api/v1/students` — administrator + CSRF

Request `application/json` (maks 1 MiB):

```json
{ "name": "Fajar Nugroho", "nim": "IK24006", "classId": "IK24-A" }
```

`nim` dan `classId` di-uppercase. Respons `201`:

```json
{ "id": "student-ik24006", "nim": "IK24006", "name": "Fajar Nugroho", "classId": "IK24-A", "faceEnrolled": false }
```

Error: `400 invalid_json | missing_fields | invalid_class`, `409 student_exists`.

### `POST /api/v1/students/{nim}/face-template` — administrator + CSRF

`multipart/form-data` dengan file `image` (≤ 8 MiB). Go meneruskan ke
face service `POST /enroll` dengan `student_id` = `_id` mahasiswa. Respons
`201`:

```json
{ "nim": "IK24001", "faceEnrolled": true, "result": { "…": "body dari face service" } }
```

Error: `400 invalid_image`, `404 student_not_found`, `502 face_service_error`.

### `POST /api/v1/scans` — perangkat

`multipart/form-data`: `device_id`, `class_id`, `captured_at` (RFC3339),
`liveness_passed` (`true`/`false`), file `image` (≤ 8 MiB). Header:
`Authorization: Bearer <DEVICE_TOKEN>`, `X-Device-Timestamp` (RFC3339),
`X-Device-Signature` (hex HMAC-SHA256).

Pesan HMAC = enam baris digabung `\n`, kunci = `DEVICE_TOKEN`:

```text
<X-Device-Timestamp RFC3339>
<device_id>
<class_id huruf besar>
<captured_at persis seperti field>
<liveness_passed lowercase true/false>
<hex SHA-256 dari byte image>
```

Urutan validasi: token → gambar → field wajib → `captured_at` (±5 menit) →
`liveness_passed` → HMAC & `X-Device-Timestamp` (±2 menit) → perangkat
terdaftar → kelas ada → face service `/verify`.

Penentuan status:

| Kondisi | `status` | `reviewReason` |
| --- | --- | --- |
| tidak cocok / tidak ada kandidat | `Ditolak` | `reason` dari face service |
| cocok, bukan anggota `class_id` | `Perlu ditinjau` | Mahasiswa terverifikasi tetapi bukan anggota kelas pada perangkat. |
| cocok, anggota kelas, `liveness_passed=false` | `Perlu ditinjau` | Cocok wajah; bukti liveness belum tersedia atau tidak lolos. |
| cocok, anggota kelas, `liveness_passed=true` | `Hadir` | — |

Event disimpan ke `scan_events` dan `devices.last_seen` diperbarui. Respons
`202`:

```json
{
  "status": "Hadir",
  "student": { "id": "student-ik24001", "nim": "IK24001", "name": "Alya Putri" },
  "faceVerified": true,
  "confidence": 0.91,
  "reviewReason": ""
}
```

`student` bernilai `null` bila wajah tidak cocok. Bila face service gagal,
respons `502` dan **tidak ada** event yang dicatat.

Penting: `liveness_passed` adalah klaim perangkat yang ditandatangani, bukan
deteksi biologis. HMAC melindungi integritas pesan, bukan membuktikan bahwa
wajah di gambar adalah orang hidup. Belum ada nonce anti-replay.

Contoh klien uji: [docs/devices.md](docs/devices.md) §4.

### `GET|POST /api/v1/devices/ping` — perangkat

Heartbeat untuk status "Terhubung". ID dibaca dari form `device_id`, query
`device_id`, atau header `X-Device-Id` (default `ESP32-GED-A1`). Token
bearer hanya diperiksa saat `APP_ENV=production`. Upsert `devices`.

```json
{ "status": "ok", "device_id": "ESP32-GED-A1", "last_seen": "2026-10-21T00:15:00Z", "message": "Perangkat berhasil terhubung dan status aktif." }
```

## 5. Fragment htmx (`/partials/*`)

Semua wajib session staf, membalas HTML, dan memakai query ber-scope role.
Sesi tidak valid → `401` JSON (`app.js` lalu mengalihkan ke `/login`).
POST wajib CSRF (`app.js` menambah header `X-CSRF-Token` otomatis).

| Endpoint | Guna | Akses | Penyegaran |
| --- | --- | --- | --- |
| `GET /partials/dashboard` | metrik, kehadiran per kelas, status perangkat/face service, 8 baris pertama roster hari ini (beserta scan terakhirnya) | staf | `load, every 15s` |
| `GET /partials/kelas` | ringkasan + tabel kelas | staf | `load, every 15s` |
| `GET /partials/laporan` | rekap hari ini, komposisi status, kehadiran per kelas, 50 scan terbaru | staf | `load, every 15s` |
| `GET /partials/mahasiswa?q=&class=` | tabel roster (+ ringkasan via `hx-swap-oob`) | staf | `load, every 15s`, ketik, ganti filter, event `roster-changed` |
| `GET /partials/mahasiswa/form` | form dialog pendaftaran | administrator | sekali saat halaman dimuat |
| `POST /partials/mahasiswa/students` | tambah mahasiswa (form-urlencoded `name`, `nim`, `classId`) | administrator | — |
| `POST /partials/mahasiswa/{nim}/face-template` | enrollment wajah (multipart `image`, dikirim otomatis saat file dipilih; `q`/`class` ikut lewat `hx-include` agar filter tetap) | administrator | — |
| `GET /partials/logs?q=&status=&class=` | log pemindaian seluruh waktu, maks 200 baris (+ ringkasan via `hx-swap-oob`) | staf | `load, every 15s`, ketik, ganti filter |

Konvensi:

- **Sukses POST** → HTML (form bersih) + header `HX-Trigger` JSON, mis.
  `{"toast":{"message":"Mahasiswa X berhasil ditambahkan."},"student-dialog-close":true,"roster-changed":true}`.
- **Gagal validasi** → **HTTP 200** dengan pesan inline di form/region, bukan
  status 4xx. Non-administrator → `403` teks biasa.
- ⚠️ **Bug yang diketahui:** pada `/partials/logs`, parameter `class` menimpa
  pembatasan kelas dosen di `AllScanEvents` (`filter["class_id"]` ditulis
  ulang), sehingga dosen dapat melihat log kelas lain lewat
  `?class=<kelas lain>`. Perbaikan: abaikan/ tolak `class` yang tidak ada di
  `users.class_ids` untuk role dosen.
- Ringkasan `/partials/logs` (total, terverifikasi, ditinjau, ditolak)
  dihitung untuk **seluruh waktu** dalam cakupan role dan **tidak** ikut
  filter; tabelnya mengikuti filter. Pencarian `q` mencocokkan nama atau NIM
  (regex case-insensitive, input di-escape).

## 6. Role

| Kemampuan | administrator | dosen | mahasiswa |
| --- | --- | --- | --- |
| Ringkasan, kelas, laporan, log, roster | semua kelas | kelas di `users.class_ids` | tidak ada akses |
| Daftarkan mahasiswa | ya | tidak | — |
| Enrollment wajah | ya | tidak | — |
| Halaman `/akses` | ya | dialihkan ke `/` | — |
| Absensi | — | — | lewat scan perangkat |

Mahasiswa adalah record roster/subjek absensi tanpa akun aplikasi. Tidak ada
`STUDENT_TOKEN` atau endpoint akses mahasiswa. Dosen tanpa kelas yang
ditugaskan melihat data kosong.

## 7. Privasi biometrik

- Jangan kirim gambar atau template wajah ke browser; halaman hanya menerima
  label status.
- Gambar enrollment/scan hanya diteruskan sementara ke face service; yang
  disimpan adalah embedding di `face_templates`.
- Lindungi template dengan kontrol akses ketat, enkripsi backup, retensi
  terbatas, serta prosedur persetujuan dan penghapusan.
