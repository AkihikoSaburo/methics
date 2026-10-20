![svc by algorithmics](https://proxy-assets.etherthink.xyz/algorithmics/methiccs.webp)
# Algorithmics Face Recognation

Sistem absensi mahasiswa berbasis pengenalan wajah untuk Institut Teknologi Bacharuddin Jusuf Habibie.

Dashboard absensi dengan API Go, SQLite, dan service verifikasi wajah Python.

Untuk orientasi cepat AI/kontributor berikutnya, mulai dari [HANDOFF.md](HANDOFF.md); dokumen itu merangkum alur, arsitektur, kontrak, batasan, dan prioritas lanjutan.

## Dokumentasi

- [docs/README.md](docs/README.md) — indeks seluruh dokumentasi (mulai dari sini).
- [HANDOFF.md](HANDOFF.md) — status, keputusan, risiko, dan langkah lanjutan untuk kontributor berikutnya.
- [docs/architecture.md](docs/architecture.md) — arsitektur, batas kepercayaan, dan ADR.
- [docs/code-guide.md](docs/code-guide.md) — penjelasan kode per lapisan, alur request, model data, dan resep tugas umum.
- [docs/SRS.md](docs/SRS.md) — kerangka SRS (kebutuhan fungsional & non-fungsional) siap diisi.
- [API.md](API.md) — kontrak API JSON, endpoint fragment `/partials/*`, dan skema HMAC perangkat.

## Menjalankan Go

Perlu Go 1.23 atau lebih baru. Dari direktori project:

Aplikasi menolak start tanpa secret, jadi set dulu (rincian di bagian konfigurasi di bawah):

```powershell
go mod tidy
$env:CSRF_SECRET="ubah-ini-menjadi-secret-acak-32-karakter"
$env:DEVICE_TOKEN="ubah-ini-menjadi-token-acak-32-karakter"
$env:BOOTSTRAP_ADMIN_USERNAME="admin"
$env:BOOTSTRAP_ADMIN_PASSWORD="ubah-minimal-12-karakter"
go run ./cmd/server
```

Untuk pengembangan lokal, nilai-nilai itu tersedia di `.env` (di-gitignore) dan dimuat otomatis saat aplikasi mulai, baik dengan `go run` maupun `air`. Variabel yang sudah disetel di shell tetap lebih diprioritaskan daripada nilai di `.env`.

Buka `http://localhost:8080`. Halaman dirender server dengan `html/template` Go dan struktur MVC: model di `internal/model`, controller di `internal/controller` (termasuk fragment htmx di `internal/controller/partials.go`), view di `views`, dan aset di `static/css` serta `static/js`. Interaktivitas memakai htmx yang di-vendor lokal; halaman menukar fragment HTML dari `/partials/*` (bukan SPA/JSON render).

### Hot reload saat pengembangan (air)

Agar server dibangun ulang otomatis ketika kode Go atau template berubah:

```powershell
go install github.com/air-verse/air@latest   # cukup sekali
air
```

Konfigurasi ada di `.air.toml` (build ke `./tmp/main.exe`, memantau `.go` dan `.html`, serta memuat `.env`). Pastikan `%USERPROFILE%\go\bin` ada di `PATH` agar perintah `air` dikenali. Perubahan `static/css` dan `static/js` tidak memicu restart karena aset disajikan langsung dari disk — cukup refresh browser. Artifacts air masuk ke `tmp/` yang sudah diabaikan Git.

Route halaman:

- `/` untuk ringkasan absensi
- `/mahasiswa` untuk roster dan enrollment
- `/kelas` untuk ringkasan kehadiran dan enrollment per kelas
- `/laporan` untuk rekap scan, status kehadiran, dan riwayat
- `/akses` untuk matriks role (administrator saja)

Server menyiapkan `data/attendance.db` dan seed tiga kelas serta lima profil contoh. Seed ini tidak membuat template biometrik; enrollment wajah harus dilakukan secara eksplisit.

Secret wajib diisi (semua mode): `CSRF_SECRET` dan `DEVICE_TOKEN`, masing-masing acak minimal 32 karakter. Server menolak start bila kosong atau kurang dari 32 karakter. `APP_ENV=production` juga mewajibkan `FACE_SERVICE_TOKEN` (minimal 32 karakter) dan mengaktifkan cookie session Secure; gunakan TLS/reverse proxy.

Akun staf dibuat lewat environment, bukan kredensial demo bawaan. Pada database kosong, startup pertama memerlukan `BOOTSTRAP_ADMIN_USERNAME` dan `BOOTSTRAP_ADMIN_PASSWORD` (minimal 12 karakter). Akun dosen opsional memakai `BOOTSTRAP_LECTURER_USERNAME`, `BOOTSTRAP_LECTURER_PASSWORD`, dan `BOOTSTRAP_LECTURER_CLASSES` (default `IK24-A,IK24-B`). Hapus/unset secret bootstrap setelah akun dibuat.

Login staf tersedia di `/login` dan memakai session cookie HttpOnly/SameSite serta CSRF token. Password disimpan dengan bcrypt; login dibatasi 10 percobaan per IP per lima menit. Mahasiswa adalah record roster/subjek absensi tanpa akun aplikasi.

Session store dan login limiter saat ini in-memory, sehingga restart akan mengakhiri sesi dan deployment multi-instance perlu store/session limiter bersama.

Konfigurasi opsional: `HTTP_ADDR` (default `:8080`), `DATABASE_PATH` (default `data/attendance.db`), `STATIC_DIR` (default `static`), `VIEW_DIR` (default `views`), `FACE_SERVICE_URL` (default `http://127.0.0.1:8001`), `FACE_DISTANCE_THRESHOLD` (default `0.50`). Login dibatasi 10 percobaan per IP setiap 5 menit.

## Menjalankan face service

Perlu Python 3.10-3.12, CMake, dan toolchain C/C++ yang didukung `dlib`/`face-recognition`. Pada Windows, instalasi `face-recognition` dapat membutuhkan Visual Studio C++ Build Tools atau environment Linux/WSL.

```powershell
py -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install -r services/face/requirements.txt
$env:DATABASE_PATH = (Join-Path (Get-Location) 'data/attendance.db')
$env:FACE_SERVICE_TOKEN = 'ganti-dengan-secret-random-yang-sama-dengan-Go'
python -m uvicorn services.face.main:app --host 127.0.0.1 --port 8001
```

Atur `FACE_SERVICE_TOKEN` yang sama pada proses Go. Model hanya menyimpan embedding 128 dimensi, bukan gambar. Data biometrik tetap sensitif: aktifkan enkripsi disk/backup, batasi akses database, dokumentasikan consent dan retensi, serta sediakan penghapusan template.

## API

Lihat [API.md](API.md) untuk bentuk dashboard, enrollment, scan, autentikasi perangkat, dan format HMAC. Endpoint utama:

- `GET /api/v1/health`
- `GET /api/v1/dashboard`
- `POST /api/v1/students`
- `POST /api/v1/students/{nim}/face-template` (multipart `image`)
- `POST /api/v1/scans` (multipart `device_id`, `class_id`, `captured_at`, `liveness_passed`, `image`)

HMAC dan timestamp melindungi integritas request dan membantu menolak replay; keduanya tidak membuktikan bahwa wajah hidup. Implementasi ini belum melakukan liveness detection, dan tidak boleh dipakai untuk keputusan kehadiran berisiko tinggi tanpa mekanisme anti-spoof tervalidasi, persetujuan yang sesuai, serta peninjauan manusia untuk hasil tidak pasti.
