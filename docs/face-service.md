# Face Service (Python) dan Integrasinya dengan Go

> Status: **[TERSEDIA]**. Layanan ada di `services/face/` (FastAPI + ONNX
> Runtime: YOLO-FaceV2 untuk deteksi, ArcFace R50 untuk embedding 512-d,
> model diunduh dari Hugging Face `AkihikoSaburo/methics`). Cara menjalankan
> layanannya sendiri ada di `services/face/README.md`.

## 1. Pembagian tugas: satu API publik

```text
Browser / ESP32 ──► Go (satu-satunya API publik, :8080)
                     │  POST /register?student_id=…  (1–6 file "images")   — enrollment
                     │  POST /embed                   (1 file "image")     — scan
                     ├──────────────────────────────► Python face service (:8000, internal)
                     │◄──────────── embedding ArcFace 512-d (+ confidence) per foto
                     │
                     ├── simpan semua embedding pose → MongoDB face_templates   (enrollment)
                     └── cosine similarity 1:N       → bandingkan dengan face_templates (scan)
```

Face service bersifat **stateless**: ia tidak menyimpan apa pun dan tidak
terhubung ke MongoDB (`student_id` pada `/register` hanya dikembalikan
lagi). Go yang:

- menyimpan template wajah (1–6 embedding per mahasiswa) di koleksi `face_templates`,
- melakukan pencocokan 1:N (cosine similarity) saat scan,
- menentukan status `Hadir` / `Perlu ditinjau` / `Ditolak`.

Akibatnya perangkat ESP32 dan dashboard cukup berbicara dengan **satu** API
(Go). Face service tidak perlu dan tidak boleh diekspos ke jaringan publik.

Kode Go: `internal/api/face.go` (klien `/embed` & `/register`, validasi
enrollment, pencocokan), dipanggil dari `EnrollFace` dan `receiveScan` di
`internal/api/server.go`. Foto wajah opsional disimpan di Garage (S3), lihat §8.

## 2. Endpoint Python yang dipakai Go

| Endpoint | Dipakai untuk | Request dari Go | Respons yang dibaca Go |
| --- | --- | --- | --- |
| `GET /health` | status di dashboard & `/api/v1/health` | — | `model_loaded`, `det_model`, `rec_model` |
| `POST /embed` | scan perangkat | multipart, 1 file `image` | `embedding` (512-d), `confidence`, `processing_time_ms` |
| `POST /register?student_id=<id>` | enrollment | multipart, 1–6 file `images` bernama `foto-1.jpg` … `foto-6.jpg` | `num_templates`, `templates[].embedding`, `templates[].confidence`, `processing_time_ms` |

Keduanya memilih **wajah dengan confidence deteksi tertinggi** pada tiap
foto; foto berisi beberapa orang tidak ditolak oleh Python, jadi pastikan
foto enrollment hanya berisi mahasiswa yang bersangkutan. `return_aligned`
tidak dipakai (crop wajah tidak dikirim balik). Endpoint `/models*` tidak
dipakai Go dan tetap bisa dipakai tim lewat Swagger
(`http://127.0.0.1:8000/docs`).

Status HTTP dari Python:

| Status Python | Perlakuan Go |
| --- | --- |
| `200` | diproses |
| `400` (gambar kosong/tidak bisa di-decode, atau lebih dari 6 foto) | enrollment `400 invalid_image`; scan `400 invalid_image` |
| `422` dengan `detail` "No face detected …" | enrollment `422 no_face` (nomor foto dibaca dari `image #N`); scan dicatat `Ditolak` "Wajah tidak terdeteksi" |
| `422` validasi FastAPI, `503` model belum siap, lainnya, timeout, tidak terjangkau | `502 face_service_error`; scan **tidak** dicatat |

## 3. Enrollment (administrator)

`POST /partials/mahasiswa/{nim}/face-template` (tombol **Enroll wajah** /
**Enroll ulang** di halaman Mahasiswa, pilih 1–6 file sekaligus) atau
`POST /api/v1/students/{nim}/face-template` (JSON, field `images`):

1. Go membaca 1–6 foto (maks 8 MB per foto; field `images`, atau `image`
   untuk klien lama) lalu mengirim semuanya ke `POST /register`.
   Pose yang disarankan: depan, kiri (~30°), kanan (~30°), atas, bawah,
   senyum. Satu foto depan tetap boleh.
2. Foto tanpa wajah → `422 no_face` dengan nomor fotonya.
3. **Konsistensi:** setiap foto harus orang yang sama: kemiripannya dengan
   foto pertama **atau** dengan rata-rata foto lain harus ≥
   `FACE_POSE_THRESHOLD` (default 0.30, lebih longgar dari ambang scan karena
   pose miring wajar kurang mirip foto depan). Bila tidak →
   `422 inconsistent_faces` (mis. foto ke-4 orang lain).
4. **Duplikat:** setiap embedding dibandingkan dengan template mahasiswa
   **lain**. Bila kemiripan ≥ ambang, enrollment ditolak
   (`409 face_already_enrolled`, menyebut NIM pemilik wajah) untuk mencegah
   satu wajah terdaftar di dua NIM (titip absen). Template milik mahasiswa itu
   sendiri diabaikan sehingga enrollment ulang tidak bentrok.
5. Semua embedding di-*upsert* ke **satu** dokumen `face_templates` milik
   mahasiswa itu (field `encodings`); enrollment ulang mengganti seluruh pose
   lama.

Dokumen `face_templates`:

```json
{
  "student_id": "student-241011001",
  "encodings": [[0.0123, -0.0456, "… 512 angka …"], "… 1–6 vektor …"],
  "template_count": 6,
  "dimension": 512,
  "detection_confidences": [0.93, 0.91, 0.9, 0.88, 0.9, 0.92],
  "image_key": "enroll/student-241011001/20261028T080000Z-3f9a1c2b4d5e.jpg",
  "image_keys": ["enroll/student-241011001/…", "… satu per foto …"],
  "created_at": "2026-10-28T08:00:00Z",
  "updated_at": "2026-10-28T08:00:00Z"
}
```

Dokumen dari versi lama (satu foto, field `encoding` dan
`detection_confidence`) tetap dibaca dan dicocokkan sebagai satu pose;
enrollment ulang mengubahnya ke format baru.

## 4. Verifikasi saat scan (perangkat)

Perangkat menyalakan kamera saat sensor ultrasonik mendeteksi orang, lalu
mengirim 1–6 frame dari jendela scan ±2 detik dalam satu
`POST /api/v1/scans` (setelah token, HMAC, timestamp, dan perangkat lolos
validasi; kode di `internal/api/scan.go`):

1. `matchFrames` mengirim frame **satu per satu** ke `POST /embed`. Frame
   tanpa wajah (`422`) atau rusak (`400`) dilewati; face service mati /
   timeout → `502`, scan tidak dicatat.
2. Embedding tiap frame dibandingkan dengan **semua pose semua** mahasiswa
   (1:N). Skor seorang mahasiswa = skor pose terbaiknya. Frame dengan skor ≥
   `FACE_SIM_THRESHOLD` dihitung sebagai satu "suara" untuk mahasiswa itu.
   Begitu satu mahasiswa mendapat `SCAN_MIN_MATCH_FRAMES` (2) suara, sisa
   frame tidak diproses (lebih cepat untuk wajah terdaftar).
3. `decideFrames` memilih mahasiswa dengan suara terbanyak (seri → skor
   tertinggi):
   - ≥ `SCAN_MIN_MATCH_FRAMES` suara (atau `liveness_passed=true`) →
     **Hadir** di kelas mahasiswa itu (`class_id` kosong) atau di kelas
     perangkat bila mahasiswa anggotanya;
   - 1 suara saja → **Perlu ditinjau**;
   - ada wajah tetapi tidak ada suara → **Ditolak**, "Wajah tidak
     Terdaftar" (`result: tidak_terdaftar`);
   - tidak ada wajah di semua frame → **Ditolak**, "Wajah tidak terdeteksi".
4. `confidence` pada event = skor terbaik mahasiswa yang menang (atau skor
   tertinggi keseluruhan untuk wajah tidak terdaftar).

Beberapa pose meningkatkan peluang cocok saat mahasiswa tidak tepat
menghadap kamera, tetapi juga sedikit menaikkan peluang salah cocok; ukur
ulang ambang setelah data nyata terkumpul (§7). Konsensus beberapa frame
hanya memastikan wajah yang sama terlihat konsisten selama ±2 detik —
**bukan** anti-spoof.

## 5. Konfigurasi

Sisi Go (`.env`):

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `FACE_SERVICE_URL` | `http://127.0.0.1:8000` | base URL face service (port default `main.py`/`api.py`) |
| `FACE_SIM_THRESHOLD` | `0.45` | ambang cosine similarity untuk scan dan cek duplikat; samakan dengan `SIM_THRESHOLD` di Python |
| `FACE_POSE_THRESHOLD` | `0.30` | ambang kemiripan antar-foto dalam satu enrollment (cek konsistensi pose) |
| `SCAN_MIN_MATCH_FRAMES` | `2` | jumlah frame scan yang harus cocok dengan mahasiswa yang sama agar langsung **Hadir** tanpa liveness dari perangkat |
| `FACE_SERVICE_TIMEOUT_SECONDS` | `15` | batas waktu satu panggilan `/embed`; `/register` mendapat nilai ini × jumlah foto (6 foto → 90 detik). Health memakai 3 detik |

Request enrollment juga mendapat batas baca/tulis sendiri **3 menit**
(`api.EnrollRequestTimeout`), menggantikan `ReadTimeout`/`WriteTimeout`
server 15 detik, dan foto ke Garage diunggah paralel. Sebelumnya enrollment
lebih dari ±2 foto HP bisa terputus di tengah jalan karena total waktunya
melewati 15 detik.
| `FACE_SERVICE_TOKEN` | kosong | dikirim sebagai `Authorization: Bearer`; **belum diperiksa oleh `api.py`** (lihat §7) |

Sisi Python: lihat `services/face/src/config.py` (`DET_MODEL_PATH`,
`REC_MODEL_PATH`, `CONF_THRESHOLD`, `SIM_THRESHOLD`, dst.). Host dan port
diatur lewat argumen `main.py` (`--host`, `--port`).

## 6. Menjalankan bersama (pengembangan)

Gunakan Python **3.10–3.12**: conda `face_rec` dari `environment.yml`
(Python 3.10) atau venv. Python 3.15 belum didukung `numpy<2.3`/`onnxruntime`,
sehingga `pip install` gagal.

```powershell
# Terminal 1 — face service (dari services/face)
conda activate face_rec
python main.py --host 127.0.0.1 --port 8000
# atau: uvicorn api:app --host 127.0.0.1 --port 8000

# Terminal 2 — backend Go (dari root proyek)
air        # atau: go run ./cmd/server
```

> `python main.py` tanpa argumen mendengarkan di `0.0.0.0` (semua
> interface). Untuk pengembangan di satu PC selalu tambahkan
> `--host 127.0.0.1` karena face service belum punya autentikasi.

Cek integrasi:

```bash
curl http://localhost:8080/api/v1/health
# {"faceVerification":{"online":true,"provider":"Python YOLO-FaceV2 + ArcFace",
#   "detectionModel":"face_detection/yolo-facev2s.onnx",
#   "recognitionModel":"face_recognition/arcface_r50_fp16.onnx"},"status":"ok"}
```

Lalu lakukan enrollment dari halaman **Mahasiswa** (pilih 1–6 foto
sekaligus), dan uji scan dengan klien Python di `docs/devices.md` §6 (tanpa
perangkat keras), atau perangkat ESP32 (`docs/devices.md` §2.5).

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
- Endpoint `/embed` dan `/register` di `api.py` menjalankan inferensi di
  dalam `async def`, sehingga request diproses **satu per satu** per worker.
  Enrollment 6 foto sesaat menahan scan lain; untuk banyak perangkat jalankan
  beberapa worker (`--workers`) atau pindahkan inferensi ke thread pool.
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
| Enrollment berhasil | `enroll/{student_id}/{waktu}-{acak}.jpg`, satu per foto | `face_templates.image_keys` (semua) dan `image_key` (foto pertama) | sampai enrollment ulang (foto lama dihapus) |
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

Menghapus mahasiswa (halaman Mahasiswa atau `DELETE /api/v1/students/{nim}`)
ikut menghapus foto enrollment dan foto scannya dari Garage.

Foto enrollment dibuka lewat tombol **Lihat foto** (foto pertama) dan
nomor **2–6** di sebelahnya (pose lain) di halaman Mahasiswa. Tombolnya
muncul hanya bila foto tersimpan di Garage; enrollment yang dibuat sebelum
`S3_*` diisi tidak punya foto. Foto scan dibuka dari kolom **Foto**
di halaman Log. Belum ada: enrollment ulang otomatis dari foto tersimpan.

## 9. Apa yang disimpan dari hasil Python

| Saat | Embedding dari Python | Yang disimpan |
| --- | --- | --- |
| Enrollment (`/register`) | disimpan Go | `face_templates.encodings` (1–6 × 512 angka) + `template_count`, `dimension`, `detection_confidences`, `image_key(s)`, waktu |
| Scan perangkat (`/embed`, 1–6 frame) | **tidak** disimpan; hanya dipakai untuk pencocokan lalu dibuang | `scan_events`: `student_id` kandidat, `confidence` (cosine similarity), `status`, `review_reason`, `frames_total/with_face/matched`, `image_key` (satu frame bukti) |

Python sendiri tidak menyimpan apa pun; folder `services/face/models/` hanya
berisi file model.
