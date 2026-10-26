# Face Service (Python) dan Integrasinya dengan Go

> Status: **[TERSEDIA]**. Layanan ada di `services/face/` (FastAPI + ONNX
> Runtime: YOLO-FaceV2 untuk deteksi, ArcFace R50 untuk embedding 512-d,
> model diunduh dari Hugging Face `AkihikoSaburo/methics`). Cara menjalankan
> layanannya sendiri ada di `services/face/README.md`.

## 1. Pembagian tugas: satu API publik

```text
Browser / ESP32 ──► Go (satu-satunya API publik, :8080)
                     │  POST /detect (multipart image)
                     ├──────────────────────────────► Python face service (:8000, internal)
                     │◄──────────── {num_faces, faces[].embedding (512-d), confidence}
                     │
                     ├── simpan embedding  → MongoDB face_templates   (enrollment)
                     └── cosine similarity → bandingkan dengan face_templates (scan)
```

Face service bersifat **stateless**: ia tidak menyimpan apa pun dan tidak
terhubung ke MongoDB. Go yang:

- menyimpan template wajah (embedding) di koleksi `face_templates`,
- melakukan pencocokan 1:N (cosine similarity) saat scan,
- menentukan status `Hadir` / `Perlu ditinjau` / `Ditolak`.

Akibatnya perangkat ESP32 dan dashboard cukup berbicara dengan **satu** API
(Go). Face service tidak perlu dan tidak boleh diekspos ke jaringan publik.

Kode Go: `internal/api/face.go` (klien + pencocokan), dipanggil dari
`EnrollFace` dan `receiveScan` di `internal/api/server.go`. Foto wajah
opsional disimpan di Garage (S3), lihat §8.

## 2. Endpoint Python yang dipakai Go

| Endpoint | Dipakai untuk | Field/respons yang dibaca Go |
| --- | --- | --- |
| `GET /health` | status di dashboard & `/api/v1/health` | `model_loaded`, `det_model`, `rec_model` |
| `POST /detect` | enrollment dan scan | request: file `image`; respons: `num_faces`, `faces[].confidence`, `faces[].embedding`, `processing_time_ms` |

Endpoint lain (`/compare`, `/embed`, `/detect/visualize`, `/models*`) tidak
dipakai Go dan tetap bisa dipakai tim untuk eksperimen lewat Swagger
(`http://127.0.0.1:8000/docs`).

Status HTTP dari `/detect`:

| Status Python | Perlakuan Go |
| --- | --- |
| `200` | diproses |
| `400` (gambar tidak bisa di-decode) | `400 invalid_image` ke klien |
| lainnya / timeout / tidak terjangkau | `502 face_service_error`; scan **tidak** dicatat |

## 3. Enrollment (administrator)

`POST /partials/mahasiswa/{nim}/face-template` (UI) atau
`POST /api/v1/students/{nim}/face-template` (JSON):

1. Go mengirim foto ke `POST /detect`.
2. Wajib **tepat satu** wajah: 0 wajah → `422 no_face`, lebih dari satu →
   `422 multiple_faces`.
3. Embedding dibandingkan dengan template mahasiswa **lain**. Bila kemiripan
   ≥ ambang, enrollment ditolak (`409 face_already_enrolled`, menyebut NIM
   pemilik wajah) untuk mencegah satu wajah terdaftar di dua NIM (titip absen).
4. Embedding di-*upsert* ke `face_templates` (satu template per mahasiswa;
   enrollment ulang menimpa template lama).

Dokumen `face_templates`:

```json
{
  "student_id": "student-ik24001",
  "encoding": [0.0123, -0.0456, "… 512 angka …"],
  "dimension": 512,
  "detection_confidence": 0.93,
  "created_at": "2026-10-26T04:00:00Z",
  "updated_at": "2026-10-26T04:00:00Z"
}
```

## 4. Verifikasi saat scan (perangkat)

`POST /api/v1/scans` (setelah token, HMAC, timestamp, perangkat, dan kelas
lolos validasi):

1. Go mengirim capture ke `POST /detect`.
2. Wajah dengan `confidence` deteksi tertinggi dipakai (bila ada beberapa).
   Tidak ada wajah → `Ditolak` dengan alasan "Wajah tidak terdeteksi".
3. Embedding dibandingkan dengan **semua** template (1:N). Template dengan
   dimensi berbeda (mis. dari model lama) dilewati.
4. Skor terbaik ≥ ambang → kandidat cocok; `confidence` pada event = cosine
   similarity. Di bawah ambang → `Ditolak` dengan skor pada alasan.
5. Go lalu menerapkan aturan yang sama seperti sebelumnya: anggota kelas +
   `liveness_passed=true` → `Hadir`, selain itu `Perlu ditinjau`.

## 5. Konfigurasi

Sisi Go (`.env`):

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `FACE_SERVICE_URL` | `http://127.0.0.1:8000` | base URL face service (port default `api.py`) |
| `FACE_SIM_THRESHOLD` | `0.45` | ambang cosine similarity; samakan dengan `SIM_THRESHOLD` di Python |
| `FACE_SERVICE_TIMEOUT_SECONDS` | `15` | batas waktu satu panggilan `/detect` (health memakai 3 detik) |
| `FACE_SERVICE_TOKEN` | kosong | dikirim sebagai `Authorization: Bearer`; **belum diperiksa oleh `api.py`** (lihat §7) |

Sisi Python: lihat `services/face/src/config.py` (`DET_MODEL_PATH`,
`REC_MODEL_PATH`, `CONF_THRESHOLD`, `SIM_THRESHOLD`, `API_PORT`, dst.).

## 6. Menjalankan bersama (pengembangan)

```powershell
# Terminal 1 — face service (dari services/face)
conda activate face_rec
uvicorn api:app --host 127.0.0.1 --port 8000

# Terminal 2 — backend Go (dari root proyek)
air        # atau: go run ./cmd/server
```

Cek integrasi:

```bash
curl http://localhost:8080/api/v1/health
# {"faceVerification":{"online":true,"provider":"Python YOLO-FaceV2 + ArcFace",
#   "detectionModel":"face_detection/yolo-facev2s.onnx",
#   "recognitionModel":"face_recognition/arcface_r50_fp16.onnx"},"status":"ok"}
```

Lalu lakukan enrollment dari halaman **Mahasiswa**, dan uji scan dengan
klien Python di `docs/devices.md` §4 (tanpa perangkat keras).

Pada request pertama setelah start, model sudah dimuat oleh `lifespan`
FastAPI. Bila model belum ada di `services/face/models/`, layanan akan
mengunduhnya dari Hugging Face saat start (butuh internet).

## 7. Keamanan dan hal yang perlu diperhatikan

- **Face service tidak memiliki autentikasi** dan memakai CORS `*`. Jalankan
  dengan `--host 127.0.0.1` atau di jaringan internal saja; pada
  `docker-compose.yml`, ganti `"8000:8000"` menjadi `"127.0.0.1:8000:8000"`
  bila Go berjalan di mesin yang sama.
- Mode `APP_ENV=production` tetap mewajibkan `FACE_SERVICE_TOKEN` ≥ 32
  karakter di Go. Token itu sudah dikirim, tetapi baru berguna setelah
  `api.py` memeriksanya (mis. dependency FastAPI yang membandingkan header
  `Authorization`).
- **Ambang `0.45` belum dikalibrasi** dengan data kampus. Ukur FAR/FRR
  dengan beberapa foto per mahasiswa sebelum dipakai sungguhan.
- Pencocokan memuat seluruh template tiap scan (O(N)); cukup untuk ratusan
  hingga beberapa ribu mahasiswa. Untuk skala lebih besar, batasi kandidat
  per kelas atau tambahkan cache.
- Mengganti model recognition (`/models/switch` atau `REC_MODEL_PATH`)
  membuat embedding lama tidak sebanding → lakukan enrollment ulang. Template
  berdimensi berbeda otomatis diabaikan, tetapi model berbeda dengan dimensi
  sama **tidak** terdeteksi.
- Liveness/anti-spoof belum ada di Python maupun Go; `liveness_passed` masih
  klaim perangkat.
- Python tidak menyimpan foto. Go menyimpan foto di Garage hanya bila
  `S3_*` diisi (lihat §8). Embedding maupun foto adalah data biometrik
  sensitif.

## 8. Penyimpanan foto (S3 Garage)

Pencocokan hanya memakai embedding, jadi foto tidak dibutuhkan untuk
absensi. Foto tetap disimpan untuk tiga keperluan:

1. **Enrollment ulang otomatis** saat model recognition diganti (embedding
   model lama tidak sebanding dengan model baru).
2. **Peninjauan** scan `Perlu ditinjau` oleh dosen/admin.
3. **Audit dan sanggahan** (mahasiswa protes, dugaan titip absen).

Alur:

| Kejadian | Kunci objek | Dicatat di | Masa simpan |
| --- | --- | --- | --- |
| Enrollment berhasil | `enroll/{student_id}/{waktu}-{acak}.jpg` | `face_templates.image_key` | sampai enrollment ulang (foto lama dihapus) |
| Scan `Hadir` | `scans/{yyyy}/{mm}/{dd}/{waktu}-{device}-{acak}.jpg` | `scan_events.image_key` | `IMAGE_RETENTION_DAYS` (30) |
| Scan `Perlu ditinjau` / `Ditolak` | sama | sama | `IMAGE_REVIEW_RETENTION_DAYS` (90) |
| Request gagal validasi / face service error | — | — | tidak disimpan |

- Unggah diberi batas 3 detik. Bila Garage mati, absensi dan enrollment
  tetap berjalan tanpa foto; kesalahannya dicatat di log.
- Job retensi berjalan saat start lalu tiap jam: foto kedaluwarsa dihapus
  dari bucket dan `image_key` di-`$unset`; event absensinya tetap ada.
- Foto dilihat lewat `GET /api/v1/images/{key}` (sesi staf; dosen hanya
  kelasnya). Bucket tidak perlu dan tidak boleh dibuka publik.
- Klien S3 ada di `internal/storage/s3.go` (pustaka standar, AWS Signature
  V4, gaya path); logika foto di `internal/api/images.go`.
- `GET /api/v1/health` menampilkan `imageStorage.enabled/online`; log start
  menulis `image storage: bucket "…" siap` atau peringatan.

Menyiapkan Garage (di server):

```bash
garage bucket create methics-faces
garage key create methics-app            # catat Key ID dan Secret key
garage bucket allow --read --write methics-faces --key methics-app
```

Lalu isi `S3_ENDPOINT=http://10.100.1.11:3900`, `S3_REGION=garage` (sama
dengan `s3_region` di `garage.toml`), `S3_BUCKET=methics-faces`,
`S3_ACCESS_KEY_ID`, dan `S3_SECRET_ACCESS_KEY` di `.env`. Jangan memakai
`admin_token` atau akun root server untuk aplikasi.

Belum ada: penghapusan foto saat data mahasiswa dihapus (aplikasi belum punya
fitur hapus mahasiswa) dan enrollment ulang otomatis dari foto tersimpan.
