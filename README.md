![svc by algorithmics](https://proxy-assets.etherthink.xyz/algorithmics/methiccs.webp)
# Algorithmics Face Recognation

Sistem absensi mahasiswa berbasis pengenalan wajah untuk Institut Teknologi
Bacharuddin Jusuf Habibie (ITBJH).

Satu aplikasi Go (halaman server-rendered + htmx dan API JSON) dengan MongoDB,
perangkat ESP8266/ESP32 sebagai pemindai, face service Python internal
untuk deteksi wajah & embedding, dan (opsional) object storage S3 Garage untuk
foto wajah.

Untuk orientasi cepat AI/kontributor berikutnya, mulai dari
[HANDOFF.md](HANDOFF.md): status, keputusan, batasan, dan prioritas lanjutan.

## Status singkat

| Komponen | Status |
| --- | --- |
| Backend Go + dashboard staf (login, ringkasan, mahasiswa, kelas, laporan, log, akses) | berjalan |
| Administrasi: tambah/hapus mahasiswa, buat/hapus akun dosen | berjalan (administrator) |
| MongoDB (index + seed kelas/perangkat otomatis) | berjalan |
| Firmware perangkat absensi ESP32 + ESP32-CAM (UART) (`devices/esp32_scanner`, `devices/esp32cam_uart`) | ada; lolos kompilasi, **belum diuji di perangkat keras** |
| Firmware heartbeat ESP8266 (`devices/esp8266_ping`) | ada (uji koneksi) |
| Face service Python (`services/face`, YOLO-FaceV2 + ArcFace) | ada; terhubung ke Go untuk enrollment & scan ([docs/face-service.md](docs/face-service.md)) |
| Penyimpanan foto wajah di Garage (S3) + retensi otomatis | ada, opsional (`S3_*`); tautan foto di halaman Log |
| Test otomatis | unit test (`go test ./...`: face service, pencocokan, foto, klien S3, validasi akun dosen, render fragment); belum ada test MongoDB |

## Dokumentasi

| Dokumen | Isi |
| --- | --- |
| [docs/README.md](docs/README.md) | indeks seluruh dokumentasi |
| [HANDOFF.md](HANDOFF.md) | status, keputusan, risiko, langkah lanjutan |
| [API.md](API.md) | kontrak API JSON, fragment `/partials/*`, format error, HMAC perangkat |
| [docs/architecture.md](docs/architecture.md) | arsitektur, batas kepercayaan, ADR |
| [docs/code-guide.md](docs/code-guide.md) | penjelasan kode per lapisan dan resep tugas umum |
| [docs/database.md](docs/database.md) | koleksi MongoDB, index, seed, waktu, backup |
| [docs/devices.md](docs/devices.md) | firmware ESP32 + ESP32-CAM (wiring, UART, upload, uji bertahap), ESP8266, protokol ping & scan, klien uji |
| [docs/face-service.md](docs/face-service.md) | integrasi face service Python dan penyimpanan foto (Garage) |
| [docs/diagram.excalidraw](docs/diagram.excalidraw) | diagram tahapan proyek, arsitektur, alur enrollment & scan |
| [docs/SRS.md](docs/SRS.md) | kebutuhan fungsional & non-fungsional |

## Struktur proyek

```text
cmd/server/main.go        entry point: env, routing, middleware, bootstrap akun, shutdown
internal/api/             endpoint JSON, query ber-scope role, logika domain,
                          klien face service (face.go), foto wajah (images.go),
                          fitur administrator (admin.go)
internal/storage/         klien S3 untuk Garage (pustaka standar, SigV4)
internal/controller/      halaman (web.go), fragment htmx (partials.go, admin_partials.go), login (auth.go)
internal/model/           view model halaman
internal/security/        session, principal, CSRF, rate limit login, CSP, recover
internal/store/           koneksi MongoDB, index, seed, akun staf
views/                    template html/template (layouts, pages, partials, fragments)
static/                   CSS, JS, htmx & Lucide yang di-vendor lokal
devices/                  firmware Arduino: esp32_scanner (perangkat absensi), esp32cam_uart (kamera),
                          sketch uji layar/sensor, stream kamera, dan heartbeat ESP8266
services/face/            face service Python (FastAPI: deteksi wajah + embedding ArcFace)
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

`.env` dimuat otomatis saat startup (`godotenv.Overload`), baik dengan `go run`
maupun `air`, dan **menimpa** variabel environment yang sama. Ini disengaja:
terminal VS Code (`python.terminal.useEnvFile`) dan `air` menyuntikkan isi
`.env` saat dibuka, sehingga tanpa Overload nilai lama akan menang. Untuk
memakai nilai dari environment, hapus barisnya dari `.env`. Simpan `.env`
tanpa BOM (lihat [code-guide §8](docs/code-guide.md)).

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
| `/mahasiswa` | roster, pendaftaran & penghapusan mahasiswa, enrollment wajah | staf (aksi tulis: administrator) |
| `/kelas` | kehadiran dan enrollment per kelas | staf |
| `/laporan` | rekap status scan hari ini, kehadiran per kelas, 50 scan terbaru, cetak | staf |
| `/logs` | log pemindaian seluruh waktu (200 terbaru) dengan filter status, kelas, pencarian, dan tautan foto capture | staf |
| `/akses` | buat & hapus akun dosen, daftar akun staf, matriks role | administrator |
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
| `MONGODB_DATABASE` | `attendance` | nama database (dibuat otomatis saat pertama ditulisi) |
| `FACE_SERVICE_URL` | `http://127.0.0.1:8000` | face service internal (`services/face/api.py`) |
| `FACE_SIM_THRESHOLD` | `0.45` | ambang cosine similarity untuk menyatakan wajah cocok |
| `FACE_SERVICE_TIMEOUT_SECONDS` | `15` | batas waktu panggilan `/detect` |
| `FACE_SERVICE_TOKEN` | kosong | bearer ke face service; ≥ 32 karakter di production |
| `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET` | kosong, `garage`, kosong | object storage S3 (Garage) untuk foto wajah; kosong = foto tidak disimpan |
| `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | kosong | kunci Garage khusus bucket (bukan kunci admin) |
| `IMAGE_RETENTION_DAYS` | `30` | masa simpan foto scan berstatus `Hadir`; `0` = tanpa batas |
| `IMAGE_REVIEW_RETENTION_DAYS` | `90` | masa simpan foto scan `Perlu ditinjau`/`Ditolak`; `0` = tanpa batas |
| `HTTP_ADDR` | `:8080` | alamat listen |
| `STATIC_DIR` | `static` | lokasi aset |
| `VIEW_DIR` | `views` | lokasi template |
| `BOOTSTRAP_ADMIN_USERNAME`, `BOOTSTRAP_ADMIN_PASSWORD` | — | wajib saat belum ada administrator aktif; password ≥ 12 karakter di production, ≥ 4 di development |
| `BOOTSTRAP_LECTURER_USERNAME`, `BOOTSTRAP_LECTURER_PASSWORD` | — | opsional; akun dosen dibuat bila username belum ada (akun dosen lain dibuat di `/akses`) |
| `BOOTSTRAP_LECTURER_CLASSES` | `IK24-A,IK24-B` | kelas untuk akun dosen bootstrap |
| `BOOTSTRAP_RESET_PASSWORDS` | `false` | hanya development: `true` = setiap start, password akun bootstrap admin & dosen disetel ulang ke nilai `.env` (dibuat bila belum ada) |

Server menolak start bila `CSRF_SECRET`/`DEVICE_TOKEN` kosong atau kurang dari
32 karakter, bila MongoDB tidak dapat dihubungi dalam 8 detik, atau bila
belum ada administrator dan variabel bootstrap admin tidak diisi, atau bila
`BOOTSTRAP_RESET_PASSWORDS=true` dipakai dengan `APP_ENV=production`. Hapus
secret bootstrap setelah akun terbentuk. Templat lengkap ada di
[.env.example](.env.example).

Ambang deteksi (`CONF_THRESHOLD`) dan pilihan model diatur di face service
Python; ambang pencocokan wajah (`FACE_SIM_THRESHOLD`) diatur di Go karena
pencocokan dilakukan oleh Go.

## Keamanan akun staf

- Tidak ada kredensial demo bawaan; akun staf dibuat dari `BOOTSTRAP_*`.
- Password disimpan dengan bcrypt; login dibatasi 10 percobaan per IP per
  5 menit.
- Session cookie HttpOnly/SameSite (`itbjh_session`, 8 jam, idle 30 menit)
  dan CSRF token (`itbjh_csrf`).
- Session store dan login limiter masih **in-memory**: restart mengakhiri
  semua sesi dan deployment multi-instance perlu store bersama.
- Mahasiswa adalah record roster/subjek absensi tanpa akun aplikasi.
- Administrator membuat akun dosen (username, nama, password, kelas yang
  diampu) dan menghapusnya di halaman `/akses`; akun administrator tidak bisa
  dihapus dari sana. Mengubah kelas dosen, reset password, dan menonaktifkan
  akun belum ada di UI; untuk sementara gunakan `mongosh` (contoh di
  [docs/database.md](docs/database.md) §6).
- Password minimal 12 karakter di production dan 4 karakter di development
  (agar akun uji seperti `admin`/`admin` bisa dipakai lokal).

## Perangkat

Perangkat absensi = **ESP32 utama** (`devices/esp32_scanner`: ultrasonik, layar
TFT, buzzer, Wi-Fi) + **ESP32-CAM** (`devices/esp32cam_uart`) yang tersambung
lewat kabel UART. Saat orang terdeteksi, ESP32 meminta foto ke kamera, lalu
mengirimnya ke `POST /api/v1/scans` (token + HMAC + jam NTP) dan menampilkan
hasilnya; tiap 15 detik ia juga mengirim heartbeat ke
`POST /api/v1/devices/ping`. Wiring, konfigurasi, urutan upload, dan uji
bertahap ada di [docs/devices.md](docs/devices.md) §2.

Wi-Fi dan `DEVICE_TOKEN` untuk ESP32 disimpan di
`devices/esp32_scanner/secrets.h` (salin dari `secrets.example.h`; di-gitignore).

> **Kredensial perangkat sudah ter-commit.** Versi lama sketch ESP8266 menulis
> password Wi-Fi dan `DEVICE_TOKEN` langsung di kode dan sudah masuk riwayat
> Git. Ganti token dan password itu ([docs/devices.md](docs/devices.md) §4).

## Face service

Face service ada di `services/face/` (FastAPI + ONNX Runtime: YOLO-FaceV2 untuk
deteksi, ArcFace untuk embedding 512 dimensi). Layanan ini **stateless**: Go
memanggil `POST /detect`, lalu Go sendiri yang menyimpan embedding ke MongoDB
(enrollment) dan mencocokkannya dengan cosine similarity (scan). Jadi dashboard
dan perangkat ESP32 cukup memakai satu API, yaitu Go.

Gunakan Python **3.10–3.12** (`services/face/environment.yml` memakai 3.10).
Python 3.15 belum didukung: `numpy<2.3` dan `onnxruntime` belum punya wheel
untuknya, sehingga `pip install` gagal membangun `numpy` dari source.

```powershell
# Terminal 1 — face service (dari services/face; lihat services/face/README.md)
conda activate face_rec            # atau venv Python 3.10–3.12
pip install -r requirements.txt    # sekali
uvicorn api:app --host 127.0.0.1 --port 8000

# Terminal 2 — backend Go
air
```

Detail alur, konfigurasi, dan catatan keamanan ada di
[docs/face-service.md](docs/face-service.md). Face service belum memiliki
autentikasi, jadi jalankan hanya di `127.0.0.1`/jaringan internal.

## Penyimpanan foto (Garage)

Opsional. Bila `S3_*` diisi, Go menyimpan foto enrollment dan foto capture
scan di bucket Garage privat; MongoDB hanya menyimpan kuncinya. Foto dipakai
untuk peninjauan, audit, dan enrollment ulang saat model diganti — pencocokan
tetap memakai embedding. Foto enrollment bisa dibuka dari tombol **Lihat
foto** di halaman Mahasiswa; foto scan dari kolom **Foto** di halaman Log.
Foto scan dihapus otomatis setelah
`IMAGE_RETENTION_DAYS`/`IMAGE_REVIEW_RETENTION_DAYS`. Bila Garage mati,
absensi tetap berjalan tanpa foto.

```bash
# di server Garage
garage bucket create methics-faces
garage key create methics-app
garage bucket allow --read --write methics-faces --key methics-app
```

Isi Key ID dan Secret ke `.env`, lalu restart server; log start harus
menulis `image storage: bucket "methics-faces" di … siap`. Detail di
[docs/face-service.md](docs/face-service.md) §8.

Embedding dan foto adalah data biometrik sensitif: aktifkan enkripsi
disk/backup, batasi akses database dan bucket, dokumentasikan persetujuan
(consent) dan retensi, serta sediakan penghapusan data.

## API

Detail di [API.md](API.md). Ringkasan:

- `GET /api/v1/health` — publik
- `GET /api/v1/dashboard`, `/classes`, `/reports` — session staf
- `POST /api/v1/students` — administrator + CSRF (JSON)
- `POST /api/v1/students/{nim}/face-template` — administrator + CSRF (multipart `image`)
- `DELETE /api/v1/students/{nim}` — administrator + CSRF (hapus mahasiswa + data wajah, foto, riwayat scan)
- `GET|POST /api/v1/staff`, `DELETE /api/v1/staff/{id}` — administrator + CSRF (akun dosen)
- `POST /api/v1/scans` — perangkat (multipart `device_id`, `class_id`, `captured_at`, `liveness_passed`, `image`; bearer + HMAC)
- `GET|POST /api/v1/devices/ping` — heartbeat perangkat
- `GET /api/v1/images/{key...}` — foto enrollment/scan dari Garage (session staf; dosen hanya kelasnya)

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
   Buka terminal di server MongoDB (`mongosh`), pindah ke database `admin`, dan buat akun aplikasi dengan izin `readWrite` hanya pada database aplikasi (nilai `MONGODB_DATABASE`; contoh di bawah memakai `attendance`):
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
- [ ] Ping/health tidak membuat perangkat baru secara otomatis (lihat [docs/devices.md](docs/devices.md) §7).
- [ ] Session store bersama bila lebih dari satu instance.
- [ ] Bila foto disimpan: bucket Garage privat, kunci khusus bucket (bukan `admin_token`), port RPC 3901 dibatasi, retensi disetujui institusi.

## Pemecahan masalah

| Gejala | Penyebab / solusi |
| --- | --- |
| `CSRF_SECRET must contain at least 32 characters` | `.env` belum terisi/terbaca (cek BOM, cek direktori kerja) |
| `initialize database (...): ... ping mongodb` | MongoDB tidak terjangkau; cek `MONGODB_URI`, firewall, `bindIp` |
| `first startup requires BOOTSTRAP_ADMIN_USERNAME ...` | belum ada administrator aktif; isi variabel bootstrap |
| Login `429` | lebih dari 10 percobaan dari IP yang sama dalam 5 menit; tunggu |
| Semua pengguna ter-logout | server di-restart (session in-memory) |
| "Verifikasi Python" offline | face service belum berjalan, model belum selesai dimuat, atau `FACE_SERVICE_URL` salah (default port `8000`) |
| Enrollment: "Tidak ada wajah yang terdeteksi" | foto buram/terlalu gelap/wajah terlalu kecil; coba foto wajah frontal yang jelas |
| Enrollment: "Wajah ini sudah terdaftar atas NIM …" | wajah mirip (≥ `FACE_SIM_THRESHOLD`) dengan template mahasiswa lain |
| Perangkat "offline" | tidak ada ping/scan dalam 2 menit; lihat [docs/devices.md](docs/devices.md) §8 |
| Log: `image storage: PERINGATAN bucket … belum bisa diakses` | Garage tidak terjangkau dari server Go, bucket belum dibuat, atau `S3_ACCESS_KEY_ID`/`S3_SECRET_ACCESS_KEY` salah |
| Kolom Foto di Log berisi `-` | penyimpanan foto nonaktif, unggah gagal (lihat log `image storage: upload … gagal`), scan dibuat sebelum `S3_*` diisi/server di-restart, atau foto sudah lewat retensi |
| `image storage: S3 endpoint, bucket, access key, dan secret wajib diisi` | `S3_*` terisi sebagian; lengkapi semua atau kosongkan semua |
| Perubahan `.env` tidak berpengaruh (mis. log masih `database: attendance`) | versi lama memakai `godotenv.Load`, yang kalah dari nilai lama di environment terminal; kini `Overload`. Hentikan `air` lalu jalankan lagi, dan cek baris log `.env dimuat` |
| Login gagal padahal `BOOTSTRAP_ADMIN_PASSWORD` sudah diubah | akun admin lama masih ada (bootstrap hanya berjalan bila belum ada administrator). Di development set `BOOTSTRAP_RESET_PASSWORDS=true` lalu restart, atau hapus akunnya dengan `mongosh` |
| Mahasiswa contoh lama (`IK24001`–`IK24005`) masih tampil | sisa seed versi lama di MongoDB; seed tidak membuatnya lagi, jadi hapus sekali lewat tombol hapus di halaman Mahasiswa |
| `NIM harus 9 digit angka` | NIM wajib 9 digit angka, mis. `241011001` |
| Python: `ModuleNotFoundError: onnxruntime` / `numpy` gagal dibangun | `uvicorn` dijalankan dari environment tanpa paket, atau Python 3.15; pakai conda/venv Python 3.10–3.12 lalu `pip install -r requirements.txt` |
| `air` tidak dikenali | tambahkan `%USERPROFILE%\go\bin` ke `PATH` |

## Lisensi

Lihat [LICENSE](LICENSE). Lisensi pustaka pihak ketiga yang di-vendor ada di
`static/js/vendor/*.LICENSE.txt`.
