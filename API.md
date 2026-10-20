# Kontrak Integrasi Algorithmics Face Recognation

Halaman server-rendered Go pada `/`, `/mahasiswa`, `/kelas`, `/laporan`, dan `/akses` **tidak** membaca `/api/v1`. Halaman dirender Go dari query ber-scope role, lalu diperbarui lewat htmx yang menukar fragment HTML dari prefix `/partials/*`. Prefix `/api/v1` adalah API JSON publik/integrasi yang dipakai perangkat ESP32 dan klien lain; keduanya memakai query dan aturan validasi yang sama di `internal/api/server.go`, jadi bentuk data tetap konsisten. Backend Go dan service Python ada di workspace ini. Go menjadi satu-satunya API yang diakses browser dan ESP32. Layanan Python untuk verifikasi wajah tetap internal di belakang Go. View memakai `html/template` di `views/`, CSS di `static/css/`, dan JavaScript di `static/js/`.

## Dashboard

`GET /api/v1/dashboard`

Administrator menerima ringkasan semua kelas dan dosen hanya kelas yang ditugaskan. Mahasiswa adalah record roster tanpa akun dan tidak dapat mengakses dashboard/API staf; absensi mereka dicatat melalui perangkat ESP32. Halaman dan API staf memakai session cookie HttpOnly/SameSite yang diterbitkan setelah login. Semua POST di jalur browser/staf dilindungi CSRF (`csrf_token` form atau header `X-CSRF-Token`). Query role dan header role tidak digunakan untuk autentikasi.

Contoh respons:

```json
{
  "summary": {
    "students": 5,
    "classes": 3,
    "verifiedScans": 3,
    "scansToReview": 0,
    "faceTemplates": 2
  },
  "classes": [
    { "id": "IK24-A", "name": "Ilmu Komputer A", "present": 2, "studentCount": 2 },
    { "id": "IK24-B", "name": "Ilmu Komputer B", "present": 1, "studentCount": 2 },
    { "id": "IK24-C", "name": "Ilmu Komputer C", "present": 0, "studentCount": 1 }
  ],
  "integrations": {
    "esp32": { "online": true, "lastSeen": "08:11" },
    "faceVerification": { "online": true, "provider": "Python service" }
  },
  "attendance": [
    {
      "student": { "name": "Alya Putri", "nim": "IK24001" },
      "class": { "id": "IK24-A", "name": "Ilmu Komputer A" },
      "scannedAt": "2026-10-16T08:02:00+07:00",
      "faceVerified": true,
      "faceEnrolled": true,
      "status": "Hadir",
      "deviceId": "ESP32-GED-A1"
    }
  ]
}
```

Endpoint mengembalikan `application/json`. **Halaman staf tidak memanggil endpoint ini** — halaman memuat fragment dari `/partials/*` (dashboard/kelas/laporan menyegarkan diri setiap 15 detik lewat atribut htmx). Endpoint `/api/v1` ditujukan untuk integrasi dan pengujian; ia menyertakan session cookie dan menolak akses tanpa session staf. GET health bersifat publik; scan ESP32 tidak menggunakan session.

## Kelas dan laporan

`GET /api/v1/classes`

Session staf wajib. Mengembalikan ringkasan dan daftar kelas dalam cakupan role; dosen hanya menerima kelas yang ditugaskan, administrator menerima semua kelas.

```json
{
  "summary": { "classes": 3, "students": 5, "enrolled": 2, "present": 1 },
  "classes": [
    { "id": "IK24-A", "name": "Ilmu Komputer A", "studentCount": 2, "enrolled": 1, "present": 1, "attendanceRate": 50 }
  ]
}
```

`GET /api/v1/reports`

Session staf wajib dan dibatasi role. Mengembalikan rekap status scan hari ini, komposisi status, kehadiran per kelas, dan 50 event scan terbaru.

```json
{
  "summary": { "verifiedScans": 1, "reviewScans": 0, "rejectedScans": 0, "students": 5, "present": 1, "attendanceRate": 20 },
  "statusBreakdown": [ { "status": "Hadir", "count": 1 } ],
  "classes": [ { "id": "IK24-A", "name": "Ilmu Komputer A", "studentCount": 2, "enrolled": 1, "present": 1, "attendanceRate": 50 } ],
  "events": [
    {
      "student": { "nim": "IK24001", "name": "Alya Putri" },
      "class": { "id": "IK24-A", "name": "Ilmu Komputer A" },
      "scannedAt": "2026-10-16T08:02:00+07:00",
      "status": "Hadir",
      "confidence": 0.91,
      "reviewReason": "",
      "deviceId": "ESP32-GED-A1"
    }
  ]
}
```

## Scan ESP32

`POST /api/v1/scans`

ESP32 mengirim multipart fields `device_id`, `class_id`, `captured_at` (RFC3339), `liveness_passed`, dan file `image` ke Go melalui TLS. Gunakan `Authorization: Bearer <device token>` dan HMAC-SHA256 request signature. String yang ditandatangani adalah enam baris berikut:

```text
<X-Device-Timestamp RFC3339>
<device_id>
<class_id>
<captured_at persis seperti field>
<liveness_passed lowercase true/false>
<hex SHA-256 dari byte image>
```

Kirim digest sebagai `X-Device-Signature` hex dan timestamp sebagai `X-Device-Timestamp`. Kunci HMAC adalah device token. Timestamp request diterima maksimal dua menit dari jam server; capture maksimal lima menit. Go memvalidasi perangkat dan kelas, lalu meneruskan gambar sementara ke service Python internal. Python mencocokkan template dan mengembalikan identitas kandidat serta skor jarak. Go mencatat scan hadir hanya bila wajah cocok, kandidat anggota kelas, dan flag liveness benar; selain itu hasil ditandai untuk ditinjau atau ditolak.

Penting: flag liveness saat ini adalah klaim perangkat yang ditandatangani, bukan deteksi biologis oleh backend. HMAC tidak membuktikan bahwa orang di gambar hidup. Perangkat perlu mekanisme anti-spoof tervalidasi sebelum hasil dipakai sebagai bukti kehadiran.

Hindari menyimpan gambar mentah bila embedding/template sudah mencukupi. Lindungi template biometrik dengan enkripsi, kontrol akses ketat, retensi terbatas, serta prosedur persetujuan dan penghapusan. Jangan mengirim gambar atau template wajah ke browser.

## Fragment htmx (antarmuka UI)

Halaman staf memuat dan menyegarkan datanya dari endpoint fragment berikut. Semuanya **wajib session staf**; respons berupa HTML (bukan JSON), dan bila sesi tidak valid mengembalikan `401` (htmx mengalihkan ke `/login`). Semua POST butuh CSRF (`X-CSRF-Token` header untuk htmx, atau field `csrf_token`).

| Endpoint | Guna | Akses |
| --- | --- | --- |
| `GET /partials/dashboard` | metrik ringkasan, kehadiran per kelas, status perangkat/layanan, riwayat scan | staf |
| `GET /partials/kelas` | ringkasan + tabel kelas | staf (ter-scope role) |
| `GET /partials/laporan` | rekap status, komposisi, kehadiran per kelas, riwayat scan | staf (ter-scope role) |
| `GET /partials/mahasiswa` | tabel roster; menerima query `q` dan `class` | staf (ter-scope role) |
| `GET /partials/mahasiswa/form` | form dialog pendaftaran mahasiswa | administrator |
| `POST /partials/mahasiswa/students` | tambah mahasiswa (form-urlencoded) | administrator |
| `POST /partials/mahasiswa/{nim}/face-template` | enrollment wajah (multipart `image`) | administrator |

Konvensi respons:

- **Sukses POST** membalas HTML dan header `HX-Trigger` berisi event (`toast`, `student-dialog-close`, `roster-changed`).
- **Gagal validasi** membalas **HTTP 200** dengan pesan inline di form/region, bukan JSON error.
- Ringkasan roster pada `/partials/mahasiswa` dikirim lewat out-of-band swap (`hx-swap-oob`).

## Role dan enrollment

- `administrator`: kelola roster, enrollment wajah, dan konfigurasi yang tersedia.
- `dosen`: lihat absensi dan laporan kelas yang ditugaskan.
- Mahasiswa adalah subjek absensi, bukan role login; tidak ada `STUDENT_TOKEN` atau endpoint akses mahasiswa.
- `POST /api/v1/students`: administrator mendaftarkan identitas mahasiswa dan kelas.
- Enrollment template wajah dilakukan lewat alur terautentikasi yang menghubungkan mahasiswa ke perangkat enrollment; jangan menerima template biometrik dari form publik.

Go menyediakan `GET /api/v1/health` dan `POST /api/v1/students/{nim}/face-template`; enrollment memakai multipart field `image`, session admin, dan CSRF. Service Python internal menyediakan `/health`, `/enroll`, dan `/verify`; jangan ekspos port Python ke jaringan publik. Service menyimpan embedding 128 dimensi, tidak menyimpan gambar mentah, dan saat ini tidak mengimplementasikan liveness detection. Endpoint Python mewajibkan bearer `FACE_SERVICE_TOKEN` bila secret dikonfigurasi. Login staf ada di `GET/POST /login`, logout di `POST /logout`; password di-hash bcrypt dan session diregenerasi setelah login.

Login tersedia pada `GET/POST /login`; `POST /logout` mengakhiri session dan wajib menyertakan CSRF token. Password staf di-hash menggunakan bcrypt. Middleware memuat user aktif dari session, mengambil daftar kelas dosen dari database, dan memasang principal pada request context. Pendaftaran dan enrollment hanya administrator. Request scan ESP32 (`POST /api/v1/scans`) sengaja tidak melewati session/CSRF browser: ia memakai bearer device token, HMAC-SHA256, dan timestamp. Server menolak start bila `CSRF_SECRET` atau `DEVICE_TOKEN` kosong atau kurang dari 32 karakter — di **semua mode**, bukan hanya production. Mode `APP_ENV=production` menambah kewajiban `FACE_SERVICE_TOKEN` (≥ 32 karakter) dan mengaktifkan cookie session `Secure`. Jangan meneruskan role dari query/header sebagai identitas.