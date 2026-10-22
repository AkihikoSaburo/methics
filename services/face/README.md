# Face Detection (YOLO-FaceV2) & Face Recognition (ArcFace) Pipeline

Pipeline pengenalan wajah lengkap berbasis Python dan ONNX Runtime yang terintegrasi dengan **Hugging Face Hub** ([AkihikoSaburo/methics](https://huggingface.co/AkihikoSaburo/methics)):
1. **Face Detection**: `yolo-facev2s.onnx` (cepat) & `yolo-facev2m.onnx` (akurasi tinggi).
2. **Face Alignment**: Melakukan transformasi affine / similarity alignment ke ukuran standar `(112, 112)` atau dimensi kustom sebelum ekstraksi fitur.
3. **Face Recognition**: `arcface_r50_fp16.onnx` (ResNet-50 ArcFace) untuk menghasilkan feature embedding 512 dimensi ter-normalisasi L2. Mendukung berbagai model turunan InsightFace dan model kustom.
4. **Hugging Face Model Manager**: Mengunduh model secara on-demand, caching lokal otomatis, dan listing model langsung dari repo Hugging Face secara real-time.

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
│   └── utils.py                # Letterbox, NMS, sigmoid, drawing utils
├── main.py                     # CLI entrypoint runner
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

## 4. Cara Menjalankan CLI

Pastikan environment conda telah aktif (`conda activate face_rec`).

### A. Membandingkan 2 Gambar Wajah (Face Verification / Comparison)
```bash
# Menggunakan model deteksi default (yolo-facev2s)
python main.py --compare foto1.jpg foto2.jpg

# Menggunakan model deteksi medium (yolo-facev2m) dan simpan hasil alignment
python main.py --compare foto1.jpg foto2.jpg --model-type m --save-aligned
```

### B. Deteksi Wajah pada 1 Gambar & Ekstraksi Embedding
```bash
python main.py --detect foto.jpg --save-viz --save-aligned --model-type s
```
Hasil visualisasi bounding box dan wajah ter-align akan disimpan di folder `output/`.

### C. Real-Time Melalui Webcam
```bash
python main.py --webcam --model-type s
```
*(Tekan `q` untuk keluar)*

---

## 5. REST API (FastAPI)

Menjalankan server API:
```bash
uvicorn api:app --host 0.0.0.0 --port 8000 --reload
```

Dokumentasi interaktif Swagger UI tersedia di: `http://localhost:8000/docs`.

### Endpoint Utama:
- `GET  /models` — Melihat katalog model dari Hugging Face Hub dan status download di lokal.
- `POST /models/download` — Mengunduh model tertentu dari Hugging Face ke disk server.
- `POST /models/switch` — Mengganti model deteksi atau recognizer yang sedang aktif di RAM secara instan.
- `POST /detect` — Deteksi wajah & ekstrak embedding 512-d.
- `POST /compare` — Bandingkan kemiripan 2 gambar wajah (Cosine Similarity).
- `POST /embed` — Ekstraksi embedding 512-d wajah utama.
- `POST /detect/visualize` — Deteksi wajah dan kembalikan gambar hasil bounding box.
- `GET  /health` — Status server dan model yang sedang aktif.

---

## 6. Menambahkan Model Baru di Masa Depan

1. **Upload model `.onnx`** baru ke repository Hugging Face kamu (`AkihikoSaburo/methics`) pada folder `face_detection/` atau `face_recognition/`.
2. *(Opsional)* Daftarkan nama, ukuran input, dan rekomendasinya di [models_config.json](file:///run/media/akihikosaburo/nvme/Kuliah/Semester_5/PTC/FaceRecognition/models_config.json).
3. Jalankan `python main.py --list-models`. Model baru akan langsung terdeteksi dan siap dipakai secara instan!
