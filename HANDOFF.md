# Project Handoff: Algorithmics Face Recognation

Dokumen ini adalah titik awal untuk AI/engineer berikutnya. Baca dokumen ini lebih dulu agar tidak perlu memetakan seluruh repository sebelum melanjutkan pekerjaan. Nama produk pada UI: **Algorithmics Face Recognation**; nama institusi/workspace: **Institut Teknologi Bacharuddin Jusuf Habibie (ITBJH)**.

Status dokumen: diperbarui **18 Oktober 2026** untuk menyelaraskan nama produk dan mencatat pembaruan UI login/navigasi. Perubahan sebelumnya mencakup code cleaning (seluruh berkas `*_test.go` dihapus dan kredensial demo dibersihkan dari source produksi), diverifikasi ulang dengan `go vet ./...`. Paket dokumentasi terdiri dari `docs/README.md` (indeks), `docs/code-guide.md` (penjelasan kode), dan `docs/SRS.md` (kerangka SRS siap-isi). Workspace ini belum berupa git repository, jadi tidak ada riwayat commit yang bisa dirujuk.

## Ringkasan Produk

**Algorithmics Face Recognation** adalah sistem absensi mahasiswa berbasis scan wajah untuk ITBJH. ESP32 mengirim capture ke backend Go. Go meneruskan capture ke service Python untuk pencocokan template wajah, memvalidasi hasil, lalu mencatat event absensi di SQLite.

Tiga kelas seed:

- Ilmu Komputer A (`IK24-A`): Alya Putri (`IK24001`), Bima Saputra (`IK24002`)
- Ilmu Komputer B (`IK24-B`): Citra Nabila (`IK24003`), Dafa Ramadhan (`IK24004`)
- Ilmu Komputer C (`IK24-C`): Eka Anindya (`IK24005`)

Mahasiswa adalah **record akademik dan subjek absensi, bukan akun aplikasi**. Tidak ada login, bearer token, atau preview role mahasiswa. Akses aplikasi hanya untuk administrator dan dosen. Mahasiswa melakukan absensi melalui scan perangkat.

## Status Implementasi

Sudah berjalan:

- Halaman server-rendered: `/`, `/mahasiswa`, `/kelas`, `/laporan`, `/akses`, plus `/login`.
- Login staf dengan bcrypt, session SCS, CSRF, rate limit login, security headers/CSP, panic recovery.
- Otorisasi berbasis role dari session + pembatasan data dosen ke `user_classes` pada dashboard/kelas/laporan.
- API dashboard, kelas, laporan; pendaftaran mahasiswa; proxy enrollment wajah ke Python.
- Endpoint scan ESP32 dengan bearer device token, HMAC-SHA256, timestamp, batas ukuran gambar, dan pencatatan status `Hadir`/`Perlu ditinjau`/`Ditolak`.
- SQLite schema, seed kelas/mahasiswa/perangkat, store, security, controller, dan API.

Belum ada / masih terbatas:

- UI/API manajemen akun staf dan penugasan kelas (halaman `/akses` masih matriks statis).
- CRUD kelas dan perangkat; daftar kelas di form/view masih hardcoded `IK24-A`/`IK24-B`/`IK24-C`.
- Rotasi/revokasi token per perangkat dan audit enrollment.
- Liveness detection nyata; service Python belum dijalankan/diuji end-to-end di mesin ini.
- Session store dan login limiter masih in-memory (tidak cocok untuk multi-instance tanpa perubahan).

## Arsitektur MVC

Go server-rendered memakai `html/template`:

- **Entry point/router:** `cmd/server/main.go`
- **Controller halaman:** `internal/controller/web.go`
- **Page model/view data:** `internal/model/page.go`
- **Views:** `views/layouts/base.html`, `views/partials/sidebar.html`, `views/partials/topbar.html`, `views/partials/toast.html`, `views/pages/{dashboard,mahasiswa,kelas,laporan,akses}.html`, `views/fragments/*.html`, `views/login.html`
- **CSS:** entry `static/css/app.css` mengimpor `tokens.css`, `base.css`, `layout.css`, `components.css`, `pages.css`, `responsive.css`, `print.css`
- **JavaScript:** entry `static/js/app.js` (ES module) hanya menyiapkan htmx + perilaku global; tidak ada modul per-halaman lagi (`static/js/pages/` dan `static/js/lib/api.js` sudah dihapus), helper tersisa di `static/js/lib/ui.js`; htmx yang di-vendor di `static/js/vendor/htmx.min.js` untuk pembaruan fragment
- **Fragment HTML (htmx):** `internal/controller/partials.go` merender `views/fragments/*.html` memakai query/logika ber-scope role dari `internal/api/server.go` (lihat `ClassRows`, `StudentCount`, `TemplateCount`, `ScanStatusCounts`, `RecentEvents`, `Roster`, `DeviceStatus`, `FaceStatus`, serta domain method `CreateStudent`/`EnrollFace`). SQL, validasi, dan status integrasi hanya ada di `api`; controller tinggal merender.
- **API/controller aplikasi dan integrasi:** `internal/api/server.go`
- **Session, CSRF, principal dan security middleware:** `internal/security/middleware.go`
- **Login/logout controller:** `internal/controller/auth.go`
- **SQLite schema, migration, seed:** `internal/store/database.go`
- **Akun staf, bcrypt, bootstrap helpers:** `internal/store/users.go`
- **Python verifier:** `services/face/main.py`
- **Test Go:** tidak ada saat ini (dihapus pada code cleaning; tidak ada berkas `*_test.go` di repo)

Route halaman:

- `GET /`: ringkasan dashboard.
- `GET /mahasiswa`: roster, pendaftaran mahasiswa, dan enrollment wajah. Tombol daftar/enroll hanya dirender untuk administrator; data tetap dibatasi role oleh API.
- `GET /kelas`: ringkasan kehadiran dan enrollment per kelas. Dosen hanya menerima kelas yang ditugaskan.
- `GET /laporan`: rekap status scan, kehadiran per kelas, dan riwayat scan. Dibatasi role.
- `GET /akses`: matriks hak akses. Administrator saja; dosen dialihkan ke `/`.
- `GET /static/*`: CSS/JS statis.
- `GET /partials/dashboard`: fragment HTML (htmx) berisi metrik ringkasan, kehadiran per kelas, status perangkat/layanan, dan tabel scan terbaru. Wajib session; data dibatasi role.
- `GET /partials/kelas`: fragment HTML (htmx) berisi ringkasan dan tabel kelas. Wajib session; data dibatasi role.
- `GET /partials/laporan`: fragment HTML (htmx) berisi ringkasan, komposisi status, kehadiran per kelas, dan riwayat scan. Wajib session; data dibatasi role.
- `GET /partials/mahasiswa`: fragment roster mahasiswa (tabel + ringkasan; ringkasan dikirim lewat out-of-band swap `hx-swap-oob`). Menerima query `q` dan `class` (filter di server); data dibatasi role.
- `GET /partials/mahasiswa/form`: formulir dialog pendaftaran mahasiswa. Administrator saja; opsi kelas diambil dari `ClassRows` sehingga ikut ter-scope role.
- `POST /partials/mahasiswa/students`: menambah mahasiswa (form-urlencoded, administrator saja). Sukses membalas form bersih + header `HX-Trigger` (`toast`, `student-dialog-close`, `roster-changed`); gagal membalas form dengan pesan validasi inline.
- `POST /partials/mahasiswa/{nim}/face-template`: enrollment wajah (multipart, administrator saja). Selalu 200; hasil/kesalahan ditampilkan inline di region roster.
- `GET /login`, `POST /login`, `POST /logout`: autentikasi staf.

Arsitektur UI: halaman dirender controller Go. Navigasi sidebar memakai htmx untuk mengambil halaman berikutnya dan mengganti `#main-content`, sehingga sidebar/ikon tidak ikut dimuat ulang; data halaman diperbarui lewat fragment HTML `/partials/*`. Endpoint JSON `/api/v1/*` tetap tersedia untuk integrasi perangkat/test, bukan sumber data halaman. Sidebar menampilkan logo `algorithmics/anthropic.png` dengan nama produk **Algorithmics Face Recognation**; panel login memakai foto kampus sebagai visual kiri dan form kaca buram di kanan (berubah menjadi susunan vertikal di layar kecil).

Detail UI yang perlu diketahui:

- `static/js/app.js` tidak lagi memuat modul per-halaman; ia hanya memasang konfigurasi htmx dan perilaku global. Pemuatan/penyegaran data tiap halaman (termasuk dashboard setiap 15 detik) dilakukan oleh atribut `hx-get`/`hx-trigger` pada kontainer di fragment.
- Navigasi sidebar di `views/partials/sidebar.html` memakai `hx-boost` dengan target `#main-content`; panel utama hasil server render menggantikan panel sebelumnya tanpa reload penuh. `static/js/app.js` memperbarui status menu aktif dan menyegarkan ikon sesudah swap.
- Semua halaman memakai pola yang sama: `views/pages/*.html` hanya memuat shell + kontainer, lalu htmx menukar fragment dari `/partials/*`. `/`, `/kelas`, dan `/laporan` memakai `hx-trigger="load, every 15s"`; `/mahasiswa` memakai form filter `#studentFilters` (`hx-get="/partials/mahasiswa"`, `hx-trigger="load, every 15s, keyup changed delay:300ms, change, roster-changed from:body"`) sehingga pencarian/filter kelas diproses di server, ringkasan roster diperbarui lewat out-of-band swap (`hx-swap-oob`), tombol `#openStudentDialog` membuka `<dialog>` yang mengisi `#studentFormShell` dari `/partials/mahasiswa/form`, dan enrollment wajah dikirim multipart dari `<input type="file" hidden>` di dalam tabel. `static/js/app.js` menonaktifkan `htmx.config.allowEval` (aman untuk CSP `script-src 'self'`), menyisipkan `X-CSRF-Token` pada setiap request htmx lewat event `htmx:configRequest`, mengalihkan ke `/login` saat `htmx:responseError` 401, menyegarkan ikon Lucide pada `htmx:afterSwap`, dan menangani event `HX-Trigger` (`toast`, `student-dialog-close`) serta tombol cetak `#printReport`/dialog secara global (delegasi klik).
- Filter pencarian/kelas pada halaman mahasiswa dikerjakan di server (query `q`/`class` ke `/partials/mahasiswa`); pengisian ulang periodik menghapus pesan inline sementara.
- Perbaikan kecil saat migrasi: badge `#deviceState`/`#faceState` di dashboard kini benar-benar berganti kelas `online`/`offline` (sebelumnya teks berubah tetapi warna badge tetap offline), kode kelas pada kartu "Kelas aktif" diambil dari data ber-scope role (bukan hardcoded), dan `pattern` NIM diperbaiki agar valid di Chrome (regex `v`-flag).
- `views/layouts/base.html` memuat Lucide yang sudah di-vendor lokal dari `static/js/vendor/lucide.min.js` (versi terpin, lisensi di `static/js/vendor/lucide.LICENSE.txt`), htmx dari `static/js/vendor/htmx.min.js` (versi terpin, lisensi di `static/js/vendor/htmx.LICENSE.txt`), Google Fonts, dan logo dari `https://proxy-assets.etherthink.xyz`. Tanpa internet, font/logo tidak tampil tetapi ikon tetap tampil (fungsi utama tetap berjalan). CSP di `internal/security/middleware.go` mengizinkan `fonts.googleapis.com`, `fonts.gstatic.com`, dan `proxy-assets.etherthink.xyz`; `unpkg.com` tidak lagi diizinkan.
- Form logout memakai `csrf_token`; request htmx mengirim `X-CSRF-Token` dari meta tag.

Keputusan frontend: **tetap Go server-rendered (bukan SPA framework)** dan adopsi **htmx** untuk interaktivitas. Migrasi bertahap (strangler) **selesai**: `/`, `/kelas`, `/laporan`, dan `/mahasiswa` semuanya memakai htmx + `/partials/*`. Endpoint JSON `/api/v1/*` tetap dipertahankan sebagai API publik/integrasi (dipakai pula oleh perangkat dan test), bukan sumber data halaman. Query dan aturan tulis kini bertipe di `api` (`api.ClassRow`, `api.ScanEvent`, `api.RosterEntry`, serta domain method `CreateStudent`/`EnrollFace` dengan error sentinel) dan dipakai bersama endpoint JSON serta fragment agar tidak ada duplikasi SQL/validasi; bentuk JSON lama tetap dipertahankan lewat `classRowsToMaps`/`scanEventsToMaps`/`rosterToMaps`.

## Alur Sistem

1. Go membuka SQLite (`DATABASE_PATH`, default `data/attendance.db`), membuat schema bila belum ada, dan seed tiga kelas, lima mahasiswa, dan dua perangkat demo (`ESP32-GED-A1`, `ESP32-GED-B1`). Seed mahasiswa **tidak** membuat template wajah atau attendance palsu.
2. Staf login lewat `/login`. Password dicocokkan terhadap bcrypt hash; sukses melakukan session token renewal dan menaruh user ID ke session cookie HttpOnly/SameSite (`itbjh_session`, lifetime 8 jam, idle timeout 30 menit).
3. Middleware session memuat user aktif dari SQLite, mengambil role dan kelas dosen, lalu menaruh principal di request context. Role tidak pernah dipercaya dari query/header browser.
4. CSRF middleware (`itbjh_csrf`) melindungi semua POST browser/staf; form memakai `csrf_token`, request htmx mengirim `X-CSRF-Token`. Login dilimit 10 percobaan per IP per 5 menit. Security headers/CSP dan panic recovery membungkus server.
5. Halaman server-rendered memuat & menyegarkan datanya lewat htmx: kontainer memanggil `GET /partials/*` (dashboard/kelas/laporan `every 15s`; mahasiswa lewat form filter), yang dirender server dari query ber-scope role. Go membatasi dosen ke kelas yang ditugaskan dalam tabel `user_classes`; administrator melihat semua roster.
6. Administrator mendaftarkan mahasiswa dan mengirim foto enrollment lewat UI htmx (`POST /partials/mahasiswa/students`, `POST /partials/mahasiswa/{nim}/face-template`); controller memakai domain method yang sama dengan endpoint JSON `POST /api/v1/students` dan `POST /api/v1/students/{nim}/face-template` (session admin + CSRF).
7. Go meneruskan foto enrollment ke Python `/enroll`. Python mendeteksi tepat satu wajah, membuat encoding 128 dimensi, dan menyimpan encoding ke tabel `face_templates`; gambar mentah tidak disimpan.
8. ESP32 mengirim scan ke `POST /api/v1/scans` memakai bearer device token, timestamp, HMAC signature, metadata kelas/perangkat/liveness, dan file gambar. Route ini terpisah dan tidak memakai session/CSRF browser.
9. Go memvalidasi device, class, timestamp, HMAC, dan ukuran gambar; Go meneruskan gambar sementara ke Python `/verify`.
10. Python mencocokkan wajah dan mengembalikan student ID kandidat serta confidence. Go memastikan kandidat anggota kelas. Event tersimpan di `scan_events`; status `Hadir` hanya jika cocok, anggota kelas, dan flag liveness true. Flag liveness belum merupakan deteksi biologis.

Diagram ringkas:

```text
Browser -> Go SSR pages (htmx /partials/*) + /api/v1
ESP32 -> Go /api/v1/scans -> Python /verify -> SQLite templates
Administrator -> Go /api/v1/students -> SQLite roster
Administrator -> Go face-template -> Python /enroll -> SQLite templates
```

## Kontrak API Penting

- `GET /api/v1/health`: publik; status backend dan Python. `integrations.faceVerification.online` hanya true bila Python `/health` mengembalikan `modelReady` (praktisnya: database siap dan service hidup), bukan bukti bahwa inferensi wajah sudah diuji.
- `GET /api/v1/dashboard`: session staf wajib; ringkasan (`students`, `classes`, `verifiedScans`, `scansToReview`, `faceTemplates`), kelas dalam cakupan role, attendance/roster hari ini (`faceVerified`, `faceEnrolled`), status integrasi.
- `GET /api/v1/classes`: session staf wajib; ringkasan dan daftar kelas dengan jumlah mahasiswa, template wajah, hadir hari ini, dan tingkat kehadiran; dibatasi role.
- `GET /api/v1/reports`: session staf wajib; ringkasan status scan hari ini, komposisi status, kehadiran per kelas, dan 50 event scan terbaru; dibatasi role.
- `POST /api/v1/students`: JSON `{ "name": "...", "nim": "...", "classId": "IK24-A" }`; session admin + CSRF.
- `POST /api/v1/students/{nim}/face-template`: multipart field `image`; session admin + CSRF.
- `POST /api/v1/scans`: multipart `device_id`, `class_id`, `captured_at` RFC3339, `liveness_passed` boolean, `image`; device bearer token + HMAC.
- `GET/POST /login`, `POST /logout`: login session staf, CSRF berlaku untuk POST.
- Python hanya internal: `GET /health`, `POST /enroll`, `POST /verify`. Jangan ekspos port Python ke jaringan publik.

Catatan konsistensi: contoh JSON di `API.md` sudah disinkronkan dengan kode (menyertakan `summary.faceTemplates` dan `attendance[].faceEnrolled`). Kode tetap acuan utama bila keduanya berbeda.

Format pesan HMAC-SHA256 device, persis enam baris UTF-8 yang digabung newline:

```text
<X-Device-Timestamp RFC3339>
<device_id>
<class_id>
<captured_at persis seperti field>
<liveness_passed lowercase true/false>
<hex SHA-256 dari byte image>
```

`X-Device-Signature` adalah digest HMAC hex; key = device token. Header `X-Device-Timestamp` maksimal berjarak dua menit dari waktu server. `captured_at` maksimal lima menit. Batas file gambar 8 MiB. Detail lengkap ada di `API.md`.

## Database

Tabel utama:

- `classes`, `students`, `class_students`
- `users` (password bcrypt, role staf, active), `user_classes` (penugasan kelas dosen)
- `devices`
- `face_templates` (satu encoding JSON per student)
- `scan_events` (student/class/device, timestamp, face/liveness flags, confidence, status, review reason)

SQLite memakai WAL, foreign keys, koneksi Go maksimum satu koneksi. Python harus diarahkan ke **file database yang sama** melalui absolute `DATABASE_PATH`. Schema dibuat dengan `CREATE TABLE IF NOT EXISTS`; belum ada sistem versi migrasi.

## Konfigurasi

Go membaca environment berikut:

- `APP_ENV` (`development` default; `production` mewajibkan `FACE_SERVICE_TOKEN` dan mengaktifkan cookie Secure).
- `HTTP_ADDR` (default `:8080`).
- `DATABASE_PATH` (default `data/attendance.db`).
- `STATIC_DIR` (default `static`), `VIEW_DIR` (default `views`).
- `FACE_SERVICE_URL` (default `http://127.0.0.1:8001`), `FACE_SERVICE_TOKEN`.
- `DEVICE_TOKEN` (wajib, acak minimal 32 karakter; dipakai staf-perangkat untuk bearer + HMAC).
- `CSRF_SECRET` (wajib, acak minimal 32 karakter).
- Bootstrap akun (semua mode, saat database kosong): `BOOTSTRAP_ADMIN_USERNAME`, `BOOTSTRAP_ADMIN_PASSWORD`, opsional `BOOTSTRAP_LECTURER_USERNAME`, `BOOTSTRAP_LECTURER_PASSWORD`, `BOOTSTRAP_LECTURER_CLASSES`.

Python membaca `DATABASE_PATH`, `FACE_SERVICE_TOKEN`, dan `FACE_DISTANCE_THRESHOLD` (default `0.50`).

## Menjalankan

Go (Go 1.23+):

```powershell
go vet ./...
go run ./cmd/server
```

Buka `http://localhost:8080`. Default database: `data/attendance.db`.

Tidak ada akun demo bawaan. Startup pertama pada database kosong membaca `BOOTSTRAP_ADMIN_USERNAME`/`BOOTSTRAP_ADMIN_PASSWORD` (opsional `BOOTSTRAP_LECTURER_*`), dan server mewajibkan `CSRF_SECRET` serta `DEVICE_TOKEN` minimal 32 karakter di semua mode. Perangkat ESP32 memakai `DEVICE_TOKEN` yang sama dengan yang di-set.

Session store saat ini in-memory; restart Go akan logout semua sesi. Untuk deployment multi-instance, pindahkan store session ke Redis/database dan atur TLS reverse proxy.

Python perlu Python 3.10–3.12, FastAPI, NumPy, `face-recognition`/dlib, CMake dan toolchain C/C++. Versi pinned ada di `services/face/requirements.txt`. Lihat perintah virtualenv di `README.md`. Pada kondisi workspace terakhir Python/launcher belum terdeteksi di Windows, sehingga service wajah belum dapat dijalankan/diuji di mesin ini. Go dan UI bisa berjalan dengan Python status offline.

Tidak ada test otomatis di repo saat ini (berkas `*_test.go` dihapus pada code cleaning), sehingga `go test ./...` hanya melaporkan "no test files". Sebelumnya test memakai fake Python server (`httptest`) untuk `/health`, `/enroll`, dan `/verify`, serta mencakup scoping role, HMAC/token scan, status liveness, enrollment hanya admin, rate limit login, bcrypt/role akun, dan render halaman. Salinan lengkap ada di arsip zip cadangan di luar folder proyek bila perlu dipulihkan.

## Batasan dan Risiko yang Harus Diingat

1. **Login/session dan CSRF sudah dipasang, tetapi perlu deployment hardening.** Production memerlukan `CSRF_SECRET`, `DEVICE_TOKEN`, dan `FACE_SERVICE_TOKEN` acak minimal 32 karakter, TLS, session store bersama untuk multi-instance, dan pengelolaan/rotasi akun staf. Login limiter dan session store saat ini in-memory.
2. **Mahasiswa tanpa login adalah keputusan produk saat ini.** Jangan menambahkan kembali `STUDENT_TOKEN`, opsi role mahasiswa, atau akses dashboard mandiri tanpa keputusan eksplisit baru.
3. **Liveness belum benar-benar diukur.** `liveness_passed` berasal dari perangkat dan hanya integritas pesannya dilindungi HMAC; ini bukan bukti biologis. HMAC tidak mencegah foto/video replay bila perangkat mengklaim liveness palsu. Perlu mekanisme anti-spoof tervalidasi dan peninjauan manusia untuk kasus berisiko.
4. Python memakai `face_recognition` threshold default `0.50` yang dapat diatur `FACE_DISTANCE_THRESHOLD`. Threshold belum dikalibrasi pada dataset kampus; ukur false acceptance/rejection sebelum produksi. `/verify` mencocokkan ke **semua** template yang ada, lalu Go memeriksa keanggotaan kelas; belum ada pengindeksan per kelas.
5. Belum ada manajemen secret per perangkat, rotasi/revokasi token, UI manajemen akun/penugasan staf, audit enrollment memadai, consent/retention workflow, backup encryption, atau penghapusan biometrik lengkap. Rate limit login saat ini in-memory dan tidak tersinkron antarreplika.
6. Capture gambar diproses sementara dan template disimpan sebagai embedding, tetapi embedding tetap data biometrik sensitif. Lindungi disk, database, backup, serta akses operasional.
7. Data seed berisi nama contoh; belum ada template wajah demo. Jangan menyimpulkan verifikasi wajah aktif hanya karena profil mahasiswa ada.
8. **Ketergantungan CDN pada UI (sebagian sudah diperbaiki):** ikon Lucide kini di-vendor lokal dengan versi terpin (`static/js/vendor/lucide.min.js`) dan `unpkg.com` sudah dihapus dari CSP, sehingga ikon tetap tampil offline dan tidak ada lagi risiko supply-chain dari `@latest`. Google Fonts dan logo institusi masih dimuat dari internet; untuk lingkungan tertutup/offline, vendor aset tersebut juga lalu sesuaikan CSP. Jangan menambahkan inline script karena CSP saat ini hanya mengizinkan `script-src 'self'`.
9. **Opsi filter kelas masih statis.** Dialog pendaftaran sudah memakai opsi kelas ber-scope dari server (`ClassRows`), tetapi `<select>` filter kelas di `views/pages/mahasiswa.html` masih hardcoded (`IK24-A/B/C`). Menambah/mengganti kelas baru masih memerlukan pembaruan opsi itu, insert database, dan seed bila diinginkan.

## Langkah Lanjutan yang Disarankan

1. Tambahkan UI/API administrasi akun staf dan penugasan kelas di halaman `/akses`; akun login mahasiswa tetap tidak dibuat.
2. Tambahkan CRUD kelas/perangkat dan ganti opsi kelas hardcoded di view dengan data dari API.
3. Pasang Python environment, uji `/health`, enrollment dengan gambar persetujuan, lalu uji match dan mismatch end-to-end; kalibrasi threshold.
4. Definisikan protokol ESP32 final: mutual device identity/per-device secrets, timestamp/replay handling, payload format, TLS, serta liveness detection hardware yang tervalidasi.
5. Tambahkan test integrasi persistent database, migrasi versi, file upload, confidence edge cases, enrollment permissions, dan request replay.
6. Evaluasi penggunaan SQLite bersama Go/Python untuk deployment multi-instance; pertimbangkan satu service database/queue bila concurrency atau deployment terpisah meningkat.

## Berkas Acuan

Dokumentasi:

- `README.md` — setup singkat, konfigurasi, kredensial lokal, perintah menjalankan.
- `docs/README.md` — indeks seluruh dokumentasi (mulai dari sini).
- `HANDOFF.md` — dokumen ini; status, keputusan, risiko, prioritas lanjutan.
- `docs/architecture.md` — arsitektur, batas kepercayaan, ADR, dan invarian sistem.
- `docs/code-guide.md` — penjelasan kode per lapisan, tiga alur request, model data, resep tugas umum, dan jebakan.
- `docs/SRS.md` — kerangka SRS (kebutuhan fungsional & non-fungsional) siap diisi; penanda `[TERSEDIA]`/`[SEBAGIAN]`/`[BELUM]` dan `ISI:` untuk bagian yang perlu keputusanmu.
- `API.md` — payload API, HMAC perangkat, dan endpoint fragment `/partials/*`.

Kode:

Mulai dari `cmd/server/main.go` untuk wiring server, lalu `internal/controller`, `internal/api`, `internal/store`, dan `services/face/main.py` sesuai alur yang akan dikerjakan.
