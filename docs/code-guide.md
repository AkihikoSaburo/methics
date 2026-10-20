# Panduan Kode Algorithmics Face Recognation

Dokumen ini menjelaskan **bagaimana kode disusun dan mengapa**, agar perubahan
berikutnya konsisten dengan pola yang ada. Untuk gambaran besar, lihat
[architecture.md](architecture.md). Untuk kontrak API, lihat [API.md](../API.md).

## 1. Prinsip yang dipegang

1. **Server-rendered, bukan SPA.** Halaman dirender Go dengan `html/template`;
   htmx hanya menukar potongan HTML. Tidak ada logika role di klien.
2. **Satu sumber query & validasi.** Sembilan endpoint JSON dan tujuh endpoint
   fragment memakai fungsi yang sama di `internal/api/server.go`. Tidak ada SQL
   atau aturan validasi yang ditulis dua kali.
3. **Otorisasi di server, dari database.** Role dan daftar kelas dosen diambil
   ulang dari DB setiap request (`security.RequireSession`). Query/header role
   dari klien tidak pernah dipercaya.
4. **Tidak ada data biometrik ke browser.** Hanya label status yang dikirim.

## 2. Peta direktori

```text
cmd/server/main.go            entry point: env, routing, middleware, bootstrap, shutdown
internal/api/server.go        endpoint JSON + query ber-scope role + domain method + error sentinel
internal/controller/
  web.go                      render halaman penuh (shell) + login page
  partials.go                 render fragment htmx (/partials/*)
  auth.go                     login, logout, safeNext
internal/model/page.go        view model untuk halaman (judul, role, tanggal, csrf)
internal/security/middleware.go
                              session SCS, Principal, RequireSession, RequireRole,
                              LoginRateLimit, SecurityHeaders (CSP), Recover
internal/store/
  database.go                 Open, migrate (schema), seed (kelas/mahasiswa/perangkat)
  users.go                    User, CreateUser, EnsureUser, AuthenticateUser, UserByID
services/face/main.py         FastAPI: /health, /enroll, /verify (embedding 128-dimensi)
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

## 3. Tiga alur request

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
  → validScanSignature      (HMAC-SHA256 atas X-Device-Timestamp + fields + hash gambar)
  → cek device terdaftar, kelas ada
  → callFaceService POST /verify  (Python; bearer FACE_SERVICE_TOKEN)
  → tentukan status: Hadir / Perlu ditinjau / Ditolak
  → INSERT scan_events + UPDATE devices.last_seen
```

Urutan penentuan status (di `receiveScan`) penting dan berlapis:

| Kondisi | Status | Alasan dicatat |
| --- | --- | --- |
| Wajah tidak cocok | `Ditolak` | alasan dari Python |
| Cocok tapi bukan anggota kelas di perangkat | `Perlu ditinjau` | "…bukan anggota kelas pada perangkat." |
| Cocok, anggota kelas, tapi `liveness_passed != true` | `Perlu ditinjau` | "…bukti liveness belum tersedia atau tidak lolos." |
| Cocok + anggota kelas + liveness true | `Hadir` | kosong |

## 4. Penjelasan per berkas

### `cmd/server/main.go`

- Membaca env lewat `api.ParseConfig()` dan `env()`.
- **Menolak start** bila `CSRF_SECRET` atau `DEVICE_TOKEN` < 32 karakter (semua
  mode); `FACE_SERVICE_TOKEN` < 32 karakter ditegakkan di mode production.
- `bootstrapStaff` membuat administrator pertama dari `BOOTSTRAP_ADMIN_*` bila
  tabel `users` belum punya administrator; dosen opsional dari
  `BOOTSTRAP_LECTURER_*`. Tidak ada akun demo bawaan.
- Dua cabang routing:
  - `protected` (di balik session + CSRF): halaman, `/api/v1/` staf, `/partials/`, `/static/`.
  - `router` (tanpa session): `POST /api/v1/scans`, `GET /api/v1/health`.
- Middleware global: `sessions.LoadAndSave` → `csrf.Protect` → (`Recover` +
  `SecurityHeaders`) pada level server HTTP.
- Graceful shutdown 8 detik pada SIGINT/SIGTERM.

### `internal/api/server.go`

Dua peran dalam satu berkas:

**a. Handler HTTP** — `StaffHandler()` (`/api/v1/dashboard|classes|reports|students…`),
`DeviceHandler()` (`/api/v1/health|scans`), `StaticHandler()`. Semua membungkus
mux dengan `securityHeaders`.

**b. Query & domain** — fungsi ber-scope role yang menerima `principal{role, classes}`:

| Fungsi | Guna |
| --- | --- |
| `classOverview` | ringkasan per kelas (jumlah mahasiswa, enrolled, hadir hari ini) |
| `dashboardAttendance` | roster + scan terakhir hari ini per mahasiswa |
| `scanStatusCounts` | hitung status scan hari ini |
| `recentEvents` | 50 event scan terbaru |
| `countStudents`, `countTemplates`, `countScans` | agregat dashboard |
| `lastDeviceSeen` | dasar status online perangkat |
| `faceHealth` | cek `/health` layanan Python |

Pola berulang untuk scoping dosen: bila `role == "dosen"`, query diberi
`WHERE … IN (<placeholders>)`; bila daftar kelas kosong, langsung kembalikan
hasil kosong (tanpa menyentuh DB).

**c. API publik untuk controller.** Fungsi ber-scope diekspor sebagai pembungkus
bertipe supaya `internal/controller/partials.go` bisa memakainya:

```go
s.ClassRows(ctx, role, classes)      // → classOverview
s.StudentCount(ctx, role, classes)   // → countStudents
s.TemplateCount(ctx, role, classes)  // → countTemplates
s.ScanStatusCounts(...) / s.Roster(...) / s.RecentEvents(...)
s.DeviceStatus(ctx) / s.FaceStatus(ctx)
s.CreateStudent(ctx, name, nim, classID)
s.EnrollFace(ctx, nim, image, filename)
api.ReadImagePart(w, r)
```

**d. Error sentinel** — supaya controller bisa memetakan pesan tanpa tahu SQL:

```go
ErrMissingFields, ErrStudentExists, ErrInvalidClass, ErrStudentNotFound, ErrFaceService
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
- `createStudent` dan `enrollFace` menolak non-administrator dengan 403.

### `internal/controller/web.go`

- `NewWeb` mem-parse tiap halaman bersama layout + partials (sidebar, topbar,
  toast) sekali saat startup. **Konsekuensi: mengubah `.html` butuh restart**
  (sudah ditangani air).
- `render` menolak bila principal tidak ada, mengalihkan non-admin dari
  `/akses`, dan mengeksekusi template `layout`.

### `internal/security/middleware.go`

| Simbol | Perilaku |
| --- | --- |
| `NewSessionManager` | SCS; cookie `itbjh_session`, lifetime 8 jam, idle 30 menit, HttpOnly, SameSite Lax, `Secure` bila production |
| `RequireSession(…, apiRequest)` | Ambil `user_id` sesi → muat user dari DB → taruh `Principal`. Gagal: JSON 401 (`apiRequest=true`) atau redirect `/login?next=…` (`false`) |
| `RequireRole` | 403 JSON untuk `/api/`, redirect `/` untuk halaman |
| `LoginRateLimit(10, 5m)` | Per `RemoteAddr`, jendela 5 menit, `Retry-After` pada 429 |
| `SecurityHeaders` | nosniff, Referrer-Policy, X-Frame-Options DENY, Permissions-Policy, CSP, `Cache-Control: no-store` untuk non-`/static/` |
| `Recover` | Tangkap panic → 500 + log |

### `internal/store/`

- `database.go`: `Open` → `migrate` (semua `CREATE TABLE IF NOT EXISTS`) →
  `seed` (3 kelas, 5 mahasiswa, 2 perangkat; idempoten via `INSERT OR IGNORE`).
- `users.go`: `CreateUser` (bcrypt, password ≥ 12 karakter, role terbatas
  `administrator`/`dosen`), `EnsureUser`, `AuthenticateUser` (termasuk
  perbandingan hash dummy untuk menyamakan waktu saat user tidak ada),
  `UserByID`, `UserClassIDs`, `CountUsersByRole`.

### `services/face/main.py`

- `/health` mengembalikan `modelReady` = database siap (bukan cek model AI).
- `/enroll` mewajibkan tepat satu wajah terdeteksi, menyimpan embedding 128
  dimensi (`face_templates`), **bukan gambar mentah**.
- `/verify` menghitung jarak ke semua template, ambil terkecil, cocok bila
  `distance <= FACE_DISTANCE_THRESHOLD` (default `0.50`); `confidence = 1 - distance`.
- `livenessSupported: false` — liveness belum ada di sisi Python.

## 5. Model data (SQLite)

```text
classes(id PK, name UNIQUE)
students(id PK, nim UNIQUE, name, email)
users(id PK, username UNIQUE NOCASE, display_name, role CHECK(administrator|dosen),
      password_hash BLOB, active, created_at)
user_classes(user_id FK, class_id FK, PK(user_id,class_id))
class_students(class_id FK, student_id FK, PK(class_id,student_id))
devices(id PK, name, last_seen)
face_templates(student_id PK FK, encoding TEXT, created_at)
scan_events(id PK AUTOINCREMENT, student_id FK NULL, class_id FK, device_id FK,
            scanned_at, face_verified, liveness_passed, confidence, status, review_reason)
```

Catatan penyimpanan waktu:

- `scan_events.scanned_at` disimpan sebagai **RFC3339** (dari `captured_at`
  perangkat, di-UTC-kan); filter "hari ini" memakai
  `date(scanned_at, 'localtime') = date('now', 'localtime')`.
- `devices.last_seen` disimpan RFC3339 UTC.

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

**Menambah kolom tabel**

1. Tambahkan `ALTER TABLE`/kolom pada `migrate` di `internal/store/database.go`
   (perhatikan DB yang sudah ada; `CREATE TABLE IF NOT EXISTS` tidak mengubah
   tabel lama).
2. Sesuaikan `INSERT`/`SELECT` terkait di `api/server.go`.
3. Bila tampil di UI, tambahkan field struct data fragment + template.

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
5. **Waktu lokal vs UTC.** Selalu pakai pola `date(…, 'localtime')` untuk
   perbandingan "hari ini", karena `scanned_at` disimpan dalam UTC/RFC3339.
6. **`.env` tidak boleh ber-BOM.** PowerShell 5.1 `Set-Content -Encoding utf8`
   menulis BOM dan membuat parser `godotenv` air gagal memuat seluruh file.
   Pakai `UTF8Encoding($false)` atau `-Encoding ascii`.
7. **Satu `DEVICE_TOKEN` untuk semua perangkat.** Belum ada secret per
   perangkat; lihat risiko di [HANDOFF.md](../HANDOFF.md) §Batasan.
8. **`.env` dimuat saat startup Go.** Nilai environment dari shell tidak
   ditimpa oleh `.env`; `air` juga memuat file tersebut melalui
   `env_files` di `.air.toml`.
