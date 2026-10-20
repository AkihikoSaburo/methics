# Face Service (Python)

> Status: **[BELUM ADA DI REPO]**. Folder `services/face/` saat ini hanya
> berisi `README..txt` (placeholder). Berkas `services/face/main.py` dan
> `services/face/requirements.txt` yang dirujuk dokumen lama **tidak ada**.
> Dokumen ini menjadi **kontrak** yang harus dipenuhi implementasi Python agar
> cocok dengan backend Go (`internal/api/server.go`).

## 1. Peran

Backend Go tidak melakukan inferensi wajah (ADR-2 di `architecture.md`). Go
hanya:

- meneruskan foto enrollment ke `POST /enroll`,
- meneruskan capture perangkat ke `POST /verify`,
- membaca `GET /health` untuk status dashboard,
- menghitung dokumen `face_templates` untuk status "wajah terdaftar".

Face service bertanggung jawab mendeteksi wajah, membuat embedding, menyimpan
template ke MongoDB, dan mencocokkan capture.

## 2. Konfigurasi sisi Go

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `FACE_SERVICE_URL` | `http://127.0.0.1:8001` | base URL; garis miring di akhir dibuang |
| `FACE_SERVICE_TOKEN` | kosong | bila diisi, Go mengirim `Authorization: Bearer <token>` ke semua endpoint. **Wajib ≥ 32 karakter** saat `APP_ENV=production` |

Timeout HTTP klien Go: **8 detik** per panggilan (termasuk `/health`, yang
dipanggil setiap kali dashboard dimuat — jadi service yang lambat
memperlambat dashboard).

## 3. Kontrak endpoint

Semua request dari Go memakai bearer token di atas (bila dikonfigurasi).
Service sebaiknya menolak request tanpa token yang benar dengan `401`.

### `GET /health`

Respons yang diharapkan Go (`200`):

```json
{ "modelReady": true }
```

Go menganggap service **online** hanya bila status `200` dan `modelReady`
bernilai `true`. Status selain 200, JSON tak valid, atau timeout → offline.

### `POST /enroll`

Request (`multipart/form-data`):

| Field | Isi |
| --- | --- |
| `student_id` | `_id` mahasiswa, mis. `student-ik24001` (bukan NIM) |
| `image` | foto enrollment (≤ 8 MiB, sudah dicek Go) |

Yang diharapkan dilakukan service:

1. Tolak bila wajah tidak terdeteksi atau lebih dari satu wajah (respons non-2xx).
2. Buat embedding 128 dimensi.
3. Upsert ke koleksi `face_templates` dengan `student_id` (index unik — satu
   template per mahasiswa), `encoding`, `created_at`.
4. **Jangan simpan gambar mentah.**

Respons: status 2xx dengan body JSON objek apa pun. Go meneruskannya apa
adanya sebagai `result` pada `POST /api/v1/students/{nim}/face-template`.
Status non-2xx → Go membalas `502 face_service_error` (JSON) atau pesan
"Enrollment gagal; pastikan service Python dan model wajah aktif." (UI).

### `POST /verify`

Request (`multipart/form-data`):

| Field | Isi |
| --- | --- |
| `class_id` | kelas yang dikirim perangkat (huruf besar) |
| `image` | capture perangkat |

Respons `200` yang di-decode Go:

```json
{
  "verified": true,
  "studentId": "student-ik24001",
  "confidence": 0.91,
  "reason": ""
}
```

| Field | Tipe | Arti |
| --- | --- | --- |
| `verified` | bool | jarak terbaik ≤ ambang |
| `studentId` | string | `_id` mahasiswa kandidat (`""` bila tidak ada) |
| `confidence` | number | disarankan `1 - distance` |
| `reason` | string | alasan penolakan (dipakai sebagai `review_reason` bila `Ditolak`) |

Go menganggap wajah cocok hanya bila `verified == true` **dan** `studentId`
tidak kosong, lalu menentukan status sendiri (anggota kelas + liveness). Field
`class_id` boleh dipakai service untuk membatasi kandidat ke anggota kelas,
tetapi Go tetap memeriksa keanggotaan kelas secara independen.

Status non-2xx atau timeout → Go membalas `502 face_service_error` ke
perangkat dan **tidak** mencatat event.

## 4. Konfigurasi sisi Python (yang disarankan)

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `FACE_SERVICE_TOKEN` | — | sama dengan Go |
| `FACE_DISTANCE_THRESHOLD` | `0.50` | ambang jarak Euclid `face_recognition` |
| `MONGODB_URI` | `mongodb://10.100.1.11:27017` | sama dengan Go |
| `MONGODB_DATABASE` | `attendance` | sama dengan Go |

Catatan: `FACE_DISTANCE_THRESHOLD` dibaca oleh service Python, **bukan** oleh
Go — Go tidak membaca variabel ini.

## 5. Kerangka implementasi

Kerangka minimal (FastAPI + `face_recognition` + `pymongo`) yang memenuhi
kontrak di atas. Ini titik awal, belum diuji di mesin pengembang:

```python
# services/face/main.py
import os
from datetime import datetime, timezone

import face_recognition
import numpy as np
from fastapi import Depends, FastAPI, File, Form, Header, HTTPException, UploadFile
from pymongo import MongoClient

TOKEN = os.environ.get("FACE_SERVICE_TOKEN", "")
THRESHOLD = float(os.environ.get("FACE_DISTANCE_THRESHOLD", "0.50"))
db = MongoClient(os.environ.get("MONGODB_URI", "mongodb://10.100.1.11:27017"))[
    os.environ.get("MONGODB_DATABASE", "attendance")
]
app = FastAPI()


def require_token(authorization: str = Header(default="")):
    if TOKEN and authorization != f"Bearer {TOKEN}":
        raise HTTPException(status_code=401, detail="invalid token")


def encode(upload: UploadFile) -> list[np.ndarray]:
    image = face_recognition.load_image_file(upload.file)
    return face_recognition.face_encodings(image)


@app.get("/health", dependencies=[Depends(require_token)])
def health():
    db.command("ping")
    return {"modelReady": True}


@app.post("/enroll", dependencies=[Depends(require_token)])
def enroll(student_id: str = Form(...), image: UploadFile = File(...)):
    encodings = encode(image)
    if len(encodings) != 1:
        raise HTTPException(status_code=422, detail="harus tepat satu wajah")
    db.face_templates.update_one(
        {"student_id": student_id},
        {"$set": {"encoding": encodings[0].tolist(), "created_at": datetime.now(timezone.utc)}},
        upsert=True,
    )
    return {"studentId": student_id, "enrolled": True}


@app.post("/verify", dependencies=[Depends(require_token)])
def verify(class_id: str = Form(""), image: UploadFile = File(...)):
    encodings = encode(image)
    if not encodings:
        return {"verified": False, "studentId": "", "confidence": 0, "reason": "Wajah tidak terdeteksi."}
    templates = list(db.face_templates.find({}, {"student_id": 1, "encoding": 1}))
    if not templates:
        return {"verified": False, "studentId": "", "confidence": 0, "reason": "Belum ada template wajah."}
    known = np.array([t["encoding"] for t in templates])
    distances = face_recognition.face_distance(known, encodings[0])
    best = int(np.argmin(distances))
    distance = float(distances[best])
    if distance > THRESHOLD:
        return {"verified": False, "studentId": "", "confidence": 1 - distance, "reason": "Wajah tidak cocok."}
    return {"verified": True, "studentId": templates[best]["student_id"], "confidence": 1 - distance, "reason": ""}
```

`requirements.txt` yang sesuai: `fastapi`, `uvicorn[standard]`,
`python-multipart`, `face-recognition`, `numpy`, `pymongo` (pin versinya).
`face-recognition` membutuhkan `dlib`, CMake, dan toolchain C/C++ (di Windows:
Visual Studio C++ Build Tools, atau gunakan WSL/Linux).

Menjalankan:

```bash
python -m uvicorn services.face.main:app --host 127.0.0.1 --port 8001
```

Bind ke `127.0.0.1` atau jaringan internal saja; **jangan** ekspos port
service ke jaringan publik.

## 6. Hal yang belum diputuskan

- Kalibrasi ambang (`0.50`) terhadap data kampus: ukur FAR/FRR.
- Pembatasan kandidat per kelas memakai `class_id` (saat ini 1:N ke semua
  template).
- Liveness / anti-spoof: belum ada di sisi service.
- Enkripsi template saat disimpan, audit enrollment, consent, dan retensi.
