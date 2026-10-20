# Project Handoff: Algorithmics Face Recognation

Dokumen ini adalah titik awal untuk AI/engineer berikutnya. Baca dokumen ini lebih dulu agar tidak perlu memetakan seluruh repository sebelum melanjutkan pekerjaan. Nama produk pada UI: **Algorithmics Face Recognation**; nama institusi/workspace: **Institut Teknologi Bacharuddin Jusuf Habibie (ITBJH)**.

Status dokumen: diperbarui **21 Oktober 2026** — dokumentasi diselaraskan dengan kode setelah migrasi **SQLite → MongoDB**, penambahan halaman **Log pemindaian** (`/logs`), endpoint **heartbeat perangkat** (`/api/v1/devices/ping`), dan firmware **ESP8266**. Dokumen baru: `docs/database.md`, `docs/devices.md`, `docs/face-service.md`, dan `.env.example`.

Riwayat sebelumnya: 18 Oktober 2026 — penyelarasan nama produk dan pembaruan UI login/navigasi; code cleaning (seluruh `*_test.go` dihapus, kredensial demo dibersihkan), diverifikasi dengan `go vet ./...`.

Repository Git: `github.com:soulightric/methics` (branch `main`). Modul Go: `dash.algorithmics`. Beberapa berkas terbaru **belum di-commit**, termasuk `README.md`, `devices/`, `views/pages/logs.html`, `views/fragments/logs.html`, dan `views/fragments/mahasiswa_form.html` — periksa `git status` sebelum melanjutkan.

## Ringkasan Produk

**Algorithmics Face Recognation** adalah sistem absensi mahasiswa berbasis scan wajah untuk ITBJH. Perangkat ESP32-CAM mengirim capture ke backend Go. Go meneruskan capture ke face service Python untuk pencocokan template wajah, memvalidasi hasil, lalu mencatat event absensi di **MongoDB**.

Tiga kelas seed:

- Ilmu Komputer A (`IK24-A`): Alya Putri (`IK24001`), Bima Saputra (`IK24002`)
- Ilmu Komputer B (`IK24-B`): Citra Nabila (`IK24003`), Dafa Ramadhan (`IK24004`)
- Ilmu Komputer C (`IK24-C`): Eka Anindya (`IK24005`)

Mahasiswa adalah **record akademik dan subjek absensi, bukan akun aplikasi**. Tidak ada login, bearer token, atau preview role mahasiswa. Akses aplikasi hanya untuk administrator dan dosen. Mahasiswa melakukan absensi melalui scan perangkat.

## Status Implementasi

Sudah berjalan:

- Halaman server-rendered: `/`, `/mahasiswa`, `/kelas`, `/laporan`, `/logs`, `/akses`, plus `/login`.
- Login staf dengan bcrypt, session SCS, CSRF, rate limit login, security headers/CSP, panic recovery.
- Otorisasi berbasis role dari session + pembatasan data dosen ke `users.class_ids` pada semua halaman dan API staf (dengan satu bug pada filter kelas halaman log — lihat Batasan no. 1).
- API dashboard, kelas, laporan; pendaftaran mahasiswa; proxy enrollment wajah ke Python.
- Endpoint scan perangkat dengan bearer device token, HMAC-SHA256, timestamp, batas ukuran gambar, dan pencatatan status `Hadir`/`Perlu ditinjau`/`Ditolak`.
- Heartbeat perangkat `GET|POST /api/v1/devices/ping` + firmware ESP8266 (`devices/esp8266/esp8266_ping/`) untuk status "Terhubung" di dashboard.
- Halaman Log pemindaian (`/logs`): 200 event terbaru seluruh waktu, filter status/kelas/pencarian di server, ringkasan seluruh waktu.
- MongoDB: koneksi, index, seed kelas/mahasiswa/perangkat, store akun staf.

Belum ada / masih terbatas:

- **Face service Python tidak ada di repo.** `services/face/` hanya berisi `README..txt` (placeholder). Kontrak dan kerangka implementasi ada di `docs/face-service.md`. Tanpa service ini, enrollment dan scan selalu gagal dengan `502 face_service_error`.
- **Firmware kamera ESP32-CAM** untuk `POST /api/v1/scans` belum ada; yang ada hanya heartbeat ESP8266.
- UI/API manajemen akun staf dan penugasan kelas (halaman `/akses` masih matriks statis).
- CRUD kelas dan perangkat; opsi filter kelas di `views/pages/mahasiswa.html` dan `views/pages/logs.html` masih hardcoded `IK24-A`/`IK24-B`/`IK24-C`.
- Rotasi/revokasi token per perangkat dan audit enrollment.
- Liveness detection nyata.
- Session store dan login limiter masih in-memory (tidak cocok untuk multi-instance tanpa perubahan).
- Test otomatis.

## Arsitektur MVC

Go server-rendered memakai `html/template`:

- **Entry point/router:** `cmd/server/main.go`
- **Controller halaman:** `internal/controller/web.go`
- **Page model/view data:** `internal/model/page.go`
- **Views:** `views/layouts/base.html`, `views/partials/{sidebar,topbar,toast}.html`, `views/pages/{dashboard,mahasiswa,kelas,laporan,logs,akses}.html`, `views/fragments/*.html`, `views/login.html`
- **CSS:** entry `static/css/app.css` mengimpor `tokens.css`, `base.css`, `layout.css`, `components.css`, `pages.css`, `responsive.css`, `print.css`
- **JavaScript:** entry `static/js/app.js` (ES module) hanya menyiapkan htmx + perilaku global; helper di `static/js/lib/ui.js`; htmx dan Lucide di-vendor di `static/js/vendor/`
- **Fragment HTML (htmx):** `internal/controller/partials.go` merender `views/fragments/*.html` memakai query ber-scope role dari `internal/api/server.go` (`ClassRows`, `StudentCount`, `TemplateCount`, `ScanStatusCounts`, `RecentEvents`, `AllScanEvents`, `AllScanStatusCounts`, `Roster`, `DeviceStatus`, `FaceStatus`, serta domain method `CreateStudent`/`EnrollFace`). Query, validasi, dan status integrasi hanya ada di `api`; controller tinggal merender.
- **API/controller aplikasi dan integrasi:** `internal/api/server.go`
- **Session, CSRF, principal dan security middleware:** `internal/security/middleware.go`
- **Login/logout controller:** `internal/controller/auth.go`
- **Koneksi MongoDB, index, seed:** `internal/store/database.go`
- **Akun staf, bcrypt, bootstrap helpers:** `internal/store/users.go`
- **Firmware perangkat:** `devices/esp8266/esp8266_ping/esp8266_ping.ino`
- **Python verifier:** belum ada (`services/face/`, lihat `docs/face-service.md`)
- **Test Go:** tidak ada saat ini

Route halaman dan fragment:

- `GET /`: ringkasan dashboard.
- `GET /mahasiswa`: roster, pendaftaran mahasiswa, dan enrollment wajah. Tombol daftar/enroll hanya dirender untuk administrator; data tetap dibatasi role oleh API.
- `GET /kelas`: ringkasan kehadiran dan enrollment per kelas. Dosen hanya menerima kelas yang ditugaskan.
- `GET /laporan`: rekap status scan, kehadiran per kelas, dan riwayat scan. Dibatasi role.
- `GET /logs`: log pemindaian seluruh waktu dengan filter status, kelas, dan pencarian. Dibatasi role.
- `GET /akses`: matriks hak akses. Administrator saja; dosen dialihkan ke `/`.
- `GET /static/*`: CSS/JS statis.
- `GET /partials/dashboard|kelas|laporan|mahasiswa|logs`, `GET /partials/mahasiswa/form`, `POST /partials/mahasiswa/students`, `POST /partials/mahasiswa/{nim}/face-template`: fragment htmx (detail di `API.md` §5).
- `GET /login`, `POST /login`, `POST /logout`: autentikasi staf.

Arsitektur UI: halaman dirender controller Go. Navigasi sidebar memakai htmx (`hx-boost`) untuk mengambil halaman berikutnya dan mengganti `#main-content`, sehingga sidebar/ikon tidak ikut dimuat ulang; data halaman diperbarui lewat fragment HTML `/partials/*`. Endpoint JSON `/api/v1/*` tetap tersedia untuk integrasi perangkat/test, bukan sumber data halaman. Sidebar menampilkan logo `algorithmics/anthropic.png` dengan nama produk **Algorithmics Face Recognation**; panel login memakai foto kampus sebagai visual kiri dan form kaca buram di kanan (berubah menjadi susunan vertikal di layar kecil).

Detail UI yang perlu diketahui:

- Semua halaman memakai pola yang sama: `views/pages/*.html` hanya memuat shell + kontainer, lalu htmx menukar fragment dari `/partials/*`. `/`, `/kelas`, dan `/laporan` memakai `hx-trigger="load, every 15s"`. `/mahasiswa` memakai form filter `#studentFilters` (`load, every 15s, keyup changed delay:300ms, change, roster-changed from:body`); `/logs` memakai form `#logFilters` (`load, every 15s, keyup changed delay:300ms, change`). Ringkasan roster dan ringkasan log diperbarui lewat out-of-band swap (`hx-swap-oob`).
- Tombol `#openStudentDialog` membuka `<dialog>` yang berisi `#studentFormShell` (dimuat dari `/partials/mahasiswa/form` saat halaman dimuat); enrollment wajah dikirim multipart dari `<input type="file" hidden>` di dalam tabel.
- `static/js/app.js` menonaktifkan `htmx.config.allowEval` (aman untuk CSP `script-src 'self'`), menyisipkan `X-CSRF-Token` pada setiap request htmx lewat `htmx:configRequest`, mengalihkan ke `/login` saat `htmx:responseError` 401, menyegarkan ikon Lucide dan status menu aktif pada `htmx:afterSwap`, serta menangani event `HX-Trigger` (`toast`, `student-dialog-close`) dan tombol cetak `#printReport`/dialog secara global (delegasi klik).
- `views/layouts/base.html` memuat Lucide dan htmx dari vendor lokal (versi terpin, lisensi di `static/js/vendor/*.LICENSE.txt`), Google Fonts, dan logo dari `https://proxy-assets.etherthink.xyz`. Tanpa internet, font/logo tidak tampil tetapi ikon tetap tampil. CSP di `internal/security/middleware.go` mengizinkan `fonts.googleapis.com`, `fonts.gstatic.com`, dan `proxy-assets.etherthink.xyz`.
- Nilai tampilan yang masih hardcoded: `TeacherName: "Dr. Rina Wibowo"` di `internal/model/page.go` dan inisial avatar sidebar (`RW`/`AY`) di `views/partials/sidebar.html`. Nama pengguna dan role sudah diambil dari session.

Keputusan frontend: **tetap Go server-rendered (bukan SPA framework)** dan adopsi **htmx** untuk interaktivitas. Migrasi htmx **selesai** untuk semua halaman. Query dan aturan tulis bertipe di `api` (`api.ClassRow`, `api.ScanEvent`, `api.RosterEntry`, domain method `CreateStudent`/`EnrollFace` dengan error sentinel) dan dipakai bersama endpoint JSON serta fragment; bentuk JSON lama dipertahankan lewat `classRowsToMaps`/`scanEventsToMaps`/`rosterToMaps`.

## Alur Sistem

1. Go memuat `.env`, memvalidasi secret, lalu terhubung ke MongoDB (`MONGODB_URI`, default `mongodb://10.100.1.11:27017`; database `MONGODB_DATABASE`, default `attendance`). Startup membuat index dan seed tiga kelas, lima mahasiswa, dan dua perangkat (`ESP32-GED-A1`, `ESP32-GED-B1`) bila belum ada. Seed **tidak** membuat template wajah atau scan palsu. Detail: `docs/database.md`.
2. Bila belum ada administrator aktif, akun dibuat dari `BOOTSTRAP_ADMIN_*`; akun dosen opsional dari `BOOTSTRAP_LECTURER_*`.
3. Staf login lewat `/login`. Password dicocokkan terhadap bcrypt hash; sukses melakukan session token renewal dan menaruh user ID ke session cookie HttpOnly/SameSite (`itbjh_session`, lifetime 8 jam, idle timeout 30 menit).
4. Middleware session memuat user aktif dari koleksi `users`, mengambil role dan `class_ids`, lalu menaruh principal di request context. Role tidak pernah dipercaya dari query/header browser.
5. CSRF middleware (`itbjh_csrf`) melindungi semua POST staf; form memakai `csrf_token`, request htmx mengirim `X-CSRF-Token`. Login dilimit 10 percobaan per IP per 5 menit. Security headers/CSP dan panic recovery membungkus server.
6. Halaman memuat & menyegarkan datanya lewat htmx dari `/partials/*` yang dirender server dari query ber-scope role. Dosen dibatasi ke `users.class_ids`; administrator melihat semua.
7. Administrator mendaftarkan mahasiswa dan mengirim foto enrollment lewat UI htmx; controller memakai domain method yang sama dengan endpoint JSON `POST /api/v1/students` dan `POST /api/v1/students/{nim}/face-template`.
8. Go meneruskan foto enrollment ke Python `/enroll` (`student_id` + `image`). Python seharusnya mendeteksi tepat satu wajah, membuat encoding 128 dimensi, dan menyimpannya ke koleksi `face_templates`; gambar mentah tidak disimpan.
9. Perangkat mengirim heartbeat ke `/api/v1/devices/ping` (memperbarui `devices.last_seen`) dan scan ke `POST /api/v1/scans` memakai bearer device token, timestamp, HMAC signature, metadata kelas/perangkat/liveness, dan file gambar. Route perangkat terpisah dan tidak memakai session/CSRF browser.
10. Go memvalidasi token, gambar, field, `captured_at`, HMAC/timestamp, perangkat, dan kelas; lalu meneruskan gambar ke Python `/verify` (`class_id` + `image`).
11. Python mengembalikan `verified`, `studentId`, `confidence`, `reason`. Go memeriksa keanggotaan kelas (`students.class_id`). Event tersimpan di `scan_events`; status `Hadir` hanya jika cocok, anggota kelas, dan flag liveness true. Flag liveness belum merupakan deteksi biologis.

Diagram ringkas:

```text
Browser -> Go SSR pages (htmx /partials/*) + /api/v1
ESP8266 -> Go /api/v1/devices/ping -> MongoDB devices.last_seen
ESP32   -> Go /api/v1/scans -> Python /verify -> MongoDB face_templates
                            -> MongoDB scan_events
Administrator -> Go /partials/mahasiswa/students -> MongoDB students
Administrator -> Go face-template -> Python /enroll -> MongoDB face_templates
```

## Kontrak API Penting

Detail lengkap ada di `API.md`. Ringkasan:

- `GET /api/v1/health`: publik; status backend dan Python. `faceVerification.online` hanya true bila Python `/health` mengembalikan `modelReady: true`. Query opsional `device_id` memperbarui `last_seen` perangkat (tanpa token).
- `GET /api/v1/dashboard`, `/classes`, `/reports`: session staf wajib; dibatasi role.
- `POST /api/v1/students`: JSON `{ "name": "...", "nim": "...", "classId": "IK24-A" }`; session admin + CSRF.
- `POST /api/v1/students/{nim}/face-template`: multipart field `image`; session admin + CSRF.
- `POST /api/v1/scans`: multipart `device_id`, `class_id`, `captured_at` RFC3339, `liveness_passed` boolean, `image`; device bearer token + HMAC.
- `GET|POST /api/v1/devices/ping`: heartbeat; token hanya diperiksa di production; upsert perangkat.
- `GET/POST /login`, `POST /logout`: login session staf, CSRF berlaku untuk POST.
- Python hanya internal: `GET /health`, `POST /enroll`, `POST /verify`. Jangan ekspos port Python ke jaringan publik.

Format pesan HMAC-SHA256 device, enam baris UTF-8 yang digabung newline:

```text
<X-Device-Timestamp RFC3339>
<device_id>
<class_id huruf besar>
<captured_at persis seperti field>
<liveness_passed lowercase true/false>
<hex SHA-256 dari byte image>
```

`X-Device-Signature` adalah digest HMAC hex; key = device token. Header `X-Device-Timestamp` maksimal berjarak dua menit dari waktu server. `captured_at` maksimal lima menit. Batas file gambar 8 MiB.

## Database

Basis data utama **MongoDB** (detail field, index, waktu, dan operasi di `docs/database.md`):

- `classes`: `_id` (kode kelas), `name`
- `students`: `_id` (`student-<nim>`), `nim` (unik), `name`, `email`, `class_id`
- `class_students`: relasi kelas-mahasiswa (ditulis, belum dibaca query)
- `users`: akun staf (`_id`, `username`, `display_name`, `role`, `password_hash`, `class_ids`, `active`, `created_at`)
- `devices`: `_id`, `name`, `last_seen` (RFC3339 UTC)
- `face_templates`: `student_id` (unik), `encoding`, `created_at` — ditulis face service
- `scan_events`: `student_id`, `class_id`, `device_id`, `scanned_at` (RFC3339 UTC), `face_verified`, `liveness_passed`, `confidence`, `status`, `review_reason`

Batas "hari ini" dihitung dari zona waktu lokal **server** (`todayRangeUTC`). Pada environment production, aktifkan otentikasi MongoDB sesuai panduan di `README.md`.

## Konfigurasi

Templat lengkap: `.env.example`. Go membaca:

- `APP_ENV` (`development` default; `production` mewajibkan `FACE_SERVICE_TOKEN`, mengaktifkan cookie Secure, dan mewajibkan token pada ping perangkat).
- `HTTP_ADDR` (default `:8080`), `STATIC_DIR` (default `static`), `VIEW_DIR` (default `views`).
- `MONGODB_URI` (default `mongodb://10.100.1.11:27017`), `MONGODB_DATABASE` (default `attendance`).
- `FACE_SERVICE_URL` (default `http://127.0.0.1:8001`), `FACE_SERVICE_TOKEN`.
- `DEVICE_TOKEN` (wajib, ≥ 32 karakter; bearer + kunci HMAC perangkat).
- `CSRF_SECRET` (wajib, ≥ 32 karakter).
- Bootstrap akun (saat belum ada administrator aktif): `BOOTSTRAP_ADMIN_USERNAME`, `BOOTSTRAP_ADMIN_PASSWORD`, opsional `BOOTSTRAP_LECTURER_USERNAME`, `BOOTSTRAP_LECTURER_PASSWORD`, `BOOTSTRAP_LECTURER_CLASSES`.

Face service Python (bila dibangun) membaca `FACE_SERVICE_TOKEN`, `FACE_DISTANCE_THRESHOLD` (default `0.50`), `MONGODB_URI`, dan `MONGODB_DATABASE`. Go **tidak** membaca `FACE_DISTANCE_THRESHOLD`.

## Menjalankan

```powershell
copy .env.example .env   # isi secret
go vet ./...
go run ./cmd/server      # atau: air
```

Buka `http://localhost:8080`. Tidak ada akun demo bawaan. Session store in-memory; restart Go akan logout semua sesi.

Tidak ada test otomatis (`go test ./...` hanya melaporkan "no test files"). Sebelumnya test memakai fake Python server (`httptest`) untuk `/health`, `/enroll`, dan `/verify`, serta mencakup scoping role, HMAC/token scan, status liveness, enrollment hanya admin, rate limit login, bcrypt/role akun, dan render halaman — tetapi ditulis untuk versi SQLite, sehingga perlu ditulis ulang untuk MongoDB bila dipulihkan dari arsip cadangan di luar folder proyek.

Untuk menguji alur scan tanpa perangkat keras, pakai klien uji Python di `docs/devices.md` §4 (membutuhkan face service yang berjalan).

## Batasan dan Risiko yang Harus Diingat

1. **Bug otorisasi pada log pemindaian.** Di `api.Server.AllScanEvents`, filter `class` dari query menimpa filter `$in` kelas dosen, sehingga dosen dapat membuka log kelas yang tidak ditugaskan lewat `/partials/logs?class=<kelas lain>`. Perbaiki sebelum dipakai dosen sungguhan (validasi `class` terhadap `principal.ClassIDs`).
2. **Kredensial perangkat tertulis di firmware.** `devices/esp8266/esp8266_ping/esp8266_ping.ino` memuat SSID, password Wi-Fi, dan `DEVICE_TOKEN` yang sama dengan `.env`. Folder `devices/` belum di-commit; pindahkan rahasia ke `secrets.h` yang di-gitignore dan **ganti `DEVICE_TOKEN`** sebelum commit/push (`docs/devices.md` §2).
3. **Ping & health dapat membuat perangkat baru.** `/api/v1/devices/ping` (tanpa token di non-production) dan `/api/v1/health?device_id=` (tanpa token di semua mode) melakukan upsert ke `devices`. ID yang terbentuk dengan cara ini lolos pengecekan "perangkat terdaftar" pada scan. Ping tanpa `device_id` juga selalu menandai `ESP32-GED-A1`.
4. **Login/session dan CSRF sudah dipasang, tetapi perlu deployment hardening.** Production memerlukan secret acak ≥ 32 karakter, TLS, session store bersama untuk multi-instance, dan pengelolaan/rotasi akun staf. HSTS belum dipasang.
5. **Mahasiswa tanpa login adalah keputusan produk saat ini.** Jangan menambahkan kembali `STUDENT_TOKEN`, opsi role mahasiswa, atau akses dashboard mandiri tanpa keputusan eksplisit baru.
6. **Liveness belum benar-benar diukur.** `liveness_passed` berasal dari perangkat dan hanya integritas pesannya dilindungi HMAC. HMAC tidak mencegah foto/video replay bila perangkat mengklaim liveness palsu, dan belum ada nonce anti-replay.
7. **Threshold `0.50` belum dikalibrasi**; ukur false acceptance/rejection sebelum produksi. Go mengirim `class_id` ke `/verify`, tetapi pencocokan per kelas bergantung pada implementasi Python; Go tetap memeriksa keanggotaan kelas.
8. Belum ada secret per perangkat, rotasi/revokasi token, UI manajemen akun/penugasan staf, audit enrollment, consent/retention workflow, enkripsi backup, atau penghapusan biometrik lewat UI.
9. **Batas "hari ini" mengikuti zona waktu server.** Jalankan server dengan zona waktu kampus (WITA, `Asia/Makassar`). `scanned_at` wajib RFC3339 UTC agar perbandingan string benar.
10. **URI MongoDB dicetak ke log saat startup**, termasuk password bila URI memuatnya.
11. **Index `users.username` case-sensitive**, sedangkan login case-insensitive; hindari akun yang hanya berbeda huruf besar/kecil.
12. **`class_students` belum dibaca query**; keanggotaan kelas efektif adalah `students.class_id` (satu kelas per mahasiswa).
13. Data seed berisi nama contoh; belum ada template wajah demo. Jangan menyimpulkan verifikasi wajah aktif hanya karena profil mahasiswa ada.
14. Google Fonts dan logo institusi masih dimuat dari internet; untuk lingkungan tertutup, vendor aset tersebut lalu sesuaikan CSP. Jangan menambahkan inline script (CSP `script-src 'self'`).
15. **Opsi filter kelas masih statis** di `views/pages/mahasiswa.html` dan `views/pages/logs.html`. Dialog pendaftaran sudah memakai opsi ber-scope dari server (`ClassRows`).
16. Sisa kode era SQLite: helper `placeholders` dan `stringArgs` di `internal/api/server.go` tidak lagi dipakai; komentar `data/ (SQLite)` di `.air.toml` sudah usang.

## Langkah Lanjutan yang Disarankan

1. Perbaiki bug scoping kelas di `AllScanEvents` (risiko no. 1).
2. Amankan kredensial firmware (secrets.h + rotasi `DEVICE_TOKEN`), lalu commit berkas yang belum ter-commit.
3. Implementasikan face service Python sesuai `docs/face-service.md`; uji `/health`, enrollment dengan gambar bersetuju, lalu match dan mismatch end-to-end; kalibrasi threshold.
4. Batasi ping/health agar hanya memperbarui perangkat terdaftar (tanpa upsert), dan wajibkan token di semua mode.
5. Tambahkan UI/API administrasi akun staf dan penugasan kelas di halaman `/akses`; akun login mahasiswa tetap tidak dibuat.
6. Tambahkan CRUD kelas/perangkat dan ganti opsi kelas hardcoded di view dengan data dari server.
7. Bangun firmware ESP32-CAM: NTP, HMAC, TLS, per-device secret, dan liveness hardware yang tervalidasi.
8. Tulis ulang test untuk MongoDB (mis. database uji terpisah): scoping role, HMAC/token scan, status liveness, enrollment hanya admin, rate limit, render halaman.
9. Pindahkan session store & login limiter ke penyimpanan bersama (mis. MongoDB/Redis) bila akan multi-instance.

## Berkas Acuan

Dokumentasi:

- `README.md` — setup, konfigurasi, hardening MongoDB, checklist production, troubleshooting.
- `docs/README.md` — indeks seluruh dokumentasi.
- `HANDOFF.md` — dokumen ini.
- `API.md` — kontrak API, format error, fragment `/partials/*`, HMAC perangkat.
- `docs/architecture.md` — arsitektur, batas kepercayaan, ADR, invarian.
- `docs/code-guide.md` — penjelasan kode per lapisan, alur request, resep, jebakan.
- `docs/database.md` — skema MongoDB, index, seed, waktu, operasi, backup.
- `docs/devices.md` — firmware ESP8266, protokol ping/scan, klien uji.
- `docs/face-service.md` — kontrak & kerangka face service Python.
- `docs/SRS.md` — kebutuhan fungsional & non-fungsional dengan status implementasi.
- `.env.example` — templat konfigurasi.

Kode: mulai dari `cmd/server/main.go` untuk wiring server, lalu `internal/controller`, `internal/api`, dan `internal/store` sesuai alur yang akan dikerjakan.
