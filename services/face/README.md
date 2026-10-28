# Face Detection (YOLO-FaceV2) & Face Recognition (ArcFace) Pipeline

Pipeline pengenalan wajah lengkap berbasis Python dan ONNX Runtime yang terintegrasi dengan **Hugging Face Hub** ([AkihikoSaburo/methics](https://huggingface.co/AkihikoSaburo/methics)):
1. **Face Detection**: `yolo-facev2s.onnx` (cepat) & `yolo-facev2m.onnx` (akurasi tinggi).
2. **Face Alignment**: Melakukan transformasi affine / similarity alignment ke ukuran standar `(112, 112)` atau dimensi kustom sebelum ekstraksi fitur.
3. **Face Recognition**: `arcface_r50_fp16.onnx` (ResNet-50 ArcFace) untuk menghasilkan feature embedding 512 dimensi ter-normalisasi L2. Mendukung berbagai model turunan InsightFace dan model kustom.
4. **Hugging Face Model Manager**: Mengunduh model secara on-demand, caching lokal otomatis, dan listing model langsung dari repo Hugging Face secara real-time.
5. **REST API**: Semua fungsi diakses melalui endpoint FastAPI (tanpa webcam/GUI).

---

## 1. Setup Conda Profile & Instalasi Library

Gunakan profil conda `face_rec` yang telah dibuat:

```bash
conda activate face_rec
```

Atau instalasi dependencies secara manual jika dibutuhkan:
```bash
pip install -r requirements.txt
```

---

## 2. Struktur Direktori

```text
FaceRecognition/
├── models_config.json          # Konfigurasi model, default, metadata & threshold
├── models/                     # Cache lokal model ONNX (auto-download dari Hugging Face)
│   ├── face_detection/
│   │   ├── yolo-facev2s.onnx
│   │   └── yolo-facev2m.onnx
│   └── face_recognition/
│       └── arcface_r50_fp16.onnx
├── src/
│   ├── __init__.py             # Re-export API package
│   ├── config.py               # Pengaturan konfigurasi & environment variable
│   ├── model_manager.py        # Hugging Face Hub downloader, caching & model catalog
│   ├── detector.py             # YOLOFaceDetector ONNX inference
│   ├── aligner.py              # FaceAligner canonical ArcFace 112x112
│   ├── recognizer.py           # FaceRecognizer (InsightFace/ArcFace & custom norm)
│   ├── pipeline.py             # FacePipeline orchestrator & singleton
│   └── utils.py                # Letterbox, NMS, sigmoid utils
├── main.py                     # CLI entrypoint — API launcher & model management
├── api.py                      # FastAPI REST API service
├── Dockerfile                  # Multi-stage production container build
├── docker-compose.yml          # Container orchestration config
├── requirements.txt            # Daftar library yang dibutuhkan
└── README.md                   # Dokumentasi penggunaan
```

---

## 3. Manajemen Model dari Hugging Face Hub

Proyek ini terhubung langsung ke repository Hugging Face: **`AkihikoSaburo/methics`**.
File `.onnx` tidak perlu dimasukkan ke Git manual; pipeline akan mengunduhnya secara otomatis saat pertama kali dipanggil.

### Melihat Daftar Model yang Tersedia:
```bash
python main.py --list-models
```
*Perintah ini akan meminta list file secara langsung ke Hugging Face Hub dan menampilkan status apakah file sudah terunduh di lokal beserta ukurannya.*

### Mengunduh Model Tertentu Secara Manual (Opsional):
```bash
python main.py --download-model face_detection/yolo-facev2m.onnx
```

---

## 4. Menjalankan API Server

Semua fungsi (ekstraksi embedding, registrasi mahasiswa, model management) diakses melalui REST API.

### Cara Menjalankan:
```bash
# Default (host 0.0.0.0, port 8000)
python main.py

# Custom host & port
python main.py --host 127.0.0.1 --port 9000

# Development mode (auto-reload)
python main.py --reload

# Atau langsung via uvicorn
uvicorn api:app --host 0.0.0.0 --port 8000 --reload
```

Dokumentasi interaktif Swagger UI tersedia di: `http://localhost:8000/docs`

### Endpoint Utama:
| Endpoint | Method | Deskripsi |
|---|---|---|
| `/health` | GET | Status server dan model yang sedang aktif |
| `/models` | GET | Melihat katalog model dari Hugging Face Hub dan status download di lokal |
| `/models/download` | POST | Mengunduh model tertentu dari Hugging Face ke disk server |
| `/models/switch` | POST | Mengganti model deteksi atau recognizer yang sedang aktif di RAM secara instan |
| `/embed` | POST | Ekstraksi embedding 512-d wajah utama (untuk absensi) |
| `/register` | POST | Pendaftaran wajah mahasiswa (enrollment 1–6 foto pose berbeda) & ekstraksi template embedding |

---

## 5. Contoh Penggunaan API

### Ekstrak Embedding (Absensi):
```bash
curl -X POST http://localhost:8000/embed \
  -F "image=@foto.jpg"
```

### Registrasi Wajah (Enrollment Mahasiswa):
```bash
curl -X POST "http://localhost:8000/register?student_id=12345678" \
  -F "images=@depan.jpg" \
  -F "images=@kiri.jpg" \
  -F "images=@kanan.jpg" \
  -F "images=@atas.jpg" \
  -F "images=@bawah.jpg" \
  -F "images=@senyum.jpg"
```
