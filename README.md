![svc by algorithmics](https://proxy-assets.etherthink.xyz/algorithmics/methiccs.webp)
# Algorithmics Face Recognation

Sistem absensi mahasiswa berbasis pengenalan wajah untuk Institut Teknologi
Bacharuddin Jusuf Habibie (ITBJH).

Satu aplikasi Go (halaman server-rendered + htmx dan API JSON) dengan MongoDB,
perangkat ESP8266/ESP32 sebagai pemindai, dan face service Python internal
untuk enrollment/verifikasi wajah.

Untuk orientasi cepat AI/kontributor berikutnya, mulai dari
[HANDOFF.md](HANDOFF.md): status, keputusan, batasan, dan prioritas lanjutan.

## Status singkat

| Komponen | Status |
| --- | --- |
| Backend Go + dashboard staf (login, ringkasan, mahasiswa, kelas, laporan, log, akses) | berjalan |
| MongoDB (index + seed otomatis) | berjalan |
| Firmware heartbeat ESP8266 (`devices/esp8266/esp8266_ping`) | ada |
| Firmware kamera ESP32-CAM untuk scan wajah | **belum ada** (kontrak API siap) |
| Face service Python (`services/face`) | **belum ada di repo** (kontrak di [docs/face-service.md](docs/face-service.md)) |
| Test otomatis | belum ada |

## Dokumentasi

| Dokumen | Isi |
| --- | --- |
| [docs/README.md](docs/README.md) | indeks seluruh dokumentasi |
| [HANDOFF.md](HANDOFF.md) | status, keputusan, risiko, langkah lanjutan |
| [API.md](API.md) | kontrak API JSON, fragment `/partials/*`, format error, HMAC perangkat |
| [docs/architecture.md](docs/architecture.md) | arsitektur, batas kepercayaan, ADR |
| [docs/code-guide.md](docs/code-guide.md) | penjelasan kode per lapisan dan resep tugas umum |
| [docs/database.md](docs/database.md) | koleksi MongoDB, index, seed, waktu, backup |
| [docs/devices.md](docs/devices.md) | firmware ESP8266, protokol ping & scan, klien uji |
| [docs/face-service.md](docs/face-service.md) | kontrak dan kerangka face service Python |
| [docs/SRS.md](docs/SRS.md) | kebutuhan fungsional & non-fungsional |

## Struktur proyek

```text
cmd/server/main.go        entry point: env, routing, middleware, bootstrap akun, shutdown
internal/api/             endpoint JSON, query ber-scope role, logika domain
internal/controller/      halaman (web.go), fragment htmx (partials.go), login (auth.go)
internal/model/           view model halaman
internal/security/        session, principal, CSRF, rate limit login, CSP, recover
internal/store/           koneksi MongoDB, index, seed, akun staf
views/                    template html/template (layouts, pages, partials, fragments)
static/                   CSS, JS, htmx & Lucide yang di-vendor lokal
devices/esp8266/          firmware heartbeat ESP8266 (Arduino)
services/face/            tempat face service Python (belum diimplementasi)
docs/                     dokumentasi
```

## Mulai cepat

Kebutuhan: **Go 1.23+** dan **MongoDB** yang dapat dijangkau (default
`mongodb://10.100.1.11:27017`).

```powershell
copy .env.example .env      # lalu isi CSRF_SECRET, DEVICE_TOKEN, BOOTSTRAP_ADMIN_PASSWORD, MONGODB_URI
go mod tidy
go run ./cmd/server
```

Buka `http://localhost:8080` dan login dengan akun bootstrap. Face service
yang belum berjalan hanya membuat status "Verifikasi Python" offline;
dashboard tetap bisa dipakai.

Nilai acak 32+ karakter bisa dibuat dengan `openssl rand -hex 32` atau di
PowerShell:

```powershell
-join ((48..57)+(65..90)+(97..122) | Get-Random -Count 40 | % {[char]$_})
```

`.env` dimuat otomatis saat startup (`godotenv`), baik dengan `go run` maupun
`air`. Variabel yang sudah disetel di shell **lebih diprioritaskan** daripada
`.env`. Simpan `.env` tanpa BOM (lihat [code-guide §8](docs/code-guide.md)).

### Hot reload (air)

```powershell
go install github.com/air-verse/air@latest   # cukup sekali
air
```

Konfigurasi ada di `.air.toml` (build ke `./tmp/main.exe`, memantau `.go` dan
`.html`, memuat `.env`). Pastikan `%USERPROFILE%\go\bin` ada di `PATH`.
Perubahan `static/css` dan `static/js` cukup di-refresh di browser karena aset
disajikan langsung dari disk. Artefak air masuk ke `tmp/` yang di-gitignore.

### Build binary

```powershell
go vet ./...
go build -o bin/methics.exe ./cmd/server
```

Binary membaca `views/` dan `static/` dari direktori kerja (atau dari
`VIEW_DIR`/`STATIC_DIR`), jadi salin kedua folder bersama binary saat deploy.

## Halaman

| Route | Isi | Akses |
| --- | --- | --- |
| `/` | ringkasan absensi hari ini, status perangkat & face service | staf |
| `/mahasiswa` | roster, pendaftaran mahasiswa, enrollment wajah | staf (aksi tulis: administrator) |
| `/kelas` | kehadiran dan enrollment per kelas | staf |
| `/laporan` | rekap status scan hari ini, kehadiran per kelas, 50 scan terbaru, cetak | staf |
| `/logs` | log pemindaian seluruh waktu (200 terbaru) dengan filter status, kelas, dan pencarian | staf |
| `/akses` | matriks role | administrator |
| `/login` | login staf | publik |

Dosen hanya melihat kelas yang ditugaskan (`users.class_ids`). Halaman
dirender Go dengan `html/template`; data dimuat dan disegarkan oleh htmx dari
`/partials/*` (bukan SPA/JSON render).

## Konfigurasi

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` mengaktifkan cookie `Secure`, mewajibkan `FACE_SERVICE_TOKEN`, dan mewajibkan token pada ping perangkat |
| `CSRF_SECRET` | — | **wajib**, ≥ 32 karakter (semua mode) |
| `DEVICE_TOKEN` | — | **wajib**, ≥ 32 karakter (semua mode); bearer + kunci HMAC perangkat |
| `MONGODB_URI` | `mongodb://10.100.1.11:27017` | URI MongoDB |
| `MONGODB_DATABASE` | `attendance` | nama database |
| `FACE_SERVICE_URL` | `http://127.0.0.1:8001` | face service internal |
| `FACE_SERVICE_TOKEN` | kosong | bearer ke face service; ≥ 32 karakter di production |
| `HTTP_ADDR` | `:8080` | alamat listen |
| `STATIC_DIR` | `static` | lokasi aset |
| `VIEW_DIR` | `views` | lokasi template |
| `BOOTSTRAP_ADMIN_USERNAME`, `BOOTSTRAP_ADMIN_PASSWORD` | — | wajib saat belum ada administrator aktif; password ≥ 12 karakter |
| `BOOTSTRAP_LECTURER_USERNAME`, `BOOTSTRAP_LECTURER_PASSWORD` | — | opsional; akun dosen dibuat bila username belum ada |
| `BOOTSTRAP_LECTURER_CLASSES` | `IK24-A,IK24-B` | kelas untuk akun dosen bootstrap |

Server menolak start bila `CSRF_SECRET`/`DEVICE_TOKEN` kosong atau kurang dari
32 karakter, bila MongoDB tidak dapat dihubungi dalam 8 detik, atau bila
belum ada administrator dan variabel bootstrap admin tidak diisi. Hapus
secret bootstrap setelah akun terbentuk. Templat lengkap ada di
[.env.example](.env.example).

`FACE_DISTANCE_THRESHOLD` **tidak** dibaca oleh Go; variabel itu milik face
service Python.

## Keamanan akun staf

- Tidak ada kredensial demo bawaan; akun staf dibuat dari `BOOTSTRAP_*`.
- Password disimpan dengan bcrypt; login dibatasi 10 percobaan per IP per
  5 menit.
- Session cookie HttpOnly/SameSite (`itbjh_session`, 8 jam, idle 30 menit)
  dan CSRF token (`itbjh_csrf`).
- Session store dan login limiter masih **in-memory**: restart mengakhiri
  semua sesi dan deployment multi-instance perlu store bersama.
- Mahasiswa adalah record roster/subjek absensi tanpa akun aplikasi.
- Belum ada UI manajemen akun/penugasan kelas; untuk sementara gunakan
  `mongosh` (contoh di [docs/database.md](docs/database.md) §6).

## Perangkat

Firmware heartbeat ESP8266 ada di `devices/esp8266/esp8266_ping/`. Ia mengirim
`POST /api/v1/devices/ping` setiap 15 detik sehingga dashboard menampilkan
perangkat "Terhubung". Cara konfigurasi, unggah, protokol scan, dan klien uji
Python ada di [docs/devices.md](docs/devices.md).

> **Jangan commit kredensial perangkat.** Sketch saat ini menulis password
> Wi-Fi dan `DEVICE_TOKEN` langsung di kode; pindahkan ke `secrets.h` yang
> di-gitignore sebelum folder `devices/` di-commit, lalu ganti token.

## Face service

Face service Python **belum ada di repo** (folder `services/face/` baru berisi
placeholder). Kontrak yang diharapkan Go (`GET /health`, `POST /enroll`,
`POST /verify`), variabel lingkungan, kerangka implementasi FastAPI, dan
kebutuhan instalasi (`face-recognition`/`dlib`, CMake, toolchain C/C++) ada di
[docs/face-service.md](docs/face-service.md).

Atur `FACE_SERVICE_TOKEN` yang sama pada Go dan Python. Service hanya
menyimpan embedding 128 dimensi, bukan gambar, tetapi embedding tetap data
biometrik sensitif: aktifkan enkripsi disk/backup, batasi akses database,
dokumentasikan consent dan retensi, serta sediakan penghapusan template.

## API

Detail di [API.md](API.md). Ringkasan:

- `GET /api/v1/health` — publik
- `GET /api/v1/dashboard`, `/classes`, `/reports` — session staf
- `POST /api/v1/students` — administrator + CSRF (JSON)
- `POST /api/v1/students/{nim}/face-template` — administrator + CSRF (multipart `image`)
- `POST /api/v1/scans` — perangkat (multipart `device_id`, `class_id`, `captured_at`, `liveness_passed`, `image`; bearer + HMAC)
- `GET|POST /api/v1/devices/ping` — heartbeat perangkat

HMAC dan timestamp melindungi integritas request dan membantu menolak replay;
keduanya tidak membuktikan bahwa wajah hidup. Implementasi ini belum melakukan
liveness detection, dan tidak boleh dipakai untuk keputusan kehadiran berisiko
tinggi tanpa mekanisme anti-spoof tervalidasi, persetujuan yang sesuai, serta
peninjauan manusia untuk hasil tidak pasti.

## Panduan Hardening MongoDB Menuju Production

Pada tahap pengembangan lokal (seperti di VMware lokal), MongoDB dapat berjalan
tanpa password (`authorization` nonaktif). Namun, **sebelum sistem
dipublikasikan ke production**, langkah-langkah hardening berikut wajib
diterapkan:

1. **Buat User Otentikasi Khusus Aplikasi:**
   Buka terminal di server MongoDB (`mongosh`), pindah ke database `admin`, dan buat akun aplikasi dengan izin `readWrite` hanya pada database `attendance`:
   ```javascript
   use admin
   db.createUser({
     user: "methics_app",
     pwd: "GantiDenganPasswordSangatKuatDanPanjang123!",
     roles: [
       { role: "readWrite", db: "attendance" }
     ]
   })
   ```

2. **Aktifkan Autentikasi di Konfigurasi MongoDB (`/etc/mongod.conf`):**
   Pastikan blok `security.authorization` disetel ke `enabled`:
   ```yaml
   security:
     authorization: enabled
   ```

3. **Batasi Network Binding & Firewall:**
   - Jangan membuka MongoDB ke internet publik (`0.0.0.0`). Setel `net.bindIp` hanya ke IP private/internal atau VPN:
     ```yaml
     net:
       port: 27017
       bindIp: 127.0.0.1,10.100.1.11
     ```
   - Gunakan firewall Ubuntu (`ufw`) agar hanya server backend Go (dan face service) yang diizinkan mengakses port 27017:
     ```bash
     sudo ufw allow from <IP_SERVER_GO> to any port 27017 proto tcp
     ```

4. **Perbarui Connection String di `.env` Aplikasi:**
   Sesuaikan URI di environment file server backend:
   ```env
   MONGODB_URI=mongodb://methics_app:GantiDenganPasswordSangatKuatDanPanjang123!@10.100.1.11:27017/attendance?authSource=admin
   MONGODB_DATABASE=attendance
   ```
   Catatan: URI dicetak ke log saat startup, jadi password ikut tercetak.
   Batasi akses ke log server atau samarkan URI sebelum produksi.

5. **Restart Service MongoDB:**
   ```bash
   sudo systemctl restart mongod
   ```

6. **Enkripsi In-Transit (TLS/SSL) & Backup:**
   - Bila MongoDB dan server Go berjalan pada server fisik yang terpisah melewati jaringan umum, aktifkan TLS pada MongoDB (`net.tls.mode: requireTLS`).
   - Jadwalkan backup berkala menggunakan utilitas `mongodump` dan simpan arsip backup secara aman (contoh perintah di [docs/database.md](docs/database.md) §7).

## Checklist sebelum production

- [ ] `APP_ENV=production`, semua secret acak ≥ 32 karakter, secret bootstrap dihapus.
- [ ] TLS di reverse proxy (HSTS belum dipasang oleh aplikasi).
- [ ] MongoDB di-hardening sesuai panduan di atas, backup terenkripsi terjadwal.
- [ ] Zona waktu server = zona waktu kampus (batas "hari ini" mengikuti jam server).
- [ ] Face service berjalan di jaringan internal dengan `FACE_SERVICE_TOKEN`.
- [ ] Kredensial perangkat tidak ada di Git; `DEVICE_TOKEN` diganti bila pernah bocor.
- [ ] Ping/health tidak membuat perangkat baru secara otomatis (lihat [docs/devices.md](docs/devices.md) §5).
- [ ] Session store bersama bila lebih dari satu instance.

## Pemecahan masalah

| Gejala | Penyebab / solusi |
| --- | --- |
| `CSRF_SECRET must contain at least 32 characters` | `.env` belum terisi/terbaca (cek BOM, cek direktori kerja) |
| `initialize database (...): ... ping mongodb` | MongoDB tidak terjangkau; cek `MONGODB_URI`, firewall, `bindIp` |
| `first startup requires BOOTSTRAP_ADMIN_USERNAME ...` | belum ada administrator aktif; isi variabel bootstrap |
| Login `429` | lebih dari 10 percobaan dari IP yang sama dalam 5 menit; tunggu |
| Semua pengguna ter-logout | server di-restart (session in-memory) |
| "Verifikasi Python" offline | face service belum berjalan atau `FACE_SERVICE_URL`/token salah |
| Perangkat "offline" | tidak ada ping/scan dalam 2 menit; lihat [docs/devices.md](docs/devices.md) §6 |
| `air` tidak dikenali | tambahkan `%USERPROFILE%\go\bin` ke `PATH` |

## Lisensi

Lihat [LICENSE](LICENSE). Lisensi pustaka pihak ketiga yang di-vendor ada di
`static/js/vendor/*.LICENSE.txt`.
