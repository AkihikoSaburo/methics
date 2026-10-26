# Panduan Kode Algorithmics Face Recognation

Dokumen ini menjelaskan **bagaimana kode disusun dan mengapa**, agar perubahan
berikutnya konsisten dengan pola yang ada. Untuk gambaran besar, lihat
[architecture.md](architecture.md). Untuk kontrak API, lihat [API.md](../API.md).
Untuk skema data, lihat [database.md](database.md). Terakhir diselaraskan dengan
kode: 26 Oktober 2026.

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
internal/api/face_test.go     unit test klien & pencocokan (tanpa MongoDB)
internal/api/images.go        foto wajah di S3: unggah, otorisasi & stream, retensi
internal/api/images_test.go   unit test kunci objek, unggah gagal, storage nonaktif
internal/storage/s3.go        klien S3 minimal (SigV4, gaya path) untuk Garage
internal/storage/s3_test.go   vektor uji AWS SigV4 + round-trip ke S3 tiruan
internal/controller/
  web.go                      render halaman penuh (shell) + login page
  partials.go                 render fragment htmx (/partials/*)
  auth.go                     login, logout, safeNext
internal/model/page.go        view model untuk halaman (judul, role, tanggal, csrf)
internal/security/middleware.go
                              session SCS, Principal, RequireSession, RequireRole,
                              LoginRateLimit, SecurityHeaders (CSP), Recover
internal/store/
  database.go                 Open (connect+ping MongoDB), migrate (index), seed (kelas/mahasiswa/perangkat)
  users.go                    User, CreateUser, EnsureUser, AuthenticateUser, UserByID
services/face/                face service Python (FastAPI: /health, /detect → embedding ArcFace 512-d)
devices/esp8266/esp8266_ping/ firmware heartbeat ESP8266 (Arduino)
views/
  layouts/base.html           kerangka halaman (head, sidebar, topbar, content)
  pages/*.html                shell per halaman (isi div tempat htmx menaruh fragment)
  partials/*.html             sidebar, topbar, toast
  fragments/*.html            fragment htmx ({{define}} saja)
  login.html                  dokumen login mandiri
static/css/*                  tokens, base, layout, components, pages, responsive, print
static/js/app.js              setup htmx + perilaku global
static/js/lib/ui.js           refreshIcons, toast
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
POST /api/v1/scans
  → (tanpa sesi, tanpa CSRF)
  → validDeviceToken        (Authorization: Bearer <DEVICE_TOKEN>, perbandingan konstan-waktu)
  → readImagePart           (multipart field "image", maks 8 MB)
  → cek device_id & class_id tidak kosong, captured_at RFC3339 ±5 menit, liveness_passed true/false
  → validScanSignature      (HMAC-SHA256 atas X-Device-Timestamp ±2 menit + fields + hash gambar)
  → cek devices._id ada, classes._id ada
  → verifyCapture: POST /detect ke Python (file image; timeout 15 s)
      → ambil wajah dengan confidence tertinggi
      → cosine similarity terhadap semua face_templates (≥ FACE_SIM_THRESHOLD → kandidat)
  → tentukan status: Hadir / Perlu ditinjau / Ditolak
  → (bila S3_* diisi) unggah capture ke Garage, maks 3 s → image_key
  → InsertOne scan_events + UpdateOne devices.last_seen
  → 202 JSON {status, student, faceVerified, confidence, reviewReason}
```

Urutan penentuan status (di `receiveScan`) penting dan berlapis:

| Kondisi | Status | Alasan dicatat |
| --- | --- | --- |
| Wajah tidak cocok | `Ditolak` | alasan dari Go (mis. kemiripan di bawah ambang) |
| Cocok tapi bukan anggota kelas di perangkat | `Perlu ditinjau` | "…bukan anggota kelas pada perangkat." |
| Cocok, anggota kelas, tapi `liveness_passed != true` | `Perlu ditinjau` | "…bukti liveness belum tersedia atau tidak lolos." |
| Cocok + anggota kelas + liveness true | `Hadir` | kosong |

"Anggota kelas" dicek dengan `students.class_id == class_id` (bukan
`class_students`). Bila face service gagal, respons `502` dan event **tidak**
disimpan. Bila unggah foto gagal, event **tetap** disimpan tanpa `image_key`.

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
Lihat risiko di [devices.md](devices.md) §5.

## 4. Penjelasan per berkas

### `cmd/server/main.go`

- Membaca env lewat `api.ParseConfig()` dan `env()`.
- **Menolak start** bila `CSRF_SECRET` atau `DEVICE_TOKEN` < 32 karakter (semua
  mode); `FACE_SERVICE_TOKEN` < 32 karakter ditegakkan di mode production.
- `bootstrapStaff` membuat administrator pertama dari `BOOTSTRAP_ADMIN_*` bila
  koleksi `users` belum punya administrator aktif; dosen opsional dari
  `BOOTSTRAP_LECTURER_*` (dibuat lewat `EnsureUser`, jadi dilewati bila
  username sudah ada — kelasnya tidak diperbarui). Tidak ada akun demo bawaan.
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

**a. Handler HTTP** — `StaffHandler()` (`/api/v1/dashboard|classes|reports|students…|images/…`),
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
s.EnrollFace(ctx, nim, image, filename)
api.ReadImagePart(w, r)
```

**d. Error sentinel** — supaya controller bisa memetakan pesan tanpa tahu detail query MongoDB:

```go
ErrMissingFields, ErrStudentExists, ErrInvalidClass, ErrStudentNotFound, ErrFaceService
ErrInvalidImage, ErrNoFace, ErrMultipleFaces, ErrFaceAlreadyEnrolled   // face.go
ErrImageNotFound, ErrImageForbidden                                    // images.go
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
  `enrollFace` (keduanya mengembalikan tabel roster yang sama).
- `createStudent`, `mahasiswaForm`, dan `enrollFace` menolak non-administrator
  dengan 403.
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
  idempoten) → `seed` (3 kelas, 5 mahasiswa, relasi `class_students`,
  2 perangkat; idempoten via upsert `$setOnInsert`). `Collection(name)` adalah
  helper akses koleksi.
- `users.go`: `CreateUser` (bcrypt, password ≥ 12 karakter, role terbatas
  `administrator`/`dosen`, `_id` hex acak), `EnsureUser` (cek username
  case-insensitive), `AuthenticateUser` (username case-insensitive lewat regex
  yang di-escape; perbandingan hash dummy untuk menyamakan waktu saat user
  tidak ada), `UserByID` (menolak akun nonaktif), `CountUsersByRole` (hanya
  akun aktif).

### `internal/api/face.go`

- `detectFaces` memanggil `POST /detect` pada face service dan mengembalikan
  `faceServiceError{Status}` untuk respons non-2xx (400 → `ErrInvalidImage`).
- `enrollEmbedding` (dipanggil `EnrollFace`): wajib tepat satu wajah
  (`ErrNoFace`/`ErrMultipleFaces`), tolak wajah yang mirip template mahasiswa
  lain (`ErrFaceAlreadyEnrolled`), lalu upsert ke `face_templates`.
- `verifyCapture` (dipanggil `receiveScan`): pilih wajah dengan confidence
  tertinggi, hitung `cosineSimilarity` ke semua template lewat
  `bestTemplateMatch`, kembalikan `FaceMatch{Verified, StudentID, Confidence,
  Reason}`.
- Template dengan dimensi berbeda diabaikan. Ambang: `Config.SimThreshold`
  (`FACE_SIM_THRESHOLD`).

### `internal/api/images.go` dan `internal/storage/s3.go`

- `ImageStore` adalah antarmuka (Put/Get/Delete/HeadBucket); implementasinya
  `storage.S3`. `Server.images == nil` berarti penyimpanan foto nonaktif dan
  semua fungsi foto menjadi no-op.
- `uploadImage` memakai batas 3 detik dan **tidak pernah** menggagalkan
  pemanggil: gagal unggah hanya dicatat di log dan mengembalikan kunci kosong.
- `storeEnrollmentImage` mengganti `face_templates.image_key` dan menghapus
  foto enrollment lama.
- `serveImage` (`GET /api/v1/images/{key...}`): kunci harus tercatat di
  MongoDB; `authorizeImage` membatasi dosen ke kelasnya.
- `PurgeExpiredImages` / `RunImageRetention`: hapus foto scan yang lewat
  `IMAGE_RETENTION_DAYS` / `IMAGE_REVIEW_RETENTION_DAYS`, lalu `$unset`
  `image_key`. Event absensinya tidak dihapus.
- `storage.S3` ditulis dengan pustaka standar (AWS Signature V4, gaya path)
  supaya tidak menambah dependensi; diuji dengan vektor resmi AWS.

### `services/face/` (Python)

FastAPI stateless milik tim ML (YOLO-FaceV2 + ArcFace). Go hanya memakai
`GET /health` dan `POST /detect`. Detail: [face-service.md](face-service.md)
dan `services/face/README.md`.

### `devices/esp8266/esp8266_ping/esp8266_ping.ino`

Sketch Arduino ESP8266 yang tersambung ke Wi-Fi lalu mengirim
`POST /api/v1/devices/ping` setiap 15 detik. Konfigurasi dan peringatan
kredensial: [devices.md](devices.md).

## 5. Model data (MongoDB)

Ringkas — detail field, index, dan operasi ada di [database.md](database.md).

```text
classes        {_id: "IK24-A", name}
students       {_id: "student-ik24001", nim (unik), name, email, class_id}
class_students {class_id, student_id}              (unik gabungan; belum dibaca query)
users          {_id, username (unik), display_name, role, password_hash,
                active, class_ids: [...], created_at}
devices        {_id: "ESP32-GED-A1", name, last_seen}
face_templates {student_id (unik), encoding, dimension, detection_confidence,
                image_key?, created_at, updated_at}          (ditulis Go)
scan_events    {student_id, class_id, device_id, scanned_at, face_verified,
                liveness_passed, confidence, status, review_reason, image_key?}
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
8. **`.env` dimuat saat startup Go.** Nilai environment dari shell tidak
   ditimpa oleh `.env`; `air` juga memuat file tersebut melalui
   `env_files` di `.air.toml`.
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
