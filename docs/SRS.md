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
| Enrollment | Pendaftaran template wajah seorang mahasiswa |
| Embedding | Vektor numerik wajah (128 dimensi pada implementasi ini) |
| Template wajah | Embedding tersimpan untuk seorang mahasiswa |
| Scan | Satu pengiriman capture dari perangkat dan hasilnya |
| Liveness | Bukti bahwa wajah berasal dari orang hidup, bukan foto |
| HMAC | Kode autentikasi pesan berbasis hash untuk menandatangani request |
| SRS | Software Requirements Specification |

### 1.4 Referensi

- `README.md` — cara menjalankan & konfigurasi.
- `API.md` — kontrak API & skema HMAC perangkat.
- `docs/architecture.md` — arsitektur & keputusan.
- `docs/code-guide.md` — penjelasan kode.
- `HANDOFF.md` — status & risiko.
- `ISI:` standar/regulasi yang berlaku (mis. UU PDP), kebijakan kampus, dokumen akademik.

### 1.5 Ikhtisar dokumen

Bagian 2 menjelaskan konteks produk; Bagian 3 kebutuhan fungsional; Bagian 4
antarmuka eksternal; Bagian 5 kebutuhan non-fungsional; Bagian 6 batasan;
Bagian 7 matriks keterlacakan; Bagian 8 daftar hal yang harus diputuskan.

## 2. Deskripsi Umum

### 2.1 Perspektif produk

Sistem baru yang menggantikan pencatatan absensi manual. Terdiri dari:

```text
Perangkat ESP32-CAM ──► Backend Go ──► Layanan verifikasi wajah (Python)
                          └──► Basis data (SQLite)
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
| Perangkat (ESP32) | Klien mesin | Hanya endpoint scan/health dengan token perangkat |
| Mahasiswa | Subjek absensi | Tidak punya akses aplikasi |
| `ISI:` Operator/IT | ISI: bila ada | ISI: |

### 2.4 Lingkungan operasi

- **Server:** `ISI:` OS & spesifikasi (dev saat ini Windows; produksi TBD).
- **Basis data:** SQLite (implementasi sekarang). `ISI:` apakah produksi tetap SQLite atau Postgres.
- **Perangkat:** ESP32-CAM dengan modul jaringan. `ISI:` jumlah perangkat & lokasi.
- **Jaringan:** `ISI:` LAN kampus / VPN / TLS reverse proxy.
- **Layanan wajah:** Python 3.10–3.12 + `face-recognition`/dlib.

### 2.5 Batasan desain

| ID | Batasan |
| --- | --- |
| CO-01 | Frontend server-rendered (Go `html/template` + htmx); bukan SPA. |
| CO-02 | CSP `script-src 'self'`; tidak ada script inline maupun CDN skrip. |
| CO-03 | Aset skrip pihak ketiga di-vendor lokal dan versi-terpin. |
| CO-04 | Backend tidak melakukan inference wajah; itu tugas layanan Python. |
| CO-05 | Browser tidak pernah menerima gambar/template wajah. |
| CO-06 | Autentikasi staf memakai session cookie; perangkat memakai token + HMAC. |

### 2.6 Asumsi & ketergantungan

| ID | Asumsi/Ketergantungan |
| --- | --- |
| AS-01 | Jam perangkat ESP32 tersinkron dengan server (toleransi ±2 menit untuk request, ±5 menit untuk capture). |
| AS-02 | Layanan wajah berjalan dan dapat dijangkau backend. |
| AS-03 | `ISI:` ketersediaan jaringan di lokasi perangkat. |
| AS-04 | `ISI:` data mahasiswa (NIM, nama, kelas) berasal dari sumber tepercaya. |

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
| FR-AUTH-09 | Administrator dapat mengelola akun staf (buat, ubah, nonaktifkan, reset password) | W | `[BELUM]` |
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
| FR-AUTHZ-06 | Administrator dapat mengatur penugasan kelas ke dosen melalui UI | S | `[BELUM]` |
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
| FR-STU-07 | Administrator dapat melakukan enrollment wajah mahasiswa | W | `[TERSEDIA]` |
| FR-STU-08 | Enrollment gagal (wajah tidak terdeteksi/service mati) memberi pesan yang dapat ditindaklanjuti | W | `[TERSEDIA]` |
| FR-STU-09 | Administrator dapat mengubah data mahasiswa | S | `[BELUM]` |
| FR-STU-10 | Administrator dapat menghapus/menonaktifkan mahasiswa | S | `[BELUM]` |
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
| FR-DEV-03 | Hanya perangkat terdaftar yang diterima | W | `[TERSEDIA]` |
| FR-DEV-04 | Kelas pada scan harus valid | W | `[TERSEDIA]` |
| FR-DEV-05 | Capture diverifikasi ke template wajah melalui layanan wajah | W | `[TERSEDIA]` |
| FR-DEV-06 | Capture > 8 MB atau bukan gambar ditolak | W | `[TERSEDIA]` |
| FR-DEV-07 | Baris absensi dicatat dengan status dan alasan peninjauan | W | `[TERSEDIA]` |
| FR-DEV-08 | Status ditentukan berlapis: cocok + anggota kelas + liveness → Hadir; selain kondisi tertentu → Perlu ditinjau/Ditolak | W | `[TERSEDIA]` |
| FR-DEV-09 | Waktu terakhir perangkat aktif diperbarui | S | `[TERSEDIA]` |
| FR-DEV-10 | Endpoint health publik untuk diagnostik | S | `[TERSEDIA]` |
| FR-DEV-11 | Secret unik per perangkat + rotasi/revokasi | W | `[BELUM]` |
| FR-DEV-12 | Proteksi replay (nonce sekali pakai) | W | `[BELUM]` |
| FR-DEV-13 | Deteksi liveness yang tervalidasi (anti-spoof) | W | `[BELUM]` |
| FR-DEV-14 | Administrator dapat mengelola daftar perangkat | S | `[BELUM]` |
| FR-DEV-15 | Perangkat menerima konfigurasi/pembaruan dari server | B | `[BELUM]` |

### 3.8 Pengenalan wajah

| ID | Kebutuhan | Pri | Status |
| --- | --- | --- | --- |
| FR-BIO-01 | Enrollment memerlukan tepat satu wajah terdeteksi | W | `[TERSEDIA]` |
| FR-BIO-02 | Disimpan sebagai embedding (bukan gambar mentah) | W | `[TERSEDIA]` |
| FR-BIO-03 | Pencocokan 1:N dengan ambang batas jarak | W | `[TERSEDIA]` (default 0.50) |
| FR-BIO-04 | Hasil cocok menghasilkan identitas kandidat + skor keyakinan | W | `[TERSEDIA]` |
| FR-BIO-05 | Ambang batas dikalibrasi terhadap data nyata (FRR/FAR) | W | `[BELUM]` |
| FR-BIO-06 | Pencocokan dapat dibatasi ke kandidat kelas terkait | S | `[BELUM]` |
| FR-BIO-07 | Enrollment tercatat untuk audit (siapa, kapan, hasil) | W | `[BELUM]` |
| FR-BIO-08 | Persetujuan (consent), masa retensi, dan prosedur penghapusan biometrik | W | `[BELUM]` |
| FR-BIO-09 | Template biometrik dienkripsi saat disimpan | W | `[BELUM]` |

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
| IF-SW-01 | Backend ↔ Layanan wajah | HTTP internal; `/health`, `/enroll`, `/verify`; bearer `FACE_SERVICE_TOKEN` | `[TERSEDIA]` |
| IF-SW-02 | Backend ↔ Basis data | SQLite | `[TERSEDIA]` |
| IF-SW-03 | Browser ↔ Backend | HTML/htmx + JSON `/api/v1` | `[TERSEDIA]` |
| IF-SW-04 | `ISI:` Backend ↔ SIAKAD/SSO kampus | ISI: | TBD |

### 4.3 Antarmuka perangkat keras

| ID | Kebutuhan | Status |
| --- | --- | --- |
| IF-HW-01 | Perangkat kamera mengirim gambar via HTTP multipart ke backend | `[TERSEDIA]` |
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
| NFR-PERF-02 | Timeout panggilan ke layanan wajah | 8 detik | `[TERSEDIA]` |
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

### 5.3 Privasi & kepatuhan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-PRIV-01 | Gambar/template wajah tidak dikirim ke browser | `[TERSEDIA]` |
| NFR-PRIV-02 | Gambar mentah tidak disimpan permanen | `[TERSEDIA]` |
| NFR-PRIV-03 | Persetujuan (consent) penggunaan biometrik | `[BELUM]` |
| NFR-PRIV-04 | Masa retensi & penghapusan data biometrik | `[BELUM]` |
| NFR-PRIV-05 | Enkripsi at-rest untuk template & backup | `[BELUM]` |
| NFR-PRIV-06 | `ISI:` dasar hukum & kebijakan privasi institusi (mis. UU PDP) | TBD |

### 5.4 Keandalan & ketersediaan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-REL-01 | Pematian anggun (graceful shutdown) | `[TERSEDIA]` (8 detik) |
| NFR-REL-02 | Skema & seed idempoten saat startup | `[TERSEDIA]` |
| NFR-REL-03 | Perilaku saat layanan wajah mati: scan tidak dicatat sebagai hadir | `[TERSEDIA]` |
| NFR-REL-04 | Backup basis data terjadwal & teruji | `[BELUM]` |
| NFR-REL-05 | Target ketersediaan | `ISI:` (mis. 99% jam kerja) | TBD |
| NFR-REL-06 | Pemulihan setelah kegagalan (RTO/RPO) | `ISI:` | TBD |

### 5.5 Pemeliharaan & keterlacakan

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-MAINT-01 | Satu sumber query/validasi untuk halaman dan API | `[TERSEDIA]` |
| NFR-MAINT-02 | Dokumentasi arsitektur, kode, API, handoff | `[TERSEDIA]` |
| NFR-MAINT-03 | Test otomatis (unit/integrasi) | `[BELUM]` (dihapus pada code cleaning) |
| NFR-MAINT-04 | Logging terstruktur & observability | `[BELUM]` |
| NFR-MAINT-05 | Migrasi skema berversi | `[BELUM]` |
| NFR-MAINT-06 | CI (lint, build, test) | `[BELUM]` |

### 5.6 Portabilitas

| ID | Kebutuhan | Status |
| --- | --- | --- |
| NFR-PORT-01 | Dapat berjalan di Windows (dev) dan Linux (produksi) | `[SEBAGIAN]` |
| NFR-PORT-02 | Dukungan basis data produksi (SQLite/Postgres) | `ISI:` | TBD |
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
| CN-06 | `ISI:` kepatuhan lain (akreditasi, audit internal) | ISI: |

## 7. Matriks Keterlacakan (ringkas)

| Area | Jumlah FR | `[TERSEDIA]` | `[SEBAGIAN]` | `[BELUM]` |
| --- | --- | --- | --- | --- |
| Autentikasi & sesi | 11 | 8 | 0 | 2 (1 TBD) |
| Otorisasi | 7 | 5 | 0 | 2 |
| Dashboard | 7 | 6 | 0 | 1 |
| Kelas | 3 | 2 | 0 | 1 |
| Mahasiswa & enrollment | 12 | 8 | 0 | 4 |
| Laporan | 9 | 5 | 0 | 3 (1 TBD) |
| Perangkat | 15 | 10 | 0 | 5 |
| Pengenalan wajah | 9 | 4 | 0 | 5 |
| API | 7 | 5 | 1 | 1 |
| **Total** | **80** | **53** | **1** | **24 (+3 TBD)** |

> Angka di atas dihitung dari tabel Bagian 3 pada dokumen ini; perbarui bila
> kebutuhan berubah.

## 8. Daftar Keputusan yang Diperlukan (TBD)

1. Basis data produksi: tetap SQLite atau pindah ke Postgres?
2. Target performa & ketersediaan (NFR-PERF-03/04, NFR-REL-05/06).
3. Regulasi privasi yang mengikat & kebijakan retensi biometrik.
4. Perlu integrasi SIAKAD/SSO kampus?
5. Jumlah perangkat & lokasi pemasangan.
6. Apakah mahasiswa perlu melihat rekap absensinya sendiri (media lain)?
7. SLA dukungan & siapa pemilik operasional sistem.
