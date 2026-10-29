# Kontrak API Algorithmics Face Recognation

Acuan: `cmd/server/main.go` (routing), `internal/api/server.go` (handler JSON),
`internal/api/admin.go` (hapus mahasiswa, akun dosen), `internal/api/images.go`
(foto), `internal/controller/partials.go` dan `admin_partials.go` (fragment htmx). Bila dokumen ini berbeda
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
| Fragment | `GET/POST/DELETE /partials/*` (§5) | session staf (+ CSRF untuk POST/DELETE) | HTML |
| API staf | `GET /api/v1/dashboard`, `/classes`, `/reports` | session staf | JSON |
| API staf | `POST /api/v1/students`, `/students/{nim}/face-template` | session administrator + CSRF | JSON |
| API staf | `DELETE /api/v1/students/{nim}`, `GET\|POST /api/v1/staff`, `DELETE /api/v1/staff/{id}` | session administrator + CSRF | JSON |
| API staf | `GET /api/v1/images/{key...}` | session staf (dosen: hanya kelasnya) | gambar |
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
  aman di jalur staf, termasuk `POST`/`DELETE /api/v1/*` staf. Kirim lewat field form
  `csrf_token` atau header `X-CSRF-Token`. Token tersedia di
  `<meta name="csrf-token">` pada halaman. Token salah → `403` (teks biasa).
- Role dan kelas dosen dibaca ulang dari MongoDB di setiap request
  (`users.role`, `users.class_ids`). Role dari query/header klien tidak
  pernah dipakai. Akun yang dihapus atau dinonaktifkan langsung kehilangan
  sesinya pada request berikutnya.

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
| 400 | `invalid_nim` | NIM bukan 9 digit angka (format `241011001`) |
| 400 | `invalid_class`, `unknown_class` | kelas tidak ada |
| 409 | `student_exists` | NIM sudah terdaftar |
| 404 | `student_not_found` | NIM tidak ditemukan (enrollment / hapus) |
| 400 | `invalid_input` | data akun dosen tidak valid (username, password, kelas) |
| 409 | `username_taken` | username akun sudah dipakai |
| 403 | `not_lecturer` | mencoba menghapus akun administrator lewat `/api/v1/staff/{id}` |
| 404 | `user_not_found` | akun staf tidak ditemukan |
| 422 | `no_face` | salah satu foto enrollment tidak berisi wajah |
| 422 | `inconsistent_faces` | foto-foto enrollment bukan orang yang sama |
| 409 | `face_already_enrolled` | wajah pada foto enrollment sudah terdaftar atas NIM lain |
| 400 | `invalid_image` | bukan multipart, field `image` hilang, kosong, atau > 8 MiB |
| 401 | `invalid_device` | bearer token perangkat salah (scan) |
| 401 | `invalid_token` | bearer token perangkat salah (ping, production) |
| 401 | `invalid_signature` | HMAC salah atau `X-Device-Timestamp` > ±2 menit |
| 400 | `stale_capture` | `captured_at` bukan RFC3339 atau > ±5 menit |
| 400 | `invalid_liveness` | `liveness_passed` bukan `true`/`false` |
| 403 | `unknown_device` | `device_id` tidak ada di koleksi `devices` |
| 502 | `face_service_error` | face service gagal/timeout (`FACE_SERVICE_TIMEOUT_SECONDS`, default 15 detik) |
| 404 | `image_not_found`, `image_storage_disabled` | foto tidak tercatat/sudah dihapus, atau `S3_*` kosong |
| 502 | `image_storage_error` | Garage tidak dapat dihubungi saat mengambil foto |
| 500 | `database_error`, `enroll_error` | kegagalan internal |

## 4. API JSON

### `GET /api/v1/health` — publik

```json
{
  "status": "ok",
  "faceVerification": {
    "online": true,
    "provider": "Python YOLO-FaceV2 + ArcFace",
    "detectionModel": "face_detection/yolo-facev2s.onnx",
    "recognitionModel": "face_recognition/arcface_r50_fp16.onnx"
  },
  "imageStorage": { "enabled": true, "online": true }
}
```

`imageStorage.enabled` = `S3_*` terisi; `online` = `HEAD` bucket berhasil
dalam 3 detik.

`faceVerification.online` = face service menjawab `GET /health` dalam
3 detik dengan `200` dan `model_loaded: true`. Bila tidak terjangkau:
`{"online": false, "provider": "Python face service"}`. Opsional `?device_id=<id>` memperbarui `devices.last_seen`
(upsert, **tanpa token** — lihat catatan keamanan di `docs/devices.md` §7).

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
    "faceVerification": { "online": true, "provider": "Python YOLO-FaceV2 + ArcFace", "detectionModel": "…", "recognitionModel": "…" }
  },
  "attendance": [
    {
      "student": { "id": "student-241011001", "nim": "241011001", "name": "Alya Putri" },
      "class": { "id": "IK24-A", "name": "Ilmu Komputer A" },
      "scannedAt": "2026-10-21T00:02:00Z",
      "faceVerified": true,
      "faceEnrolled": true,
      "faceTemplates": 6,
      "faceImageUrl": "/api/v1/images/enroll/student-241011001/20261028T080000Z-3f9a1c2b4d5e.jpg",
      "faceImageUrls": ["/api/v1/images/enroll/student-241011001/20261028T080000Z-3f9a1c2b4d5e.jpg", "…"],
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
      "student": { "nim": "241011001", "name": "Alya Putri" },
      "class": { "id": "IK24-A", "name": "Ilmu Komputer A" },
      "scannedAt": "2026-10-21T00:02:00Z",
      "status": "Hadir",
      "confidence": 0.91,
      "reviewReason": "",
      "deviceId": "ESP32-GED-A1",
      "imageUrl": "/api/v1/images/scans/2026/10/21/000200Z-ESP32-GED-A1-3f9a1c2b4d5e.jpg"
    }
  ]
}
```

`imageUrl` kosong bila foto tidak disimpan atau sudah lewat masa retensi.
`summary` dan `statusBreakdown` dihitung untuk **hari ini**; `events` adalah
**50 event terbaru tanpa batas tanggal**. `statusBreakdown` hanya memuat
status yang jumlahnya > 0.

### `POST /api/v1/students` — administrator + CSRF

Request `application/json` (maks 1 MiB):

```json
{ "name": "Fajar Nugroho", "nim": "241011006", "classId": "IK24-A" }
```

`nim` wajib **9 digit angka** (mis. `241011001`); `classId` di-uppercase. Respons `201`:

```json
{ "id": "student-241011006", "nim": "241011006", "name": "Fajar Nugroho", "classId": "IK24-A", "faceEnrolled": false }
```

Error: `400 invalid_json | missing_fields | invalid_nim | invalid_class`, `409 student_exists`.

### `DELETE /api/v1/students/{nim}` — administrator + CSRF

Menghapus mahasiswa **beserta** template wajah, foto enrollment & foto scan di
Garage, seluruh event scan, dan relasi `class_students`. Tidak bisa dibatalkan.
Respons `200`: `{"nim":"241011001","name":"Alya Putri","deleted":true}`.
Error: `403 forbidden`, `404 student_not_found`.

Mahasiswa tidak di-seed, jadi mahasiswa yang dihapus tidak muncul lagi
setelah restart.

### `GET /api/v1/staff` · `POST /api/v1/staff` · `DELETE /api/v1/staff/{id}` — administrator + CSRF

- `GET` → `{"staff":[{"id","username","displayName","role","active","classIds","createdAt"}]}` (tanpa hash password).
- `POST` membuat **akun dosen**: JSON `{"username":"rina.wibowo","displayName":"Dr. Rina Wibowo","password":"…","classIds":["IK24-A"]}`.
  Username disimpan huruf kecil (`^[a-z0-9._-]{3,32}$`), password ≥ 12
  karakter di production (≥ 4 di development), minimal satu kelas yang ada.
  Respons `201`. Error: `400 invalid_input`, `409 username_taken`.
- `DELETE` menghapus **akun dosen** saja (`403 not_lecturer` untuk administrator,
  `404 user_not_found`). Sesi dosen itu langsung berakhir karena
  `RequireSession` memuat ulang user dari database setiap request.

### `POST /api/v1/students/{nim}/face-template` — administrator + CSRF

`multipart/form-data` dengan **1–6** file pada field `images` (field `image`
tetap diterima; ≤ 8 MiB per foto). Pose yang disarankan: depan, kiri,
kanan, atas, bawah, senyum. Go mengirim semua foto ke face service
`POST /register`, memastikan tiap foto berisi wajah, semua foto adalah orang
yang sama, dan wajahnya belum terdaftar atas NIM lain, lalu menyimpan semua
embedding 512-d ke satu dokumen `face_templates` (enrollment ulang
mengganti semua pose lama).

```bash
curl -b cookie.txt -H "X-CSRF-Token: <token>" \
  -F images=@depan.jpg -F images=@kiri.jpg -F images=@kanan.jpg \
  http://localhost:8080/api/v1/students/241011001/face-template
```

Respons `201`:

```json
{
  "nim": "241011001",
  "faceEnrolled": true,
  "result": {
    "studentId": "student-241011001", "templateCount": 3, "dimension": 512,
    "detectionConfidences": [0.93, 0.91, 0.9], "processingTimeMs": 412.7,
    "imageStored": true,
    "imageUrls": [
      "/api/v1/images/enroll/student-241011001/20261028T080000Z-3f9a1c2b4d5e.jpg",
      "/api/v1/images/enroll/student-241011001/20261028T080000Z-a1b2c3d4e5f6.jpg",
      "/api/v1/images/enroll/student-241011001/20261028T080000Z-0a1b2c3d4e5f.jpg"
    ]
  }
}
```

`imageStored`/`imageUrls` hanya muncul bila penyimpanan foto aktif. Gagal
menyimpan foto (Garage mati) **tidak** menggagalkan enrollment:
`imageStored: false`, template tetap tersimpan.

Error (pesan menyebut nomor foto bila relevan, mis. "foto ke-2"):

| Status | `error` | Arti |
| --- | --- | --- |
| 400 | `invalid_image` | tidak ada foto, lebih dari 6 foto, foto > 8 MiB, atau tidak bisa di-decode |
| 404 | `student_not_found` | NIM tidak ada |
| 422 | `no_face` | salah satu foto tidak berisi wajah |
| 422 | `inconsistent_faces` | salah satu foto bukan orang yang sama (kemiripan dengan foto pertama dan rata-rata foto lain < `FACE_POSE_THRESHOLD`, default 0.30) |
| 409 | `face_already_enrolled` | wajah sudah terdaftar atas NIM lain |
| 502 | `face_service_error` | face service mati, model belum siap, atau timeout |

### `POST /api/v1/scans` — perangkat

`multipart/form-data`: `device_id`, `class_id` (**opsional**; kosong = kelas
otomatis), `captured_at` (RFC3339), `liveness_passed` (`true`/`false`), dan
**1–6** file `image` berurutan (frame dari jendela scan ±2 detik; ≤ 8 MiB
per frame, total body ≤ 16 MiB). Header: `Authorization: Bearer
<DEVICE_TOKEN>`, `X-Device-Timestamp` (RFC3339), `X-Device-Signature` (hex
HMAC-SHA256).

Pesan HMAC = enam baris digabung `\n`, kunci = `DEVICE_TOKEN`:

```text
<X-Device-Timestamp RFC3339>
<device_id>
<class_id huruf besar, atau kosong>
<captured_at persis seperti field>
<liveness_passed lowercase true/false>
<hex SHA-256 dari semua frame digabung berurutan>   ← satu frame = SHA-256 gambar itu
```

Urutan validasi: token → frame → `device_id` → `captured_at` (±5 menit) →
`liveness_passed` → HMAC & `X-Device-Timestamp` (±2 menit) → perangkat
terdaftar → kelas ada (bila `class_id` diisi) → tiap frame ke face service
`POST /embed` → pencocokan cosine similarity di Go terhadap semua pose
`face_templates` (ambang `FACE_SIM_THRESHOLD`, default 0.45). Go berhenti
memproses frame begitu satu mahasiswa cocok di `SCAN_MIN_MATCH_FRAMES`
(default 2) frame. Frame tanpa wajah atau rusak dilewati.

Penentuan status (mahasiswa dengan frame cocok terbanyak menang):

| Kondisi | `status` | `result` | `reviewReason` |
| --- | --- | --- | --- |
| tidak ada wajah di semua frame | `Ditolak` | `wajah_tidak_terdeteksi` | "Wajah tidak terdeteksi pada 4 frame." |
| ada wajah, kemiripan < ambang / belum ada template | `Ditolak` | `tidak_terdaftar` | "Wajah tidak terdaftar (kemiripan tertinggi 0.31 < 0.45)." |
| cocok, `class_id` diisi dan mahasiswa bukan anggotanya | `Perlu ditinjau` | `perlu_ditinjau` | Mahasiswa terverifikasi tetapi bukan anggota kelas pada perangkat. |
| cocok di ≥ `SCAN_MIN_MATCH_FRAMES` frame, atau `liveness_passed=true` | `Hadir` | `hadir` | — |
| cocok di kurang dari `SCAN_MIN_MATCH_FRAMES` frame | `Perlu ditinjau` | `perlu_ditinjau` | "Wajah cocok di 1 dari 4 frame; minimal 2 frame agar langsung hadir." |

Dengan `class_id` kosong, event dicatat di **kelas mahasiswa yang cocok**
(`students.class_id`); event wajah tidak dikenal disimpan dengan `class_id`
kosong (terlihat oleh administrator di halaman Log). Event disimpan ke
`scan_events` (ditambah `frames_total`, `frames_with_face`,
`frames_matched`) dan `devices.last_seen` diperbarui. Respons `202`:

```json
{
  "status": "Hadir",
  "result": "hadir",
  "message": "Hadir",
  "student": { "id": "student-241011001", "nim": "241011001", "name": "Alya Putri" },
  "classId": "IK24-A",
  "faceVerified": true,
  "confidence": 0.91,
  "reviewReason": "",
  "framesTotal": 4,
  "framesWithFace": 2,
  "framesMatched": 2
}
```

`student` bernilai `null` bila wajah tidak cocok; `message` adalah teks
singkat untuk layar perangkat (`"Wajah tidak Terdaftar"`, `"Wajah tidak
terdeteksi"`, `"Hadir"`, atau alasan peninjauan). Bila face service gagal,
respons `502` dan **tidak ada** event yang dicatat.

> Konsensus beberapa frame **bukan** anti-spoof: foto cetak di depan kamera
> bisa tercatat Hadir. Lihat `docs/devices.md` §7.

Bila penyimpanan foto aktif, **satu** frame bukti (frame dengan skor terbaik,
atau frame berwajah pertama) diunggah ke Garage (batas 3 detik) sebelum event
disimpan, dan kuncinya dicatat di `scan_events.image_key`. Gagal unggah hanya
dicatat di log; event tetap disimpan tanpa foto.

### `GET /api/v1/images/{key...}` — staf

Mengalirkan foto enrollment (`enroll/...`) atau scan (`scans/...`) dari
Garage lewat Go; browser tidak pernah mengakses Garage langsung. Kunci harus
tercatat di `face_templates.image_key` atau `scan_events.image_key`.
Administrator boleh melihat semua foto; dosen hanya foto dari kelasnya.
Error: `401`, `403 forbidden`, `404 image_not_found` (termasuk foto yang sudah
dihapus karena retensi), `404 image_storage_disabled`, `502 image_storage_error`.
Halaman **Log** menampilkan tautan "Lihat" pada kolom Foto.

Penting: `liveness_passed` adalah klaim perangkat yang ditandatangani, bukan
deteksi biologis. HMAC melindungi integritas pesan, bukan membuktikan bahwa
wajah di gambar adalah orang hidup. Belum ada nonce anti-replay.

Contoh klien uji: [docs/devices.md](docs/devices.md) §6. Firmware perangkat yang memakai kontrak ini: `devices/esp32_scanner/` (lihat `docs/devices.md` §2).

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
POST/DELETE wajib CSRF (`app.js` menambah header `X-CSRF-Token` otomatis).
Filter `class` dari dosen yang bukan kelasnya menghasilkan data kosong.

| Endpoint | Guna | Akses | Penyegaran |
| --- | --- | --- | --- |
| `GET /partials/dashboard` | metrik, kehadiran per kelas, status perangkat/face service, 8 baris pertama roster hari ini (beserta scan terakhirnya) | staf | `load, every 15s` |
| `GET /partials/kelas` | ringkasan + tabel kelas | staf | `load, every 15s` |
| `GET /partials/laporan` | rekap hari ini, komposisi status, kehadiran per kelas, 50 scan terbaru | staf | `load, every 15s` |
| `GET /partials/mahasiswa?q=&class=` | tabel roster (+ ringkasan via `hx-swap-oob`) | staf | `load, every 15s`, ketik, ganti filter, event `roster-changed` |
| `GET /partials/mahasiswa/form` | form dialog pendaftaran | administrator | sekali saat halaman dimuat |
| `POST /partials/mahasiswa/students` | tambah mahasiswa (form-urlencoded `name`, `nim`, `classId`) | administrator | — |
| `POST /partials/mahasiswa/{nim}/face-template` | enrollment / enrollment ulang wajah (multipart `images`, 1–6 foto, dikirim otomatis saat file dipilih; `q`/`class` ikut lewat `hx-include` agar filter tetap) | administrator | — |
| `DELETE /partials/mahasiswa/{nim}` | hapus mahasiswa + data wajah, foto, riwayat scan (konfirmasi `hx-confirm`; `q`/`class` lewat `hx-include`) → roster | administrator | — |
| `GET /partials/akses/staff` | form buat dosen + tabel akun staf | administrator | sekali saat halaman `/akses` dimuat |
| `POST /partials/akses/lecturers` | buat akun dosen (form `username`, `displayName`, `password`, `classIds` berulang) → fragment akun | administrator | — |
| `DELETE /partials/akses/staff/{id}` | hapus akun dosen (konfirmasi) → fragment akun | administrator | — |
| `GET /partials/logs?q=&status=&class=` | log pemindaian seluruh waktu, maks 200 baris (+ ringkasan via `hx-swap-oob`) | staf | `load, every 15s`, ketik, ganti filter |

Konvensi:

- **Sukses POST** → HTML (form bersih) + header `HX-Trigger` JSON, mis.
  `{"toast":{"message":"Mahasiswa X berhasil ditambahkan."},"student-dialog-close":true,"roster-changed":true}`.
  Enrollment, hapus mahasiswa, dan aksi akun dosen mengembalikan fragmen yang
  sama (roster / akun) dengan pesan sukses atau gagal di atas tabel.
- **Hapus** memakai `hx-confirm` (dialog konfirmasi browser) sebelum request
  `DELETE` dikirim.
- **Gagal validasi** → **HTTP 200** dengan pesan inline di form/region, bukan
  status 4xx. Non-administrator → `403` teks biasa.
- Ringkasan `/partials/logs` (total, terverifikasi, ditinjau, ditolak)
  dihitung untuk **seluruh waktu** dalam cakupan role dan **tidak** ikut
  filter; tabelnya mengikuti filter. Pencarian `q` mencocokkan nama atau NIM
  (regex case-insensitive, input di-escape).

## 6. Role

| Kemampuan | administrator | dosen | mahasiswa |
| --- | --- | --- | --- |
| Ringkasan, kelas, laporan, log, roster | semua kelas | kelas di `users.class_ids` | tidak ada akses |
| Daftarkan & hapus mahasiswa | ya | tidak | — |
| Buat & hapus akun dosen (halaman `/akses`) | ya | tidak | — |
| Enrollment wajah | ya | tidak | — |
| Halaman `/akses` | ya | dialihkan ke `/` | — |
| Absensi | — | — | lewat scan perangkat |

Mahasiswa adalah record roster/subjek absensi tanpa akun aplikasi. Tidak ada
`STUDENT_TOKEN` atau endpoint akses mahasiswa. Dosen tanpa kelas yang
ditugaskan melihat data kosong.

## 7. Privasi biometrik

- Template wajah (embedding) tidak pernah dikirim ke browser.
- Foto enrollment dan scan disimpan di bucket Garage privat bila `S3_*`
  diisi, dan hanya bisa dilihat staf lewat `GET /api/v1/images/...` dengan
  pembatasan kelas. Foto scan dihapus otomatis setelah
  `IMAGE_RETENTION_DAYS` (Hadir) / `IMAGE_REVIEW_RETENTION_DAYS` (lainnya);
  foto enrollment lama dihapus saat enrollment ulang.
- Menghapus mahasiswa (`DELETE /api/v1/students/{nim}` atau tombol di halaman
  Mahasiswa) menghapus template, foto enrollment, foto scan, dan riwayat
  scannya sekaligus.
- Lindungi template dengan kontrol akses ketat, enkripsi backup, retensi
  terbatas, serta prosedur persetujuan.
