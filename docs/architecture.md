# Arsitektur Algorithmics Face Recognation

> Status: **arsitektur yang berlaku saat ini** (Go SSR + htmx, satu binary).
> Dokumen ini menggantikan draf rencana pemisahan frontend/Nuxt yang ditolak —
> lihat §9 untuk catatannya.

## 1. Ringkasan

Satu aplikasi Go yang merender HTML dan sekaligus melayani API. Tidak ada
runtime kedua, tidak ada build frontend terpisah, tidak ada langkah deploy
kedua.

- **Bahasa/runtime**: Go (satu binary) + SQLite.
- **Frontend**: `html/template` server-rendered + htmx untuk navigasi/aksi
  tanpa reload penuh.
- **Layanan pendukung**: face service Python (internal, tidak diekspos).
- **Klien**: browser staf (admin/dosen) dan perangkat ESP32-CAM.

Konsekuensi yang disengaja: halaman dan API berbagi satu sumber query/validasi
(`internal/api`), sehingga aturan role tidak bisa berbeda antara tampilan dan
API.

## 2. Komponen

| Lapisan | Lokasi | Peran |
| --- | --- | --- |
| Entrypoint & routing | `cmd/server/main.go` | wiring route, middleware, bootstrap, env |
| HTTP API & domain | `internal/api/server.go` | endpoint staf + perangkat, query ber-scope role, error sentinel |
| Halaman & fragment | `internal/controller/` | render `views/pages` (shell) dan `views/fragments` (htmx) |
| Keamanan | `internal/security/middleware.go` | `RequireSession`, `RequireRole`, `Principal`, security headers/CSP |
| Penyimpanan | `internal/store/database.go` | schema, seed, migrasi SQLite |
| Tampilan | `views/` | `layouts/base.html`, `pages/*`, `fragments/*` |
| Aset statis | `static/` | `css/`, `js/app.js`, `js/lib/ui.js`, vendor lokal |
| Face service | `services/face/` | Python: `/enroll`, `/verify` (embedding, bukan gambar) |

Vendor frontend di-host lokal dan versi-terpin: htmx 2.0.11, Lucide 1.59.0,
dengan `htmx.config.allowEval = false`.

## 3. Alur data

### Halaman staf (browser)

```text
Browser ──GET /,/mahasiswa,/kelas,/laporan,/akses──► Go (shell htmx)
        ──GET /partials/...──────────────────────► Go (fragment HTML)
        ──POST /partials/... (+ X-CSRF-Token)────► Go → internal/api → SQLite
                                                    └── HX-Trigger: toast / roster-changed / ...
```

Navigasi sidebar memakai htmx `hx-boost` untuk mengambil halaman server-rendered
berikutnya lalu mengganti `#main-content` (`hx-target` + `hx-select`). Sidebar
dan ikon navigasi tetap di DOM, sehingga tidak ikut dimuat ulang. Aksi dan
pembaruan data memakai fragment HTML di `/partials/*` (bukan JSON + perakitan
DOM); ikon Lucide disegarkan setelah swap fragment.

### Perangkat (ESP32-CAM)

```text
ESP32 ──POST /api/v1/scans (bearer DEVICE_TOKEN + HMAC + timestamp)──► Go
                                                                       ├── Python /verify
                                                                       └── SQLite: scan_events
```

### Enrollment wajah (admin)

```text
Admin ──POST /partials/mahasiswa/students/{nim}/face-template (multipart)──► Go
                                                                             └── Python /enroll
```

### API staf (integrasi)

`GET /api/v1/*` tetap tersedia sebagai API JSON publik/integrasi (dipakai
perangkat). Sejak migrasi htmx, halaman **tidak** lagi membaca
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

### ADR-2 — Inference wajah tetap di Python, Go hanya orkestrasi (Diterima)

- **Keputusan**: model/encoding wajah tinggal di `services/face`. Go
  memvalidasi perangkat/role, memanggil `/verify` atau `/enroll`, dan menyimpan
  hasil.
- **Konsekuensi**: service Python tidak pernah diekspos ke jaringan publik;
  komunikasi memakai `FACE_SERVICE_TOKEN`.

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

## 5. Batas kepercayaan

1. **Internet → Go (browser)**: session + CSRF; role & kelas diambil dari DB
   tiap request, bukan dari klien.
2. **Internet → Go (perangkat)**: device token + HMAC + timestamp.
3. **Go → Face service**: jaringan internal, bearer `FACE_SERVICE_TOKEN`;
   port Python tidak diekspos.

## 6. Ringkasan kontrak API

Acuan payload/skema ada di `API.md`.

| Kelompok | Contoh | Auth |
| --- | --- | --- |
| Staf baca | `GET /api/v1/dashboard`, `/classes`, `/reports`, `/students` | session + (tulis) CSRF |
| Staf tulis | `POST /api/v1/students`, `POST /api/v1/students/{nim}/face-template` | session + CSRF (admin) |
| Perangkat | `POST /api/v1/scans`, `GET /api/v1/health` | device token + HMAC + timestamp |
| Fragment (staf) | `GET/POST /partials/*` | session (+ CSRF untuk POST) |

Catatan: `API.md` §3 masih menyebut halaman server-rendered membaca
`/api/v1`. Itu tidak lagi akurat sejak migrasi htmx — halaman membaca
`/partials/*`. (Perlu diselaraskan; belum diubah agar tidak mencampur
pekerjaan.)

## 7. Konfigurasi & deployment

Satu proses di belakang reverse proxy/TLS. Face service dan database berada di
jaringan internal.

Variabel lingkungan (dari `cmd/server/main.go`):

| Variabel | Keterangan |
| --- | --- |
| `HTTP_ADDR` | alamat listen (default `:8080`) |
| `DATABASE_PATH` | lokasi SQLite (default `data/attendance.db`) |
| `STATIC_DIR`, `VIEW_DIR` | lokasi aset & template |
| `APP_ENV` | `development` / `production` |
| `CSRF_SECRET` | wajib ≥32 karakter di production |
| `DEVICE_TOKEN` | wajib ≥32 karakter di production |
| `FACE_SERVICE_URL`, `FACE_SERVICE_TOKEN` | koneksi ke Python (token ≥32 di production) |
| `FACE_DISTANCE_THRESHOLD` | ambang pencocokan (default `0.50`) |
| `BOOTSTRAP_ADMIN_*`, `BOOTSTRAP_LECTURER_*` | pembuatan akun awal |

Catatan hardening: HSTS **belum** dipasang (lihat §10). Header yang sudah ada:
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy:
same-origin`, dan CSP.

## 8. Invarian

- Otorisasi hanya di server Go, dari DB — bukan dari role/query/header klien.
- Tidak ada gambar atau template wajah yang dikirim ke browser.
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

Ini adalah pekerjaan backend yang masih outstanding (dari `HANDOFF.md` §187):

1. **Session & rate limit in-memory** → belum aman untuk multi-instance; butuh
   store bersama (Redis/DB) bila di-scale.
2. **Satu `DEVICE_TOKEN` global** untuk semua perangkat → butuh secret per
   perangkat + rotasi/revokasi.
3. **Belum ada anti-replay nonce** pada `POST /api/v1/scans`.
4. **Liveness** masih klaim perangkat (`liveness_passed`), bukan deteksi
   biologis tervalidasi.
5. **Threshold `0.50` belum dikalibrasi**; `/verify` masih memindai semua
   template (belum ada pengindeksan kandidat per kelas).
6. **Audit & privasi**: belum ada consent/retention workflow, audit enrollment
   memadai, atau enkripsi backup.
7. **Hardening HTTP**: HSTS dan audit logging belum ada.

## 11. Berkas acuan

- `README.md` — setup & konfigurasi.
- `API.md` — payload API & skema HMAC perangkat.
- `HANDOFF.md` — status implementasi, keputusan UI, daftar risiko.
- `cmd/server/main.go` — wiring route, middleware, env, bootstrap.
- `internal/api/server.go` — endpoint + query ber-scope role.
- `internal/security/middleware.go` — session, CSRF, principal, CSP.
- `internal/controller/` — handler halaman & fragment.
- `internal/store/database.go` — schema, seed, migrasi.
- `services/face/main.py` — enroll/verify.
