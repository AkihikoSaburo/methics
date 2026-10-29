# Project Handoff: Algorithmics Face Recognation

Dokumen ini adalah titik awal untuk AI/engineer berikutnya. Baca dokumen ini lebih dulu agar tidak perlu memetakan seluruh repository sebelum melanjutkan pekerjaan. Nama produk pada UI: **Algorithmics Face Recognation**; nama institusi/workspace: **Institut Teknologi Bacharuddin Jusuf Habibie (ITBJH)**.

Pembaruan 28 Oktober 2026 (kamera Wi-Fi, mode uji) — karena kamera belum bisa dicatu/dikabel bersama ESP32 utama (5V kamera dari ESP32 memicu brownout), ditambahkan `devices/esp32cam_wifi` (ESP32-CAM di MB dengan USB sendiri, HTTP `/ping` `/on` `/capture` `/off` + header `X-Cam-Token`, mDNS `methics-cam.local`) dan pilihan `CAM_LINK_WIFI` di `esp32_scanner` (default 1 = Wi-Fi; 0 = kabel UART). Alur scan dan backend Go tidak berubah. `secrets.h` ESP32 utama kini butuh `CAM_TOKEN` pada mode Wi-Fi.

Pembaruan 28 Oktober 2026 (scan stream) — rangkaian perangkat tetap, cara scan berubah: ESP32-CAM mati saat idle dan menyala (`ON`) begitu ultrasonik mendeteksi orang; ESP32 utama mengambil hingga 4 frame QVGA selama ±2 detik lalu mengirim semuanya dalam satu `POST /api/v1/scans` (field `image` berulang, HMAC atas SHA-256 semua frame). Go (`internal/api/scan.go`) mencocokkan tiap frame lewat `/embed`; wajah yang sama cocok di ≥ `SCAN_MIN_MATCH_FRAMES` (2) frame → **Hadir** di kelas mahasiswa itu (`CLASS_ID` kosong = kelas otomatis); wajah tak dikenal → `result: tidak_terdaftar`, layar TFT **"Wajah tidak Terdaftar"**. Konsensus frame bukan anti-spoof (Batasan no. 21).

Pembaruan 28 Oktober 2026 (face service) — `services/face/api.py` kini hanya menyediakan `/embed`, `/register`, `/health`, dan `/models*` (`/detect` dan `/compare` dihapus; `main.py` menjadi peluncur API). Go disesuaikan: scan memakai `POST /embed`, enrollment memakai `POST /register` dengan **1–6 foto pose** per mahasiswa (`face_templates.encodings`, cek konsistensi pose dan duplikat per foto), pencocokan memakai pose terbaik tiap mahasiswa, semua foto enrollment disimpan di Garage (`image_keys`), dan halaman Mahasiswa menerima beberapa file sekaligus + tombol **Enroll ulang**. Template lama (satu `encoding`) tetap dipakai sampai di-enroll ulang. Perbaikan susulan: enrollment >2 foto sempat terputus karena `ReadTimeout`/`WriteTimeout` server 15 detik dan timeout klien 15 detik; kini request enrollment mendapat batas 3 menit, `/register` mendapat `FACE_SERVICE_TIMEOUT_SECONDS` × jumlah foto, unggah Garage paralel, dan cek konsistensi pose memakai ambang terpisah `FACE_POSE_THRESHOLD` (0.30).

Pembaruan 26 Oktober 2026 (data mahasiswa) — seed mahasiswa contoh dihapus (seed kini hanya kelas dan perangkat; mahasiswa didaftarkan manual), NIM wajib **9 digit angka** (mis. `241011001`, `api.ValidNIM`, error `invalid_nim`), dan tombol **Lihat foto** enrollment di halaman Mahasiswa (`RosterEntry.FaceImageURL`, `faceImageUrl` di JSON dashboard).

Status dokumen: diperbarui **26 Oktober 2026 (penyelarasan dokumentasi)** — seluruh dokumen Markdown (kecuali `docs/SRS.md`, yang dirawat terpisah oleh tim) dan komentar kode diselaraskan dengan kode saat ini; temuan baru: seed mahasiswa memunculkan kembali mahasiswa contoh yang dihapus (Batasan no. 17), dan sebagian besar helper di `static/js/lib/ui.js` tidak terpakai.

Pembaruan 26 Oktober 2026 (akun & mahasiswa) — administrator dapat menghapus mahasiswa (beserta template wajah, foto Garage, dan riwayat scan) dan membuat/menghapus akun dosen di `/akses` (`internal/api/admin.go`, `internal/controller/admin_partials.go`); `.env` kini menimpa environment (`godotenv.Overload`); mode development menerima password pendek dan `BOOTSTRAP_RESET_PASSWORDS`.

Pembaruan 26 Oktober 2026 (perangkat) — firmware perangkat absensi ESP32 + ESP32-CAM lewat UART (`devices/esp32_scanner`, `devices/esp32cam_uart`): sensor → foto → `POST /api/v1/scans` → hasil di layar, plus heartbeat; lolos kompilasi, belum diuji di perangkat keras.

Pembaruan 26 Oktober 2026 (sore) — penyimpanan foto wajah opsional di object storage S3 (**Garage**, `internal/storage/s3.go` + `internal/api/images.go`) dengan retensi otomatis dan tampilan foto di halaman Log; bug scoping filter kelas di `AllScanEvents` diperbaiki; code cleaning (fungsi mati dan pembungkus header keamanan ganda dihapus).

Pembaruan 26 Oktober 2026 (pagi) — face service Python (`services/face/`, YOLO-FaceV2 + ArcFace) dihubungkan ke backend Go: Go memanggil `POST /detect` (sejak 28 Oktober diganti `/embed` + `/register`), menyimpan embedding di MongoDB, dan melakukan pencocokan cosine sendiri (`internal/api/face.go`), sehingga Go menjadi satu-satunya API untuk dashboard dan ESP32.

Pembaruan 21 Oktober 2026 — dokumentasi diselaraskan dengan kode setelah migrasi **SQLite → MongoDB**, penambahan halaman **Log pemindaian** (`/logs`), endpoint **heartbeat perangkat** (`/api/v1/devices/ping`), dan firmware **ESP8266**. Dokumen baru: `docs/database.md`, `docs/devices.md`, `docs/face-service.md`, dan `.env.example`.

Riwayat sebelumnya: 18 Oktober 2026 — penyelarasan nama produk dan pembaruan UI login/navigasi; code cleaning (seluruh `*_test.go` dihapus, kredensial demo dibersihkan), diverifikasi dengan `go vet ./...`.

Repository Git: `github.com:soulightric/methics` (branch `main`). Modul Go: `dash.algorithmics`. Periksa `git status` sebelum melanjutkan.

## Ringkasan Produk

**Algorithmics Face Recognation** adalah sistem absensi mahasiswa berbasis scan wajah untuk ITBJH. Perangkat absensi (ESP32 utama + ESP32-CAM lewat UART) mengirim capture ke backend Go. Go meminta embedding wajah dari face service Python, mencocokkannya dengan template di **MongoDB**, lalu mencatat event absensi. Foto capture dan foto enrollment dapat disimpan di bucket **Garage** (S3) untuk peninjauan dan audit.

Tiga kelas seed: Ilmu Komputer A (`IK24-A`), Ilmu Komputer B (`IK24-B`), Ilmu Komputer C (`IK24-C`). Mahasiswa **tidak** di-seed; administrator mendaftarkannya manual di halaman Mahasiswa dengan NIM 9 digit angka (format `241011001`, `241011002`, …).

Mahasiswa adalah **record akademik dan subjek absensi, bukan akun aplikasi**. Tidak ada login, bearer token, atau preview role mahasiswa. Akses aplikasi hanya untuk administrator dan dosen. Mahasiswa melakukan absensi melalui scan perangkat.

## Status Implementasi

Sudah berjalan:

- Halaman server-rendered: `/`, `/mahasiswa`, `/kelas`, `/laporan`, `/logs`, `/akses`, plus `/login`.
- Login staf dengan bcrypt, session SCS, CSRF, rate limit login, security headers/CSP, panic recovery.
- Otorisasi berbasis role dari session + pembatasan data dosen ke `users.class_ids` pada semua halaman dan API staf (termasuk filter kelas halaman log dan endpoint foto).
- API dashboard, kelas, laporan; pendaftaran dan penghapusan mahasiswa (hapus ikut menghapus template wajah, foto Garage, dan riwayat scan); enrollment wajah 1–6 foto pose (Python `/register` → semua embedding disimpan Go).
- Administrasi akun: administrator membuat akun dosen (username, nama, password, kelas) dan menghapusnya di `/akses`; daftar semua akun staf. Akun administrator tidak bisa dihapus dari UI.
- Bootstrap akun dari `.env`: admin dibuat bila belum ada administrator aktif; dosen bootstrap opsional; di development `BOOTSTRAP_RESET_PASSWORDS=true` menyetel ulang password kedua akun bootstrap setiap start. Password minimal 12 karakter di production, 4 di development.
- Endpoint scan perangkat dengan bearer device token, HMAC-SHA256, timestamp, batas ukuran gambar, dan pencatatan status `Hadir`/`Perlu ditinjau`/`Ditolak`.
- Heartbeat perangkat `GET|POST /api/v1/devices/ping` untuk status "Terhubung" di dashboard.
- Firmware perangkat absensi: `devices/esp32_scanner/` (ESP32 utama: HC-SR04, TFT ILI9341, buzzer, Wi-Fi, NTP, HMAC, `POST /api/v1/scans`, heartbeat) dan `devices/esp32cam_uart/` (ESP32-CAM tanpa Wi-Fi, kirim JPEG + CRC32 lewat UART 460800). Lolos kompilasi core ESP32 3.0.7 dan uji format request terhadap backend; **belum diuji di perangkat keras**. Sketch uji lama tetap ada: `algorithmics_engine_ultrasonic_buzzer/`, `esp32cam_stream/`, `esp8266_ping/`.
- Halaman Log pemindaian (`/logs`): 200 event terbaru seluruh waktu, filter status/kelas/pencarian di server, ringkasan seluruh waktu.
- MongoDB: koneksi, index, seed kelas/perangkat, store akun staf.
- Penyimpanan foto wajah di Garage (opsional, `S3_*`): foto enrollment (`enroll/…`) dan capture scan (`scans/…`), endpoint `GET /api/v1/images/{key...}` ber-otorisasi, kolom **Foto** di halaman Log, job retensi tiap jam, status di `/api/v1/health`.

Belum ada / masih terbatas:

- Firmware perangkat belum diuji di perangkat keras; `CLASS_ID` default kosong (kelas otomatis = kelas mahasiswa) karena belum ada jadwal kuliah per ruangan; `liveness_passed` dikirim `false` dan status Hadir berasal dari konsensus ≥ 2 frame, bukan anti-spoof.
- Pengelolaan akun staf masih sebagian: administrator bisa **membuat dan menghapus akun dosen** (beserta kelasnya) di `/akses`, tetapi belum bisa mengubah kelas, reset password, atau menonaktifkan akun lewat UI.
- CRUD kelas dan perangkat; opsi filter kelas di `views/pages/mahasiswa.html` dan `views/pages/logs.html` masih hardcoded `IK24-A`/`IK24-B`/`IK24-C`.
- Rotasi/revokasi token per perangkat dan audit enrollment.
- Liveness detection nyata.
- Foto enrollment (semua pose) bisa dibuka lewat tombol **Lihat foto** dan nomor 2–6 di halaman Mahasiswa (hanya bila fotonya tersimpan di Garage); enrollment ulang otomatis dari foto tersimpan belum ada.
- Face service memilih wajah dengan confidence tertinggi tanpa melaporkan jumlah wajah, jadi foto berisi beberapa orang tidak lagi ditolak (dulu `multiple_faces`).
- Template lama (satu foto, field `encoding`) belum dimigrasi otomatis; enroll ulang mahasiswa itu dengan beberapa pose.
- Session store dan login limiter masih in-memory (tidak cocok untuk multi-instance tanpa perubahan).
- Test yang membutuhkan MongoDB (unit test saat ini untuk face service, pencocokan, foto, klien S3, validasi akun dosen, dan render fragment).

## Arsitektur MVC

Go server-rendered memakai `html/template`:

- **Entry point/router:** `cmd/server/main.go`
- **Controller halaman:** `internal/controller/web.go`
- **Page model/view data:** `internal/model/page.go`
- **Views:** `views/layouts/base.html`, `views/partials/{sidebar,topbar,toast}.html`, `views/pages/{dashboard,mahasiswa,kelas,laporan,logs,akses}.html`, `views/fragments/*.html`, `views/login.html`
- **CSS:** entry `static/css/app.css` mengimpor `tokens.css`, `base.css`, `layout.css`, `components.css`, `pages.css`, `responsive.css`, `print.css`
- **JavaScript:** entry `static/js/app.js` (ES module) hanya menyiapkan htmx + perilaku global; helper di `static/js/lib/ui.js`; htmx dan Lucide di-vendor di `static/js/vendor/`
- **Fragment HTML (htmx):** `internal/controller/partials.go` dan `admin_partials.go` merender `views/fragments/*.html` memakai query ber-scope role dari `internal/api` (`ClassRows`, `StudentCount`, `TemplateCount`, `ScanStatusCounts`, `RecentEvents`, `AllScanEvents`, `AllScanStatusCounts`, `Roster`, `DeviceStatus`, `FaceStatus`, serta domain method `CreateStudent`/`EnrollFace`/`DeleteStudent`/`CreateLecturer`/`DeleteLecturer`/`StaffAccounts`). Query, validasi, dan status integrasi hanya ada di `api`; controller tinggal merender.
- **API/controller aplikasi dan integrasi:** `internal/api/server.go`; fitur administrator (hapus mahasiswa, akun dosen) di `internal/api/admin.go`
- **Session, CSRF, principal dan security middleware:** `internal/security/middleware.go`
- **Login/logout controller:** `internal/controller/auth.go`
- **Foto wajah (S3 Garage):** `internal/api/images.go` (unggah, otorisasi, retensi) dan `internal/storage/s3.go` (klien S3 SigV4, pustaka standar)
- **Koneksi MongoDB, index, seed:** `internal/store/database.go`
- **Akun staf, bcrypt, bootstrap helpers:** `internal/store/users.go`
- **Firmware perangkat:** `devices/esp32_scanner/` (ESP32 utama) dan `devices/esp32cam_uart/` (kamera); lihat `docs/devices.md` §2–3
- **Face service Python:** `services/face/` (FastAPI stateless, YOLO-FaceV2 + ArcFace 512-d); klien & pencocokan di Go: `internal/api/face.go` (lihat `docs/face-service.md`)
- **Test Go:** `internal/api/{face,images,admin}_test.go`, `internal/storage/s3_test.go`, `internal/controller/fragments_test.go` (tanpa MongoDB)

Route halaman dan fragment:

- `GET /`: ringkasan dashboard.
- `GET /mahasiswa`: roster, pendaftaran & penghapusan mahasiswa, enrollment wajah, dan tombol **Lihat foto** enrollment (admin dan dosen untuk kelasnya). Tombol daftar/enroll/hapus hanya dirender untuk administrator; data tetap dibatasi role oleh API. Hapus mahasiswa juga menghapus template wajah, foto di Garage, dan riwayat scannya.
- `GET /kelas`: ringkasan kehadiran dan enrollment per kelas. Dosen hanya menerima kelas yang ditugaskan.
- `GET /laporan`: rekap status scan, kehadiran per kelas, dan riwayat scan. Dibatasi role.
- `GET /logs`: log pemindaian seluruh waktu dengan filter status, kelas, dan pencarian, plus tautan foto capture. Dibatasi role.
- `GET /akses`: buat/hapus akun dosen, daftar akun staf, dan matriks hak akses. Administrator saja; dosen dialihkan ke `/`.
- `GET /static/*`: CSS/JS statis.
- `GET /partials/dashboard|kelas|laporan|mahasiswa|logs`, `GET /partials/mahasiswa/form`, `POST /partials/mahasiswa/students`, `POST /partials/mahasiswa/{nim}/face-template`, `DELETE /partials/mahasiswa/{nim}`, `GET /partials/akses/staff`, `POST /partials/akses/lecturers`, `DELETE /partials/akses/staff/{id}`: fragment htmx (detail di `API.md` §5).
- `GET /login`, `POST /login`, `POST /logout`: autentikasi staf.

Arsitektur UI: halaman dirender controller Go. Navigasi sidebar memakai htmx (`hx-boost`) untuk mengambil halaman berikutnya dan mengganti `#main-content`, sehingga sidebar/ikon tidak ikut dimuat ulang; data halaman diperbarui lewat fragment HTML `/partials/*`. Endpoint JSON `/api/v1/*` tetap tersedia untuk integrasi perangkat/test, bukan sumber data halaman. Sidebar menampilkan logo `algorithmics/anthropic.png` dengan nama produk **Algorithmics Face Recognation**; panel login memakai foto kampus sebagai visual kiri dan form kaca buram di kanan (berubah menjadi susunan vertikal di layar kecil).

Detail UI yang perlu diketahui:

- Semua halaman memakai pola yang sama: `views/pages/*.html` hanya memuat shell + kontainer, lalu htmx menukar fragment dari `/partials/*`. `/`, `/kelas`, dan `/laporan` memakai `hx-trigger="load, every 15s"`. `/mahasiswa` memakai form filter `#studentFilters` (`load, every 15s, keyup changed delay:300ms, change, roster-changed from:body`); `/logs` memakai form `#logFilters` (`load, every 15s, keyup changed delay:300ms, change`). Ringkasan roster dan ringkasan log diperbarui lewat out-of-band swap (`hx-swap-oob`).
- Tombol `#openStudentDialog` membuka `<dialog>` yang berisi `#studentFormShell` (dimuat dari `/partials/mahasiswa/form` saat halaman dimuat); enrollment wajah dikirim multipart dari `<input type="file" hidden>` di dalam tabel. Tombol hapus mahasiswa (`hx-delete` + `hx-confirm`) ada di kolom Aksi untuk administrator.
- `/akses` memuat panel "Akun staf" dari `/partials/akses/staff` (form buat dosen + tabel akun dengan tombol hapus dosen) di atas matriks hak akses.
- `static/js/lib/ui.js`: hanya `refreshIcons` dan `toast` yang dipakai; helper lain (perakit tabel dari JSON) sisa versi sebelum htmx dan aman dihapus.
- `static/js/app.js` menonaktifkan `htmx.config.allowEval` (aman untuk CSP `script-src 'self'`), menyisipkan `X-CSRF-Token` pada setiap request htmx lewat `htmx:configRequest`, mengalihkan ke `/login` saat `htmx:responseError` 401, menyegarkan ikon Lucide dan status menu aktif pada `htmx:afterSwap`, serta menangani event `HX-Trigger` (`toast`, `student-dialog-close`) dan tombol cetak `#printReport`/dialog secara global (delegasi klik).
- `views/layouts/base.html` memuat Lucide dan htmx dari vendor lokal (versi terpin, lisensi di `static/js/vendor/*.LICENSE.txt`), Google Fonts, dan logo dari `https://proxy-assets.etherthink.xyz`. Tanpa internet, font/logo tidak tampil tetapi ikon tetap tampil. CSP di `internal/security/middleware.go` mengizinkan `fonts.googleapis.com`, `fonts.gstatic.com`, dan `proxy-assets.etherthink.xyz`.
- Nilai tampilan yang masih hardcoded: `TeacherName: "Dr. Rina Wibowo"` di `internal/model/page.go` (hanya atribut `data-teacher-name`) dan inisial avatar sidebar (`RW`/`AY`) di `views/partials/sidebar.html`. Nama pengguna dan role sudah diambil dari session.

Keputusan frontend: **tetap Go server-rendered (bukan SPA framework)** dan adopsi **htmx** untuk interaktivitas. Migrasi htmx **selesai** untuk semua halaman. Query dan aturan tulis bertipe di `api` (`api.ClassRow`, `api.ScanEvent`, `api.RosterEntry`, domain method `CreateStudent`/`EnrollFace` dengan error sentinel) dan dipakai bersama endpoint JSON serta fragment; bentuk JSON lama dipertahankan lewat `classRowsToMaps`/`scanEventsToMaps`/`rosterToMaps`.

## Alur Sistem

1. Go memuat `.env` dengan `godotenv.Overload` (nilai `.env` menimpa environment), memvalidasi secret, lalu terhubung ke MongoDB (`MONGODB_URI`, default `mongodb://10.100.1.11:27017`; database `MONGODB_DATABASE`, default `attendance`; `.env` pengembangan saat ini memakai `methics`). Startup membuat index dan meng-upsert seed tiga kelas dan dua perangkat (`ESP32-GED-A1`, `ESP32-GED-B1`) di **setiap** start. Seed **tidak** membuat mahasiswa, template wajah, atau scan palsu. Detail: `docs/database.md`.
2. Bila belum ada administrator aktif, akun dibuat dari `BOOTSTRAP_ADMIN_*`; akun dosen opsional dari `BOOTSTRAP_LECTURER_*`. Dengan `BOOTSTRAP_RESET_PASSWORDS=true` (development saja), password kedua akun itu disetel ulang setiap start.
3. Staf login lewat `/login`. Password dicocokkan terhadap bcrypt hash; sukses melakukan session token renewal dan menaruh user ID ke session cookie HttpOnly/SameSite (`itbjh_session`, lifetime 8 jam, idle timeout 30 menit).
4. Middleware session memuat user aktif dari koleksi `users`, mengambil role dan `class_ids`, lalu menaruh principal di request context. Role tidak pernah dipercaya dari query/header browser.
5. CSRF middleware (`itbjh_csrf`) melindungi semua POST/DELETE staf; form memakai `csrf_token`, request htmx mengirim `X-CSRF-Token`. Login dilimit 10 percobaan per IP per 5 menit. Security headers/CSP dan panic recovery membungkus server.
6. Halaman memuat & menyegarkan datanya lewat htmx dari `/partials/*` yang dirender server dari query ber-scope role. Dosen dibatasi ke `users.class_ids`; administrator melihat semua.
7. Administrator mendaftarkan, menghapus, dan meng-enroll mahasiswa serta membuat/menghapus akun dosen lewat UI htmx; controller memakai domain method yang sama dengan endpoint JSON (`/api/v1/students…`, `/api/v1/staff…`).
8. Administrator memilih 1–6 foto pose (depan, kiri, kanan, atas, bawah, senyum); Go mengirim semuanya ke Python `POST /register`. Go menolak foto tanpa wajah (`no_face`, menyebut nomor foto), foto yang bukan orang yang sama (`inconsistent_faces`, ambang `FACE_POSE_THRESHOLD` 0.30 terhadap foto pertama atau rata-rata foto lain), dan wajah yang sudah terdaftar atas NIM lain (cosine ≥ `FACE_SIM_THRESHOLD`), lalu menyimpan semua embedding ArcFace 512-d ke satu dokumen `face_templates` (`encodings`). Bila `S3_*` diisi, semua foto disimpan ke Garage (`face_templates.image_keys`, foto pertama juga di `image_key`); foto lama dihapus saat enrollment ulang.
9. Perangkat mengirim heartbeat ke `/api/v1/devices/ping` (memperbarui `devices.last_seen`) dan scan ke `POST /api/v1/scans` memakai bearer device token, timestamp, HMAC signature, metadata kelas (opsional)/perangkat/liveness, dan 1–6 frame dari jendela scan ±2 detik. Route perangkat terpisah dan tidak memakai session/CSRF browser.
10. Go memvalidasi token, frame, field, `captured_at`, HMAC/timestamp, perangkat, dan kelas (bila diisi); lalu mengirim frame satu per satu ke Python `POST /embed`; skor tiap mahasiswa per frame adalah kemiripan pose terbaiknya, dan Go berhenti begitu satu mahasiswa cocok di `SCAN_MIN_MATCH_FRAMES` frame.
11. Python mengembalikan embedding wajah dengan confidence tertinggi per frame (`422` bila tidak ada wajah). Go memilih mahasiswa dengan frame cocok terbanyak. Status `Hadir` bila cocok (≥ ambang) di ≥ `SCAN_MIN_MATCH_FRAMES` frame (atau liveness true) dan — bila perangkat memakai `CLASS_ID` tetap — mahasiswa anggota kelas itu; dengan `CLASS_ID` kosong event dicatat di kelas mahasiswa. Wajah tidak cocok → `Ditolak` dengan `result: tidak_terdaftar` (TFT: "Wajah tidak Terdaftar"); tanpa wajah → `wajah_tidak_terdeteksi`.
12. Bila `S3_*` diisi, satu frame bukti (skor terbaik) diunggah ke Garage (batas 3 detik) dan kuncinya disimpan di `scan_events.image_key`; gagal unggah tidak menggagalkan scan. Job retensi menghapus foto scan setelah `IMAGE_RETENTION_DAYS` (Hadir, default 30) atau `IMAGE_REVIEW_RETENTION_DAYS` (lainnya, default 90).

Diagram ringkas:

```text
Browser -> Go SSR pages (htmx /partials/*) + /api/v1
ESP8266 -> Go /api/v1/devices/ping -> MongoDB devices.last_seen
ESP32 (kamera ON, ±2 s, 1–6 frame) -> Go /api/v1/scans -> Python /embed per frame -> Go cosine vs semua pose, konsensus ≥ 2 frame
                            -> Garage scans/... (opsional) -> MongoDB scan_events (+ image_key)
Administrator -> Go /partials/mahasiswa/students -> MongoDB students (+ class_students)
Administrator -> Go DELETE /partials/mahasiswa/{nim} -> Garage (hapus foto) -> MongoDB (hapus students, face_templates, scan_events, class_students)
Administrator -> Go /partials/akses/lecturers -> MongoDB users (role dosen)
Administrator -> Go face-template (1–6 foto) -> Python /register -> Go cek pose & duplikat -> MongoDB face_templates
                                  -> Garage enroll/... (opsional)
Staf -> Go /api/v1/images/{key} (cek role/kelas) -> Garage
```

## Kontrak API Penting

Detail lengkap ada di `API.md`. Ringkasan:

- `GET /api/v1/health`: publik; status backend, Python, dan penyimpanan foto. `faceVerification.online` hanya true bila Python `/health` menjawab dalam 3 detik dengan `model_loaded: true`; `imageStorage.online` bila `HEAD` bucket berhasil. Query opsional `device_id` memperbarui `last_seen` perangkat (tanpa token).
- `GET /api/v1/dashboard`, `/classes`, `/reports`: session staf wajib; dibatasi role.
- `POST /api/v1/students`: JSON `{ "name": "...", "nim": "...", "classId": "IK24-A" }`; session admin + CSRF.
- `POST /api/v1/students/{nim}/face-template`: multipart field `images` (1–6 foto; `image` juga diterima); session admin + CSRF.
- `DELETE /api/v1/students/{nim}`: hapus mahasiswa + template, foto, riwayat scan; session admin + CSRF.
- `GET|POST /api/v1/staff`, `DELETE /api/v1/staff/{id}`: daftar akun staf, buat akun dosen (JSON `username`, `displayName`, `password`, `classIds`), hapus akun dosen; session admin + CSRF.
- `POST /api/v1/scans`: multipart `device_id`, `class_id` (opsional, kosong = kelas otomatis), `captured_at` RFC3339, `liveness_passed` boolean, 1–6 `image`; device bearer token + HMAC atas SHA-256 semua frame. Respons memuat `result` (`hadir`/`perlu_ditinjau`/`tidak_terdaftar`/`wajah_tidak_terdeteksi`) dan `message` untuk layar.
- `GET|POST /api/v1/devices/ping`: heartbeat; token hanya diperiksa di production; upsert perangkat.
- `GET /api/v1/images/{key...}`: foto enrollment/scan dari Garage; session staf, dosen hanya kelasnya.
- `GET/POST /login`, `POST /logout`: login session staf, CSRF berlaku untuk POST/DELETE.
- Python hanya internal; Go memakai `GET /health`, `POST /embed`, dan `POST /register`. `python main.py` tanpa argumen mendengarkan di `0.0.0.0`; jalankan dengan `--host 127.0.0.1`. Jangan ekspos port Python (default `8000`) ke jaringan publik — `api.py` belum punya autentikasi dan memakai CORS `*`.

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
- `face_templates`: `student_id` (unik), `encodings` (1–6 × 512-d), `template_count`, `dimension`, `detection_confidences`, `image_key` + `image_keys` (opsional), `created_at`, `updated_at` — ditulis Go; dokumen lama memakai `encoding`/`detection_confidence` tunggal dan tetap dibaca
- `scan_events`: `student_id`, `class_id`, `device_id`, `scanned_at` (RFC3339 UTC), `face_verified`, `liveness_passed`, `confidence` (cosine similarity), `status`, `review_reason`, `image_key` (opsional)

Foto tidak disimpan di MongoDB, melainkan di bucket Garage (`S3_BUCKET`); MongoDB hanya menyimpan kuncinya.

Batas "hari ini" dihitung dari zona waktu lokal **server** (`todayRangeUTC`). Pada environment production, aktifkan otentikasi MongoDB sesuai panduan di `README.md`.

## Konfigurasi

Templat lengkap: `.env.example`. Go membaca:

- `APP_ENV` (`development` default; `production` mewajibkan `FACE_SERVICE_TOKEN`, mengaktifkan cookie Secure, dan mewajibkan token pada ping perangkat).
- `HTTP_ADDR` (default `:8080`), `STATIC_DIR` (default `static`), `VIEW_DIR` (default `views`).
- `MONGODB_URI` (default `mongodb://10.100.1.11:27017`), `MONGODB_DATABASE` (default `attendance`).
- `FACE_SERVICE_URL` (default `http://127.0.0.1:8000`), `FACE_SERVICE_TOKEN` (dikirim sebagai Bearer; belum diperiksa oleh `api.py`), `FACE_SIM_THRESHOLD` (default `0.45`), `FACE_SERVICE_TIMEOUT_SECONDS` (default `15`).
- `S3_ENDPOINT` (mis. `http://10.100.1.11:3900`), `S3_REGION` (default `garage`), `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` — semua kosong = foto tidak disimpan; terisi sebagian = server menolak start. `IMAGE_RETENTION_DAYS` (default `30`), `IMAGE_REVIEW_RETENTION_DAYS` (default `90`); `0` = tanpa batas.
- `DEVICE_TOKEN` (wajib, ≥ 32 karakter; bearer + kunci HMAC perangkat).
- `CSRF_SECRET` (wajib, ≥ 32 karakter).
- Bootstrap akun (saat belum ada administrator aktif): `BOOTSTRAP_ADMIN_USERNAME`, `BOOTSTRAP_ADMIN_PASSWORD`, opsional `BOOTSTRAP_LECTURER_USERNAME`, `BOOTSTRAP_LECTURER_PASSWORD`, `BOOTSTRAP_LECTURER_CLASSES`; `BOOTSTRAP_RESET_PASSWORDS=true` (development saja) menyetel ulang password akun bootstrap setiap start.
- `.env` menimpa environment (`godotenv.Overload`); terminal VS Code dengan `python.terminal.useEnvFile` dan `air` menyuntikkan isi `.env` lama, jadi dengan `Load` biasa perubahan `.env` dulu tidak terbaca.

Face service Python membaca konfigurasinya sendiri (`services/face/src/config.py`: `DET_MODEL_PATH`, `REC_MODEL_PATH`, `CONF_THRESHOLD`, `SIM_THRESHOLD`, `API_PORT`, dst.) dan tidak terhubung ke MongoDB.

## Menjalankan

```powershell
# Terminal 1 — face service (services/face, lihat services/face/README.md)
conda activate face_rec          # Python 3.10–3.12 (3.15 gagal memasang numpy/onnxruntime)
uvicorn api:app --host 127.0.0.1 --port 8000

# Terminal 2 — backend Go (root proyek)
copy .env.example .env   # isi secret
go vet ./...
go test ./...
go run ./cmd/server      # atau: air
```

Buka `http://localhost:8080`. Tidak ada akun demo bawaan. Session store in-memory; restart Go akan logout semua sesi.

Test otomatis: `go test ./...` menjalankan unit test integrasi face service (server tiruan dengan skema `services/face/api.py`), logika pencocokan, logika foto, klien S3 (vektor uji resmi AWS SigV4 + S3 tiruan), validasi akun dosen, dan render fragment (tombol hapus hanya untuk administrator). Belum ada test yang membutuhkan MongoDB atau Garage asli.

Log startup yang perlu dicek: `.env dimuat …`, `development mode: password staf minimal 4 karakter` (non-production), `connected to MongoDB at … (database: …)`, `bootstrap …`, dan status penyimpanan foto (`image storage: bucket "…" siap`, `… PERINGATAN …`, atau `… nonaktif`). `.env` dibaca saat start, jadi restart server setelah mengubahnya.

Untuk menguji alur scan tanpa perangkat keras, pakai klien uji Python di `docs/devices.md` §6 (membutuhkan face service yang berjalan). Dengan perangkat keras, ikuti uji bertahap `docs/devices.md` §2.5 (perintah Serial `cam`, `ping`, `scan`).

## Batasan dan Risiko yang Harus Diingat

1. **Foto wajah adalah data biometrik.** Bucket Garage harus privat; gunakan kunci khusus bucket (`garage key create methics-app`), bukan `admin_token` atau akun root server. Retensi foto scan diatur lewat `IMAGE_*_RETENTION_DAYS`; menghapus mahasiswa ikut menghapus foto enrollment dan foto scannya. Pastikan ada persetujuan mahasiswa (UU PDP).
2. **Kredensial perangkat tertulis di firmware.** Sketch ESP8266 (dulu `devices/esp8266/esp8266_ping/`, kini `devices/esp8266_ping/`) memuat SSID, password Wi-Fi, dan `DEVICE_TOKEN`; versi lamanya **sudah ter-commit** (commit `9af21ce`), sehingga token dan password Wi-Fi ada di riwayat Git: **ganti `DEVICE_TOKEN`** dan password Wi-Fi. `esp32cam_stream` juga menulis Wi-Fi di kode — kosongkan sebelum commit. `esp32_scanner` sudah memakai `secrets.h` yang di-gitignore (`docs/devices.md` §4).
3. **Ping & health dapat membuat perangkat baru.** `/api/v1/devices/ping` (tanpa token di non-production) dan `/api/v1/health?device_id=` (tanpa token di semua mode) melakukan upsert ke `devices`. ID yang terbentuk dengan cara ini lolos pengecekan "perangkat terdaftar" pada scan. Ping tanpa `device_id` juga selalu menandai `ESP32-GED-A1`.
4. **Login/session dan CSRF sudah dipasang, tetapi perlu deployment hardening.** Production memerlukan secret acak ≥ 32 karakter, TLS, session store bersama untuk multi-instance, dan pengelolaan/rotasi akun staf. HSTS belum dipasang.
5. **Mahasiswa tanpa login adalah keputusan produk saat ini.** Jangan menambahkan kembali `STUDENT_TOKEN`, opsi role mahasiswa, atau akses dashboard mandiri tanpa keputusan eksplisit baru.
6. **Liveness belum benar-benar diukur.** `liveness_passed` berasal dari perangkat dan hanya integritas pesannya dilindungi HMAC. HMAC tidak mencegah foto/video replay bila perangkat mengklaim liveness palsu, dan belum ada nonce anti-replay.
7. **Ambang cosine `0.45` belum dikalibrasi**; ukur false acceptance/rejection dengan data kampus sebelum produksi. Pencocokan memindai semua template (1:N) di Go setiap scan; Go lalu memeriksa keanggotaan kelas. Mengganti model recognition membuat template lama tidak sebanding → enrollment ulang.
8. Belum ada secret per perangkat, rotasi/revokasi token, UI untuk mengubah kelas/reset password/menonaktifkan akun staf, audit enrollment, alur persetujuan (consent), atau enkripsi backup. Penghapusan biometrik tersedia lewat hapus mahasiswa; retensi foto scan otomatis.
9. **Batas "hari ini" mengikuti zona waktu server.** Jalankan server dengan zona waktu kampus (WITA, `Asia/Makassar`). `scanned_at` wajib RFC3339 UTC agar perbandingan string benar.
10. **URI MongoDB dicetak ke log saat startup**, termasuk password bila URI memuatnya.
11. **Index `users.username` case-sensitive**, sedangkan login case-insensitive; hindari akun yang hanya berbeda huruf besar/kecil.
12. **`class_students` belum dibaca query**; keanggotaan kelas efektif adalah `students.class_id` (satu kelas per mahasiswa).
13. Profil mahasiswa tidak otomatis memiliki template wajah. Jangan menyimpulkan verifikasi wajah aktif hanya karena profil mahasiswa ada.
14. Google Fonts dan logo institusi masih dimuat dari internet; untuk lingkungan tertutup, vendor aset tersebut lalu sesuaikan CSP. Jangan menambahkan inline script (CSP `script-src 'self'`).
15. **Opsi filter kelas masih statis** di `views/pages/mahasiswa.html` dan `views/pages/logs.html`. Dialog pendaftaran sudah memakai opsi ber-scope dari server (`ClassRows`).
16. **Port RPC Garage (3901) terbuka di semua interface** pada `garage.toml` pengembangan, dan `rpc_secret`-nya pernah dibagikan di luar server. Batasi port 3901 dengan firewall atau ganti `rpc_secret`.
17. **Sisa mahasiswa contoh dari seed lama.** Seed tidak lagi membuat `IK24001`–`IK24005`, tetapi dokumen yang sudah terlanjur ada di MongoDB tidak dihapus otomatis; hapus sekali lewat halaman Mahasiswa. NIM berformat lama itu juga tidak lolos validasi 9 digit bila didaftarkan ulang.
18. **`BOOTSTRAP_RESET_PASSWORDS=true` menimpa password akun bootstrap setiap start** dan membuat ulang akun dosen bootstrap yang dihapus lewat `/akses`. Matikan setelah login berhasil.
19. **Face service memproses request satu per satu.** `api.py` menjalankan inferensi di dalam `async def`, sehingga enrollment 6 foto menahan scan lain sesaat. Untuk banyak perangkat jalankan beberapa worker (`--workers`) atau pindahkan inferensi ke thread pool.
20. **`python main.py` default mendengarkan di `0.0.0.0`.** Tanpa `--host 127.0.0.1`, face service tanpa autentikasi terbuka ke jaringan.
21. **"Hadir" dari konsensus frame bukan anti-spoof.** Perangkat mengirim `liveness_passed=false`; Go mencatat Hadir bila wajah yang sama cocok di ≥ `SCAN_MIN_MATCH_FRAMES` frame dalam ±2 detik. Foto cetak atau layar HP di depan kamera tetap bisa lolos. Tambahkan liveness nyata sebelum produksi, lalu naikkan `SCAN_MIN_MATCH_FRAMES` di atas jumlah frame perangkat agar konsensus saja tidak cukup.
22. **Perangkat kelas otomatis (`CLASS_ID` kosong) mencatat kehadiran di kelas mahasiswa mana pun**, tanpa jadwal ruangan; event wajah tak dikenal disimpan dengan `class_id` kosong (hanya terlihat administrator).

## Langkah Lanjutan yang Disarankan

1. Uji penyimpanan foto ke Garage dari mesin pengembang (cek log startup dan `/api/v1/health`), lalu tambahkan enrollment ulang otomatis dari foto enrollment yang tersimpan.
2. Rotasi `DEVICE_TOKEN` dan password Wi-Fi yang sudah ter-commit; pindahkan rahasia firmware ke `secrets.h`.
3. Uji end-to-end dengan face service asli: enrollment beberapa mahasiswa (foto bersetuju), lalu scan cocok/tidak cocok lewat klien uji di `docs/devices.md` §6 atau perangkat ESP32 (`docs/devices.md` §2.5); kalibrasi `FACE_SIM_THRESHOLD`. Tambahkan pemeriksaan Bearer token di `services/face/api.py`.
4. Batasi ping/health agar hanya memperbarui perangkat terdaftar (tanpa upsert), dan wajibkan token di semua mode.
5. Lengkapi administrasi akun staf di `/akses`: ubah kelas dosen, reset password, nonaktifkan akun; akun login mahasiswa tetap tidak dibuat.
6. Tambahkan CRUD kelas/perangkat dan ganti opsi kelas hardcoded di view dengan data dari server.
7. Uji firmware `esp32_scanner` + `esp32cam_uart` di perangkat keras (wiring `docs/devices.md` §2.1), lalu lanjutkan: TLS, secret per perangkat, `CLASS_ID` dari jadwal, dan liveness/anti-spoof tervalidasi.
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
- `docs/devices.md` — firmware ESP32 + ESP32-CAM (wiring, UART, upload, uji bertahap), ESP8266, protokol ping/scan, klien uji.
- `docs/face-service.md` — integrasi face service Python dan penyimpanan foto (S3 Garage).
- `docs/diagram.excalidraw` — diagram tahapan proyek, arsitektur, alur enrollment & scan.
- `docs/SRS.md` — kebutuhan fungsional & non-fungsional dengan status implementasi (dirawat terpisah; bisa tertinggal dari dokumen lain).
- `.env.example` — templat konfigurasi.

Kode: mulai dari `cmd/server/main.go` untuk wiring server, lalu `internal/controller`, `internal/api`, dan `internal/store` sesuai alur yang akan dikerjakan.
