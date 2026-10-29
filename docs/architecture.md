# Arsitektur Algorithmics Face Recognation

> Status: **arsitektur yang berlaku saat ini** (Go SSR + htmx, satu binary,
> MongoDB, foto opsional di S3 Garage). Terakhir diselaraskan dengan kode:
> 26 Oktober 2026.
> Dokumen ini menggantikan draf rencana pemisahan frontend/Nuxt yang ditolak —
> lihat §9 untuk catatannya.

## 1. Ringkasan

Satu aplikasi Go yang merender HTML dan sekaligus melayani API. Tidak ada
runtime kedua, tidak ada build frontend terpisah, tidak ada langkah deploy
kedua.

- **Bahasa/runtime**: Go (satu binary) + MongoDB.
- **Frontend**: `html/template` server-rendered + htmx untuk navigasi/aksi
  tanpa reload penuh.
- **Layanan pendukung**: face service Python (internal, tidak diekspos) dan
  object storage S3 **Garage** untuk foto wajah (opsional, privat).
- **Klien**: browser staf (admin/dosen) dan perangkat absensi ESP32 + ESP32-CAM
  (scan wajah + heartbeat, `devices/esp32_scanner` & `devices/esp32cam_uart`);
  ESP8266 hanya untuk uji heartbeat.

Konsekuensi yang disengaja: halaman dan API berbagi satu sumber query/validasi
(`internal/api`), sehingga aturan role tidak bisa berbeda antara tampilan dan
API.

## 2. Komponen

| Lapisan | Lokasi | Peran |
| --- | --- | --- |
| Entrypoint & routing | `cmd/server/main.go` | wiring route, middleware, bootstrap, env |
| HTTP API & domain | `internal/api/server.go`, `admin.go`, `face.go` | endpoint staf + perangkat, query ber-scope role, error sentinel; fitur administrator (hapus mahasiswa, akun dosen); klien face service |
| Halaman & fragment | `internal/controller/` | render `views/pages` (shell) dan `views/fragments` (htmx) |
| Keamanan | `internal/security/middleware.go` | `RequireSession`, `RequireRole`, `Principal`, security headers/CSP |
| Penyimpanan | `internal/store/database.go`, `users.go` | koneksi, index, seed MongoDB; akun staf (bcrypt) |
| Foto wajah | `internal/api/images.go`, `internal/storage/s3.go` | unggah/stream/retensi foto di Garage (S3 SigV4, pustaka standar) |
| Tampilan | `views/` | `layouts/base.html`, `pages/*`, `fragments/*` |
| Aset statis | `static/` | `css/`, `js/app.js`, `js/lib/ui.js`, vendor lokal |
| Face service | `services/face/` | Python (stateless): `/health`, `/embed` (1 foto) dan `/register` (1–6 foto) → embedding ArcFace 512-d; dipanggil oleh `internal/api/face.go` |
| Firmware | `devices/` | `esp32_scanner` (ESP32 utama → `/api/v1/scans`, `/devices/ping`), `esp32cam_uart` (kamera via UART), `esp32cam_wifi` (kamera mode uji Wi-Fi), sketch uji |

Vendor frontend di-host lokal dan versi-terpin: htmx 2.0.11, Lucide 1.59.0,
dengan `htmx.config.allowEval = false`.

## 3. Alur data

### Halaman staf (browser)

```text
Browser ──GET /,/mahasiswa,/kelas,/laporan,/akses──► Go (shell htmx)
        ──GET /partials/...──────────────────────► Go (fragment HTML)
        ──POST /partials/... (+ X-CSRF-Token)────► Go → internal/api → MongoDB
                                                    └── HX-Trigger: toast / roster-changed / ...
```

Navigasi sidebar memakai htmx `hx-boost` untuk mengambil halaman server-rendered
berikutnya lalu mengganti `#main-content` (`hx-target` + `hx-select`). Sidebar
dan ikon navigasi tetap di DOM, sehingga tidak ikut dimuat ulang. Aksi dan
pembaruan data memakai fragment HTML di `/partials/*` (bukan JSON + perakitan
DOM); ikon Lucide disegarkan setelah swap fragment.

### Perangkat (ESP32 + ESP32-CAM) — scan

```text
HC-SR04 → ESP32 ──UART "ON", "CAP"×4 (±2 s), "OFF"──► ESP32-CAM (stream) ──JPEG + CRC32──► ESP32
ESP32 ──POST /api/v1/scans (1–6 frame, bearer DEVICE_TOKEN + HMAC + timestamp)──► Go (scan.go)
                                                                       ├── Python /embed per frame → embedding
                                                                       ├── Go: cosine vs semua pose; ≥ 2 frame cocok → Hadir
                                                                       ├── kelas: CLASS_ID perangkat, atau kelas mahasiswa (kosong)
                                                                       ├── Garage: scans/... (opsional, maks 3 s)
                                                                       ├── MongoDB: scan_events (+ image_key)
                                                                       └── MongoDB: devices.last_seen
```

### Perangkat (ESP8266) — heartbeat

```text
ESP8266 ──POST /api/v1/devices/ping (bearer DEVICE_TOKEN, wajib hanya di production)──► Go
                                                                                       └── MongoDB: devices.last_seen (upsert)
```

Dashboard menganggap perangkat online bila `last_seen` < 2 menit.

### Enrollment wajah (admin)

```text
Admin ──POST /partials/mahasiswa/{nim}/face-template (multipart, 1–6 foto pose)──► Go
                                                                   ├── Python /register → embedding per foto
                                                                   ├── Go: cek tiap foto ada wajah, satu orang, belum terdaftar di NIM lain
                                                                   ├── MongoDB: face_templates.encodings (1–6 vektor)
                                                                   └── Garage: enroll/... per foto (opsional; foto lama dihapus)
```

### Administrasi (administrator)

```text
Admin ──DELETE /partials/mahasiswa/{nim} (hx-confirm)──► Go ──► Garage: hapus foto enroll/ & scans/ mahasiswa itu
                                                         └──► MongoDB: hapus scan_events, face_templates, class_students, students
Admin ──POST /partials/akses/lecturers──► Go: validasi username/password/kelas ──► MongoDB: users (role dosen, bcrypt)
Admin ──DELETE /partials/akses/staff/{id}──► Go: hanya role dosen ──► MongoDB: hapus users (sesi dosen langsung tidak berlaku)
```

### Foto wajah (staf)

```text
Staf ──GET /api/v1/images/{key} (session)──► Go: cek key di MongoDB + kelas dosen ──► Garage (GET) ──► stream ke browser
Go (tiap jam) ──► hapus foto scan lewat masa retensi di Garage + $unset image_key
```

Browser tidak pernah mengakses Garage langsung; bucket tetap privat.

### API staf (integrasi)

`/api/v1/*` tetap tersedia sebagai API JSON untuk integrasi, pengujian, dan
perangkat. Sejak migrasi htmx, halaman **tidak** lagi membaca
`/api/v1`; sumber data halaman adalah `/partials/*`. Detail payload dan skema
HMAC ada di `API.md`.

## 4. Keputusan arsitektur (ADR)

### ADR-1 — Frontend tetap Go SSR + htmx (Diterima)

- **Konteks**: sempat dipertimbangkan frontend terpisah (Nuxt) yang mengonsumsi
  Go sebagai API murni, dengan kekhawatiran Go + htmx "berat" dan ikon berkedip
  saat navigasi/refresh.
- **Keputusan**: pertahankan Go SSR + htmx sebagai satu aplikasi.
- **Dasar**: navigasi htmx mengganti `#main-content` tanpa memuat ulang
  sidebar/ikon; pembaruan data memakai fragment HTML. Interaksi terasa ringan
  dengan satu runtime, satu deploy, CSP ketat tetap utuh, dan rantai pasok
  tetap ramping (tanpa `node_modules`). Lihat §9 untuk alasan lengkap penolakan
  Nuxt.
- **Konsekuensi**: jika suatu saat muncul kebutuhan klien frontend lain (mis.
  aplikasi mobile resmi), `/api/v1` sudah menjadi jalur JSON yang stabil —
  pemisahan bisa dilakukan saat itu, dengan alasan konkret, bukan sekarang.

### ADR-2 — Inference wajah di Python, penyimpanan & pencocokan di Go (Diterima, direvisi 28 Okt 2026)

- **Keputusan**: model deteksi & embedding wajah tinggal di `services/face`
  (Python, ONNX). Go memvalidasi perangkat/role, memanggil `POST /embed`
  (scan) dan `POST /register` (enrollment 1–6 pose), menyimpan embedding, dan
  mencocokkannya. Revisi 28 Okt: `/detect` dihapus dari Python; satu
  mahasiswa kini punya beberapa embedding pose dan skornya adalah pose terbaik.
- **Konsekuensi**: service Python tidak pernah diekspos ke jaringan publik;
  Go mengirim `FACE_SERVICE_TOKEN` sebagai Bearer (belum diperiksa oleh
  `api.py`).

Revisi: face service yang dibangun tim (`services/face/api.py`) bersifat
stateless (deteksi + embedding saja). Go menyimpan embedding di
`face_templates` dan melakukan pencocokan 1:N dengan cosine similarity
(`internal/api/face.go`). Dengan begitu hanya Go yang mengakses MongoDB dan
hanya Go yang menjadi API publik untuk dashboard maupun ESP32.

### ADR-7 — Foto wajah di object storage S3 (Garage), bukan MongoDB (Diterima, 26 Okt 2026)

- **Konteks**: pencocokan hanya butuh embedding, tetapi foto diperlukan untuk
  enrollment ulang saat model diganti, peninjauan scan, dan audit.
- **Keputusan**: simpan foto di bucket Garage privat; MongoDB hanya menyimpan
  kuncinya (`image_key`). Klien S3 ditulis dengan pustaka standar (SigV4)
  agar tidak menambah dependensi.
- **Konsekuensi**: penyimpanan foto bersifat *best effort* — Garage mati tidak
  menghentikan absensi. Foto scan punya retensi (30/90 hari); foto
  enrollment disimpan selama template masih ada.

### ADR-3 — htmx memakai fragment server-rendered, bukan JSON + DOM (Diterima)

- **Keputusan**: halaman terautentikasi mengambil HTML dari `/partials/*`
  (butuh session); `/api/v1/*` dipertahankan sebagai API integrasi, bukan
  sumber data halaman.
- **Konsekuensi**: validasi dan otorisasi tetap di server; tidak ada logika
  role di klien. Error form dikembalikan inline (HTTP 200) agar muncul di
  dalam form, sedangkan sukses memicu event via `HX-Trigger`.

### ADR-4 — Dua mekanisme auth yang terpisah (Diterima)

- **Staf**: session cookie `itbjh_session` (HttpOnly, SameSite=Lax, 8 jam, idle
  30 menit) + CSRF `itbjh_csrf` (gorilla). `RemoteAddr` untuk rate limit login
  (10 / 5 menit).
- **Perangkat**: bearer `DEVICE_TOKEN` + HMAC-SHA256 + timestamp.
- **Konsekuensi**: perangkat tidak punya jalur ke endpoint staf, dan sebaliknya.

### ADR-5 — CSP ketat, aset di-vendor lokal (Diterima)

- **Keputusan**: `script-src 'self'`, `allowEval = false`, tanpa CDN; JS/font
  pihak ketiga di-host sendiri dan versi-terpin.
- **Konsekuensi**: tidak ada permukaan skrip eksternal; kebijakan bisa
  dikencangkan tanpa menyesuaikan kode halaman.

### ADR-6 — MongoDB sebagai basis data (Diterima)

- **Konteks**: versi awal memakai SQLite berkas lokal (`data/attendance.db`)
  yang dibagi Go dan Python.
- **Keputusan**: pindah ke MongoDB di server terpisah (default
  `mongodb://10.100.1.11:27017`, database `attendance`).
- **Dasar** (disimpulkan dari kode — koreksi bila alasan sebenarnya berbeda):
  Go dan face service Python dapat berjalan di mesin berbeda dan mengakses
  data yang sama lewat jaringan, tanpa berbagi berkas SQLite.
- **Konsekuensi**: penugasan kelas dosen pindah ke `users.class_ids`; waktu
  disimpan sebagai string RFC3339 UTC dan "hari ini" dihitung di Go
  (`todayRangeUTC`); MongoDB wajib di-hardening (auth, bindIp, TLS, backup).
  Detail di `database.md`.

## 5. Batas kepercayaan

1. **Internet → Go (browser)**: session + CSRF; role & kelas diambil dari DB
   tiap request, bukan dari klien.
2. **Internet → Go (perangkat)**: scan memakai device token + HMAC +
   timestamp. Heartbeat ping hanya memakai token (dan hanya di production);
   `GET /api/v1/health` publik.
3. **Go → Face service**: jaringan internal, bearer `FACE_SERVICE_TOKEN`;
   port Python tidak diekspos.
4. **Go → MongoDB**: jaringan internal; di production wajib user aplikasi +
   `authorization: enabled` (panduan di `README.md`). Face service Python
   tidak mengakses MongoDB.
5. **Go → Garage (S3)**: jaringan internal, kunci akses khusus bucket
   (bukan `admin_token`). Browser tidak mengakses Garage.

## 6. Ringkasan kontrak API

Acuan payload/skema ada di `API.md`.

| Kelompok | Contoh | Auth |
| --- | --- | --- |
| Staf baca | `GET /api/v1/dashboard`, `/classes`, `/reports` | session |
| Staf tulis | `POST /api/v1/students`, `POST /api/v1/students/{nim}/face-template` | session + CSRF (admin) |
| Perangkat — scan | `POST /api/v1/scans` | device token + HMAC + timestamp |
| Staf baca foto | `GET /api/v1/images/{key...}` | session (dosen: hanya kelasnya) |
| Perangkat — heartbeat | `GET\|POST /api/v1/devices/ping` | device token (hanya production) |
| Publik | `GET /api/v1/health` | — |
| Fragment (staf) | `GET/POST /partials/*` | session (+ CSRF untuk POST) |

## 7. Konfigurasi & deployment

Satu proses di belakang reverse proxy/TLS. Face service dan database berada di
jaringan internal.

Variabel lingkungan (dari `cmd/server/main.go`):

| Variabel | Keterangan |
| --- | --- |
| `HTTP_ADDR` | alamat listen (default `:8080`) |
| `MONGODB_URI` | URI koneksi MongoDB (default `mongodb://10.100.1.11:27017`) |
| `MONGODB_DATABASE` | nama database MongoDB (default `attendance`) |
| `STATIC_DIR`, `VIEW_DIR` | lokasi aset & template |
| `APP_ENV` | `development` / `production` (cookie Secure, token face service & ping wajib) |
| `CSRF_SECRET` | wajib ≥32 karakter (semua mode) |
| `DEVICE_TOKEN` | wajib ≥32 karakter (semua mode) |
| `FACE_SERVICE_URL`, `FACE_SERVICE_TOKEN` | koneksi ke Python (token ≥32 di production) |
| `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | penyimpanan foto di Garage (kosong semua = nonaktif) |
| `IMAGE_RETENTION_DAYS`, `IMAGE_REVIEW_RETENTION_DAYS` | retensi foto scan (default 30 / 90 hari) |
| `BOOTSTRAP_ADMIN_*`, `BOOTSTRAP_LECTURER_*` | pembuatan akun awal |
| `BOOTSTRAP_RESET_PASSWORDS` | development saja: setel ulang password akun bootstrap setiap start |

`FACE_SIM_THRESHOLD` (default `0.45`, ambang cosine similarity) dan
`FACE_SERVICE_TIMEOUT_SECONDS` (default `15`) dibaca oleh Go;
`FACE_SERVICE_URL` default `http://127.0.0.1:8000`. Templat lengkap: `.env.example`. Zona waktu server menentukan
batas "hari ini" pada ringkasan dan laporan.

`.env` dimuat dengan `godotenv.Overload`, jadi nilainya menimpa environment
proses (lihat `code-guide.md` §8). Di production tanpa berkas `.env`, hanya
environment proses yang dipakai. Password staf minimal 12 karakter di
production dan 4 di development.

Catatan hardening: HSTS **belum** dipasang (lihat §10). Header yang sudah ada:
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy:
same-origin`, dan CSP.

## 8. Invarian

- Otorisasi hanya di server Go, dari DB — bukan dari role/query/header klien.
- Template wajah (embedding) tidak pernah dikirim ke browser; foto hanya
  lewat `GET /api/v1/images/...` setelah cek role/kelas.
- Kegagalan penyimpanan foto tidak boleh menggagalkan scan atau enrollment.
- Endpoint perangkat terpisah dari endpoint staf.
- CSP tidak boleh memasukkan `unsafe-inline` untuk `script-src`.
- Tidak ada akun login mahasiswa tanpa keputusan produk baru.

## 9. Opsi yang ditolak (arsip)

**Pemisahan frontend: Go API + Nuxt di domain berbeda.** Ditolak karena:

1. Menambah runtime kedua (Node) dan langkah deploy kedua, padahal satu Go
   binary sudah cukup; kekhawatiran "htmx berat" dan ikon berkedip tidak
   terbukti.
2. Harus merombak auth: dashboard dan API akan cross-site, sehingga cookie
   session + CSRF tidak bisa dipakai apa adanya. Perlu BFF atau token + CORS.
3. CSP harus dilonggarkan (nonce/hash) untuk bundle dan hidrasi Nuxt.
4. Menambah ratusan paket npm transitif, padahal CDN baru saja dihapus dan
   dependensi JS di-vendor lokal secara terpin.
5. Tidak ada kebutuhan klien frontend kedua saat ini; ESP32 sudah dilayani API
   JSON yang sama, dan `/api/v1` tetap tersedia jika itu berubah.

Dicatat agar tidak diusulkan ulang tanpa alasan baru yang konkret.

## 10. Risiko terbuka & TODO backend

Ini adalah pekerjaan backend yang masih outstanding (lihat juga `HANDOFF.md` §Batasan dan Risiko):

1. **Session & rate limit in-memory** → belum aman untuk multi-instance; butuh
   store bersama (Redis/DB) bila di-scale.
2. **Satu `DEVICE_TOKEN` global** untuk semua perangkat → butuh secret per
   perangkat + rotasi/revokasi.
3. **Belum ada anti-replay nonce** pada `POST /api/v1/scans`.
4. **Liveness** masih klaim perangkat (`liveness_passed`), bukan deteksi
   biologis tervalidasi.
5. **Ambang cosine `0.45` belum dikalibrasi**; pencocokan di Go memindai
   semua template tiap scan (belum ada pembatasan kandidat per kelas/cache).
6. **Audit & privasi**: belum ada alur persetujuan (consent), audit enrollment
   / aksi administrator, atau enkripsi backup. Retensi foto scan otomatis dan
   hapus mahasiswa ikut menghapus data biometriknya.
7. **Hardening HTTP**: HSTS dan audit logging belum ada.
8. **Port RPC Garage (3901)** terbuka di semua interface pada konfigurasi
   pengembangan; batasi dengan firewall atau ganti `rpc_secret`.
9. **Ping/health melakukan upsert perangkat** tanpa token (ping di
   non-production, health di semua mode) → ID perangkat baru bisa dibuat
   dari luar.
10. **Kredensial di firmware ESP8266** (Wi-Fi + `DEVICE_TOKEN`) tertulis di
   sketch; pindahkan ke `secrets.h` dan rotasi token.
11. **Face service tanpa autentikasi** (CORS `*`, token Go belum diperiksa)
    → wajib dijalankan di `127.0.0.1`/jaringan internal.
12. **Sisa mahasiswa contoh dari seed lama** (`IK24001`–`IK24005`) tidak
    dihapus otomatis; seed kini hanya kelas dan perangkat (lihat `database.md` §5).

## 11. Berkas acuan

- `README.md` — setup & konfigurasi.
- `API.md` — payload API & skema HMAC perangkat.
- `HANDOFF.md` — status implementasi, keputusan UI, daftar risiko.
- `cmd/server/main.go` — wiring route, middleware, env, bootstrap.
- `internal/api/server.go` — endpoint + query ber-scope role.
- `internal/api/admin.go`, `internal/controller/admin_partials.go` — fitur administrator.
- `internal/security/middleware.go` — session, CSRF, principal, CSP.
- `internal/controller/` — handler halaman & fragment.
- `internal/store/database.go` — koneksi, index, seed MongoDB.
- `database.md` — skema koleksi & operasi.
- `devices.md` — firmware & protokol perangkat.
- `face-service.md` — integrasi face service dan penyimpanan foto (§8).
- `internal/api/images.go`, `internal/storage/s3.go` — foto wajah di Garage.
- `diagram.excalidraw` — diagram tahapan proyek & alur sistem.
