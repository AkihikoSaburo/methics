# SRS — Algorithmics Face Recognation

> **Status dokumen: KERANGKA SIAP-ISI.** Kebutuhan di bawah ini diangkat dari
> kode yang ada saat ini, lalu ditandai status implementasinya. Bagian yang
> hanya kamu yang tahu (target angka, regulasi, jumlah perangkat, jadwal)
> ditandai **`ISI:`** untuk dilengkapi.
>
> Penanda status: `[TERSEDIA]` sudah ada di kode · `[SEBAGIAN]` sebagian ·
> `[BELUM]` belum ada · `TBD` perlu keputusan.

## 0. Kendali Dokumen

| Item | Nilai |
| --- | --- |
| Nama produk | Algorithmics Face Recognation — sistem absensi mahasiswa ITBJH |
| Versi dokumen | ISI: 0.1 (draf) |
| Tanggal | ISI: |
| Penyusun | ISI: |
| Peninjau | ISI: |
| Status | ISI: Draf / Untuk ditinjau / Disetujui |

Riwayat revisi:

| Versi | Tanggal | Perubahan | Oleh |
| --- | --- | --- | --- |
| 0.1 | ISI: | Kerangka awal dari kode yang ada | ISI: |
| 0.6 | 2026-10-26 | Hapus mahasiswa (FR-STU-10), buat & hapus akun dosen (FR-AUTH-09, FR-AUTHZ-06 sebagian); matriks dihitung ulang | ISI: |
| 0.5 | 2026-10-26 | Firmware perangkat ESP32 + ESP32-CAM (UART); IF-HW-01/04 diperbarui, tambah IF-HW-05 | ISI: |
| 0.4 | 2026-10-26 | Penyimpanan foto wajah di object storage S3 (Garage) dengan retensi; tambah FR-BIO-11, IF-SW-06, NFR-REL-07; bug cakupan log (FR-LOG-01) diperbaiki; status privasi & test diperbarui; matriks dihitung ulang | ISI: |
| 0.3 | 2026-10-26 | Face service Python (YOLO-FaceV2 + ArcFace) terhubung ke Go; penyimpanan & pencocokan embedding di Go; status biometrik diperbarui; tambah FR-BIO-10 | ISI: |
| 0.2 | 2026-10-21 | Selaras dengan MongoDB; tambah log pemindaian (FR-LOG), heartbeat perangkat (FR-DEV-16); status biometrik diturunkan ke `[SEBAGIAN]` karena face service belum ada di repo; matriks dihitung ulang | ISI: |

## 1. Pendahuluan

### 1.1 Tujuan

Menetapkan kebutuhan perangkat lunak untuk sistem absensi mahasiswa berbasis
pengenalan wajah: halaman web untuk staf (administrator/dosen) dan API untuk
perangkat kamera (ESP32-CAM) beserta layanan verifikasi wajah.

### 1.2 Ruang lingkup

Cakupan produk:

- Dashboard web untuk staf akademik (absensi, kelas, mahasiswa, laporan, akses).
- API untuk perangkat ESP32 yang mengirim capture wajah.
- Layanan verifikasi wajah internal (embedding, pencocokan).
- Penyimpanan data akademik dan event absensi.

Di luar cakupan:

- Aplikasi mobile.
- Akun/login untuk mahasiswa.
- Perangkat keras kamera itu sendiri (hanya kontrak integrasinya).
- `ISI:` integrasi SIAKAD / sistem akademik kampus, bila direncanakan.

### 1.3 Definisi & singkatan

| Istilah | Arti |
| --- | --- |
| Staf | Pengguna ber-akun: `administrator` atau `dosen` |
| Mahasiswa | Subjek absensi; record akademik, **bukan** akun aplikasi |
| Enrollment | Pendaftaran template wajah seorang mahasiswa (1–6 pose) |
| Embedding | Vektor numerik fitur wajah (512 dimensi pada implementasi ini, ArcFace R50) |
| Template wajah | Embedding tersimpan untuk seorang mahasiswa (`encodings` 1–6 pose) |
| Scan | Satu pengiriman capture multi-frame dari perangkat dan keputusannya |
| Liveness | Bukti bahwa wajah berasal dari orang hidup, bukan foto |
| HMAC | Kode autentikasi pesan berbasis hash untuk menandatangani request |
| SRS | Software Requirements Specification |

### 1.4 Referensi

- `README.md` — cara menjalankan & konfigurasi.
- `API.md` — kontrak API & skema HMAC perangkat.
- `docs/architecture.md` — arsitektur & keputusan.
- `docs/code-guide.md` — penjelasan kode.
- `HANDOFF.md` — status & risiko.
- `docs/database.md` — skema MongoDB.
- `docs/devices.md` — firmware & protokol perangkat.
- `docs/face-service.md` — kontrak layanan wajah.
- `ISI:` standar/regulasi yang berlaku (mis. UU PDP), kebijakan kampus, dokumen akademik.

### 1.5 Ikhtisar dokumen

Bagian 2 menjelaskan konteks produk; Bagian 3 kebutuhan fungsional; Bagian 4
antarmuka eksternal; Bagian 5 kebutuhan non-fungsional; Bagian 6 batasan;
Bagian 7 matriks keterlacakan; Bagian 8 daftar hal yang harus diputuskan.

## 2. Deskripsi Umum

### 2.1 Perspektif produk

Sistem baru yang menggantikan pencatatan absensi manual. Terdiri dari:

```text
Perangkat ESP32-CAM ──► Backend Go ──► Layanan verifikasi wajah (Python, stateless)
Perangkat ESP8266 ───►     │
(heartbeat)                ├──► Basis data (MongoDB): template, absensi
                           └──► Object storage S3 (Garage): foto wajah (opsional)
Staf (browser) ──────► Backend Go (halaman + API)
```

Backend Go adalah satu-satunya pintu masuk publik; layanan wajah tidak
diekspos ke jaringan luar.

### 2.2 Fungsi utama

1. Autentikasi & otorisasi staf berbasis role.
2. Ringkasan absensi harian.
3. Pengelolaan data mahasiswa dan enrollment wajah.
4. Pencatatan absensi otomatis dari perangkat.
5. Pelaporan & riwayat scan.
6. Integrasi perangkat (ESP32) dengan autentikasi & tanda tangan request.

### 2.3 Aktor & karakteristik

| Aktor | Deskripsi | Hak akses |
| --- | --- | --- |
| Administrator | Staf pengelola sistem/roster | Semua kelas; kelola mahasiswa; enrollment; halaman akses |
| Dosen | Pengajar | Hanya kelas yang ditugaskan (baca) |
| Perangkat (ESP32/ESP8266) | Klien mesin | Endpoint scan (token + HMAC), heartbeat ping, dan health |
| Mahasiswa | Subjek absensi | Tidak punya akses aplikasi |
| `ISI:` Operator/IT | ISI: bila ada | ISI: |

### 2.4 Lingkungan operasi

- **Server:** `ISI:` OS & spesifikasi (dev saat ini Windows; produksi TBD). Zona waktu server menentukan batas "hari ini".
- **Basis data:** MongoDB (default `mongodb://10.100.1.11:27017`, database `attendance`). `ISI:` versi & topologi produksi (single node / replica set).
- **Perangkat:** ESP32 utama (HC-SR04, TFT, buzzer, Wi-Fi) + ESP32-CAM (UART atau mode uji Wi-Fi `esp32cam_wifi`) untuk scan multi-frame stream dan heartbeat (firmware ada, belum diuji di perangkat keras); ESP8266 untuk uji heartbeat. `ISI:` jumlah perangkat & lokasi.
- **Jaringan:** `ISI:` LAN kampus / VPN / TLS reverse proxy.
- **Object storage:** Garage (S3-compatible), mis. `http://10.100.1.11:3900`, bucket `methics-faces`; opsional.
- **Layanan wajah:** Python 3.10 + FastAPI + ONNX Runtime (YOLO-FaceV2, ArcFace R50), model dari Hugging Face `AkihikoSaburo/methics` (`services/face/`).

### 2.5 Batasan desain

| ID | Batasan |
| --- | --- |
| CO-01 | Frontend server-rendered (Go `html/template` + htmx); bukan SPA. |
| CO-02 | CSP `script-src 'self'`; tidak ada script inline maupun CDN skrip. |
| CO-03 | Aset skrip pihak ketiga di-vendor lokal dan versi-terpin. |
| CO-04 | Backend tidak melakukan inference wajah; itu tugas layanan Python. |
| CO-05 | Browser tidak pernah menerima template wajah; foto hanya dikirim ke staf berwenang lewat endpoint ber-otorisasi. |
| CO-06 | Autentikasi staf memakai session cookie; perangkat memakai token + HMAC. |

### 2.6 Asumsi & ketergantungan

| ID | Asumsi/Ketergantungan |
| --- | --- |
| AS-01 | Jam perangkat ESP32 tersinkron dengan server (toleransi ±2 menit untuk request, ±5 menit untuk capture). |
| AS-02 | Layanan wajah berjalan dan dapat dijangkau backend melalui jaringan internal. |
| AS-03 | `ISI:` ketersediaan jaringan di lokasi perangkat. |
| AS-04 | `ISI:` data mahasiswa (NIM, nama, kelas) berasal dari sumber tepercaya. |
| AS-05 | Object storage Garage dapat dijangkau backend bila penyimpanan foto diaktifkan; bila tidak, absensi tetap berjalan tanpa foto. |

## 3. Kebutuhan Fungsional

Kolom **Pri** = prioritas (W wajib, S sebaiknya, B bila memungkinkan).

### 3.1 Autentikasi & sesi

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-AUTH-01 | Staf dapat login dengan username & password | W | `[TERSEDIA]` |
| FR-AUTH-02 | Password disimpan ter-hash (bcrypt), tidak pernah plaintext | W | `[TERSEDIA]` |
| FR-AUTH-03 | Percobaan login dibatasi per IP (10 kali / 5 menit) | W | `[TERSEDIA]` |
| FR-AUTH-04 | Sesi diperbarui (regenerasi token) setelah login berhasil | W | `[TERSEDIA]` |
| FR-AUTH-05 | Staf dapat logout dan sesinya diakhiri | W | `[TERSEDIA]` |
| FR-AUTH-06 | Sesi kedaluwarsa → pengguna diarahkan ke login, tanpa kehilangan konteks halaman | S | `[TERSEDIA]` |
| FR-AUTH-07 | Sesi punya batas umur dan batas idle | W | `[TERSEDIA]` (8 jam / idle 30 menit) |
| FR-AUTH-08 | Semua aksi tulis staf dilindungi proteksi CSRF | W | `[TERSEDIA]` |
| FR-AUTH-09 | Administrator dapat mengelola akun staf (buat, ubah, nonaktifkan, reset password) | W | `[SEBAGIAN]` (buat & hapus akun dosen di `/akses`; ubah, nonaktifkan, reset password belum) |
| FR-AUTH-10 | Akun dapat dikunci setelah kegagalan berulang pada akun yang sama | S | `[BELUM]` |
| FR-AUTH-11 | `ISI:` kebijakan password (panjang, kompleksitas, masa berlaku) | — | TBD |

### 3.2 Otorisasi

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-AUTHZ-01 | Administrator dapat mengakses semua kelas | W | `[TERSEDIA]` |
| FR-AUTHZ-02 | Dosen hanya melihat data kelas yang ditugaskan padanya | W | `[TERSEDIA]` |
| FR-AUTHZ-03 | Data dibatasi di sisi server (query), bukan di klien | W | `[TERSEDIA]` |
| FR-AUTHZ-04 | Pendaftaran mahasiswa & enrollment wajah hanya administrator | W | `[TERSEDIA]` |
| FR-AUTHZ-05 | Halaman manajemen akses hanya administrator | W | `[TERSEDIA]` |
| FR-AUTHZ-06 | Administrator dapat mengatur penugasan kelas ke dosen melalui UI | S | `[SEBAGIAN]` (kelas dipilih saat membuat akun; belum bisa diubah) |
| FR-AUTHZ-07 | Aksi pengguna tercatat pada jejak audit | S | `[BELUM]` |

### 3.3 Dashboard

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-DASH-01 | Menampilkan ringkasan: jumlah mahasiswa, kelas, scan terverifikasi, perlu ditinjau, template wajah | W | `[TERSEDIA]` |
| FR-DASH-02 | Menampilkan kehadiran per kelas (hadir/total, persentase) hari ini | W | `[TERSEDIA]` |
| FR-DASH-03 | Menampilkan status perangkat terakhir (online bila < 2 menit) | S | `[TERSEDIA]` |
| FR-DASH-04 | Menampilkan status layanan verifikasi wajah | S | `[TERSEDIA]` |
| FR-DASH-05 | Menampilkan riwayat scan terbaru | S | `[TERSEDIA]` (8 baris) |
| FR-DASH-06 | Memperbarui data secara berkala tanpa reload penuh | S | `[TERSEDIA]` (15 detik) |
| FR-DASH-07 | Menampilkan tren/grafik historis | B | `[BELUM]` |

### 3.4 Kelas

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-CLASS-01 | Menampilkan ringkasan per kelas (jumlah mahasiswa, ter-enroll, hadir) | W | `[TERSEDIA]` |
| FR-CLASS-02 | Administrator dapat membuat/mengubah/menghapus kelas | W | `[BELUM]` |
| FR-CLASS-03 | Kelas memiliki kode unik dan nama | W | `[TERSEDIA]` |

### 3.5 Mahasiswa & enrollment

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-STU-01 | Menampilkan roster mahasiswa beserta kelas dan status wajah | W | `[TERSEDIA]` |
| FR-STU-02 | Mencari mahasiswa berdasarkan nama atau NIM | W | `[TERSEDIA]` |
| FR-STU-03 | Menyaring roster per kelas | S | `[TERSEDIA]` |
| FR-STU-04 | Administrator dapat menambah mahasiswa (nama, NIM, kelas) | W | `[TERSEDIA]` |
| FR-STU-05 | Kesalahan validasi ditampilkan pada form (inline), bukan halaman error | W | `[TERSEDIA]` |
| FR-STU-06 | NIM unik; penambahan dengan NIM ganda ditolak dengan pesan jelas | W | `[TERSEDIA]` |
| FR-STU-07 | Administrator dapat melakukan enrollment wajah mahasiswa | W | `[TERSEDIA]` (1–6 foto pose berbeda; validasi konsistensi pose & anti-duplikasi) |
| FR-STU-08 | Enrollment gagal (wajah tidak terdeteksi/service mati/pose inkonsisten) memberi pesan yang dapat ditindaklanjuti per foto | W | `[TERSEDIA]` |
| FR-STU-09 | Administrator dapat mengubah data mahasiswa | S | `[BELUM]` |
| FR-STU-10 | Administrator dapat menghapus/menonaktifkan mahasiswa | S | `[TERSEDIA]` (hapus beserta template wajah, foto, dan riwayat scan; nonaktifkan belum) |
| FR-STU-11 | Mahasiswa dapat diimpor secara massal (unggah CSV) | B | `[BELUM]` |
| FR-STU-12 | Riwayat absensi per mahasiswa | S | `[TERSEDIA]` (via laporan) |

### 3.6 Laporan

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-REP-01 | Rekap scan hari ini: terverifikasi, perlu ditinjau, ditolak | W | `[TERSEDIA]` |
| FR-REP-02 | Komposisi status (jumlah & persentase) | S | `[TERSEDIA]` |
| FR-REP-03 | Kehadiran per kelas (persentase) | W | `[TERSEDIA]` |
| FR-REP-04 | Riwayat scan terbaru (maks 50) dengan waktu, mahasiswa, kelas, perangkat, status | W | `[TERSEDIA]` |
| FR-REP-05 | Laporan dapat dicetak | S | `[TERSEDIA]` |
| FR-REP-06 | Filter rentang tanggal | W | `[BELUM]` |
| FR-REP-07 | Ekspor CSV/Excel | S | `[BELUM]` |
| FR-REP-08 | Rekap per mahasiswa (jumlah hadir/alpha dalam periode) | W | `[BELUM]` |
| FR-REP-09 | `ISI:` format/jadwal laporan resmi untuk akademik | — | TBD |

### 3.7 Perangkat & absensi otomatis

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-DEV-01 | Perangkat mengautentikasi dengan token + tanda tangan HMAC + timestamp | W | `[TERSEDIA]` |
| FR-DEV-02 | Request dengan timestamp kedaluwarsa ditolak | W | `[TERSEDIA]` |
| FR-DEV-03 | Hanya perangkat terdaftar yang diterima | W | `[SEBAGIAN]` (scan memeriksa `devices`, tetapi ping/health dapat mendaftarkan ID baru otomatis) |
| FR-DEV-04 | Kelas pada scan harus valid | W | `[TERSEDIA]` |
| FR-DEV-05 | Capture diverifikasi ke template wajah melalui layanan wajah | W | `[TERSEDIA]` |
| FR-DEV-06 | Frame scan divalidasi (1–6 frame, per frame ≤ 8 MB, total ≤ 16 MB; frame bukan gambar/rusak ditolak/dilewati) | W | `[TERSEDIA]` |
| FR-DEV-07 | Baris absensi dicatat dengan status dan alasan peninjauan | W | `[TERSEDIA]` |
| FR-DEV-08 | Status ditentukan berlapis: konsensus ≥ 2 frame cocok (`SCAN_MIN_MATCH_FRAMES`) atau liveness → Hadir; selain itu Perlu ditinjau / Ditolak | W | `[TERSEDIA]` |
| FR-DEV-09 | Waktu terakhir perangkat aktif diperbarui | S | `[TERSEDIA]` |
| FR-DEV-10 | Endpoint health publik untuk diagnostik | S | `[TERSEDIA]` |
| FR-DEV-11 | Secret unik per perangkat + rotasi/revokasi | W | `[BELUM]` |
| FR-DEV-12 | Proteksi replay (nonce sekali pakai) | W | `[BELUM]` |
| FR-DEV-13 | Deteksi liveness yang tervalidasi (anti-spoof) | W | `[BELUM]` |
| FR-DEV-14 | Administrator dapat mengelola daftar perangkat | S | `[BELUM]` |
| FR-DEV-15 | Perangkat menerima konfigurasi/pembaruan dari server | B | `[BELUM]` |
| FR-DEV-16 | Perangkat mengirim heartbeat berkala sehingga status "Terhubung" tampil di dashboard | S | `[TERSEDIA]` (`/api/v1/devices/ping` + firmware ESP32 `esp32_scanner`/ESP8266) |

### 3.8 Pengenalan wajah

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-BIO-01 | Enrollment memerlukan tepat satu wajah terdeteksi pada tiap foto (1–6 pose) | W | `[TERSEDIA]` |
| FR-BIO-02 | Disimpan sebagai embedding (bukan gambar mentah) | W | `[TERSEDIA]` (ArcFace 512-d, array `encodings`) |
| FR-BIO-03 | Pencocokan 1:N dengan ambang batas | W | `[TERSEDIA]` (cosine similarity di Go, default 0.45, pose terbaik) |
| FR-BIO-04 | Hasil cocok menghasilkan identitas kandidat + skor keyakinan | W | `[TERSEDIA]` |
| FR-BIO-05 | Ambang batas dikalibrasi terhadap data nyata (FRR/FAR) | W | `[BELUM]` |
| FR-BIO-06 | Pencocokan dapat dibatasi ke kandidat kelas terkait | S | `[BELUM]` |
| FR-BIO-07 | Enrollment tercatat untuk audit (siapa, kapan, hasil) | W | `[BELUM]` |
| FR-BIO-08 | Persetujuan (consent), masa retensi, dan prosedur penghapusan biometrik | W | `[SEBAGIAN]` (retensi otomatis foto scan 30/90 hari; penghapusan data biometrik per mahasiswa tersedia lewat hapus mahasiswa; consent belum) |
| FR-BIO-09 | Template biometrik dienkripsi saat disimpan | W | `[BELUM]` |
| FR-BIO-10 | Satu wajah tidak dapat didaftarkan pada dua NIM berbeda | S | `[TERSEDIA]` |
| FR-BIO-11 | Foto enrollment (semua pose) & scan disimpan di object storage privat dan hanya dapat dilihat staf berwenang (dosen: kelasnya) | S | `[TERSEDIA]` (Garage; tautan foto di halaman Mahasiswa & Log) |

### 3.9 API integrasi

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-API-01 | API JSON untuk dashboard/kelas/laporan, dibatasi role | W | `[TERSEDIA]` |
| FR-API-02 | API pendaftaran mahasiswa & enrollment (administrator) | W | `[TERSEDIA]` |
| FR-API-03 | API untuk perangkat (scan & health) | W | `[TERSEDIA]` |
| FR-API-04 | Versi API ditandai pada path | W | `[SEBAGIAN]` (`/api/v1`) |
| FR-API-05 | Dokumentasi mesin (OpenAPI/Swagger) | S | `[BELUM]` |
| FR-API-06 | Format error konsisten (kode + pesan) | W | `[TERSEDIA]` |
| FR-API-07 | Rate limit terpisah per jenis klien | S | `[SEBAGIAN]` (baru untuk login) |

### 3.10 Log pemindaian

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-LOG-01 | Menampilkan riwayat scan seluruh waktu (200 terbaru) dalam cakupan role | S | `[TERSEDIA]` |
| FR-LOG-02 | Menyaring log berdasarkan status dan kelas | S | `[TERSEDIA]` |
| FR-LOG-03 | Mencari log berdasarkan nama atau NIM mahasiswa | S | `[TERSEDIA]` |
| FR-LOG-04 | Ringkasan jumlah total/terverifikasi/ditinjau/ditolak seluruh waktu | S | `[TERSEDIA]` |
| FR-LOG-05 | Log dapat dicetak | B | `[TERSEDIA]` |
| FR-LOG-06 | Paginasi untuk melihat lebih dari 200 event | S | `[BELUM]` |

## 4. Kebutuhan Antarmuka Eksternal

### 4.1 Antarmuka pengguna

| ID | Kebutuhan | Status |
| --- | --- | --- |
| UI-01 | Bahasa Indonesia | `[TERSEDIA]` |
| UI-02 | Tata letak responsif + menu untuk layar kecil | `[TERSEDIA]` |
| UI-03 | Navigasi antar halaman tanpa reload penuh | `[TERSEDIA]` |
| UI-04 | Umpan balik aksi: notifikasi (toast) & dialog | `[TERSEDIA]` |
| UI-05 | Halaman dapat dicetak dengan tata letak khusus | `[TERSEDIA]` |
| UI-06 | Mudah diakses keyboard/screen reader | `[SEBAGIAN]` |
| UI-07 | `ISI:` pedoman merek/desain institusi | TBD |

### 4.2 Antarmuka perangkat lunak

| ID | Antarmuka | Detail | Status |
| --- | --- | --- | --- |
| IF-SW-01 | Backend ↔ Layanan wajah | HTTP internal; `GET /health`, `POST /embed` (scan), `POST /register` (enrollment 1–6 pose) → embedding 512-d | `[TERSEDIA]` (layanan belum memeriksa token) |
| IF-SW-02 | Backend ↔ Basis data | MongoDB (driver Go resmi v1.17) | `[TERSEDIA]` |
| IF-SW-05 | Penyimpanan template wajah | Go menulis `face_templates`; layanan wajah stateless | `[TERSEDIA]` |
| IF-SW-03 | Browser ↔ Backend | HTML/htmx + JSON `/api/v1` | `[TERSEDIA]` |
| IF-SW-06 | Backend ↔ Object storage | S3 API (SigV4, gaya path) ke Garage; `PUT/GET/DELETE` objek, `HEAD` bucket | `[TERSEDIA]` (opsional) |
| IF-SW-04 | `ISI:` Backend ↔ SIAKAD/SSO kampus | ISI: | TBD |

### 4.3 Antarmuka perangkat keras

| ID | Kebutuhan | Status |
| --- | --- | --- |
| IF-HW-01 | Perangkat kamera mengirim gambar multi-frame via HTTP multipart ke backend (`POST /api/v1/scans`) | `[SEBAGIAN]` (firmware `esp32_scanner` + `esp32cam_uart` ada dan lolos kompilasi; belum diuji di perangkat keras) |
| IF-HW-04 | Perangkat mengirim heartbeat HTTP berkala (< 2 menit) | `[TERSEDIA]` (ESP32 `esp32_scanner`, ESP8266) |
| IF-HW-05 | ESP32 utama ↔ ESP32-CAM melalui UART (`ON`/`CAP`/`OFF`, JPEG + CRC32) atau mode uji Wi-Fi (`esp32cam_wifi`) | `[SEBAGIAN]` (firmware ada; belum diuji di perangkat keras) |
| IF-HW-02 | `ISI:` spesifikasi minimum kamera/resolusi & pencahayaan | TBD |
| IF-HW-03 | `ISI:` kebutuhan daya & jaringan di lokasi | TBD |

### 4.4 Antarmuka komunikasi

| ID | Kebutuhan | Status |
| --- | --- | --- |
| IF-CM-01 | HTTPS/TLS di produksi | `[BELUM]` (belum ditegakkan kode) |
| IF-CM-02 | Header keamanan (CSP, nosniff, frame-ancestors, dsb.) | `[TERSEDIA]` |
| IF-CM-03 | `ISI:` perlu tidaknya jaringan terisolasi untuk perangkat | TBD |

## 5. Kebutuhan Non-Fungsional

### 5.1 Performa

| ID | Kebutuhan | Target | Status |
| --- | --- | --- | --- |
| NFR-PERF-01 | Batas ukuran gambar yang diterima | 8 MB | `[TERSEDIA]` |
| NFR-PERF-02 | Timeout panggilan ke layanan wajah | 15 detik (`FACE_SERVICE_TIMEOUT_SECONDS`); unggah foto 3 detik | `[TERSEDIA]` |
| NFR-PERF-03 | Waktu muat halaman dashboard | `ISI:` (mis. < 2 s pada LAN) | TBD |
| NFR-PERF-04 | Throughput scan bersamaan | `ISI:` (mis. N per menit) | TBD |
| NFR-PERF-05 | Latensi checkout absensi | `ISI:` | TBD |
| NFR-PERF-06 | Batas besar body request JSON | 1 MB | `[TERSEDIA]` |

### 5.2 Keamanan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-SEC-01 | CSP `script-src 'self'`, tanpa inline script | `[TERSEDIA]` |
| NFR-SEC-02 | Cookie sesi HttpOnly, SameSite=Lax, Secure di produksi | `[TERSEDIA]` |
| NFR-SEC-03 | Perlindungan CSRF untuk semua aksi tulis staf | `[TERSEDIA]` |
| NFR-SEC-04 | Secret wajib ≥ 32 karakter; server menolak start bila kurang | `[TERSEDIA]` |
| NFR-SEC-05 | Perbandingan token memakai waktu konstan | `[TERSEDIA]` |
| NFR-SEC-06 | HSTS | `[BELUM]` |
| NFR-SEC-07 | Session store & rate limiter bersama (multi-instance) | `[BELUM]` |
| NFR-SEC-08 | Jejak audit (login, enrollment, perubahan data) | `[BELUM]` |
| NFR-SEC-09 | Pemindaian dependensi / pembaruan berkala | `ISI:` |
| NFR-SEC-10 | Penanganan panic tanpa membocorkan detail internal | `[TERSEDIA]` |
| NFR-SEC-11 | Kredensial (Wi-Fi, token) tidak tertulis di kode firmware/repo | `[BELUM]` |
| NFR-SEC-12 | Endpoint perangkat tidak membuat perangkat baru tanpa autentikasi | `[BELUM]` |

### 5.3 Privasi & kepatuhan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-PRIV-01 | Template wajah tidak dikirim ke browser; foto hanya untuk staf berwenang | `[TERSEDIA]` |
| NFR-PRIV-02 | Foto tidak disimpan permanen | `[SEBAGIAN]` (foto scan berretensi 30/90 hari; foto enrollment disimpan selama template ada; dapat dimatikan dengan mengosongkan `S3_*`) |
| NFR-PRIV-03 | Persetujuan (consent) penggunaan biometrik | `[BELUM]` |
| NFR-PRIV-04 | Masa retensi & penghapusan data biometrik | `[TERSEDIA]` (retensi otomatis foto scan 30/90 hari di Garage; penghapusan mahasiswa oleh administrator menghapus template wajah, foto enrollment, foto scan, dan log scan terkait secara menyeluruh) |
| NFR-PRIV-05 | Enkripsi at-rest untuk template & backup | `[BELUM]` |
| NFR-PRIV-06 | `ISI:` dasar hukum & kebijakan privasi institusi (mis. UU PDP) | TBD |

### 5.4 Keandalan & ketersediaan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-REL-01 | Pematian anggun (graceful shutdown) | `[TERSEDIA]` (8 detik) |
| NFR-REL-02 | Skema & seed idempoten saat startup | `[TERSEDIA]` |
| NFR-REL-03 | Perilaku saat layanan wajah mati: scan tidak dicatat sebagai hadir | `[TERSEDIA]` |
| NFR-REL-07 | Object storage mati tidak menghentikan absensi maupun enrollment | `[TERSEDIA]` |
| NFR-REL-04 | Backup basis data terjadwal & teruji | `[BELUM]` |
| NFR-REL-05 | Target ketersediaan | `ISI:` (mis. 99% jam kerja) | TBD |
| NFR-REL-06 | Pemulihan setelah kegagalan (RTO/RPO) | `ISI:` | TBD |

### 5.5 Pemeliharaan & keterlacakan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-MAINT-01 | Satu sumber query/validasi untuk halaman dan API | `[TERSEDIA]` |
| NFR-MAINT-02 | Dokumentasi arsitektur, kode, API, handoff | `[TERSEDIA]` |
| NFR-MAINT-03 | Test otomatis (unit/integrasi) | `[SEBAGIAN]` (unit test face service mock, pencocokan & konsistensi pose, multi-frame scan & konsensus, S3 SigV4, validasi admin, render fragment; belum ada test MongoDB) |
| NFR-MAINT-04 | Logging terstruktur & observability | `[BELUM]` |
| NFR-MAINT-05 | Migrasi skema berversi | `[BELUM]` |
| NFR-MAINT-06 | CI (lint, build, test) | `[BELUM]` |

### 5.6 Portabilitas

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-PORT-01 | Dapat berjalan di Windows (dev) dan Linux (produksi) | `[SEBAGIAN]` |
| NFR-PORT-02 | Basis data produksi MongoDB dengan autentikasi & TLS | `[SEBAGIAN]` (koneksi ada; hardening manual sesuai README) |
| NFR-PORT-03 | Konfigurasi via environment, tanpa mengubah kode | `[TERSEDIA]` |

### 5.7 Kegunaan & aksesibilitas

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-USA-01 | Umpan balik jelas untuk aksi gagal/berhasil | `[TERSEDIA]` |
| NFR-USA-02 | Pesan kesalahan berbahasa Indonesia & dapat ditindaklanjuti | `[TERSEDIA]` |
| NFR-USA-03 | `ISI:` target perangkat & browser yang didukung | TBD |

## 6. Batasan & Kepatuhan

| ID | Batasan | Dampak |
| --- | --- | --- |
| CN-01 | Data biometrik termasuk data pribadi sensitif | Perlu dasar hukum, transparansi, kontrol akses, retensi |
| CN-02 | Liveness belum tervalidasi | Hasil absensi berisiko spoofing; wajib ditinjau manusia |
| CN-03 | Ambang pencocokan belum dikalibrasi | Risiko false accept/reject |
| CN-04 | Session & rate limit in-memory | Tidak aman untuk multi-instance |
| CN-05 | Satu token perangkat global | Bocor satu = bocor semua |
| CN-07 | Face service tanpa autentikasi | Harus berjalan di jaringan internal |
| CN-08 | Foto wajah tersimpan di object storage | Bucket privat, kunci khusus bucket, port RPC Garage dibatasi |
| CN-06 | `ISI:` kepatuhan lain (akreditasi, audit internal) | ISI: |

## 7. Matriks Keterlacakan (ringkas)

| Area | Jumlah FR | `[TERSEDIA]` | `[SEBAGIAN]` | `[BELUM]` | TBD |
| --- | --- | --- | --- | --- | --- |
| Autentikasi & sesi | 11 | 8 | 1 | 1 | 1 |
| Otorisasi | 7 | 5 | 1 | 1 | 0 |
| Dashboard | 7 | 6 | 0 | 1 | 0 |
| Kelas | 3 | 2 | 0 | 1 | 0 |
| Mahasiswa & enrollment | 12 | 10 | 0 | 2 | 0 |
| Laporan | 9 | 5 | 0 | 3 | 1 |
| Perangkat & absensi otomatis | 16 | 10 | 1 | 5 | 0 |
| Pengenalan wajah | 11 | 6 | 1 | 4 | 0 |
| API integrasi | 7 | 4 | 2 | 1 | 0 |
| Log pemindaian | 6 | 5 | 0 | 1 | 0 |
| **Total** | **89** | **61** | **6** | **20** | **2** |

> Angka di atas dihitung dari tabel Bagian 3 pada dokumen ini (revisi 0.7,
> 29 Oktober 2026); perbarui bila kebutuhan berubah.

## 8. Daftar Keputusan yang Diperlukan (TBD)

1. ~~Basis data produksi~~ — **diputuskan: MongoDB** (lihat ADR-6). Sisa: topologi (replica set?) dan kebijakan backup.
2. Target performa & ketersediaan (NFR-PERF-03/04, NFR-REL-05/06).
3. Regulasi privasi yang mengikat & kebijakan retensi biometrik (default teknis saat ini: foto scan 30 hari untuk `Hadir`, 90 hari lainnya — perlu disahkan).
4. Perlu integrasi SIAKAD/SSO kampus?
5. Jumlah perangkat & lokasi pemasangan.
6. Apakah mahasiswa perlu melihat rekap absensinya sendiri (media lain)?
7. SLA dukungan & siapa pemilik operasional sistem.
