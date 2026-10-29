# Panduan Kode Algorithmics Face Recognation

Dokumen ini menjelaskan **bagaimana kode disusun dan mengapa**, agar perubahan
berikutnya konsisten dengan pola yang ada. Untuk gambaran besar, lihat
[architecture.md](architecture.md). Untuk kontrak API, lihat [API.md](../API.md).
Untuk skema data, lihat [database.md](database.md). Terakhir diselaraskan dengan
kode: 26 Oktober 2026 (malam).

## 1. Prinsip yang dipegang

1. **Server-rendered, bukan SPA.** Halaman dirender Go dengan `html/template`;
   htmx hanya menukar potongan HTML. Tidak ada logika role di klien.
2. **Satu sumber query & validasi.** Endpoint JSON (`/api/v1/*`) dan delapan
   endpoint fragment (`/partials/*`) memakai fungsi yang sama di
   `internal/api/server.go`. Tidak ada query MongoDB atau aturan validasi yang
   ditulis dua kali.
3. **Otorisasi di server, dari database.** Role dan daftar kelas dosen diambil
   ulang dari DB setiap request (`security.RequireSession`). Query/header role
   dari klien tidak pernah dipercaya.
4. **Tidak ada template biometrik ke browser.** Embedding tidak pernah dikirim;
   foto hanya dialirkan lewat endpoint ber-otorisasi (`/api/v1/images/...`).

## 2. Peta direktori

```text
cmd/server/main.go            entry point: env, routing, middleware, bootstrap, shutdown
internal/api/server.go        endpoint JSON + query ber-scope role + domain method + error sentinel
internal/api/face.go          klien face service, enrollment embedding, pencocokan cosine 1:N
internal/api/scan.go          POST /api/v1/scans: frame multi, HMAC, konsensus frame, status + result
internal/api/scan_test.go     unit test konsensus frame, status, hash/HMAC, pembacaan frame
internal/api/face_test.go     unit test klien & pencocokan (tanpa MongoDB)
internal/api/images.go        foto wajah di S3: unggah, otorisasi & stream, retensi
internal/api/admin.go         hapus mahasiswa (+ data wajah/foto/scan), buat & hapus akun dosen + endpoint JSON-nya
internal/api/admin_test.go    unit test validasi akun dosen (tanpa MongoDB)
internal/api/images_test.go   unit test kunci objek, unggah gagal, storage nonaktif
internal/storage/s3.go        klien S3 minimal (SigV4, gaya path) untuk Garage
internal/storage/s3_test.go   vektor uji AWS SigV4 + round-trip ke S3 tiruan
internal/controller/
  web.go                      render halaman penuh (shell) + login page
  partials.go                 render fragment htmx (/partials/*)
  admin_partials.go           fragment administrator: hapus mahasiswa, akun dosen (/akses)
  fragments_test.go           render fragment akun staf & tombol hapus roster (tanpa MongoDB)
  auth.go                     login, logout, safeNext
internal/model/page.go        view model untuk halaman (judul, role, tanggal, csrf)
internal/security/middleware.go
                              session SCS, Principal, RequireSession, RequireRole,
                              LoginRateLimit, SecurityHeaders (CSP), Recover
internal/store/
  database.go                 Open (connect+ping MongoDB), migrate (index), seed (kelas/perangkat)
  users.go                    User, CreateUser, EnsureUser, ResetUser, AuthenticateUser, UserByID,
                              ListStaff, UsernameTaken, UserRole, DeleteUser, MinPasswordLength
services/face/                face service Python (FastAPI: /health, /embed, /register → embedding ArcFace 512-d)
devices/esp32_scanner/        firmware ESP32 utama (sensor, TFT, buzzer, Wi-Fi, scan, heartbeat)
devices/esp32cam_uart/        firmware ESP32-CAM (foto via UART)
devices/esp32cam_wifi/        firmware ESP32-CAM (mode uji Wi-Fi, pengganti UART)
devices/{esp8266_ping,esp32cam_stream,algorithmics_engine_ultrasonic_buzzer}/  sketch uji
views/
  layouts/base.html           kerangka halaman (head, sidebar, topbar, content)
  pages/*.html                shell per halaman (isi div tempat htmx menaruh fragment)
  partials/*.html             sidebar, topbar, toast
  fragments/*.html            fragment htmx ({{define}} saja)
  login.html                  dokumen login mandiri
static/css/*                  tokens, base, layout, components, pages, responsive, print
static/js/app.js              setup htmx + perilaku global
static/js/lib/ui.js           refreshIcons, toast (helper lain sisa versi pra-htmx, tidak dipakai)
static/js/vendor/*            htmx, lucide (versi terpin, di-vendor lokal)
```

## 3. Empat alur request

### 3.1 Halaman staf (muat penuh)

```text
GET /mahasiswa
  → sessions.LoadAndSave          (memuat sesi dari cookie itbjh_session)
  → csrf.Protect                  (menyediakan token untuk form)
  → RequireSession                (ambil user dari DB → Principal ke context)
  → Web.Students                  (render shell dari views/pages/mahasiswa.html)
```

Shell hanya mengirim kerangka + elemen dengan `hx-get`/`hx-trigger` yang
menarik fragment. Jadi HTML awal kecil, data diisi menyusul.

### 3.2 Fragment htmx (pembaruan sebagian)

```text
GET /partials/mahasiswa
  → RequireSession (apiRequest=true)   (401 JSON bila sesi habis)
  → Partials.mahasiswa
      → api.Server.Roster / ClassRows / ...   (query ber-scope role)
  → render views/fragments/mahasiswa.html
```

Untuk POST, htmx menambahkan header CSRF lewat `htmx:configRequest` di
`static/js/app.js`:

```text
POST /partials/mahasiswa/students
  Header: X-CSRF-Token: <token dari <meta name="csrf-token">>
  → csrf.Protect memvalidasi
  → Partials.createStudent → api.Server.CreateStudent
```

Sukses **tidak** mengembalikan JSON, tapi HTML form + header `HX-Trigger`
berisi event (`toast`, `student-dialog-close`, `roster-changed`). Gagal
validasi mengembalikan **HTTP 200** dengan pesan di dalam form (error inline),
bukan status 4xx.

### 3.3 Scan perangkat ESP32

```text
POST /api/v1/scans                               (internal/api/scan.go)
  → (tanpa sesi, tanpa CSRF)
  → validDeviceToken        (Authorization: Bearer <DEVICE_TOKEN>, perbandingan konstan-waktu)
  → readScanFrames          (1–6 file "image" berurutan, maks 8 MB per frame, 16 MB total)
  → cek device_id, captured_at RFC3339 ±5 menit, liveness_passed true/false
  → validScanSignature      (HMAC-SHA256 atas X-Device-Timestamp ±2 menit + fields + framesHash)
  → cek devices._id ada; classes._id ada bila class_id diisi (kosong = kelas otomatis)
  → loadTemplates sekali, lalu matchFrames: POST /embed per frame (timeout 15 s per frame)
      → 422 "No face" / 400 → frame dilewati; error lain → 502
      → bestTemplateMatch per frame (pose terbaik ≥ FACE_SIM_THRESHOLD → 1 suara)
      → berhenti begitu satu mahasiswa punya ScanMinMatchFrames (2) suara
  → decideFrames  → mahasiswa dengan suara terbanyak, BestFrame (frame bukti)
  → kelas event: class_id perangkat, atau students.class_id bila kosong
  → scanStatus    → Hadir / Perlu ditinjau / Ditolak + result code
  → (bila S3_* diisi) unggah frame bukti ke Garage, maks 3 s → image_key
  → InsertOne scan_events (+ frames_total/with_face/matched) + UpdateOne devices.last_seen
  → log "scan <device>: <status> (<result>) frame=… wajah=… cocok=… skor=…"
  → 202 JSON {status, result, message, student, classId, faceVerified, confidence,
              reviewReason, framesTotal, framesWithFace, framesMatched}
```

Urutan penentuan status (`scanStatus`) penting dan berlapis:

| Kondisi | Status | `result` |
| --- | --- | --- |
| Tidak ada wajah di semua frame | `Ditolak` | `wajah_tidak_terdeteksi` |
| Ada wajah, tidak cocok dengan siapa pun | `Ditolak` | `tidak_terdaftar` (layar: "Wajah tidak Terdaftar") |
| Cocok, `class_id` diisi, tapi bukan anggota kelas itu | `Perlu ditinjau` | `perlu_ditinjau` |
| Cocok di ≥ `ScanMinMatchFrames` frame, atau `liveness_passed=true` | `Hadir` | `hadir` |
| Cocok di kurang dari `ScanMinMatchFrames` frame | `Perlu ditinjau` | `perlu_ditinjau` |

"Anggota kelas" dicek dengan `students.class_id == class_id` (bukan
`class_students`); dengan `class_id` kosong pemeriksaan ini tidak berlaku
karena event memakai kelas mahasiswa itu sendiri. Bila face service gagal,
respons `502` dan event **tidak** disimpan. Bila unggah foto gagal, event
**tetap** disimpan tanpa `image_key`. Konsensus frame **bukan** anti-spoof.

### 3.4 Heartbeat perangkat

```text
GET|POST /api/v1/devices/ping
  → (tanpa sesi, tanpa CSRF)
  → device_id dari form → query → header X-Device-Id → default "ESP32-GED-A1"
  → validDeviceToken hanya bila APP_ENV=production
  → UpdateOne devices {$set: last_seen, $setOnInsert: name} upsert=true
  → 200 JSON {status, device_id, last_seen, message}
```

`GET /api/v1/health?device_id=…` melakukan upsert yang sama **tanpa** token.
Lihat risiko di [devices.md](devices.md) §7.

## 4. Penjelasan per berkas

### `cmd/server/main.go`

- Memuat `.env` dengan `godotenv.Overload` (menimpa environment; lihat jebakan
  no. 8), lalu membaca env lewat `api.ParseConfig()` dan `env()`.
- Di luar production, `store.MinPasswordLength` diturunkan dari 12 ke 4
  karakter (log `development mode: …`).
- **Menolak start** bila `CSRF_SECRET` atau `DEVICE_TOKEN` < 32 karakter (semua
  mode); `FACE_SERVICE_TOKEN` < 32 karakter ditegakkan di mode production.
- `bootstrapStaff` membuat administrator pertama dari `BOOTSTRAP_ADMIN_*` bila
  koleksi `users` belum punya administrator aktif; dosen opsional dari
  `BOOTSTRAP_LECTURER_*` (dibuat lewat `EnsureUser`, jadi dilewati bila
  username sudah ada — kelasnya tidak diperbarui). Tidak ada akun demo bawaan.
- `resetBootstrapStaff` (bila `BOOTSTRAP_RESET_PASSWORDS=true`, ditolak di
  production) memanggil `store.ResetUser` untuk kedua akun bootstrap di setiap
  start: password, role, kelas, dan status aktif disetel ulang, atau akun
  dibuat bila belum ada.
- `store.Open(MONGODB_URI, MONGODB_DATABASE)` — gagal konek/ping dalam 8 detik
  menghentikan proses.
- `setupImageStorage` mengaktifkan penyimpanan foto bila `S3_*` diisi:
  konfigurasi tidak lengkap menghentikan proses, bucket yang belum terjangkau
  hanya memberi peringatan. Job retensi foto berjalan tiap jam.
- Dua cabang routing:
  - `protected` (di balik session + CSRF): halaman (`/`, `/mahasiswa`,
    `/kelas`, `/laporan`, `/logs`, `/akses`), login/logout, `/api/v1/` staf,
    `/partials/`, `/static/`.
  - `router` (tanpa session): `POST /api/v1/scans`, `GET /api/v1/health`,
    `GET|POST /api/v1/devices/ping`; sisanya diteruskan ke `protected`.
- Middleware: `Recover` → `SecurityHeaders` membungkus seluruh router; cabang
  `protected` dibungkus `sessions.LoadAndSave` → `csrf.Protect`.
- Timeout server: read header 5 s, read 15 s, write 15 s, idle 60 s.
- Graceful shutdown 8 detik pada SIGINT/SIGTERM.

### `internal/api/server.go`

Dua peran dalam satu berkas:

**a. Handler HTTP** — `StaffHandler()` (`/api/v1/dashboard|classes|reports|students…|staff…|images/…`),
`DeviceHandler()` (`/api/v1/health|scans|devices/ping`), `StaticHandler()`.
Header keamanan dipasang sekali oleh `security.SecurityHeaders` yang
membungkus seluruh router di `main.go`.

**b. Query & domain** — fungsi ber-scope role yang menerima `principal{role, classes}`:

| Fungsi | Guna |
| --- | --- |
| `classOverview` | ringkasan per kelas (jumlah mahasiswa, enrolled, hadir hari ini) |
| `dashboardAttendance` | roster + scan terakhir hari ini per mahasiswa |
| `scanStatusCounts` | hitung status scan hari ini |
| `recentEvents` | 50 event scan terbaru (tanpa batas tanggal) |
| `AllScanEvents` | 200 event terbaru dengan filter status/kelas/pencarian (halaman log) |
| `AllScanStatusCounts` | hitung status scan seluruh waktu |
| `countStudents`, `countTemplates`, `countScans` | agregat dashboard |
| `lastDeviceSeen` | dasar status online perangkat |
| `faceHealth` | cek `/health` layanan Python |
| `imageStorageHealth` | cek `HEAD` bucket S3 (di `images.go`) |

Pola berulang untuk scoping dosen: bila `role == "dosen"`, filter diberi
`bson.M{"class_id": bson.M{"$in": classes}}` (atau `_id` untuk koleksi
`classes`); bila daftar kelas kosong, langsung kembalikan hasil kosong (tanpa
menyentuh DB). Data "hari ini" difilter dengan rentang string dari
`todayRangeUTC()`. Filter dari input pengguna (mis. `class` di halaman log)
diperiksa terhadap daftar kelas dosen, bukan menimpa filter role.

**c. API publik untuk controller.** Fungsi ber-scope diekspor sebagai pembungkus
bertipe supaya `internal/controller/partials.go` bisa memakainya:

```go
s.ClassRows(ctx, role, classes)      // → classOverview
s.StudentCount(ctx, role, classes)   // → countStudents
s.TemplateCount(ctx, role, classes)  // → countTemplates
s.ScanStatusCounts(...) / s.Roster(...) / s.RecentEvents(...)
s.AllScanEvents(ctx, role, classes, status, class, q) / s.AllScanStatusCounts(...)
s.DeviceStatus(ctx) / s.FaceStatus(ctx)
s.CreateStudent(ctx, name, nim, classID)
s.EnrollFace(ctx, nim, []api.EnrollImage{…})   // 1–6 foto
s.DeleteStudent(ctx, nim)                 // admin.go
s.StaffAccounts(ctx) / s.CreateLecturer(ctx, LecturerInput{…}) / s.DeleteLecturer(ctx, id)
api.ReadEnrollImages(w, r) / api.EnrollErrorMessage(err, detail) / api.LecturerErrorMessage(err)
```

**d. Error sentinel** — supaya controller bisa memetakan pesan tanpa tahu detail query MongoDB:

```go
ErrMissingFields, ErrStudentExists, ErrInvalidClass, ErrStudentNotFound, ErrFaceService
ErrInvalidImage, ErrNoFace, ErrInconsistentFaces, ErrFaceAlreadyEnrolled, ErrTooManyPhotos   // face.go (per foto: *PhotoError)
ErrImageNotFound, ErrImageForbidden                                    // images.go
ErrInvalidUsername, ErrPasswordTooShort, ErrUsernameTaken, ErrNoClasses,
ErrUserNotFound, ErrNotLecturer                                        // admin.go
```

**e. Tipe bertipe** — `ClassRow`, `ScanEvent`, `RosterEntry`. Fungsi
`classRowsToMaps` / `scanEventsToMaps` / `rosterToMaps` menjaga **bentuk JSON
lama** tetap sama meski internalnya bertipe.

### `internal/controller/partials.go`

- `NewPartials` mem-`Glob` `views/fragments/*.html`, memasang FuncMap
  (`initials`, `statusClass`, `confidence`), lalu parse sekali saat startup.
- Setiap handler: ambil `Principal` dari context → panggil query `api` →
  rakit struct data → `p.render(w, "<nama>_fragment", data)`.
- `mahasiswaRoster` adalah helper bersama untuk `/partials/mahasiswa` dan
  `enrollFace` (keduanya mengembalikan tabel roster yang sama). Kolom
  enrollment menampilkan jumlah pose (`FaceTemplates`). Baris yang punya
  `FaceImageURLs` (dari `face_templates.image_keys`/`image_key`) menampilkan
  tombol **Lihat foto** (foto pertama) dan link nomor 2–6 yang membuka
  `/api/v1/images/enroll/...` di tab baru; izin tetap diperiksa
  `authorizeImage`. Tombol **Enroll wajah / Enroll ulang** adalah
  `<input type="file" name="images" multiple>` yang langsung dikirim saat
  file dipilih (fungsi template `inc` dipakai untuk nomor foto).
- `createStudent`, `mahasiswaForm`, dan `enrollFace` menolak non-administrator
  dengan 403.
- `admin_partials.go`: `deleteStudent` (roster setelah hapus), `staff`,
  `createLecturer`, `deleteStaff` (fragment `staff_fragment` di
  `views/fragments/akses.html`). Semua lewat `requireAdminPartial` (403 untuk
  non-administrator); gagal validasi tetap HTTP 200 dengan pesan di region.
- `logs` memanggil `AllScanStatusCounts` (ringkasan seluruh waktu, tidak ikut
  filter) dan `AllScanEvents` (tabel, ikut filter `q`/`status`/`class`).

### `internal/controller/web.go`

- `NewWeb` mem-parse tiap halaman (`dashboard`, `mahasiswa`, `kelas`,
  `laporan`, `logs`, `akses`) bersama layout + partials (sidebar, topbar,
  toast) sekali saat startup. **Konsekuensi: mengubah `.html` butuh restart**
  (sudah ditangani air).
- `render` menolak bila principal tidak ada, mengalihkan non-admin dari
  `/akses`, dan mengeksekusi template `layout`.

### `internal/security/middleware.go`

| Simbol | Perilaku |
| --- | --- |
| `NewSessionManager` | SCS; cookie `itbjh_session`, lifetime 8 jam, idle 30 menit, HttpOnly, SameSite Lax, `Secure` bila production |
| `RequireSession(…, apiRequest)` | Ambil `user_id` sesi → muat user aktif dari koleksi `users` (termasuk `class_ids`) → taruh `Principal`. Gagal: JSON 401 (`apiRequest=true`) atau redirect `/login?next=…` (`false`) |
| `RequireRole` | 403 JSON untuk `/api/`, redirect `/` untuk halaman |
| `LoginRateLimit(10, 5m)` | Per `RemoteAddr`, jendela 5 menit, `Retry-After` pada 429 |
| `SecurityHeaders` | nosniff, Referrer-Policy, X-Frame-Options DENY, Permissions-Policy, CSP, `Cache-Control: no-store` untuk non-`/static/` |
| `Recover` | Tangkap panic → 500 + log |

### `internal/store/`

- `database.go`: `Open` (connect + ping 8 detik) → `migrate` (membuat index,
  idempoten) → `seed` (3 kelas dan 2 perangkat; idempoten via upsert
  `$setOnInsert`; mahasiswa tidak di-seed). `Collection(name)` adalah
  helper akses koleksi.
- `users.go`: `CreateUser` (bcrypt, password ≥ `MinPasswordLength`: 12 di
  production, 4 di development, role terbatas `administrator`/`dosen`, `_id`
  hex acak), `EnsureUser` (cek username case-insensitive), `ResetUser`
  (setel ulang/buat akun bootstrap), `AuthenticateUser` (username
  case-insensitive lewat regex yang di-escape; perbandingan hash dummy untuk
  menyamakan waktu saat user tidak ada), `UserByID` (menolak akun nonaktif),
  `CountUsersByRole` (hanya akun aktif), serta `ListStaff`, `UsernameTaken`,
  `UserRole`, `DeleteUser` untuk halaman `/akses`.
- `seed` berjalan di **setiap** start dengan upsert `$setOnInsert`; mahasiswa
  hanya berasal dari pendaftaran manual (lihat jebakan no. 15).

### `internal/api/face.go`

- `postImages` mengirim multipart ke face service; respons non-2xx menjadi
  `faceServiceError{Status, Detail}` (field `detail` FastAPI).
  `classifyFaceError` memetakannya: 400 → `ErrInvalidImage`, 422 "No face" →
  `ErrNoFace` (nomor foto dari `image #N` dibungkus `*PhotoError`), lainnya →
  `ErrFaceService`.
- `embedFace` → `POST /embed` (field `image`); `registerFaces` →
  `POST /register?student_id=` (field `images`, file dinamai `foto-N.<ext>`).
- `enrollEmbeddings` (dipanggil `EnrollFace`): 1–`MaxEnrollPhotos` (6) foto,
  `checkEnrollEmbeddings` menolak foto yang bukan orang yang sama
  (`poseConsistency` = maks kemiripan dengan foto pertama atau rata-rata foto
  lain < `Config.PoseThreshold`, `ErrInconsistentFaces`) dan wajah yang mirip
  template mahasiswa lain
  (`ErrFaceAlreadyEnrolled`), lalu upsert `encodings` ke `face_templates`
  (dan `$unset` field lama `encoding`).
- `bestTemplateMatch` (dipakai enrollment dan `scan.go`): `faceTemplate.vectors()`
  membaca `encodings` atau `encoding` lama, dan skor mahasiswa = pose terbaik.
- Vektor dengan dimensi berbeda diabaikan. Ambang: `Config.SimThreshold`
  (`FACE_SIM_THRESHOLD`) untuk scan dan cek duplikat; `Config.PoseThreshold`
  (`FACE_POSE_THRESHOLD`, 0.30) untuk konsistensi pose.
- Batas waktu: `postImages` memberi `FaceServiceTimeout × jumlah foto`
  lewat context (`http.Client` tanpa timeout global). `ReadEnrollImages`
  memperpanjang deadline baca/tulis request menjadi `EnrollRequestTimeout`
  (3 menit) lewat `http.ResponseController`, karena server memakai
  `ReadTimeout`/`WriteTimeout` 15 detik. `storeEnrollmentImages` mengunggah
  foto ke Garage secara paralel.
- `EnrollFace` menulis log `enrollment <nim> berhasil/gagal (N foto, durasi)`.
- Pesan error untuk UI/JSON dirakit `EnrollErrorMessage` di `server.go`
  (menyebut "foto ke-N" lewat `PhotoNumber`).

### `internal/api/images.go` dan `internal/storage/s3.go`

- `ImageStore` adalah antarmuka (Put/Get/Delete/HeadBucket); implementasinya
  `storage.S3`. `Server.images == nil` berarti penyimpanan foto nonaktif dan
  semua fungsi foto menjadi no-op.
- `uploadImage` memakai batas 3 detik dan **tidak pernah** menggagalkan
  pemanggil: gagal unggah hanya dicatat di log dan mengembalikan kunci kosong.
- `storeEnrollmentImages` mengunggah semua foto enrollment, mengisi
  `face_templates.image_keys` (semua) dan `image_key` (foto pertama), lalu
  menghapus foto enrollment lama (`enrollmentKeys` menggabungkan kedua field).
- `authorizeImage` mencari kunci `enroll/…` di `image_key` **atau** `image_keys`.
- `serveImage` (`GET /api/v1/images/{key...}`): kunci harus tercatat di
  MongoDB; `authorizeImage` membatasi dosen ke kelasnya.
- `PurgeExpiredImages` / `RunImageRetention`: hapus foto scan yang lewat
  `IMAGE_RETENTION_DAYS` / `IMAGE_REVIEW_RETENTION_DAYS`, lalu `$unset`
  `image_key`. Event absensinya tidak dihapus.
- `storage.S3` ditulis dengan pustaka standar (AWS Signature V4, gaya path)
  supaya tidak menambah dependensi; diuji dengan vektor resmi AWS.

### `internal/api/admin.go`

- `DeleteStudent`: kumpulkan `image_key`/`image_keys` dari `face_templates` dan
  `scan_events`, hapus fotonya dari Garage (`deleteImage`, gagal hanya
  di-log), lalu hapus `scan_events`, `face_templates`, `class_students`, dan
  `students` milik mahasiswa itu.
- `CreateLecturer`: username dinormalisasi huruf kecil dan dicek
  `^[a-z0-9._-]{3,32}$`, password ≥ `store.MinPasswordLength`, minimal satu
  kelas yang ada di `classes`, username belum dipakai (case-insensitive).
  Validasi format berjalan sebelum menyentuh MongoDB (diuji di
  `admin_test.go`).
- `DeleteLecturer`: hanya akun ber-role `dosen`; administrator ditolak
  (`ErrNotLecturer`) agar admin tidak mengunci diri sendiri.
- Endpoint JSON `DELETE /api/v1/students/{nim}`, `GET|POST /api/v1/staff`,
  `DELETE /api/v1/staff/{id}` memakai fungsi yang sama dengan fragment.

### `services/face/` (Python)

FastAPI stateless milik tim ML (YOLO-FaceV2 + ArcFace). Go hanya memakai
`GET /health`, `POST /embed`, dan `POST /register` (tanpa `return_aligned`).
`main.py` adalah peluncur uvicorn (`--host`, `--port`, `--reload`,
`--workers`, `--list-models`, `--download-model`).
Butuh Python 3.10–3.12. Detail: [face-service.md](face-service.md) dan
`services/face/README.md`.

### `devices/esp32_scanner/` dan `devices/esp32cam_uart/`

- `esp32_scanner.ino`: loop non-blocking (ultrasonik tiap 100 ms, heartbeat
  tiap 15 s, buzzer, backlight); `doScan()` blocking: `camCapture()` (UART2,
  CRC32) → `postScan()` (SHA-256 + HMAC mbedTLS, multipart ditulis bertahap ke
  `WiFiClient`) → `jsonString()` mengurai respons → `showMessage()`.
  Sejak 28 Okt: `doScan()` = `camOn()` (`ON`) → `captureWindow()` (hingga
  `SCAN_MAX_FRAMES` `CAP` tersebar dalam `SCAN_WINDOW_MS`, disimpan di
  `Frames`) → `camOff()` → `postScan(frames)` (SHA-256 bertahap atas semua
  frame, satu part `image` per frame) → `showResult()` memetakan `result`
  ke layar (`tidak_terdaftar` → "Wajah tidak Terdaftar").
  Kredensial di `secrets.h` (di-gitignore). Semua fungsi didefinisikan sebelum
  dipakai. **Tipe buatan sendiri (`HttpResult`, `Frames`) wajib didefinisikan
  di atas fungsi pertama**: Arduino IDE menyisipkan prototipe otomatis semua
  fungsi tepat sebelum fungsi pertama, sehingga struct yang didefinisikan lebih
  bawah memicu error `'Frames' does not name a type`.
- `esp32cam_uart.ino`: kamera tanpa Wi-Fi; perintah `PING`/`ON`/`CAP`/`OFF`
  di UART1 (GPIO14/15), balasan `IMG <len> <crc>` + JPEG. Sensor power-down
  (PWDN) saat idle; `ON` = `esp_camera_init` + buang `WARMUP_FRAMES`, lalu
  driver terus menangkap ke 2 buffer PSRAM (`CAMERA_GRAB_LATEST`).
- `esp32cam_wifi.ino` (mode uji): fungsi kamera sama, tetapi lewat `WebServer`
  port 80 (`/ping`, `/on`, `/capture`, `/off`, header `X-Cam-Token`) dan mDNS
  `methics-cam`. Di `esp32_scanner.ino`, `CAM_LINK_WIFI` memilih implementasi
  `camPing`/`camOn`/`camOff`/`camCapture`: blok `#if CAM_LINK_WIFI` (HTTPClient,
  `camResolve()` = `CAM_HOST` atau `MDNS.queryHost`) atau blok UART. Nama dan
  tanda tangan fungsi kedua blok sama, jadi `doScan()` tidak berubah.
- Kontrak HMAC/multipart sama dengan `validScanSignature`/`readScanFrames`;
  bila salah satu berubah, ubah keduanya (lihat [devices.md](devices.md) §3, §6).

### `devices/esp8266_ping/esp8266_ping.ino`

Sketch uji ESP8266 yang mengirim `POST /api/v1/devices/ping` setiap 15 detik.
Konfigurasi dan peringatan kredensial: [devices.md](devices.md) §4.

## 5. Model data (MongoDB)

Ringkas — detail field, index, dan operasi ada di [database.md](database.md).

```text
classes        {_id: "IK24-A", name}
students       {_id: "student-241011001", nim (unik, 9 digit), name, email, class_id}
class_students {class_id, student_id}              (unik gabungan; belum dibaca query)
users          {_id, username (unik), display_name, role, password_hash,
                active, class_ids: [...], created_at}
devices        {_id: "ESP32-GED-A1", name, last_seen}
face_templates {student_id (unik), encodings [1–6 × 512], template_count, dimension,
                detection_confidences, image_key?, image_keys?, created_at,
                updated_at}                                  (ditulis Go; dokumen lama: encoding tunggal)
scan_events    {student_id, class_id (boleh ""), device_id, scanned_at, face_verified,
                liveness_passed, confidence, status, review_reason,
                frames_total, frames_with_face, frames_matched, image_key?}
```

Catatan penyimpanan waktu:

- `scan_events.scanned_at` disimpan sebagai **string RFC3339 UTC** (dari
  `captured_at` perangkat). Filter "hari ini" memakai
  `todayRangeUTC()`: tengah malam lokal server → +24 jam, diubah ke UTC dan
  dibandingkan sebagai string (`$gte`/`$lt`).
- `devices.last_seen` disimpan RFC3339 UTC; ditampilkan sebagai `HH:MM` lokal.

## 6. Konvensi & pola

- **Sentinel error**: lapisan `api` mengembalikan `Err…`; controller memetakan
  ke pesan pengguna. Tambah sentinel baru bila ada kelas kegagalan baru.
- **Fragment adalah `{{define}}` saja**, tanpa HTML di luar blok.
- **Sukses htmx** → `HX-Trigger` (JSON) membawa event; **gagal** → 200 + pesan
  inline di form.
- **Scoping role** selalu dari `principal`, bukan dari input pengguna.
- **Ekspor minimal**: hanya yang dipakai controller yang diekspor dari `api`.

## 7. Resep tugas umum

**Menambah halaman baru**

1. `views/pages/<nama>.html` (shell) + `views/fragments/<nama>.html` (fragment, `{{define}}`).
2. Tambahkan `"<nama>"` ke daftar `pages` di `web.go`, dan judulnya di `model/page.go`.
3. Tambahkan handler `GET /<nama>` di `main.go` (biasanya dibungkus `RequireSession`).
4. Tambahkan endpoint fragment `GET /partials/<nama>` di `partials.go` + handler di mux `Handler()`.
5. Tambahkan item sidebar di `views/partials/sidebar.html`.

**Menambah endpoint JSON staf**

1. Tambahkan query/logika sebagai fungsi ber-scope di `api/server.go`
   (terima `principal`).
2. Daftarkan route di `StaffHandler()`.
3. Bila dipakai UI juga, ekspor pembungkusnya dan pakai dari `partials.go`.

**Menambah field dokumen**

1. Tulis field baru pada `InsertOne`/`UpdateOne` terkait di `api/server.go`
   (atau `store/users.go`). Dokumen lama tidak otomatis punya field itu —
   pastikan pembaca menoleransi nilai nol, atau backfill dengan skrip `mongosh`.
2. Tambahkan field pada struct decode (`bson:"…"`) dan, bila perlu, index di
   `migrate()` pada `internal/store/database.go`.
3. Bila tampil di UI, tambahkan field struct data fragment + template.
4. Perbarui [database.md](database.md).

**Menambah aksi administrator baru**

Ikuti pola `admin.go` + `admin_partials.go`: logika & validasi di fungsi
`api.Server` (dipakai juga endpoint JSON), handler fragment memanggil
`requireAdminPartial`, lalu merender ulang region yang sama dengan pesan
`Notice`/`Error`. Untuk aksi yang menghapus data, pakai `hx-delete` +
`hx-confirm` dan sertakan filter halaman lewat `hx-include`.

**Menambah halaman log/filter baru**

Ikuti pola `/logs`: kontainer form dengan `hx-get` + `hx-trigger="load, every
15s, keyup changed delay:300ms, change"`, fragment dengan ringkasan
`hx-swap-oob`, dan query ber-scope role yang menerima nilai filter dari
`r.URL.Query()`. Escape input pencarian dengan `regexp.QuoteMeta` sebelum
dipakai di `$regex`.

## 8. Jebakan yang mudah menimbulkan bug

1. **Token CSRF di HTML ter-escape.** `html/template` menulis `+` sebagai
   `&#43;`. Kalau membaca token dari HTML (mis. lewat skrip), HTML-decode dulu,
   atau ambil dari `meta[name="csrf-token"]` seperti `app.js`.
2. **Template di-parse sekali saat startup.** Perubahan `.html` tidak muncul
   tanpa restart; `air` sudah menangani ini.
3. **`/partials/*` wajib di belakang session.** Handler-nya bergantung pada
   `Principal` di context; memasangnya tanpa `RequireSession` akan 401.
4. **Jangan tambahkan inline script.** CSP hanya mengizinkan `script-src 'self'`;
   `htmx.config.allowEval = false` juga menutup jalur eval.
5. **Waktu lokal vs UTC.** Pakai `todayRangeUTC()` untuk "hari ini" dan simpan
   waktu selalu sebagai RFC3339 UTC (`time.Now().UTC().Format(time.RFC3339)`).
   Format lain (mis. offset `+08:00`) merusak perbandingan string. Batas hari
   mengikuti zona waktu **server**.
6. **`.env` tidak boleh ber-BOM.** PowerShell 5.1 `Set-Content -Encoding utf8`
   menulis BOM dan membuat parser `godotenv` air gagal memuat seluruh file.
   Pakai `UTF8Encoding($false)` atau `-Encoding ascii`.
7. **Satu `DEVICE_TOKEN` untuk semua perangkat.** Belum ada secret per
   perangkat; lihat risiko di [HANDOFF.md](../HANDOFF.md) §Batasan.
8. **`.env` dimuat saat startup Go dan menimpa environment.** `main.go` memakai
   `godotenv.Overload()`, karena terminal VS Code (`python.terminal.useEnvFile`)
   dan `air` (`env_files`) menyuntikkan isi `.env` saat dibuka; dengan
   `Load()` biasa, nilai lama itu menang dan perubahan `.env` diabaikan. Untuk
   memakai nilai dari environment, hapus baris tersebut dari `.env`.
9. **Ping tanpa `device_id` menandai `ESP32-GED-A1`.** Perangkat yang lupa
   mengirim ID akan membuat perangkat A1 tampak online.
10. **Opsi filter kelas di view masih hardcoded** (`views/pages/mahasiswa.html`,
    `views/pages/logs.html`). Menambah kelas di database tidak otomatis
    menambah opsi filter.
11. **Jangan menimpa filter scoping role.** Saat menambah filter dari input
    pengguna, gabungkan dengan filter role (mis. cek nilai ada di
    `principal.ClassIDs`), jangan menulis ulang key yang sama (contoh benar:
    filter `class` di `AllScanEvents`).
12. **Keanggotaan kelas = `students.class_id`.** `class_students` ditulis
    tetapi tidak dibaca; jangan mengandalkannya tanpa memperbarui query.
13. **Foto bukan bagian wajib alur absensi.** Jangan membuat scan/enrollment
    gagal karena S3; ikuti pola `uploadImage` (log + lanjut).
14. **Ganti model recognition = enrollment ulang.** Embedding lama tidak
    sebanding dengan model baru; foto enrollment di Garage bisa dipakai untuk
    membuat ulang template (belum diotomasi).
15. **NIM wajib 9 digit angka** (`api.ValidNIM`, mis. `241011001`). Validasi
    ada di `CreateStudent`, jadi endpoint JSON dan form htmx memakai aturan
    yang sama; pola `pattern` di `mahasiswa_form.html` hanya bantuan browser.
    Mahasiswa contoh `IK24…` dari seed lama tidak lolos aturan ini dan tidak
    dibuat ulang lagi.
16. **`hx-include` pada `DELETE` dikirim sebagai query string**, jadi handler
    membaca filter dengan `r.FormValue` (bukan `r.PostFormValue`).
